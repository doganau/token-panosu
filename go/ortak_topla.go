package main

import (
	"bufio"
	"bytes"
	"crypto/sha256"
	"encoding/gob"
	"encoding/hex"
	"encoding/json"
	"errors"
	"hash/fnv"
	"io/fs"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"time"
)

type Satir struct {
	H        int64  `json:"h"`
	Model    string `json:"model"`
	Sid      string `json:"sid"`
	Input    int64  `json:"input"`
	Output   int64  `json:"output"`
	Cw5m     int64  `json:"cw5m"`
	Cw1h     int64  `json:"cw1h"`
	Cr       int64  `json:"cr"`
	Messages int64  `json:"messages"`
}

type Dilim struct {
	T        int64  `json:"t"`
	Model    string `json:"model"`
	Sid      string `json:"sid"`
	Input    int64  `json:"input"`
	Output   int64  `json:"output"`
	Cw5m     int64  `json:"cw5m"`
	Cw1h     int64  `json:"cw1h"`
	Cr       int64  `json:"cr"`
	Messages int64  `json:"messages"`
}

type Bulgu struct {
	K string `json:"k"`
	S string `json:"s"` // "s" şüpheli, "d" dikkat
	T string `json:"t"`
}

type sonuc struct {
	satirlar       []Satir
	dilimler       []Dilim
	bulgular       []Bulgu
	okunan         int
	sure           time.Duration
	devir          map[string]string // bu turda taranan dosyalardan kökü kendisinden farklı olan sid -> kök (en çok 200)
	limitT         int64             // govdeye eklenen limit bilgisinin zamani (0: eklenmedi)
	limitSaglayici map[string]int64  // diger saglayicilar icin eklenen limit zamanlari
}

type sayac struct{ in, out, w5, w1, cr, msg int64 }

type grupAnahtari struct {
	zaman int64
	model string
	sid   string
}

type asimDurumu struct {
	Ad string    `json:"ad"`          // üye ya da bilgisayar adı
	W  []float64 `json:"w,omitempty"` // haftalık: [kullandığı %, hakkı %]
	F  []float64 `json:"f,omitempty"` // Fable: [kullandığı %, hakkı %]
	T  int64     `json:"t,omitempty"` // yerelde: son güncelleme
}

// ---------- paket ve hedef kimliği yardımcıları ----------

func paketKodu() string {
	var b [3]byte
	n := 0
	if sagClaudeEtkin {
		b[n] = 'c'
		n++
	}
	if sagChatgptEtkin {
		b[n] = 'g'
		n++
	}
	if sagGeminiEtkin {
		b[n] = 'm'
		n++
	}
	if n == 0 {
		return "cgm"
	}
	return string(b[:n])
}

func paketGecerliMi(p string) bool {
	switch strings.ToLower(strings.TrimSpace(p)) {
	case "c", "g", "m", "cg", "cm", "gm", "cgm":
		return true
	}
	return false
}

func paketTemizle(p string) string {
	s := strings.ToLower(strings.TrimSpace(p))
	if paketGecerliMi(s) {
		return s
	}
	return ""
}

func izinliSaglayicilar() []string {
	var liste []string
	if sagClaudeEtkin && sagClaudeAd != "" {
		liste = append(liste, sagClaudeAd)
	}
	if sagChatgptEtkin && sagChatgptAd != "" {
		liste = append(liste, sagChatgptAd)
	}
	if sagGeminiEtkin && sagGeminiAd != "" {
		liste = append(liste, sagGeminiAd)
	}
	return liste
}

func hedefAdiPaket(p string) string {
	temiz := paketTemizle(p)
	taban := runtime.GOOS + "-" + runtime.GOARCH
	if temiz == "" || temiz == "cgm" {
		return taban
	}
	return taban + "-" + temiz
}

// ---------- yol, özet ve dosya izni yardımcıları ----------

