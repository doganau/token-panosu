package main

// Antigravity okuyucu (Antigravity uygulaması, Antigravity IDE ve agy CLI).
//
// Antigravity her konuşma için yerelde bir döküm tutar:
//   ~/.gemini/antigravity[-ide|-cli]/brain/<konuşma>/.system_generated/logs/transcript.jsonl
// Model yanıtı adımlarında input_tokens (önbellek hariç girdi), cache_read_tokens ve output_tokens alanları bulunur.
// Seçili model, kullanıcı ayar değişikliği bildiriminden ("`Model Selection` from X to Y.") izlenir.
//
// Ayrıca masaüstü Antigravity / Antigravity IDE kurulumlarında yerel uygulama klasöründeki:
//   - app_storage.json -> "jetski.onboarding.lastLoginUsername" (yalnızca düz metin e-posta)
//   - User/globalStorage/state.vscdb -> ItemTable["antigravityUnifiedStateSync.userStatus"] (e-posta + kota yüzdesi)
// pasif olarak (saf Go ile, CGO olmadan) okunur.
//
// Gizlilik: hiçbir OAuth belirteci (oauthToken, jetski-standalone-oauth-token, oauth_creds.json), Anahtar Zinciri
// kaydı, istem veya yanıt metni ASLA okunmaz; ağ isteği atılmaz. E-posta adresi istemcide SHA-256 ile özetlenir.

import (
	"bufio"
	"bytes"
	"encoding/base64"
	"encoding/binary"
	"encoding/json"
	"errors"
	"io"
	"io/fs"
	"math"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"strconv"
	"strings"
	"time"
)

const (
	agyVarsayilanModel = "agy-gemini"
	agyAzamiDosya      = 512 << 20 // 512 MB üstü dökümler atlanır
	agyAzamiSatir      = 16 << 20  // tek satır sınırı
	agyAzamiVscdb      = 64 << 20  // 64 MB üstü state.vscdb dosyaları güvenlik için atlanır
	agyUserStatusKey   = "antigravityUnifiedStateSync.userStatus"
)

var agyModelRe = regexp.MustCompile("`Model Selection` from (.*?) to (.*?)\\.(?:\\s|$)")

// agyKaynaklari: Antigravity sürümlerinin brain klasörleri (yalnızca var olanlar).
func agyKaynaklari() []string {
	ev := evDizini()
	var liste []string
	gorulen := map[string]bool{}
	for _, ad := range []string{"antigravity", "antigravity-ide", "antigravity-cli"} {
		y := filepath.Join(ev, ".gemini", ad, "brain")
		if !klasorMu(y) {
			continue
		}
		g := gercekYol(y)
		if gorulen[g] {
			continue
		}
		gorulen[g] = true
		liste = append(liste, y)
	}
	return liste
}

// agyUygulamaDizinleri: Antigravity IDE ve Antigravity yerel uygulama verisi klasörleri.
func agyUygulamaDizinleri() []string {
	ev := evDizini()
	var adaylar []string
	ekle := func(y string) {
		if y != "" && klasorMu(y) {
			adaylar = append(adaylar, y)
		}
	}
	uygAdlari := []string{"Antigravity IDE", "Antigravity"}
	if testModu {
		for _, ad := range uygAdlari {
			ekle(filepath.Join(dizin, ad))
		}
	}
	for _, ad := range uygAdlari {
		switch runtime.GOOS {
		case "darwin":
			ekle(filepath.Join(ev, "Library", "Application Support", ad))
		case "windows":
			if ap := os.Getenv("APPDATA"); ap != "" && !testModu &&
				strings.HasPrefix(strings.ToLower(filepath.Clean(ap)), strings.ToLower(filepath.Clean(ev))) {
				ekle(filepath.Join(ap, ad))
			} else {
				ekle(filepath.Join(ev, "AppData", "Roaming", ad))
			}
		default:
			ekle(filepath.Join(ev, ".config", ad))
		}
	}
	return adaylar
}

// agyModelAd: "Claude Opus 5.5 (Medium)" -> "agy-claude-opus-5-5"; "Gemini 4 Carbon (High)" -> "agy-gemini-4-carbon"
func agyModelAd(goruntu string) string {
	s := strings.TrimSpace(goruntu)
	if i := strings.Index(s, "("); i >= 0 {
		s = strings.TrimSpace(s[:i])
	}
	s = strings.ToLower(s)
	if s == "" || s == "none" || s == "null" {
		return agyVarsayilanModel
	}
	if strings.HasPrefix(s, "claude") {
		s = strings.ReplaceAll(s, ".", "-")
	}
	m := modelTemizle(strings.ReplaceAll(s, " ", "-"))
	if m == "" {
		return agyVarsayilanModel
	}
	m = "agy-" + m
	if len(m) > 60 {
		m = m[:60]
	}
	return m
}

