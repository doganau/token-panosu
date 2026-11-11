package main

// Ek sağlayıcı toplayıcıları (ChatGPT/Codex, Gemini CLI ve Antigravity) ve yerel hesap parmak izi.
//
// Güvenlik ve gizlilik ilkeleri:
//   - Yalnızca bu bilgisayardaki düz metin oturum/yapılandırma dosyaları pasif ve salt okunur biçimde okunur; hiçbir dosyaya dokunulmaz.
//   - Hiçbir sağlayıcıya (Anthropic, OpenAI, Google) ağ isteği ATILMAZ.
//   - Hiçbir API anahtarı, OAuth erişim/yenileme belirteci (access_token, refresh_token, oauth_creds.json vb.) veya Anahtar Zinciri OKUNMAZ ve GÖNDERİLMEZ.
//   - Ortak hesap kilidi için hesap kimliğinin 16 haneli SHA-256 özeti ile yerel dosyalardaki hesap e-posta adresi birlikte iletilir.
//   - Herhangi bir dosya yoksa ya da okunamazsa sessizce atlanır; gönderim hiçbir zaman aksamaz.

import (
	"bufio"
	"bytes"
	"crypto/sha256"
	"encoding/base64"
	"encoding/gob"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"
)

const digerOnbellekBicimi = 1

func digerOnbellekYolu() string { return filepath.Join(dizin, "onbellek_diger.bin") }

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

