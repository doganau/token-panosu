//go:build sag_chatgpt || (!sag_claude && !sag_gemini)

package main

import (
	"bufio"
	"bytes"
	"encoding/base64"
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

const (
	sagChatgptEtkin = true
	sagChatgptAd    = "chatgpt"
)

func codexModelAd(ham string) string {
	m := modelTemizle(ham)
	if m == "" {
		return "gpt-5-codex"
	}
	if !strings.HasPrefix(m, "gpt-") &&
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
		if !yerelYolMu(yol) {
			continue
		}
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

func codexHesapOku() string {
	hesap, _ := codexAuthBilgisiOku()
	return hesapOzeti(sagChatgptAd, hesap)
}

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

func codexKokleri() []string {
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

func codexKaynaklari() []string { return codexKokleri() }

// codexIlkMetaOku: eski bir oturum dosyasından yalnızca ilk birkaç satırdaki session_meta kimliğini okur (en çok 512 KB).
func codexIlkMetaOku(yol string) string {
	if !yerelYolMu(yol) {
		return ""
	}
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
		eski.Saglayici = sagChatgptAd
		return eski, false
	}
	if !yerelYolMu(yol) {
		return nil, false
	}
	f, err := os.Open(yol)
	if err != nil {
		return nil, false
	}
	defer f.Close()

	dosyaAdi := strings.TrimSuffix(filepath.Base(yol), ".jsonl")
	d := &digerDosyaKaydi{
		Saglayici: sagChatgptAd,
		Boyut:     boyut,
		Mtime:     mt,
		Sid:       digerSid("codex", dosyaAdi),
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

func chatgptSaglayiciKaydiMi(yol string, d *digerDosyaKaydi) bool {
	if d == nil {
		return false
	}
	if d.Saglayici != "" {
		return d.Saglayici == sagChatgptAd
	}
	y := strings.ToLower(filepath.ToSlash(yol))
	return strings.Contains(y, "/.codex/") || strings.Contains(y, "/sessions/") || strings.Contains(y, "/archived_sessions/")
}

func chatgptEsikAnahtarMi(k string) bool {
	for _, onek := range []string{"gpt-", "o1", "o3", "o4", "codex-"} {
		if strings.HasPrefix(k, onek) {
			return true
		}
	}
	return false
}

func chatgptEsikAltModel(model string) string {
	if strings.HasPrefix(model, "openai-") && len(model) > 7 {
		return model[7:]
	}
	return ""
}

func chatgptOrtamKontrol(ekBulgu func(kod, seviye, metin string)) {
	ev := evDizini()
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
}

func chatgptTopla(simdi, sinir int64, onceki, yeniDosyalar map[string]*digerDosyaKaydi, s *digerSonuc, ekBulgu func(kod, seviye, metin string)) []*digerDosyaKaydi {
	chatgptOrtamKontrol(ekBulgu)

	var codexDosyalar []*digerDosyaKaydi
	var enYeniCodexMt int64
	var enYeniCodexYol string
	var enYeniCodexHesap string
	var engelChatgpt bool
	kuculenCodex := map[*digerDosyaKaydi]bool{}

	for _, kok := range codexKokleri() {
		if !yerelYolMu(kok) {
			continue
		}
		_ = filepath.WalkDir(kok, func(yol string, e fs.DirEntry, err error) error {
			if !yerelYolMu(yol) {
				if e != nil && e.IsDir() && yol != kok {
					return fs.SkipDir
				}
				return nil
			}
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
			if _, var_ := yeniDosyalar[yol]; var_ {
				return nil
			}
			eski := onceki[yol]
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
				s.okunan++
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
			yeniDosyalar[yol] = d
			codexDosyalar = append(codexDosyalar, d)
			if mtNano == enYeniCodexMt && d.HesapHam != "" {
				enYeniCodexHesap = d.HesapHam
			}
			return nil
		})
	}
	if enYeniCodexHesap == "" && enYeniCodexYol != "" {
		if d, var_ := yeniDosyalar[enYeniCodexYol]; var_ && d.HesapHam != "" {
			enYeniCodexHesap = d.HesapHam
		} else {
			enYeniCodexHesap = codexIlkMetaOku(enYeniCodexYol)
		}
	}
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

	codexAuthHesap, codexAuthEposta := codexAuthBilgisiOku()
	if enYeniCodexHesap == "" && codexAuthHesap != "" {
		enYeniCodexHesap = codexAuthHesap
	}

	aktifSidler := make(map[string]bool, len(codexDosyalar))
	for _, d := range codexDosyalar {
		if d != nil && d.Sid != "" {
			aktifSidler[d.Sid] = true
		}
	}

	var silinenChatgpt int64
	yediGunOnce := simdi - 7*86400
	for yol, eskiD := range onceki {
		if _, var_ := yeniDosyalar[yol]; var_ || eskiD == nil || !chatgptSaglayiciKaydiMi(yol, eskiD) || !yerelYolMu(yol) {
			continue
		}
		if eskiD.Sid != "" && aktifSidler[eskiD.Sid] {
			continue
		}
		if _, err := os.Stat(yol); !errors.Is(err, fs.ErrNotExist) {
			continue
		}
		if enYeniCodexHesap != "" && eskiD.HesapHam != "" && !strings.EqualFold(eskiD.HesapHam, enYeniCodexHesap) {
			continue
		}
		for _, k := range eskiD.Kayitlar {
			if k.T >= yediGunOnce {
				silinenChatgpt += k.In + k.Out + k.W5 + k.Cr
			}
		}
	}

	var suzulen []*digerDosyaKaydi
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
		}
		suzulen = append(suzulen, d)
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

	if h := hesapOzeti(sagChatgptAd, enYeniCodexHesap); h != "" {
		s.hesaplar[sagChatgptAd] = h
	}
	if codexAuthEposta != "" && (enYeniCodexHesap == "" || codexAuthHesap == "" || strings.EqualFold(enYeniCodexHesap, codexAuthHesap)) {
		s.epostalar[sagChatgptAd] = codexAuthEposta
	}
	if codexHaftaReset > 0 {
		s.haftaSifirlama[sagChatgptAd] = codexHaftaReset
	}
	if codexLimitT > 0 && len(codexLimitR) > 0 && codexLimitT > sonLimitSaglayiciT(sagChatgptAd) {
		numHarita := make(map[string]json.Number, len(codexLimitR))
		for k, v := range codexLimitR {
			numHarita[k] = json.Number(v)
		}
		if sinirli := limitSinirla(numHarita); len(sinirli) > 0 {
			s.limitler[sagChatgptAd] = limitDosyasi{T: codexLimitT, R: sinirli}
		}
	}

	return suzulen
}
