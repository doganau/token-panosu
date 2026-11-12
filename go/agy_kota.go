//go:build sag_gemini || (!sag_claude && !sag_chatgpt)

package main

// Antigravity CLI (agy) resmi durum satırı (statusLine) bağlantısı ve sessiz kota yenileyici.
//
// Belgelendirme: https://antigravity.google/docs/cli/statusline/
// Antigravity 2.0 masaüstü uygulaması kota yüzdelerini artık state.vscdb'ye yazmaz; bunun yerine
// aynı kimlik oturumunu paylaşan resmi `agy` CLI aracı, ~/.gemini/antigravity-cli/settings.json
// içindeki statusLine komutunun stdin'ine kota bilgilerini JSON olarak verir:
//   {"email":"...","plan_tier":"...","quota":{"gemini-weekly":{"remaining_fraction":0.48,"reset_time":"..."}, ...}}
//
// Bu dosya:
//  1. `token-panosu agy-durum-satiri` alt komutuyla stdin'deki resmi JSON'dan yalnızca e-posta ve kota
//     yüzdelerini süzüp ~/.token-panosu/limit_agy.json dosyasına yazar; kullanıcının önceki bir statusLine
//     komutu varsa aynı girdiyi ona iletir, yoksa boş çıktı vererek ("stack_with_default": true sayesinde)
//     agy'nin kendi varsayılan durum satırını hiç bozmadan korur.
//  2. `gonder` sırasında limit_agy.json yoksa veya 15 dakikadan eskiyse, agy ikilisini arka planda kısa
//     ömürlü bir sözde uçbirimde (PTY/ConPTY) başlatır; statusLine tetiklenip kota yazıldığı anda (~1.9 sn,
//     0 token harcaması) Ctrl+D Ctrl+D göndererek temizce kapatır.
//  3. Hiçbir OAuth belirteci, Anahtar Zinciri kaydı veya yerel dil sunucusu portu/CSRF anahtarı okunmaz.

import (
	"bytes"
	"context"
	"encoding/json"
	"math"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"time"
)

const (
	agyDurumAnahtar   = "agy-durum-satiri"
	agyYenileAralikSn = 15 * 60
	agyPtyZamanAsimi  = 8 * time.Second
	testAgyCliDizin   = "TOKEN_PANOSU_AGY_CLI_DIZIN"
)

func agyLimitYolu() string     { return filepath.Join(dizin, "limit_agy.json") }
func sonAgyYenileYolu() string { return filepath.Join(dizin, "son_agy_yenile") }

type agyLimitDosyasi struct {
	T          int64             `json:"t"`
	Eposta     string            `json:"eposta,omitempty"`
	Plan       string            `json:"plan,omitempty"`
	HaftaReset int64             `json:"hafta_reset,omitempty"`
	R          map[string]string `json:"r"`
}

type agyStatusKotaPenceresi struct {
	RemainingFraction *float64 `json:"remaining_fraction"`
	ResetTime         string   `json:"reset_time"`
	ResetInSeconds    *int64   `json:"reset_in_seconds"`
	Disabled          bool     `json:"disabled"`
}

type agyStatusGirdi struct {
	Product  string                             `json:"product"`
	Email    string                             `json:"email"`
	PlanTier string                             `json:"plan_tier"`
	Quota    map[string]*agyStatusKotaPenceresi `json:"quota"`
}

type agyKovaDegeri struct {
	varMi bool
	pct   float64
	reset int64
}

func agyKovaGuncelle(hedef *agyKovaDegeri, pct float64, reset int64) {
	if !hedef.varMi || pct > hedef.pct || (pct == hedef.pct && reset > hedef.reset) {
		hedef.varMi = true
		hedef.pct = pct
		hedef.reset = reset
	}
}

