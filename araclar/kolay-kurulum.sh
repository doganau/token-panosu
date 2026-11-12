#!/usr/bin/env bash
# Token Panosu — Ücretsiz Tek Komutla Cloudflare Kurulum Sihirbazı
# Kullanım:
#   npm run kurulum
#   veya: bash araclar/kolay-kurulum.sh
set -euo pipefail

cd "$(dirname "$0")/.."

KALIN="\033[1m"
YESIL="\033[32m"
SARI="\033[33m"
MAVI="\033[36m"
KIRMIZI="\033[31m"
SIFIR="\033[0m"

bilgi()  { printf "${MAVI}▸${SIFIR} %s\n" "$*"; }
basari() { printf "${YESIL}✓${SIFIR} %s\n" "$*"; }
uyari()  { printf "${SARI}!${SIFIR} %s\n" "$*"; }
hata()   { printf "${KIRMIZI}✗ %s${SIFIR}\n" "$*" >&2; exit 1; }

printf "\n${KALIN}Token Panosu — Ücretsiz Cloudflare Kurulum Sihirbazı${SIFIR}\n"
printf "Cloudflare Workers + D1 ücretsiz katmanı (0\$/ay, kredi kartı gerekmez) üzerine kurar.\n\n"

# 1. Önkoşul kontrolü
command -v node >/dev/null 2>&1 || hata "Node.js (v18+) bulunamadı. Lütfen https://nodejs.org adresinden kurun."
command -v npm  >/dev/null 2>&1 || hata "npm bulunamadı. Lütfen Node.js ile birlikte kurun."

if [ ! -x "./node_modules/.bin/wrangler" ]; then
  bilgi "Bağımlılıklar (wrangler) kuruluyor..."
  npm install --no-fund --no-audit
fi

wrangler() {
  ./node_modules/.bin/wrangler "$@"
}

# 2. Cloudflare oturumu kontrolü
bilgi "Cloudflare oturumu kontrol ediliyor..."
WHOAMI_CIKTI="$(wrangler whoami 2>/dev/null || true)"
if [ -z "$WHOAMI_CIKTI" ] || ! printf '%s' "$WHOAMI_CIKTI" | grep -qE '[0-9a-f]{32}'; then
  uyari "Cloudflare hesabınıza giriş yapmanız gerekiyor (tarayıcı açılacak, ücretsiz hesap yeterlidir)."
  wrangler login
  WHOAMI_CIKTI="$(wrangler whoami 2>/dev/null || true)"
fi
basari "Cloudflare oturumu hazır."

# Birden fazla Cloudflare hesabı varsa kullanıcıya seçtir
SECILEN_HESAP_ID="${CLOUDFLARE_ACCOUNT_ID:-}"
if [ -z "$SECILEN_HESAP_ID" ]; then
  HESAP_SATIRLARI="$(WHOAMI="$WHOAMI_CIKTI" node -e '
    const raw = process.env.WHOAMI || "";
    const out = [];
    for (const line of raw.split(/\r?\n/)) {
      const m = line.match(/│\s*([^│]+?)\s*│\s*([0-9a-f]{32})\s*│/i);
      if (m) out.push(m[2].toLowerCase() + "|" + m[1].trim());
    }
    console.log(out.join("\n"));
  ')"
  HESAP_SAYISI="$(printf '%s\n' "$HESAP_SATIRLARI" | sed '/^$/d' | wc -l | tr -d ' ')"
  if [ "$HESAP_SAYISI" -gt 1 ] && [ -t 0 ]; then
    bilgi "Oturumunuzda birden fazla Cloudflare hesabı bulundu:"
    SAYAC=1
    while IFS='|' read -r HID HAD; do
      [ -n "$HID" ] || continue
      printf "  %d) %s (%s)\n" "$SAYAC" "$HAD" "$HID"
      SAYAC=$((SAYAC + 1))
    done <<EOF
