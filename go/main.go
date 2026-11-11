// Token Panosu gönderici (macOS, Windows, Linux).
//
// Bu bilgisayardaki Claude Code (CLI ve masaüstü uygulamadaki Code sekmesi) kullanımını
// okur, saat/model/oturum bazında toplar ve panoya HMAC-SHA256 ile imzalı olarak gönderir.
//
// Gizli anahtar programın içinde değildir: kurulumda bu bilgisayarda rastgele üretilir ve
// işletim sisteminin korumasında saklanır (macOS Anahtar Zinciri, Windows DPAPI, Linux 0600 dosya).
// Hangi oturumun sayılmayacağına yalnızca sunucu (pano yöneticisi) karar verir; programda
// hariç tutma yoktur.
package main

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/url"
	"os"
	"path/filepath"
	"runtime"
	"runtime/debug"
	"sort"
	"strconv"
	"strings"
	"time"
)

const (
	Surum    = 26 // yeni sürüm yayınlanınca artırılır; imzalı güncelleme bunu kullanır
	Protokol = 2  // sunucu ile veri biçimi

	yukEsigi      = 1.5               // 1 dk yük / çekirdek: üstündeyse (işlemci çok meşgul) otomatik çalıştırma o turu atlar
	zorlaSn       = 10 * 60           // son başarılı gönderim bundan eskiyse yük ne olursa olsun gönder (ekran kuralı 15 dk)
	zamanAsimi    = 240 * time.Second // tek çalıştırma en fazla bu kadar sürer
	saatPenceresi = 8 * 24            // son 8 günün saatleri gönderilir
)

// Derleme anında -ldflags "-X main.VarsayilanURL=https://..." ile kendi sunucu adresiniz gömülebilir.
var VarsayilanURL = ""
var EskiSunucuURLler = ""

var (
	dizin    string
	testModu bool // TOKEN_PANOSU_DIZIN ayarlıysa: ayrı klasör, dosyada anahtar, zamanlayıcı yok
)

func init() {
	if d := os.Getenv("TOKEN_PANOSU_DIZIN"); d != "" {
		dizin, testModu = d, true
		return
	}
	dizin = filepath.Join(evDizini(), ".token-panosu")
}

func evDizini() string {
	if h, err := os.UserHomeDir(); err == nil && h != "" {
		return h
	}
	return "."
}

func ayarYolu() string     { return filepath.Join(dizin, "ayar.json") }
func logYolu() string      { return filepath.Join(dizin, "log.txt") }
func sonYolu() string      { return filepath.Join(dizin, "son_gonderim") }
func onbellekYolu() string { return filepath.Join(dizin, "onbellek.bin") }
func ikiliYolu() string {
	ad := "token-panosu"
	if runtime.GOOS == "windows" {
		ad += ".exe"
	}
	return filepath.Join(dizin, ad)
}

// İşletim sistemi adı (sunucunun beklediği biçim)
func sistemAdi() string {
	switch runtime.GOOS {
	case "darwin":
		return "Darwin"
	case "windows":
		return "Windows"
	default:
		return "Linux"
	}
}

type Ayar struct {
	URL    string `json:"url"`
	Cihaz  string `json:"cihaz"`
	Etiket string `json:"etiket"`
	// Durum satırı kaydı (bkz. durum_satiri.go): ayarYaz bunları kaybetmesin
	DurumOnceki  map[string]json.RawMessage `json:"durum_satiri_onceki,omitempty"`
	DurumKuruldu []string                   `json:"durum_satiri_kuruldu,omitempty"`
}