type agySatirJSON struct {
	Type      string `json:"type"`
	CreatedAt string `json:"created_at"`
	Content   string `json:"content"`
	Model     string `json:"model"`
	Input     *int64 `json:"input_tokens"`
	CacheRead int64  `json:"cache_read_tokens"`
	Output    int64  `json:"output_tokens"`
}

var (
	agyAnahtarToken = []byte(`"input_tokens"`)
	agyAnahtarModel = []byte("Model Selection")
)

func agyDosyaOku(yol, sidAnahtar string, st fs.FileInfo, eski *digerDosyaKaydi, sinir int64) (*digerDosyaKaydi, bool) {
	boyut, mt := st.Size(), st.ModTime().UnixNano()
	if eski != nil && eski.Boyut == boyut && eski.Mtime == mt {
		return eski, false
	}
	if boyut <= 0 || boyut > agyAzamiDosya {
		return nil, false
	}
	f, err := os.Open(yol)
	if err != nil {
		return nil, false
	}
	defer f.Close()

	d := &digerDosyaKaydi{Boyut: boyut, Mtime: mt, Sid: digerSid("agy", sidAnahtar), SonModel: agyVarsayilanModel}
	var bas int64
	// Yalnızca önceki kısım değişmeden sona ekleme yapıldıysa kaldığımız yerden devam et;
	// küçülmüş ya da ortası düzenlenmiş dosya baştan okunur.
	if eski != nil && eski.Ofset > 0 && boyut > eski.Boyut && boyut >= eski.Ofset && eski.Sid == d.Sid &&
		eski.Bas != 0 && basOzeti(f, eski.Ofset) == eski.Bas && kuyrukOzeti(f, eski.Ofset) == eski.Kuyruk {
		bas = eski.Ofset
		d.SonModel = eski.SonModel
		for _, k := range eski.Kayitlar {
			if k.T >= sinir {
				d.Kayitlar = append(d.Kayitlar, k)
			}
		}
	}
	if bas > 0 {
		if _, err := f.Seek(bas, io.SeekStart); err != nil {
			return nil, false
		}
	}
	r := bufio.NewReaderSize(f, 256<<10)
	ofset := bas
	for {
		satir, err := r.ReadBytes('\n')
		if len(satir) > 0 && satir[len(satir)-1] == '\n' {
			ofset += int64(len(satir))
			if len(satir) <= agyAzamiSatir {
				agySatirIsle(d, satir, sinir)
			}
		}
		if err != nil {
			break // yarım kalan son satır bir sonraki turda okunur
		}
	}
	d.Ofset = ofset
	if ofset > 0 {
		d.Bas, d.Kuyruk = basOzeti(f, ofset), kuyrukOzeti(f, ofset)
	}
	return d, true
}

func agySatirIsle(d *digerDosyaKaydi, satir []byte, sinir int64) {
	tokenli := bytes.Contains(satir, agyAnahtarToken)
	modelli := !tokenli && bytes.Contains(satir, agyAnahtarModel)
	if !tokenli && !modelli {
		return
	}
	var s agySatirJSON
	if json.Unmarshal(satir, &s) != nil {
		return
	}
	if modelli {
		if s.Type == "USER_INPUT" || s.Type == "SYSTEM_MESSAGE" {
			if m := agyModelRe.FindAllStringSubmatch(s.Content, -1); len(m) > 0 {
				d.SonModel = agyModelAd(m[len(m)-1][2])
			}
		}
		return
	}
	if s.Input == nil {
		return
	}
	if strings.TrimSpace(s.Model) != "" {
		d.SonModel = agyModelAd(s.Model)
	}
	t, err := time.Parse(time.RFC3339Nano, s.CreatedAt)
	if err != nil || t.Unix() < sinir {
		return
	}
	girdi, okuma, cikti := max(0, *s.Input), max(0, s.CacheRead), max(0, s.Output)
	if girdi == 0 && okuma == 0 && cikti == 0 {
		return
	}
	model := d.SonModel
	if model == "" {
		model = agyVarsayilanModel
	}
	d.Kayitlar = append(d.Kayitlar, digerKayit{T: t.Unix(), Model: model, In: girdi, Out: cikti, Cr: okuma})
}