func agyResetZamaniCoz(s string, kalanSn *int64, simdi int64) int64 {
	s = strings.TrimSpace(s)
	if s != "" {
		if t, err := time.Parse(time.RFC3339Nano, s); err == nil && t.Unix() > 0 {
			return t.Unix()
		}
		if t, err := time.Parse(time.RFC3339, s); err == nil && t.Unix() > 0 {
			return t.Unix()
		}
	}
	if kalanSn != nil && *kalanSn > 0 && simdi > 0 {
		return simdi + *kalanSn
	}
	return 0
}

// agyStatusCoz: agy statusLine JSON girdisinden e-posta, kota pencereleri ve Gemini haftalık sıfırlama anını çözer.
func agyStatusCoz(girdi []byte, simdi int64) (agyLimitDosyasi, bool) {
	var ham agyStatusGirdi
	if json.Unmarshal(girdi, &ham) != nil {
		return agyLimitDosyasi{}, false
	}
	cikti := agyLimitDosyasi{
		T:      simdi,
		Eposta: strings.TrimSpace(ham.Email),
		Plan:   strings.TrimSpace(ham.PlanTier),
		R:      map[string]string{},
	}

	var gemW, gem5, ucpW, ucp5, genelW, genel5 agyKovaDegeri
	var gecmisGemWReset, gecmisGenelWReset int64

	for anahtar, p := range ham.Quota {
		if p == nil {
			continue
		}
		k := strings.ToLower(strings.TrimSpace(anahtar))
		if k == "" {
			continue
		}
		resetSn := agyResetZamaniCoz(p.ResetTime, p.ResetInSeconds, simdi)
		if resetSn <= 0 {
			continue
		}

		haftalikMi := strings.Contains(k, "week") || strings.Contains(k, "7d")
		besSaatMi := strings.Contains(k, "5h") || strings.Contains(k, "hour")
		if !haftalikMi && !besSaatMi {
			if simdi > 0 && resetSn-simdi <= 8*3600 {
				besSaatMi = true
			} else {
				haftalikMi = true
			}
		}

		geminiHavuzu := strings.Contains(k, "gemini")
		ucpHavuzu := strings.Contains(k, "3p") || strings.Contains(k, "claude") || strings.Contains(k, "third") ||
			strings.Contains(k, "gpt") || strings.Contains(k, "openai")

		if haftalikMi && (simdi <= 0 || simdi-resetSn <= 14*86400) {
			if geminiHavuzu {
				if resetSn > gecmisGemWReset {
					gecmisGemWReset = resetSn
				}
			} else if !ucpHavuzu && resetSn > gecmisGenelWReset {
				gecmisGenelWReset = resetSn
			}
		}

		// Yalnızca sıfırlanma zamanı henüz geçmemiş (aktif) pencerelerin kullanım yüzdesini raporla
		if simdi > 0 && resetSn <= simdi {
			continue
		}

		var kalan float64
		switch {
		case p.Disabled:
			kalan = 0
		case p.RemainingFraction != nil:
			kalan = *p.RemainingFraction
		default:
			// Protobuf JSON serileştirmesinde kota tamamen bittiğinde remaining_fraction: 0 alanı atlanabilir
			kalan = 0
		}
		if math.IsNaN(kalan) || math.IsInf(kalan, 0) {
			continue
		}
		if kalan < 0 {
			kalan = 0
		} else if kalan > 1 {
			kalan = 1
		}
		pct := math.Round((1.0-kalan)*1000) / 10
		if pct < 0 {
			pct = 0
		} else if pct > 100 {
			pct = 100
		}

		if haftalikMi {
			switch {
			case geminiHavuzu:
				agyKovaGuncelle(&gemW, pct, resetSn)
			case ucpHavuzu:
				agyKovaGuncelle(&ucpW, pct, resetSn)
			default:
				agyKovaGuncelle(&genelW, pct, resetSn)
			}
		} else if besSaatMi {
			switch {
			case geminiHavuzu:
				agyKovaGuncelle(&gem5, pct, resetSn)
			case ucpHavuzu:
				agyKovaGuncelle(&ucp5, pct, resetSn)
			default:
				agyKovaGuncelle(&genel5, pct, resetSn)
			}
		}
	}

	// Ana Gemini ofisi haftalık kotası ve haftalık sıfırlama saati: YALNIZCA Gemini / genel havuz!
	// Claude ve GPT (3P) havuzu ayrı özel limittir (Claude'daki Fable gibi); Gemini haftalık kotasına veya sıfırlama gününe asla düşmez.
	anaW := gemW
	if !anaW.varMi && genelW.varMi {
		anaW = genelW
	}
	if anaW.varMi {
		cikti.R["seven_day.used_percentage"] = strconv.FormatFloat(anaW.pct, 'f', -1, 64)
		cikti.R["seven_day.resets_at"] = strconv.FormatInt(anaW.reset, 10)
		cikti.HaftaReset = sonrakiHaftaReset(anaW.reset, simdi)
	} else if gecmisGemWReset > 0 {
		cikti.HaftaReset = sonrakiHaftaReset(gecmisGemWReset, simdi)
	} else if gecmisGenelWReset > 0 {
		cikti.HaftaReset = sonrakiHaftaReset(gecmisGenelWReset, simdi)
	}

	ana5 := gem5
	if !ana5.varMi && genel5.varMi {
		ana5 = genel5
	}
	if ana5.varMi {
		cikti.R["five_hour.used_percentage"] = strconv.FormatFloat(ana5.pct, 'f', -1, 64)
		cikti.R["five_hour.resets_at"] = strconv.FormatInt(ana5.reset, 10)
	}

	if gemW.varMi {
		cikti.R["agy_gemini.used_percentage"] = strconv.FormatFloat(gemW.pct, 'f', -1, 64)
		cikti.R["agy_gemini.resets_at"] = strconv.FormatInt(gemW.reset, 10)
	}
	if gem5.varMi {
		cikti.R["agy_gemini_5h.used_percentage"] = strconv.FormatFloat(gem5.pct, 'f', -1, 64)
		cikti.R["agy_gemini_5h.resets_at"] = strconv.FormatInt(gem5.reset, 10)
	}

	if ucpW.varMi {
		cikti.R["agy_claude.used_percentage"] = strconv.FormatFloat(ucpW.pct, 'f', -1, 64)
		cikti.R["agy_claude.resets_at"] = strconv.FormatInt(ucpW.reset, 10)
	}
	if ucp5.varMi {
		cikti.R["agy_claude_5h.used_percentage"] = strconv.FormatFloat(ucp5.pct, 'f', -1, 64)
		cikti.R["agy_claude_5h.resets_at"] = strconv.FormatInt(ucp5.reset, 10)
	}

	return cikti, cikti.Eposta != "" || len(cikti.R) > 0 || cikti.HaftaReset > 0
}