func ayarOku() (Ayar, error) {
	var a Ayar
	b, err := os.ReadFile(ayarYolu())
	if err != nil {
		return a, errors.New("bu bilgisayar kurulu değil")
	}
	if err := json.Unmarshal(b, &a); err != nil || a.URL == "" || a.Cihaz == "" {
		return a, errors.New("ayar dosyası bozuk")
	}
	if !testModu && VarsayilanURL != "" && VarsayilanURL != a.URL && adresGecerli(VarsayilanURL) && EskiSunucuURLler != "" {
		temiz := strings.TrimRight(a.URL, "/")
		for _, eski := range strings.Split(EskiSunucuURLler, ",") {
			eski = strings.TrimRight(strings.TrimSpace(eski), "/")
			if eski != "" && temiz == eski {
				a.URL = VarsayilanURL
				_ = ayarYaz(a)
				break
			}
		}
	}
	return a, nil
}

func ayarYaz(a Ayar) error {
	b, _ := json.MarshalIndent(a, "", " ")
	return dosyaYaz(ayarYolu(), b, 0o600)
}

// Atomik yazma: geçici dosya + yeniden adlandırma
func dosyaYaz(yol string, veri []byte, izin os.FileMode) error {
	gec := yol + ".yeni"
	if err := os.WriteFile(gec, veri, izin); err != nil {
		return err
	}
	return os.Rename(gec, yol)
}

func logYaz(format string, a ...any) {
	_ = os.MkdirAll(dizin, 0o700)
	yol := logYolu()
	if st, err := os.Stat(yol); err == nil && st.Size() > 200_000 {
		if b, err := os.ReadFile(yol); err == nil {
			satirlar := strings.Split(strings.TrimRight(string(b), "\n"), "\n")
			if len(satirlar) > 200 {
				satirlar = satirlar[len(satirlar)-200:]
			}
			_ = os.WriteFile(yol, []byte(strings.Join(satirlar, "\n")+"\n"), 0o600)
		}
	}
	f, err := os.OpenFile(yol, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o600)
	if err != nil {
		return
	}
	defer f.Close()
	fmt.Fprintf(f, "%s %s\n", time.Now().Format("2006-01-02 15:04:05"), fmt.Sprintf(format, a...))
}

// ---------- komut satırı ----------

type bayraklar struct {
	kod, url                      string
	sessiz, guncellemeYok, yardim bool
}

func bayrakAyir(args []string) bayraklar {
	var b bayraklar
	for i := 0; i < len(args); i++ {
		switch args[i] {
		case "--kod":
			if i+1 < len(args) {
				b.kod = args[i+1]
				i++
			}
		case "--url":
			if i+1 < len(args) {
				b.url = args[i+1]
				i++
			}
		case "--sessiz":
			b.sessiz = true
		case "--guncelleme-yok":
			b.guncellemeYok = true
		case "-h", "--help", "yardim":
			b.yardim = true
		}
	}
	return b
}

const yardimMetni = `Token Panosu gönderici

  token-panosu kur --kod XXXXX-XXXXX   bu bilgisayarı panoya bağlar
  token-panosu gonder                  şimdi gönder
  token-panosu durum                   durumu ve son kayıtları göster
  token-panosu guncelle                yeni sürüm varsa şimdi güncelle
  token-panosu kaldir                  gönderimi durdurur, anahtarı ve dosyaları siler
`

