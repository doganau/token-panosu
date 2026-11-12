//go:build sag_gemini || (!sag_claude && !sag_chatgpt)

package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"time"
)

const (
	sagGeminiEtkin = true
	sagGeminiAd    = "gemini"
)

func geminiModelAd(ham string) string {
	m := modelTemizle(ham)
	if m == "" {
		return "gemini-2.5-pro"
	}
	if !strings.HasPrefix(m, "gemini-") {
		m = "gemini-" + m
	}
	if len(m) > 60 {
		m = m[:60]
	}
	return m
}

// geminiHesapLimitVeResetOku: ~/.gemini/google_accounts.json (Gemini CLI), Antigravity app_storage.json ve
// Antigravity / Antigravity IDE state.vscdb kaynakları arasından EN YENİ mtime'lı aktif hesabı seçer;
// aktif hesapla eşleşen taze Antigravity kota yüzdeleri varsa onları da döndürür.
func geminiHesapLimitVeResetOku(simdi int64) (string, string, limitDosyasi, bool, int64) {
	ev := evDizini()
	adayYollar := []string{}
	if testModu {
		adayYollar = append(adayYollar, filepath.Join(dizin, "google_accounts.json"))
	}
	if gh := os.Getenv("GEMINI_CLI_HOME"); gh != "" {
		adayYollar = append(adayYollar,
			filepath.Join(genislet(gh), ".gemini", "google_accounts.json"),
			filepath.Join(genislet(gh), "google_accounts.json"),
		)
	}
	adayYollar = append(adayYollar, filepath.Join(ev, ".gemini", "google_accounts.json"))

	var enYeniHesap string
	var enYeniMt int64
	for _, yol := range adayYollar {
		if !yerelYolMu(yol) {
			continue
		}
		st, err := os.Stat(yol)
		if err != nil || !st.Mode().IsRegular() {
			continue
		}
		b, err := os.ReadFile(yol)
		if err != nil {
			continue
		}
		var g struct {
			Active string `json:"active"`
		}
		if json.Unmarshal(b, &g) == nil {
			ak := strings.TrimSpace(g.Active)
			if ak != "" && st.ModTime().UnixNano() >= enYeniMt {
				enYeniHesap = ak
				enYeniMt = st.ModTime().UnixNano()
			}
		}
	}

	agyAdaylar := agyDurumAdaylariOku(simdi)
	for _, a := range agyAdaylar {
		if a.eposta != "" && a.mtNano >= enYeniMt {
			enYeniHesap = a.eposta
			enYeniMt = a.mtNano
		}
	}

	var enIyiLimitT int64
	var enIyiLimitR map[string]string
	var enIyiResetMt int64
	var enIyiHaftaReset int64
	for _, a := range agyAdaylar {
		if enYeniHesap != "" && a.eposta != "" && !strings.EqualFold(a.eposta, enYeniHesap) {
			continue
		}
		if a.haftaReset > 0 && a.mtNano >= enIyiResetMt {
			enIyiResetMt = a.mtNano
			enIyiHaftaReset = a.haftaReset
		}
		if a.limitT <= 0 || len(a.limitR) == 0 {
			continue
		}
		if a.limitT >= enIyiLimitT {
			enIyiLimitT = a.limitT
			enIyiLimitR = a.limitR
		}
	}

	hesapHex := hesapOzeti(sagGeminiAd, enYeniHesap)
	temizEposta := epostaTemizle(enYeniHesap)
	if enIyiLimitT > 0 && len(enIyiLimitR) > 0 && enIyiLimitT > sonLimitSaglayiciT(sagGeminiAd) {
		numHarita := make(map[string]json.Number, len(enIyiLimitR))
		for k, v := range enIyiLimitR {
			numHarita[k] = json.Number(v)
		}
		if sinirli := limitSinirla(numHarita); len(sinirli) > 0 {
			return hesapHex, temizEposta, limitDosyasi{T: enIyiLimitT, R: sinirli}, true, enIyiHaftaReset
		}
	}
	return hesapHex, temizEposta, limitDosyasi{}, false, enIyiHaftaReset
}

func geminiHesapVeLimitOku(simdi int64) (string, string, limitDosyasi, bool) {
	h, ep, lim, ok, _ := geminiHesapLimitVeResetOku(simdi)
	return h, ep, lim, ok
}

func geminiHesapOku() string {
	h, _, _, _ := geminiHesapVeLimitOku(0)
	return h
}

type geminiMesajJSON struct {
	SessionID string `json:"sessionId"`
	Timestamp string `json:"timestamp"`
	Type      string `json:"type"`
	Model     string `json:"model"`
	Tokens    *struct {
		Input    int64 `json:"input"`
		Output   int64 `json:"output"`
		Cached   int64 `json:"cached"`
		Thoughts int64 `json:"thoughts"`
		Tool     int64 `json:"tool"`
	} `json:"tokens"`
}

