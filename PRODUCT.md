# Product

<!-- impeccable:product-schema 1 -->

## Platform

web

## Users
Claude Code, ChatGPT (Codex CLI) ve Gemini CLI araçlarını birden fazla bilgisayarda ya da ortak abonelik paylaşan küçük bir ekipte kullanan geliştiriciler. Hangi bilgisayarın veya üyenin haftalık ve 5 saatlik abonelik limitini ne kadar tükettiğini tek bakışta görmek, limit paylaşımını adil yönetmek ve tüm sistemi kendi ücretsiz Cloudflare hesabında barındırmak isterler.

## Product Purpose
Eşleştirilmiş bilgisayarlardaki yerel oturum günlüklerini (girdi, çıktı, önbellek yazma, önbellek okuma, toplam token, resmî API karşılığı \$ ve sağlayıcı limit yüzdeleri) pasif biçimde okuyup saatlik ve 5 dakikalık dilimler halinde toplar; üye ve bilgisayar bazında haftalık/günlük grafiklerle ve canlı bir izometrik ofis simülasyonuyla sunar. Aynı zamanda her ayrıntının (saat dilimi, hafta başlangıcı, üye kota ağırlıkları, karakter görünümleri, limit yüzdeleri, manuel kullanım kayıtları, ortak hesap kilitleri, ofis kuralları ve model birim fiyatları) doğrudan web arayüzünden ayarlanabilmesini sağlar.

## Positioning
- **%100 Ücretsiz Barındırma:** Cloudflare Workers + D1 ücretsiz katmanı üzerinde kredi kartı gerektirmeden çalışır.
- **Sıfır Müdahale & Derleme Zamanı İzolasyonu:** Yalnızca yerel günlük dosyalarını pasif olarak okur; sağlayıcılara fazladan API isteği atmaz, sohbet metinlerini veya gizli anahtarları asla sunucuya göndermez. Cihaz başına seçilen sağlayıcı kombinasyonuna (`c`, `g`, `m`, `cg`, `cm`, `gm`, `cgm`) göre izole derlenmiş ikilide izin verilmeyen sağlayıcının okuma kodu bile bulunmaz; macOS'ta yalnızca yerel APFS diskleri taranarak ağ diski izin pencereleri tetiklenmez.
- **Kriptografik Güven & Otomatik Güncelleme:** Gönderimler cihaza özel anahtarla HMAC-SHA256 + sürüm/ikili mührüyle doğrulanır; istemci ikili güncellemeleri RSA-3072 açık anahtarıyla imzalanır; açık kaynak çatallar GitHub Actions veya `npm run guncelle` ile ana depodaki yeni sürümlere otomatik güncellenebilir.

## Operating Context
- Hafta sıfırlama günü/saati ve IANA saat dilimi `/pano/ayarlar` üzerinden serbestçe seçilebilir (varsayılan: `Europe/Istanbul`, Cumartesi `09:00`).
- Gönderici her 5 dakikada bir son 8 günün saatlik ve son 48 saatin 5 dakikalık toplamlarını yollar; geçmiş haftalar D1'de saklanır.
- Yeni bilgisayarlar, panodan üretilen 15 dakika geçerli tek kullanımlık eşleşme koduyla tek satırlık komutla (`kur.sh` / `kur.ps1`) bağlanır.
- Ana sayfa kurulum komutlarını gösterir; pano ve yönetim ekranları şifre korumalı oturumla açılır.

## Capabilities and Constraints
- Cloudflare Workers + D1 (ücretsiz katman), `workers.dev` veya özel alan adı.
- Vanilla HTML/CSS/JS, haricî ön yüz çerçevesi veya derleme adımı yok; Worker'dan doğrudan servis edilir.
- Çoklu sağlayıcı: Claude Code (`claude`), OpenAI Codex CLI (`chatgpt`) ve Gemini CLI + Google Antigravity (`gemini` — `Gemini 4 Carbon`, `Argon`, `3.8 Flash`, `3.5 Flash`, `Claude Opus 5.5` dahil).
- Resmî API fiyat listesi günlük otomatik yenilenir ve istenirse `/pano/ayarlar/fiyatlar` üzerinden model bazında elle ezilebilir.

## Product Principles
1. Ana soru tek bakışta cevaplanır: bu hafta hangi üye ve bilgisayar ne kadar tüketti.
2. Her otomatik hesaplama ve okuma gerektiğinde web arayüzünden şeffafça elle düzeltilebilir.
3. Sayıların kaynağı, tazeliği ve tahmin niteliği her zaman açıkça gösterilir.
4. Güvenlik varsayılandır: imzasız veri kabul edilmez, gizli bilgi yerel bilgisayardan dışarı çıkmaz.
