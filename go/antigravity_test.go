//go:build sag_gemini || (!sag_claude && !sag_chatgpt)

package main

import (
	"encoding/base64"
	"encoding/binary"
	"math"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"testing"
	"time"
)

func TestAgyModelAd(t *testing.T) {
	durumlar := map[string]string{
		"Claude Opus 5.5 (Medium)":  "agy-claude-opus-5-5",
		"Claude Sonnet 5.5 (High)":  "agy-claude-sonnet-5-5",
		"Gemini 4 Carbon (High)":    "agy-gemini-4-carbon",
		"Gemini 4 Argon (Medium)":   "agy-gemini-4-argon",
		"Gemini 3.8 Flash (Medium)": "agy-gemini-3.8-flash",
		"Gemini 3.5 Flash (Low)":    "agy-gemini-3.5-flash",
		"gemini-3-flash-a":          "agy-gemini-3.5-flash",
		"gemini-pro-b":              "agy-gemini-3.1-pro",
		"gemini-3.8-flash-tiered":   "agy-gemini-3.8-flash",
		"MODEL_PLACEHOLDER_M427":    "agy-gemini-4-carbon",
		"None":                      "agy-gemini",
		"":                          "agy-gemini",
	}
	for g, b := range durumlar {
		if s := agyModelAd(g); s != b {
			t.Errorf("%q -> %q, beklenen %q", g, s, b)
		}
	}
}

func TestAgyDokumOkumaVeEkleme(t *testing.T) {
	dir := t.TempDir()
	yol := filepath.Join(dir, "transcript.jsonl")
	simdi := time.Now().UTC().Truncate(time.Second)
	ts := func(d time.Duration) string { return simdi.Add(d).Format(time.RFC3339) }
	// Kullanıcı isteminde rastgele "Model Selection ... to `sahte`." geçmesi modeli bozmamalı
	ilk := `{"step_index":0,"type":"USER_INPUT","created_at":"` + ts(-30*time.Minute) + `","content":"<USER_SETTINGS_CHANGE>The user changed setting ` + "`Model Selection`" + ` from None to Claude Opus 5.5 (Medium). No need to comment.</USER_SETTINGS_CHANGE>"}` + "\n" +
		`{"step_index":1,"type":"USER_INPUT","created_at":"` + ts(-29*time.Minute-30*time.Second) + `","content":"Check Model Selection docs and rename foo to ` + "`bar`" + `."}` + "\n" +
		`{"step_index":2,"type":"PLANNER_RESPONSE","created_at":"` + ts(-29*time.Minute) + `","input_tokens":1000,"cache_read_tokens":5000,"output_tokens":200,"thinking":"gizli"}` + "\n"
	if err := os.WriteFile(yol, []byte(ilk), 0o644); err != nil {
		t.Fatal(err)
	}
	st, _ := os.Stat(yol)
	sinir := simdi.Add(-8 * 24 * time.Hour).Unix()
	d, okundu := agyDosyaOku(yol, "antigravity/x", st, nil, sinir)
	if d == nil || !okundu || len(d.Kayitlar) != 1 {
		t.Fatalf("ilk okuma hatalı: %+v", d)
	}
	k := d.Kayitlar[0]
	if k.Model != "agy-claude-opus-5-5" || k.In != 1000 || k.Cr != 5000 || k.Out != 200 {
		t.Fatalf("kayıt hatalı: %+v", k)
	}
	// Sona ekleme: model değişimi + yeni yanıt + yarım satır
	ek := `{"step_index":3,"type":"USER_INPUT","created_at":"` + ts(-10*time.Minute) + `","content":"The user changed setting ` + "`Model Selection`" + ` from Claude Opus 5.5 (Medium) to Gemini 4 Carbon (High). ok"}` + "\n" +
		`{"step_index":4,"type":"PLANNER_RESPONSE","created_at":"` + ts(-9*time.Minute) + `","input_tokens":300,"cache_read_tokens":0,"output_tokens":50}` + "\n" +
		`{"step_index":5,"type":"PLANNER_RESPONSE","created_at":"` + ts(-8*time.Minute) + `","input_tok`
	f, _ := os.OpenFile(yol, os.O_APPEND|os.O_WRONLY, 0o644)
	f.WriteString(ek)
	f.Close()
	st2, _ := os.Stat(yol)
	d2, okundu2 := agyDosyaOku(yol, "antigravity/x", st2, d, sinir)
	if !okundu2 || len(d2.Kayitlar) != 2 {
		t.Fatalf("ekleme okuması hatalı: %+v", d2)
	}
	if d2.Kayitlar[1].Model != "agy-gemini-4-carbon" || d2.Kayitlar[1].In != 300 {
		t.Fatalf("ikinci kayıt hatalı: %+v", d2.Kayitlar[1])
	}
	if d2.Ofset >= st2.Size() {
		t.Fatalf("yarım satır ofsete dahil edilmemeli: ofset=%d boyut=%d", d2.Ofset, st2.Size())
	}
}

// ---------- Protobuf ve SQLite3 taşma / WAL test yardımcıları ----------

func pbYazVarint(b []byte, v uint64) []byte {
	for v >= 0x80 {
		b = append(b, byte(v)|0x80)
		v >>= 7
	}
	return append(b, byte(v))
}