func agyLimitYaz(l agyLimitDosyasi) {
	b, err := json.Marshal(l)
	if err != nil {
		return
	}
	_ = os.MkdirAll(dizin, 0o700)
	_ = dosyaYaz(agyLimitYolu(), b, 0o600)
}

func agyLimitOku() (agyLimitDosyasi, bool) {
	var l agyLimitDosyasi
	b, err := os.ReadFile(agyLimitYolu())
	if err != nil || json.Unmarshal(b, &l) != nil || l.T <= 0 {
		return agyLimitDosyasi{}, false
	}
	if l.Eposta == "" && len(l.R) == 0 && l.HaftaReset <= 0 {
		return agyLimitDosyasi{}, false
	}
	return l, true
}

// agyLimitAktifFiltrele: diskteki limit_agy.json kaydında süresi dolmuş veya eski sürümden kalan
// çapraz havuz (Claude/GPT -> seven_day, 5s -> 7g) kirlenmelerini temizler.
func agyLimitAktifFiltrele(l agyLimitDosyasi, simdi int64) map[string]string {
	if len(l.R) == 0 {
		return nil
	}
	if simdi > 0 && simdi-l.T > 7*86400 {
		return nil
	}
	temiz := map[string]string{}
	pencereler := []string{"seven_day", "five_hour", "agy_gemini", "agy_gemini_5h", "agy_claude", "agy_claude_5h"}
	for _, on := range pencereler {
		pStr, pOk := l.R[on+".used_percentage"]
		rStr, rOk := l.R[on+".resets_at"]
		if !pOk {
			continue
		}
		if rOk {
			resetSn, err := strconv.ParseInt(rStr, 10, 64)
			if err != nil || (simdi > 0 && resetSn <= simdi) {
				continue
			}
			// Haftalık pencerelere 5 saatlik pencerenin sızmasını engelle
			if (on == "seven_day" || on == "agy_gemini" || on == "agy_claude") && l.T > 0 && resetSn <= l.T+8*3600 {
				continue
			}
			temiz[on+".resets_at"] = rStr
		}
		temiz[on+".used_percentage"] = pStr
	}
	if temiz["agy_gemini.resets_at"] != "" && temiz["agy_gemini.resets_at"] == temiz["agy_gemini_5h.resets_at"] {
		delete(temiz, "agy_gemini.used_percentage")
		delete(temiz, "agy_gemini.resets_at")
	}
	if temiz["agy_claude.resets_at"] != "" && temiz["agy_claude.resets_at"] == temiz["agy_claude_5h.resets_at"] {
		delete(temiz, "agy_claude.used_percentage")
		delete(temiz, "agy_claude.resets_at")
	}
	// Eski sürümlerde Gemini haftalık yokken Claude/GPT haftalık (agy_claude) seven_day alanına yazılabiliyordu
	if cRes := l.R["agy_claude.resets_at"]; cRes != "" && temiz["seven_day.resets_at"] == cRes && temiz["agy_gemini.resets_at"] != cRes {
		delete(temiz, "seven_day.used_percentage")
		delete(temiz, "seven_day.resets_at")
	}
	return temiz
}