type geminiOturumJSON struct {
	SessionID string            `json:"sessionId"`
	Messages  []geminiMesajJSON `json:"messages"`
}

func geminiKokleri() []string {
	ev := evDizini()
	var liste []string
	gorulen := map[string]bool{}
	ekle := func(y string) {
		if y == "" || !klasorMu(y) {
			return
		}
		g := gercekYol(y)
		if gorulen[g] {
			return
		}
		gorulen[g] = true
		liste = append(liste, y)
	}
	if gh := os.Getenv("GEMINI_CLI_HOME"); gh != "" {
		ekle(filepath.Join(genislet(gh), ".gemini", "tmp"))
		ekle(filepath.Join(genislet(gh), "tmp"))
	}
	ekle(filepath.Join(ev, ".gemini", "tmp"))
	return liste
}

func geminiKaynaklari() []string { return geminiKokleri() }

func geminiDosyaOku(yol string, st fs.FileInfo, eski *digerDosyaKaydi) (*digerDosyaKaydi, bool) {
	boyut, mt := st.Size(), st.ModTime().UnixNano()
	if eski != nil && eski.Boyut == boyut && eski.Mtime == mt {
		eski.Saglayici = sagGeminiAd
		return eski, false
	}
	if boyut <= 0 || boyut > 32<<20 || !yerelYolMu(yol) {
		return nil, false
	}
	b, err := os.ReadFile(yol)
	if err != nil {
		return nil, false
	}
	var ot geminiOturumJSON
	if strings.HasSuffix(yol, ".jsonl") {
		for _, satir := range bytes.Split(b, []byte{'\n'}) {
			satir = bytes.TrimSpace(satir)
			if len(satir) == 0 {
				continue
			}
			var sarmal geminiOturumJSON
			if bytes.Contains(satir, []byte(`"messages"`)) && json.Unmarshal(satir, &sarmal) == nil && len(sarmal.Messages) > 0 {
				if ot.SessionID == "" && strings.TrimSpace(sarmal.SessionID) != "" {
					ot.SessionID = strings.TrimSpace(sarmal.SessionID)
				}
				ot.Messages = append(ot.Messages, sarmal.Messages...)
				continue
			}
			var m geminiMesajJSON
			if json.Unmarshal(satir, &m) == nil {
				if ot.SessionID == "" && strings.TrimSpace(m.SessionID) != "" {
					ot.SessionID = strings.TrimSpace(m.SessionID)
				}
				ot.Messages = append(ot.Messages, m)
			}
		}
		if ot.SessionID == "" && len(ot.Messages) == 0 {
			return nil, false
		}
	} else if json.Unmarshal(b, &ot) != nil {
		return nil, false
	}
	kimlik := strings.TrimSpace(ot.SessionID)
	if kimlik == "" {
		kimlik = strings.TrimSuffix(strings.TrimSuffix(filepath.Base(yol), ".jsonl"), ".json")
	}
	d := &digerDosyaKaydi{
		Saglayici: sagGeminiAd,
		Boyut:     boyut,
		Mtime:     mt,
		Sid:       digerSid(sagGeminiAd, kimlik),
	}
	for _, m := range ot.Messages {
		if m.Tokens == nil || (m.Type != "gemini" && m.Type != "model" && m.Type != "assistant") {
			continue
		}
		t, err := time.Parse(time.RFC3339Nano, m.Timestamp)
		if err != nil {
			continue
		}
		girdi := m.Tokens.Input - m.Tokens.Cached
		if girdi < 0 {
			girdi = 0
		}
		cikti := max(0, m.Tokens.Output) + max(0, m.Tokens.Thoughts) + max(0, m.Tokens.Tool)
		okuma := max(0, m.Tokens.Cached)
		if girdi == 0 && cikti == 0 && okuma == 0 {
			continue
		}
		d.Kayitlar = append(d.Kayitlar, digerKayit{
			T:     t.Unix(),
			Model: geminiModelAd(m.Model),
			In:    girdi,
			Out:   cikti,
			Cr:    okuma,
		})
	}
	return d, true
}

func geminiSaglayiciKaydiMi(yol string, d *digerDosyaKaydi) bool {
	if d == nil {
		return false
	}
	if d.Saglayici != "" {
		return d.Saglayici == sagGeminiAd
	}
	if d.SonModel != "" {
		return true
	}
	y := strings.ToLower(filepath.ToSlash(yol))
	return strings.Contains(y, "/.gemini/") || strings.Contains(y, "antigravity")
}