func pbYazAlanUzun(alan int, ic []byte) []byte {
	var b []byte
	b = pbYazVarint(b, uint64(alan<<3|2))
	b = pbYazVarint(b, uint64(len(ic)))
	return append(b, ic...)
}

func pbYazAlanFixed32(alan int, f float32) []byte {
	var b []byte
	b = pbYazVarint(b, uint64(alan<<3|5))
	var d [4]byte
	binary.LittleEndian.PutUint32(d[:], math.Float32bits(f))
	return append(b, d[:]...)
}

func pbYazAlanVarint(alan int, v uint64) []byte {
	var b []byte
	b = pbYazVarint(b, uint64(alan<<3|0))
	return pbYazVarint(b, v)
}

func sahteUserStatusDisB64(eposta string, dolguUzunluk int, kotalar []struct {
	etiket  string
	kalan   float32
	resetSn int64
}) []byte {
	var cfgler []byte
	for _, k := range kotalar {
		ts := pbYazAlanVarint(1, uint64(k.resetSn))
		var qi []byte
		if k.kalan > 0 {
			qi = append(qi, pbYazAlanFixed32(1, k.kalan)...)
		}
		qi = append(qi, pbYazAlanUzun(2, ts)...)
		var mc []byte
		mc = append(mc, pbYazAlanUzun(1, []byte(k.etiket))...)
		mc = append(mc, pbYazAlanUzun(15, qi)...)
		cfgler = append(cfgler, pbYazAlanUzun(1, mc)...)
	}
	var userStatus []byte
	userStatus = append(userStatus, pbYazAlanUzun(3, []byte(strings.Repeat("x", dolguUzunluk)))...)
	userStatus = append(userStatus, pbYazAlanUzun(7, []byte(eposta))...)
	userStatus = append(userStatus, pbYazAlanUzun(33, cfgler)...)

	icB64 := base64.StdEncoding.EncodeToString(userStatus)
	sarmal := pbYazAlanUzun(1, []byte(icB64))
	var giris []byte
	giris = append(giris, pbYazAlanUzun(1, []byte("userStatusSentinelKey"))...)
	giris = append(giris, pbYazAlanUzun(2, sarmal)...)
	disPB := pbYazAlanUzun(1, giris)
	return []byte(base64.StdEncoding.EncodeToString(disPB))
}

func sqliteYazVarint(v uint64) []byte {
	if v == 0 {
		return []byte{0}
	}
	var parcalar [10]byte
	n := 0
	for x := v; x > 0; x >>= 7 {
		parcalar[n] = byte(x & 0x7f)
		n++
	}
	cikti := make([]byte, n)
	for i := 0; i < n; i++ {
		b := parcalar[n-1-i]
		if i < n-1 {
			b |= 0x80
		}
		cikti[i] = b
	}
	return cikti
}

