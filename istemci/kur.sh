#!/bin/sh
# Token Panosu kurulumu (macOS ve Linux)
#   curl -fsSL https://pano.ornek.com/kur.sh | sh -s -- --kod XXXXX-XXXXX
# Bu bilgisayara uygun tek dosyalık programı indirir, özetini doğrular, ~/.token-panosu içine koyar
# ve eşleştirmeyi yapar. Python ya da başka bir şey kurmaz.
set -eu

ADRES="${TOKEN_PANOSU_ADRES:-https://pano.ornek.com}"

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
ad="token-panosu-$sistem-$mimari"

dizin="$HOME/.token-panosu"
mkdir -p "$dizin"
chmod 700 "$dizin"
gecici="$dizin/.indiriliyor.$$"
trap 'rm -f "$gecici"' EXIT INT TERM

echo "İndiriliyor: $ad"
curl -fsSL "$ADRES/indir/$ad" -o "$gecici"
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

"$dizin/token-panosu" kur --url "$ADRES" "$@"
