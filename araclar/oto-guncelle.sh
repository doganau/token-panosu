#!/usr/bin/env bash
# Token Panosu — Açık Kaynak Otomatik Güncelleyici (Sunucu + 42 İzole İkili)
#
# GitHub ana deposundaki (varsayılan: doganau/token-panosu) en güncel sürümü kontrol eder.
# Yeni sürüm yayınlandığında yerel yapılandırmanızı (wrangler.toml, .derle.env, imza/mühür anahtarları)
# koruyarak güncellemeyi çeker, 42 izole gönderici ikilisini sizin anahtarınızla derleyip imzalar
# ve Cloudflare Workers + D1 sunucunuzu günceller. Sunucu güncellendikten sonraki 5 dakika içinde
# bağlı tüm bilgisayarlar kendi göndericilerini otomatik olarak yeni sürüme yükseltir.
#
# Kullanım:
#   npm run guncelle                        # yeni sürüm varsa şimdi güncelle ve yayınla
#   bash araclar/oto-guncelle.sh --zorla    # sürüm aynı olsa bile yeniden derle ve yayınla
#   bash araclar/oto-guncelle.sh --kontrol  # yalnızca yeni sürüm olup olmadığını kontrol et
#   bash araclar/oto-guncelle.sh --zamanlayici-kur    # günlük otomatik arka plan kontrolünü kur
#   bash araclar/oto-guncelle.sh --zamanlayici-kaldir # günlük otomatik kontrolü kaldır
set -euo pipefail

KOK="$(cd "$(dirname "$0")/.." && pwd)"
cd "$KOK"

KAYNAK_REPO="${TOKEN_PANOSU_KAYNAK_REPO:-doganau/token-panosu}"
KAYNAK_DAL="${TOKEN_PANOSU_KAYNAK_DAL:-main}"
KAYNAK_GIT="https://github.com/${KAYNAK_REPO}.git"

ZORLA=0
SADECE_KONTROL=0
ZAMANLAYICI=""

for arg in "$@"; do
  case "$arg" in
    --zorla|-f) ZORLA=1 ;;
    --kontrol|--check) SADECE_KONTROL=1 ;;
    --zamanlayici-kur) ZAMANLAYICI="kur" ;;
    --zamanlayici-kaldir) ZAMANLAYICI="kaldir" ;;
  esac
done

bilgi()  { printf "▸ %s\n" "$*"; }
basari() { printf "✓ %s\n" "$*"; }
hata()   { printf "✗ %s\n" "$*" >&2; exit 1; }

if [ "$ZAMANLAYICI" = "kur" ]; then
  KOMUT="cd \"$KOK\" && bash araclar/oto-guncelle.sh >> \"$KOK/.oto-guncelle.log\" 2>&1"
  if [ "$(uname -s)" = "Darwin" ]; then
    PLIST="$HOME/Library/LaunchAgents/com.tokenpanosu.sunucu-guncelle.plist"
    mkdir -p "$HOME/Library/LaunchAgents"
    cat > "$PLIST" <<EOF
<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0">
<dict>
  <key>Label</key><string>com.tokenpanosu.sunucu-guncelle</string>
  <key>ProgramArguments</key>
  <array>
    <string>/bin/bash</string>
    <string>${KOK}/araclar/oto-guncelle.sh</string>
  </array>
  <key>WorkingDirectory</key><string>${KOK}</string>
  <key>StartInterval</key><integer>43200</integer>
  <key>StandardOutPath</key><string>${KOK}/.oto-guncelle.log</string>
  <key>StandardErrorPath</key><string>${KOK}/.oto-guncelle.log</string>
</dict>
</plist>
EOF
    launchctl bootout "gui/$(id -u)" "$PLIST" 2>/dev/null || true
    launchctl bootstrap "gui/$(id -u)" "$PLIST"
    basari "Otomatik sunucu güncelleyici kuruldu (12 saatte bir GitHub sürümünü denetler)."
  else
    MEVCUT_CRON="$(crontab -l 2>/dev/null | grep -v 'token-panosu-oto-guncelle' || true)"
    printf "%s\n17 */12 * * * %s # token-panosu-oto-guncelle\n" "$MEVCUT_CRON" "$KOMUT" | sed '/^$/d' | crontab -
    basari "Crontab otomatik güncelleyici kuruldu (12 saatte bir GitHub sürümünü denetler)."
  fi
  exit 0