// sahteVscdbOlustur: 4096 bayt sayfalı, taşma (overflow) sayfaları içeren geçerli bir SQLite3 dosyası üretir.
func sahteVscdbOlustur(satirlar [][2]string) []byte {
	const ps = 4096
	const maxLocal = ps - 35
	minLocal := ((ps - 12) * 32 / 255) - 23

	sayfalar := [][]byte{make([]byte, ps), make([]byte, ps)} // sayfa 1 (sqlite_schema), sayfa 2 (ItemTable yaprak)
	copy(sayfalar[0][0:16], []byte("SQLite format 3\x00"))
	binary.BigEndian.PutUint16(sayfalar[0][16:18], ps)
	sayfalar[0][18] = 1
	sayfalar[0][19] = 1

	sqlMetni := "CREATE TABLE ItemTable (key TEXT, value BLOB)"
	var semaBaslik []byte
	semaBaslik = append(semaBaslik, sqliteYazVarint(uint64(len("table")*2+13))...)
	semaBaslik = append(semaBaslik, sqliteYazVarint(uint64(len("ItemTable")*2+13))...)
	semaBaslik = append(semaBaslik, sqliteYazVarint(uint64(len("ItemTable")*2+13))...)
	semaBaslik = append(semaBaslik, sqliteYazVarint(1)...)
	semaBaslik = append(semaBaslik, sqliteYazVarint(uint64(len(sqlMetni)*2+13))...)
	hBoy := sqliteYazVarint(uint64(len(semaBaslik) + 1))
	var semaKayit []byte
	semaKayit = append(semaKayit, hBoy...)
	semaKayit = append(semaKayit, semaBaslik...)
	semaKayit = append(semaKayit, []byte("tableItemTableItemTable")...)
	semaKayit = append(semaKayit, 2)
	semaKayit = append(semaKayit, []byte(sqlMetni)...)

	var semaHucre []byte
	semaHucre = append(semaHucre, sqliteYazVarint(uint64(len(semaKayit)))...)
	semaHucre = append(semaHucre, sqliteYazVarint(1)...)
	semaHucre = append(semaHucre, semaKayit...)

	sayfalar[0][100] = 0x0d
	binary.BigEndian.PutUint16(sayfalar[0][103:105], 1)
	cp1 := ps - len(semaHucre)
	binary.BigEndian.PutUint16(sayfalar[0][108:110], uint16(cp1))
	copy(sayfalar[0][cp1:], semaHucre)

	sayfalar[1][0] = 0x0d
	binary.BigEndian.PutUint16(sayfalar[1][3:5], uint16(len(satirlar)))
	imlec := ps
	for i, kv := range satirlar {
		k, v := []byte(kv[0]), []byte(kv[1])
		var rBaslik []byte
		rBaslik = append(rBaslik, sqliteYazVarint(uint64(len(k)*2+13))...)
		rBaslik = append(rBaslik, sqliteYazVarint(uint64(len(v)*2+13))...)
		rhUz := sqliteYazVarint(uint64(len(rBaslik) + 1))
		if len(rhUz) > 1 {
			rhUz = sqliteYazVarint(uint64(len(rBaslik) + len(rhUz)))
		}
		var tamKayit []byte
		tamKayit = append(tamKayit, rhUz...)
		tamKayit = append(tamKayit, rBaslik...)
		tamKayit = append(tamKayit, k...)
		tamKayit = append(tamKayit, v...)

		toplam := len(tamKayit)
		yerelUz := toplam
		if toplam > maxLocal {
			kk := minLocal + ((toplam - minLocal) % (ps - 4))
			if kk <= maxLocal {
				yerelUz = kk
			} else {
				yerelUz = minLocal
			}
		}

		var ilkTasma uint32
		if toplam > yerelUz {
			kalan := tamKayit[yerelUz:]
			var oncekiIdx int = -1
			for len(kalan) > 0 {
				yeniNo := uint32(len(sayfalar) + 1)
				if ilkTasma == 0 {
					ilkTasma = yeniNo
				}
				if oncekiIdx >= 0 {
					binary.BigEndian.PutUint32(sayfalar[oncekiIdx][0:4], yeniNo)
				}
				opg := make([]byte, ps)
				parca := ps - 4
				if len(kalan) < parca {
					parca = len(kalan)
				}
				copy(opg[4:4+parca], kalan[:parca])
				kalan = kalan[parca:]
				sayfalar = append(sayfalar, opg)
				oncekiIdx = len(sayfalar) - 1
			}
		}

		var hucre []byte
		hucre = append(hucre, sqliteYazVarint(uint64(toplam))...)
		hucre = append(hucre, sqliteYazVarint(uint64(i+1))...)
		hucre = append(hucre, tamKayit[:yerelUz]...)
		if ilkTasma != 0 {
			var p4 [4]byte
			binary.BigEndian.PutUint32(p4[:], ilkTasma)
			hucre = append(hucre, p4[:]...)
		}
		imlec -= len(hucre)
		binary.BigEndian.PutUint16(sayfalar[1][8+2*i:10+2*i], uint16(imlec))
		copy(sayfalar[1][imlec:], hucre)
	}

	binary.BigEndian.PutUint32(sayfalar[0][28:32], uint32(len(sayfalar)))
	var tum []byte
	for _, s := range sayfalar {
		tum = append(tum, s...)
	}
	return tum
}

func sahteGenMetaProto(display, canon string, enumID uint64, sonAdim int) []byte {
	var ic []byte
	if enumID > 0 {
		ic = append(ic, pbYazAlanVarint(3, enumID)...)
	}
	if canon != "" {
		ic = append(ic, pbYazAlanUzun(19, []byte(canon))...)
	}
	var kv []byte
	kv = append(kv, pbYazAlanUzun(1, []byte("last_step_index"))...)
	kv = append(kv, pbYazAlanUzun(2, []byte(strconv.Itoa(sonAdim)))...)
	ic = append(ic, pbYazAlanUzun(20, kv)...)
	if display != "" {
		ic = append(ic, pbYazAlanUzun(21, []byte(display))...)
	}
	return pbYazAlanUzun(1, ic)
}

func sahteGenMetaYaprakSayfa(kayitlar [][]byte) []byte {
	const ps = 4096
	sayfa := make([]byte, ps)
	sayfa[0] = 0x0d
	binary.BigEndian.PutUint16(sayfa[3:5], uint16(len(kayitlar)))
	imlec := ps
	for i, veri := range kayitlar {
		// gen_metadata: (idx INTEGER PRIMARY KEY [NULL in record -> st=0], data BLOB, size INTEGER)
		var rBaslik []byte
		rBaslik = append(rBaslik, sqliteYazVarint(0)...)                      // idx: INTEGER PRIMARY KEY alias
		rBaslik = append(rBaslik, sqliteYazVarint(uint64(len(veri)*2+12))...) // data BLOB
		rBaslik = append(rBaslik, sqliteYazVarint(2)...)                      // size 2-bayt int
		rhUz := sqliteYazVarint(uint64(len(rBaslik) + 1))
		if len(rhUz) > 1 {
			rhUz = sqliteYazVarint(uint64(len(rBaslik) + len(rhUz)))
		}
		var sz2 [2]byte
		binary.BigEndian.PutUint16(sz2[:], uint16(len(veri)))
		var tamKayit []byte
		tamKayit = append(tamKayit, rhUz...)
		tamKayit = append(tamKayit, rBaslik...)
		tamKayit = append(tamKayit, veri...)
		tamKayit = append(tamKayit, sz2[:]...)

		var hucre []byte
		hucre = append(hucre, sqliteYazVarint(uint64(len(tamKayit)))...)
		hucre = append(hucre, sqliteYazVarint(uint64(i+1))...)
		hucre = append(hucre, tamKayit...)
		imlec -= len(hucre)
		binary.BigEndian.PutUint16(sayfa[8+2*i:10+2*i], uint16(imlec))
		copy(sayfa[imlec:], hucre)
	}
	return sayfa
}