// agyHaftaResetTemizle: limit_agy.json içindeki HaftaReset alanının Claude/GPT (3P) penceresinden gelmediğini doğrular.
func agyHaftaResetTemizle(l agyLimitDosyasi, simdi int64) int64 {
	if l.HaftaReset <= 0 || (simdi > 0 && simdi-l.T > 14*86400) {
		return 0
	}
	hr := sonrakiHaftaReset(l.HaftaReset, simdi)
	if cResStr := l.R["agy_claude.resets_at"]; cResStr != "" && l.R["agy_gemini.resets_at"] != cResStr {
		if cRes, err := strconv.ParseInt(cResStr, 10, 64); err == nil && cRes > 0 {
			cr := sonrakiHaftaReset(cRes, simdi)
			fark := hr - cr
			if fark < 0 {
				fark = -fark
			}
			if fark < 600 {
				return 0
			}
		}
	}
	return hr
}

// Gizli komut: agy statusLine tarafından çağrılır; her zaman 0 ile çıkar, hata basmaz.
func agyDurumSatiri() {
	defer func() { _ = recover() }()
	girdi, ok := stdinOku()
	if !ok || len(bytes.TrimSpace(girdi)) == 0 {
		return
	}
	simdi := time.Now().Unix()
	if cozulmus, gecerli := agyStatusCoz(girdi, simdi); gecerli {
		// agy açılışta ilk karede kota henüz gelmeden yalnızca e-posta gönderebilir;
		// elimizde aynı hesaba ait dolu kota varken boş haritayla ezmeyelim.
		if len(cozulmus.R) == 0 && cozulmus.HaftaReset == 0 {
			if eski, varEski := agyLimitOku(); varEski && (len(eski.R) > 0 || eski.HaftaReset > 0) &&
				(cozulmus.Eposta == "" || eski.Eposta == "" || strings.EqualFold(eski.Eposta, cozulmus.Eposta)) {
				cozulmus = agyLimitDosyasi{}
			}
		}
		if cozulmus.Eposta != "" || len(cozulmus.R) > 0 || cozulmus.HaftaReset > 0 {
			agyLimitYaz(cozulmus)
		}
	}
	onceki := agyOncekiKomut(varsayilanAgyCliDizini())
	if onceki == "" {
		return
	}
	ctx, iptal := context.WithTimeout(context.Background(), oncekiSure)
	defer iptal()
	c := kabukKomutu(ctx, onceki)
	c.Stdin = bytes.NewReader(girdi)
	var cikti bytes.Buffer
	c.Stdout = &cikti
	if c.Run() == nil || cikti.Len() > 0 {
		_, _ = os.Stdout.Write(cikti.Bytes())
	}
}