elif [ "$ZAMANLAYICI" = "kaldir" ]; then
  if [ "$(uname -s)" = "Darwin" ]; then
    PLIST="$HOME/Library/LaunchAgents/com.tokenpanosu.sunucu-guncelle.plist"
    launchctl bootout "gui/$(id -u)" "$PLIST" 2>/dev/null || true
    rm -f "$PLIST"
  else
    (crontab -l 2>/dev/null | grep -v 'token-panosu-oto-guncelle' || true) | crontab -
  fi
  basari "Otomatik sunucu güncelleyici kaldırıldı."
  exit 0
fi

YEREL_SURUM="$(sed -nE 's/^[[:space:]]*Surum[[:space:]]*=[[:space:]]*([0-9]+).*/\1/p' go/main.go | head -n 1)"
[ -n "$YEREL_SURUM" ] || YEREL_SURUM=0

UZAK_GO="$(curl -fsSL --max-time 10 "https://raw.githubusercontent.com/${KAYNAK_REPO}/${KAYNAK_DAL}/go/main.go" || true)"
UZAK_SURUM="$(printf '%s\n' "$UZAK_GO" | sed -nE 's/^[[:space:]]*Surum[[:space:]]*=[[:space:]]*([0-9]+).*/\1/p' | head -n 1)"

if [ -z "$UZAK_SURUM" ]; then
  hata "GitHub (${KAYNAK_REPO}) sürüm bilgisi okunamadı."
fi

bilgi "Yerel sürüm: v${YEREL_SURUM} | GitHub son sürüm: v${UZAK_SURUM} (${KAYNAK_REPO})"

if [ "$SADECE_KONTROL" = "1" ]; then
  if [ "$UZAK_SURUM" -gt "$YEREL_SURUM" ]; then
    bilgi "Yeni sürüm mevcut: v${UZAK_SURUM} -> güncellemek için: npm run guncelle"
    exit 0
  fi
  basari "Sisteminiz zaten en güncel sürümde (v${YEREL_SURUM})."
  exit 0
fi

if [ "$UZAK_SURUM" -le "$YEREL_SURUM" ] && [ "$ZORLA" != "1" ]; then
  basari "Zaten en güncel sürümdesiniz (v${YEREL_SURUM}). Zorla yeniden derlemek için --zorla ekleyin."
  exit 0
fi

if [ -d .git ] && [ "$UZAK_SURUM" -gt "$YEREL_SURUM" ]; then
  bilgi "GitHub üzerinden v${UZAK_SURUM} kaynak kodları çekiliyor..."
  if ! git remote get-url upstream >/dev/null 2>&1; then
    git remote add upstream "$KAYNAK_GIT"
  else
    git remote set-url upstream "$KAYNAK_GIT"
  fi
  git fetch upstream "$KAYNAK_DAL"
  STASH_YAPILDI=0
  if ! git diff --quiet || ! git diff --cached --quiet; then
    git stash push -m "oto-guncelle-yedek" >/dev/null
    STASH_YAPILDI=1
  fi
  git merge --ff-only "upstream/${KAYNAK_DAL}" || git rebase "upstream/${KAYNAK_DAL}"
  if [ "$STASH_YAPILDI" = "1" ]; then
    git stash pop >/dev/null || true
  fi
  basari "Kaynak kodları v${UZAK_SURUM} sürümüne yükseltildi."
fi

npm install --no-fund --no-audit

if command -v go >/dev/null 2>&1; then
  bilgi "42 izole gönderici ikilisi derleniyor..."
  sh araclar/derle.sh
  if command -v python3 >/dev/null 2>&1 && python3 araclar/imzala.py acik-anahtar >/dev/null 2>&1; then
    python3 araclar/imzala.py imzala
    basari "İkililer RSA-3072 anahtarınızla imzalandı."
  else
    python3 araclar/imzala.py imzasiz
  fi
fi

node --test src/*.test.mjs

if [ -f wrangler.toml ]; then
  bilgi "Cloudflare Worker ve statik ikililer yayınlanıyor..."
  ./node_modules/.bin/wrangler deploy
  basari "Sunucu v${UZAK_SURUM} sürümüne güncellendi; bağlı bilgisayarlar 5 dakika içinde kendiliğinden güncellenecek."
else
  bilgi "wrangler.toml bulunamadı; ilk kurulum için 'npm run kurulum' çalıştırın."
fi
