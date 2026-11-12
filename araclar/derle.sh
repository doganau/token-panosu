#!/bin/sh
# Uygulamayı tüm sistemler ve 7 sağlayıcı paketi için derler (6 hedef x 7 varyant = 42 ikili):
#   dist/indir/token-panosu-<sistem>-<mimari>[-<paket>][.exe] ve .sha256.txt
# İsteğe bağlı ortam değişkenleri (.derle.env dosyasından da okunabilir):
#   TOKEN_PANOSU_URL          -> ikilinin varsayılan sunucu adresi (ör. https://pano.ornek.workers.dev)
#   TOKEN_PANOSU_ESKI_URLLER  -> eski sunucu adreslerinden otomatik geçiş listesi (virgülle ayrılmış)
#   TOKEN_PANOSU_ACIK_N       -> güncelleme doğrulama RSA açık modülü (veya "devre-disi"); boşsa mevcut imza anahtarından okunur
#   TOKEN_PANOSU_CIKTI_DIZIN  -> çıktı klasörü (varsayılan: dist/indir)
#   TOKEN_PANOSU_MUHUR_YOK    -> "1" ise imzala.py çağrılmaz (yalıtılmış test derlemeleri için)
set -eu
cd "$(dirname "$0")/.."
if [ -f .derle.env ]; then
  while IFS='=' read -r anahtar deger; do
    case "$anahtar" in
      ''|'#'*) continue ;;
      TOKEN_PANOSU_URL) [ -n "${TOKEN_PANOSU_URL:-}" ] || TOKEN_PANOSU_URL="$deger" ;;
      TOKEN_PANOSU_ESKI_URLLER) [ -n "${TOKEN_PANOSU_ESKI_URLLER:-}" ] || TOKEN_PANOSU_ESKI_URLLER="$deger" ;;
      TOKEN_PANOSU_ACIK_N) [ -n "${TOKEN_PANOSU_ACIK_N:-}" ] || TOKEN_PANOSU_ACIK_N="$deger" ;;
    esac
  done < .derle.env
fi
surum="$(sed -nE 's/^[[:space:]]*Surum[[:space:]]*=[[:space:]]*([0-9]+).*/\1/p' go/main.go)"
[ -n "$surum" ] || { echo "go/main.go içinde Surum bulunamadı" >&2; exit 1; }

acik_n="${TOKEN_PANOSU_ACIK_N:-}"
if [ -z "$acik_n" ] && [ "${TOKEN_PANOSU_MUHUR_YOK:-}" != "1" ] && command -v python3 >/dev/null 2>&1; then
  acik_n="$(python3 araclar/imzala.py acik-anahtar 2>/dev/null || true)"
fi

sha256_hesapla() {
  if command -v shasum >/dev/null 2>&1; then
    shasum -a 256 "$1" | cut -d' ' -f1
  else
    sha256sum "$1" | cut -d' ' -f1
  fi
}

cikti_dizin="${TOKEN_PANOSU_CIKTI_DIZIN:-dist/indir}"
mkdir -p "$cikti_dizin"
cikti_tam="$(cd "$cikti_dizin" && pwd)"

paket_etiketleri() {
  case "$1" in
    cgm) echo "" ;;
    c)   echo "sag_claude" ;;
    g)   echo "sag_chatgpt" ;;
    m)   echo "sag_gemini" ;;
    cg)  echo "sag_claude,sag_chatgpt" ;;
    cm)  echo "sag_claude,sag_gemini" ;;
    gm)  echo "sag_chatgpt,sag_gemini" ;;
  esac
}

tek_derle() {
  sistem="$1"
  mimari="$2"
  paket="$3"
  etiketler="$(paket_etiketleri "$paket")"
  if [ "$paket" = "cgm" ]; then
    hedef_adi="$sistem-$mimari"
  else
    hedef_adi="$sistem-$mimari-$paket"
  fi
  ad="token-panosu-$hedef_adi"
  bayrak="-s -w -buildid="
  if [ -n "${TOKEN_PANOSU_URL:-}" ]; then
    bayrak="$bayrak -X main.VarsayilanURL=$TOKEN_PANOSU_URL"
  fi
  if [ -n "${TOKEN_PANOSU_ESKI_URLLER:-}" ]; then
    bayrak="$bayrak -X main.EskiSunucuURLler=$TOKEN_PANOSU_ESKI_URLLER"
  fi
  if [ -n "$acik_n" ]; then
    bayrak="$bayrak -X main.acikNHex=$acik_n"
  fi
  if [ "${TOKEN_PANOSU_MUHUR_YOK:-}" != "1" ] && command -v python3 >/dev/null 2>&1; then
    muhur_ek="$(python3 araclar/imzala.py muhur-bayrak "$surum" "$hedef_adi" 2>/dev/null || true)"
    if [ -n "$muhur_ek" ]; then
      bayrak="$bayrak $muhur_ek"
    fi
  fi
  if [ "$sistem" = windows ]; then
    ad="$ad.exe"
    bayrak="$bayrak -H=windowsgui"
  fi
  if [ -n "$etiketler" ]; then
    (cd go && CGO_ENABLED=0 GOOS="$sistem" GOARCH="$mimari" go build -trimpath -tags "$etiketler" -ldflags "$bayrak" -o "$cikti_tam/$ad" .)
  else
    (cd go && CGO_ENABLED=0 GOOS="$sistem" GOARCH="$mimari" go build -trimpath -ldflags "$bayrak" -o "$cikti_tam/$ad" .)
  fi
  sha256_hesapla "$cikti_tam/$ad" > "$cikti_tam/$ad.sha256.txt"
}

for hedef in darwin/arm64 darwin/amd64 linux/amd64 linux/arm64 windows/amd64 windows/arm64; do
  sistem="${hedef%/*}"
  mimari="${hedef#*/}"
  pidler=""
  for paket in cgm c g m cg cm gm; do
    tek_derle "$sistem" "$mimari" "$paket" &
    pidler="$pidler $!"
  done
  for pid in $pidler; do
    wait "$pid"
  done
  for paket in cgm c g m cg cm gm; do
    if [ "$paket" = "cgm" ]; then
      ad="token-panosu-$sistem-$mimari"
    else
      ad="token-panosu-$sistem-$mimari-$paket"
    fi
    [ "$sistem" = windows ] && ad="$ad.exe"
    printf '%-38s %s KB\n' "$ad" "$(( $(wc -c < "$cikti_tam/$ad") / 1024 ))"
  done
done
echo "Sürüm $surum derlendi (42 ikili)."