func main() {
	if len(os.Args) > 1 && os.Args[1] == "durum-satiri" {
		durumSatiri() // Claude Code durum satırı: hafif, konsol bağlamaz, her zaman 0 ile çıkar
		os.Exit(0)
	}
	if len(os.Args) > 1 && os.Args[1] == uyariAnahtar {
		uyariKomutu() // Claude Code kancası (her istek): yalnızca yerel dosyaya bakar, ağa çıkmaz, her zaman 0 ile çıkar
		os.Exit(0)
	}
	if len(os.Args) > 1 && os.Args[1] == uyariGosterAnahtar {
		uyariGoster()
		os.Exit(0)
	}
	runtime.GOMAXPROCS(1)   // tek çekirdek: işlemciyi bir anlığına bile zorlamasın
	debug.SetGCPercent(400) // kısa ömürlü süreç: çöp toplayıcı daha seyrek çalışsın
	pencereBagla()          // Windows: komut satırından çalıştırılınca çıktıyı göster
	args := os.Args[1:]
	if len(args) == 0 {
		fmt.Print(yardimMetni)
		return
	}
	b := bayrakAyir(args[1:])
	var err error
	switch args[0] {
	case "kur":
		err = kurKomutu(b)
	case "gonder":
		os.Exit(gonderKomutu(b))
	case "guncelle":
		var oldu bool
		oldu, err = guncelle()
		if err == nil {
			if oldu {
				fmt.Println("Güncellendi. Yeni sürüm bir sonraki çalıştırmada devreye girer.")
			} else {
				fmt.Printf("Zaten güncel (sürüm %d).\n", Surum)
			}
		}
	case "durum":
		err = durumKomutu()
	case "kaldir":
		err = kaldirKomutu()
	case "limit-sorgu": // sürüm 17'de kaldırıldı
		eskiSorguTemizle()
		fmt.Println("Limit sorgusu kaldırıldı. Limit yalnızca bu bilgisayardaki dosyalardan okunur.")
	case "surum", "--version":
		fmt.Println(Surum)
	case "_bildirim": // geliştirici testi: tek bildirimi gerçekten gösterir
		if len(args) < 3 {
			err = errors.New("kullanım: token-panosu _bildirim <başlık> <metin>")
		} else if err = bildirimGoster(args[1], args[2]); err == nil {
			fmt.Println("bildirim gösterildi")
		}
	case "_govde": // geliştirici testi: gönderilecek gövdeyi yazdırır, hiçbir şey göndermez
		var g []byte
		g, _, err = govdeHazirla(time.Now().Unix())
		if err == nil {
			os.Stdout.Write(g)
		}
	default:
		fmt.Print(yardimMetni)
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, "Hata:", err)
		os.Exit(1)
	}
}

func adresGecerli(u string) bool {
	p, err := url.Parse(u)
	if err != nil || p.Host == "" {
		return false
	}
	if p.Scheme == "https" {
		return true
	}
	h := p.Hostname()
	return p.Scheme == "http" && (h == "localhost" || h == "127.0.0.1") // yalnızca yerel deneme sunucusu
}

func rastgeleHex(n int) string {
	b := make([]byte, n)
	if _, err := io.ReadFull(rand.Reader, b); err != nil {
		panic(err)
	}
	return hex.EncodeToString(b)
}

// ---------- kur ----------

