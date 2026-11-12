//go:build sag_claude || (!sag_gemini && !sag_chatgpt)

package main

// Claude Code durum satırı (statusLine) bağlantısı.
//
// Claude Code, ayarlardaki statusLine komutunu çalıştırır ve stdin'e oturum bilgisini JSON olarak
// verir; içindeki rate_limits abonelik limit yüzdelerini taşır. "durum-satiri" komutu bu JSON'dan
// yalnızca rate_limits'i süzüp <uygulama klasörü>/limit.json'a yazar, sonra kullanıcının önceki
// durum satırı komutu varsa onu aynı girdiyle çalıştırıp çıktısını olduğu gibi verir.
// Hiçbir Anthropic adresine bağlanılmaz, kimlik bilgisi okunmaz.

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
)

const (
	durumAnahtar    = "durum-satiri"
	limitYenileSn   = 60
	testClaudeDizin = "TOKEN_PANOSU_CLAUDE_DIZIN" // yalnızca test: ek Claude yapılandırma klasörü
)

func limitYolu() string { return filepath.Join(dizin, "limit.json") }

func limitCikar(girdi []byte) map[string]json.Number {
	d := json.NewDecoder(bytes.NewReader(girdi))
	d.UseNumber()
	var kok map[string]any
	if d.Decode(&kok) != nil {
		return nil
	}
	rl, ok := kok["rate_limits"]
	if !ok {
		return nil
	}
	m := map[string]json.Number{}
	limitDuzlestir("", rl, m)
	return limitSinirla(m)
}

func limitEsit(a, b map[string]json.Number) bool {
	if len(a) != len(b) {
		return false
	}
	for k, v := range a {
		if w, ok := b[k]; !ok || w.String() != v.String() {
			return false
		}
	}
	return true
}

func limitOku() (limitDosyasi, bool) {
	var l limitDosyasi
	b, err := os.ReadFile(limitYolu())
	if err != nil || json.Unmarshal(b, &l) != nil || l.T <= 0 || len(l.R) == 0 {
		return l, false
	}
	l.R = limitSinirla(l.R)
	return l, len(l.R) > 0
}

func limitYaz(simdi int64, m map[string]json.Number) {
	if eski, ok := limitOku(); ok && simdi-eski.T < limitYenileSn && simdi >= eski.T && limitEsit(eski.R, m) {
		return
	}
	b, err := json.Marshal(limitDosyasi{T: simdi, R: m})
	if err != nil {
		return
	}
	_ = os.MkdirAll(dizin, 0o700)
	_ = dosyaYaz(limitYolu(), b, 0o600)
}

// Çağıran Claude yapılandırma klasörü: CLAUDE_CONFIG_DIR, yoksa ~/.claude
func cagiranClaudeDizini() string {
	if c := os.Getenv("CLAUDE_CONFIG_DIR"); c != "" {
		return dizinAnahtari(c)
	}
	return dizinAnahtari(filepath.Join(evDizini(), ".claude"))
}

// Gizli komut: her zaman 0 ile çıkar, hata yazmaz
func durumSatiri() {
	defer func() { _ = recover() }()
	girdi, ok := stdinOku()
	if !ok {
		return
	}
	// Sürüm 17: durum satırından limit okunmaz (eski ya da yanlış değer gelebiliyordu); yalnızca önceki komut çalıştırılır.
	// Durum satırı gönderimde ayarlardan geri alınır; bu kısım yalnızca geçiş anında çağrılırsa diye durur.
	onceki := oncekiKomut(cagiranClaudeDizini())
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
		os.Stdout.Write(cikti.Bytes())
	}
}

func ayarOnceki(m map[string]json.RawMessage) map[string]json.RawMessage {
	o := map[string]json.RawMessage{}
	if r, ok := m["durum_satiri_onceki"]; ok {
		_ = json.Unmarshal(r, &o)
	}
	return o
}

func ayarKuruldu(m map[string]json.RawMessage) []string {
	var l []string
	if r, ok := m["durum_satiri_kuruldu"]; ok {
		_ = json.Unmarshal(r, &l)
	}
	return l
}

func oncekiKomut(dir string) string {
	r, ok := ayarOnceki(ayarHam())[dir]
	if !ok || hamBos(r) {
		return ""
	}
	var s struct {
		Command string `json:"command"`
	}
	if json.Unmarshal(r, &s) != nil || strings.Contains(s.Command, durumAnahtar) {
		return ""
	}
	return s.Command
}

// ---------- kurulum ----------

