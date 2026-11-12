package main

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// sahte kayıt satırı: model claude-*, her mesaj 10 giriş + 5 çıkış
func sahteSatir(id string, ts time.Time) string {
	return fmt.Sprintf(`{"timestamp":%q,"requestId":"req_%s","message":{"id":"msg_%s","model":"claude-test-1","usage":{"input_tokens":10,"output_tokens":5}}}`+"\n",
		ts.UTC().Format("2006-01-02T15:04:05.000Z"), id, id)
}

func sahteDosya(t *testing.T, yol string, mt time.Time, ids ...string) {
	t.Helper()
	var s string
	for i, id := range ids {
		s += sahteSatir(id, mt.Add(time.Duration(i-len(ids))*time.Minute))
	}
	if err := os.MkdirAll(filepath.Dir(yol), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(yol, []byte(s), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Chtimes(yol, mt, mt); err != nil {
		t.Fatal(err)
	}
}

func sidler(s sonuc) (map[string]int64, int64) {
	m, toplam := map[string]int64{}, int64(0)
	for _, r := range s.satirlar {
		m[r.Sid] += r.Messages
		toplam += r.Messages
	}
	return m, toplam
}

func TestEtiketDevralma(t *testing.T) {
	ev := t.TempDir()
	t.Setenv("HOME", ev)
	t.Setenv("USERPROFILE", ev)
	t.Setenv("CLAUDE_CONFIG_DIR", "")
	dizin = t.TempDir()
	kok := filepath.Join(ev, ".claude", "projects", "proje")
	simdi := time.Now().Unix()
	an := time.Unix(simdi, 0)

	a := filepath.Join(kok, "aaaa-a.jsonl")
	b := filepath.Join(kok, "bbbb-b.jsonl")
	c := filepath.Join(kok, "cccc-c.jsonl")
	d := filepath.Join(kok, "dddd-d.jsonl")
	sahteDosya(t, a, an.Add(-40*time.Minute), "a1", "a2", "a3")
	sahteDosya(t, b, an.Add(-30*time.Minute), "a2", "a3", "b1", "b2")
	sahteDosya(t, c, an.Add(-20*time.Minute), "b2", "c1")
	sahteDosya(t, d, an.Add(-10*time.Minute), "d1")

	sidA, sidB := oturumSid(a, filepath.Dir(kok)), oturumSid(b, filepath.Dir(kok))
	sidC, sidD := oturumSid(c, filepath.Dir(kok)), oturumSid(d, filepath.Dir(kok))

	s := topla(simdi)
	m, toplam := sidler(s)
	if toplam != 7 { // a1 a2 a3 b1 b2 c1 d1
		t.Fatalf("toplam mesaj %d, beklenen 7", toplam)
	}
	if m[sidA] != 6 || m[sidD] != 1 || m[sidB] != 0 || m[sidC] != 0 || len(m) != 2 {
		t.Fatalf("beklenmeyen sid dağılımı: A=%d B=%d C=%d D=%d toplam sid=%d", m[sidA], m[sidB], m[sidC], m[sidD], len(m))
	}
	// dv: yalnızca B ve C, kökü A; D ve A yok
	if len(s.devir) != 2 || s.devir[sidB] != sidA || s.devir[sidC] != sidA {
		t.Fatalf("devir eşlemesi beklenmedik: %d çift", len(s.devir))
	}
	govde, _, err := govdeHazirla(simdi)
	if err != nil || !strings.Contains(string(govde), `"dv":{`) {
		t.Fatalf("gövdede dv yok: %v", err)
	}
	for _, dl := range s.dilimler {
		if dl.Sid == sidB || dl.Sid == sidC {
			t.Fatalf("5 dk diliminde devredilmemiş sid var")
		}
	}

	// Asıl dosya silinir (önbellek durur): devam dosyaları yine A'nın sid'ini taşımalı
	if err := os.Remove(a); err != nil {
		t.Fatal(err)
	}
	s = topla(simdi + 60)
	m, _ = sidler(s)
	if m[sidB] != 0 || m[sidC] != 0 || m[sidA] == 0 || m[sidD] != 1 {
		t.Fatalf("A silindikten sonra: A=%d B=%d C=%d D=%d", m[sidA], m[sidB], m[sidC], m[sidD])
	}
	// a1 kayboldu; a2 a3 artık B'deki kopyadan sayılır: b1 b2 c1 a2 a3 = 5
	if m[sidA] != 5 {
		t.Fatalf("A kökü altında %d mesaj, beklenen 5", m[sidA])
	}
}

func TestEskiOnbellekYuklenir(t *testing.T) {
	dizin = t.TempDir()
	o := onbellekYukle()
	if o.Devir == nil || o.Dosyalar == nil {
		t.Fatal("boş haritalar bekleniyordu")
	}
	o.Devir = nil // eski biçim: Devir alanı yok
	onbellekKaydet(o)
	if y := onbellekYukle(); y.Devir == nil {
		t.Fatal("Devir nil kaldı")
	}
}

func TestDevirSiniri(t *testing.T) {
	m := map[string]string{}
	for i := 0; i < 300; i++ {
		m[fmt.Sprintf("%016x", i)] = "ffffffffffffffff"
	}
	m["ffffffffffffffff"] = "ffffffffffffffff"
	if d := devirCifti(m); len(d) != devirSinir {
		t.Fatalf("%d çift, beklenen %d", len(d), devirSinir)
	} else if _, var_ := d["ffffffffffffffff"]; var_ {
		t.Fatal("kökün kendisi devir çiftinde olmamalı")
	}
	if len(devirCifti(map[string]string{"a": "a"})) != 0 {
		t.Fatal("boş eşleme bekleniyordu")
	}
}

// İstem eşiğini aşan mesaj "@uzun" satırına gider; aynı saatin sade satırı da (sıfır olsa bile) gönderilir.
func TestUzunIstem(t *testing.T) {
	ev := t.TempDir()
	t.Setenv("HOME", ev)
	t.Setenv("USERPROFILE", ev)
	t.Setenv("CLAUDE_CONFIG_DIR", "")
	dizin = t.TempDir()
	kok := filepath.Join(ev, ".claude", "projects", "proje")
	simdi := time.Now().Unix()
	an := time.Unix(simdi, 0).Add(-5 * time.Minute)
	satir := func(id string, giris, okuma int) string {
		return fmt.Sprintf(`{"timestamp":%q,"requestId":"req_%s","message":{"id":"msg_%s","model":"claude-test-1-20250101","usage":{"input_tokens":%d,"output_tokens":5,"cache_read_input_tokens":%d}}}`+"\n",
			an.UTC().Format("2006-01-02T15:04:05.000Z"), id, id, giris, okuma)
	}
	yol := filepath.Join(kok, "uzun-a.jsonl")
	_ = os.MkdirAll(kok, 0o755)
	if err := os.WriteFile(yol, []byte(satir("k1", 10, 0)+satir("u1", 10, 100)+satir("u2", 50, 60)), 0o644); err != nil {
		t.Fatal(err)
	}
	esikleriKaydet(map[string]int64{"claude-test-1": 100})
	s := topla(simdi)
	mesaj := map[string]int64{}
	for _, r := range s.satirlar {
		mesaj[r.Model] += r.Messages
	}
	if mesaj["claude-test-1-20250101"] != 1 || mesaj["claude-test-1-20250101@uzun"] != 2 {
		t.Fatalf("beklenmeyen dağılım: %v", mesaj)
	}
	// Eşik yoksa her şey sade satırda
	esikleriKaydet(map[string]int64{})
	s = topla(simdi + 1)
	for _, r := range s.satirlar {
		if strings.HasSuffix(r.Model, uzunEki) {
			t.Fatalf("eşik yokken @uzun satırı var")
		}
	}
}

func TestCodexGeminiVeHesapOzeti(t *testing.T) {
	ev := t.TempDir()
	t.Setenv("HOME", ev)
	t.Setenv("USERPROFILE", ev)
	t.Setenv("CLAUDE_CONFIG_DIR", "")
	t.Setenv("CODEX_HOME", "")
	t.Setenv("GEMINI_CLI_HOME", "")
	dizin = t.TempDir()

	simdi := time.Now().Unix()
	an := time.Unix(simdi-600, 0).UTC()
	zamanISO := an.Format("2006-01-02T15:04:05.000Z")

	// 1. Claude hesabı: .claude.json ve plan-usage-history.json aynı org UUID'sini taşır -> aynı özet
	orgUUID := "11111111-2222-3333-4444-555555555555"
	_ = os.WriteFile(filepath.Join(ev, ".claude.json"), []byte(fmt.Sprintf(`{"oauthAccount":{"organizationUuid":%q,"accountUuid":"aaaa"}}`, orgUUID)), 0o600)
	hClaude := claudeHesapOku()
	if len(hClaude) != 16 || hClaude != hesapOzeti("claude", orgUUID) {
		t.Fatalf("beklenmeyen claude hesap özeti: %q", hClaude)
	}

	// 2. Codex oturumu: aynı total_tokens iki kez gelirse tek sayılmalı; eski yabancı hesap karışmamalı
	codexKlasor := filepath.Join(ev, ".codex", "sessions", "2026", "11", "10")
	if err := os.MkdirAll(codexKlasor, 0o755); err != nil {
		t.Fatal(err)
	}
	yabanciYol := filepath.Join(codexKlasor, "rollout-eski.jsonl")
	yabanciIcerik := fmt.Sprintf(
		`{"timestamp":%q,"type":"session_meta","payload":{"id":"yabanci-1","creator_account_id":"baska-hesap-999"}}`+"\n"+
			`{"timestamp":%q,"type":"turn_context","payload":{"model":"gpt-5"}}`+"\n"+
			`{"timestamp":%q,"type":"event_msg","payload":{"type":"token_count","info":{"total_token_usage":{"total_tokens":500},"last_token_usage":{"input_tokens":400,"cached_input_tokens":0,"output_tokens":100,"total_tokens":500}}}}`+"\n",
		zamanISO, zamanISO, zamanISO,
	)
	_ = os.WriteFile(yabanciYol, []byte(yabanciIcerik), 0o600)
	eskiMt := time.Unix(simdi-1800, 0)
	_ = os.Chtimes(yabanciYol, eskiMt, eskiMt)

	aktifYol := filepath.Join(codexKlasor, "rollout-aktif.jsonl")
	aktifIcerik := fmt.Sprintf(
		`{"timestamp":%q,"type":"session_meta","payload":{"id":"oturum-aktif","creator_account_id":"ortak-hesap-123"}}`+"\n"+
			`{"timestamp":%q,"type":"turn_context","payload":{"model":"gpt-6-luna"}}`+"\n"+
			`{"timestamp":%q,"type":"event_msg","payload":{"type":"token_count","info":{"total_token_usage":{"total_tokens":16383},"last_token_usage":{"input_tokens":16351,"cached_input_tokens":15104,"cache_write_input_tokens":0,"output_tokens":32,"total_tokens":16383}},"rate_limits":{"primary":{"used_percent":14.5,"window_minutes":10080,"resets_at":%d},"secondary":{"used_percent":3,"window_minutes":300,"resets_at":%d}}}}`+"\n"+
			`{"timestamp":%q,"type":"event_msg","payload":{"type":"token_count","info":{"total_token_usage":{"total_tokens":16383},"last_token_usage":{"input_tokens":16351,"cached_input_tokens":15104,"cache_write_input_tokens":0,"output_tokens":32,"total_tokens":16383}}}}`+"\n",
		zamanISO, zamanISO, zamanISO, simdi+86400, simdi+3600, zamanISO,
	)
	_ = os.WriteFile(aktifYol, []byte(aktifIcerik), 0o600)
	yeniMt := time.Unix(simdi-300, 0)
	_ = os.Chtimes(aktifYol, yeniMt, yeniMt)

	// 3. Gemini CLI oturumu + google_accounts.json
	gemKlasor := filepath.Join(ev, ".gemini", "tmp", "proje1", "chats")
	if err := os.MkdirAll(gemKlasor, 0o755); err != nil {
		t.Fatal(err)
	}
	_ = os.WriteFile(filepath.Join(ev, ".gemini", "google_accounts.json"), []byte(`{"active":"ekip@sirket.com"}`), 0o600)
	gemYol := filepath.Join(gemKlasor, "session-abc.json")
	gemIcerik := fmt.Sprintf(`{"sessionId":"gem-1","messages":[{"type":"user","timestamp":%q},{"type":"gemini","timestamp":%q,"model":"gemini-2.5-pro","tokens":{"input":1000,"cached":600,"output":120,"thoughts":80}}]}`, zamanISO, zamanISO)
	_ = os.WriteFile(gemYol, []byte(gemIcerik), 0o600)

	govde, _, err := govdeHazirla(simdi)
	if err != nil {
		t.Fatalf("govdeHazirla hatası: %v", err)
	}
	var cozulmus struct {
		Rows  []Satir                 `json:"rows"`
		Hesap map[string]string       `json:"hesap"`
		Lp    map[string]limitDosyasi `json:"lp"`
	}
	if err := json.Unmarshal(govde, &cozulmus); err != nil {
		t.Fatal(err)
	}

	if cozulmus.Hesap["claude"] != hesapOzeti("claude", orgUUID) {
		t.Fatalf("claude hesabı eşleşmedi: %v", cozulmus.Hesap)
	}
	if cozulmus.Hesap["chatgpt"] != hesapOzeti("chatgpt", "ortak-hesap-123") {
		t.Fatalf("chatgpt hesabı eşleşmedi: %v", cozulmus.Hesap)
	}
	if cozulmus.Hesap["gemini"] != hesapOzeti("gemini", "ekip@sirket.com") {
		t.Fatalf("gemini hesabı eşleşmedi: %v", cozulmus.Hesap)
	}

	if len(cozulmus.Rows) != 2 {
		t.Fatalf("2 satır (aktif codex + gemini) bekleniyordu, gelen: %+v", cozulmus.Rows)
	}
	byModel := map[string]Satir{}
	for _, r := range cozulmus.Rows {
		byModel[r.Model] = r
	}
	cx := byModel["gpt-6-luna"]
	if cx.Input != 1247 || cx.Cr != 15104 || cx.Output != 32 || cx.Messages != 1 {
		t.Fatalf("codex satırı yanlış: %+v", cx)
	}
	gm := byModel["gemini-2.5-pro"]
	if gm.Input != 400 || gm.Cr != 600 || gm.Output != 200 || gm.Messages != 1 {
		t.Fatalf("gemini satırı yanlış: %+v", gm)
	}

	lpC, ok := cozulmus.Lp["chatgpt"]
	if !ok || lpC.R["seven_day.used_percentage"].String() != "14.5" || lpC.R["five_hour.used_percentage"].String() != "3" {
		t.Fatalf("chatgpt limit bilgisi eksik veya hatalı: %+v", cozulmus.Lp)
	}
}

func TestClaudeHaftaSifirlamaAraDusus(t *testing.T) {
	// 1) Cumartesi 09:00 TR (1794031200) gerçek sıfırlama (%100 -> %0), ardından Perşembe 03:40 TR (%91 -> %44) ara havuz düşüşü
	var g masaustuGecmis
	if err := json.Unmarshal([]byte(`{"version":1,"samples":[
		{"t":1794029580000,"org":"org1","u":{"sd":100}},
		{"t":1794031200000,"org":"org1","u":{"sd":0}},
		{"t":1794443847000,"org":"org1","u":{"sd":91}},
		{"t":1794444147000,"org":"org1","u":{"sd":44}},
		{"t":1794445947000,"org":"org1","u":{"sd":51}}
	]}`), &g); err != nil {
		t.Fatal(err)
	}
	r, ok := claudeGecmisSifirlamaBul(g, "org1", 1794446000)
	if !ok || r != 1794636000 {
		t.Fatalf("ara düşüş haftalık sıfırlamayı bozdu: ok=%v r=%d (beklenen 1794636000)", ok, r)
	}

	// 2) İki hafta üst üste bilgisayar kapalıyken kesişen boşluklarla (%58 -> %16 ve bir sonraki hafta dar aralık) gerçek sıfırlama bulunmalı
	var gKapali masaustuGecmis
	if err := json.Unmarshal([]byte(fmt.Sprintf(`{"version":1,"samples":[
		{"t":%d,"org":"org1","u":{"sd":58}},
		{"t":%d,"org":"org1","u":{"sd":16}},
		{"t":%d,"org":"org1","u":{"sd":95}},
		{"t":%d,"org":"org1","u":{"sd":4}}
	]}`, (1793426400-32*3600)*1000, (1793426400+5*3600)*1000, (1794031200-2*3600)*1000, (1794031200+2*3600)*1000)), &gKapali); err != nil {
		t.Fatal(err)
	}
	if r2, ok2 := claudeGecmisSifirlamaBul(gKapali, "org1", 1794446000); !ok2 || r2 < 1794636000-2*3600 || r2 > 1794636000+2*3600 {
		t.Fatalf("uzun boşluklu haftalık sıfırlama bulunamadı: ok=%v r=%d", ok2, r2)
	}
}