// agyTopla: tüm Antigravity konuşma dökümlerini tarar; değişmeyen dosyalar önbellekten gelir.
func agyTopla(onb, yeniOnb map[string]*digerDosyaKaydi, sinir int64) (dosyalar []*digerDosyaKaydi, okunan int, kuculen, engel bool) {
	ev := evDizini()
	for _, ad := range []string{"antigravity", "antigravity-ide", "antigravity-cli"} {
		if yolEngelMi(filepath.Join(ev, ".gemini", ad)) || yolEngelMi(filepath.Join(ev, ".gemini", ad, "brain")) {
			engel = true
		}
	}
	for _, kok := range agyKaynaklari() {
		kokAd := filepath.Base(filepath.Dir(kok))
		girdiler, err := os.ReadDir(kok)
		if err != nil {
			if errors.Is(err, fs.ErrPermission) {
				engel = true
			}
			continue
		}
		for _, e := range girdiler {
			if strings.HasPrefix(e.Name(), ".") {
				continue
			}
			konusmaDir := filepath.Join(kok, e.Name())
			if !e.IsDir() {
				continue
			}
			sysDir := filepath.Join(konusmaDir, ".system_generated")
			logDir := filepath.Join(sysDir, "logs")
			yol := filepath.Join(logDir, "transcript.jsonl")
			if yolEngelMi(konusmaDir) || yolEngelMi(sysDir) || yolEngelMi(logDir) || dosyaEngelMi(yol) {
				engel = true
				continue
			}
			st, err := os.Stat(yol)
			if err != nil || !st.Mode().IsRegular() {
				continue
			}
			if float64(st.ModTime().UnixNano())/1e9 < float64(sinir) {
				continue
			}
			if _, var_ := yeniOnb[yol]; var_ {
				continue
			}
			eski := onb[yol]
			var eskiTok int64
			if eski != nil {
				for _, k := range eski.Kayitlar {
					if k.T >= sinir {
						eskiTok += k.In + k.Out + k.W5 + k.Cr
					}
				}
			}
			d, okundu := agyDosyaOku(yol, kokAd+"/"+e.Name(), st, eski, sinir)
			if d == nil {
				continue
			}
			if okundu {
				okunan++
			}
			var yeniTok int64
			for _, k := range d.Kayitlar {
				if k.T >= sinir {
					yeniTok += k.In + k.Out + k.W5 + k.Cr
				}
			}
			if eskiTok-yeniTok >= 1000 {
				kuculen = true
			}
			yeniOnb[yol] = d
			dosyalar = append(dosyalar, d)
		}
	}
	return dosyalar, okunan, kuculen, engel
}

// ---------- saf Go SQLite3 (ItemTable tek-anahtar + WAL + taşma sayfası) okuyucu ----------

func sqliteVarint(b []byte) (uint64, int) {
	var v uint64
	for i := 0; i < len(b) && i < 9; i++ {
		if i == 8 {
			return (v << 8) | uint64(b[8]), 9
		}
		v = (v << 7) | uint64(b[i]&0x7f)
		if b[i]&0x80 == 0 {
			return v, i + 1
		}
	}
	return 0, 0
}

func sqliteDegerUzunlugu(st uint64) (int, bool) {
	switch st {
	case 0, 8, 9:
		return 0, true
	case 1:
		return 1, true
	case 2:
		return 2, true
	case 3:
		return 3, true
	case 4:
		return 4, true
	case 5:
		return 6, true
	case 6, 7:
		return 8, true
	case 10, 11:
		return 0, false
	default:
		if st >= 12 && st%2 == 0 {
			return int((st - 12) / 2), true
		}
		return int((st - 13) / 2), true
	}
}

func sqliteTamSayiOku(st uint64, b []byte) (int64, bool) {
	switch st {
	case 8:
		return 0, true
	case 9:
		return 1, true
	case 1:
		if len(b) >= 1 {
			return int64(int8(b[0])), true
		}
	case 2:
		if len(b) >= 2 {
			return int64(int16(binary.BigEndian.Uint16(b[:2]))), true
		}
	case 3:
		if len(b) >= 3 {
			v := int32(b[0])<<16 | int32(b[1])<<8 | int32(b[2])
			if v&0x800000 != 0 {
				v |= ^0xffffff
			}
			return int64(v), true
		}
	case 4:
		if len(b) >= 4 {
			return int64(int32(binary.BigEndian.Uint32(b[:4]))), true
		}
	case 5:
		if len(b) >= 6 {
			v := int64(b[0])<<40 | int64(b[1])<<32 | int64(b[2])<<24 | int64(b[3])<<16 | int64(b[4])<<8 | int64(b[5])
			if v&(1<<47) != 0 {
				v |= ^((1 << 48) - 1)
			}
			return v, true
		}
	case 6:
		if len(b) >= 8 {
			return int64(binary.BigEndian.Uint64(b[:8])), true
		}
	}
	return 0, false
}