// ---------- ~/.gemini/antigravity-cli/settings.json kurulumu ve geri yüklemesi ----------

func varsayilanAgyCliDizini() string {
	if testModu {
		if t := os.Getenv(testAgyCliDizin); t != "" {
			return dizinAnahtari(t)
		}
		return dizinAnahtari(filepath.Join(dizin, "antigravity-cli"))
	}
	return dizinAnahtari(filepath.Join(evDizini(), ".gemini", "antigravity-cli"))
}

func agyAyarOnceki(m map[string]json.RawMessage) map[string]json.RawMessage {
	o := map[string]json.RawMessage{}
	if r, ok := m["agy_durum_onceki"]; ok {
		_ = json.Unmarshal(r, &o)
	}
	return o
}

func agyAyarKuruldu(m map[string]json.RawMessage) []string {
	var l []string
	if r, ok := m["agy_durum_kuruldu"]; ok {
		_ = json.Unmarshal(r, &l)
	}
	return l
}

func agyOncekiKomut(dir string) string {
	r, ok := agyAyarOnceki(ayarHam())[dir]
	if !ok || hamBos(r) {
		return ""
	}
	var s struct {
		Command string `json:"command"`
	}
	if json.Unmarshal(r, &s) != nil || strings.Contains(s.Command, agyDurumAnahtar) {
		return ""
	}
	return s.Command
}

func agyKuruldaIsaretle(ham map[string]json.RawMessage, d string) error {
	l := agyAyarKuruldu(ham)
	if listedeVar(l, d) {
		return nil
	}
	l = append(l, d)
	b, _ := json.Marshal(l)
	ham["agy_durum_kuruldu"] = b
	return ayarHamYaz(ham)
}

func agyCliDizinleri() []string {
	if testModu {
		if t := os.Getenv(testAgyCliDizin); t != "" {
			k := dizinAnahtari(t)
			if yerelYolMu(k) {
				return []string{k}
			}
		}
		return nil
	}
	d := varsayilanAgyCliDizini()
	if !yerelYolMu(d) {
		return nil
	}
	if klasorMu(d) || agyIkiliBul() != "" {
		return []string{d}
	}
	return nil
}