func geminiEsikAnahtarMi(k string) bool {
	return strings.HasPrefix(k, "gemini-") || strings.HasPrefix(k, "agy-")
}

func geminiEsikAltModel(model string) string {
	if strings.HasPrefix(model, "agy-") && len(model) > 4 {
		return model[4:]
	}
	return ""
}

func geminiOzelAltKomut(arg string) bool {
	if arg == agyDurumAnahtar {
		agyDurumSatiri()
		return true
	}
	return false
}

func geminiKancaKur()              { agyDurumSatiriKur() }
func geminiKancaKaldir()           { agyDurumSatiriKaldir() }
func geminiKotaYenile(simdi int64) { agyKotaYenile(simdi) }

func geminiOrtamKontrol(ekBulgu func(kod, seviye, metin string)) {
	ev := evDizini()
	evGercek := gercekYol(ev)
	geminiYollar := []string{
		filepath.Join(ev, ".gemini"),
		filepath.Join(ev, ".gemini", "tmp"),
	}
	if gh := strings.TrimSpace(os.Getenv("GEMINI_CLI_HOME")); gh != "" {
		ghGen := genislet(gh)
		geminiYollar = append(geminiYollar, ghGen, filepath.Join(ghGen, ".gemini", "tmp"), filepath.Join(ghGen, "tmp"))
		ghGercek := gercekYol(ghGen)
		if ghGercek != gercekYol(filepath.Join(ev, ".gemini")) && ghGercek != evGercek {
			ekBulgu("config_dir_gemini", "d", "GEMINI_CLI_HOME ayarlı: Gemini kayıtları farklı bir klasörde (o da sayılıyor)")
		}
	}
	for _, p := range geminiYollar {
		if yolEngelMi(p) {
			ekBulgu("engel_gemini", "s", "Gemini kayıt klasörü okunamıyor ya da yönlendirilmiş: kullanım gizleniyor olabilir")
			break
		}
	}
	for _, ad := range []string{"GOOGLE_GEMINI_BASE_URL", "GEMINI_API_BASE_URL"} {
		if v := strings.ToLower(strings.TrimRight(strings.TrimSpace(os.Getenv(ad)), "/")); v != "" {
			if !strings.HasPrefix(v, "https://generativelanguage.googleapis.com") && !strings.HasPrefix(v, "https://aiplatform.googleapis.com") {
				ekBulgu("api_ortam_gemini", "s", ad+" ayarlı: kullanım farklı bir API adresine yönlendirilmiş olabilir")
				break
			}
		}
	}
	if strings.TrimSpace(os.Getenv("GEMINI_API_KEY")) != "" || strings.TrimSpace(os.Getenv("GOOGLE_API_KEY")) != "" {
		ekBulgu("api_key_gemini", "d", "GEMINI_API_KEY tanımlı: Gemini CLI abonelik yerine API anahtarı kullanıyor olabilir")
	}
}