// sqliteItemTableDegerOku: state.vscdb içindeki ItemTable tablosundan YALNIZCA verilen anahtarı okur.
// Eşleşmeyen satırların değer baytlarına ya da taşma sayfalarına kesinlikle dokunulmaz.
func sqliteItemTableDegerOku(yol, arananAnahtar string) ([]byte, int64, bool) {
	st, err := os.Stat(yol)
	if err != nil || !st.Mode().IsRegular() || st.Size() < 100 || st.Size() > agyAzamiVscdb {
		return nil, 0, false
	}
	mtNano := st.ModTime().UnixNano()
	db, err := os.ReadFile(yol)
	if err != nil || len(db) < 100 || string(db[:16]) != "SQLite format 3\x00" {
		return nil, 0, false
	}
	ps := int(binary.BigEndian.Uint16(db[16:18]))
	if ps == 1 {
		ps = 65536
	}
	if ps < 512 || ps > 65536 || (ps&(ps-1)) != 0 {
		return nil, 0, false
	}
	usable := ps - int(db[20])
	if usable < 480 {
		return nil, 0, false
	}

	walSayfalar := map[uint32][]byte{}
	if wst, errW := os.Stat(yol + "-wal"); errW == nil && wst.Mode().IsRegular() && wst.Size() >= 32 && wst.Size() <= 32<<20 {
		if wst.ModTime().UnixNano() > mtNano {
			mtNano = wst.ModTime().UnixNano()
		}
		if wal, errR := os.ReadFile(yol + "-wal"); errR == nil && len(wal) >= 32 {
			magic := binary.BigEndian.Uint32(wal[0:4])
			wps := int(binary.BigEndian.Uint32(wal[8:12]))
			if (magic == 0x377f0682 || magic == 0x377f0683) && wps == ps {
				s1 := binary.BigEndian.Uint32(wal[16:20])
				s2 := binary.BigEndian.Uint32(wal[20:24])
				cerceveBoy := 24 + ps
				for off := 32; off+cerceveBoy <= len(wal); off += cerceveBoy {
					pgNo := binary.BigEndian.Uint32(wal[off : off+4])
					fs1 := binary.BigEndian.Uint32(wal[off+8 : off+12])
					fs2 := binary.BigEndian.Uint32(wal[off+12 : off+16])
					if pgNo >= 1 && fs1 == s1 && fs2 == s2 {
						walSayfalar[pgNo] = wal[off+24 : off+24+ps]
					}
				}
			}
		}
	}

	sayfaGetir := func(pgNo uint32) ([]byte, bool) {
		if pgNo == 0 {
			return nil, false
		}
		if p, var_ := walSayfalar[pgNo]; var_ {
			return p, true
		}
		bas := int64(pgNo-1) * int64(ps)
		if bas < 0 || bas+int64(ps) > int64(len(db)) {
			return nil, false
		}
		return db[bas : bas+int64(ps)], true
	}

	yerelBoyut := func(payload int) int {
		maxLocal := usable - 35
		if payload <= maxLocal {
			return payload
		}
		minLocal := ((usable - 12) * 32 / 255) - 23
		k := minLocal + ((payload - minLocal) % (usable - 4))
		if k <= maxLocal {
			return k
		}
		return minLocal
	}

	tasmaBirlestir := func(yerel []byte, tasmaIlk uint32, toplam int) ([]byte, bool) {
		if toplam <= len(yerel) {
			return yerel[:toplam], true
		}
		if toplam > 16<<20 {
			return nil, false
		}
		tam := make([]byte, 0, toplam)
		tam = append(tam, yerel...)
		ov := tasmaIlk
		goruldu := map[uint32]bool{}
		for adim := 0; ov != 0 && len(tam) < toplam && adim < 512; adim++ {
			if goruldu[ov] {
				return nil, false
			}
			goruldu[ov] = true
			opg, ok := sayfaGetir(ov)
			if !ok || len(opg) < usable {
				return nil, false
			}
			sonraki := binary.BigEndian.Uint32(opg[0:4])
			alinacak := usable - 4
			if kalan := toplam - len(tam); kalan < alinacak {
				alinacak = kalan
			}
			tam = append(tam, opg[4:4+alinacak]...)
			ov = sonraki
		}
		if len(tam) < toplam {
			return nil, false
		}
		return tam, true
	}

	// 1. sqlite_schema (kök sayfa 1) içinden ItemTable tablosunun kök sayfasını bul
	var itemKok uint32
	semaZiyaret := map[uint32]bool{}
	var semaGez func(pgNo uint32)
	semaGez = func(pgNo uint32) {
		if itemKok != 0 || pgNo == 0 || semaZiyaret[pgNo] || len(semaZiyaret) > 256 {
			return
		}
		semaZiyaret[pgNo] = true
		sayfa, ok := sayfaGetir(pgNo)
		if !ok {
			return
		}
		hdr := 0
		if pgNo == 1 {
			hdr = 100
		}
		if hdr+8 > len(sayfa) {
			return
		}
		tur := sayfa[hdr]
		hucreSayi := int(binary.BigEndian.Uint16(sayfa[hdr+3 : hdr+5]))
		switch tur {
		case 0x05:
			if hdr+12+2*hucreSayi > len(sayfa) {
				return
			}
			for i := 0; i < hucreSayi; i++ {
				cp := int(binary.BigEndian.Uint16(sayfa[hdr+12+2*i : hdr+14+2*i]))
				if cp+4 <= len(sayfa) {
					semaGez(binary.BigEndian.Uint32(sayfa[cp : cp+4]))
				}
			}
			semaGez(binary.BigEndian.Uint32(sayfa[hdr+8 : hdr+12]))
		case 0x0d:
			if hdr+8+2*hucreSayi > len(sayfa) {
				return
			}
			for i := 0; i < hucreSayi; i++ {
				cp := int(binary.BigEndian.Uint16(sayfa[hdr+8+2*i : hdr+10+2*i]))
				if cp >= usable {
					continue
				}
				pUz, n1 := sqliteVarint(sayfa[cp:usable])
				if n1 == 0 || pUz == 0 || pUz > 1<<20 {
					continue
				}
				_, n2 := sqliteVarint(sayfa[cp+n1 : usable])
				if n2 == 0 {
					continue
				}
				recBas := cp + n1 + n2
				yerelUz := yerelBoyut(int(pUz))
				if recBas+yerelUz > usable {
					continue
				}
				yerel := sayfa[recBas : recBas+yerelUz]
				hUz, nh := sqliteVarint(yerel)
				if nh == 0 || int(hUz) > len(yerel) {
					continue
				}
				// sqlite_schema: (type, name, tbl_name, rootpage, sql)
				var stler [4]uint64
				hKonum := nh
				tamam := true
				for k := 0; k < 4; k++ {
					if hKonum >= int(hUz) {
						tamam = false
						break
					}
					stK, nk := sqliteVarint(yerel[hKonum:int(hUz)])
					if nk == 0 {
						tamam = false
						break
					}
					stler[k] = stK
					hKonum += nk
				}
				if !tamam {
					continue
				}
				gKonum := int(hUz)
				l0, ok0 := sqliteDegerUzunlugu(stler[0])
				l1, ok1 := sqliteDegerUzunlugu(stler[1])
				l2, ok2 := sqliteDegerUzunlugu(stler[2])
				l3, ok3 := sqliteDegerUzunlugu(stler[3])
				if !ok0 || !ok1 || !ok2 || !ok3 || gKonum+l0+l1+l2+l3 > len(yerel) {
					continue
				}
				tipStr := string(yerel[gKonum : gKonum+l0])
				adStr := string(yerel[gKonum+l0 : gKonum+l0+l1])
				if tipStr == "table" && adStr == "ItemTable" {
					if kok, okK := sqliteTamSayiOku(stler[3], yerel[gKonum+l0+l1+l2:gKonum+l0+l1+l2+l3]); okK && kok >= 2 {
						itemKok = uint32(kok)
						return
					}
				}
			}
		}
	}
	semaGez(1)
	if itemKok == 0 {
		return nil, 0, false
	}

	// 2. ItemTable B-ağacında yalnızca arananAnahtar hücresini bul ve birleştir
	var bulunan []byte
	tabloZiyaret := map[uint32]bool{}
	var tabloGez func(pgNo uint32)
	tabloGez = func(pgNo uint32) {
		if bulunan != nil || pgNo == 0 || tabloZiyaret[pgNo] || len(tabloZiyaret) > 2048 {
			return
		}
		tabloZiyaret[pgNo] = true
		sayfa, ok := sayfaGetir(pgNo)
		if !ok {
			return
		}
		hdr := 0
		if pgNo == 1 {
			hdr = 100
		}
		if hdr+8 > len(sayfa) {
			return
		}
		tur := sayfa[hdr]
		hucreSayi := int(binary.BigEndian.Uint16(sayfa[hdr+3 : hdr+5]))
		switch tur {
		case 0x05:
			if hdr+12+2*hucreSayi > len(sayfa) {
				return
			}
			for i := 0; i < hucreSayi && bulunan == nil; i++ {
				cp := int(binary.BigEndian.Uint16(sayfa[hdr+12+2*i : hdr+14+2*i]))
				if cp+4 <= len(sayfa) {
					tabloGez(binary.BigEndian.Uint32(sayfa[cp : cp+4]))
				}
			}
			if bulunan == nil {
				tabloGez(binary.BigEndian.Uint32(sayfa[hdr+8 : hdr+12]))
			}
		case 0x0d:
			if hdr+8+2*hucreSayi > len(sayfa) {
				return
			}
			for i := 0; i < hucreSayi; i++ {
				cp := int(binary.BigEndian.Uint16(sayfa[hdr+8+2*i : hdr+10+2*i]))
				if cp >= usable {
					continue
				}
				pUz, n1 := sqliteVarint(sayfa[cp:usable])
				if n1 == 0 || pUz == 0 || pUz > 16<<20 {
					continue
				}
				_, n2 := sqliteVarint(sayfa[cp+n1 : usable])
				if n2 == 0 {
					continue
				}
				recBas := cp + n1 + n2
				toplam := int(pUz)
				yerelUz := yerelBoyut(toplam)
				ekTasma := 0
				if toplam > yerelUz {
					ekTasma = 4
				}
				if recBas+yerelUz+ekTasma > usable {
					continue
				}
				yerel := sayfa[recBas : recBas+yerelUz]
				hUz, nh := sqliteVarint(yerel)
				if nh == 0 || int(hUz) > len(yerel) {
					continue
				}
				stAnahtar, nk1 := sqliteVarint(yerel[nh:int(hUz)])
				if nk1 == 0 {
					continue
				}
				stDeger, nk2 := sqliteVarint(yerel[nh+nk1 : int(hUz)])
				if nk2 == 0 {
					continue
				}
				kUz, okK := sqliteDegerUzunlugu(stAnahtar)
				vUz, okV := sqliteDegerUzunlugu(stDeger)
				if !okK || !okV || kUz != len(arananAnahtar) {
					continue
				}
				hSon := int(hUz)
				if hSon+kUz > len(yerel) || string(yerel[hSon:hSon+kUz]) != arananAnahtar {
					continue // başka anahtar: değer baytlarına ya da taşma sayfalarına dokunma
				}
				var tasmaIlk uint32
				if toplam > yerelUz {
					tasmaIlk = binary.BigEndian.Uint32(sayfa[recBas+yerelUz : recBas+yerelUz+4])
				}
				tam, okT := tasmaBirlestir(yerel, tasmaIlk, toplam)
				if !okT || hSon+kUz+vUz > len(tam) {
					return
				}
				bulunan = append([]byte(nil), tam[hSon+kUz:hSon+kUz+vUz]...)
				return
			}
		}
	}
	tabloGez(itemKok)
	if bulunan == nil {
		return nil, 0, false
	}
	return bulunan, mtNano, true
}