func sahteKonusmaDbVeWalOlustur(dbKayitlar, walKayitlar [][]byte) ([]byte, []byte) {
	const ps = 4096
	sayfa1 := make([]byte, ps)
	copy(sayfa1[0:16], []byte("SQLite format 3\x00"))
	binary.BigEndian.PutUint16(sayfa1[16:18], ps)
	sayfa1[18] = 2
	sayfa1[19] = 2

	// sqlite_schema: hem steps (sayfa 3, içi kasıtlı çöp - asla okunmamalı!) hem gen_metadata (sayfa 2) tanımla
	semaHucreOlustur := func(rowID uint64, tablo string, kokSayfa byte, sqlMetni string) []byte {
		var sb []byte
		sb = append(sb, sqliteYazVarint(uint64(len("table")*2+13))...)
		sb = append(sb, sqliteYazVarint(uint64(len(tablo)*2+13))...)
		sb = append(sb, sqliteYazVarint(uint64(len(tablo)*2+13))...)
		sb = append(sb, sqliteYazVarint(1)...)
		sb = append(sb, sqliteYazVarint(uint64(len(sqlMetni)*2+13))...)
		hb := sqliteYazVarint(uint64(len(sb) + 1))
		var kayit []byte
		kayit = append(kayit, hb...)
		kayit = append(kayit, sb...)
		kayit = append(kayit, []byte("table"+tablo+tablo)...)
		kayit = append(kayit, kokSayfa)
		kayit = append(kayit, []byte(sqlMetni)...)
		var hucre []byte
		hucre = append(hucre, sqliteYazVarint(uint64(len(kayit)))...)
		hucre = append(hucre, sqliteYazVarint(rowID)...)
		return append(hucre, kayit...)
	}

	h1 := semaHucreOlustur(1, "steps", 3, "CREATE TABLE steps (idx INTEGER PRIMARY KEY, step_payload BLOB)")
	h2 := semaHucreOlustur(2, "gen_metadata", 2, "CREATE TABLE gen_metadata (idx INTEGER PRIMARY KEY, data BLOB, size INTEGER)")
	sayfa1[100] = 0x0d
	binary.BigEndian.PutUint16(sayfa1[103:105], 2)
	imlec := ps - len(h1)
	copy(sayfa1[imlec:], h1)
	binary.BigEndian.PutUint16(sayfa1[108:110], uint16(imlec))
	imlec -= len(h2)
	copy(sayfa1[imlec:], h2)
	binary.BigEndian.PutUint16(sayfa1[110:112], uint16(imlec))
	binary.BigEndian.PutUint16(sayfa1[105:107], uint16(imlec))
	binary.BigEndian.PutUint32(sayfa1[28:32], 3)

	sayfa2 := sahteGenMetaYaprakSayfa(dbKayitlar)
	sayfa3 := make([]byte, ps) // steps kök sayfası (hiç okunmamalı)
	for i := range sayfa3 {
		sayfa3[i] = 0xaa
	}

	var db []byte
	db = append(db, sayfa1...)
	db = append(db, sayfa2...)
	db = append(db, sayfa3...)

	// .db-wal oluştur: geçerli başlık + sayfa 2'yi güncelleyen commit çerçevesi + sahte eski tuzlu artık çerçeve
	var whdr [32]byte
	binary.BigEndian.PutUint32(whdr[0:4], 0x377f0682) // little-endian WAL checksum magic
	binary.BigEndian.PutUint32(whdr[4:8], 3007000)
	binary.BigEndian.PutUint32(whdr[8:12], ps)
	binary.BigEndian.PutUint32(whdr[12:16], 1)
	binary.BigEndian.PutUint32(whdr[16:20], 0x11223344) // salt-1
	binary.BigEndian.PutUint32(whdr[20:24], 0x55667788) // salt-2
	s0, s1 := sqliteWalChecksum(true, whdr[0:24], 0, 0)
	binary.BigEndian.PutUint32(whdr[24:28], s0)
	binary.BigEndian.PutUint32(whdr[28:32], s1)

	walSayfa2 := sahteGenMetaYaprakSayfa(walKayitlar)
	var fhdr [24]byte
	binary.BigEndian.PutUint32(fhdr[0:4], 2)          // pgNo = 2 (gen_metadata)
	binary.BigEndian.PutUint32(fhdr[4:8], 3)          // dbBoy > 0 -> commit çerçevesi!
	binary.BigEndian.PutUint32(fhdr[8:12], 0x11223344) // salt-1
	binary.BigEndian.PutUint32(fhdr[12:16], 0x55667788) // salt-2
	s0, s1 = sqliteWalChecksum(true, fhdr[0:8], s0, s1)
	s0, s1 = sqliteWalChecksum(true, walSayfa2, s0, s1)
	binary.BigEndian.PutUint32(fhdr[16:20], s0)
	binary.BigEndian.PutUint32(fhdr[20:24], s1)

	// Eski nesilden kalan (farklı salt taşıyan) bozuk çerçeve: okuyucu burada durmalı ve sayfa 2 commit'ini korumalı!
	var eskiFhdr [24]byte
	binary.BigEndian.PutUint32(eskiFhdr[0:4], 2)
	binary.BigEndian.PutUint32(eskiFhdr[4:8], 3)
	binary.BigEndian.PutUint32(eskiFhdr[8:12], 0xdeadbeef) // eski salt-1
	binary.BigEndian.PutUint32(eskiFhdr[12:16], 0xcafebabe)
	bozukSayfa := sahteGenMetaYaprakSayfa([][]byte{
		sahteGenMetaProto("Bozuk Model", "bozuk-model", 0, 1),
	})

	var wal []byte
	wal = append(wal, whdr[:]...)
	wal = append(wal, fhdr[:]...)
	wal = append(wal, walSayfa2...)
	wal = append(wal, eskiFhdr[:]...)
	wal = append(wal, bozukSayfa...)
	return db, wal
}

