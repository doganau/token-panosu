package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"strconv"
	"strings"
)

type onarAday struct {
	Cihaz  string
	Gizli  string
	Etiket string
	URL    string
}

var (
	hex32Re      = regexp.MustCompile(`\b[0-9a-fA-F]{32}\b`)
	hex64Re      = regexp.MustCompile(`\b[0-9a-fA-F]{64}\b`)
	plistStrRe   = regexp.MustCompile(`<string>([^<]+)</string>`)
	bilinenBos64 = map[string]bool{
		"e3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7859b855": true,
		"e3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855": true,
		"0000000000000000000000000000000000000000000000000000000000000000": true,
	}
)

// Testlerde sahte LaunchAgents klasörü verebilmek için
var testLaunchAgentsDizini string

func launchAgentsDizini() string {
	if testModu && testLaunchAgentsDizini != "" {
		return testLaunchAgentsDizini
	}
	return filepath.Join(evDizini(), "Library", "LaunchAgents")
}

func plistBizimAlakaMi(ad, icerik string) bool {
	kucukAd := strings.ToLower(ad)
	kucuk := strings.ToLower(icerik)
	if strings.Contains(kucukAd, "tokenpanosu") || strings.Contains(kucukAd, "token-panosu") {
		return true
	}
	return strings.Contains(kucuk, ".token-panosu") ||
		strings.Contains(kucuk, "com.tokenpanosu") ||
		strings.Contains(kucuk, "/api/gonder") ||
		strings.Contains(kucuk, "x-signature")
}

func betikUzantisiMi(yol string) bool {
	u := strings.ToLower(yol)
	for _, ek := range []string{".py", ".sh", ".js", ".mjs", ".rb", ".pl"} {
		if strings.HasSuffix(u, ek) {
			return true
		}
	}
	return false
}

// satirdanBetikYollariCikar: kabuk komutu, plist argümanı ya da ps çıktısı içindeki
// olası özel betik dosyası yollarını (~ ve $HOME genişleterek) döndürür.
func satirdanBetikYollariCikar(metin string) []string {
	ev := evDizini()
	var sonuc []string
	gorulen := map[string]bool{}
	ekle := func(aday string) {
		aday = strings.TrimSpace(strings.Trim(aday, `"'();,&|<>`))
		if aday == "" || strings.HasPrefix(aday, "-") {
			return
		}
		if strings.HasPrefix(aday, "~/") {
			aday = filepath.Join(ev, aday[2:])
		} else if strings.HasPrefix(aday, "$HOME/") {
			aday = filepath.Join(ev, aday[6:])
		}
		if !betikUzantisiMi(aday) {
			return
		}
		if st, err := os.Stat(aday); err == nil && !st.IsDir() && st.Size() <= 512*1024 {
			if !gorulen[aday] {
				gorulen[aday] = true
				sonuc = append(sonuc, aday)
			}
		}
	}
	ekle(metin)
	for _, parca := range strings.Fields(metin) {
		ekle(parca)
		if i := strings.IndexByte(parca, '='); i >= 0 {
			ekle(parca[i+1:])
		}
	}
	return sonuc
}

func calisanYabanciBetikler() [] struct {
	pid   int
	betik string
} {
	if testModu || (runtime.GOOS != "darwin" && runtime.GOOS != "linux") {
		return nil
	}
	uid := strconv.Itoa(os.Getuid())
	benimPid := os.Getpid()
	cikti, err := exec.Command("ps", "-u", uid, "-o", "pid=,command=").Output()
	if err != nil || len(cikti) == 0 {
		return nil
	}
	var liste []struct {
		pid   int
		betik string
	}
	for _, satir := range strings.Split(string(cikti), "\n") {
		satir = strings.TrimSpace(satir)
		if satir == "" {
			continue
		}
		alanlar := strings.Fields(satir)
		if len(alanlar) < 2 {
			continue
		}
		pid, err := strconv.Atoi(alanlar[0])
		if err != nil || pid <= 1 || pid == benimPid {
			continue
		}
		komutSatiri := strings.TrimSpace(satir[len(alanlar[0]):])
		if strings.Contains(komutSatiri, ikiliYolu()) {
			continue
		}
		for _, y := range satirdanBetikYollariCikar(komutSatiri) {
			if strings.ToLower(filepath.Base(y)) == "kur.sh" {
				continue
			}
			if bb, err := os.ReadFile(y); err == nil && plistBizimAlakaMi(filepath.Base(y), string(bb)) {
				liste = append(liste, struct {
					pid   int
					betik string
				}{pid: pid, betik: y})
			}
		}
	}
	return liste
}