// ---------- saf Go Protobuf (yalnızca userStatusSentinelKey -> e-posta + model kotaları) ----------

func pbVarint(b []byte) (uint64, int) {
	var v uint64
	for i := 0; i < len(b) && i < 10; i++ {
		v |= uint64(b[i]&0x7f) << (7 * i)
		if b[i]&0x80 == 0 {
			return v, i + 1
		}
	}
	return 0, 0
}

func pbAlanOku(b []byte) (alanNo uint64, tel uint64, deger []byte, varintDeg uint64, sabit32 uint32, tuketilen int, tamam bool) {
	etiket, n := pbVarint(b)
	if n == 0 {
		return 0, 0, nil, 0, 0, 0, false
	}
	alanNo, tel = etiket>>3, etiket&7
	kalan := b[n:]
	switch tel {
	case 0:
		v, nv := pbVarint(kalan)
		if nv == 0 {
			return 0, 0, nil, 0, 0, 0, false
		}
		return alanNo, tel, nil, v, 0, n + nv, true
	case 1:
		if len(kalan) < 8 {
			return 0, 0, nil, 0, 0, 0, false
		}
		return alanNo, tel, kalan[:8], 0, 0, n + 8, true
	case 2:
		uz, nu := pbVarint(kalan)
		if nu == 0 || uz > uint64(len(kalan)-nu) {
			return 0, 0, nil, 0, 0, 0, false
		}
		return alanNo, tel, kalan[nu : nu+int(uz)], 0, 0, n + nu + int(uz), true
	case 5:
		if len(kalan) < 4 {
			return 0, 0, nil, 0, 0, 0, false
		}
		return alanNo, tel, nil, 0, binary.LittleEndian.Uint32(kalan[:4]), n + 4, true
	default:
		return 0, 0, nil, 0, 0, 0, false
	}
}