func TestAgyKonusmaDbVeWalModelCozumleme(t *testing.T) {
	eskiDizin := dizin
	dizin = t.TempDir()
	defer func() { dizin = eskiDizin }()

	ev := t.TempDir()
	t.Setenv("HOME", ev)
	t.Setenv("USERPROFILE", ev)
	t.Setenv("GEMINI_CLI_HOME", "")

	simdi := time.Now().UTC().Truncate(time.Second)
	agyKok := filepath.Join(ev, ".gemini", "antigravity")
	convID := "6f60e323-2a7a-42f0-be9f-be95b73617ed"
	logDir := filepath.Join(agyKok, "brain", convID, ".system_generated", "logs")
	convDir := filepath.Join(agyKok, "conversations")
	_ = os.MkdirAll(logDir, 0o755)
	_ = os.MkdirAll(convDir, 0o755)

	// 1) Önce yalnızca antigravity_state.pbtxt varken (henüz .db oluşmamışken) MODEL_PLACEHOLDER_M417 -> agy-gemini-4-argon çözülmeli
	_ = os.WriteFile(filepath.Join(agyKok, "antigravity_state.pbtxt"), []byte("last_selected_agent_model: MODEL_PLACEHOLDER_M417\n"), 0o600)

	trYol := filepath.Join(logDir, "transcript.jsonl")
	dokum := `{"step_index":1,"type":"USER_INPUT","created_at":"` + simdi.Add(-20*time.Minute).Format(time.RFC3339) + `","content":"merhaba"}` + "\n" +
		`{"step_index":2,"type":"PLANNER_RESPONSE","created_at":"` + simdi.Add(-19*time.Minute).Format(time.RFC3339) + `","model":"","input_tokens":1000,"cache_read_tokens":5000,"output_tokens":200}` + "\n" +
		`{"step_index":4,"type":"PLANNER_RESPONSE","created_at":"` + simdi.Add(-15*time.Minute).Format(time.RFC3339) + `","model":"","input_tokens":400,"cache_read_tokens":1200,"output_tokens":80}` + "\n" +
		`{"step_index":6,"type":"PLANNER_RESPONSE","created_at":"` + simdi.Add(-10*time.Minute).Format(time.RFC3339) + `","model":"","input_tokens":2500,"cache_read_tokens":9000,"output_tokens":350}` + "\n"
	if err := os.WriteFile(trYol, []byte(dokum), 0o644); err != nil {
		t.Fatal(err)
	}

	ilk := toplaDiger(simdi.Unix())
	ilkModeller := map[string]Satir{}
	for _, r := range ilk.satirlar {
		ilkModeller[r.Model] = r
	}
	if ilkModeller["agy-gemini-4-argon"].Messages != 3 {
		t.Fatalf("antigravity_state.pbtxt yedeği çalışmalıydı: %+v", ilkModeller)
	}

	// 2) Şimdi transcript.jsonl hiç değişmeden conversations/<conv_id>.db ve .db-wal yazılsın:
	//    .db içinde yalnızca ilk 2 kayıt varken, .db-wal 3. kaydı (Claude Opus 5.5) ekliyor!
	k1 := sahteGenMetaProto("Gemini 4 Carbon (High)", "gemini-4-carbon", 1427, 1)
	k2 := sahteGenMetaProto("Gemini 3.5 Flash (Medium)", "gemini-3-flash-a", 1020, 3)
	k3 := sahteGenMetaProto("Claude Opus 5.5 (High)", "claude-opus-5-5-high", 1401, 5)

	dbBayt, walBayt := sahteKonusmaDbVeWalOlustur([][]byte{k1, k2}, [][]byte{k1, k2, k3})
	dbYol := filepath.Join(convDir, convID+".db")
	if err := os.WriteFile(dbYol, dbBayt, 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(dbYol+"-wal", walBayt, 0o644); err != nil {
		t.Fatal(err)
	}

	// DbMtime değiştiği için önbellek ofset 0'dan yeniden çözülmeli ve 3 model tam sayılarla ayrışmalı!
	ikinci := toplaDiger(simdi.Unix() + 5)
	byModel := map[string]Satir{}
	for _, r := range ikinci.satirlar {
		byModel[r.Model] = r
	}
	if c := byModel["agy-gemini-4-carbon"]; c.Messages != 1 || c.Input != 1000 || c.Cr != 5000 || c.Output != 200 {
		t.Fatalf("agy-gemini-4-carbon satırı hatalı: %+v (tümü=%+v)", c, byModel)
	}
	if fl := byModel["agy-gemini-3.5-flash"]; fl.Messages != 1 || fl.Input != 400 || fl.Cr != 1200 || fl.Output != 80 {
		t.Fatalf("agy-gemini-3.5-flash satırı hatalı: %+v (tümü=%+v)", fl, byModel)
	}
	if op := byModel["agy-claude-opus-5-5"]; op.Messages != 1 || op.Input != 2500 || op.Cr != 9000 || op.Output != 350 {
		t.Fatalf("agy-claude-opus-5-5 (WAL üzerinden) satırı hatalı: %+v (tümü=%+v)", op, byModel)
	}
	if _, eskiKaldi := byModel["agy-gemini-4-argon"]; eskiKaldi {
		t.Fatalf("DbMtime güncellendiğinde eski pbtxt yedeği yenilenmeliydi: %+v", byModel)
	}
}

func TestAgyGercekKonusmaDbDumanTesti(t *testing.T) {
	ev, err := os.UserHomeDir()
	if err != nil || ev == "" {
		t.Skip("ev dizini yok")
	}
	convDir := filepath.Join(ev, ".gemini", "antigravity", "conversations")
	girdiler, err := os.ReadDir(convDir)
	if err != nil {
		t.Skip("yerel Antigravity conversations klasörü yok")
	}
	sayac := 0
	cozulen := 0
	for _, e := range girdiler {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".db") {
			continue
		}
		yol := filepath.Join(convDir, e.Name())
		ilk, adimlar := agyKonusmaModelleriOku(yol)
		if ilk != "" || len(adimlar) > 0 {
			cozulen++
		}
		sayac++
		if sayac >= 25 {
			break
		}
	}
	t.Logf("gerçek Antigravity duman testi: %d .db incelendi, %d tanesinde gen_metadata modeli çözüldü", sayac, cozulen)
}