$HESAP_SATIRLARI
EOF
    printf "Kurulum yapılacak hesap numarası [1]: "
    read -r SECIM_NO || true
    SECIM_NO="${SECIM_NO:-1}"
    SECILEN_HESAP_ID="$(printf '%s\n' "$HESAP_SATIRLARI" | sed -n "${SECIM_NO}p" | cut -d'|' -f1)"
  elif [ "$HESAP_SAYISI" -eq 1 ]; then
    SECILEN_HESAP_ID="$(printf '%s\n' "$HESAP_SATIRLARI" | head -n 1 | cut -d'|' -f1)"
  fi
fi
if [ -n "$SECILEN_HESAP_ID" ]; then
  export CLOUDFLARE_ACCOUNT_ID="$SECILEN_HESAP_ID"
fi

# 3. Proje / Worker adı ve D1 veritabanı hazırlığı
VARSAYILAN_AD="${TOKEN_PANOSU_AD:-token-panosu}"
if [ -t 0 ]; then
  printf "Worker ve D1 veritabanı adı [${VARSAYILAN_AD}]: "
  read -r GIRILEN_AD || true
  PANO_AD="${GIRILEN_AD:-$VARSAYILAN_AD}"
else
  PANO_AD="$VARSAYILAN_AD"
fi

# Küçük harf ve tire temizliği
PANO_AD="$(printf '%s' "$PANO_AD" | tr '[:upper:]' '[:lower:]' | sed -E 's/[^a-z0-9-]+/-/g; s/^-+//; s/-+$//')"
[ -n "$PANO_AD" ] || PANO_AD="token-panosu"

bilgi "Cloudflare D1 veritabanları sorgulanıyor..."
D1_JSON="$(wrangler d1 list --json 2>/dev/null || echo "[]")"