func b64TemizCoz(ham []byte) ([]byte, bool) {
	s := strings.TrimSpace(strings.Trim(string(ham), "\x00"))
	if s == "" {
		return nil, false
	}
	if b, err := base64.StdEncoding.DecodeString(s); err == nil {
		return b, true
	}
	if b, err := base64.RawStdEncoding.DecodeString(strings.TrimRight(s, "=")); err == nil {
		return b, true
	}
	return nil, false
}

func pbIlkMetinAlan(b []byte, hedefAlan uint64) string {
	for len(b) > 0 {
		no, tel, deg, _, _, n, ok := pbAlanOku(b)
		if !ok {
			return ""
		}
		if no == hedefAlan && tel == 2 {
			return string(deg)
		}
		b = b[n:]
	}
	return ""
}

func agyKotaBilgisiCoz(b []byte) (kalanOran float64, resetSn int64, gecerli bool) {
	var resetVar bool
	for len(b) > 0 {
		no, tel, deg, _, f32, n, ok := pbAlanOku(b)
		if !ok {
			return 0, 0, false
		}
		switch {
		case no == 1 && tel == 5:
			f := float64(math.Float32frombits(f32))
			if !math.IsNaN(f) && !math.IsInf(f, 0) {
				kalanOran = f
			}
		case no == 2 && tel == 2:
			for tb := deg; len(tb) > 0; {
				tno, ttel, _, tv, _, tn, tok := pbAlanOku(tb)
				if !tok {
					break
				}
				if tno == 1 && ttel == 0 && tv > 0 {
					resetSn = int64(tv)
					resetVar = true
				}
				tb = tb[tn:]
			}
		}
		b = b[n:]
	}
	if !resetVar || resetSn <= 0 {
		return 0, 0, false
	}
	if kalanOran < 0 {
		kalanOran = 0
	} else if kalanOran > 1 {
		kalanOran = 1
	}
	return kalanOran, resetSn, true
}

