//go:build sag_gemini || (!sag_claude && !sag_chatgpt)

package main

// Antigravity okuyucu (Antigravity uygulaması, Antigravity IDE ve agy CLI).
//
// Antigravity her konuşma için yerelde iki kayıt tutar:
//   - ~/.gemini/antigravity[-ide|-cli]/brain/<konuşma>/.system_generated/logs/transcript.jsonl
//   - ~/.gemini/antigravity[-ide|-cli]/conversations/<konuşma>.db (+ .db-wal): gen_metadata tablosu
//     (adım başına gerçek model kimliği: Gemini 4 Carbon, Gemini 4 Argon, Gemini 3.8 Flash, Claude Opus 5.5 vb.)
//
// Bellek güvenliği: conversations/<konuşma>.db dosyaları steps tablosu yüzünden 100+ MB olabildiğinden
// dosya tamamıyla belleğe ALINMAZ; yalnızca 4 KB B-ağacı sayfaları os.Open + ReadAt ile okunur ve
// yalnızca gen_metadata tablosu gezilir (steps tablosuna kesinlikle dokunulmaz).

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
	"sort"
	"strconv"
	"strings"
	"time"
)

const (
	agyVarsayilanModel = "agy-gemini-4-argon"
	agyAzamiDosya      = 512 << 20 // 512 MB üstü dökümler atlanır
	agyAzamiSatir      = 16 << 20  // tek satır sınırı
	agyAzamiVscdb      = 64 << 20  // 64 MB üstü state.vscdb dosyaları güvenlik için atlanır
	agyAzamiGenMeta    = 64 << 10  // tek gen_metadata kaydı üst sınırı (64 KB)
	agyAzamiSayfaOkuma = 4096      // tek .db için azami sayfa okuma sınırı
	agyUserStatusKey   = "antigravityUnifiedStateSync.userStatus"
)

var agyModelRe = regexp.MustCompile("The user changed setting `Model Selection` from ([^\\r\\n]+?) to ([^\\r\\n]+?)\\.(?:\\s|<|$)")

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

func agySeviyeEkiTemizle(s string) string {
	for {
		degisti := false
		for _, ek := range []string{"-tiered", "-medium", "-high", "-low", "-thinking", "-minimal", "-maas"} {
			if strings.HasSuffix(s, ek) && len(s) > len(ek)+2 {
				s = strings.TrimSuffix(s, ek)
				degisti = true
			}
		}
		if !degisti {
			return s
		}
	}
}

// agyGorunenModelAd: gen_metadata.display (alan 21) değerini fiyat.mjs ile birebir uyumlu agy-* adına çevirir.
func agyGorunenModelAd(goruntu string) string {
	s := strings.TrimSpace(goruntu)
	s = strings.Trim(s, "`\"'")
	if i := strings.Index(s, "("); i >= 0 {
		s = strings.TrimSpace(s[:i])
	}
	s = strings.Join(strings.Fields(strings.ToLower(s)), " ")
	if s == "" || s == "none" || s == "null" || s == "auto" || s == "default" || s == "unknown" ||
		strings.HasPrefix(s, "model_placeholder") || strings.HasPrefix(s, "placeholder") {
		return ""
	}
	switch s {
	case "gemini 4 carbon", "gemini-4-carbon":
		return "agy-gemini-4-carbon"
	case "gemini 4 argon", "gemini-4-argon", "gemini":
		return "agy-gemini-4-argon"
	case "gemini 3.8 flash", "gemini-3.8-flash":
		return "agy-gemini-3.8-flash"
	case "gemini 3.7 flash", "gemini-3.7-flash":
		return "agy-gemini-3.7-flash"
	case "gemini 3.6 flash", "gemini-3.6-flash":
		return "agy-gemini-3.6-flash"
	case "gemini 3.5 flash", "gemini-3.5-flash":
		return "agy-gemini-3.5-flash"
	case "gemini 3.5 flash lite", "gemini 3.5 flash-lite", "gemini-3.5-flash-lite":
		return "agy-gemini-3.5-flash-lite"
	case "gemini 3.1 pro", "gemini-3.1-pro":
		return "agy-gemini-3.1-pro"
	case "gemini 3.1 flash lite", "gemini 3.1 flash-lite", "gemini-3.1-flash-lite":
		return "agy-gemini-3.1-flash-lite"
	case "gemini 3 flash", "gemini-3-flash":
		return "agy-gemini-3-flash"
	case "gemini 3 pro", "gemini-3-pro":
		return "agy-gemini-3-pro"
	case "claude opus 5.5", "claude-opus-5-5":
		return "agy-claude-opus-5-5"
	case "claude sonnet 5.5", "claude-sonnet-5-5":
		return "agy-claude-sonnet-5-5"
	case "claude opus 4.6", "claude-opus-4-6":
		return "agy-claude-opus-4-6"
	case "claude sonnet 4.6", "claude-sonnet-4-6":
		return "agy-claude-sonnet-4-6"
	case "claude sonnet 4.5", "claude-sonnet-4-5":
		return "agy-claude-sonnet-4-5"
	case "claude haiku 4.5", "claude-haiku-4-5":
		return "agy-claude-haiku-4-5"
	case "gpt-oss 120b", "gpt-oss-120b", "openai/gpt-oss-120b-maas":
		return "agy-gpt-oss-120b"
	case "gpt-oss 20b", "gpt-oss-20b", "openai/gpt-oss-20b-maas":
		return "agy-gpt-oss-20b"
	}
	s = strings.TrimPrefix(s, "agy-")
	s = strings.TrimPrefix(s, "models/")
	s = strings.TrimPrefix(s, "openai/")
	if strings.HasPrefix(s, "claude") {
		s = strings.ReplaceAll(s, ".", "-")
	}
	m := modelTemizle(strings.ReplaceAll(s, " ", "-"))
	m = agySeviyeEkiTemizle(m)
	if m == "" || m == "gemini" {
		return ""
	}
	m = "agy-" + m
	if len(m) > 60 {
		m = m[:60]
	}
	return m
}