func kurKomutu(b bayraklar) error {
	adres := strings.TrimRight(b.url, "/")
	if adres == "" {
		adres = VarsayilanURL
	}
	if !adresGecerli(adres) {
		return errors.New("adres https:// ile başlamalı")
	}
	kod := strings.ToUpper(strings.TrimSpace(b.kod))
	if kod == "" {
		return errors.New("eşleşme kodu gerekli: token-panosu kur --kod XXXXX-XXXXX")
	}
	if err := os.MkdirAll(dizin, 0o700); err != nil {
		return err
	}
	_ = os.Chmod(dizin, 0o700)
	if _, err := os.Stat(ayarYolu()); err == nil {
		if _, err := os.Stat(filepath.Join(dizin, "gonderici.py")); err == nil {
			return errors.New("eski (Python) gönderici kurulu. Önce onu kaldır: python3 ~/.token-panosu/gonderici.py kaldir")
		}
		return errors.New("bu bilgisayar zaten kurulu. Önce 'kaldir' çalıştır")
	}

	gizli := rastgeleHex(32)
	govde, _ := json.Marshal(map[string]string{"code": kod, "secret": gizli})
	durum, cevap, err := postJSON(adres+"/api/eslestir", govde, nil)
	if err != nil {
		return fmt.Errorf("sunucuya ulaşılamadı: %v", err)
	}
	if durum != 200 {
		return fmt.Errorf("eşleşme başarısız (%d): %s", durum, hataMetni(cevap))
	}
	var e struct {
		DeviceID string `json:"device_id"`
		Label    string `json:"label"`
	}
	if json.Unmarshal(cevap, &e) != nil || len(e.DeviceID) != 32 {
		return errors.New("sunucudan beklenmeyen yanıt")
	}
	if err := anahtarKaydet(e.DeviceID, gizli); err != nil {
		return fmt.Errorf("anahtar saklanamadı: %v", err)
	}
	if err := ayarYaz(Ayar{URL: adres, Cihaz: e.DeviceID, Etiket: e.Label}); err != nil {
		return err
	}
	if err := kendiniYerlestir(); err != nil {
		return fmt.Errorf("program klasöre kopyalanamadı: %v", err)
	}
	uyariKur()
	fmt.Printf("Eşleşti: %s\n", e.Label)
	if ok, satir := gonder(); ok {
		fmt.Printf("İlk gönderim tamam: %d satır.\n", satir)
	} else {
		fmt.Println("İlk gönderim başarısız oldu; zamanlayıcı birkaç dakika içinde yeniden deneyecek.")
	}
	if testModu {
		return nil
	}
	if err := zamanlayiciKur(ikiliYolu()); err != nil {
		return fmt.Errorf("zamanlayıcı kurulamadı: %v", err)
	}
	fmt.Println("Arka planda 5 dakikada bir gönderecek; bilgisayar yeniden başlayınca kendiliğinden devam eder.")
	return nil
}

// Çalışan programı sabit konuma kopyalar (zaten oradaysa bir şey yapmaz)
func kendiniYerlestir() error {
	kaynak, err := os.Executable()
	if err != nil {
		return err
	}
	if g, err := filepath.EvalSymlinks(kaynak); err == nil {
		kaynak = g
	}
	hedef := ikiliYolu()
	if h, err := filepath.EvalSymlinks(hedef); err == nil && h == kaynak {
		return nil
	}
	veri, err := os.ReadFile(kaynak)
	if err != nil {
		return err
	}
	return dosyaYaz(hedef, veri, 0o755)
}

// ---------- gonder ----------

func gonderKomutu(b bayraklar) int {
	if b.sessiz {
		if uygun, oran := calismaUygun(time.Now().Unix()); !uygun {
			logYaz("sistem meşgul, atlandı (yük %.2f)", oran)
			return 0
		}
	}
	zamanlayici := time.AfterFunc(zamanAsimi, func() {
		logYaz("zaman aşımı: çalıştırma süre sınırını aştı, çıkılıyor")
		os.Exit(1)
	})
	defer zamanlayici.Stop()
	oncelikDusur()
	eskiyiTemizle()

	ok, satir := gonder()
	if !b.sessiz {
		if ok {
			fmt.Printf("Gönderildi: %d satır.\n", satir)
		} else {
			fmt.Println("Gönderim başarısız; ayrıntı için: token-panosu durum")
		}
	}
	if !b.guncellemeYok {
		oldu, err := guncelle()
		if err != nil {
			logYaz("güncelleme atlandı: %v", err)
		} else if oldu && !b.sessiz {
			fmt.Println("Gönderici yeni sürüme güncellendi.")
		}
	}
	if ok {
		return 0
	}
	return 1
}

func sonBasari() int64 {
	b, err := os.ReadFile(sonYolu())
	if err != nil {
		return 0
	}
	n, _ := strconv.ParseInt(strings.TrimSpace(string(b)), 10, 64)
	return n
}

// (uygun, oran): yük ölçülemiyorsa ya da son başarılı gönderim çok eskiyse her zaman uygun
func calismaUygun(simdi int64) (bool, float64) {
	oran, olculdu := yukOrani()
	if !olculdu || simdi-sonBasari() > zorlaSn {
		return true, oran
	}
	return oran <= yukEsigi, oran
}