func geminiTopla(simdi, sinir int64, onceki, yeniDosyalar map[string]*digerDosyaKaydi, s *digerSonuc, ekBulgu func(kod, seviye, metin string)) []*digerDosyaKaydi {
	geminiOrtamKontrol(ekBulgu)

	var geminiDosyalar []*digerDosyaKaydi
	var kuculenGemini, engelGemini bool

	for _, kok := range geminiKokleri() {
		if !yerelYolMu(kok) {
			continue
		}
		_ = filepath.WalkDir(kok, func(yol string, e fs.DirEntry, err error) error {
			if !yerelYolMu(yol) {
				if e != nil && e.IsDir() && yol != kok {
					return fs.SkipDir
				}
				return nil
			}
			if err != nil {
				if errors.Is(err, fs.ErrPermission) {
					engelGemini = true
				}
				return nil
			}
			if e == nil || e.IsDir() {
				return nil
			}
			ad := e.Name()
			if !strings.HasPrefix(ad, "session-") || (!strings.HasSuffix(ad, ".json") && !strings.HasSuffix(ad, ".jsonl")) {
				return nil
			}
			if dosyaEngelMi(yol) {
				engelGemini = true
				return nil
			}
			st, err := os.Stat(yol)
			if err != nil || !st.Mode().IsRegular() {
				return nil
			}
			if float64(st.ModTime().UnixNano())/1e9 < float64(sinir) {
				return nil
			}
			if _, var_ := yeniDosyalar[yol]; var_ {
				return nil
			}
			eski := onceki[yol]
			var eskiTok int64
			if eski != nil {
				for _, k := range eski.Kayitlar {
					if k.T >= sinir {
						eskiTok += k.In + k.Out + k.W5 + k.Cr
					}
				}
			}
			d, okundu := geminiDosyaOku(yol, st, eski)
			if d == nil {
				return nil
			}
			if okundu {
				s.okunan++
			}
			var yeniTok int64
			for _, k := range d.Kayitlar {
				if k.T >= sinir {
					yeniTok += k.In + k.Out + k.W5 + k.Cr
				}
			}
			if eskiTok-yeniTok >= 1000 {
				kuculenGemini = true
			}
			yeniDosyalar[yol] = d
			geminiDosyalar = append(geminiDosyalar, d)
			return nil
		})
	}

	agyDosyalar, agyOkunan, agyKuculen, agyEngel := antigravityTopla(onceki, yeniDosyalar, sinir)
	s.okunan += agyOkunan
	if agyKuculen {
		kuculenGemini = true
	}
	if agyEngel {
		engelGemini = true
	}

	tumDosyalar := make([]*digerDosyaKaydi, 0, len(geminiDosyalar)+len(agyDosyalar))
	tumDosyalar = append(tumDosyalar, geminiDosyalar...)
	tumDosyalar = append(tumDosyalar, agyDosyalar...)

	aktifSidler := make(map[string]bool, len(tumDosyalar))
	for _, d := range tumDosyalar {
		if d != nil && d.Sid != "" {
			aktifSidler[d.Sid] = true
		}
	}

	var silinenGemini int64
	yediGunOnce := simdi - 7*86400
	for yol, eskiD := range onceki {
		if _, var_ := yeniDosyalar[yol]; var_ || eskiD == nil || !geminiSaglayiciKaydiMi(yol, eskiD) || !yerelYolMu(yol) {
			continue
		}
		if eskiD.Sid != "" && aktifSidler[eskiD.Sid] {
			continue
		}
		if _, err := os.Stat(yol); !errors.Is(err, fs.ErrNotExist) {
			continue
		}
		for _, k := range eskiD.Kayitlar {
			if k.T >= yediGunOnce {
				silinenGemini += k.In + k.Out + k.W5 + k.Cr
			}
		}
	}

	var gelecekGemini, editliGemini, buyukGemini int
	for _, d := range tumDosyalar {
		mtSn := float64(d.Mtime) / 1e9
		for _, k := range d.Kayitlar {
			if k.T > simdi+300 {
				gelecekGemini++
			}
			if k.T >= sinir && float64(k.T) > mtSn+3600 {
				editliGemini++
			}
			if k.T >= sinir && (k.Out > 500_000 || k.In+k.Cr+k.W5 > 10_000_000) {
				buyukGemini++
			}
		}
	}

	if engelGemini {
		ekBulgu("engel_gemini", "s", "Gemini/Antigravity kayıt klasörü veya dökümü okunamıyor ya da yönlendirilmiş: kullanım gizleniyor olabilir")
	}
	if kuculenGemini {
		ekBulgu("kuculen_gemini", "s", "Gemini/Antigravity oturum dökümündeki geçmiş token kayıtları azaltılmış ya da kırpılmış")
	}
	if silinenGemini >= 25_000 {
		ekBulgu("silinen_gemini", "s", fmt.Sprintf("Son 7 güne ait silinmiş Gemini/Antigravity oturum dosyalarında %d bin token tespit edildi", silinenGemini/1000))
	}
	if gelecekGemini > 0 {
		ekBulgu("gelecek_gemini", "s", fmt.Sprintf("%d Gemini/Antigravity kaydı gelecek tarihli: saat ya da kayıt dosyası oynanmış olabilir", gelecekGemini))
	}
	if editliGemini > 0 {
		ekBulgu("editli_gemini", "s", fmt.Sprintf("%d Gemini/Antigravity kaydının tarihi dosya değişim zamanından sonra: sonradan düzenlenmiş olabilir", editliGemini))
	}
	if buyukGemini > 0 {
		ekBulgu("buyuk_gemini", "s", fmt.Sprintf("%d Gemini/Antigravity kaydında imkânsız derecede büyük token değeri var", buyukGemini))
	}

	geminiHesap, geminiEposta, geminiLim, geminiLimVar, geminiHaftaReset := geminiHesapLimitVeResetOku(simdi)
	if geminiHesap != "" {
		s.hesaplar[sagGeminiAd] = geminiHesap
	}
	if geminiEposta != "" {
		s.epostalar[sagGeminiAd] = geminiEposta
	}
	if geminiHaftaReset > 0 {
		s.haftaSifirlama[sagGeminiAd] = geminiHaftaReset
	}
	if geminiLimVar {
		s.limitler[sagGeminiAd] = geminiLim
	}

	return tumDosyalar
}