func agyEnumModelAd(enumID uint64) string {
	switch enumID {
	case 1427, 1437:
		return "agy-gemini-4-carbon"
	case 1417, 1418:
		return "agy-gemini-4-argon"
	case 1318, 1319, 1322:
		return "agy-gemini-3.8-flash"
	case 1020, 1084, 1132, 1133, 1187:
		return "agy-gemini-3.5-flash"
	case 1016:
		return "agy-gemini-3.1-pro"
	case 1401:
		return "agy-claude-opus-5-5"
	case 1404:
		return "agy-claude-sonnet-5-5"
	case 1026:
		return "agy-claude-opus-4-6"
	case 1035:
		return "agy-claude-sonnet-4-6"
	case 1342:
		return "agy-gpt-oss-120b"
	}
	return ""
}

func agyModelCozVeyaBos(goruntu string) string {
	s := strings.TrimSpace(goruntu)
	s = strings.Trim(s, "`\"'")
	if i := strings.Index(s, "("); i >= 0 {
		s = strings.TrimSpace(s[:i])
	}
	alt := strings.ToLower(s)
	alt = strings.TrimPrefix(alt, "models/")
	alt = strings.TrimPrefix(alt, "agy-")
	alt = strings.TrimPrefix(alt, "openai/")
	if alt == "" || alt == "none" || alt == "null" || alt == "auto" || alt == "default" || alt == "unknown" {
		return ""
	}
	if strings.HasPrefix(alt, "model_placeholder_m") {
		if n, err := strconv.ParseUint(strings.TrimPrefix(alt, "model_placeholder_m"), 10, 64); err == nil {
			return agyEnumModelAd(1000 + n)
		}
		return ""
	}
	switch alt {
	case "gemini-3-flash-a", "gemini-3-flash-b", "gemini-default":
		return "agy-gemini-3.5-flash"
	case "gemini-pro-a", "gemini-pro-b":
		return "agy-gemini-3.1-pro"
	case "gemini-3.8-flash-tiered":
		return "agy-gemini-3.8-flash"
	case "gpt-oss-120b-maas":
		return "agy-gpt-oss-120b"
	case "gpt-oss-20b-maas":
		return "agy-gpt-oss-20b"
	}
	if g := agyGorunenModelAd(s); g != "" {
		return g
	}
	if strings.HasPrefix(alt, "claude") {
		alt = strings.ReplaceAll(alt, ".", "-")
	}
	m := modelTemizle(strings.ReplaceAll(alt, " ", "-"))
	m = agySeviyeEkiTemizle(m)
	if m == "" || m == "gemini" {
		return ""
	}
	m = "agy-" + m
	if len(m) > 60 {
		m = m[:60]
	}
	return m
}

// agyModelAd: "Claude Opus 5.5 (Medium)" -> "agy-claude-opus-5-5"; "Gemini 4 Carbon (High)" -> "agy-gemini-4-carbon"
func agyModelAd(goruntu string) string {
	if m := agyModelCozVeyaBos(goruntu); m != "" {
		return m
	}
	return agyVarsayilanModel
}

func agyGenMetaModelCoz(display, canon string, enumID uint64) string {
	if m := agyGorunenModelAd(display); m != "" {
		return m
	}
	if m := agyModelCozVeyaBos(canon); m != "" {
		return m
	}
	return agyEnumModelAd(enumID)
}

type agyAdimModel struct {
	adim  int
	model string
}

func agyAdimModelSec(ilkModel string, adimlar []agyAdimModel, stepIdx int) string {
	if len(adimlar) == 0 {
		return ilkModel
	}
	if stepIdx < adimlar[0].adim {
		if ilkModel != "" {
			return ilkModel
		}
		return adimlar[0].model
	}
	i := sort.Search(len(adimlar), func(k int) bool {
		return adimlar[k].adim > stepIdx
	})
	if i > 0 {
		return adimlar[i-1].model
	}
	return adimlar[0].model
}