// Durum satırı kurulacak ya da kaldırılacak Claude yapılandırma klasörleri
func claudeDizinleri() []string {
	var adaylar []string
	if testModu {
		// Test modunda yalnızca TOKEN_PANOSU_DIZIN altındakiler ya da test için verilen klasör
		if t := os.Getenv(testClaudeDizin); t != "" {
			adaylar = append(adaylar, t)
		}
		if ust, err := filepath.Abs(dizin); err == nil {
			for _, p := range kaynaklar() {
				d := filepath.Dir(p)
				if a, err := filepath.Abs(d); err == nil && strings.HasPrefix(a, ust+string(filepath.Separator)) {
					adaylar = append(adaylar, d)
				}
			}
		}
	} else {
		for _, p := range kaynaklar() {
			adaylar = append(adaylar, filepath.Dir(p))
		}
	}
	var liste []string
	gorulen := map[string]bool{}
	for _, d := range adaylar {
		if !klasorMu(d) {
			continue
		}
		k := dizinAnahtari(d)
		if !gorulen[k] {
			gorulen[k] = true
			liste = append(liste, k)
		}
	}
	return liste
}

// Bir klasör için durum satırını kurar. Kaydedildiyse (kuruldu listesinde) ya da zaten bizimse dokunmaz.
func durumSatiriKurDizin(d string) error {
	yol := filepath.Join(d, "settings.json")
	m, var_, err := ayarlarOku(yol)
	if err != nil {
		return err // bozuk ya da okunamıyor: dokunma, kaydetme (sonra yeniden denenir)
	}
	ham := ayarHam()
	if sl, ok := m["statusLine"]; ok && strings.Contains(string(sl), durumAnahtar) {
		return kuruldaIsaretle(ham, d)
	}
	onceki := ayarOnceki(ham)
	if sl, ok := m["statusLine"]; ok {
		onceki[d] = append(json.RawMessage(nil), sl...)
	} else {
		onceki[d] = json.RawMessage("null")
	}
	ob, _ := json.Marshal(onceki)
	ham["durum_satiri_onceki"] = ob
	if err := ayarHamYaz(ham); err != nil { // önce önceki değeri güvenceye al
		return err
	}
	if var_ {
		eski, err := os.ReadFile(yol)
		if err != nil {
			return err
		}
		if err := dosyaYaz(yol+yedekEki, eski, 0o600); err != nil {
			return err
		}
	}
	exe, err := filepath.Abs(ikiliYolu())
	if err != nil {
		return err
	}
	yeni := map[string]any{"type": "command", "command": komutTirnakla(exe) + " " + durumAnahtar}
	if sl, ok := m["statusLine"]; ok {
		var o map[string]json.RawMessage
		if json.Unmarshal(sl, &o) == nil {
			if p, ok := o["padding"]; ok {
				yeni["padding"] = p
			}
		}
	}
	yb, _ := json.Marshal(yeni)
	m["statusLine"] = yb
	if err := ayarlarYaz(yol, m); err != nil {
		return err
	}
	return kuruldaIsaretle(ham, d)
}

func kuruldaIsaretle(ham map[string]json.RawMessage, d string) error {
	l := ayarKuruldu(ham)
	if listedeVar(l, d) {
		return nil
	}
	l = append(l, d)
	b, _ := json.Marshal(l)
	ham["durum_satiri_kuruldu"] = b
	return ayarHamYaz(ham)
}

// kur sırasında ve gonder başında: kaydı olmayan klasörlere kurar. Hata gönderimi bozmaz.
func durumSatiriKur() {
	defer func() {
		if r := recover(); r != nil {
			logYaz("durum satırı kurulumu çöktü: %v", r)
		}
	}()
	kayitli := ayarKuruldu(ayarHam())
	for _, d := range claudeDizinleri() {
		if listedeVar(kayitli, d) {
			continue
		}
		if err := durumSatiriKurDizin(d); err != nil {
			logYaz("durum satırı kurulamadı (%s): %v", filepath.Base(d), err)
		} else {
			logYaz("durum satırı kuruldu: %s", filepath.Base(d))
		}
	}
}

// kaldir: kayıtlı klasörlerde bizim komut hâlâ duruyorsa önceki değeri geri koyar
func durumSatiriKaldir() {
	defer func() { _ = recover() }()
	ham := ayarHam()
	onceki := ayarOnceki(ham)
	for _, d := range ayarKuruldu(ham) {
		yol := filepath.Join(d, "settings.json")
		m, _, err := ayarlarOku(yol)
		if err != nil {
			continue
		}
		sl, ok := m["statusLine"]
		if !ok || !strings.Contains(string(sl), durumAnahtar) {
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