MEVCUT_TOML_ID=""
MEVCUT_TOML_DB=""
if [ -f "wrangler.toml" ]; then
  MEVCUT_TOML_ID="$(sed -nE 's/^[[:space:]]*database_id[[:space:]]*=[[:space:]]*"([^"]+)".*/\1/p' wrangler.toml | head -n 1)"
  MEVCUT_TOML_DB="$(sed -nE 's/^[[:space:]]*database_name[[:space:]]*=[[:space:]]*"([^"]+)".*/\1/p' wrangler.toml | head -n 1)"
fi

BULUNAN_DB_BILGI="$(D1_JSON="$D1_JSON" MEVCUT_ID="$MEVCUT_TOML_ID" HEDEF_AD="$PANO_AD" node -e '
  let list = [];
  try { list = JSON.parse(process.env.D1_JSON || "[]"); } catch {}
  if (!Array.isArray(list)) list = [];
  const mevcutId = process.env.MEVCUT_ID || "";
  const hedefAd = process.env.HEDEF_AD || "token-panosu";
  const byId = mevcutId && mevcutId !== "BURAYA_D1_VERITABANI_KIMLIGI"
    ? list.find((x) => (x.uuid || x.database_id) === mevcutId)
    : null;
  if (byId) {
    console.log("MEVCUT|" + (byId.uuid || byId.database_id) + "|" + (byId.name || hedefAd));
    process.exit(0);
  }
  const byName = list.find((x) => x.name === hedefAd);
  if (byName) {
    console.log("ADLA|" + (byName.uuid || byName.database_id) + "|" + byName.name);
    process.exit(0);
  }
  console.log("YOK||" + hedefAd);
')"

DB_DURUM="${BULUNAN_DB_BILGI%%|*}"
KALAN="${BULUNAN_DB_BILGI#*|}"
DB_ID="${KALAN%%|*}"
DB_AD="${KALAN#*|}"

if [ "$DB_DURUM" = "MEVCUT" ] && [ -n "$DB_ID" ]; then
  basari "Mevcut wrangler.toml içindeki D1 veritabanı hesabınızda doğrulandı: ${DB_AD} (${DB_ID})"
else
  if [ "$DB_DURUM" = "ADLA" ] && [ -n "$DB_ID" ]; then
    basari "Hesabınızdaki mevcut '${DB_AD}' D1 veritabanı kullanılacak (${DB_ID})."
  else
    bilgi "Yeni ücretsiz D1 veritabanı oluşturuluyor: ${PANO_AD}..."
    wrangler d1 create "$PANO_AD" >/dev/null
    D1_JSON_YENI="$(wrangler d1 list --json 2>/dev/null || echo "[]")"
    DB_ID="$(D1_JSON="$D1_JSON_YENI" HEDEF_AD="$PANO_AD" node -e '
      let list = [];
      try { list = JSON.parse(process.env.D1_JSON || "[]"); } catch {}
      const found = Array.isArray(list) ? list.find((x) => x.name === process.env.HEDEF_AD) : null;
      if (found && (found.uuid || found.database_id)) console.log(found.uuid || found.database_id);
    ')"
    [ -n "$DB_ID" ] || hata "D1 veritabanı oluşturuldu ancak kimliği (database_id) okunamadı."
    DB_AD="$PANO_AD"
    basari "D1 veritabanı oluşturuldu: ${DB_AD} (${DB_ID})"
  fi

  [ -f "wrangler.toml.ornek" ] || hata "wrangler.toml.ornek bulunamadı."
  PANO_AD="$PANO_AD" DB_AD="$DB_AD" DB_ID="$DB_ID" HESAP_ID="${CLOUDFLARE_ACCOUNT_ID:-}" node -e '
    const fs = require("fs");
    let icerik = fs.readFileSync("wrangler.toml.ornek", "utf8");
    const hesapSatir = process.env.HESAP_ID ? `\naccount_id = "${process.env.HESAP_ID}"` : "";
    icerik = icerik
      .replace(/^name\s*=\s*"[^"]*"/m, `name = "${process.env.PANO_AD}"${hesapSatir}`)
      .replace(/^database_name\s*=\s*"[^"]*"/m, `database_name = "${process.env.DB_AD}"`)
      .replace(/^database_id\s*=\s*"[^"]*"/m, `database_id = "${process.env.DB_ID}"`);
    fs.writeFileSync("wrangler.toml", icerik);
  '
  basari "wrangler.toml kendi hesabınıza göre güncellendi."
fi

# 4. D1 veritabanı şemasını uygula (CREATE IF NOT EXISTS — mevcut verilere dokunmaz)
bilgi "Veritabanı tabloları (schema_tam.sql) hazırlanıyor..."
wrangler d1 execute "$DB_AD" --remote --file=schema_tam.sql >/dev/null
basari "Veritabanı şeması hazır."

# 5. İstemci ikililerini derle ve imzala (Go kuruluysa)
mkdir -p dist/indir
URETILEN_SIFRE=""
IKILI_HAZIR=0

if command -v go >/dev/null 2>&1; then
  bilgi "Go derleyicisi bulundu — tüm işletim sistemleri için istemci ikilileri hazırlanıyor..."
  ACIK_N_DEGER="devre-disi"
  if command -v python3 >/dev/null 2>&1 && command -v openssl >/dev/null 2>&1; then
    if ! python3 araclar/imzala.py acik-anahtar >/dev/null 2>&1; then
      bilgi "İstemci otomatik güncellemeleri için size özel RSA-3072 imza anahtarı üretiliyor..."
      python3 araclar/imzala.py anahtar-uret --dosya
    fi
    ACIK_N_DEGER="$(python3 araclar/imzala.py acik-anahtar)"
    TOKEN_PANOSU_ACIK_N="$ACIK_N_DEGER" TOKEN_PANOSU_URL="${TOKEN_PANOSU_URL:-}" sh araclar/derle.sh
    python3 araclar/imzala.py imzala
    basari "42 izole platform/sağlayıcı ikilisi derlendi ve RSA-3072 + HMAC mührünüzle imzalandı."
  else
    uyari "python3 veya openssl bulunamadığı için ikililer imzasız modda derleniyor (otomatik ikili güncelleme kapalı)."
    TOKEN_PANOSU_ACIK_N="devre-disi" TOKEN_PANOSU_URL="${TOKEN_PANOSU_URL:-}" sh araclar/derle.sh
    printf '{\n "ikili": null\n}\n' > istemci/manifest.json
    basari "42 izole platform/sağlayıcı ikilisi derlendi."
  fi
  IKILI_HAZIR=1
else
  uyari "Bu bilgisayarda Go kurulu değil; web panosu hemen yayınlanacak ancak /indir/* istemci ikilileri henüz derlenmedi."
  uyari "Bilgisayar eşleştirmeden önce Go (https://go.dev/dl/) kurup 'npm run derle && npm run deploy' çalıştırmanız yeterlidir."
fi

# 6. Önce Worker'ı yayınla (böylece yeni hesapta Worker oluşturulur ve canlı URL kesinleşir)
bilgi "Cloudflare Workers üzerine yayınlanıyor..."
YAYIN_CIKTI="$(wrangler deploy 2>&1 | tee /dev/stderr)"
CANLI_URL="$(printf '%s\n' "$YAYIN_CIKTI" | grep -Eo 'https://[a-zA-Z0-9._-]+\.workers\.dev' | head -n 1 || true)"

# 7. Gizli anahtarlar (VIEW_KEY: pano giriş şifresi, ADMIN_KEY: oturum/API imzalama anahtarı)
bilgi "Güvenlik anahtarları kontrol ediliyor..."
SECRET_JSON="$(wrangler secret list --format=json 2>/dev/null || echo "[]")"
SECRET_DURUM="$(SECRET_JSON="$SECRET_JSON" node -e '
  let list = [];
  try { list = JSON.parse(process.env.SECRET_JSON || "[]"); } catch {}
  const names = new Set(Array.isArray(list) ? list.map((x) => x.name) : []);
  console.log((names.has("VIEW_KEY") ? "1" : "0") + "|" + (names.has("ADMIN_KEY") ? "1" : "0") + "|" + (names.has("MUHUR_ANAHTAR") ? "1" : "0"));
')"
VIEW_VAR="${SECRET_DURUM%%|*}"
KALAN_SEC="${SECRET_DURUM#*|}"
ADMIN_VAR="${KALAN_SEC%%|*}"
MUHUR_VAR="${KALAN_SEC#*|}"

if [ "$ADMIN_VAR" != "1" ]; then
  YENI_ADMIN="$(node -e "console.log(require('crypto').randomBytes(32).toString('hex'))")"
  printf '%s' "$YENI_ADMIN" | wrangler secret put ADMIN_KEY >/dev/null
  basari "Oturum imzalama anahtarı (ADMIN_KEY) otomatik üretildi ve kaydedildi."
else
  basari "ADMIN_KEY zaten tanımlı."
fi

if [ "$MUHUR_VAR" != "1" ] && command -v python3 >/dev/null 2>&1; then
  MUHUR_HEX="$(python3 araclar/imzala.py muhur-anahtar 2>/dev/null || true)"
  if [ -n "$MUHUR_HEX" ]; then
    printf '%s' "$MUHUR_HEX" | wrangler secret put MUHUR_ANAHTAR >/dev/null
    basari "İkili kriptografik mühür anahtarı (MUHUR_ANAHTAR) otomatik kaydedildi."
  fi
fi

SIFRE_AYARLA=1
if [ "$VIEW_VAR" = "1" ]; then
  if [ -t 0 ]; then
    printf "Pano giriş şifresi (VIEW_KEY) zaten tanımlı. Değiştirmek ister misiniz? [e/H]: "
    read -r CEVAP || true
    case "${CEVAP:-}" in
      [eE]|[eE][vV][eE][tT]|[yY]) SIFRE_AYARLA=1 ;;
      *) SIFRE_AYARLA=0 ;;
    esac
  else
    SIFRE_AYARLA=0
  fi
fi

if [ "$SIFRE_AYARLA" = "1" ]; then
  GIRILEN_SIFRE="${TOKEN_PANOSU_SIFRE:-}"
  if [ -z "$GIRILEN_SIFRE" ] && [ -t 0 ]; then
    printf "Pano giriş şifresi belirleyin (boş bırakırsanız güçlü rastgele şifre üretilir): "
    read -rs GIRILEN_SIFRE || true
    printf "\n"
  fi
  if [ -z "$GIRILEN_SIFRE" ]; then
    GIRILEN_SIFRE="$(node -e "console.log(require('crypto').randomBytes(12).toString('base64url'))")"
    URETILEN_SIFRE="$GIRILEN_SIFRE"
  fi
  printf '%s' "$GIRILEN_SIFRE" | wrangler secret put VIEW_KEY >/dev/null
  basari "Pano giriş şifresi (VIEW_KEY) güvenli biçimde kaydedildi."
fi

# Eğer Go ile ikili derlendiyse ve derleme sırasında TOKEN_PANOSU_URL boş idiyse,
# canlı URL bulunduğunda VarsayilanURL'yi de ikililere gömüp hızlıca güncelleyelim:
if [ "$IKILI_HAZIR" = "1" ] && [ -n "$CANLI_URL" ] && [ -z "${TOKEN_PANOSU_URL:-}" ]; then
  MEVCUT_VARSAYILAN="$(sed -nE 's/^[[:space:]]*var[[:space:]]+VarsayilanURL[[:space:]]*=[[:space:]]*"([^"]+)".*/\1/p' go/main.go | head -n 1)"
  if [ "$CANLI_URL" != "$MEVCUT_VARSAYILAN" ]; then
    bilgi "Canlı adres (${CANLI_URL}) istemci ikililerine gömülüyor..."
    if command -v python3 >/dev/null 2>&1 && python3 araclar/imzala.py acik-anahtar >/dev/null 2>&1; then
      ACIK_N_DEGER="$(python3 araclar/imzala.py acik-anahtar)"
      TOKEN_PANOSU_ACIK_N="$ACIK_N_DEGER" TOKEN_PANOSU_URL="$CANLI_URL" sh araclar/derle.sh >/dev/null
      python3 araclar/imzala.py imzala >/dev/null
    else
      TOKEN_PANOSU_ACIK_N="devre-disi" TOKEN_PANOSU_URL="$CANLI_URL" sh araclar/derle.sh >/dev/null
    fi
    wrangler deploy >/dev/null
    basari "İstemci ikilileri canlı sunucu adresinizle güncellendi."
  fi
fi

printf "\n${YESIL}${KALIN}🎉 Kurulum tamamlandı!${SIFIR}\n"
printf "────────────────────────────────────────────────────────────\n"
if [ -n "$CANLI_URL" ]; then
  printf "  Pano Adresi     : ${KALIN}%s/pano${SIFIR}\n" "$CANLI_URL"
  printf "  Ayarlar Merkezi : ${KALIN}%s/pano/ayarlar${SIFIR}\n" "$CANLI_URL"
else
  printf "  Pano Adresi     : Yukarıdaki wrangler çıktısındaki https://...workers.dev/pano\n"
fi
if [ -n "$URETILEN_SIFRE" ]; then
  printf "  Giriş Şifreniz  : ${SARI}${KALIN}%s${SIFIR}  (lütfen güvenli bir yere kaydedin!)\n" "$URETILEN_SIFRE"
fi
printf "────────────────────────────────────────────────────────────\n"
printf "Sonraki adımlar:\n"
printf "  1. Pano adresine girip şifrenizle oturum açın.\n"
printf "  2. 'Ayarlar' sekmesinden pano başlığını, saat dilimini, ekip üyelerini ve ofis karakterlerini dilediğiniz gibi özelleştirin.\n"
printf "  3. 'Bilgisayarlar' sekmesinden tek kullanımlık eşleştirme kodu üretip bilgisayarlarınızda kurulum komutunu çalıştırın.\n\n"
