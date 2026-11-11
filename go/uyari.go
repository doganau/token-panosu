package main

// Limit payı uyarısı (sürüm 17+).
//
// Sunucu her gönderim cevabında bu bilgisayarın sahibinin (üye ya da tek bilgisayar) bu haftaki limit
// payını doldurup doldurmadığını söyler (asim). Doldurduysa:
//   - payın dolduğu ilk anda bir kez bildirim gösterilir,
//   - Claude Code'a kurulan UserPromptSubmit kancası sayesinde kullanıcı her yeni istek gönderdiğinde
//     bildirim tekrar gösterilir. Kanca yalnızca yerel asim.json dosyasına bakar: ağa çıkmaz, Claude'un
//     isteğini engellemez, çıktı vermez (Claude'un bağlamına hiçbir şey eklenmez).
// Pay dolmadıysa dosya silinir ve kanca hiçbir şey yapmaz.

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

const (
	uyariAnahtar       = "uyari"
	uyariGosterAnahtar = "_uyari-goster"
	uyariEskime        = 30 * 60 // asim.json bundan eskiyse (gönderim çalışmıyorsa) uyarı gösterilmez
	uyariAralik        = 10      // art arda iki uyarı arasında en az (sn)
	uyariYedekEki      = ".token-panosu-uyari-yedek"
	uyariOlay          = "UserPromptSubmit"
)

type asimDurumu struct {
	Ad string    `json:"ad"`          // üye ya da bilgisayar adı
	W  []float64 `json:"w,omitempty"` // haftalık: [kullandığı %, hakkı %]
	F  []float64 `json:"f,omitempty"` // Fable: [kullandığı %, hakkı %]
	T  int64     `json:"t,omitempty"` // yerelde: son güncelleme
}

func asimYolu() string     { return filepath.Join(dizin, "asim.json") }
func uyariSonYolu() string { return filepath.Join(dizin, "uyari_son") }

func asimGecerli(a *asimDurumu) bool {
	if a == nil {
		return false
	}
	ok := func(x []float64) bool { return len(x) == 2 && x[0] >= 0 && x[0] <= 1000 && x[1] > 0 && x[1] <= 100 }
	if !ok(a.W) {
		a.W = nil
	}
	if !ok(a.F) {
		a.F = nil
	}
	a.Ad = bildirimTemizle(a.Ad, 40)
	return a.W != nil || a.F != nil
}

func asimOku() (*asimDurumu, bool) {
	b, err := os.ReadFile(asimYolu())
	if err != nil {
		return nil, false
	}
	var a asimDurumu
	if json.Unmarshal(b, &a) != nil || !asimGecerli(&a) {
		return nil, false
	}
	return &a, true
}

// Gönderim cevabını işler: pay doluysa dosyayı yazar (doluluk yeni başladıysa hemen bir kez uyarır), değilse siler.
func asimIsle(a *asimDurumu, simdi int64) {
	if !asimGecerli(a) {
		if _, var_ := asimOku(); var_ {
			logYaz("limit payı uyarısı kalktı")
		}
		_ = os.Remove(asimYolu())
		return
	}
	_, onceVardi := asimOku()
	a.T = simdi
	b, err := json.Marshal(a)
	if err != nil {
		return
	}
	_ = dosyaYaz(asimYolu(), b, 0o600)
	if !onceVardi {
		logYaz("limit payı doldu: uyarı açıldı")
		uyariGoster()
	}
}

func uyariMetni(a *asimDurumu) (string, string) {
	baslik := "Claude limit payın doldu"
	if a.Ad != "" {
		baslik = a.Ad + ": Claude limit payın doldu"
	}
	var parca []string
	f := func(x float64) string { return strconv.FormatFloat(x, 'f', 1, 64) }
	if a.W != nil {
		parca = append(parca, fmt.Sprintf("Haftalık: %%%s kullandın, hakkın %%%s.", f(a.W[0]), f(a.W[1])))
	}
	if a.F != nil {
		parca = append(parca, fmt.Sprintf("Fable: %%%s kullandın, hakkın %%%s.", f(a.F[0]), f(a.F[1])))
	}
	parca = append(parca, "Kullanmaya devam ettikçe bu uyarı her istekte tekrar gelir.")
	return baslik, strings.Join(parca, " ")
}

// Bildirimi gösterir (ayrı süreçte çağrılır; kancayı bekletmemek için)
func uyariGoster() {
	defer func() { _ = recover() }()
	a, ok := asimOku()
	if !ok {
		return
	}
	baslik, metin := uyariMetni(a)
	if testModu {
		_ = bildirimTestYaz(0, baslik, metin)
		return
	}
	if err := bildirimGoster(baslik, metin); err != nil {
		logYaz("limit uyarısı gösterilemedi: %v", err)
	}
}

// Claude Code kancası: her zaman hemen ve 0 ile çıkar, çıktı vermez
func uyariKomutu() {
	defer func() { _ = recover() }()
	_, _ = stdinOku() // kancanın girdisi okunup atılır
	a, ok := asimOku()
	simdi := time.Now().Unix()
	if !ok || simdi-a.T > uyariEskime || a.T > simdi+300 {
		return
	}
	if b, err := os.ReadFile(uyariSonYolu()); err == nil {
		if n, err := strconv.ParseInt(strings.TrimSpace(string(b)), 10, 64); err == nil && simdi-n >= 0 && simdi-n < uyariAralik {
			return
		}
	}
	_ = os.WriteFile(uyariSonYolu(), []byte(strconv.FormatInt(simdi, 10)), 0o600)
	exe, err := os.Executable()
	if err != nil {
		return
	}
	c := exec.Command(exe, uyariGosterAnahtar)
	c.Stdin, c.Stdout, c.Stderr = nil, nil, nil
	if c.Start() == nil {
		_ = c.Process.Release() // beklenmez: Claude isteği hemen devam eder
	}
}

