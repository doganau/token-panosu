//go:build sag_chatgpt || (!sag_claude && !sag_gemini)

package main

import (
	"encoding/base64"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestChatGPTIzoleToplamaVeArsiv(t *testing.T) {
	eskiDizin, eskiTestModu := dizin, testModu
	dizin = t.TempDir()
	testModu = false
	defer func() {
		dizin = eskiDizin
		testModu = eskiTestModu
	}()

	ev := t.TempDir()
	t.Setenv("HOME", ev)
	t.Setenv("USERPROFILE", ev)
	t.Setenv("CODEX_HOME", "")
	t.Setenv("OPENAI_API_KEY", "")
	t.Setenv("OPENAI_BASE_URL", "")

	if m := codexModelAd("custom-internal"); m != "openai-custom-internal" {
		t.Fatalf("codexModelAd beklenmeyen çıktı: %q", m)
	}

	_ = os.MkdirAll(filepath.Join(ev, ".codex"), 0o700)
	jwtHdr := base64.RawURLEncoding.EncodeToString([]byte(`{"alg":"RS256"}`))
	jwtPay := base64.RawURLEncoding.EncodeToString([]byte(`{"email":"Test.Uye@Ornek.com","https://api.openai.com/auth":{"chatgpt_account_id":"acct-izole-1"}}`))
	authIcerik := `{"tokens":{"account_id":"acct-izole-1","id_token":"` + jwtHdr + `.` + jwtPay + `.imza"}}`
	_ = os.WriteFile(filepath.Join(ev, ".codex", "auth.json"), []byte(authIcerik), 0o600)

	simdi := time.Now().UTC().Truncate(time.Second)
	t1 := simdi.Add(-10 * time.Minute).Format(time.RFC3339)
	aktifDir := filepath.Join(ev, ".codex", "sessions", "2026", "11", "12")
	arsivDir := filepath.Join(ev, ".codex", "archived_sessions")
	_ = os.MkdirAll(aktifDir, 0o755)
	_ = os.MkdirAll(arsivDir, 0o755)

	oturumYol := filepath.Join(aktifDir, "rollout-1.jsonl")
	icerik := `{"timestamp":"` + t1 + `","type":"session_meta","payload":{"id":"cx-1","creator_account_id":"acct-izole-1"}}` + "\n" +
		`{"timestamp":"` + t1 + `","type":"turn_context","payload":{"model":"gpt-5.4"}}` + "\n" +
		`{"timestamp":"` + t1 + `","type":"event_msg","payload":{"type":"token_count","info":{"total_token_usage":{"total_tokens":300000},"last_token_usage":{"input_tokens":280000,"cached_input_tokens":200000,"output_tokens":20000,"total_tokens":300000}}}}` + "\n"
	_ = os.WriteFile(oturumYol, []byte(icerik), 0o600)

	esikleriKaydet(map[string]int64{"gpt-5.4": 272000})
	ilk := toplaDiger(simdi.Unix())
	if ilk.epostalar["chatgpt"] != "test.uye@ornek.com" || ilk.hesaplar["chatgpt"] != hesapOzeti("chatgpt", "acct-izole-1") {
		t.Fatalf("chatgpt hesap/eposta hatalı: %+v / %+v", ilk.hesaplar, ilk.epostalar)
	}
	byModel := map[string]Satir{}
	for _, r := range ilk.satirlar {
		byModel[r.Model] = r
	}
	if byModel["gpt-5.4@uzun"].Input != 80000 || byModel["gpt-5.4@uzun"].Cr != 200000 || byModel["gpt-5.4@uzun"].Output != 20000 {
		t.Fatalf("gpt-5.4@uzun satırı hatalı: %+v", byModel)
	}

	// Arşive taşıma silinen_chatgpt üretmemeli
	if err := os.Rename(oturumYol, filepath.Join(arsivDir, "rollout-1.jsonl")); err != nil {
		t.Fatal(err)
	}
	ikinci := toplaDiger(simdi.Unix() + 5)
	for _, b := range ikinci.bulgular {
		if b.K == "silinen_chatgpt" {
			t.Fatalf("arşivlenen oturum silinen_chatgpt üretmemeli: %+v", ikinci.bulgular)
		}
	}
}