func agyDurumSatiriKurDizin(d string) error {
	if !yerelYolMu(d) {
		return os.ErrPermission
	}
	if err := os.MkdirAll(d, 0o700); err != nil {
		return err
	}
	yol := filepath.Join(d, "settings.json")
	m, var_, err := ayarlarOku(yol)
	if err != nil {
		return err
	}
	exe, err := filepath.Abs(ikiliYolu())
	if err != nil {
		return err
	}
	if _, errS := os.Stat(exe); errS != nil {
		if calisan, errE := os.Executable(); errE == nil && calisan != "" {
			if absC, errA := filepath.Abs(calisan); errA == nil {
				exe = absC
			}
		}
	}
	hedefKomut := komutTirnakla(exe) + " " + agyDurumAnahtar
	ham := ayarHam()

	if sl, ok := m["statusLine"]; ok && strings.Contains(string(sl), agyDurumAnahtar) {
		var mevcut map[string]any
		if json.Unmarshal(sl, &mevcut) == nil {
			komut, _ := mevcut["command"].(string)
			_, stackOk := mevcut["stack_with_default"].(bool)
			if komut != hedefKomut || !stackOk {
				mevcut["type"] = "command"
				mevcut["command"] = hedefKomut
				if !stackOk {
					mevcut["stack_with_default"] = true
				}
				if yb, errM := json.Marshal(mevcut); errM == nil {
					m["statusLine"] = yb
					_ = ayarlarYaz(yol, m)
				}
			}
		}
		return agyKuruldaIsaretle(ham, d)
	}

	onceki := agyAyarOnceki(ham)
	if sl, ok := m["statusLine"]; ok {
		onceki[d] = append(json.RawMessage(nil), sl...)
	} else {
		onceki[d] = json.RawMessage("null")
	}
	ob, _ := json.Marshal(onceki)
	ham["agy_durum_onceki"] = ob
	if err := ayarHamYaz(ham); err != nil {
		return err
	}
	if var_ {
		if eski, errR := os.ReadFile(yol); errR == nil {
			_ = dosyaYaz(yol+yedekEki, eski, 0o600)
		}
	}

	yeni := map[string]any{
		"type":               "command",
		"command":            hedefKomut,
		"stack_with_default": true,
	}
	if sl, ok := m["statusLine"]; ok {
		var o map[string]json.RawMessage
		if json.Unmarshal(sl, &o) == nil {
			if swd, varSwd := o["stack_with_default"]; varSwd {
				yeni["stack_with_default"] = swd
			}
			if p, varP := o["padding"]; varP {
				yeni["padding"] = p
			}
		}
	}
	yb, _ := json.Marshal(yeni)
	m["statusLine"] = yb
	if err := ayarlarYaz(yol, m); err != nil {
		return err
	}
	return agyKuruldaIsaretle(ham, d)
}

func agyDurumSatiriKur() {
	defer func() {
		if r := recover(); r != nil {
			logYaz("agy durum satırı kurulumu çöktü: %v", r)
		}
	}()
	for _, d := range agyCliDizinleri() {
		if err := agyDurumSatiriKurDizin(d); err != nil {
			logYaz("agy durum satırı kurulamadı (%s): %v", filepath.Base(d), err)
		}
	}
}

func agyDurumSatiriKaldir() {
	defer func() { _ = recover() }()
	ham := ayarHam()
	onceki := agyAyarOnceki(ham)
	adaylar := agyAyarKuruldu(ham)
	varsayilan := varsayilanAgyCliDizini()
	if !listedeVar(adaylar, varsayilan) && klasorMu(varsayilan) {
		adaylar = append(adaylar, varsayilan)
	}
	for _, d := range adaylar {
		if !yerelYolMu(d) {
			continue
		}
		yol := filepath.Join(d, "settings.json")
		m, _, err := ayarlarOku(yol)
		if err != nil {
			continue
		}
		sl, ok := m["statusLine"]
		if !ok || !strings.Contains(string(sl), agyDurumAnahtar) {
			continue
		}
		if r, ok := onceki[d]; ok && !hamBos(r) {
			m["statusLine"] = r
		} else {
			delete(m, "statusLine")
		}
		_ = ayarlarYaz(yol, m)
	}
}

// ---------- agy ikili bulucu (ağ yollarına dokunmadan) ve sessiz PTY kota yenilemesi ----------