func metindenAdayCikar(icerik string, haric64 map[string]bool) (cihazlar []string, gizliler []string, etiket string, adres string) {
	var m map[string]any
	if json.Unmarshal([]byte(icerik), &m) == nil && m != nil {
		for _, k := range []string{"cihaz", "device", "device_id", "x-device"} {
			if v, ok := m[k].(string); ok {
				v = strings.ToLower(strings.TrimSpace(v))
				if gecerliHexAnahtar(v, 32) {
					cihazlar = append(cihazlar, v)
				}
			}
		}
		for _, k := range []string{"gizli", "secret", "anahtar", "key"} {
			if v, ok := m[k].(string); ok {
				v = strings.ToLower(strings.TrimSpace(v))
				if gecerliHexAnahtar(v, 64) && !bilinenBos64[v] && !haric64[v] {
					gizliler = append(gizliler, v)
				}
			}
		}
		if v, ok := m["etiket"].(string); ok && strings.TrimSpace(v) != "" {
			etiket = strings.TrimSpace(v)
		} else if v, ok := m["label"].(string); ok && strings.TrimSpace(v) != "" {
			etiket = strings.TrimSpace(v)
		}
		if v, ok := m["url"].(string); ok && adresGecerli(strings.TrimSpace(v)) {
			adres = strings.TrimRight(strings.TrimSpace(v), "/")
		}
	}

	for _, h := range hex64Re.FindAllString(icerik, 40) {
		h = strings.ToLower(h)
		if bilinenBos64[h] || haric64[h] {
			continue
		}
		gizliler = append(gizliler, h)
	}
	for _, h := range hex32Re.FindAllString(icerik, 60) {
		h = strings.ToLower(h)
		// 64-hex bir dizgenin parçası olmayan gerçek 32-hex adayları al
		parcaMi := false
		for _, g := range gizliler {
			if strings.Contains(g, h) {
				parcaMi = true
				break
			}
		}
		if !parcaMi {
			cihazlar = append(cihazlar, h)
		}
	}
	return
}

func benzersizEkle(hedef []string, yeniler ...string) []string {
	for _, y := range yeniler {
		if y == "" {
			continue
		}
		varMi := false
		for _, m := range hedef {
			if m == y {
				varMi = true
				break
			}
		}
		if !varMi {
			hedef = append(hedef, y)
		}
	}
	return hedef
}