// hesapOzeti: sağlayıcıya ait benzersiz kimliği geri döndürülemez 16 haneli SHA-256 özetine çevirir.
func hesapOzeti(saglayici, ham string) string {
	s := strings.ToLower(strings.TrimSpace(ham))
	if len(s) < 3 {
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

func codexModelAd(ham string) string {
	m := modelTemizle(ham)
	if m == "" {
		return "gpt-5-codex"
	}
	if strings.HasPrefix(m, "claude-") || strings.HasPrefix(m, "gemini-") {
		m = "openai-" + m
	} else if !strings.HasPrefix(m, "gpt-") &&
		!strings.HasPrefix(m, "o1") &&
		!strings.HasPrefix(m, "o3") &&
		!strings.HasPrefix(m, "o4") &&
		!strings.HasPrefix(m, "codex-") &&
		!strings.HasPrefix(m, "chatgpt-") &&
		!strings.HasPrefix(m, "openai-") {
		m = "openai-" + m
	}
	if len(m) > 60 {
		m = m[:60]
	}
	return m
}

func geminiModelAd(ham string) string {
	m := modelTemizle(ham)
	if m == "" {
		return "gemini-2.5-pro"
	}
	if !strings.HasPrefix(m, "gemini-") {
		m = "gemini-" + m
	}
	if len(m) > 60 {
		m = m[:60]
	}
	return m
}

// ---------- yerel hesap kimlikleri ve e-posta adresleri (yalnızca yerel dosyalardan salt okunur) ----------

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

// claudeHesapBilgisiOku: ortak aboneliklerde tüm ekip üyelerinde aynı olan organizationUuid özetini ve
// .claude.json içindeki oauthAccount.emailAddress değerini yerel dosyadan salt okunur biçimde okur.
func claudeHesapBilgisiOku() (string, string) {
	ev := evDizini()
	adaylar := []string{}
	if testModu {
		adaylar = append(adaylar, filepath.Join(dizin, "claude.json"))
	}
	if cfg := os.Getenv("CLAUDE_CONFIG_DIR"); cfg != "" {
		adaylar = append(adaylar, filepath.Join(genislet(cfg), ".claude.json"))
	}
	adaylar = append(adaylar,
		filepath.Join(ev, ".claude.json"),
		filepath.Join(ev, ".config", "claude", ".claude.json"),
		filepath.Join(ev, ".claude", ".claude.json"),
	)

	var yedekHesap, yedekEposta string
	for _, yol := range adaylar {
		b, err := os.ReadFile(yol)
		if err != nil {
			continue
		}
		var yapi struct {
			OAuth *struct {
				OrgUUID      string `json:"organizationUuid"`
				AccountUUID  string `json:"accountUuid"`
				EmailAddress string `json:"emailAddress"`
			} `json:"oauthAccount"`
		}
		if json.Unmarshal(b, &yapi) != nil || yapi.OAuth == nil {
			continue
		}
		ep := epostaTemizle(yapi.OAuth.EmailAddress)
		if yedekEposta == "" && ep != "" {
			yedekEposta = ep
		}
		if strings.TrimSpace(yapi.OAuth.OrgUUID) != "" {
			if ep == "" {
				ep = yedekEposta
			}
			return hesapOzeti("claude", yapi.OAuth.OrgUUID), ep
		}
		if yedekHesap == "" && strings.TrimSpace(yapi.OAuth.AccountUUID) != "" {
			yedekHesap = yapi.OAuth.AccountUUID
		}
	}

	// Masaüstü uygulamasının kendi yazdığı plan-usage-history.json içindeki "org" alanı da aynı organizationUuid'dir.
	if yol := claudeMasaustuLimitYolu(); yol != "" {
		if b, err := os.ReadFile(yol); err == nil {
			var g masaustuGecmis
			if json.Unmarshal(b, &g) == nil && len(g.Samples) > 0 {
				if org := strings.TrimSpace(g.Samples[len(g.Samples)-1].Org); org != "" {
					return hesapOzeti("claude", org), yedekEposta
				}
			}
		}
	}

	if yedekHesap != "" {
		return hesapOzeti("claude", yedekHesap), yedekEposta
	}
	return "", yedekEposta
}

func claudeHesapOku() string {
	h, _ := claudeHesapBilgisiOku()
	return h
}

// codexAuthBilgisiOku: ~/.codex/auth.json dosyasından yalnızca hesap kimliğini (account_id) ve OpenID Connect
// id_token gövdesindeki düz e-posta bilgisini bellekte çözer; access_token, refresh_token veya API anahtarlarını
// kesinlikle okumaz, saklamaz ve dışarı çıkarmaz.
func codexAuthBilgisiOku() (string, string) {
	ev := evDizini()
	adaylar := []string{}
	if testModu {
		adaylar = append(adaylar, filepath.Join(dizin, "codex_auth.json"))
	}
	if ch := os.Getenv("CODEX_HOME"); ch != "" {
		adaylar = append(adaylar, filepath.Join(genislet(ch), "auth.json"))
	}
	adaylar = append(adaylar, filepath.Join(ev, ".codex", "auth.json"))

	for _, yol := range adaylar {
		st, err := os.Stat(yol)
		if err != nil || !st.Mode().IsRegular() || st.Size() <= 2 || st.Size() > 1<<20 {
			continue
		}
		b, err := os.ReadFile(yol)
		if err != nil {
			continue
		}
		var yapi struct {
			Tokens *struct {
				AccountID string `json:"account_id"`
				IDToken   string `json:"id_token"`
			} `json:"tokens"`
		}
		if json.Unmarshal(b, &yapi) != nil || yapi.Tokens == nil {
			continue
		}
		hesap := strings.TrimSpace(yapi.Tokens.AccountID)
		var eposta string
		if parcalar := strings.Split(strings.TrimSpace(yapi.Tokens.IDToken), "."); len(parcalar) == 3 && len(parcalar[1]) > 4 {
			cozulmus, err := base64.RawURLEncoding.DecodeString(parcalar[1])
			if err != nil {
				cozulmus, err = base64.URLEncoding.DecodeString(parcalar[1])
			}
			if err != nil {
				cozulmus, err = base64.RawStdEncoding.DecodeString(parcalar[1])
			}
			if err == nil && len(cozulmus) > 2 {
				var idVeri struct {
					Email string `json:"email"`
					Auth  *struct {
						ChatGPTAccountID string `json:"chatgpt_account_id"`
					} `json:"https://api.openai.com/auth"`
				}
				if json.Unmarshal(cozulmus, &idVeri) == nil {
					eposta = epostaTemizle(idVeri.Email)
					if hesap == "" && idVeri.Auth != nil {
						hesap = strings.TrimSpace(idVeri.Auth.ChatGPTAccountID)
					}
				}
			}
		}
		if hesap != "" || eposta != "" {
			return hesap, eposta
		}
	}
	return "", ""
}

// geminiHesapVeLimitOku: ~/.gemini/google_accounts.json (Gemini CLI), Antigravity app_storage.json ve
// Antigravity / Antigravity IDE state.vscdb kaynakları arasından EN YENİ mtime'lı aktif hesabı seçer;
// aktif hesapla eşleşen taze Antigravity kota yüzdeleri varsa onları da döndürür.
// oauth_creds.json, oauthToken ya da Anahtar Zinciri'ne kesinlikle dokunmaz.
func geminiHesapLimitVeResetOku(simdi int64) (string, string, limitDosyasi, bool, int64) {
	ev := evDizini()
	adayYollar := []string{}
	if testModu {
		adayYollar = append(adayYollar, filepath.Join(dizin, "google_accounts.json"))
	}
	if gh := os.Getenv("GEMINI_CLI_HOME"); gh != "" {
		adayYollar = append(adayYollar,
			filepath.Join(genislet(gh), ".gemini", "google_accounts.json"),
			filepath.Join(genislet(gh), "google_accounts.json"),
		)
	}
	adayYollar = append(adayYollar, filepath.Join(ev, ".gemini", "google_accounts.json"))

	var enYeniHesap string
	var enYeniMt int64
	for _, yol := range adayYollar {
		st, err := os.Stat(yol)
		if err != nil || !st.Mode().IsRegular() {
			continue
		}
		b, err := os.ReadFile(yol)
		if err != nil {
			continue
		}
		var g struct {
			Active string `json:"active"`
		}
		if json.Unmarshal(b, &g) == nil {
			ak := strings.TrimSpace(g.Active)
			if ak != "" && st.ModTime().UnixNano() >= enYeniMt {
				enYeniHesap = ak
				enYeniMt = st.ModTime().UnixNano()
			}
		}
	}

	agyAdaylar := agyDurumAdaylariOku(simdi)
	for _, a := range agyAdaylar {
		if a.eposta != "" && a.mtNano >= enYeniMt {
			enYeniHesap = a.eposta
			enYeniMt = a.mtNano
		}
	}

	var enIyiLimitT int64
	var enIyiLimitR map[string]string
	var enIyiResetMt int64
	var enIyiHaftaReset int64
	for _, a := range agyAdaylar {
		if enYeniHesap != "" && a.eposta != "" && !strings.EqualFold(a.eposta, enYeniHesap) {
			continue
		}
		if a.haftaReset > 0 && a.mtNano >= enIyiResetMt {
			enIyiResetMt = a.mtNano
			enIyiHaftaReset = a.haftaReset
		}
		if a.limitT <= 0 || len(a.limitR) == 0 {
			continue
		}
		if a.limitT >= enIyiLimitT {
			enIyiLimitT = a.limitT
			enIyiLimitR = a.limitR
		}
	}

	hesapHex := hesapOzeti("gemini", enYeniHesap)
	temizEposta := epostaTemizle(enYeniHesap)
	if enIyiLimitT > 0 && len(enIyiLimitR) > 0 && enIyiLimitT > sonLimitSaglayiciT("gemini") {
		numHarita := make(map[string]json.Number, len(enIyiLimitR))
		for k, v := range enIyiLimitR {
			numHarita[k] = json.Number(v)
		}
		if sinirli := limitSinirla(numHarita); len(sinirli) > 0 {
			return hesapHex, temizEposta, limitDosyasi{T: enIyiLimitT, R: sinirli}, true, enIyiHaftaReset
		}
	}
	return hesapHex, temizEposta, limitDosyasi{}, false, enIyiHaftaReset
}

func geminiHesapVeLimitOku(simdi int64) (string, string, limitDosyasi, bool) {
	h, ep, lim, ok, _ := geminiHesapLimitVeResetOku(simdi)
	return h, ep, lim, ok
}

func geminiHesapOku() string {
	h, _, _, _ := geminiHesapVeLimitOku(0)
	return h
}

// ---------- ek sağlayıcı önbelleği ----------

type digerKayit struct {
	T     int64
	Model string
	In    int64
	Out   int64
	W5    int64
	Cr    int64
}

type digerDosyaKaydi struct {
	Boyut    int64
	Mtime    int64
	Sid      string
	HesapHam string
	LimitT   int64
	LimitR   map[string]string // json.Number string temsili
	Kayitlar []digerKayit
	Ofset    int64  // Antigravity: okunan son tam satırın sonu (sona eklenen kısım okunur)
	SonModel string // Antigravity: o ana kadar seçili model
	Bas      uint64 // Antigravity: okunan kısmın baş özeti
	Kuyruk   uint64 // Antigravity: okunan kısmın kuyruk özeti
}

type digerOnbellek struct {
	Bicim    int
	Dosyalar map[string]*digerDosyaKaydi
}

func digerOnbellekYukle() *digerOnbellek {
	o := &digerOnbellek{Bicim: digerOnbellekBicimi, Dosyalar: map[string]*digerDosyaKaydi{}}
	f, err := os.Open(digerOnbellekYolu())
	if err != nil {
		return o
	}
	defer f.Close()
	var y digerOnbellek
	if gob.NewDecoder(bufio.NewReader(f)).Decode(&y) != nil || y.Bicim != digerOnbellekBicimi || y.Dosyalar == nil {
		return o
	}
	return &y
}

func digerOnbellekKaydet(o *digerOnbellek) {
	var buf bytes.Buffer
	_ = os.MkdirAll(dizin, 0o700)
	if gob.NewEncoder(&buf).Encode(o) == nil {
		_ = dosyaYaz(digerOnbellekYolu(), buf.Bytes(), 0o600)
	}
}

// ---------- ChatGPT / Codex oturum okuyucu (~/.codex/sessions) ----------

type codexSatirJSON struct {
	Timestamp string          `json:"timestamp"`
	Type      string          `json:"type"`
	Payload   json.RawMessage `json:"payload"`
}

type codexMetaPayload struct {
	ID               string `json:"id"`
	CreatorAccountID string `json:"creator_account_id"`
	CreatorUserID    string `json:"creator_user_id"`
}

type codexTurnPayload struct {
	Model string `json:"model"`
}

type codexPencere struct {
	UsedPercent   *float64 `json:"used_percent"`
	WindowMinutes int64    `json:"window_minutes"`
	ResetsAt      int64    `json:"resets_at"`
}

type codexEventPayload struct {
	Type string `json:"type"`
	Info *struct {
		Total *struct {
			TotalTokens int64 `json:"total_tokens"`
		} `json:"total_token_usage"`
		Last *struct {
			Input      int64 `json:"input_tokens"`
			Cached     int64 `json:"cached_input_tokens"`
			CacheWrite int64 `json:"cache_write_input_tokens"`
			Output     int64 `json:"output_tokens"`
			Reasoning  int64 `json:"reasoning_output_tokens"`
			Total      int64 `json:"total_tokens"`
		} `json:"last_token_usage"`
	} `json:"info"`
	RateLimits *struct {
		Primary   *codexPencere `json:"primary"`
		Secondary *codexPencere `json:"secondary"`
	} `json:"rate_limits"`
}

func codexKaynaklari() []string {
	ev := evDizini()
	var liste []string
	gorulen := map[string]bool{}
	ekle := func(y string) {
		if y == "" || !klasorMu(y) {
			return
		}
		g := gercekYol(y)
		if gorulen[g] {
			return
		}
		gorulen[g] = true
		liste = append(liste, y)
	}
	if ch := os.Getenv("CODEX_HOME"); ch != "" {
		ekle(filepath.Join(genislet(ch), "sessions"))
		ekle(filepath.Join(genislet(ch), "archived_sessions"))
	}
	ekle(filepath.Join(ev, ".codex", "sessions"))
	ekle(filepath.Join(ev, ".codex", "archived_sessions"))
	return liste
}

// codexIlkMetaOku: eski bir oturum dosyasından yalnızca ilk birkaç satırdaki session_meta kimliğini okur (en çok 512 KB; base_instructions uzun olabilir).
func codexIlkMetaOku(yol string) string {
	f, err := os.Open(yol)
	if err != nil {
		return ""
	}
	defer f.Close()
	br := bufio.NewReaderSize(io.LimitReader(f, 512*1024), 64*1024)
	for i := 0; i < 6; i++ {
		b, err := br.ReadSlice('\n')
		if err == bufio.ErrBufferFull {
			uzun := append([]byte(nil), b...)
			for err == bufio.ErrBufferFull {
				b, err = br.ReadSlice('\n')
				uzun = append(uzun, b...)
			}
			b = uzun
		}
		if len(b) > 0 && bytes.Contains(b, []byte(`"session_meta"`)) {
			var s codexSatirJSON
			if json.Unmarshal(b, &s) == nil && s.Type == "session_meta" {
				var m codexMetaPayload
				if json.Unmarshal(s.Payload, &m) == nil {
					if strings.TrimSpace(m.CreatorAccountID) != "" {
						return strings.TrimSpace(m.CreatorAccountID)
					}
					if strings.TrimSpace(m.CreatorUserID) != "" {
						return strings.TrimSpace(m.CreatorUserID)
					}
				}
			}
		}
		if err != nil {
			break
		}
	}
	return ""
}

func codexPencereEkle(hedef map[string]string, p *codexPencere, varsayilanHaftalik bool) {
	if p == nil || p.UsedPercent == nil {
		return
	}
	v := *p.UsedPercent
	if v < 0 || v > 100 {
		return
	}
	var onek string
	switch {
	case p.WindowMinutes > 0 && p.WindowMinutes <= 480:
		onek = "five_hour"
	case (p.WindowMinutes >= 9000 && p.WindowMinutes <= 11520) || (p.WindowMinutes == 0 && varsayilanHaftalik):
		onek = "seven_day"
	case p.WindowMinutes == 0 && !varsayilanHaftalik:
		onek = "five_hour"
	case p.WindowMinutes > 11520:
		// Ücretsiz (free) hesaplarda 43200 dk (30 gün) aylık kota penceresi gelir; 7 günlük haftalık sıfırlamayla karıştırılmaz
		onek = "thirty_day"
	default:
		return
	}
	hedef[onek+".used_percentage"] = strconv.FormatFloat(v, 'f', -1, 64)
	if p.ResetsAt > 0 {
		hedef[onek+".resets_at"] = strconv.FormatInt(p.ResetsAt, 10)
	}
}

func codexDosyaOku(yol string, st fs.FileInfo, eski *digerDosyaKaydi) (*digerDosyaKaydi, bool) {
	boyut, mt := st.Size(), st.ModTime().UnixNano()
	if eski != nil && eski.Boyut == boyut && eski.Mtime == mt {
		return eski, false
	}
	f, err := os.Open(yol)
	if err != nil {
		return nil, false
	}
	defer f.Close()

	dosyaAdi := strings.TrimSuffix(filepath.Base(yol), ".jsonl")
	d := &digerDosyaKaydi{
		Boyut: boyut,
		Mtime: mt,
		Sid:   digerSid("codex", dosyaAdi),
	}

	br := bufio.NewReaderSize(f, 128*1024)
	model := "gpt-5-codex"
	sonToplam := int64(-1)
	sonTekil := ""

	for {
		b, err := br.ReadSlice('\n')
		if err == bufio.ErrBufferFull {
			uzun := append([]byte(nil), b...)
			for err == bufio.ErrBufferFull {
				b, err = br.ReadSlice('\n')
				uzun = append(uzun, b...)
			}
			b = uzun
		}
		if len(bytes.TrimSpace(b)) > 0 {
			if bytes.Contains(b, []byte(`"session_meta"`)) || bytes.Contains(b, []byte(`"turn_context"`)) || bytes.Contains(b, []byte(`"token_count"`)) {
				var s codexSatirJSON
				if json.Unmarshal(b, &s) == nil {
					switch s.Type {
					case "session_meta":
						var m codexMetaPayload
						if json.Unmarshal(s.Payload, &m) == nil {
							if m.ID != "" {
								d.Sid = digerSid("codex", m.ID)
							}
							if strings.TrimSpace(m.CreatorAccountID) != "" {
								d.HesapHam = strings.TrimSpace(m.CreatorAccountID)
							} else if strings.TrimSpace(m.CreatorUserID) != "" {
								d.HesapHam = strings.TrimSpace(m.CreatorUserID)
							}
						}
					case "turn_context":
						var tc codexTurnPayload
						if json.Unmarshal(s.Payload, &tc) == nil && tc.Model != "" {
							model = codexModelAd(tc.Model)
						}
					case "event_msg":
						var ev codexEventPayload
						if json.Unmarshal(s.Payload, &ev) == nil && ev.Type == "token_count" {
							var ts int64
							if t, errT := time.Parse(time.RFC3339Nano, s.Timestamp); errT == nil {
								ts = t.Unix()
							}
							if ev.RateLimits != nil && ts > 0 {
								rm := map[string]string{}
								codexPencereEkle(rm, ev.RateLimits.Primary, true)
								codexPencereEkle(rm, ev.RateLimits.Secondary, false)
								if len(rm) > 0 && ts >= d.LimitT {
									d.LimitT = ts
									d.LimitR = rm
								}
							}
							if ev.Info != nil && ev.Info.Last != nil && ts > 0 {
								son := ev.Info.Last
								atla := false
								if ev.Info.Total != nil && ev.Info.Total.TotalTokens > 0 {
									if ev.Info.Total.TotalTokens == sonToplam {
										atla = true
									} else {
										sonToplam = ev.Info.Total.TotalTokens
									}
								} else {
									imza := s.Timestamp + "|" + strconv.FormatInt(son.Input, 10) + "|" + strconv.FormatInt(son.Cached, 10) + "|" + strconv.FormatInt(son.Output, 10) + "|" + strconv.FormatInt(son.Reasoning, 10)
									if imza == sonTekil {
										atla = true
									} else {
										sonTekil = imza
									}
								}
								if !atla {
									girdi := son.Input - son.Cached
									if girdi < 0 {
										girdi = 0
									}
									cikti := son.Output
									if son.Reasoning > 0 {
										if cikti == 0 {
											cikti = son.Reasoning
										} else if son.Total > 0 && son.Total >= son.Input+son.Output+son.Reasoning {
											cikti = son.Output + son.Reasoning
										}
									}
									if girdi > 0 || cikti > 0 || son.Cached > 0 || son.CacheWrite > 0 {
										d.Kayitlar = append(d.Kayitlar, digerKayit{
											T:     ts,
											Model: model,
											In:    girdi,
											Out:   cikti,
											W5:    max(0, son.CacheWrite),
											Cr:    max(0, son.Cached),
										})
									}
								}
							}
						}
					}
				}
			}
		}
		if err != nil {
			break
		}
	}
	return d, true
}

// ---------- Gemini CLI oturum okuyucu (~/.gemini/tmp/.../chats/session-*.json) ----------

type geminiMesajJSON struct {
	SessionID string `json:"sessionId"`
	Timestamp string `json:"timestamp"`
	Type      string `json:"type"`
	Model     string `json:"model"`
	Tokens    *struct {
		Input    int64 `json:"input"`
		Output   int64 `json:"output"`
		Cached   int64 `json:"cached"`
		Thoughts int64 `json:"thoughts"`
		Tool     int64 `json:"tool"`
	} `json:"tokens"`
}

type geminiOturumJSON struct {
	SessionID string            `json:"sessionId"`
	Messages  []geminiMesajJSON `json:"messages"`
}

func geminiKaynaklari() []string {
	ev := evDizini()
	var liste []string
	gorulen := map[string]bool{}
	ekle := func(y string) {
		if y == "" || !klasorMu(y) {
			return
		}
		g := gercekYol(y)
		if gorulen[g] {
			return
		}
		gorulen[g] = true
		liste = append(liste, y)
	}
	if gh := os.Getenv("GEMINI_CLI_HOME"); gh != "" {
		ekle(filepath.Join(genislet(gh), ".gemini", "tmp"))
		ekle(filepath.Join(genislet(gh), "tmp"))
	}
	ekle(filepath.Join(ev, ".gemini", "tmp"))
	return liste
}

func geminiDosyaOku(yol string, st fs.FileInfo, eski *digerDosyaKaydi) (*digerDosyaKaydi, bool) {
	boyut, mt := st.Size(), st.ModTime().UnixNano()
	if eski != nil && eski.Boyut == boyut && eski.Mtime == mt {
		return eski, false
	}
	// Güvenlik sınırı: aşırı büyük beklenmeyen dosyaları atla (32 MB)
	if boyut <= 0 || boyut > 32<<20 {
		return nil, false
	}
	b, err := os.ReadFile(yol)
	if err != nil {
		return nil, false
	}
	var ot geminiOturumJSON
	if strings.HasSuffix(yol, ".jsonl") {
		for _, satir := range bytes.Split(b, []byte{'\n'}) {
			satir = bytes.TrimSpace(satir)
			if len(satir) == 0 {
				continue
			}
			var sarmal geminiOturumJSON
			if bytes.Contains(satir, []byte(`"messages"`)) && json.Unmarshal(satir, &sarmal) == nil && len(sarmal.Messages) > 0 {
				if ot.SessionID == "" && strings.TrimSpace(sarmal.SessionID) != "" {
					ot.SessionID = strings.TrimSpace(sarmal.SessionID)
				}
				ot.Messages = append(ot.Messages, sarmal.Messages...)
				continue
			}
			var m geminiMesajJSON
			if json.Unmarshal(satir, &m) == nil {
				if ot.SessionID == "" && strings.TrimSpace(m.SessionID) != "" {
					ot.SessionID = strings.TrimSpace(m.SessionID)
				}
				ot.Messages = append(ot.Messages, m)
			}
		}
		if ot.SessionID == "" && len(ot.Messages) == 0 {
			return nil, false
		}
	} else if json.Unmarshal(b, &ot) != nil {
		return nil, false
	}
	kimlik := strings.TrimSpace(ot.SessionID)
	if kimlik == "" {
		kimlik = strings.TrimSuffix(strings.TrimSuffix(filepath.Base(yol), ".jsonl"), ".json")
	}
	d := &digerDosyaKaydi{
		Boyut: boyut,
		Mtime: mt,
		Sid:   digerSid("gemini", kimlik),
	}
	for _, m := range ot.Messages {
		if m.Tokens == nil || (m.Type != "gemini" && m.Type != "model" && m.Type != "assistant") {
			continue
		}
		t, err := time.Parse(time.RFC3339Nano, m.Timestamp)
		if err != nil {
			continue
		}
		girdi := m.Tokens.Input - m.Tokens.Cached
		if girdi < 0 {
			girdi = 0
		}
		cikti := max(0, m.Tokens.Output) + max(0, m.Tokens.Thoughts) + max(0, m.Tokens.Tool)
		okuma := max(0, m.Tokens.Cached)
		if girdi == 0 && cikti == 0 && okuma == 0 {
			continue
		}
		d.Kayitlar = append(d.Kayitlar, digerKayit{
			T:     t.Unix(),
			Model: geminiModelAd(m.Model),
			In:    girdi,
			Out:   cikti,
			Cr:    okuma,
		})
	}
	return d, true
}

// ---------- birleşik ek toplama ----------

type digerSonuc struct {
	satirlar       []Satir
	dilimler       []Dilim
	bulgular       []Bulgu
	okunan         int
	hesaplar       map[string]string       // {"claude": "16hex", "chatgpt": "16hex", "gemini": "16hex"}
	epostalar      map[string]string       // {"claude": "ad@alan.com", "chatgpt": "ad@alan.com", "gemini": "ad@alan.com"}
	limitler       map[string]limitDosyasi // {"chatgpt": {t, r}, "gemini": {t, r}}
	haftaSifirlama map[string]int64        // {"claude": unix, "chatgpt": unix, "gemini": unix}
}

func digerDosyaSaglayici(yol string, d *digerDosyaKaydi) string {
	if d != nil {
		if d.SonModel != "" {
			return "gemini"
		}
		for _, k := range d.Kayitlar {
			if strings.HasPrefix(k.Model, "gemini-") || strings.HasPrefix(k.Model, "agy-") {
				return "gemini"
			}
			return "chatgpt"
		}
	}
	y := strings.ToLower(filepath.ToSlash(yol))
	if strings.Contains(y, "/.gemini/") || strings.Contains(y, "antigravity") {
		return "gemini"
	}
	return "chatgpt"
}

func digerOrtamKontrol(ekBulgu func(kod, seviye, metin string)) {
	ev := evDizini()
	evGercek := gercekYol(ev)

	// ChatGPT / Codex klasör ve ortam kontrolleri
	codexYollar := []string{
		filepath.Join(ev, ".codex"),
		filepath.Join(ev, ".codex", "sessions"),
		filepath.Join(ev, ".codex", "archived_sessions"),
	}
	if ch := strings.TrimSpace(os.Getenv("CODEX_HOME")); ch != "" {
		chGen := genislet(ch)
		codexYollar = append(codexYollar, chGen, filepath.Join(chGen, "sessions"), filepath.Join(chGen, "archived_sessions"))
		if gercekYol(chGen) != gercekYol(filepath.Join(ev, ".codex")) {
			ekBulgu("config_dir_chatgpt", "d", "CODEX_HOME ayarlı: ChatGPT/Codex kayıtları farklı bir klasörde (o da sayılıyor)")
		}
	}
	for _, p := range codexYollar {
		if yolEngelMi(p) {
			ekBulgu("engel_chatgpt", "s", "ChatGPT/Codex kayıt klasörü okunamıyor ya da yönlendirilmiş: kullanım gizleniyor olabilir")
			break
		}
	}
	for _, ad := range []string{"OPENAI_BASE_URL", "OPENAI_API_BASE"} {
		if v := strings.ToLower(strings.TrimRight(strings.TrimSpace(os.Getenv(ad)), "/")); v != "" {
			if !strings.HasPrefix(v, "https://api.openai.com") && !strings.HasPrefix(v, "https://chatgpt.com") {
				ekBulgu("api_ortam_chatgpt", "s", ad+" ayarlı: kullanım farklı bir API adresine yönlendirilmiş olabilir")
				break
			}
		}
	}
	if strings.TrimSpace(os.Getenv("OPENAI_API_KEY")) != "" {
		ekBulgu("api_key_chatgpt", "d", "OPENAI_API_KEY tanımlı: Codex abonelik yerine API anahtarı kullanıyor olabilir")
	}

	// Gemini CLI / Antigravity klasör ve ortam kontrolleri
	geminiYollar := []string{
		filepath.Join(ev, ".gemini"),
		filepath.Join(ev, ".gemini", "tmp"),
	}
	if gh := strings.TrimSpace(os.Getenv("GEMINI_CLI_HOME")); gh != "" {
		ghGen := genislet(gh)
		geminiYollar = append(geminiYollar, ghGen, filepath.Join(ghGen, ".gemini", "tmp"), filepath.Join(ghGen, "tmp"))
		ghGercek := gercekYol(ghGen)
		if ghGercek != gercekYol(filepath.Join(ev, ".gemini")) && ghGercek != evGercek {
			ekBulgu("config_dir_gemini", "d", "GEMINI_CLI_HOME ayarlı: Gemini kayıtları farklı bir klasörde (o da sayılıyor)")
		}
	}
	for _, p := range geminiYollar {
		if yolEngelMi(p) {
			ekBulgu("engel_gemini", "s", "Gemini kayıt klasörü okunamıyor ya da yönlendirilmiş: kullanım gizleniyor olabilir")
			break
		}
	}
	for _, ad := range []string{"GOOGLE_GEMINI_BASE_URL", "GEMINI_API_BASE_URL"} {
		if v := strings.ToLower(strings.TrimRight(strings.TrimSpace(os.Getenv(ad)), "/")); v != "" {
			if !strings.HasPrefix(v, "https://generativelanguage.googleapis.com") && !strings.HasPrefix(v, "https://aiplatform.googleapis.com") {
				ekBulgu("api_ortam_gemini", "s", ad+" ayarlı: kullanım farklı bir API adresine yönlendirilmiş olabilir")
				break
			}
		}
	}
	if strings.TrimSpace(os.Getenv("GEMINI_API_KEY")) != "" || strings.TrimSpace(os.Getenv("GOOGLE_API_KEY")) != "" {
		ekBulgu("api_key_gemini", "d", "GEMINI_API_KEY tanımlı: Gemini CLI abonelik yerine API anahtarı kullanıyor olabilir")
	}
}

func toplaDiger(simdi int64) digerSonuc {
	sinir := (simdi/3600)*3600 - (saatPenceresi-1)*3600
	var ekBulgular []Bulgu
	ekBulgu := func(kod, seviye, metin string) {
		for _, b := range ekBulgular {
			if b.K == kod {
				return
			}
		}
		if r := []rune(metin); len(r) > 140 {
			metin = string(r[:140])
		}
		ekBulgular = append(ekBulgular, Bulgu{K: kod, S: seviye, T: metin})
	}
	digerOrtamKontrol(ekBulgu)

	onb := digerOnbellekYukle()
	yeniOnb := map[string]*digerDosyaKaydi{}
	okunan := 0

	var codexDosyalar []*digerDosyaKaydi
	var enYeniCodexMt int64
	var enYeniCodexYol string
	var enYeniCodexHesap string
	var engelChatgpt bool
	kuculenCodex := map[*digerDosyaKaydi]bool{}

	for _, kok := range codexKaynaklari() {
		_ = filepath.WalkDir(kok, func(yol string, e fs.DirEntry, err error) error {
			if err != nil {
				if errors.Is(err, fs.ErrPermission) {
					engelChatgpt = true
				}
				return nil
			}
			if e == nil || e.IsDir() || !strings.HasSuffix(e.Name(), ".jsonl") {
				return nil
			}
			if dosyaEngelMi(yol) {
				engelChatgpt = true
				return nil
			}
			st, err := os.Stat(yol)
			if err != nil || !st.Mode().IsRegular() {
				return nil
			}
			mtNano := st.ModTime().UnixNano()
			if mtNano > enYeniCodexMt {
				enYeniCodexMt = mtNano
				enYeniCodexYol = yol
			}
			if float64(mtNano)/1e9 < float64(sinir) {
				return nil
			}
			if _, var_ := yeniOnb[yol]; var_ {
				return nil
			}
			eski := onb.Dosyalar[yol]
			var eskiTok int64
			if eski != nil {
				for _, k := range eski.Kayitlar {
					if k.T >= sinir {
						eskiTok += k.In + k.Out + k.W5 + k.Cr
					}
				}
			}
			d, okundu := codexDosyaOku(yol, st, eski)
			if d == nil {
				return nil
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
				kuculenCodex[d] = true
			}
			yeniOnb[yol] = d
			codexDosyalar = append(codexDosyalar, d)
			if mtNano == enYeniCodexMt && d.HesapHam != "" {
				enYeniCodexHesap = d.HesapHam
			}
			return nil
		})
	}
	if enYeniCodexHesap == "" && enYeniCodexYol != "" {
		if d, var_ := yeniOnb[enYeniCodexYol]; var_ && d.HesapHam != "" {
			enYeniCodexHesap = d.HesapHam
		} else {
			enYeniCodexHesap = codexIlkMetaOku(enYeniCodexYol)
		}
	}
	// En yeni dosyada hesap yoksa pencerede taranan en güncel hesaplı dosyayı bul
	if enYeniCodexHesap == "" && len(codexDosyalar) > 0 {
		sirali := append([]*digerDosyaKaydi(nil), codexDosyalar...)
		sort.Slice(sirali, func(i, j int) bool { return sirali[i].Mtime > sirali[j].Mtime })
		for _, d := range sirali {
			if d.HesapHam != "" {
				enYeniCodexHesap = d.HesapHam
				break
			}
		}
	}

	var geminiDosyalar []*digerDosyaKaydi
	var kuculenGemini, engelGemini bool
	for _, kok := range geminiKaynaklari() {
		_ = filepath.WalkDir(kok, func(yol string, e fs.DirEntry, err error) error {
			if err != nil {
				if errors.Is(err, fs.ErrPermission) {
					engelGemini = true
				}
				return nil
			}
			if e == nil || e.IsDir() {
				return nil
			}
			ad := e.Name()
			if !strings.HasPrefix(ad, "session-") || (!strings.HasSuffix(ad, ".json") && !strings.HasSuffix(ad, ".jsonl")) {
				return nil
			}
			if dosyaEngelMi(yol) {
				engelGemini = true
				return nil
			}
			st, err := os.Stat(yol)
			if err != nil || !st.Mode().IsRegular() {
				return nil
			}
			if float64(st.ModTime().UnixNano())/1e9 < float64(sinir) {
				return nil
			}
			if _, var_ := yeniOnb[yol]; var_ {
				return nil
			}
			eski := onb.Dosyalar[yol]
			var eskiTok int64
			if eski != nil {
				for _, k := range eski.Kayitlar {
					if k.T >= sinir {
						eskiTok += k.In + k.Out + k.W5 + k.Cr
					}
				}
			}
			d, okundu := geminiDosyaOku(yol, st, eski)
			if d == nil {
				return nil
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
				kuculenGemini = true
			}
			yeniOnb[yol] = d
			geminiDosyalar = append(geminiDosyalar, d)
			return nil
		})
	}

	agyDosyalar, agyOkunan, agyKuculen, agyEngel := agyTopla(onb.Dosyalar, yeniOnb, sinir)
	okunan += agyOkunan
	if agyKuculen {
		kuculenGemini = true
	}
	if agyEngel {
		engelGemini = true
	}

	codexAuthHesap, codexAuthEposta := codexAuthBilgisiOku()
	if enYeniCodexHesap == "" && codexAuthHesap != "" {
		enYeniCodexHesap = codexAuthHesap
	}

	// Önceden önbellekte olup son 7 günde gerçek kullanım taşıyan ancak diskten silinmiş oturum dosyaları
	// (Codex arşivleme gibi aynı Sid'in başka bir klasöre taşındığı durumlar silinme sayılmaz.)
	aktifSidler := make(map[string]bool, len(yeniOnb))
	for _, d := range yeniOnb {
		if d != nil && d.Sid != "" {
			aktifSidler[d.Sid] = true
		}
	}
	var silinenChatgpt, silinenGemini int64
	yediGunOnce := simdi - 7*86400
	eskiSayisi := len(onb.Dosyalar)
	for yol, eskiD := range onb.Dosyalar {
		if _, var_ := yeniOnb[yol]; var_ || eskiD == nil {
			continue
		}
		if eskiD.Sid != "" && aktifSidler[eskiD.Sid] {
			continue
		}
		if _, err := os.Stat(yol); !errors.Is(err, fs.ErrNotExist) {
			continue
		}
		sgl := digerDosyaSaglayici(yol, eskiD)
		if sgl == "chatgpt" && enYeniCodexHesap != "" && eskiD.HesapHam != "" && !strings.EqualFold(eskiD.HesapHam, enYeniCodexHesap) {
			continue
		}
		for _, k := range eskiD.Kayitlar {
			if k.T >= yediGunOnce {
				if sgl == "gemini" {
					silinenGemini += k.In + k.Out + k.W5 + k.Cr
				} else {
					silinenChatgpt += k.In + k.Out + k.W5 + k.Cr
				}
			}
		}
	}

	onb.Dosyalar = yeniOnb
	if len(yeniOnb) > 0 || eskiSayisi > 0 {
		digerOnbellekKaydet(onb)
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
		s := saatlik[ga]
		if s == nil {
			s = &sayac{}
			saatlik[ga] = s
		}
		s.in += k.In
		s.out += k.Out
		s.w5 += k.W5
		s.cr += k.Cr
		s.msg++

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

	var codexLimitT int64
	var codexLimitR map[string]string
	var codexResetT, codexHaftaReset int64
	var kuculenChatgpt bool
	var gelecekChatgpt, editliChatgpt, buyukChatgpt int
	for _, d := range codexDosyalar {
		// Aynı bilgisayarda farklı bir hesapla açılmış eski Codex oturumu varsa aktif hesaba karışmasın
		if enYeniCodexHesap != "" && d.HesapHam != "" && !strings.EqualFold(d.HesapHam, enYeniCodexHesap) {
			continue
		}
		if kuculenCodex[d] {
			kuculenChatgpt = true
		}
		mtSn := float64(d.Mtime) / 1e9
		for _, k := range d.Kayitlar {
			if k.T > simdi+300 {
				gelecekChatgpt++
			}
			if k.T >= sinir && float64(k.T) > mtSn+3600 {
				editliChatgpt++
			}
			if k.T >= sinir && (k.Out > 500_000 || k.In+k.Cr+k.W5 > 10_000_000) {
				buyukChatgpt++
			}
			kayitIsle(d.Sid, k)
		}
		if d.LimitT > codexResetT && len(d.LimitR) > 0 && (simdi <= 0 || simdi-d.LimitT <= 14*86400) {
			if rs, var_ := d.LimitR["seven_day.resets_at"]; var_ {
				if wr, err := strconv.ParseInt(rs, 10, 64); err == nil && wr > 1_600_000_000 {
					codexResetT = d.LimitT
					codexHaftaReset = sonrakiHaftaReset(wr, simdi)
				}
			}
		}
		if d.LimitT > codexLimitT && len(d.LimitR) > 0 && (simdi <= 0 || simdi-d.LimitT <= 24*3600) {
			codexLimitT = d.LimitT
			codexLimitR = d.LimitR
		}
	}

	var gelecekGemini, editliGemini, buyukGemini int
	geminiDenetle := func(d *digerDosyaKaydi) {
		mtSn := float64(d.Mtime) / 1e9
		for _, k := range d.Kayitlar {
			if k.T > simdi+300 {
				gelecekGemini++
			}
			if k.T >= sinir && float64(k.T) > mtSn+3600 {
				editliGemini++
			}
			if k.T >= sinir && (k.Out > 500_000 || k.In+k.Cr+k.W5 > 10_000_000) {
				buyukGemini++
			}
			kayitIsle(d.Sid, k)
		}
	}
	for _, d := range geminiDosyalar {
		geminiDenetle(d)
	}
	for _, d := range agyDosyalar {
		geminiDenetle(d)
	}

	if engelChatgpt {
		ekBulgu("engel_chatgpt", "s", "ChatGPT/Codex kayıt klasörü veya dosyası okunamıyor ya da yönlendirilmiş: kullanım gizleniyor olabilir")
	}
	if kuculenChatgpt {
		ekBulgu("kuculen_chatgpt", "s", "ChatGPT/Codex oturum kayıt dosyasındaki geçmiş token kayıtları azaltılmış ya da kırpılmış")
	}
	if silinenChatgpt >= 25_000 {
		ekBulgu("silinen_chatgpt", "s", fmt.Sprintf("Son 7 güne ait silinmiş ChatGPT/Codex oturum dosyalarında %d bin token tespit edildi", silinenChatgpt/1000))
	}
	if gelecekChatgpt > 0 {
		ekBulgu("gelecek_chatgpt", "s", fmt.Sprintf("%d ChatGPT/Codex kaydı gelecek tarihli: saat ya da kayıt dosyası oynanmış olabilir", gelecekChatgpt))
	}
	if editliChatgpt > 0 {
		ekBulgu("editli_chatgpt", "s", fmt.Sprintf("%d ChatGPT/Codex kaydının tarihi dosya değişim zamanından sonra: sonradan düzenlenmiş olabilir", editliChatgpt))
	}
	if buyukChatgpt > 0 {
		ekBulgu("buyuk_chatgpt", "s", fmt.Sprintf("%d ChatGPT/Codex kaydında imkânsız derecede büyük token değeri var", buyukChatgpt))
	}

	if engelGemini {
		ekBulgu("engel_gemini", "s", "Gemini/Antigravity kayıt klasörü veya dökümü okunamıyor ya da yönlendirilmiş: kullanım gizleniyor olabilir")
	}
	if kuculenGemini {
		ekBulgu("kuculen_gemini", "s", "Gemini/Antigravity oturum dökümündeki geçmiş token kayıtları azaltılmış ya da kırpılmış")
	}
	if silinenGemini >= 25_000 {
		ekBulgu("silinen_gemini", "s", fmt.Sprintf("Son 7 güne ait silinmiş Gemini/Antigravity oturum dosyalarında %d bin token tespit edildi", silinenGemini/1000))
	}
	if gelecekGemini > 0 {
		ekBulgu("gelecek_gemini", "s", fmt.Sprintf("%d Gemini/Antigravity kaydı gelecek tarihli: saat ya da kayıt dosyası oynanmış olabilir", gelecekGemini))
	}
	if editliGemini > 0 {
		ekBulgu("editli_gemini", "s", fmt.Sprintf("%d Gemini/Antigravity kaydının tarihi dosya değişim zamanından sonra: sonradan düzenlenmiş olabilir", editliGemini))
	}
	if buyukGemini > 0 {
		ekBulgu("buyuk_gemini", "s", fmt.Sprintf("%d Gemini/Antigravity kaydında imkânsız derecede büyük token değeri var", buyukGemini))
	}

	hesaplar := map[string]string{}
	epostalar := map[string]string{}
	claudeHesap, claudeEposta := claudeHesapBilgisiOku()
	if claudeHesap != "" {
		hesaplar["claude"] = claudeHesap
	}
	if claudeEposta != "" {
		epostalar["claude"] = claudeEposta
	}
	if h := hesapOzeti("chatgpt", enYeniCodexHesap); h != "" {
		hesaplar["chatgpt"] = h
	}
	if codexAuthEposta != "" && (enYeniCodexHesap == "" || codexAuthHesap == "" || strings.EqualFold(enYeniCodexHesap, codexAuthHesap)) {
		epostalar["chatgpt"] = codexAuthEposta
	}
	geminiHesap, geminiEposta, geminiLim, geminiLimVar, geminiHaftaReset := geminiHesapLimitVeResetOku(simdi)
	if geminiHesap != "" {
		hesaplar["gemini"] = geminiHesap
	}
	if geminiEposta != "" {
		epostalar["gemini"] = geminiEposta
	}

	haftaSifirlama := map[string]int64{}
	if claudeReset, ok := claudeHaftalikSifirlamaOku(simdi); ok && claudeReset > 0 {
		haftaSifirlama["claude"] = claudeReset
	}
	if codexHaftaReset > 0 {
		haftaSifirlama["chatgpt"] = codexHaftaReset
	}
	if geminiHaftaReset > 0 {
		haftaSifirlama["gemini"] = geminiHaftaReset
	}

	limitler := map[string]limitDosyasi{}
	if codexLimitT > 0 && len(codexLimitR) > 0 && codexLimitT > sonLimitSaglayiciT("chatgpt") {
		numHarita := make(map[string]json.Number, len(codexLimitR))
		for k, v := range codexLimitR {
			numHarita[k] = json.Number(v)
		}
		sinirli := limitSinirla(numHarita)
		if len(sinirli) > 0 {
			limitler["chatgpt"] = limitDosyasi{T: codexLimitT, R: sinirli}
		}
	}
	if geminiLimVar {
		limitler["gemini"] = geminiLim
	}

	out := digerSonuc{
		satirlar:       make([]Satir, 0, len(saatlik)),
		dilimler:       make([]Dilim, 0, len(besDk)),
		bulgular:       ekBulgular,
		okunan:         okunan,
		hesaplar:       hesaplar,
		epostalar:      epostalar,
		limitler:       limitler,
		haftaSifirlama: haftaSifirlama,
	}
	for ga, v := range saatlik {
		out.satirlar = append(out.satirlar, Satir{ga.zaman, ga.model, ga.sid, v.in, v.out, v.w5, v.w1, v.cr, v.msg})
	}
	for ga, v := range besDk {
		out.dilimler = append(out.dilimler, Dilim{ga.zaman, ga.model, ga.sid, v.in, v.out, v.w5, v.w1, v.cr, v.msg})
	}
	return out
}