func agyUserStatusCoz(disB64 []byte, mtSn, simdi int64) (eposta string, limitR map[string]string, limitVar bool, haftaReset int64) {
	disPB, ok := b64TemizCoz(disB64)
	if !ok {
		return "", nil, false, 0
	}
	var icB64 string
	for b := disPB; len(b) > 0; {
		no, tel, deg, _, _, n, tamam := pbAlanOku(b)
		if !tamam {
			break
		}
		if no == 1 && tel == 2 {
			var k string
			var sarmal []byte
			for eb := deg; len(eb) > 0; {
				eno, etel, edeg, _, _, en, etamam := pbAlanOku(eb)
				if !etamam {
					break
				}
				if eno == 1 && etel == 2 {
					k = string(edeg)
				} else if eno == 2 && etel == 2 {
					sarmal = edeg
				}
				eb = eb[en:]
			}
			if k == "userStatusSentinelKey" && len(sarmal) > 0 {
				icB64 = pbIlkMetinAlan(sarmal, 1)
				break
			}
		}
		b = b[n:]
	}
	if icB64 == "" {
		return "", nil, false, 0
	}
	icPB, ok := b64TemizCoz([]byte(icB64))
	if !ok {
		return "", nil, false, 0
	}

	var wPct, hPct, claudePct, geminiPct float64
	var wReset, hReset, gecmisWReset int64
	var wVar, hVar, claudeVar, geminiVar bool

	for b := icPB; len(b) > 0; {
		no, tel, deg, _, _, n, tamam := pbAlanOku(b)
		if !tamam {
			break
		}
		switch {
		case no == 7 && tel == 2:
			aday := strings.TrimSpace(string(deg))
			if strings.Contains(aday, "@") && !strings.ContainsAny(aday, " \t\r\n") {
				eposta = aday
			}
		case no == 33 && tel == 2:
			for cb := deg; len(cb) > 0; {
				cno, ctel, cdeg, _, _, cn, ctamam := pbAlanOku(cb)
				if !ctamam {
					break
				}
				if cno == 1 && ctel == 2 {
					var etiket string
					var kotaHam []byte
					for mb := cdeg; len(mb) > 0; {
						mno, mtel, mdeg, _, _, mn, mtamam := pbAlanOku(mb)
						if !mtamam {
							break
						}
						if mno == 1 && mtel == 2 {
							etiket = strings.ToLower(strings.TrimSpace(string(mdeg)))
						} else if mno == 15 && mtel == 2 {
							kotaHam = mdeg
						}
						mb = mb[mn:]
					}
					if len(kotaHam) > 0 {
						kalan, resetSn, gecerli := agyKotaBilgisiCoz(kotaHam)
						if gecerli && resetSn > mtSn {
							sure := resetSn - mtSn
							// 7 günlük pencere sıfırlama saati: pencere geçmiş olsa bile son 14 gün içindeyse bir sonraki haftalık sıfırlamayı verir
							if sure > 8*3600 && sure <= 14*86400 && (simdi <= 0 || simdi-resetSn <= 14*86400) {
								if resetSn > gecmisWReset {
									gecmisWReset = resetSn
								}
							}
							// Kullanım yüzdesi için yalnızca sıfırlanma zamanı henüz geçmemiş (aktif pencere) kotaları kabul et
							if simdi <= 0 || (resetSn > simdi && simdi-mtSn <= 7*86400) {
								kullanim := math.Round((1.0-kalan)*1000) / 10
								if kullanim < 0 {
									kullanim = 0
								} else if kullanim > 100 {
									kullanim = 100
								}
								if sure <= 8*3600 {
									if !hVar || kullanim > hPct {
										hVar, hPct, hReset = true, kullanim, resetSn
									}
								} else if sure <= 14*86400 {
									if !wVar || kullanim > wPct {
										wVar, wPct, wReset = true, kullanim, resetSn
									}
									if strings.Contains(etiket, "claude") && (!claudeVar || kullanim > claudePct) {
										claudeVar, claudePct = true, kullanim
									}
									if strings.Contains(etiket, "gemini") && (!geminiVar || kullanim > geminiPct) {
										geminiVar, geminiPct = true, kullanim
									}
								}
							}
						}
					}
				}
				cb = cb[cn:]
			}
		}
		b = b[n:]
	}

	rm := map[string]string{}
	if wVar {
		rm["seven_day.used_percentage"] = strconv.FormatFloat(wPct, 'f', -1, 64)
		rm["seven_day.resets_at"] = strconv.FormatInt(wReset, 10)
		haftaReset = sonrakiHaftaReset(wReset, simdi)
	} else if gecmisWReset > 0 {
		haftaReset = sonrakiHaftaReset(gecmisWReset, simdi)
	}
	if hVar {
		rm["five_hour.used_percentage"] = strconv.FormatFloat(hPct, 'f', -1, 64)
		rm["five_hour.resets_at"] = strconv.FormatInt(hReset, 10)
	}
	if claudeVar {
		rm["agy_claude.used_percentage"] = strconv.FormatFloat(claudePct, 'f', -1, 64)
	}
	if geminiVar {
		rm["agy_gemini.used_percentage"] = strconv.FormatFloat(geminiPct, 'f', -1, 64)
	}
	return eposta, rm, len(rm) > 0, haftaReset
}