func mevcutAdaylariBul() []onarAday {
	haric64 := map[string]bool{}
	// İndirilen ikililerin kendi sha256 özetlerini gizli anahtar sanmamak için dışla
	if girisler, err := os.ReadDir(dizin); err == nil {
		for _, g := range girisler {
			if strings.HasSuffix(g.Name(), ".sha256.txt") {
				if b, err := os.ReadFile(filepath.Join(dizin, g.Name())); err == nil {
					oz := strings.ToLower(strings.TrimSpace(string(b)))
					if gecerliHexAnahtar(oz, 64) {
						haric64[oz] = true
					}
				}
			}
		}
	}
	if b, err := os.ReadFile(ikiliYolu()); err == nil && len(b) > 0 {
		// Kendi ikilimizin sha256'sı bir betiğin içinde geçiyorsa gizli anahtar sanma
		oz := sha256HexYerel(b)
		if oz != "" {
			haric64[oz] = true
		}
	}

	var adaylar []onarAday
	adayEkle := func(c, g, et, u string) {
		if !gecerliHexAnahtar(c, 32) || !gecerliHexAnahtar(g, 64) || haric64[g] || bilinenBos64[g] {
			return
		}
		for _, m := range adaylar {
			if m.Cihaz == c && m.Gizli == g {
				return
			}
		}
		adaylar = append(adaylar, onarAday{Cihaz: c, Gizli: g, Etiket: et, URL: u})
	}

	var tumCihazlar, tumGizliler []string
	var genelEtiket, genelURL string

	// 1) Mevcut ayar.json + Anahtar Zinciri / anahtar dosyası
	if a, err := ayarOku(); err == nil && gecerliHexAnahtar(strings.ToLower(a.Cihaz), 32) {
		c := strings.ToLower(a.Cihaz)
		tumCihazlar = benzersizEkle(tumCihazlar, c)
		if genelEtiket == "" {
			genelEtiket = a.Etiket
		}
		if genelURL == "" {
			genelURL = a.URL
		}
		if g, err := anahtarOku(c); err == nil {
			g = strings.ToLower(strings.TrimSpace(g))
			adayEkle(c, g, a.Etiket, a.URL)
			tumGizliler = benzersizEkle(tumGizliler, g)
		}
	}

	dosyaTara := func(yol string) {
		st, err := os.Stat(yol)
		if err != nil || st.IsDir() || st.Size() <= 0 || st.Size() > 512*1024 {
			return
		}
		ad := strings.ToLower(filepath.Base(yol))
		if ad == "onbellek.bin" || ad == "log.txt" || ad == "token-panosu" || ad == "token-panosu.exe" || strings.HasSuffix(ad, ".sha256.txt") {
			return
		}
		b, err := os.ReadFile(yol)
		if err != nil {
			return
		}
		cs, gs, et, u := metindenAdayCikar(string(b), haric64)
		if et != "" && genelEtiket == "" {
			genelEtiket = et
		}
		if u != "" && genelURL == "" {
			genelURL = u
		}
		for _, c := range cs {
			if g, err := anahtarOku(c); err == nil {
				adayEkle(c, strings.ToLower(strings.TrimSpace(g)), et, u)
			}
			for _, g := range gs {
				adayEkle(c, g, et, u)
			}
		}
		tumCihazlar = benzersizEkle(tumCihazlar, cs...)
		tumGizliler = benzersizEkle(tumGizliler, gs...)
	}

	// 2) ~/.token-panosu içindeki yapılandırma / özel betik dosyaları
	if girisler, err := os.ReadDir(dizin); err == nil {
		for _, g := range girisler {
			if !g.IsDir() {
				dosyaTara(filepath.Join(dizin, g.Name()))
			}
		}
	}

	// 3) macOS LaunchAgents içindeki ilgili plist dosyaları ve işaret ettikleri özel betikler
	if runtime.GOOS == "darwin" || (testModu && testLaunchAgentsDizini != "") {
		laDizin := launchAgentsDizini()
		if girisler, err := os.ReadDir(laDizin); err == nil {
			for _, g := range girisler {
				if g.IsDir() || !strings.HasSuffix(strings.ToLower(g.Name()), ".plist") {
					continue
				}
				pYol := filepath.Join(laDizin, g.Name())
				st, err := os.Stat(pYol)
				if err != nil || st.Size() > 128*1024 {
					continue
				}
				pb, err := os.ReadFile(pYol)
				if err != nil {
					continue
				}
				icerik := string(pb)
				eslesmeler := plistStrRe.FindAllStringSubmatch(icerik, 30)
				alaka := plistBizimAlakaMi(g.Name(), icerik)
				var adayBetikler []string
				for _, m := range eslesmeler {
					for _, y := range satirdanBetikYollariCikar(m[1]) {
						if bb, err := os.ReadFile(y); err == nil {
							if alaka || plistBizimAlakaMi(filepath.Base(y), string(bb)) {
								alaka = true
								adayBetikler = append(adayBetikler, y)
							}
						}
					}
				}
				if !alaka {
					continue
				}
				dosyaTara(pYol)
				for _, betik := range adayBetikler {
					dosyaTara(betik)
					// Betiğin yanındaki olası config/ayar dosyalarını da kontrol et
					bDizin := filepath.Dir(betik)
					for _, ekAd := range []string{"ayar.json", "config.json", "anahtar", ".env"} {
						dosyaTara(filepath.Join(bDizin, ekAd))
					}
				}
			}
		}
	}

	// 4) macOS/Linux crontab içinde özel betik varsa aday olarak tara
	if !testModu && (runtime.GOOS == "darwin" || runtime.GOOS == "linux") {
		if cb, err := exec.Command("crontab", "-l").Output(); err == nil && len(cb) > 0 {
			for _, satir := range strings.Split(string(cb), "\n") {
				s := strings.TrimSpace(satir)
				if s == "" || strings.HasPrefix(s, "#") {
					continue
				}
				satirAlaka := plistBizimAlakaMi("", s)
				for _, parca := range satirdanBetikYollariCikar(s) {
					bb, err := os.ReadFile(parca)
					if err != nil {
						continue
					}
					if satirAlaka || plistBizimAlakaMi(filepath.Base(parca), string(bb)) {
						dosyaTara(parca)
						bDizin := filepath.Dir(parca)
						for _, ekAd := range []string{"ayar.json", "config.json", "anahtar", ".env"} {
							dosyaTara(filepath.Join(bDizin, ekAd))
						}
					}
				}
			}
		}
	}

	// 5) Bellekte arka planda çalışan yabancı Python/kabuk döngüleri varsa betiklerinden aday çıkar
	for _, cs := range calisanYabanciBetikler() {
		dosyaTara(cs.betik)
		bDizin := filepath.Dir(cs.betik)
		for _, ekAd := range []string{"ayar.json", "config.json", "anahtar", ".env"} {
			dosyaTara(filepath.Join(bDizin, ekAd))
		}
	}

	// Aynı klasör veya plist'ten ayrı dosyalarda bulunan cihaz ve gizli anahtarları eşleştir
	for _, c := range tumCihazlar {
		if g, err := anahtarOku(c); err == nil {
			adayEkle(c, strings.ToLower(strings.TrimSpace(g)), genelEtiket, genelURL)
		}
		for _, g := range tumGizliler {
			adayEkle(c, g, genelEtiket, genelURL)
		}
	}
	return adaylar
}