func agyGenMetaKayitCoz(veri []byte) (model string, sonAdim int, sonAdimVar bool) {
	coz := func( ham []byte ) (string, int, bool) {
		var ustDisplay, ustCanon string
		var ustEnum uint64
		ustAdim, ustAdimVar := 0, false

		altTara := func(ic []byte) (disp, can string, en uint64, adim int, adimVar bool) {
			for len(ic) > 0 {
				no, tel, deg, v, _, n, ok := pbAlanOku(ic)
				if !ok {
					break
				}
				switch {
				case no == 3 && tel == 0:
					en = v
				case no == 19 && tel == 2:
					can = strings.TrimSpace(string(deg))
				case no == 21 && tel == 2:
					disp = strings.TrimSpace(string(deg))
				case no == 20 && tel == 2:
					var k, val string
					for kv := deg; len(kv) > 0; {
						kno, ktel, kdeg, _, _, kn, kok := pbAlanOku(kv)
						if !kok {
							break
						}
						if kno == 1 && ktel == 2 {
							k = strings.TrimSpace(string(kdeg))
						} else if kno == 2 && ktel == 2 {
							val = strings.TrimSpace(string(kdeg))
						}
						kv = kv[kn:]
					}
					if k == "last_step_index" {
						if sayi, err := strconv.Atoi(val); err == nil && sayi >= -1 {
							adim = sayi
							adimVar = true
						}
					}
				}
				ic = ic[n:]
			}
			return
		}

		for b := ham; len(b) > 0; {
			no, tel, deg, v, _, n, ok := pbAlanOku(b)
			if !ok {
				break
			}
			switch {
			case no == 1 && tel == 2:
				d, c, e, a, avar := altTara(deg)
				if m := agyGenMetaModelCoz(d, c, e); m != "" {
					return m, a, avar
				}
				if avar && !ustAdimVar {
					ustAdim, ustAdimVar = a, true
				}
			case no == 3 && tel == 0:
				ustEnum = v
			case no == 19 && tel == 2:
				ustCanon = strings.TrimSpace(string(deg))
			case no == 21 && tel == 2:
				ustDisplay = strings.TrimSpace(string(deg))
			case no == 20 && tel == 2:
				var k, val string
				for kv := deg; len(kv) > 0; {
					kno, ktel, kdeg, _, _, kn, kok := pbAlanOku(kv)
					if !kok {
						break
					}
					if kno == 1 && ktel == 2 {
						k = strings.TrimSpace(string(kdeg))
					} else if kno == 2 && ktel == 2 {
						val = strings.TrimSpace(string(kdeg))
					}
					kv = kv[kn:]
				}
				if k == "last_step_index" {
					if sayi, err := strconv.Atoi(val); err == nil && sayi >= -1 {
						ustAdim, ustAdimVar = sayi, true
					}
				}
			}
			b = b[n:]
		}
		return agyGenMetaModelCoz(ustDisplay, ustCanon, ustEnum), ustAdim, ustAdimVar
	}

	if m, a, av := coz(veri); m != "" {
		return m, a, av
	}
	if cozB64, ok := b64TemizCoz(veri); ok {
		return coz(cozB64)
	}
	return "", 0, false
}

// ---------- Konuşma yolu yardımcıları (<kok>/conversations/<id>.db ve antigravity_state.pbtxt) ----------

func agyTranscriptKonusmaBilgisi(transcriptYol string) (kok, convID, dbYol string) {
	logsDir := filepath.Dir(transcriptYol)
	sysDir := filepath.Dir(logsDir)
	if filepath.Base(sysDir) != ".system_generated" {
		return "", "", ""
	}
	convDir := filepath.Dir(sysDir)
	brainDir := filepath.Dir(convDir)
	if filepath.Base(brainDir) != "brain" {
		return "", "", ""
	}
	convID = filepath.Base(convDir)
	if convID == "" || convID == "." || convID == ".." {
		return "", "", ""
	}
	kok = filepath.Dir(brainDir)
	dbYol = filepath.Join(kok, "conversations", convID+".db")
	return kok, convID, dbYol
}

func agyDbMtimeOku(dbYolu string) int64 {
	if dbYolu == "" || !yerelYolMu(dbYolu) {
		return 0
	}
	var enBuyuk int64
	if st, err := os.Stat(dbYolu); err == nil && st.Mode().IsRegular() {
		enBuyuk = st.ModTime().UnixNano()
	}
	walYol := dbYolu + "-wal"
	if yerelYolMu(walYol) {
		if wst, err := os.Stat(walYol); err == nil && wst.Mode().IsRegular() && wst.ModTime().UnixNano() > enBuyuk {
			enBuyuk = wst.ModTime().UnixNano()
		}
	}
	return enBuyuk
}

func agyPbtxtModelOku(kok string) string {
	if kok == "" {
		return ""
	}
	yol := filepath.Join(kok, "antigravity_state.pbtxt")
	if !yerelYolMu(yol) {
		return ""
	}
	st, err := os.Stat(yol)
	if err != nil || !st.Mode().IsRegular() || st.Size() <= 0 || st.Size() > 1<<20 {
		return ""
	}
	b, err := os.ReadFile(yol)
	if err != nil {
		return ""
	}
	for _, satir := range strings.Split(string(b), "\n") {
		satir = strings.TrimSpace(satir)
		if !strings.HasPrefix(satir, "last_selected_agent_model:") {
			continue
		}
		deger := strings.TrimSpace(strings.TrimPrefix(satir, "last_selected_agent_model:"))
		if i := strings.IndexByte(deger, '#'); i >= 0 {
			deger = strings.TrimSpace(deger[:i])
		}
		deger = strings.Trim(deger, "\"'`")
		ust := strings.ToUpper(deger)
		if strings.HasPrefix(ust, "MODEL_PLACEHOLDER_M") {
			if n, errN := strconv.ParseUint(strings.TrimPrefix(ust, "MODEL_PLACEHOLDER_M"), 10, 64); errN == nil {
				if m := agyEnumModelAd(1000 + n); m != "" {
					return m
				}
			}
			continue
		}
		if n, errN := strconv.ParseUint(deger, 10, 64); errN == nil {
			if m := agyEnumModelAd(n); m != "" {
				return m
			}
		}
		if m := agyGenMetaModelCoz(deger, deger, 0); m != "" {
			return m
		}
	}
	return ""
}

// ---------- Düşük bellekli saf Go SQLite3 sayfa + WAL okuyucu ----------