func uygulamaKlasoruTestYolu(ev, ad string) string {
	switch runtime.GOOS {
	case "darwin":
		return filepath.Join(ev, "Library", "Application Support", ad)
	case "windows":
		return filepath.Join(ev, "AppData", "Roaming", ad)
	default:
		return filepath.Join(ev, ".config", ad)
	}
}

func TestAgySqliteTasmaKotaVeHesapOnceligi(t *testing.T) {
	eskiDizin, eskiTestModu := dizin, testModu
	dizin = t.TempDir()
	testModu = false
	defer func() { dizin = eskiDizin; testModu = eskiTestModu }()

	ev := t.TempDir()
	t.Setenv("HOME", ev)
	t.Setenv("USERPROFILE", ev)
	t.Setenv("GEMINI_CLI_HOME", "")

	simdi := time.Now().Unix()

	eskiKok := uygulamaKlasoruTestYolu(ev, "Antigravity")
	eskiDbYol := filepath.Join(eskiKok, "User", "globalStorage", "state.vscdb")
	_ = os.MkdirAll(filepath.Dir(eskiDbYol), 0o755)
	eskiB64 := sahteUserStatusDisB64("eski@ornek.com", 5000, []struct {
		etiket  string
		kalan   float32
		resetSn int64
	}{
		{"Claude Opus 5.5", 0.01, simdi - 3600},
	})
	if err := os.WriteFile(eskiDbYol, sahteVscdbOlustur([][2]string{
		{"antigravityAuthStatus", "gizli-okunmamali"},
		{agyUserStatusKey, string(eskiB64)},
	}), 0o644); err != nil {
		t.Fatal(err)
	}
	eskiZaman := time.Unix(simdi-5*86400, 0)
	_ = os.Chtimes(eskiDbYol, eskiZaman, eskiZaman)

	yeniKok := uygulamaKlasoruTestYolu(ev, "Antigravity IDE")
	yeniDbYol := filepath.Join(yeniKok, "User", "globalStorage", "state.vscdb")
	_ = os.MkdirAll(filepath.Dir(yeniDbYol), 0o755)
	yeniB64 := sahteUserStatusDisB64("aktif@firma.com", 7000, []struct {
		etiket  string
		kalan   float32
		resetSn int64
	}{
		{"Claude Opus 5.5 (Thinking)", 0.38, simdi + 4*86400},
		{"Gemini 4 Argon", 0.75, simdi + 5*86400},
		{"Gemini 3.8 Flash", 0.80, simdi + 3*3600},
		{"Eski Model", 0.0, simdi - 120},
	})
	dbBayt := sahteVscdbOlustur([][2]string{
		{"jetskiStateSync.agentManagerInitState", strings.Repeat("Z", 6000)},
		{agyUserStatusKey, string(yeniB64)},
	})
	if len(dbBayt) <= 3*4096 {
		t.Fatalf("taşma sayfaları oluşmalıydı, boyut=%d", len(dbBayt))
	}
	if err := os.WriteFile(yeniDbYol, dbBayt, 0o644); err != nil {
		t.Fatal(err)
	}
	yeniZaman := time.Unix(simdi-300, 0)
	_ = os.Chtimes(yeniDbYol, yeniZaman, yeniZaman)

	hesapHex, eposta, lim, varMi := geminiHesapVeLimitOku(simdi)
	beklenenHex := hesapOzeti("gemini", "aktif@firma.com")
	if hesapHex != beklenenHex || eposta != "aktif@firma.com" {
		t.Fatalf("en güncel e-posta seçilmeliydi: got=(%q,%q) want=(%q,%q)", hesapHex, eposta, beklenenHex, "aktif@firma.com")
	}
	if !varMi {
		t.Fatalf("aktif Antigravity kotası okunmalıydı")
	}
	if got := string(lim.R["seven_day.used_percentage"]); got != "25" {
		t.Fatalf("haftalık kota Gemini havuzunu önceliklendirmeli: got=%q want=25 (tüm=%v)", got, lim.R)
	}
	if got := string(lim.R["seven_day.resets_at"]); got != strconv.FormatInt(simdi+5*86400, 10) {
		t.Fatalf("haftalık sıfırlama Gemini havuzunu önceliklendirmeli: got=%q", got)
	}
	if got := string(lim.R["five_hour.used_percentage"]); got != "20" {
		t.Fatalf("5 saatlik kota hatalı: got=%q want=20", got)
	}
	if got := string(lim.R["agy_claude.used_percentage"]); got != "62" {
		t.Fatalf("agy_claude kotası hatalı: got=%q want=62", got)
	}
	if got := string(lim.R["agy_gemini.used_percentage"]); got != "25" {
		t.Fatalf("agy_gemini kotası hatalı: got=%q want=25", got)
	}

	asYol := filepath.Join(eskiKok, "app_storage.json")
	_ = os.WriteFile(asYol, []byte(`{"jetski.onboarding.lastLoginUsername":"yenihesap@firma.com"}`), 0o600)
	enYeniZaman := time.Unix(simdi-10, 0)
	_ = os.Chtimes(asYol, enYeniZaman, enYeniZaman)

	hesapHex2, eposta2, _, varMi2 := geminiHesapVeLimitOku(simdi)
	if hesapHex2 != hesapOzeti("gemini", "yenihesap@firma.com") || eposta2 != "yenihesap@firma.com" {
		t.Fatalf("app_storage.json en yeni olduğunda onun e-postası seçilmeli: got=%q", eposta2)
	}
	if varMi2 {
		t.Fatalf("farklı hesaba ait state.vscdb kotası yeni hesaba taşınmamalı")
	}
}