func gonder() (bool, int) {
	a, err := ayarOku()
	if err != nil {
		logYaz("HATA: %v", err)
		return false, 0
	}
	gizli, err := anahtarOku(a.Cihaz)
	if err != nil {
		logYaz("HATA: anahtar okunamadı: %v", err)
		return false, 0
	}
	durumSatiriKaldir() // v17: durum satırından limit okunmuyor; kurulduysa önceki ayar geri konur
	uyariKur()          // v17: limit payı dolunca her istekte uyarı
	eskiSorguTemizle()
	simdi := time.Now().Unix()
	govde, s, err := govdeHazirla(simdi)
	if err != nil {
		logYaz("HATA: %v", err)
		return false, 0
	}
	ts := strconv.FormatInt(time.Now().Unix(), 10)
	basliklar := map[string]string{
		"x-device":    a.Cihaz,
		"x-timestamp": ts,
		"x-signature": imzaHMAC(gizli, a.Cihaz, ts, govde),
	}
	durum, cevap, err := postJSON(a.URL+"/api/gonder", govde, basliklar)
	if err != nil {
		logYaz("HATA: sunucuya ulaşılamadı: %v", err)
		return false, 0
	}
	if durum != 200 {
		logYaz("HATA %d: %s", durum, hataMetni(cevap))
		return false, 0
	}
	_ = os.WriteFile(sonYolu(), []byte(strconv.FormatInt(simdi, 10)), 0o600)
	if s.limitT > 0 {
		_ = os.WriteFile(sonLimitYolu(), []byte(strconv.FormatInt(s.limitT, 10)), 0o600)
	}
	for k, lt := range s.limitSaglayici {
		if lt > 0 {
			_ = os.WriteFile(sonLimitSaglayiciYolu(k), []byte(strconv.FormatInt(lt, 10)), 0o600)
		}
	}
	var yanit struct {
		Yazilan  *int             `json:"yazilan"`
		Bildirim []bildirimOge    `json:"b"`    // sürüm 14+: gösterilecek bildirimler (en çok 3)
		Esik     map[string]int64 `json:"esik"` // sürüm 16+: istem uzunluğuna göre fiyatlanan modellerin eşikleri
		Asim     *asimDurumu      `json:"asim"` // sürüm 17+: bu bilgisayarın sahibi limit payını doldurduysa
	}
	_ = json.Unmarshal(cevap, &yanit)
	bildirimleriIsle(yanit.Bildirim)
	if yanit.Esik != nil {
		esikleriKaydet(yanit.Esik)
	}
	asimIsle(yanit.Asim, simdi)
	ek := ""
	if len(s.bulgular) > 0 {
		kodlar := make([]string, len(s.bulgular))
		for i, x := range s.bulgular {
			kodlar[i] = x.K
		}
		ek = "; kontrol: " + strings.Join(kodlar, ", ")
	}
	if yanit.Yazilan != nil {
		ek = fmt.Sprintf(", sunucuda %d satır değişti", *yanit.Yazilan) + ek
	}
	logYaz("gönderildi: %d satır, %d dilim, %d dosya okundu (%d ms)%s", len(s.satirlar), len(s.dilimler), s.okunan, s.sure.Milliseconds(), ek)
	return true, len(s.satirlar)
}