type sqliteOkuyucu struct {
	f              *os.File
	wf             *os.File
	boyut          int64
	ps             int
	usable         int
	walSayfaOffset map[uint32]int64
	okunanSayfa    int
	azamiSayfa     int
}

func (r *sqliteOkuyucu) kapat() {
	if r.wf != nil {
		_ = r.wf.Close()
	}
	if r.f != nil {
		_ = r.f.Close()
	}
}

func sqliteWalChecksum(le bool, veri []byte, s0, s1 uint32) (uint32, uint32) {
	for i := 0; i+8 <= len(veri); i += 8 {
		var x0, x1 uint32
		if le {
			x0 = binary.LittleEndian.Uint32(veri[i : i+4])
			x1 = binary.LittleEndian.Uint32(veri[i+4 : i+8])
		} else {
			x0 = binary.BigEndian.Uint32(veri[i : i+4])
			x1 = binary.BigEndian.Uint32(veri[i+4 : i+8])
		}
		s0 += x0 + s1
		s1 += x1 + s0
	}
	return s0, s1
}

func sqliteAc(yol string, azamiBoyut int64) (*sqliteOkuyucu, int64, bool) {
	if yol == "" || !yerelYolMu(yol) {
		return nil, 0, false
	}
	st, err := os.Stat(yol)
	if err != nil || !st.Mode().IsRegular() || (azamiBoyut > 0 && st.Size() > azamiBoyut) {
		return nil, 0, false
	}
	mtNano := st.ModTime().UnixNano()
	f, err := os.Open(yol)
	if err != nil {
		return nil, 0, false
	}

	var ps, usable int
	if st.Size() >= 100 {
		var hdr [100]byte
		if n, errH := f.ReadAt(hdr[:], 0); errH != nil || n < 100 || string(hdr[:16]) != "SQLite format 3\x00" {
			_ = f.Close()
			return nil, 0, false
		}
		ps = int(binary.BigEndian.Uint16(hdr[16:18]))
		if ps == 1 {
			ps = 65536
		}
		if ps < 512 || ps > 65536 || (ps&(ps-1)) != 0 {
			_ = f.Close()
			return nil, 0, false
		}
		usable = ps - int(hdr[20])
		if usable < 480 {
			_ = f.Close()
			return nil, 0, false
		}
	}

	okuyucu := &sqliteOkuyucu{
		f:          f,
		boyut:      st.Size(),
		ps:         ps,
		usable:     usable,
		azamiSayfa: agyAzamiSayfaOkuma,
	}

	walYol := yol + "-wal"
	if yerelYolMu(walYol) {
		if wst, errW := os.Stat(walYol); errW == nil && wst.Mode().IsRegular() && wst.Size() >= 32 {
			if wst.ModTime().UnixNano() > mtNano {
				mtNano = wst.ModTime().UnixNano()
			}
			if wf, errO := os.Open(walYol); errO == nil {
				var whdr [32]byte
				if nw, errWH := wf.ReadAt(whdr[:], 0); errWH == nil && nw == 32 {
					magic := binary.BigEndian.Uint32(whdr[0:4])
					wps := int(binary.BigEndian.Uint32(whdr[8:12]))
					if wps == 1 {
						wps = 65536
					}
					if ps == 0 && wps >= 512 && wps <= 65536 && (wps&(wps-1)) == 0 {
						ps = wps
						usable = wps
						okuyucu.ps = ps
						okuyucu.usable = usable
					}
					if (magic == 0x377f0682 || magic == 0x377f0683) && ps > 0 && wps == ps {
						le := magic == 0x377f0682
						s0, s1 := sqliteWalChecksum(le, whdr[0:24], 0, 0)
						hCk0 := binary.BigEndian.Uint32(whdr[24:28])
						hCk1 := binary.BigEndian.Uint32(whdr[28:32])
						if s0 == hCk0 && s1 == hCk1 {
							type walCerceve struct {
								pgNo uint32
								off  int64
							}
							var cerceveler []walCerceve
							sonCommit := -1
							cerceveBoy := int64(24 + ps)
							var fhdr [24]byte
							sayfaBuf := make([]byte, ps)
							for off, sayacCerceve := int64(32), 0; off+cerceveBoy <= wst.Size() && sayacCerceve < 32768; off, sayacCerceve = off+cerceveBoy, sayacCerceve+1 {
								if nf, errF := wf.ReadAt(fhdr[:], off); errF != nil || nf != 24 {
									break
								}
								pgNo := binary.BigEndian.Uint32(fhdr[0:4])
								dbBoy := binary.BigEndian.Uint32(fhdr[4:8])
								if pgNo < 1 || !bytes.Equal(fhdr[8:16], whdr[16:24]) {
									break
								}
								if np, errP := wf.ReadAt(sayfaBuf, off+24); errP != nil || np != ps {
									break
								}
								s0, s1 = sqliteWalChecksum(le, fhdr[0:8], s0, s1)
								s0, s1 = sqliteWalChecksum(le, sayfaBuf, s0, s1)
								fCk0 := binary.BigEndian.Uint32(fhdr[16:20])
								fCk1 := binary.BigEndian.Uint32(fhdr[20:24])
								if s0 != fCk0 || s1 != fCk1 {
									break
								}
								cerceveler = append(cerceveler, walCerceve{
									pgNo: pgNo,
									off:  off + 24,
								})
								if dbBoy > 0 {
									sonCommit = len(cerceveler) - 1
								}
							}
							if sonCommit >= 0 {
								harita := make(map[uint32]int64, sonCommit+1)
								for i := 0; i <= sonCommit; i++ {
									harita[cerceveler[i].pgNo] = cerceveler[i].off
								}
								okuyucu.wf = wf
								okuyucu.walSayfaOffset = harita
								// Henüz checkpoint görmemiş 0 baytlık .db dosyasında sayfa 1 WAL içindedir
								if st.Size() < 100 {
									if off1, var1 := harita[1]; var1 {
										var hdr1 [100]byte
										if n1, err1 := wf.ReadAt(hdr1[:], off1); err1 == nil && n1 == 100 && string(hdr1[:16]) == "SQLite format 3\x00" {
											okuyucu.usable = ps - int(hdr1[20])
										} else {
											okuyucu.usable = 0
										}
									} else {
										okuyucu.usable = 0
									}
								}
							}
						}
					}
				}
				if okuyucu.wf == nil {
					_ = wf.Close()
				}
			}
		}
	}
	if okuyucu.ps == 0 || okuyucu.usable < 480 {
		okuyucu.kapat()
		return nil, 0, false
	}
	return okuyucu, mtNano, true
}