func TestGeminiIzoleUzunBaglamVeSupheli(t *testing.T) {
	eskiDizin := dizin
	dizin = t.TempDir()
	defer func() { dizin = eskiDizin }()

	ev := t.TempDir()
	t.Setenv("HOME", ev)
	t.Setenv("USERPROFILE", ev)
	t.Setenv("GEMINI_CLI_HOME", "")
	t.Setenv("GOOGLE_GEMINI_BASE_URL", "")

	esikleriKaydet(map[string]int64{"gemini-3.1-pro": 200000})
	okunan := esikleriOku()
	if esikBul(okunan, "agy-gemini-3.1-pro-preview") != 200000 {
		t.Fatalf("agy-gemini-3.1-pro-preview eşiği 200000 olmalı")
	}

	simdi := time.Now().UTC().Truncate(time.Second)
	logDir := filepath.Join(ev, ".gemini", "antigravity", "brain", "aktif-konusma", ".system_generated", "logs")
	_ = os.MkdirAll(logDir, 0o755)
	trYol := filepath.Join(logDir, "transcript.jsonl")
	satir1 := `{"step_index":0,"type":"USER_INPUT","created_at":"` + simdi.Add(-20*time.Minute).Format(time.RFC3339) + `","content":"The user changed setting ` + "`Model Selection`" + ` from None to Gemini 3.1 Pro (High). ok"}` + "\n" +
		`{"step_index":1,"type":"PLANNER_RESPONSE","created_at":"` + simdi.Add(-19*time.Minute).Format(time.RFC3339) + `","input_tokens":50000,"cache_read_tokens":180000,"output_tokens":1200}` + "\n"
	_ = os.WriteFile(trYol, []byte(satir1), 0o644)

	ilk := toplaDiger(simdi.Unix())
	modeller := map[string]int64{}
	for _, r := range ilk.satirlar {
		modeller[r.Model] = r.Input + r.Cr + r.Output
	}
	if modeller["agy-gemini-3.1-pro@uzun"] != 231200 {
		t.Fatalf(">200k Antigravity isteği @uzun olmalıydı: %+v", modeller)
	}
}

