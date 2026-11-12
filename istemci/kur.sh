#!/bin/sh
# Token Panosu kurulumu ve onarımı (macOS ve Linux)
#   Yeni kurulum: curl -fsSL https://pano.ornek.com/kur.sh | sh -s -- --kod XXXXX-XXXXX
#   Onarım/güncelleme (kod gerekmez): curl -fsSL https://pano.ornek.com/kur.sh | sh
# Bu bilgisayara uygun tek dosyalık programı indirir, özetini doğrular, ~/.token-panosu içine koyar
# ve eşleştirmeyi yapar. Python ya da başka bir şey kurmaz.
set -eu

ADRES="${TOKEN_PANOSU_ADRES:-https://pano.ornek.com}"

kod_var=0
kod_deger=""
onceki=""
for arg in "$@"; do
  if [ "$onceki" = "--kod" ]; then
    kod_deger="$arg"
    onceki=""
    continue
  fi
  if [ "$onceki" = "--url" ]; then
    ADRES="$arg"
    onceki=""
    continue
  fi
  case "$arg" in
    --kod)
      kod_var=1
      onceki="--kod"
      ;;
    --kod=*)
      kod_var=1
      kod_deger="${arg#--kod=}"
      ;;
    --url)
      onceki="--url"
      ;;
    --url=*)
      ADRES="${arg#--url=}"
      ;;
  esac
done
ADRES="${ADRES%/}"

case "$(uname -s)" in
  Darwin) sistem=darwin ;;
  Linux) sistem=linux ;;
  *) echo "Bu sistem desteklenmiyor: $(uname -s)" >&2; exit 1 ;;
esac
case "$(uname -m)" in
  arm64 | aarch64) mimari=arm64 ;;
  x86_64 | amd64) mimari=amd64 ;;
  *) echo "Bu işlemci desteklenmiyor: $(uname -m)" >&2; exit 1 ;;
esac

dizin="$HOME/.token-panosu"
mkdir -p "$dizin"
chmod 700 "$dizin"

paket=""
if [ -n "$kod_deger" ]; then
  paket_ham="$(curl -fsSL --max-time 5 "$ADRES/api/kurulum-paket?kod=$kod_deger" 2>/dev/null || true)"
  if [ -n "$paket_ham" ]; then
    paket="$(printf '%s' "$paket_ham" | tr '\r\n' '  ' | sed -n 's/.*"paket"[[:space:]]*:[[:space:]]*"\([cgm]*\)".*/\1/p' | head -n 1)"
  fi
elif [ -f "$dizin/ayar.json" ]; then
  paket="$(tr '\r\n' '  ' < "$dizin/ayar.json" 2>/dev/null | sed -n 's/.*"hedef_paket"[[:space:]]*:[[:space:]]*"\([cgm]*\)".*/\1/p' | head -n 1 || true)"
fi

temel_ad="token-panosu-$sistem-$mimari"
case "$paket" in
  c|g|m|cg|cm|gm) ad="$temel_ad-$paket" ;;
  *) ad="$temel_ad" ;;
esac

gecici="$dizin/.indiriliyor.$$"
trap 'rm -f "$gecici"' EXIT INT TERM

echo "İndiriliyor: $ad"
if [ "$ad" != "$temel_ad" ]; then
  http_kod="$(curl -sSL -w '%{http_code}' "$ADRES/indir/$ad" -o "$gecici" || echo "000")"
  if [ "$http_kod" = "404" ]; then
    rm -f "$gecici"
    ad="$temel_ad"
    echo "İndiriliyor: $ad"
    curl -fsSL "$ADRES/indir/$ad" -o "$gecici"
  elif [ "$http_kod" != "200" ]; then
    echo "İndirme başarısız oldu (HTTP $http_kod)." >&2
    exit 1
  fi
else
  curl -fsSL "$ADRES/indir/$ad" -o "$gecici"
fi

beklenen="$(curl -fsSL "$ADRES/indir/$ad.sha256.txt" | tr -d ' \r\n')"
if command -v shasum >/dev/null 2>&1; then
  bulunan="$(shasum -a 256 "$gecici" | cut -d' ' -f1)"
else
  bulunan="$(sha256sum "$gecici" | cut -d' ' -f1)"
fi
if [ -z "$beklenen" ] || [ "$beklenen" != "$bulunan" ]; then
  echo "İndirilen dosya doğrulanamadı, kurulum durduruldu." >&2
  exit 1
fi
chmod 755 "$gecici"
mv -f "$gecici" "$dizin/token-panosu"
trap - EXIT INT TERM

if [ "$kod_var" = "1" ]; then
  "$dizin/token-panosu" kur --url "$ADRES" "$@"
else
  "$dizin/token-panosu" onar --url "$ADRES" "$@"
fi