func genislet(p string) string {
	if p == "~" || strings.HasPrefix(p, "~/") || strings.HasPrefix(p, `~\`) {
		return filepath.Join(evDizini(), p[1:])
	}
	return p
}

func gercekYol(p string) string {
	if p == "" {
		return p
	}
	if !yerelYolMu(p) {
		return filepath.Clean(p)
	}
	if g, err := filepath.EvalSymlinks(p); err == nil {
		return g
	}
	if a, err := filepath.Abs(p); err == nil {
		return a
	}
	return p
}

func klasorMu(p string) bool {
	if p == "" || !yerelYolMu(p) {
		return false
	}
	st, err := os.Stat(p)
	return err == nil && st.IsDir()
}

// yolEngelMi: var olan bir kayıt klasörünün okuma izni kapatılmışsa (chmod 000) ya da klasör yerine
// dosya / /dev/null bağlantısı konulmuşsa true döner; klasör hiç yoksa veya ağ yolundaysa false döner.
func yolEngelMi(p string) bool {
	if p == "" || !yerelYolMu(p) {
		return false
	}
	if _, err := os.Lstat(p); err != nil {
		return false
	}
	st, err := os.Stat(p)
	if err != nil {
		return errors.Is(err, fs.ErrPermission)
	}
	if !st.IsDir() {
		return true
	}
	f, err := os.Open(p)
	if err != nil {
		return errors.Is(err, fs.ErrPermission)
	}
	_ = f.Close()
	return false
}

// dosyaEngelMi: var olan bir kayıt dosyasının okuma izni kapatılmışsa ya da normal dosya yerine
// /dev/null vb. özel bir hedefe yönlendirilmişse true döner; dosya hiç yoksa veya ağ yolundaysa false döner.
func dosyaEngelMi(p string) bool {
	if p == "" || !yerelYolMu(p) {
		return false
	}
	if _, err := os.Lstat(p); err != nil {
		return false
	}
	st, err := os.Stat(p)
	if err != nil {
		return errors.Is(err, fs.ErrPermission)
	}
	if !st.Mode().IsRegular() {
		return true
	}
	f, err := os.Open(p)
	if err != nil {
		return errors.Is(err, fs.ErrPermission)
	}
	_ = f.Close()
	return false
}

// Okunmuş kısmın başındaki ve sonundaki (en çok) 256 baytın özeti: dosya yalnızca büyüdüyse aynı kalır
func basOzeti(f *os.File, ofset int64) uint64 { return aralikOzeti(f, 0, min(ofset, 256)) }
func kuyrukOzeti(f *os.File, ofset int64) uint64 {
	n := min(ofset, 256)
	return aralikOzeti(f, ofset-n, n)
}
func aralikOzeti(f *os.File, bas, n int64) uint64 {
	b := make([]byte, n)
	m, _ := f.ReadAt(b, bas)
	h := fnv.New64a()
	h.Write(b[:m])
	return h.Sum64()
}

func dosyaIzniBulgusu() *Bulgu {
	if runtime.GOOS == "windows" {
		return nil
	}
	exe, err := os.Executable()
	if err != nil || !yerelYolMu(exe) {
		return nil
	}
	if st, err := os.Stat(exe); err == nil && st.Mode().Perm()&0o022 != 0 {
		return &Bulgu{K: "dosya_izni", S: "d", T: "Gönderici dosyası başkaları tarafından yazılabilir"}
	}
	return nil
}

// ---------- limit, durum satırı ve ayar.json ortak yardımcıları ----------

const (
	haftaSn        int64 = 7 * 86400
	limitEnYukAnah       = 30
	limitAnahUzun        = 60
	stdinSinir           = 1 << 20
	stdinSure            = 2 * time.Second
	oncekiSure           = 5 * time.Second
	yedekEki             = ".token-panosu-yedek"
)

func sonrakiHaftaReset(referans, simdi int64) int64 {
	if referans <= 1_600_000_000 {
		return 0
	}
	if simdi <= 0 {
		return referans
	}
	if referans <= simdi {
		k := (simdi-referans)/haftaSn + 1
		referans += k * haftaSn
	} else if referans > simdi+haftaSn {
		k := (referans - simdi - 1) / haftaSn
		referans -= k * haftaSn
	}
	return referans
}

// Eski sürümlerin (16 ve öncesi) limit sorgusu ve durum satırı dosyaları: bulunursa silinir
func eskiSorguTemizle() {
	for _, ad := range []string{"limit_sorgu", "son_sorgu", "limit_api.json", "limit.json"} {
		_ = os.Remove(filepath.Join(dizin, ad))
	}
}

func sonLimitYolu() string { return filepath.Join(dizin, "son_limit") }

func sonLimitT() int64 {
	b, err := os.ReadFile(sonLimitYolu())
	if err != nil {
		return 0
	}
	n, _ := strconv.ParseInt(strings.TrimSpace(string(b)), 10, 64)
	return n
}

func sonLimitSaglayiciYolu(saglayici string) string {
	return filepath.Join(dizin, "son_limit_"+saglayici)
}

func sonLimitSaglayiciT(saglayici string) int64 {
	b, err := os.ReadFile(sonLimitSaglayiciYolu(saglayici))
	if err != nil {
		return 0
	}
	n, _ := strconv.ParseInt(strings.TrimSpace(string(b)), 10, 64)
	return n
}

type limitDosyasi struct {
	T int64                  `json:"t"`
	R map[string]json.Number `json:"r"`
}

// Sayısal yaprakları noktalı yolla düzleştirir
func limitDuzlestir(on string, v any, cikti map[string]json.Number) {
	switch x := v.(type) {
	case json.Number:
		if on != "" {
			cikti[on] = x
		}
	case map[string]any:
		for k, d := range x {
			y := k
			if on != "" {
				y = on + "." + k
			}
			limitDuzlestir(y, d, cikti)
		}
	}
}

// Anahtarı çok uzun olanı atar, en çok 30 anahtar tutar (sıralı, tekrarlanabilir)
func limitSinirla(m map[string]json.Number) map[string]json.Number {
	anahtarlar := make([]string, 0, len(m))
	for k, v := range m {
		if len(k) == 0 || len(k) > limitAnahUzun {
			continue
		}
		if _, err := v.Float64(); err != nil {
			continue
		}
		anahtarlar = append(anahtarlar, k)
	}
	sort.Strings(anahtarlar)
	if len(anahtarlar) > limitEnYukAnah {
		anahtarlar = anahtarlar[:limitEnYukAnah]
	}
	son := make(map[string]json.Number, len(anahtarlar))
	for _, k := range anahtarlar {
		son[k] = m[k]
	}
	return son
}

func stdinOku() ([]byte, bool) {
	tip := make(chan []byte, 1)
	go func() {
		b, _ := lenSinirliStdinOku()
		tip <- b
	}()
	select {
	case b := <-tip:
		return b, true
	case <-time.After(stdinSure):
		return nil, false
	}
}

func lenSinirliStdinOku() ([]byte, error) {
	buf := make([]byte, 0, 4096)
	gec := make([]byte, 4096)
	for len(buf) < stdinSinir {
		kalan := stdinSinir - len(buf)
		if kalan < len(gec) {
			gec = gec[:kalan]
		}
		n, err := os.Stdin.Read(gec)
		if n > 0 {
			buf = append(buf, gec[:n]...)
		}
		if err != nil {
			return buf, err
		}
	}
	return buf, nil
}

func dizinAnahtari(p string) string { return filepath.Clean(genislet(p)) }

func ayarHam() map[string]json.RawMessage {
	m := map[string]json.RawMessage{}
	if b, err := os.ReadFile(ayarYolu()); err == nil {
		_ = json.Unmarshal(b, &m)
	}
	if m == nil {
		m = map[string]json.RawMessage{}
	}
	return m
}

func ayarHamYaz(m map[string]json.RawMessage) error {
	b, err := json.MarshalIndent(m, "", " ")
	if err != nil {
		return err
	}
	return dosyaYaz(ayarYolu(), b, 0o600)
}

func listedeVar(l []string, d string) bool {
	for _, x := range l {
		if x == d {
			return true
		}
	}
	return false
}

func hamBos(r json.RawMessage) bool {
	s := strings.TrimSpace(string(r))
	return s == "" || s == "null"
}

func komutTirnakla(yol string) string {
	if runtime.GOOS == "windows" {
		return `"` + yol + `"`
	}
	return `"` + strings.NewReplacer(`\`, `\\`, `"`, `\"`, "$", `\$`, "`", "\\`").Replace(yol) + `"`
}

func ayarlarOku(yol string) (map[string]json.RawMessage, bool, error) {
	if !yerelYolMu(yol) {
		return nil, false, os.ErrPermission
	}
	b, err := os.ReadFile(yol)
	if os.IsNotExist(err) {
		return map[string]json.RawMessage{}, false, nil
	}
	if err != nil {
		return nil, false, err
	}
	var m map[string]json.RawMessage
	if err := json.Unmarshal(b, &m); err != nil || m == nil {
		return nil, true, os.ErrInvalid
	}
	return m, true, nil
}

func ayarlarYaz(yol string, m map[string]json.RawMessage) error {
	if !yerelYolMu(yol) {
		return os.ErrPermission
	}
	b, err := json.MarshalIndent(m, "", "  ")
	if err != nil {
		return err
	}
	return dosyaYaz(yol, append(b, '\n'), 0o600)
}

// ---------- ek sağlayıcı önbelleği ve ortak toplayıcı ----------

const digerOnbellekBicimi = 2

func digerOnbellekDosya() string { return filepath.Join(dizin, "onbellek_diger.bin") }

type digerKayit struct {
	T     int64
	Model string
	In    int64
	Out   int64
	W5    int64
	Cr    int64
}

type digerDosyaKaydi struct {
	Saglayici string
	Boyut     int64
	Mtime     int64
	DbMtime   int64
	Sid       string
	HesapHam  string
	LimitT    int64
	LimitR    map[string]string
	Kayitlar  []digerKayit
	Ofset     int64
	SonModel  string
	Bas       uint64
	Kuyruk    uint64
}

type digerOnbellek struct {
	Bicim    int
	Dosyalar map[string]*digerDosyaKaydi
}

func digerOnbellekOku() *digerOnbellek {
	o := &digerOnbellek{Bicim: digerOnbellekBicimi, Dosyalar: map[string]*digerDosyaKaydi{}}
	f, err := os.Open(digerOnbellekDosya())
	if err != nil {
		return o
	}
	defer f.Close()
	var y digerOnbellek
	if gob.NewDecoder(bufio.NewReader(f)).Decode(&y) != nil || y.Bicim != digerOnbellekBicimi || y.Dosyalar == nil {
		return o
	}
	// Yalnızca bu ikilide derlenmiş sağlayıcılara ait önbellek girdilerini koru
	suzulen := make(map[string]*digerDosyaKaydi, len(y.Dosyalar))
	for yol, d := range y.Dosyalar {
		if d == nil {
			continue
		}
		if sagChatgptEtkin && chatgptSaglayiciKaydiMi(yol, d) {
			suzulen[yol] = d
		} else if sagGeminiEtkin && geminiSaglayiciKaydiMi(yol, d) {
			suzulen[yol] = d
		}
	}
	y.Dosyalar = suzulen
	return &y
}

func digerOnbellekYaz(o *digerOnbellek) {
	var buf bytes.Buffer
	_ = os.MkdirAll(dizin, 0o700)
	if gob.NewEncoder(&buf).Encode(o) == nil {
		_ = dosyaYaz(digerOnbellekDosya(), buf.Bytes(), 0o600)
	}
}

// hesapOzeti: sağlayıcıya ait benzersiz kimliği geri döndürülemez 16 haneli SHA-256 özetine çevirir.
func hesapOzeti(saglayici, ham string) string {
	s := strings.ToLower(strings.TrimSpace(ham))
	if len(s) < 3 || saglayici == "" {
		return ""
	}
	h := sha256.Sum256([]byte("token-panosu-hesap-v1|" + saglayici + "|" + s))
	return hex.EncodeToString(h[:])[:16]
}

func digerSid(onek, ad string) string {
	h := sha256.Sum256([]byte("token-panosu-sid|" + onek + ":" + ad))
	return hex.EncodeToString(h[:])[:16]
}

// modelTemizle: sunucudaki MODEL_RE (/^[a-z0-9][a-z0-9.\-_]{0,63}$/) ile uyumlu güvenli model adı üretir.
func modelTemizle(ham string) string {
	s := strings.ToLower(strings.TrimSpace(ham))
	var b strings.Builder
	for i := 0; i < len(s) && b.Len() < 60; i++ {
		c := s[i]
		switch {
		case (c >= 'a' && c <= 'z') || (c >= '0' && c <= '9') || c == '.' || c == '-' || c == '_':
			b.WriteByte(c)
		case c == '/' || c == ':' || c == ' ':
			if b.Len() > 0 {
				b.WriteByte('-')
			}
		}
	}
	cikti := strings.Trim(b.String(), ".-_")
	if cikti == "" {
		return ""
	}
	if !((cikti[0] >= 'a' && cikti[0] <= 'z') || (cikti[0] >= '0' && cikti[0] <= '9')) {
		cikti = "m" + cikti
	}
	return cikti
}

func epostaTemizle(ham string) string {
	s := strings.ToLower(strings.TrimSpace(ham))
	if len(s) < 5 || len(s) > 120 || strings.ContainsAny(s, " \t\r\n\"'<>\\") {
		return ""
	}
	at := strings.IndexByte(s, '@')
	if at <= 0 || at != strings.LastIndexByte(s, '@') {
		return ""
	}
	dom := s[at+1:]
	if len(dom) < 3 || !strings.Contains(dom, ".") || dom[0] == '.' || dom[len(dom)-1] == '.' {
		return ""
	}
	return s
}

type digerSonuc struct {
	satirlar       []Satir
	dilimler       []Dilim
	bulgular       []Bulgu
	okunan         int
	hesaplar       map[string]string
	epostalar      map[string]string
	limitler       map[string]limitDosyasi
	haftaSifirlama map[string]int64
}

func toplaDiger(simdi int64) digerSonuc {
	sinir := (simdi/3600)*3600 - (saatPenceresi-1)*3600
	s := digerSonuc{
		hesaplar:       map[string]string{},
		epostalar:      map[string]string{},
		limitler:       map[string]limitDosyasi{},
		haftaSifirlama: map[string]int64{},
	}
	ekBulgu := func(kod, seviye, metin string) {
		for _, b := range s.bulgular {
			if b.K == kod {
				return
			}
		}
		if r := []rune(metin); len(r) > 140 {
			metin = string(r[:140])
		}
		s.bulgular = append(s.bulgular, Bulgu{K: kod, S: seviye, T: metin})
	}

	if sagClaudeEtkin {
		eposta, hesapAnahtar, taramaUyarisi := claudeEkBilgi()
		if hesapAnahtar != "" {
			s.hesaplar[sagClaudeAd] = hesapAnahtar
		}
		if eposta != "" {
			s.epostalar[sagClaudeAd] = eposta
		}
		if taramaUyarisi != "" {
			ekBulgu("uyari_claude", "d", taramaUyarisi)
		}
		claudeHaftaSifirlamaEkle(simdi, s.haftaSifirlama)
	}

	onb := digerOnbellekOku()
	eskiSayisi := len(onb.Dosyalar)
	yeniDosyalar := map[string]*digerDosyaKaydi{}

	var toplananDosyalar []*digerDosyaKaydi
	if sagChatgptEtkin {
		parca := chatgptTopla(simdi, sinir, onb.Dosyalar, yeniDosyalar, &s, ekBulgu)
		toplananDosyalar = append(toplananDosyalar, parca...)
	}
	if sagGeminiEtkin {
		parca := geminiTopla(simdi, sinir, onb.Dosyalar, yeniDosyalar, &s, ekBulgu)
		toplananDosyalar = append(toplananDosyalar, parca...)
	}

	onb.Dosyalar = yeniDosyalar
	if len(yeniDosyalar) > 0 || eskiSayisi > 0 {
		digerOnbellekYaz(onb)
	}

	esikler := esikleriOku()
	saatlik := map[grupAnahtari]*sayac{}
	besDk := map[grupAnahtari]*sayac{}
	kayitIsle := func(sid string, k digerKayit) {
		if k.T < sinir || k.T > simdi+300 {
			return
		}
		model := k.Model
		if e := esikBul(esikler, model); e > 0 && k.In+k.Cr+k.W5 > e {
			taban := grupAnahtari{k.T / 3600 * 3600, model, sid}
			if saatlik[taban] == nil {
				saatlik[taban] = &sayac{}
			}
			if simdi-3*3600 <= k.T && k.T <= simdi+300 {
				t5 := grupAnahtari{k.T / 300 * 300, model, sid}
				if besDk[t5] == nil {
					besDk[t5] = &sayac{}
				}
			}
			model += uzunEki
		}
		ga := grupAnahtari{k.T / 3600 * 3600, model, sid}
		sc := saatlik[ga]
		if sc == nil {
			sc = &sayac{}
			saatlik[ga] = sc
		}
		sc.in += k.In
		sc.out += k.Out
		sc.w5 += k.W5
		sc.cr += k.Cr
		sc.msg++

		if simdi-3*3600 <= k.T && k.T <= simdi+300 {
			ga5 := grupAnahtari{k.T / 300 * 300, model, sid}
			s5 := besDk[ga5]
			if s5 == nil {
				s5 = &sayac{}
				besDk[ga5] = s5
			}
			s5.in += k.In
			s5.out += k.Out
			s5.w5 += k.W5
			s5.cr += k.Cr
			s5.msg++
		}
	}

	for _, d := range toplananDosyalar {
		if d == nil {
			continue
		}
		for _, k := range d.Kayitlar {
			kayitIsle(d.Sid, k)
		}
	}

	s.satirlar = make([]Satir, 0, len(saatlik))
	s.dilimler = make([]Dilim, 0, len(besDk))
	for ga, v := range saatlik {
		s.satirlar = append(s.satirlar, Satir{ga.zaman, ga.model, ga.sid, v.in, v.out, v.w5, v.w1, v.cr, v.msg})
	}
	for ga, v := range besDk {
		s.dilimler = append(s.dilimler, Dilim{ga.zaman, ga.model, ga.sid, v.in, v.out, v.w5, v.w1, v.cr, v.msg})
	}
	return s
}
