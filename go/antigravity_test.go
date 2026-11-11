package main

import (
	"encoding/base64"
	"encoding/binary"
	"encoding/json"
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
		"Gemini 4 Carbon (High)":    "agy-gemini-4-carbon",
		"Gemini 3.8 Flash (Medium)": "agy-gemini-3.8-flash",
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
	ilk := `{"step_index":0,"type":"USER_INPUT","created_at":"` + ts(-30*time.Minute) + `","content":"<USER_SETTINGS_CHANGE>The user changed setting ` + "`Model Selection`" + ` from None to Claude Opus 5.5 (Medium). No need to comment.</USER_SETTINGS_CHANGE>"}` + "\n" +
		`{"step_index":1,"type":"PLANNER_RESPONSE","created_at":"` + ts(-29*time.Minute) + `","input_tokens":1000,"cache_read_tokens":5000,"output_tokens":200,"thinking":"gizli"}` + "\n"
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
	ek := `{"step_index":2,"type":"USER_INPUT","created_at":"` + ts(-10*time.Minute) + `","content":"The user changed setting ` + "`Model Selection`" + ` from Claude Opus 5.5 (Medium) to Gemini 4 Carbon (High). ok"}` + "\n" +
		`{"step_index":3,"type":"PLANNER_RESPONSE","created_at":"` + ts(-9*time.Minute) + `","input_tokens":300,"cache_read_tokens":0,"output_tokens":50}` + "\n" +
		`{"step_index":4,"type":"PLANNER_RESPONSE","created_at":"` + ts(-8*time.Minute) + `","input_tok`
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

// ---------- Protobuf ve SQLite3 taşma sayfası test yardımcıları ----------

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

	// Sayfa 1: sqlite_schema içinde ("table", "ItemTable", "ItemTable", 2, "CREATE TABLE ItemTable (key TEXT, value BLOB)")
	sqlMetni := "CREATE TABLE ItemTable (key TEXT, value BLOB)"
	var semaBaslik []byte
	semaBaslik = append(semaBaslik, sqliteYazVarint(uint64(len("table")*2+13))...)
	semaBaslik = append(semaBaslik, sqliteYazVarint(uint64(len("ItemTable")*2+13))...)
	semaBaslik = append(semaBaslik, sqliteYazVarint(uint64(len("ItemTable")*2+13))...)
	semaBaslik = append(semaBaslik, sqliteYazVarint(1)...) // 1 bayt tamsayı (rootpage = 2)
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

	// Sayfa 2: ItemTable yaprak sayfası + taşma sayfaları
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

	// 1. Eski tarihli Antigravity state.vscdb (farklı/eski e-posta + süresi geçmiş kota)
	eskiKok := uygulamaKlasoruTestYolu(ev, "Antigravity")
	eskiDbYol := filepath.Join(eskiKok, "User", "globalStorage", "state.vscdb")
	_ = os.MkdirAll(filepath.Dir(eskiDbYol), 0o755)
	eskiB64 := sahteUserStatusDisB64("eski@ornek.com", 5000, []struct {
		etiket  string
		kalan   float32
		resetSn int64
	}{
		{"Claude Opus 5.5", 0.01, simdi - 3600}, // dün sıfırlanmış: yok sayılmalı
	})
	if err := os.WriteFile(eskiDbYol, sahteVscdbOlustur([][2]string{
		{"antigravityAuthStatus", "gizli-okunmamali"},
		{agyUserStatusKey, string(eskiB64)},
	}), 0o644); err != nil {
		t.Fatal(err)
	}
	eskiZaman := time.Unix(simdi-5*86400, 0)
	_ = os.Chtimes(eskiDbYol, eskiZaman, eskiZaman)

	// 2. Yeni tarihli Antigravity IDE state.vscdb (taşma sayfalı >9 KB değer + aktif 7g ve 5sa kotalar)
	yeniKok := uygulamaKlasoruTestYolu(ev, "Antigravity IDE")
	yeniDbYol := filepath.Join(yeniKok, "User", "globalStorage", "state.vscdb")
	_ = os.MkdirAll(filepath.Dir(yeniDbYol), 0o755)
	yeniB64 := sahteUserStatusDisB64("aktif@firma.com", 7000, []struct {
		etiket  string
		kalan   float32
		resetSn int64
	}{
		{"Claude Opus 5.5 (Thinking)", 0.38, simdi + 4*86400}, // %62 haftalık
		{"Gemini 4 Argon", 0.75, simdi + 5*86400},             // %25 haftalık
		{"Gemini 3.8 Flash", 0.80, simdi + 3*3600},            // %20 5 saatlik
		{"Eski Model", 0.0, simdi - 120},                      // süresi geçmiş: yok sayılmalı
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
	if got := string(lim.R["seven_day.used_percentage"]); got != "62" {
		t.Fatalf("haftalık kota hatalı: got=%q want=62 (tüm=%v)", got, lim.R)
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

	// 3. Daha yeni app_storage.json farklı bir e-postaya geçerse yeni e-posta kazanmalı ve eski hesabın kotası sızmamalı
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

func TestDigerUzunBaglamEsikleri(t *testing.T) {
	eskiDizin := dizin
	dizin = t.TempDir()
	defer func() { dizin = eskiDizin }()

	ev := t.TempDir()
	t.Setenv("HOME", ev)
	t.Setenv("USERPROFILE", ev)
	t.Setenv("CODEX_HOME", filepath.Join(ev, ".codex"))
	t.Setenv("GEMINI_CLI_HOME", filepath.Join(ev, ".gemini"))

	esikleriKaydet(map[string]int64{
		"claude-haiku-5-5": 100000,
		"gemini-3.1-pro":   200000,
		"gpt-6.1-sol":      272000,
		"gpt-5.4":          272000,
	})
	okunan := esikleriOku()
	if esikBul(okunan, "agy-gemini-3.1-pro-preview") != 200000 {
		t.Fatalf("agy-gemini-3.1-pro-preview eşiği 200000 olmalı")
	}
	if esikBul(okunan, "gpt-6.1-sol-2026-11-01") != 272000 {
		t.Fatalf("gpt-6.1-sol-2026-11-01 eşiği 272000 olmalı")
	}
	if esikBul(okunan, "gpt-5.4-mini") != 0 {
		t.Fatalf("gpt-5.4-mini için uzun bağlam eşiği olmamalı")
	}

	simdi := time.Now().UTC().Truncate(time.Second)
	agyYol := filepath.Join(ev, ".gemini", "antigravity-ide", "brain", "konusma-1", ".system_generated", "logs")
	_ = os.MkdirAll(agyYol, 0o755)
	dokum := `{"step_index":0,"type":"USER_INPUT","created_at":"` + simdi.Add(-15*time.Minute).Format(time.RFC3339) + `","content":"The user changed setting ` + "`Model Selection`" + ` from None to Gemini 3.1 Pro (High). ok"}` + "\n" +
		`{"step_index":1,"type":"PLANNER_RESPONSE","created_at":"` + simdi.Add(-14*time.Minute).Format(time.RFC3339) + `","input_tokens":50000,"cache_read_tokens":180000,"output_tokens":1200}` + "\n"
	_ = os.WriteFile(filepath.Join(agyYol, "transcript.jsonl"), []byte(dokum), 0o644)

	son := toplaDiger(simdi.Unix())
	modeller := map[string]int64{}
	for _, r := range son.satirlar {
		modeller[r.Model] = r.Input + r.Cr + r.Output
	}
	if modeller["agy-gemini-3.1-pro@uzun"] != 231200 {
		t.Fatalf(">200k Antigravity isteği @uzun olarak ayrılmalıydı: %+v", modeller)
	}
	if _, tabanVar := modeller["agy-gemini-3.1-pro"]; !tabanVar {
		t.Fatalf("sıfırlama için taban model satırı da üretilmeliydi: %+v", modeller)
	}
}

func TestUcSaglayiciEpostaVeGizlilik(t *testing.T) {
	eskiDizin, eskiTestModu := dizin, testModu
	dizin = t.TempDir()
	testModu = false
	defer func() { dizin = eskiDizin; testModu = eskiTestModu }()

	ev := t.TempDir()
	t.Setenv("HOME", ev)
	t.Setenv("USERPROFILE", ev)
	t.Setenv("CLAUDE_CONFIG_DIR", "")
	t.Setenv("CODEX_HOME", "")
	t.Setenv("GEMINI_CLI_HOME", "")

	// 1. Claude: ~/.claude.json
	claudeJSON := `{"oauthAccount":{"organizationUuid":"org-ortak-123","accountUuid":"acc-999","emailAddress":"  Claude.Ortak@Firma.com "}}`
	_ = os.WriteFile(filepath.Join(ev, ".claude.json"), []byte(claudeJSON), 0o600)

	// 2. ChatGPT / Codex: ~/.codex/auth.json (gizli alanlar asla gövdeye girmemeli)
	_ = os.MkdirAll(filepath.Join(ev, ".codex"), 0o700)
	jwtHdr := base64.RawURLEncoding.EncodeToString([]byte(`{"alg":"RS256"}`))
	jwtPay := base64.RawURLEncoding.EncodeToString([]byte(`{"email":"ChatGPT.Ekip@Firma.com","https://api.openai.com/auth":{"chatgpt_account_id":"acct-ortak-456"}}`))
	codexAuth := `{"OPENAI_API_KEY":"sk-gizli-anahtar-sizmamali","tokens":{"account_id":"acct-ortak-456","access_token":"at-gizli-sizmamali","refresh_token":"rt-gizli-sizmamali","id_token":"` + jwtHdr + `.` + jwtPay + `.imza-sizmamali"}}`
	_ = os.WriteFile(filepath.Join(ev, ".codex", "auth.json"), []byte(codexAuth), 0o600)

	// 3. Gemini: ~/.gemini/google_accounts.json
	_ = os.MkdirAll(filepath.Join(ev, ".gemini"), 0o700)
	_ = os.WriteFile(filepath.Join(ev, ".gemini", "google_accounts.json"), []byte(`{"active":"Gemini.Ortak@Firma.com"}`), 0o600)

	simdi := time.Now().Unix()
	govde, _, err := govdeHazirla(simdi)
	if err != nil {
		t.Fatal(err)
	}
	ham := string(govde)
	for _, yasak := range []string{"sk-gizli-anahtar-sizmamali", "at-gizli-sizmamali", "rt-gizli-sizmamali", "imza-sizmamali", "org-ortak-123", "acct-ortak-456"} {
		if strings.Contains(ham, yasak) {
			t.Fatalf("gizli bilgi gövdeye sızdı: %s", yasak)
		}
	}

	son := toplaDiger(simdi)
	if son.epostalar["claude"] != "claude.ortak@firma.com" {
		t.Fatalf("claude e-posta hatalı: %q", son.epostalar["claude"])
	}
	if son.epostalar["chatgpt"] != "chatgpt.ekip@firma.com" {
		t.Fatalf("chatgpt e-posta hatalı: %q", son.epostalar["chatgpt"])
	}
	if son.epostalar["gemini"] != "gemini.ortak@firma.com" {
		t.Fatalf("gemini e-posta hatalı: %q", son.epostalar["gemini"])
	}
	if son.hesaplar["claude"] != hesapOzeti("claude", "org-ortak-123") {
		t.Fatalf("claude özet hatalı: %q", son.hesaplar["claude"])
	}
	if son.hesaplar["chatgpt"] != hesapOzeti("chatgpt", "acct-ortak-456") {
		t.Fatalf("chatgpt özet hatalı: %q", son.hesaplar["chatgpt"])
	}
	if son.hesaplar["gemini"] != hesapOzeti("gemini", "gemini.ortak@firma.com") {
		t.Fatalf("gemini özet hatalı: %q", son.hesaplar["gemini"])
	}
}

func TestGeminiVeChatGPTSupheliTespiti(t *testing.T) {
	eskiDizin := dizin
	dizin = t.TempDir()
	defer func() { dizin = eskiDizin }()

	ev := t.TempDir()
	t.Setenv("HOME", ev)
	t.Setenv("USERPROFILE", ev)
	t.Setenv("CLAUDE_CONFIG_DIR", "")
	t.Setenv("CODEX_HOME", "")
	t.Setenv("GEMINI_CLI_HOME", "")
	t.Setenv("GOOGLE_GEMINI_BASE_URL", "")
	t.Setenv("OPENAI_BASE_URL", "")

	simdi := time.Now().UTC().Truncate(time.Second)
	brainKok := filepath.Join(ev, ".gemini", "antigravity", "brain")

	// Normal durumda transcript.jsonl içermeyen eski/boş brain klasörü yanlış alarm üretmemeli
	_ = os.MkdirAll(filepath.Join(brainKok, "bos-konusma"), 0o755)

	logDir := filepath.Join(brainKok, "aktif-konusma", ".system_generated", "logs")
	_ = os.MkdirAll(logDir, 0o755)
	trYol := filepath.Join(logDir, "transcript.jsonl")
	satir1 := `{"step_index":1,"type":"PLANNER_RESPONSE","created_at":"` + simdi.Add(-20*time.Minute).Format(time.RFC3339) + `","input_tokens":20000,"cache_read_tokens":15000,"output_tokens":2000}` + "\n"
	satir2 := `{"step_index":2,"type":"PLANNER_RESPONSE","created_at":"` + simdi.Add(-10*time.Minute).Format(time.RFC3339) + `","input_tokens":10000,"cache_read_tokens":5000,"output_tokens":1000}` + "\n"
	_ = os.WriteFile(trYol, []byte(satir1+satir2), 0o644)

	ilk := toplaDiger(simdi.Unix())
	if len(ilk.bulgular) != 0 {
		t.Fatalf("normal kullanımda şüpheli bulgu olmamalı: %+v", ilk.bulgular)
	}

	// 1. transcript.jsonl içindeki token sayısı kırpılır / azaltılırsa (boyut boşlukla büyütülse bile!) yakalanmalı
	sahteIcerik := `{"step_index":1,"type":"PLANNER_RESPONSE","created_at":"` + simdi.Add(-20*time.Minute).Format(time.RFC3339) + `","input_tokens":100,"cache_read_tokens":0,"output_tokens":10}` + strings.Repeat(" ", 400) + "\n"
	_ = os.WriteFile(trYol, []byte(sahteIcerik), 0o644)
	yeniZaman := simdi.Add(-5 * time.Minute)
	_ = os.Chtimes(trYol, yeniZaman, yeniZaman)

	ikinci := toplaDiger(simdi.Unix() + 10)
	bulguVar := func(liste []Bulgu, kod string) bool {
		for _, b := range liste {
			if b.K == kod && b.S == "s" {
				return true
			}
		}
		return false
	}
	if !bulguVar(ikinci.bulgular, "kuculen_gemini") {
		t.Fatalf("azaltılmış transcript.jsonl için kuculen_gemini bekleniyordu: %+v", ikinci.bulgular)
	}

	// 2. Orijinal yüksek tokenlı dosya geri yazılıp önbelleğe alındıktan sonra diskten tamamen silinirse silinen_gemini yakalanmalı
	_ = os.WriteFile(trYol, []byte(satir1+satir2), 0o644)
	_ = os.Chtimes(trYol, simdi, simdi)
	_ = toplaDiger(simdi.Unix() + 20)
	_ = os.Remove(trYol)
	ucuncu := toplaDiger(simdi.Unix() + 30)
	if !bulguVar(ucuncu.bulgular, "silinen_gemini") {
		t.Fatalf("silinen transcript.jsonl için silinen_gemini bekleniyordu: %+v", ucuncu.bulgular)
	}

	// 3. transcript.jsonl /dev/null'a yönlendirilirse engel_gemini + sahte proxy ortamında api_ortam_chatgpt yakalanmalı
	if err := os.Symlink(os.DevNull, trYol); err == nil {
		t.Setenv("OPENAI_BASE_URL", "https://sahte-proxy.ornek.com/v1")
		_, birlesik, errG := govdeHazirla(simdi.Unix() + 40)
		if errG != nil {
			t.Fatal(errG)
		}
		if !bulguVar(birlesik.bulgular, "engel_gemini") {
			t.Fatalf("/dev/null yönlendirmesi için engel_gemini bekleniyordu: %+v", birlesik.bulgular)
		}
		if !bulguVar(birlesik.bulgular, "api_ortam_chatgpt") {
			t.Fatalf("sahte OPENAI_BASE_URL için api_ortam_chatgpt bekleniyordu: %+v", birlesik.bulgular)
		}
	}
}

func TestHaftalikSifirlamaDiskOkuma(t *testing.T) {
	// 1) Claude plan-usage-history.json: Cumartesi 09:00 (+03:00 -> 06:00 UTC) sıfırlama düşüşü (100 -> 0)
	// 2026-11-07 05:13:02 UTC (1794028382) -> 2026-11-07 06:00:00.396 UTC (1794031200)
	simdi := int64(1794400000) // 2026-11-11
	var gecmis masaustuGecmis
	if err := json.Unmarshal([]byte(`{
		"version": 1,
		"samples": [
			{"t": 1794028382000, "org": "org-aktif", "u": {"sd": 100}},
			{"t": 1794031200396, "org": "org-aktif", "u": {"sd": 0}},
			{"t": 1794390000000, "org": "org-aktif", "u": {"sd": 42}}
		]
	}`), &gecmis); err != nil {
		t.Fatal(err)
	}
	claudeReset, ok := claudeGecmisSifirlamaBul(gecmis, "org-aktif", simdi)
	if !ok || claudeReset != 1794636000 {
		t.Fatalf("Claude haftalık sıfırlama hatalı: ok=%v reset=%d (beklenen 1794636000)", ok, claudeReset)
	}

	// Çok geniş aralıklı (ör. 3 gün) düşüş varsa tahmin yürütülmemeli (false dönmeli)
	var genisGecmis masaustuGecmis
	_ = json.Unmarshal([]byte(`{
		"version": 1,
		"samples": [
			{"t": 1793772000000, "org": "org-aktif", "u": {"sd": 80}},
			{"t": 1794031200000, "org": "org-aktif", "u": {"sd": 5}}
		]
	}`), &genisGecmis)
	if r, okGenis := claudeGecmisSifirlamaBul(genisGecmis, "org-aktif", simdi); okGenis {
		t.Fatalf("geniş aralıklı düşüşte tahmin yapılmamalıydı, dönen: %d", r)
	}

	// 2) ChatGPT / Codex: 10080 dk (7 gün) seven_day olurken, 43200 dk (30 gün free tier) thirty_day olmalı
	yuzde7 := 34.5
	yuzde30 := 12.0
	rm7 := map[string]string{}
	codexPencereEkle(rm7, &codexPencere{UsedPercent: &yuzde7, WindowMinutes: 10080, ResetsAt: simdi + 86400}, false)
	if rm7["seven_day.resets_at"] != strconv.FormatInt(simdi+86400, 10) {
		t.Fatalf("10080 dk seven_day.resets_at olmalıydı: %+v", rm7)
	}
	rm30 := map[string]string{}
	codexPencereEkle(rm30, &codexPencere{UsedPercent: &yuzde30, WindowMinutes: 43200, ResetsAt: simdi + 20*86400}, true)
	if _, var7 := rm30["seven_day.resets_at"]; var7 {
		t.Fatalf("43200 dk (30 gün) seven_day.resets_at alanına yazılmamalı: %+v", rm30)
	}
	if rm30["thirty_day.resets_at"] != strconv.FormatInt(simdi+20*86400, 10) {
		t.Fatalf("43200 dk thirty_day.resets_at olmalıydı: %+v", rm30)
	}

	// 3) Gemini / Antigravity: süresi dün dolmuş 7 günlük kota kullanım yüzdesi üretmemeli ama bir sonraki haftalık sıfırlamayı (+7 gün) vermeli
	gecmisReset := simdi - 86400 // dün sıfırlanmış
	mtSn := gecmisReset - 6*86400
	disB64 := sahteUserStatusDisB64("deniz@firma.com", 64, []struct {
		etiket  string
		kalan   float32
		resetSn int64
	}{
		{etiket: "Claude Sonnet 4.6", kalan: 0.25, resetSn: gecmisReset},
	})
	eposta, limR, limVar, haftaReset := agyUserStatusCoz(disB64, mtSn, simdi)
	if eposta != "deniz@firma.com" {
		t.Fatalf("eposta hatalı: %q", eposta)
	}
	if limVar || len(limR) != 0 {
		t.Fatalf("süresi dolmuş pencereden aktif limit yüzdesi çıkmamalıydı: %+v", limR)
	}
	if haftaReset != gecmisReset+7*86400 {
		t.Fatalf("sonraki haftalık sıfırlama %d bekleniyordu, gelen %d", gecmisReset+7*86400, haftaReset)
	}
}

func TestCodexArsivReasoningVeGeminiJSONL(t *testing.T) {
	eskiDizin := dizin
	dizin = t.TempDir()
	defer func() { dizin = eskiDizin }()

	ev := t.TempDir()
	t.Setenv("HOME", ev)
	t.Setenv("USERPROFILE", ev)
	t.Setenv("CLAUDE_CONFIG_DIR", "")
	t.Setenv("CODEX_HOME", "")
	t.Setenv("GEMINI_CLI_HOME", "")
	t.Setenv("OPENAI_API_KEY", "")
	t.Setenv("GEMINI_API_KEY", "")
	t.Setenv("GOOGLE_API_KEY", "")

	simdi := time.Now().UTC().Truncate(time.Second)
	t1 := simdi.Add(-15 * time.Minute).Format(time.RFC3339)
	t2 := simdi.Add(-10 * time.Minute).Format(time.RFC3339)

	// 1. Codex aktif oturumu: reasoning_output_tokens ayrı verildiğinde (Total >= In+Out+Reasoning) çıktıya eklenmeli,
	// total_token_usage olmadan aynı last_token_usage.total_tokens gelen iki farklı tur atlanmamalı
	aktifDir := filepath.Join(ev, ".codex", "sessions", "2026", "11", "11")
	arsivDir := filepath.Join(ev, ".codex", "archived_sessions")
	_ = os.MkdirAll(aktifDir, 0o755)
	_ = os.MkdirAll(arsivDir, 0o755)

	aktifYol := filepath.Join(aktifDir, "rollout-arsiv-test.jsonl")
	icerik := `{"timestamp":"` + t1 + `","type":"session_meta","payload":{"id":"codex-arsiv-1","creator_account_id":"acct-1"}}` + "\n" +
		`{"timestamp":"` + t1 + `","type":"turn_context","payload":{"model":"gpt-5.2-codex"}}` + "\n" +
		`{"timestamp":"` + t1 + `","type":"event_msg","payload":{"type":"token_count","info":{"last_token_usage":{"input_tokens":1000,"cached_input_tokens":600,"output_tokens":100,"reasoning_output_tokens":200,"total_tokens":1300}}}}` + "\n" +
		`{"timestamp":"` + t2 + `","type":"event_msg","payload":{"type":"token_count","info":{"last_token_usage":{"input_tokens":1000,"cached_input_tokens":600,"output_tokens":300,"reasoning_output_tokens":200,"total_tokens":1300}}}}` + "\n"
	_ = os.WriteFile(aktifYol, []byte(icerik), 0o600)

	// 2. Gemini CLI .jsonl oturumu
	gemDir := filepath.Join(ev, ".gemini", "tmp", "proje-jsonl", "chats")
	_ = os.MkdirAll(gemDir, 0o755)
	gemJSONL := `{"sessionId":"gem-jsonl-1","type":"user","timestamp":"` + t1 + `"}` + "\n" +
		`{"sessionId":"gem-jsonl-1","type":"gemini","timestamp":"` + t2 + `","model":"gemini-2.5-flash","tokens":{"input":500,"cached":200,"output":90,"thoughts":30}}` + "\n"
	_ = os.WriteFile(filepath.Join(gemDir, "session-2026.jsonl"), []byte(gemJSONL), 0o600)

	ilk := toplaDiger(simdi.Unix())
	byModel := map[string]Satir{}
	for _, r := range ilk.satirlar {
		byModel[r.Model] = r
	}
	// İlk tur: Out=100+200=300 (Total 1300 == 1000+100+200); İkinci tur: Out=300 (Total 1300 < 1000+300+200, reasoning dahil) -> toplam Out=600, Messages=2
	cx := byModel["gpt-5.2-codex"]
	if cx.Messages != 2 || cx.Input != 800 || cx.Cr != 1200 || cx.Output != 600 {
		t.Fatalf("codex reasoning/dedup hatalı: %+v", cx)
	}
	gm := byModel["gemini-2.5-flash"]
	if gm.Messages != 1 || gm.Input != 300 || gm.Cr != 200 || gm.Output != 120 {
		t.Fatalf("gemini jsonl okuma hatalı: %+v", gm)
	}

	// 3. Oturum archived_sessions klasörüne taşındığında kullanım korunmalı ve silinen_chatgpt alarmı üretilmemeli!
	arsivYol := filepath.Join(arsivDir, "rollout-arsiv-test.jsonl")
	if err := os.Rename(aktifYol, arsivYol); err != nil {
		t.Fatal(err)
	}
	ikinci := toplaDiger(simdi.Unix() + 5)
	for _, b := range ikinci.bulgular {
		if b.K == "silinen_chatgpt" {
			t.Fatalf("arşivlenen Codex oturumu silinen_chatgpt üretmemeli: %+v", ikinci.bulgular)
		}
	}
	var arsivSonrasiMesaj int64
	for _, r := range ikinci.satirlar {
		if r.Model == "gpt-5.2-codex" {
			arsivSonrasiMesaj += r.Messages
		}
	}
	if arsivSonrasiMesaj != 2 {
		t.Fatalf("arşivlenen Codex oturumu sayılmaya devam etmeliydi, gelen=%d", arsivSonrasiMesaj)
	}
}