func agyIkiliBul() string {
	ev := evDizini()
	var adaylar []string
	ikiliAd := "agy"
	if runtime.GOOS == "windows" {
		ikiliAd = "agy.exe"
	}
	// PATH dizinlerini yerelYolMu süzgecinden geçirerek tara (exec.LookPath /home autofs yollarına dokunmasın)
	for _, dir := range filepath.SplitList(os.Getenv("PATH")) {
		dir = strings.TrimSpace(dir)
		if dir == "" || !yerelYolMu(dir) {
			continue
		}
		adaylar = append(adaylar, filepath.Join(dir, ikiliAd))
	}
	if runtime.GOOS == "windows" {
		if la := os.Getenv("LOCALAPPDATA"); la != "" {
			adaylar = append(adaylar,
				filepath.Join(la, "agy", "bin", "agy.exe"),
				filepath.Join(la, "Programs", "Antigravity", "bin", "agy.exe"),
				filepath.Join(la, "Programs", "Antigravity", "agy.exe"),
				filepath.Join(la, "antigravity-cli", "bin", "agy.exe"),
			)
		}
		adaylar = append(adaylar,
			filepath.Join(ev, "AppData", "Local", "agy", "bin", "agy.exe"),
			filepath.Join(ev, ".local", "bin", "agy.exe"),
		)
	} else {
		adaylar = append(adaylar,
			filepath.Join(ev, ".local", "bin", "agy"),
			"/opt/homebrew/bin/agy",
			"/usr/local/bin/agy",
			"/usr/bin/agy",
		)
	}
	for _, y := range adaylar {
		if y == "" || !yerelYolMu(y) {
			continue
		}
		st, err := os.Stat(y)
		if err != nil || !st.Mode().IsRegular() || st.Size() <= 0 {
			continue
		}
		if runtime.GOOS != "windows" && st.Mode()&0o111 == 0 {
			continue
		}
		return y
	}
	return ""
}

func agyOrtam() []string {
	mevcut := os.Environ()
	sonuc := make([]string, 0, len(mevcut)+3)
	termVar, evVar := false, false
	for _, kv := range mevcut {
		ust := strings.ToUpper(kv)
		if strings.HasPrefix(ust, "TERM=") {
			deger := strings.TrimSpace(kv[5:])
			if deger == "" || deger == "dumb" {
				sonuc = append(sonuc, "TERM=xterm-256color")
			} else {
				sonuc = append(sonuc, kv)
			}
			termVar = true
			continue
		}
		if strings.HasPrefix(ust, "HOME=") {
			evVar = true
		}
		sonuc = append(sonuc, kv)
	}
	if !termVar {
		sonuc = append(sonuc, "TERM=xterm-256color")
	}
	if !evVar {
		sonuc = append(sonuc, "HOME="+evDizini())
	}
	sonuc = append(sonuc, "NO_COLOR=1")
	return sonuc
}

func sonAgyYenileT() int64 {
	b, err := os.ReadFile(sonAgyYenileYolu())
	if err != nil {
		return 0
	}
	n, _ := strconv.ParseInt(strings.TrimSpace(string(b)), 10, 64)
	return n
}

// agyKotaYenile: limit_agy.json 15 dakikadan eskiyse agy CLI'ı kısa ömürlü bir PTY'de başlatıp
// statusLine üzerinden kota yüzdelerinin yazılmasını bekler ve Ctrl+D ile anında çıkar.
func agyKotaYenile(simdi int64) {
	if testModu {
		return
	}
	defer func() {
		if r := recover(); r != nil {
			logYaz("agy kota yenileme hatası: %v", r)
		}
	}()
	var oncekiDoluT int64
	if mevcut, ok := agyLimitOku(); ok && len(mevcut.R) > 0 {
		if simdi-mevcut.T < agyYenileAralikSn {
			return
		}
		oncekiDoluT = mevcut.T
	}
	if simdi-sonAgyYenileT() < agyYenileAralikSn {
		return
	}
	agyYol := agyIkiliBul()
	if agyYol == "" {
		return
	}
	if err := agyDurumSatiriKurDizin(varsayilanAgyCliDizini()); err != nil {
		return
	}
	_ = os.MkdirAll(dizin, 0o700)
	_ = os.WriteFile(sonAgyYenileYolu(), []byte(strconv.FormatInt(simdi, 10)), 0o600)

	baslangic := time.Now().Unix() - 1

	agyPtyCalistir(agyYol, dizin, agyPtyZamanAsimi, func() bool {
		guncel, ok := agyLimitOku()
		return ok && guncel.T > oncekiDoluT && guncel.T >= baslangic && (len(guncel.R) > 0 || guncel.HaftaReset > 0)
	})
}