func sha256HexYerel(b []byte) string {
	if len(b) == 0 {
		return ""
	}
	return imzaMetniSha256(b)
}

func betikArsivle(yol string) {
	if yol == "" || strings.ToLower(filepath.Base(yol)) == "kur.sh" {
		return
	}
	yedekDizin := filepath.Join(dizin, "eski-betikler")
	_ = os.MkdirAll(yedekDizin, 0o700)
	hedef := filepath.Join(yedekDizin, filepath.Base(yol)+".devre-disi")
	if err := os.Rename(yol, hedef); err != nil {
		if b, err2 := os.ReadFile(yol); err2 == nil {
			_ = os.WriteFile(hedef, b, 0o600)
			_ = os.Remove(yol)
		}
	}
}

// Eski/özel Python veya kabuk gönderici zamanlayıcılarını ve çalışan döngülerini devre dışı bırakır.
func yabanciZamanlayicilariTemizle() {
	// 0) Arka planda bellekte çalışan yabancı Python/kabuk gönderici süreçlerini durdur ve betiklerini arşivle
	for _, cs := range calisanYabanciBetikler() {
		_ = exec.Command("kill", "-9", strconv.Itoa(cs.pid)).Run()
		betikArsivle(cs.betik)
		logYaz("yabancı çalışan gönderici süreci durduruldu: pid=%d (%s)", cs.pid, filepath.Base(cs.betik))
	}

	// 1) ~/.token-panosu içindeki özel .py / gonderici betiklerini etkisizleştir (veri kaybı olmasın diye yedekle)
	if girisler, err := os.ReadDir(dizin); err == nil {
		for _, g := range girisler {
			if g.IsDir() {
				continue
			}
			ad := g.Name()
			kucuk := strings.ToLower(ad)
			if strings.HasSuffix(kucuk, ".py") || (strings.HasSuffix(kucuk, ".sh") && kucuk != "kur.sh") {
				betikArsivle(filepath.Join(dizin, ad))
			}
		}
	}

	// 2) macOS LaunchAgents içindeki yabancı token-panosu plist'lerini durdur ve kaldır
	if runtime.GOOS == "darwin" || (testModu && testLaunchAgentsDizini != "") {
		laDizin := launchAgentsDizini()
		resmiPlist := ""
		if runtime.GOOS == "darwin" && !testModu {
			resmiPlist = filepath.Join(evDizini(), "Library", "LaunchAgents", "com.tokenpanosu.gonderici.plist")
		}
		girisler, err := os.ReadDir(laDizin)
		if err == nil {
			for _, g := range girisler {
				if g.IsDir() || !strings.HasSuffix(strings.ToLower(g.Name()), ".plist") {
					continue
				}
				pYol := filepath.Join(laDizin, g.Name())
				if resmiPlist != "" && pYol == resmiPlist {
					continue
				}
				st, err := os.Stat(pYol)
				if err != nil || st.Size() > 128*1024 {
					continue
				}
				pb, err := os.ReadFile(pYol)
				if err != nil {
					continue
				}
				icerik := string(pb)
				alaka := plistBizimAlakaMi(g.Name(), icerik)
				var bagliBetikler []string
				for _, m := range plistStrRe.FindAllStringSubmatch(icerik, 30) {
					for _, y := range satirdanBetikYollariCikar(m[1]) {
						if bb, err := os.ReadFile(y); err == nil && (alaka || plistBizimAlakaMi(filepath.Base(y), string(bb))) {
							alaka = true
							bagliBetikler = append(bagliBetikler, y)
						}
					}
				}
				if !alaka {
					continue
				}
				if runtime.GOOS == "darwin" && !testModu {
					alan := fmt.Sprintf("gui/%d", os.Getuid())
					_ = exec.Command("/bin/launchctl", "bootout", alan, pYol).Run()
				}
				_ = os.Remove(pYol)
				for _, y := range bagliBetikler {
					betikArsivle(y)
				}
				logYaz("yabancı zamanlayıcı temizlendi: %s", g.Name())
			}
		}
	}

	// 3) macOS/Linux crontab içindeki yabancı özel gönderici satırlarını temizle
	if !testModu && (runtime.GOOS == "darwin" || runtime.GOOS == "linux") {
		if cb, err := exec.Command("crontab", "-l").Output(); err == nil && len(cb) > 0 {
			satirlar := strings.Split(string(cb), "\n")
			var kalan []string
			degisti := false
			for _, satir := range satirlar {
				s := strings.TrimSpace(satir)
				if s != "" && !strings.HasPrefix(s, "#") {
					alaka := plistBizimAlakaMi("", s)
					var cronBetikler []string
					for _, y := range satirdanBetikYollariCikar(s) {
						if bb, err := os.ReadFile(y); err == nil && (alaka || plistBizimAlakaMi(filepath.Base(y), string(bb))) {
							alaka = true
							cronBetikler = append(cronBetikler, y)
						}
					}
					if alaka {
						resmiLinux := runtime.GOOS == "linux" && strings.Contains(s, "# token-panosu") && strings.Contains(s, ikiliYolu())
						if !resmiLinux {
							degisti = true
							for _, y := range cronBetikler {
								betikArsivle(y)
							}
							logYaz("yabancı crontab satırı temizlendi")
							continue
						}
					}
				}
				kalan = append(kalan, satir)
			}
			if degisti {
				yeniCron := strings.TrimRight(strings.Join(kalan, "\n"), "\n") + "\n"
				c := exec.Command("crontab", "-")
				c.Stdin = strings.NewReader(yeniCron)
				_ = c.Run()
			}
		}
	}
}