func (r *sqliteOkuyucu) sayfaGetir(pgNo uint32) ([]byte, bool) {
	if pgNo == 0 || r.okunanSayfa >= r.azamiSayfa {
		return nil, false
	}
	r.okunanSayfa++
	buf := make([]byte, r.ps)
	if r.wf != nil {
		if off, var_ := r.walSayfaOffset[pgNo]; var_ {
			n, err := r.wf.ReadAt(buf, off)
			return buf, err == nil && n == r.ps
		}
	}
	bas := int64(pgNo-1) * int64(r.ps)
	if bas < 0 || bas+int64(r.ps) > r.boyut {
		return nil, false
	}
	n, err := r.f.ReadAt(buf, bas)
	return buf, err == nil && n == r.ps
}

func (r *sqliteOkuyucu) yerelBoyut(payload int) int {
	maxLocal := r.usable - 35
	if payload <= maxLocal {
		return payload
	}
	minLocal := ((r.usable - 12) * 32 / 255) - 23
	k := minLocal + ((payload - minLocal) % (r.usable - 4))
	if k <= maxLocal {
		return k
	}
	return minLocal
}

func (r *sqliteOkuyucu) tasmaBirlestir(yerel []byte, tasmaIlk uint32, toplam, azamiToplam int) ([]byte, bool) {
	if toplam <= len(yerel) {
		return yerel[:toplam], true
	}
	if toplam > azamiToplam {
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
		opg, ok := r.sayfaGetir(ov)
		if !ok || len(opg) < r.usable {
			return nil, false
		}
		sonraki := binary.BigEndian.Uint32(opg[0:4])
		alinacak := r.usable - 4
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

func (r *sqliteOkuyucu) tabloKokBul(hedefTablo string) uint32 {
	var bulunanKok uint32
	ziyaret := map[uint32]bool{}
	var gez func(pgNo uint32)
	gez = func(pgNo uint32) {
		if bulunanKok != 0 || pgNo == 0 || ziyaret[pgNo] || len(ziyaret) > 256 {
			return
		}
		ziyaret[pgNo] = true
		sayfa, ok := r.sayfaGetir(pgNo)
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
			for i := 0; i < hucreSayi && bulunanKok == 0; i++ {
				cp := int(binary.BigEndian.Uint16(sayfa[hdr+12+2*i : hdr+14+2*i]))
				if cp+4 <= len(sayfa) {
					gez(binary.BigEndian.Uint32(sayfa[cp : cp+4]))
				}
			}
			if bulunanKok == 0 {
				gez(binary.BigEndian.Uint32(sayfa[hdr+8 : hdr+12]))
			}
		case 0x0d:
			if hdr+8+2*hucreSayi > len(sayfa) {
				return
			}
			for i := 0; i < hucreSayi; i++ {
				cp := int(binary.BigEndian.Uint16(sayfa[hdr+8+2*i : hdr+10+2*i]))
				if cp >= r.usable {
					continue
				}
				pUz, n1 := sqliteVarint(sayfa[cp:r.usable])
				if n1 == 0 || pUz == 0 || pUz > 1<<20 {
					continue
				}
				_, n2 := sqliteVarint(sayfa[cp+n1 : r.usable])
				if n2 == 0 {
					continue
				}
				recBas := cp + n1 + n2
				yerelUz := r.yerelBoyut(int(pUz))
				if recBas+yerelUz > r.usable {
					continue
				}
				yerel := sayfa[recBas : recBas+yerelUz]
				hUz, nh := sqliteVarint(yerel)
				if nh == 0 || int(hUz) > len(yerel) {
					continue
				}
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
				if tipStr == "table" && adStr == hedefTablo {
					if kok, okK := sqliteTamSayiOku(stler[3], yerel[gKonum+l0+l1+l2:gKonum+l0+l1+l2+l3]); okK && kok >= 2 {
						bulunanKok = uint32(kok)
						return
					}
				}
			}
		}
	}
	gez(1)
	return bulunanKok
}

// agyKonusmaModelleriOku: conversations/<id>.db (+ .db-wal) içindeki gen_metadata tablosunu
// salt okunur ve düşük bellekle (yalnızca ilgili 4 KB B-ağacı sayfalarını okuyarak) çözer.
// steps tablosuna kesinlikle dokunmaz.
func agyKonusmaModelleriOku(dbYolu string) (ilkModel string, adimlar []agyAdimModel) {
	r, _, ok := sqliteAc(dbYolu, 0)
	if !ok {
		return "", nil
	}
	defer r.kapat()

	genKok := r.tabloKokBul("gen_metadata")
	if genKok == 0 {
		return "", nil
	}

	adimHarita := map[int]string{}
	ziyaret := map[uint32]bool{}
	var tabloGez func(pgNo uint32)
	tabloGez = func(pgNo uint32) {
		if pgNo == 0 || ziyaret[pgNo] || len(ziyaret) > 2048 {
			return
		}
		ziyaret[pgNo] = true
		sayfa, okS := r.sayfaGetir(pgNo)
		if !okS {
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
					tabloGez(binary.BigEndian.Uint32(sayfa[cp : cp+4]))
				}
			}
			tabloGez(binary.BigEndian.Uint32(sayfa[hdr+8 : hdr+12]))
		case 0x0d:
			if hdr+8+2*hucreSayi > len(sayfa) {
				return
			}
			for i := 0; i < hucreSayi; i++ {
				cp := int(binary.BigEndian.Uint16(sayfa[hdr+8+2*i : hdr+10+2*i]))
				if cp >= r.usable {
					continue
				}
				pUz, n1 := sqliteVarint(sayfa[cp:r.usable])
				if n1 == 0 || pUz == 0 || pUz > agyAzamiGenMeta {
					continue
				}
				_, n2 := sqliteVarint(sayfa[cp+n1 : r.usable])
				if n2 == 0 {
					continue
				}
				recBas := cp + n1 + n2
				toplam := int(pUz)
				yerelUz := r.yerelBoyut(toplam)
				ekTasma := 0
				if toplam > yerelUz {
					ekTasma = 4
				}
				if recBas+yerelUz+ekTasma > r.usable {
					continue
				}
				yerel := sayfa[recBas : recBas+yerelUz]
				hUz, nh := sqliteVarint(yerel)
				if nh == 0 || int(hUz) > len(yerel) {
					continue
				}
				// gen_metadata: (idx INTEGER PRIMARY KEY, data BLOB, size INTEGER)
				stIdx, nk1 := sqliteVarint(yerel[nh:int(hUz)])
				if nk1 == 0 || nh+nk1 >= int(hUz) {
					continue
				}
				stData, nk2 := sqliteVarint(yerel[nh+nk1 : int(hUz)])
				if nk2 == 0 {
					continue
				}
				idxUz, okI := sqliteDegerUzunlugu(stIdx)
				dataUz, okD := sqliteDegerUzunlugu(stData)
				if !okI || !okD || dataUz <= 0 {
					continue
				}
				var tasmaIlk uint32
				if toplam > yerelUz {
					tasmaIlk = binary.BigEndian.Uint32(sayfa[recBas+yerelUz : recBas+yerelUz+4])
				}
				tam, okT := r.tasmaBirlestir(yerel, tasmaIlk, toplam, agyAzamiGenMeta)
				veriBas := int(hUz) + idxUz
				if !okT || veriBas+dataUz > len(tam) {
					continue
				}
				m, sonAdim, adimVar := agyGenMetaKayitCoz(tam[veriBas : veriBas+dataUz])
				if m == "" {
					continue
				}
				if ilkModel == "" {
					ilkModel = m
				}
				if adimVar {
					hedefAdim := sonAdim + 1
					if hedefAdim < 0 {
						hedefAdim = 0
					}
					adimHarita[hedefAdim] = m
				}
			}
		}
	}
	tabloGez(genKok)

	if len(adimHarita) > 0 {
		adimlar = make([]agyAdimModel, 0, len(adimHarita))
		for adim, model := range adimHarita {
			adimlar = append(adimlar, agyAdimModel{adim: adim, model: model})
		}
		sort.Slice(adimlar, func(i, j int) bool { return adimlar[i].adim < adimlar[j].adim })
		if ilkModel == "" {
			ilkModel = adimlar[0].model
		}
	}
	return ilkModel, adimlar
}

