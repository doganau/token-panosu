package main

// Kayıt dosyalarını okuyup saat/model/oturum bazında toplar.
//
// Hafiflik: her dosyanın okunmuş kısmı önbellekte tutulur. Değişmeyen dosya hiç açılmaz,
// büyüyen dosyanın yalnızca yeni eklenen kısmı okunur. Dosya başka biçimde değişmişse
// (kısalmış ya da okunmuş kısmı farklılaşmışsa) baştan okunur. Sonuç, her seferinde her
// şeyi baştan okumakla birebir aynıdır.

import (
	"bufio"
	"bytes"
	"crypto/sha256"
	"encoding/gob"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"hash/fnv"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"runtime"
	"sort"
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

var bulgular []Bulgu

func bulgu(kod, seviye, metin string) {
	for _, b := range bulgular {
		if b.K == kod {
			return
		}
	}
	if r := []rune(metin); len(r) > 140 {
		metin = string(r[:140])
	}
	bulgular = append(bulgular, Bulgu{K: kod, S: seviye, T: metin})
}

// ---------- önbellek ----------

const onbellekBicimi = 1

type kayit struct {
	T       int64
	Model   uint16
	Anahtar uint64 // (message.id, requestId) özeti; 0 = tekilleştirme yok
	In      int64
	Out     int64
	W5      int64
	W1      int64
	Cr      int64
	Buyuk   bool
}

type dosyaKaydi struct {
	Boyut    int64
	Mtime    int64 // nanosaniye
	Ofset    int64 // buraya kadar okundu (tam satır sınırı)
	Bas      uint64
	Kuyruk   uint64
	Kayitlar []kayit
}

// devirKaydi: bir oturum kimliğinin (sid) hangi sohbet köküne bağlandığı. Devam ettirilen ya da
// çatallanan sohbet yeni dosya/sid açar; kök kalıcı tutulur ki asıl dosya pencere dışına düşse
// ya da silinse bile devam dosyası aynı kök altında raporlansın.
type devirKaydi struct {
	Kok string
	Ilk int64 // ilk kaydedildiği an (unix); birden çok kök birleşirse en eskisi kazanır
	Son int64 // en son görüldüğü an (unix); devirBudamaSn kadar görülmeyen budanır
}

const devirBudamaSn = 60 * 86400

// Biçim numarası artırılmadı: gob eksik alanı (Devir) boş bırakır, eski önbellek olduğu gibi yüklenir.
type onbellek struct {
	Bicim    int
	Modeller []string
	Dosyalar map[string]*dosyaKaydi
	Devir    map[string]devirKaydi
}

func onbellekYukle() *onbellek {
	o := &onbellek{Bicim: onbellekBicimi, Dosyalar: map[string]*dosyaKaydi{}, Devir: map[string]devirKaydi{}}
	f, err := os.Open(onbellekYolu())
	if err != nil {
		return o
	}
	defer f.Close()
	var y onbellek
	if gob.NewDecoder(bufio.NewReader(f)).Decode(&y) != nil || y.Bicim != onbellekBicimi || y.Dosyalar == nil {
		return o
	}
	for _, d := range y.Dosyalar {
		for _, k := range d.Kayitlar {
			if int(k.Model) >= len(y.Modeller) {
				return o // tutarsız önbellek: baştan kur
			}
		}
	}
	if y.Devir == nil {
		y.Devir = map[string]devirKaydi{}
	}
	return &y
}

func onbellekKaydet(o *onbellek) {
	// Kullanılmayan model adlarını at, indeksleri yeniden numarala
	yeni, eslem := []string{}, map[uint16]uint16{}
	for _, d := range o.Dosyalar {
		for i := range d.Kayitlar {
			m := d.Kayitlar[i].Model
			n, ok := eslem[m]
			if !ok {
				n = uint16(len(yeni))
				eslem[m] = n
				yeni = append(yeni, o.Modeller[m])
			}
			d.Kayitlar[i].Model = n
		}
	}
	o.Modeller = yeni
	var buf bytes.Buffer
	_ = os.MkdirAll(dizin, 0o700)
	if gob.NewEncoder(&buf).Encode(o) == nil {
		_ = dosyaYaz(onbellekYolu(), buf.Bytes(), 0o600)
	}
}

// ---------- kaynak klasörleri ve kontroller ----------

var apiOrtam = []string{"ANTHROPIC_API_KEY", "ANTHROPIC_AUTH_TOKEN", "ANTHROPIC_BASE_URL",
	"CLAUDE_CODE_USE_BEDROCK", "CLAUDE_CODE_USE_VERTEX", "CLAUDE_CODE_USE_FOUNDRY"}

func genislet(p string) string {
	if p == "~" || strings.HasPrefix(p, "~/") || strings.HasPrefix(p, `~\`) {
		return filepath.Join(evDizini(), p[1:])
	}
	return p
}

func gercekYol(p string) string {
	if g, err := filepath.EvalSymlinks(p); err == nil {
		return g
	}
	if a, err := filepath.Abs(p); err == nil {
		return a
	}
	return p
}

func klasorMu(p string) bool {
	st, err := os.Stat(p)
	return err == nil && st.IsDir()
}

// yolEngelMi: var olan bir kayıt klasörünün okuma izni kapatılmışsa (chmod 000) ya da klasör yerine
// dosya / /dev/null bağlantısı konulmuşsa true döner; klasör hiç yoksa false döner.
func yolEngelMi(p string) bool {
	if p == "" {
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
// /dev/null vb. özel bir hedefe yönlendirilmişse true döner; dosya hiç yoksa false döner.
func dosyaEngelMi(p string) bool {
	if p == "" {
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

func kaynaklar() []string {
	ev := evDizini()
	var liste []string
	gorulen := map[string]bool{}
	ekle := func(y string) bool {
		g := gercekYol(y)
		if gorulen[g] {
			return false
		}
		gorulen[g] = true
		liste = append(liste, y)
		return true
	}
	ekle(filepath.Join(ev, ".claude", "projects"))
	ekle(filepath.Join(ev, ".config", "claude", "projects"))
	if cfg := os.Getenv("CLAUDE_CONFIG_DIR"); cfg != "" {
		ekle(filepath.Join(genislet(cfg), "projects"))
		bulgu("config_dir_claude", "d", "CLAUDE_CONFIG_DIR ayarlı: kayıtlar farklı bir klasörde (o da sayılıyor)")
	}
	for _, desen := range []string{".claude*", ".config/claude*", ".local/share/claude*"} {
		esler, _ := filepath.Glob(filepath.Join(ev, filepath.FromSlash(desen)))
		for _, d := range esler {
			pr := filepath.Join(d, "projects")
			if klasorMu(pr) && ekle(pr) {
				bulgu("ek_klasor_claude", "d", fmt.Sprintf("Ek Claude klasörü var: %s (o da sayılıyor)", filepath.Base(d)))
			}
		}
	}
	return liste
}

func apiAyari(ad string, deger any) bool {
	if !dolu(deger) {
		return false
	}
	if ad == "ANTHROPIC_BASE_URL" {
		s := strings.ToLower(strings.TrimRight(fmt.Sprint(deger), "/"))
		return !strings.HasPrefix(s, "https://api.anthropic.com")
	}
	return true
}

// Python'daki "doğruluk" kuralı: boş/sıfır/yok değerler yanlış sayılır
func dolu(v any) bool {
	switch x := v.(type) {
	case nil:
		return false
	case bool:
		return x
	case string:
		return x != ""
	case json.Number:
		f, err := x.Float64()
		return err != nil || f != 0
	case float64:
		return x != 0
	case map[string]any:
		return len(x) > 0
	case []any:
		return len(x) > 0
	}
	return true
}

func ortamKontrol(klasorler []string) {
	ev := evDizini()
	for _, p := range append([]string{filepath.Join(ev, ".claude")}, klasorler...) {
		if yolEngelMi(p) {
			bulgu("engel_claude", "s", "Claude kayıt klasörü okunamıyor ya da yönlendirilmiş: kullanım gizleniyor olabilir")
			break
		}
	}
	for _, ad := range apiOrtam {
		if v := os.Getenv(ad); apiAyari(ad, v) {
			bulgu("api_ortam_claude", "s", ad+" ayarlı: kullanım abonelik limitinden düşmeyebilir")
			break
		}
	}
	ayarlar := map[string]bool{}
	for _, pr := range klasorler {
		kok := filepath.Dir(pr)
		ayarlar[filepath.Join(kok, "settings.json")] = true
		ayarlar[filepath.Join(kok, "settings.local.json")] = true
	}
	yollar := make([]string, 0, len(ayarlar))
	for y := range ayarlar {
		yollar = append(yollar, y)
	}
	sort.Strings(yollar)
	for _, yol := range yollar {
		b, err := os.ReadFile(yol)
		if err != nil {
			continue
		}
		dec := json.NewDecoder(bytes.NewReader(b))
		dec.UseNumber()
		var a map[string]any
		if dec.Decode(&a) != nil || a == nil {
			continue
		}
		if dolu(a["apiKeyHelper"]) {
			bulgu("api_ortam_claude", "s", "apiKeyHelper ayarlı: kullanım abonelik limitinden düşmeyebilir")
		}
		if env, ok := a["env"].(map[string]any); ok {
			for _, ad := range apiOrtam {
				if apiAyari(ad, env[ad]) {
					bulgu("api_ortam_claude", "s", ad+" ayarlı: kullanım abonelik limitinden düşmeyebilir")
					break
				}
			}
		}
		if n, ok := a["cleanupPeriodDays"].(json.Number); ok && !strings.ContainsAny(n.String(), ".eE") {
			if gun, err := n.Int64(); err == nil && gun >= 0 && gun < saatPenceresi/24 {
				sev := "d"
				if gun <= 1 {
					sev = "s"
				}
				bulgu("temizlik_claude", sev, fmt.Sprintf("Kayıtlar %d gün sonra siliniyor: kullanımın bir kısmı kaçabilir", gun))
			}
		}
	}
	if runtime.GOOS != "windows" {
		if exe, err := os.Executable(); err == nil {
			if st, err := os.Stat(exe); err == nil && st.Mode().Perm()&0o022 != 0 {
				bulgu("dosya_izni", "d", "Gönderici dosyası başkaları tarafından yazılabilir")
			}
		}
	}
	ben := gercekYol(evDizini())
	for _, desen := range []string{"/Users/*/.claude/projects", "/home/*/.claude/projects", `C:\Users\*\.claude\projects`} {
		if runtime.GOOS == "windows" && strings.HasPrefix(desen, "/") || runtime.GOOS != "windows" && strings.HasPrefix(desen, "C:") {
			continue
		}
		esler, _ := filepath.Glob(desen)
		for _, pr := range esler {
			ev := filepath.Dir(filepath.Dir(pr))
			if gercekYol(ev) == ben {
				continue
			}
			if j, _ := filepath.Glob(filepath.Join(pr, "*", "*.jsonl")); len(j) > 0 {
				bulgu("baska_kullanici_claude", "d", "Bu bilgisayarda başka bir kullanıcı hesabında da Claude Code kullanılmış (sayılmıyor)")
			}
		}
	}
}

// Oturum etiketi: proje klasöründen sonraki ilk bileşenin uzantısız hali.
// Ana dosya ve alt ajanları aynı etiketi alır. Sunucu aynı formülü kullanır.
func oturumSid(yol, kok string) string {
	rel, err := filepath.Rel(kok, yol)
	if err != nil {
		rel = yol
	}
	parca := strings.Split(filepath.ToSlash(rel), "/")
	ad := parca[0]
	if len(parca) > 1 {
		ad = parca[1]
	}
	ad = strings.TrimSuffix(ad, ".jsonl")
	h := sha256.Sum256([]byte("token-panosu-sid|" + ad))
	return hex.EncodeToString(h[:])[:16]
}

// ---------- satır ayrıştırma ----------

type kullanimJSON struct {
	Input         float64 `json:"input_tokens"`
	Output        float64 `json:"output_tokens"`
	CacheCreation float64 `json:"cache_creation_input_tokens"`
	CacheRead     float64 `json:"cache_read_input_tokens"`
	Ayrinti       *struct {
		W5 float64 `json:"ephemeral_5m_input_tokens"`
		W1 float64 `json:"ephemeral_1h_input_tokens"`
	} `json:"cache_creation"`
}

type satirJSON struct {
	Timestamp string  `json:"timestamp"`
	RequestID *string `json:"requestId"`
	Message   *struct {
		ID    *string       `json:"id"`
		Model string        `json:"model"`
		Usage *kullanimJSON `json:"usage"`
	} `json:"message"`
}

var (
	usageIsareti = []byte(`"usage"`)
	tsIsareti    = []byte(`"timestamp":"`)
)

func rakam(b byte) bool { return b >= '0' && b <= '9' }

// `"timestamp":"YYYY-MM-DDTHH:MM:SS..."` değerini JSON çözmeden bulur (eski satırları ucuzca atlamak için)
func tsOnSuzgec(satir []byte) string {
	i := 0
	for {
		j := bytes.Index(satir[i:], tsIsareti)
		if j < 0 {
			return ""
		}
		bas := i + j + len(tsIsareti)
		i = bas
		v := satir[bas:]
		if len(v) < 19 {
			continue
		}
		ok := true
		for k, c := range v[:19] {
			switch k {
			case 4, 7:
				ok = ok && c == '-'
			case 10:
				ok = ok && c == 'T'
			case 13, 16:
				ok = ok && c == ':'
			default:
				ok = ok && rakam(c)
			}
		}
		if !ok {
			continue
		}
		son := bytes.IndexByte(v, '"')
		if son < 19 {
			continue
		}
		return string(v[:son])
	}
}

func anahtarOzeti(id, istek *string) uint64 {
	if id == nil && istek == nil {
		return 0
	}
	h := fnv.New64a()
	for _, s := range []*string{id, istek} {
		if s == nil {
			h.Write([]byte{0})
		} else {
			h.Write([]byte{1})
			h.Write([]byte(*s))
		}
		h.Write([]byte{0xff})
	}
	if v := h.Sum64(); v != 0 {
		return v
	}
	return 1
}

type okuyucu struct {
	sinir    int64
	sinirISO string
	modeller *[]string
	modelNo  map[string]uint16
}

func (r *okuyucu) model(ad string) uint16 {
	if n, ok := r.modelNo[ad]; ok {
		return n
	}
	n := uint16(len(*r.modeller))
	*r.modeller = append(*r.modeller, ad)
	r.modelNo[ad] = n
	return n
}

func (r *okuyucu) satir(b []byte) (kayit, bool) {
	var k kayit
	if !bytes.Contains(b, usageIsareti) {
		return k, false
	}
	if ts := tsOnSuzgec(b); ts != "" && strings.HasSuffix(ts, "Z") && ts[:19] < r.sinirISO {
		return k, false
	}
	var o satirJSON
	if json.Unmarshal(b, &o) != nil || o.Message == nil || o.Message.Usage == nil || o.Timestamp == "" {
		return k, false
	}
	model := o.Message.Model
	if !strings.HasPrefix(model, "claude-") {
		return k, false
	}
	t, err := time.Parse(time.RFC3339Nano, o.Timestamp)
	if err != nil {
		return k, false
	}
	k.T = t.Unix()
	if k.T < r.sinir {
		return k, false
	}
	if len(model) > 64 {
		model = model[:64]
	}
	u := o.Message.Usage
	var w5, w1 int64
	if u.Ayrinti != nil {
		w5, w1 = int64(u.Ayrinti.W5), int64(u.Ayrinti.W1)
	}
	toplamYazma := int64(u.CacheCreation)
	if w5+w1 < toplamYazma { // ayrıntı yoksa 5 dk say
		w5 += toplamYazma - (w5 + w1)
	}
	k.Model = r.model(model)
	k.Anahtar = anahtarOzeti(o.Message.ID, o.RequestID)
	k.In, k.Out, k.W5, k.W1, k.Cr = int64(u.Input), int64(u.Output), w5, w1, int64(u.CacheRead)
	k.Buyuk = k.Out > 200_000 || k.In+k.Cr+toplamYazma > 2_000_000
	return k, true
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

// Dosyayı (gerekiyorsa) okur ve önbellek kaydını günceller. nil: dosya açılamadı.
func (r *okuyucu) dosya(yol string, st fs.FileInfo, eski *dosyaKaydi) (*dosyaKaydi, bool) {
	boyut, mt := st.Size(), st.ModTime().UnixNano()
	if eski != nil && eski.Boyut == boyut && eski.Mtime == mt {
		return eski, false
	}
	f, err := os.Open(yol)
	if err != nil {
		return nil, false
	}
	defer f.Close()

	d := eski
	basla := int64(0)
	// Yalnızca büyümüşse kaldığı yerden devam et; aynı boyutta değişmiş ya da kısalmış dosya baştan okunur
	if d != nil && d.Ofset > 0 && boyut > d.Boyut && boyut >= d.Ofset &&
		basOzeti(f, d.Ofset) == d.Bas && kuyrukOzeti(f, d.Ofset) == d.Kuyruk {
		basla = d.Ofset // yalnızca ekleme yapılmış: kaldığı yerden devam
	} else {
		d = &dosyaKaydi{}
	}
	if _, err := f.Seek(basla, io.SeekStart); err != nil {
		return nil, false
	}
	br := bufio.NewReaderSize(f, 256*1024)
	poz := basla
	for {
		b, err := br.ReadSlice('\n')
		if err == bufio.ErrBufferFull { // çok uzun satır: tamamını topla
			uzun := append([]byte(nil), b...)
			for err == bufio.ErrBufferFull {
				b, err = br.ReadSlice('\n')
				uzun = append(uzun, b...)
			}
			b = uzun
		}
		if len(b) > 0 && b[len(b)-1] == '\n' {
			if k, ok := r.satir(b); ok {
				d.Kayitlar = append(d.Kayitlar, k)
			}
			poz += int64(len(b))
		} else if len(b) > 0 {
			// Sonunda satır sonu olmayan parça: tam bir JSON ise işle, değilse (yazılıyor) sonraya bırak
			if json.Valid(bytes.TrimSpace(b)) {
				if k, ok := r.satir(b); ok {
					d.Kayitlar = append(d.Kayitlar, k)
				}
				poz += int64(len(b))
			}
		}
		if err != nil {
			break
		}
	}
	d.Boyut, d.Mtime, d.Ofset = boyut, mt, poz
	d.Bas, d.Kuyruk = basOzeti(f, poz), kuyrukOzeti(f, poz)
	return d, true
}

// ---------- toplama ----------

type sayac struct{ in, out, w5, w1, cr, msg int64 }

func (s *sayac) ekle(k *kayit) {
	s.in += k.In
	s.out += k.Out
	s.w5 += k.W5
	s.w1 += k.W1
	s.cr += k.Cr
	s.msg++
}

// sidBirlesim: sid'ler üzerinde union-find
type sidBirlesim map[string]string

func (u sidBirlesim) bul(x string) string {
	if _, ok := u[x]; !ok {
		u[x] = x
		return x
	}
	for u[x] != x {
		u[x] = u[u[x]]
		x = u[x]
	}
	return x
}

func (u sidBirlesim) birlestir(a, b string) {
	ra, rb := u.bul(a), u.bul(b)
	if ra == rb {
		return
	}
	if rb < ra { // deterministik temsilci
		ra, rb = rb, ra
	}
	u[rb] = ra
}

type grupAnahtari struct {
	zaman int64
	model string
	sid   string
}

func topla(simdi int64) sonuc {
	bas := time.Now()
	bulgular = nil
	sinir := (simdi/3600)*3600 - (saatPenceresi-1)*3600
	r := &okuyucu{sinir: sinir, sinirISO: time.Unix(sinir, 0).UTC().Format("2006-01-02T15:04:05")}

	klasorler := kaynaklar()
	ortamKontrol(klasorler)

	onb := onbellekYukle()
	r.modeller = &onb.Modeller
	r.modelNo = map[string]uint16{}
	for i, m := range onb.Modeller {
		r.modelNo[m] = uint16(i)
	}

	type dosyaIsi struct {
		d   *dosyaKaydi
		sid string
		mt  float64
	}
	var isler []dosyaIsi
	yeni := map[string]*dosyaKaydi{}
	okunan := 0
	kuculen := false
	for _, kok := range klasorler {
		_ = filepath.WalkDir(kok, func(yol string, e fs.DirEntry, err error) error {
			if err != nil {
				if errors.Is(err, fs.ErrPermission) {
					bulgu("engel_claude", "s", "Claude kayıt klasörü veya alt dizini okunamıyor: kullanım gizleniyor olabilir")
				}
				if e != nil && e.IsDir() && yol != kok {
					return fs.SkipDir
				}
				return nil
			}
			if yol != kok && strings.HasPrefix(e.Name(), ".") {
				if e.IsDir() {
					return fs.SkipDir
				}
				return nil
			}
			if e.IsDir() || !strings.HasSuffix(e.Name(), ".jsonl") {
				return nil
			}
			if dosyaEngelMi(yol) {
				bulgu("engel_claude", "s", "Claude oturum kayıt dosyası okunamıyor ya da /dev/null vb. hedefe yönlendirilmiş")
				return nil
			}
			st, err := os.Stat(yol) // bağlantıları izle
			if err != nil || !st.Mode().IsRegular() {
				return nil
			}
			mt := float64(st.ModTime().UnixNano()) / 1e9
			if mt < float64(sinir) {
				return nil
			}
			if _, var_ := yeni[yol]; var_ {
				return nil
			}
			eskiKayit := onb.Dosyalar[yol]
			var eskiTok int64
			if eskiKayit != nil {
				for _, k := range eskiKayit.Kayitlar {
					if k.T >= sinir {
						eskiTok += k.In + k.Out + k.W5 + k.W1 + k.Cr
					}
				}
			}
			d, okundu := r.dosya(yol, st, eskiKayit)
			if d == nil {
				return nil
			}
			if okundu {
				okunan++
			}
			// Pencere dışına düşen kayıtları buda
			var yeniTok int64
			kalan := d.Kayitlar[:0]
			for _, k := range d.Kayitlar {
				if k.T >= sinir {
					kalan = append(kalan, k)
					yeniTok += k.In + k.Out + k.W5 + k.W1 + k.Cr
				}
			}
			d.Kayitlar = kalan
			if eskiTok-yeniTok >= 1000 {
				kuculen = true
			}
			yeni[yol] = d
			isler = append(isler, dosyaIsi{d: d, sid: oturumSid(yol, kok), mt: mt})
			return nil
		})
	}
	eskiDosyalar := onb.Dosyalar
	onb.Dosyalar = yeni

	// Aynı mesaj (message.id + requestId) birden çok dosyada olabilir: devam ettirilen ya da çatallanan
	// oturumlar ve alt ajanlar geçmişi kopyalar, kopyalarda kullanım sıfır ya da eksik olabilir.
	// Her mesaj bir kez sayılır: kullanımı en büyük olan kopya (eşitlikte en eski dosya) asıl kabul edilir.
	sort.SliceStable(isler, func(i, j int) bool { return isler[i].mt < isler[j].mt })
	type konum struct {
		dosya, sira int
		toplam      int64
	}
	asil := map[uint64]konum{}
	// Etiket devralma: ortak mesaj taşıyan dosyalar aynı sohbetin parçasıdır, sid'leri birleştirilir.
	birlesim := sidBirlesim{}
	ilkSid := map[uint64]string{}
	for _, is := range isler {
		birlesim.bul(is.sid)
	}
	for sid, e := range onb.Devir { // kalıcı bağlar (asıl dosya artık görünmese de)
		if e.Son < simdi-devirBudamaSn {
			delete(onb.Devir, sid)
			continue
		}
		if _, var_ := birlesim[sid]; var_ {
			birlesim.birlestir(sid, e.Kok)
		}
	}
	for di, is := range isler {
		for ki := range is.d.Kayitlar {
			k := &is.d.Kayitlar[ki]
			if k.Anahtar == 0 {
				continue
			}
			if ilk, var_ := ilkSid[k.Anahtar]; var_ {
				if ilk != is.sid {
					birlesim.birlestir(ilk, is.sid)
				}
			} else {
				ilkSid[k.Anahtar] = is.sid
			}
			t := k.In + k.Out + k.W5 + k.W1 + k.Cr
			if e, var_ := asil[k.Anahtar]; !var_ || t > e.toplam {
				asil[k.Anahtar] = konum{di, ki, t}
			}
		}
	}

	sidler := make([]sidMt, len(isler))
	for i, is := range isler {
		sidler[i] = sidMt{is.sid, is.mt}
	}
	kokOf := kokleriSec(birlesim, onb.Devir, sidler, simdi)

	saatlik := map[grupAnahtari]*sayac{}
	besDk := map[grupAnahtari]*sayac{}
	esikler := esikleriOku()
	var gelecek, editli, buyuk int
	for di, is := range isler {
		for ki := range is.d.Kayitlar {
			k := &is.d.Kayitlar[ki]
			if k.T > simdi+300 {
				gelecek++
			}
			if float64(k.T) > is.mt+3600 {
				editli++
			}
			if k.T > simdi+3600 {
				continue
			}
			if k.Anahtar != 0 {
				if e := asil[k.Anahtar]; e.dosya != di || e.sira != ki {
					continue
				}
			}
			if k.Buyuk {
				buyuk++
			}
			model := onb.Modeller[k.Model]
			sid := kokOf[is.sid]
			if e := esikBul(esikler, model); e > 0 && k.In+k.Cr+k.W5+k.W1 > e {
				// Sade satır da (sıfır olsa bile) gönderilir: daha önce gönderilmiş değeri sunucuda güncellensin
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
			s := saatlik[ga]
			if s == nil {
				s = &sayac{}
				saatlik[ga] = s
			}
			s.ekle(k)
			if simdi-3*3600 <= k.T && k.T <= simdi+300 {
				ga5 := grupAnahtari{k.T / 300 * 300, model, sid}
				s5 := besDk[ga5]
				if s5 == nil {
					s5 = &sayac{}
					besDk[ga5] = s5
				}
				s5.ekle(k)
			}
		}
	}
	var silinenTok int64
	yediGunOnce := simdi - 7*86400
	for yol, eskiD := range eskiDosyalar {
		if _, var_ := yeni[yol]; var_ || eskiD == nil {
			continue
		}
		if _, err := os.Stat(yol); !errors.Is(err, fs.ErrNotExist) {
			continue
		}
		for _, k := range eskiD.Kayitlar {
			if k.T < yediGunOnce {
				continue
			}
			if k.Anahtar != 0 {
				if _, var_ := asil[k.Anahtar]; var_ {
					continue
				}
			}
			silinenTok += k.In + k.Out + k.W5 + k.W1 + k.Cr
		}
	}
	if kuculen {
		bulgu("kuculen_claude", "s", "Claude oturum kayıt dosyasındaki geçmiş token kayıtları azaltılmış ya da kırpılmış")
	}
	if silinenTok >= 25_000 {
		bulgu("silinen_claude", "s", fmt.Sprintf("Son 7 güne ait silinmiş Claude oturum dosyalarında %d bin token tespit edildi", silinenTok/1000))
	}
	if gelecek > 0 {
		bulgu("gelecek_claude", "s", fmt.Sprintf("%d kayıt gelecek tarihli: bilgisayar saati ya da kayıt dosyası oynanmış olabilir", gelecek))
	}
	if editli > 0 {
		bulgu("editli_claude", "s", fmt.Sprintf("%d kaydın tarihi dosyanın değişme zamanından sonra: dosya sonradan düzenlenmiş olabilir", editli))
	}
	if buyuk > 0 {
		bulgu("buyuk_claude", "s", fmt.Sprintf("%d kayıtta imkânsız derecede büyük token değeri var", buyuk))
	}

	onbellekKaydet(onb)

	s := sonuc{satirlar: []Satir{}, dilimler: []Dilim{}, bulgular: bulgular, okunan: okunan, devir: devirCifti(kokOf)}
	if s.bulgular == nil {
		s.bulgular = []Bulgu{}
	}
	for ga, v := range saatlik {
		s.satirlar = append(s.satirlar, Satir{ga.zaman, ga.model, ga.sid, v.in, v.out, v.w5, v.w1, v.cr, v.msg})
	}
	for ga, v := range besDk {
		s.dilimler = append(s.dilimler, Dilim{ga.zaman, ga.model, ga.sid, v.in, v.out, v.w5, v.w1, v.cr, v.msg})
	}
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
	s.sure = time.Since(bas)
	return s
}

type sidMt struct {
	sid string
	mt  float64
}

// kokleriSec: her sid için sohbet kökünü seçer ve kalıcı devir kaydını günceller.
// Kök: gruptaki kalıcı kayıtların en eskisinin (Ilk) kökü; yoksa en eski mtime'lı dosyanın sid'i.
// Birden çok farklı kalıcı kök birleşirse hepsi en eskisine bağlanır.
// isler mtime'a göre artan sıralı olmalı.
func kokleriSec(u sidBirlesim, devir map[string]devirKaydi, isler []sidMt, simdi int64) map[string]string {
	// Bileşen başına en eski kalıcı kayıt
	type aday struct {
		ilk int64
		kok string
	}
	adaylar := map[string]aday{}
	for sid, e := range devir {
		var bilesen string
		if _, var_ := u[sid]; var_ {
			bilesen = u.bul(sid)
		} else if _, var_ := u[e.Kok]; var_ {
			bilesen = u.bul(e.Kok)
		} else {
			continue
		}
		a, var_ := adaylar[bilesen]
		if !var_ || e.Ilk < a.ilk || (e.Ilk == a.ilk && e.Kok < a.kok) {
			adaylar[bilesen] = aday{e.Ilk, e.Kok}
		}
	}
	kok := map[string]string{} // bileşen temsilcisi -> kök sid
	uyeler := map[string]int{} // bileşen -> görünen sid sayısı
	sidKok := make(map[string]string, len(isler))
	for _, is := range isler {
		b := u.bul(is.sid)
		if _, var_ := sidKok[is.sid]; !var_ {
			uyeler[b]++
			sidKok[is.sid] = ""
		}
		if _, var_ := kok[b]; !var_ {
			if a, var_ := adaylar[b]; var_ {
				kok[b] = a.kok
			} else {
				kok[b] = is.sid // en eski mtime'lı dosya (isler sıralı)
			}
		}
	}
	for sid := range sidKok {
		sidKok[sid] = kok[u.bul(sid)]
	}
	// Kalıcı kaydı güncelle: bileşende birden çok sid ya da zaten kayıt varsa
	yaz := func(sid, k string) {
		e, var_ := devir[sid]
		if !var_ {
			e.Ilk = simdi
		}
		e.Kok, e.Son = k, simdi
		devir[sid] = e
	}
	for sid, k := range sidKok {
		b := u.bul(sid)
		_, kayitli := devir[sid]
		_, kokKayitli := adaylar[b]
		if uyeler[b] > 1 || kayitli || kokKayitli {
			yaz(sid, k)
			if _, var_ := devir[k]; !var_ || devir[k].Kok != k {
				yaz(k, k) // kökün kendisi de kalıcı olsun
			}
		}
	}
	// Görünmeyen sid'lerin kayıtları da yeni köke yönlensin
	for sid, e := range devir {
		if _, var_ := u[e.Kok]; var_ {
			if k, ok := kok[u.bul(e.Kok)]; ok && e.Kok != k {
				e.Kok = k
				devir[sid] = e
			}
		}
	}
	return sidKok
}

// devirCifti: gönderilecek devir eşlemesi; yalnızca kökü kendisinden farklı sid'ler, en çok devirSinir çift (sıralı, kararlı)
const devirSinir = 200

func devirCifti(sidKok map[string]string) map[string]string {
	anahtarlar := make([]string, 0, len(sidKok))
	for sid, k := range sidKok {
		if sid != k {
			anahtarlar = append(anahtarlar, sid)
		}
	}
	sort.Strings(anahtarlar)
	if len(anahtarlar) > devirSinir {
		anahtarlar = anahtarlar[:devirSinir]
	}
	m := make(map[string]string, len(anahtarlar))
	for _, a := range anahtarlar {
		m[a] = sidKok[a]
	}
	return m
}