func TestAgyStatusLineKotaVeAyarKoruma(t *testing.T) {
	eskiDizin, eskiTestModu := dizin, testModu
	dizin = t.TempDir()
	testModu = true
	defer func() {
		dizin = eskiDizin
		testModu = eskiTestModu
	}()

	ev := t.TempDir()
	t.Setenv("HOME", ev)
	t.Setenv("USERPROFILE", ev)
	agyCliDir := filepath.Join(ev, ".gemini", "antigravity-cli")
	t.Setenv(testAgyCliDizin, agyCliDir)

	simdi := int64(1794448616)
	girdi := []byte(`{
		"product": "antigravity",
		"email": "ornek@ekip.com",
		"plan_tier": "pro",
		"quota": {
			"gemini-weekly": { "remaining_fraction": 0.48571146, "reset_time": "2026-11-14T08:44:04Z", "reset_in_seconds": 197228 },
			"gemini-5h":     { "remaining_fraction": 0.6231997,  "reset_time": "2026-11-12T03:26:45Z", "reset_in_seconds": 5389 },
			"3p-weekly":     { "remaining_fraction": 0.02278612, "reset_time": "2026-11-17T12:09:53Z", "reset_in_seconds": 468777 },
			"3p-5h":         { "remaining_fraction": 0.87698054, "reset_time": "2026-11-12T05:45:51Z", "reset_in_seconds": 13735 }
		}
	}`)
	coz, ok := agyStatusCoz(girdi, simdi)
	if !ok {
		t.Fatal("agyStatusCoz geçerli dönmeliydi")
	}
	if coz.Eposta != "ornek@ekip.com" || coz.HaftaReset != 1794645844 {
		t.Fatalf("eposta veya Gemini haftalık sıfırlama hatalı: eposta=%q reset=%d", coz.Eposta, coz.HaftaReset)
	}
	if coz.R["seven_day.used_percentage"] != "51.4" || coz.R["seven_day.resets_at"] != "1794645844" {
		t.Fatalf("seven_day Gemini havuzundan gelmeliydi: %v", coz.R)
	}
	if coz.R["five_hour.used_percentage"] != "37.7" || coz.R["agy_gemini_5h.used_percentage"] != "37.7" {
		t.Fatalf("five_hour Gemini 5s havuzundan gelmeliydi: %v", coz.R)
	}
	if coz.R["agy_claude.used_percentage"] != "97.7" || coz.R["agy_claude_5h.used_percentage"] != "12.3" {
		t.Fatalf("3p havuz yüzdeleri hatalı: %v", coz.R)
	}

	tukenen := []byte(`{"email":"ornek@ekip.com","quota":{"gemini-weekly":{"reset_time":"2026-11-14T08:44:04Z"}}}`)
	cozTukenen, okT := agyStatusCoz(tukenen, simdi)
	if !okT || cozTukenen.R["seven_day.used_percentage"] != "100" {
		t.Fatalf("tükenen kota %%100 olmalıydı: %+v", cozTukenen)
	}

	agyLimitYaz(coz)
	hHex, ep, lim, limVar, hReset := geminiHesapLimitVeResetOku(simdi)
	if !limVar || ep != "ornek@ekip.com" || hHex != hesapOzeti("gemini", "ornek@ekip.com") || hReset != 1794645844 {
		t.Fatalf("geminiHesapLimitVeResetOku hatalı: limVar=%v ep=%q hReset=%d", limVar, ep, hReset)
	}
	if string(lim.R["seven_day.used_percentage"]) != "51.4" || string(lim.R["agy_claude.used_percentage"]) != "97.7" {
		t.Fatalf("limit haritası hatalı: %v", lim.R)
	}

	_ = os.MkdirAll(agyCliDir, 0o700)
	ayarYol := filepath.Join(agyCliDir, "settings.json")
	_ = os.WriteFile(ayarYol, []byte(`{"statusLine":{"type":"command","command":"echo ozel"}}`), 0o600)
	_ = ayarYaz(Ayar{URL: "https://ornek.com", Cihaz: strings.Repeat("a", 32), Etiket: "test", UyariKuruldu: []string{"/ornek/test-dizin"}})

	agyDurumSatiriKur()
	okunanAyar, err := ayarOku()
	if err != nil {
		t.Fatalf("ayarOku hatası: %v", err)
	}
	if err := ayarYaz(okunanAyar); err != nil {
		t.Fatal(err)
	}
	okunanAyar2, _ := ayarOku()
	if len(okunanAyar2.UyariKuruldu) != 1 || len(okunanAyar2.AgyDurumKuruldu) != 1 {
		t.Fatalf("ayarYaz UyariKuruldu ve AgyDurumKuruldu alanlarını korumalıydı: %+v", okunanAyar2)
	}
	if onceki := agyOncekiKomut(dizinAnahtari(agyCliDir)); onceki != "echo ozel" {
		t.Fatalf("önceki statusLine komutu korunmalıydı: got=%q", onceki)
	}

	agyDurumSatiriKaldir()
	geriYuklenen, _ := os.ReadFile(ayarYol)
	if !strings.Contains(string(geriYuklenen), "echo ozel") || strings.Contains(string(geriYuklenen), agyDurumAnahtar) {
		t.Fatalf("kaldir sonrası önceki statusLine geri gelmeliydi: %s", string(geriYuklenen))
	}
}

func TestAgyPtyCalistir(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Unix kabuk betiği testi")
	}
	gec := t.TempDir()
	isaret := filepath.Join(gec, "okundu")
	betik := filepath.Join(gec, "sahte-agy.sh")
	icerik := "#!/bin/sh\ntouch \"" + isaret + "\"\nread -r _\nexit 0\n"
	if err := os.WriteFile(betik, []byte(icerik), 0o755); err != nil {
		t.Fatal(err)
	}
	bas := time.Now()
	agyPtyCalistir(betik, gec, 4*time.Second, func() bool {
		_, err := os.Stat(isaret)
		return err == nil
	})
	if _, err := os.Stat(isaret); err != nil {
		t.Fatalf("PTY içindeki betik çalışmalıydı: %v", err)
	}
	if gecen := time.Since(bas); gecen > 3*time.Second {
		t.Fatalf("PTY hazir() sonrası hızlıca kapanmalıydı: %v", gecen)
	}
}
