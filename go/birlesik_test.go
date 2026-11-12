//go:build (sag_claude && sag_chatgpt && sag_gemini) || (!sag_claude && !sag_chatgpt && !sag_gemini)

package main

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"
)

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

	_ = os.WriteFile(trYol, []byte(satir1+satir2), 0o644)
	_ = os.Chtimes(trYol, simdi, simdi)
	_ = toplaDiger(simdi.Unix() + 20)
	_ = os.Remove(trYol)
	ucuncu := toplaDiger(simdi.Unix() + 30)
	if !bulguVar(ucuncu.bulgular, "silinen_gemini") {
		t.Fatalf("silinen transcript.jsonl için silinen_gemini bekleniyordu: %+v", ucuncu.bulgular)
	}

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

	gecmisReset := simdi - 86400
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
	cx := byModel["gpt-5.2-codex"]
	if cx.Messages != 2 || cx.Input != 800 || cx.Cr != 1200 || cx.Output != 600 {
		t.Fatalf("codex reasoning/dedup hatalı: %+v", cx)
	}
	gm := byModel["gemini-2.5-flash"]
	if gm.Messages != 1 || gm.Input != 300 || gm.Cr != 200 || gm.Output != 120 {
		t.Fatalf("gemini jsonl okuma hatalı: %+v", gm)
	}

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