func govdeHazirla(simdi int64) ([]byte, sonuc, error) {
	s := topla(simdi)
	ekBas := time.Now()
	ek := toplaDiger(simdi)
	s.sure += time.Since(ekBas)
	s.okunan += ek.okunan
	for _, eb := range ek.bulgular {
		mevcut := false
		for _, mb := range s.bulgular {
			if mb.K == eb.K {
				mevcut = true
				break
			}
		}
		if !mevcut {
			s.bulgular = append(s.bulgular, eb)
		}
	}
	if len(ek.satirlar) > 0 {
		s.satirlar = append(s.satirlar, ek.satirlar...)
		sort.Slice(s.satirlar, func(i, j int) bool {
			a, b := s.satirlar[i], s.satirlar[j]
			if a.H != b.H {
				return a.H < b.H
			}
			if a.Model != b.Model {
				return a.Model < b.Model
			}
			return a.Sid < b.Sid
		})
	}
	if len(ek.dilimler) > 0 {
		s.dilimler = append(s.dilimler, ek.dilimler...)
		sort.Slice(s.dilimler, func(i, j int) bool {
			a, b := s.dilimler[i], s.dilimler[j]
			if a.T != b.T {
				return a.T < b.T
			}
			if a.Model != b.Model {
				return a.Model < b.Model
			}
			return a.Sid < b.Sid
		})
	}
	alanlar := map[string]any{
		"rows": s.satirlar,
		"m":    s.dilimler,
		"v":    Protokol,
		"s":    Surum,
		"c":    s.bulgular,
		"o":    sistemAdi(),
	}
	if len(ek.hesaplar) > 0 {
		alanlar["hesap"] = ek.hesaplar
	}
	if len(ek.epostalar) > 0 {
		alanlar["eposta"] = ek.epostalar
	}
	if len(ek.haftaSifirlama) > 0 {
		alanlar["hr"] = ek.haftaSifirlama
	}
	if len(s.devir) > 0 {
		alanlar["dv"] = s.devir // {"eskiSid": "kokSid"}: sunucu eski sid'li satırları pencerede siler (sürüm 15+)
	}
	if n, ok := sonBildirim(); ok {
		alanlar["bs"] = n // gösterilen son bildirim kimliği
	}
	if l, ok := limitBirlesik(simdi); ok && l.T > sonLimitT() {
		alanlar["l"] = l // yeni limit bilgisi varsa: {"t": unix, "r": {düz harita}}
		s.limitT = l.T
	}
	if len(ek.limitler) > 0 {
		alanlar["lp"] = ek.limitler
		s.limitSaglayici = make(map[string]int64, len(ek.limitler))
		for k, v := range ek.limitler {
			s.limitSaglayici[k] = v.T
		}
	}
	govde, err := json.Marshal(alanlar)
	return govde, s, err
}

// ---------- durum / kaldir ----------

func durumKomutu() error {
	a, err := ayarOku()
	if err != nil {
		return err
	}
	fmt.Printf("Bilgisayar: %s\nAdres: %s\nCihaz: %s\nGönderici sürümü: %d\n", a.Etiket, a.URL, a.Cihaz, Surum)
	if n := sonBasari(); n > 0 {
		fmt.Printf("Son başarılı gönderim: %s\n", time.Unix(n, 0).Format("2006-01-02 15:04:05"))
	}
	if b, err := os.ReadFile(logYolu()); err == nil {
		satirlar := strings.Split(strings.TrimRight(string(b), "\n"), "\n")
		if len(satirlar) > 5 {
			satirlar = satirlar[len(satirlar)-5:]
		}
		fmt.Println("Son kayıtlar:\n" + strings.Join(satirlar, "\n"))
	}
	return nil
}

func kaldirKomutu() error {
	durumSatiriKaldir()
	uyariKaldir()
	if !testModu {
		zamanlayiciKaldir()
	}
	if a, err := ayarOku(); err == nil {
		anahtarSil(a.Cihaz)
	}
	klasoruSil(dizin)
	fmt.Println("Kaldırıldı. Bilgisayarı panodan silmek ayrı bir işlem (detay sayfası).")
	return nil
}

func hataMetni(b []byte) string {
	var m struct {
		Hata string `json:"hata"`
	}
	if json.Unmarshal(b, &m) == nil && m.Hata != "" {
		return m.Hata
	}
	if len(b) > 200 {
		b = b[:200]
	}
	return string(b)
}