func sunucuAdresiCoz(istenen, mevcut string) string {
	adres := strings.TrimRight(strings.TrimSpace(istenen), "/")
	if adres == "" {
		adres = strings.TrimRight(strings.TrimSpace(mevcut), "/")
	}
	if adres == "" {
		adres = VarsayilanURL
	}
	if !testModu && VarsayilanURL != "" && adres != VarsayilanURL && adresGecerli(VarsayilanURL) && EskiSunucuURLler != "" {
		for _, eski := range strings.Split(EskiSunucuURLler, ",") {
			eski = strings.TrimRight(strings.TrimSpace(eski), "/")
			if eski != "" && adres == eski {
				return VarsayilanURL
			}
		}
	}
	return adres
}

// Eşleşme kodu gerektirmeden mevcut kurulumu (veya özel Python/betik kurulumunu) resmî ikiliye taşır ve onarır.
func onarKomutu(b bayraklar) error {
	if b.kod != "" {
		return kurKomutu(b)
	}
	if err := os.MkdirAll(dizin, 0o700); err != nil {
		return err
	}
	_ = os.Chmod(dizin, 0o700)

	adaylar := mevcutAdaylariBul()
	if len(adaylar) == 0 {
		return errors.New("bu bilgisayarda mevcut eşleşme kaydı bulunamadı; panodan kod üretip '--kod XXXXX-XXXXX' ile kurun")
	}

	// Önce eski/yabancı Python döngülerini ve zamanlayıcıları durdur ki yeni gönderimle yarışmasın
	yabanciZamanlayicilariTemizle()
	if err := kendiniYerlestir(); err != nil {
		return fmt.Errorf("program klasöre kopyalanamadı: %v", err)
	}

	mevcutAyar, _ := ayarOku()
	var secilen *onarAday
	var sonAdres string

	for i := range adaylar {
		ad := &adaylar[i]
		adres := sunucuAdresiCoz(b.url, ad.URL)
		if !adresGecerli(adres) {
			continue
		}
		etiket := ad.Etiket
		if etiket == "" {
			etiket = mevcutAyar.Etiket
		}
		if etiket == "" {
			etiket = "Bilgisayar"
		}
		if err := anahtarKaydet(ad.Cihaz, ad.Gizli); err != nil {
			continue
		}
		yeniAyar := mevcutAyar
		yeniAyar.URL = adres
		yeniAyar.Cihaz = ad.Cihaz
		yeniAyar.Etiket = etiket
		if err := ayarYaz(yeniAyar); err != nil {
			continue
		}
		if ok, satir := gonder(); ok {
			secilen = ad
			sonAdres = adres
			fmt.Printf("Onarıldı: %s (sürüm %d, %d satır gönderildi).\n", etiket, Surum, satir)
			break
		}
	}

	if secilen == nil {
		// Ağ geçici kapalıysa ilk geçerli adayı yine de kaydet
		ad := adaylar[0]
		sonAdres = sunucuAdresiCoz(b.url, ad.URL)
		if !adresGecerli(sonAdres) {
			return errors.New("geçerli sunucu adresi bulunamadı; --url https://... belirtin")
		}
		etiket := ad.Etiket
		if etiket == "" {
			etiket = mevcutAyar.Etiket
		}
		if etiket == "" {
			etiket = "Bilgisayar"
		}
		if err := anahtarKaydet(ad.Cihaz, ad.Gizli); err != nil {
			return fmt.Errorf("anahtar saklanamadı: %v", err)
		}
		yeniAyar := mevcutAyar
		yeniAyar.URL = sonAdres
		yeniAyar.Cihaz = ad.Cihaz
		yeniAyar.Etiket = etiket
		if err := ayarYaz(yeniAyar); err != nil {
			return err
		}
		fmt.Printf("Yapılandırma onarıldı (%s); ilk gönderim bir sonraki turda yeniden denenecek.\n", etiket)
	}

	uyariKur()
	agyDurumSatiriKur()
	if testModu {
		return nil
	}
	if err := zamanlayiciKur(ikiliYolu()); err != nil {
		return fmt.Errorf("zamanlayıcı kurulamadı (%v). ÖNEMLİ: Özel Python/kabuk betiği YAZMAYIN; yapay zekâ kum havuzu yerine doğrudan bilgisayarın kendi Terminal uygulamasında şu komutu çalıştırın: curl -fsSL %s/kur.sh | sh", err, sonAdres)
	}
	fmt.Println("Resmî gönderici ve otomatik güncelleyici etkinleştirildi (5 dakikada bir çalışır).")
	return nil
}
