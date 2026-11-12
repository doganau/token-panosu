#!/bin/sh
# Uygulamayı tüm sistemler için derler: dist/indir/token-panosu-<sistem>-<mimari>[.exe] ve .sha256.txt
# İsteğe bağlı ortam değişkenleri (.derle.env dosyasından da okunabilir):
#   TOKEN_PANOSU_URL          -> ikilinin varsayılan sunucu adresi (ör. https://pano.ornek.workers.dev)
#   TOKEN_PANOSU_ESKI_URLLER  -> eski sunucu adreslerinden otomatik geçiş listesi (virgülle ayrılmış)
#   TOKEN_PANOSU_ACIK_N       -> güncelleme doğrulama RSA açık modülü (veya "devre-disi"); boşsa mevcut imza anahtarından okunur
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
if [ -z "$acik_n" ] && command -v python3 >/dev/null 2>&1; then
  acik_n="$(python3 araclar/imzala.py acik-anahtar 2>/dev/null || true)"
fi

sha256_hesapla() {
  if command -v shasum >/dev/null 2>&1; then
    shasum -a 256 "$1" | cut -d' ' -f1
  else
    sha256sum "$1" | cut -d' ' -f1
  fi
}

mkdir -p dist/indir
for hedef in darwin/arm64 darwin/amd64 linux/amd64 linux/arm64 windows/amd64 windows/arm64; do
  sistem="${hedef%/*}"; mimari="${hedef#*/}"
  ad="token-panosu-$sistem-$mimari"
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
  if command -v python3 >/dev/null 2>&1; then
    muhur_ek="$(python3 araclar/imzala.py muhur-bayrak "$surum" "$sistem-$mimari" 2>/dev/null || true)"
    if [ -n "$muhur_ek" ]; then
      bayrak="$bayrak $muhur_ek"
    fi
  fi
  if [ "$sistem" = windows ]; then ad="$ad.exe"; bayrak="$bayrak -H=windowsgui"; fi
  (cd go && CGO_ENABLED=0 GOOS="$sistem" GOARCH="$mimari" go build -trimpath -ldflags "$bayrak" -o "../dist/indir/$ad" .)
  sha256_hesapla "dist/indir/$ad" > "dist/indir/$ad.sha256.txt"
  printf '%-34s %s KB\n' "$ad" "$(( $(wc -c < "dist/indir/$ad") / 1024 ))"
done
echo "Sürüm $surum derlendi."