type agyDurumAdayi struct {
	eposta     string
	mtNano     int64
	limitT     int64
	limitR     map[string]string
	haftaReset int64
}

func agyDurumAdaylariOku(simdi int64) []agyDurumAdayi {
	var sonuc []agyDurumAdayi
	gorulen := map[string]bool{}
	for _, kok := range agyUygulamaDizinleri() {
		g := gercekYol(kok)
		if gorulen[g] {
			continue
		}
		gorulen[g] = true

		// 1. app_storage.json -> "jetski.onboarding.lastLoginUsername" (yalnızca düz metin e-posta)
		asYol := filepath.Join(kok, "app_storage.json")
		if st, err := os.Stat(asYol); err == nil && st.Mode().IsRegular() && st.Size() > 2 && st.Size() <= 8<<20 {
			if b, errR := os.ReadFile(asYol); errR == nil {
				var m map[string]json.RawMessage
				if json.Unmarshal(b, &m) == nil {
					if ham, var_ := m["jetski.onboarding.lastLoginUsername"]; var_ {
						var kullanici string
						if json.Unmarshal(ham, &kullanici) == nil {
							kullanici = strings.TrimSpace(kullanici)
							if kullanici != "" {
								sonuc = append(sonuc, agyDurumAdayi{
									eposta: kullanici,
									mtNano: st.ModTime().UnixNano(),
								})
							}
						}
					}
				}
			}
		}

		// 2. User/globalStorage/state.vscdb -> ItemTable["antigravityUnifiedStateSync.userStatus"]
		dbYol := filepath.Join(kok, "User", "globalStorage", "state.vscdb")
		if deger, mtNano, ok := sqliteItemTableDegerOku(dbYol, agyUserStatusKey); ok {
			mtSn := mtNano / 1e9
			if simdi > 0 && mtSn > simdi {
				mtSn = simdi
			}
			eposta, lr, lvar, hReset := agyUserStatusCoz(deger, mtSn, simdi)
			if eposta != "" || lvar || hReset > 0 {
				aday := agyDurumAdayi{eposta: eposta, mtNano: mtNano, haftaReset: hReset}
				if lvar {
					aday.limitT = mtSn
					aday.limitR = lr
				}
				sonuc = append(sonuc, aday)
			}
		}
	}
	return sonuc
}