// ---------- Döküm (transcript.jsonl) okuma ve artımlı önbellek ----------

type agySatirJSON struct {
	StepIndex *int   `json:"step_index"`
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
	kok, _, dbYol := agyTranscriptKonusmaBilgisi(yol)
	dbMtime := agyDbMtimeOku(dbYol)

	if eski != nil && eski.Boyut == boyut && eski.Mtime == mt && eski.DbMtime == dbMtime {
		eski.Saglayici = sagGeminiAd
		return eski, false
	}
	if boyut <= 0 || boyut > agyAzamiDosya || !yerelYolMu(yol) {
		return nil, false
	}
	// conversations/<id>.db veya .db-wal sonradan oluştuysa/güncellendiyse dökümü ofset 0'dan yeniden çöz
	if eski != nil && eski.DbMtime != dbMtime {
		eski = nil
	}

	f, err := os.Open(yol)
	if err != nil {
		return nil, false
	}
	defer f.Close()

	var ilkDbModel string
	var adimModelleri []agyAdimModel
	if dbYol != "" && dbMtime > 0 {
		ilkDbModel, adimModelleri = agyKonusmaModelleriOku(dbYol)
	}
	baslangicModel := ilkDbModel
	if baslangicModel == "" && kok != "" {
		baslangicModel = agyPbtxtModelOku(kok)
	}
	if baslangicModel == "" {
		baslangicModel = agyVarsayilanModel
	}

	d := &digerDosyaKaydi{
		Saglayici: sagGeminiAd,
		Boyut:     boyut,
		Mtime:     mt,
		DbMtime:   dbMtime,
		Sid:       digerSid("agy", sidAnahtar),
		SonModel:  baslangicModel,
	}
	var bas int64
	if eski != nil && eski.Ofset > 0 && boyut > eski.Boyut && boyut >= eski.Ofset && eski.Sid == d.Sid &&
		eski.Bas != 0 && basOzeti(f, eski.Ofset) == eski.Bas && kuyrukOzeti(f, eski.Ofset) == eski.Kuyruk {
		bas = eski.Ofset
		if eski.SonModel != "" {
			d.SonModel = eski.SonModel
		}
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
				agySatirIsle(d, satir, sinir, ilkDbModel, adimModelleri)
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

func agySatirIsle(d *digerDosyaKaydi, satir []byte, sinir int64, ilkDbModel string, adimlar []agyAdimModel) {
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
	model := ""
	if strings.TrimSpace(s.Model) != "" {
		model = agyModelAd(s.Model)
		d.SonModel = model
	} else if len(adimlar) > 0 && s.StepIndex != nil {
		model = agyAdimModelSec(ilkDbModel, adimlar, *s.StepIndex)
		if model != "" {
			d.SonModel = model
		}
	}
	if model == "" {
		model = d.SonModel
	}
	if model == "" {
		model = agyVarsayilanModel
	}
	t, err := time.Parse(time.RFC3339Nano, s.CreatedAt)
	if err != nil || t.Unix() < sinir {
		return
	}
	girdi, okuma, cikti := max(0, *s.Input), max(0, s.CacheRead), max(0, s.Output)
	if girdi == 0 && okuma == 0 && cikti == 0 {
		return
	}
	d.Kayitlar = append(d.Kayitlar, digerKayit{T: t.Unix(), Model: model, In: girdi, Out: cikti, Cr: okuma})
}

// antigravityTopla: tüm Antigravity konuşma dökümlerini tarar; değişmeyen dosyalar önbellekten gelir.
func antigravityTopla(onb, yeniOnb map[string]*digerDosyaKaydi, sinir int64) (dosyalar []*digerDosyaKaydi, okunan int, kuculen, engel bool) {
	ev := evDizini()
	for _, ad := range []string{"antigravity", "antigravity-ide", "antigravity-cli"} {
		if yolEngelMi(filepath.Join(ev, ".gemini", ad)) || yolEngelMi(filepath.Join(ev, ".gemini", ad, "brain")) {
			engel = true
		}
	}
	for _, brainKok := range agyKaynaklari() {
		if !yerelYolMu(brainKok) {
			continue
		}
		kokAd := filepath.Base(filepath.Dir(brainKok))
		girdiler, err := os.ReadDir(brainKok)
		if err != nil {
			if errors.Is(err, fs.ErrPermission) {
				engel = true
			}
			continue
		}
		for _, e := range girdiler {
			if strings.HasPrefix(e.Name(), ".") || !e.IsDir() {
				continue
			}
			konusmaDir := filepath.Join(brainKok, e.Name())
			if !yerelYolMu(konusmaDir) {
				continue
			}
			sysDir := filepath.Join(konusmaDir, ".system_generated")
			logDir := filepath.Join(sysDir, "logs")
			yol := filepath.Join(logDir, "transcript.jsonl")
			if yolEngelMi(konusmaDir) || yolEngelMi(sysDir) || yolEngelMi(logDir) || dosyaEngelMi(yol) {
				engel = true
				continue
			}
			if !yerelYolMu(yol) {
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

func agyTopla(onb, yeniOnb map[string]*digerDosyaKaydi, sinir int64) ([]*digerDosyaKaydi, int, bool, bool) {
	return antigravityTopla(onb, yeniOnb, sinir)
}

// ---------- saf Go SQLite3 yardımcı kod çözücüler ve state.vscdb ItemTable okuyucu ----------

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
	r, mtNano, ok := sqliteAc(yol, agyAzamiVscdb)
	if !ok {
		return nil, 0, false
	}
	defer r.kapat()

	itemKok := r.tabloKokBul("ItemTable")
	if itemKok == 0 {
		return nil, 0, false
	}

	var bulunan []byte
	tabloZiyaret := map[uint32]bool{}
	var tabloGez func(pgNo uint32)
	tabloGez = func(pgNo uint32) {
		if bulunan != nil || pgNo == 0 || tabloZiyaret[pgNo] || len(tabloZiyaret) > 2048 {
			return
		}
		tabloZiyaret[pgNo] = true
		sayfa, okS := r.sayfaGetir(pgNo)
		if !okS {
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
				if cp >= r.usable {
					continue
				}
				pUz, n1 := sqliteVarint(sayfa[cp:r.usable])
				if n1 == 0 || pUz == 0 || pUz > 16<<20 {
					continue
				}
				_, n2 := sqliteVarint(sayfa[cp+n1 : r.usable])
				if n2 == 0 {
					continue
				}
				recBas := cp + n1 + n2
				toplam := int(pUz)
				yerelUz := r.yerelBoyut(toplam)
				ekTasma := 0
				if toplam > yerelUz {
					ekTasma = 4
				}
				if recBas+yerelUz+ekTasma > r.usable {
					continue
				}
				yerel := sayfa[recBas : recBas+yerelUz]
				hUz, nh := sqliteVarint(yerel)
				if nh == 0 || int(hUz) > len(yerel) {
					continue
				}
				stAnahtar, nk1 := sqliteVarint(yerel[nh:int(hUz)])
				if nk1 == 0 || nh+nk1 >= int(hUz) {
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
				tam, okT := r.tasmaBirlestir(yerel, tasmaIlk, toplam, 16<<20)
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

	var gemW, gem5, ucpW, ucp5, genelW, genel5 agyKovaDegeri
	var gecmisGemWReset, gecmisGenelWReset int64

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
							geminiMi := strings.Contains(etiket, "gemini")
							ucpMi := strings.Contains(etiket, "claude") || strings.Contains(etiket, "gpt") || strings.Contains(etiket, "openai") ||
								strings.Contains(etiket, "sonnet") || strings.Contains(etiket, "opus") || strings.Contains(etiket, "haiku")

							if sure > 8*3600 && sure <= 14*86400 && (simdi <= 0 || simdi-resetSn <= 14*86400) {
								if geminiMi {
									if resetSn > gecmisGemWReset {
										gecmisGemWReset = resetSn
									}
								} else if !ucpMi && resetSn > gecmisGenelWReset {
									gecmisGenelWReset = resetSn
								}
							}
							if simdi <= 0 || (resetSn > simdi && simdi-mtSn <= 7*86400) {
								kullanim := math.Round((1.0-kalan)*1000) / 10
								if kullanim < 0 {
									kullanim = 0
								} else if kullanim > 100 {
									kullanim = 100
								}
								if sure <= 8*3600 {
									switch {
									case geminiMi:
										agyKovaGuncelle(&gem5, kullanim, resetSn)
									case ucpMi:
										agyKovaGuncelle(&ucp5, kullanim, resetSn)
									default:
										agyKovaGuncelle(&genel5, kullanim, resetSn)
									}
								} else if sure <= 14*86400 {
									switch {
									case geminiMi:
										agyKovaGuncelle(&gemW, kullanim, resetSn)
									case ucpMi:
										agyKovaGuncelle(&ucpW, kullanim, resetSn)
									default:
										agyKovaGuncelle(&genelW, kullanim, resetSn)
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
	anaW := gemW
	if !anaW.varMi && genelW.varMi {
		anaW = genelW
	}
	if anaW.varMi {
		rm["seven_day.used_percentage"] = strconv.FormatFloat(anaW.pct, 'f', -1, 64)
		rm["seven_day.resets_at"] = strconv.FormatInt(anaW.reset, 10)
		haftaReset = sonrakiHaftaReset(anaW.reset, simdi)
	} else if gecmisGemWReset > 0 {
		haftaReset = sonrakiHaftaReset(gecmisGemWReset, simdi)
	} else if gecmisGenelWReset > 0 {
		haftaReset = sonrakiHaftaReset(gecmisGenelWReset, simdi)
	}

	ana5 := gem5
	if !ana5.varMi && genel5.varMi {
		ana5 = genel5
	}
	if ana5.varMi {
		rm["five_hour.used_percentage"] = strconv.FormatFloat(ana5.pct, 'f', -1, 64)
		rm["five_hour.resets_at"] = strconv.FormatInt(ana5.reset, 10)
	}

	if gemW.varMi {
		rm["agy_gemini.used_percentage"] = strconv.FormatFloat(gemW.pct, 'f', -1, 64)
		rm["agy_gemini.resets_at"] = strconv.FormatInt(gemW.reset, 10)
	}
	if gem5.varMi {
		rm["agy_gemini_5h.used_percentage"] = strconv.FormatFloat(gem5.pct, 'f', -1, 64)
		rm["agy_gemini_5h.resets_at"] = strconv.FormatInt(gem5.reset, 10)
	}

	if ucpW.varMi {
		rm["agy_claude.used_percentage"] = strconv.FormatFloat(ucpW.pct, 'f', -1, 64)
		rm["agy_claude.resets_at"] = strconv.FormatInt(ucpW.reset, 10)
	}
	if ucp5.varMi {
		rm["agy_claude_5h.used_percentage"] = strconv.FormatFloat(ucp5.pct, 'f', -1, 64)
		rm["agy_claude_5h.resets_at"] = strconv.FormatInt(ucp5.reset, 10)
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

	// 0. Antigravity 2.0 resmi agy CLI statusLine okuması (~/.token-panosu/limit_agy.json)
	if agyLim, ok := agyLimitOku(); ok {
		mtSn := agyLim.T
		if simdi > 0 && mtSn > simdi {
			mtSn = simdi
		}
		hReset := agyHaftaResetTemizle(agyLim, simdi)
		aktifR := agyLimitAktifFiltrele(agyLim, simdi)
		if agyLim.Eposta != "" || len(aktifR) > 0 || hReset > 0 {
			aday := agyDurumAdayi{
				eposta:     agyLim.Eposta,
				mtNano:     mtSn * 1e9,
				haftaReset: hReset,
			}
			if len(aktifR) > 0 {
				aday.limitT = mtSn
				aday.limitR = aktifR
			}
			sonuc = append(sonuc, aday)
		}
	}

	gorulen := map[string]bool{}
	for _, kok := range agyUygulamaDizinleri() {
		if !yerelYolMu(kok) {
			continue
		}
		g := gercekYol(kok)
		if gorulen[g] {
			continue
		}
		gorulen[g] = true

		// 1. app_storage.json -> "jetski.onboarding.lastLoginUsername" (yalnızca düz metin e-posta)
		asYol := filepath.Join(kok, "app_storage.json")
		if yerelYolMu(asYol) {
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