// ---------- kancanın kurulumu (settings.json içindeki hooks.UserPromptSubmit) ----------

func bizimKancaMi(komut string) bool {
	k := strings.TrimSpace(komut)
	return strings.Contains(k, "token-panosu") && strings.HasSuffix(k, " "+uyariAnahtar)
}

// hooks.UserPromptSubmit listesinde bizim kancamız var mı; varsa çıkarılmış listeyi de döndürür
func kancaAyikla(liste []any) (bulundu bool, kalan []any) {
	for _, e := range liste {
		grup, ok := e.(map[string]any)
		if !ok {
			kalan = append(kalan, e)
			continue
		}
		ic, _ := grup["hooks"].([]any)
		var yeniIc []any
		for _, h := range ic {
			if hm, ok := h.(map[string]any); ok {
				if k, _ := hm["command"].(string); bizimKancaMi(k) {
					bulundu = true
					continue
				}
			}
			yeniIc = append(yeniIc, h)
		}
		if len(ic) > 0 && len(yeniIc) == 0 {
			continue // grupta yalnızca bizim kancamız vardı
		}
		if len(yeniIc) != len(ic) {
			grup["hooks"] = yeniIc
		}
		kalan = append(kalan, grup)
	}
	return
}

func uyariKurDizin(d string) error {
	yol := filepath.Join(d, "settings.json")
	m, var_, err := ayarlarOku(yol)
	if err != nil {
		return err // bozuk ya da okunamıyor: dokunma
	}
	hooks := map[string]any{}
	if r, ok := m["hooks"]; ok && !hamBos(r) {
		if json.Unmarshal(r, &hooks) != nil || hooks == nil {
			return os.ErrInvalid // beklenmeyen biçim: dokunma
		}
	}
	var liste []any
	if r, ok := hooks[uyariOlay]; ok && r != nil {
		l, ok := r.([]any)
		if !ok {
			return os.ErrInvalid
		}
		liste = l
	}
	if bulundu, _ := kancaAyikla(liste); bulundu {
		return nil
	}
	if var_ {
		if _, err := os.Stat(yol + uyariYedekEki); os.IsNotExist(err) {
			eski, err := os.ReadFile(yol)
			if err != nil {
				return err
			}
			if err := dosyaYaz(yol+uyariYedekEki, eski, 0o600); err != nil {
				return err
			}
		}
	}
	exe, err := filepath.Abs(ikiliYolu())
	if err != nil {
		return err
	}
	liste = append(liste, map[string]any{
		"hooks": []any{map[string]any{"type": "command", "command": komutTirnakla(exe) + " " + uyariAnahtar, "timeout": 10}},
	})
	hooks[uyariOlay] = liste
	hb, err := json.Marshal(hooks)
	if err != nil {
		return err
	}
	m["hooks"] = hb
	return ayarlarYaz(yol, m)
}

func uyariKayitli() []string {
	var l []string
	if r, ok := ayarHam()["uyari_kuruldu"]; ok {
		_ = json.Unmarshal(r, &l)
	}
	return l
}

// kur sırasında ve gonder başında: kaydı olmayan Claude klasörlerine kancayı kurar. Hata gönderimi bozmaz.
func uyariKur() {
	defer func() {
		if r := recover(); r != nil {
			logYaz("limit uyarısı kancası kurulumu çöktü: %v", r)
		}
	}()
	kayitli := uyariKayitli()
	degisti := false
	for _, d := range claudeDizinleri() {
		if listedeVar(kayitli, d) {
			continue
		}
		if err := uyariKurDizin(d); err != nil {
			logYaz("limit uyarısı kancası kurulamadı (%s): %v", filepath.Base(d), err)
			continue
		}
		logYaz("limit uyarısı kancası kuruldu: %s", filepath.Base(d))
		kayitli = append(kayitli, d)
		degisti = true
	}
	if degisti {
		ham := ayarHam()
		b, _ := json.Marshal(kayitli)
		ham["uyari_kuruldu"] = b
		_ = ayarHamYaz(ham)
	}
}

// kaldir: kayıtlı klasörlerden bizim kancayı çıkarır, başka kancalara dokunmaz
func uyariKaldir() {
	defer func() { _ = recover() }()
	for _, d := range uyariKayitli() {
		yol := filepath.Join(d, "settings.json")
		m, _, err := ayarlarOku(yol)
		if err != nil {
			continue
		}
		r, ok := m["hooks"]
		if !ok {
			continue
		}
		hooks := map[string]any{}
		if json.Unmarshal(r, &hooks) != nil {
			continue
		}
		l, _ := hooks[uyariOlay].([]any)
		bulundu, kalan := kancaAyikla(l)
		if !bulundu {
			continue
		}
		if len(kalan) == 0 {
			delete(hooks, uyariOlay)
		} else {
			hooks[uyariOlay] = kalan
		}
		if len(hooks) == 0 {
			delete(m, "hooks")
		} else if hb, err := json.Marshal(hooks); err == nil {
			m["hooks"] = hb
		}
		_ = ayarlarYaz(yol, m)
	}
	_ = os.Remove(asimYolu())
}
