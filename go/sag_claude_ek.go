//go:build sag_claude || (!sag_gemini && !sag_chatgpt)

package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
)

const (
	sagClaudeEtkin = true
	sagClaudeAd    = "claude"
)

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
		if !yerelYolMu(yol) {
			continue
		}
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
			return hesapOzeti(sagClaudeAd, yapi.OAuth.OrgUUID), ep
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
					return hesapOzeti(sagClaudeAd, org), yedekEposta
				}
			}
		}
	}

	if yedekHesap != "" {
		return hesapOzeti(sagClaudeAd, yedekHesap), yedekEposta
	}
	return "", yedekEposta
}

func claudeHesapOku() string {
	h, _ := claudeHesapBilgisiOku()
	return h
}

func claudeEkBilgi() (eposta, hesapAnahtar, taramaUyarisi string) {
	hesapAnahtar, eposta = claudeHesapBilgisiOku()
	return eposta, hesapAnahtar, ""
}

func claudeHaftaSifirlamaEkle(simdi int64, m map[string]int64) {
	if claudeReset, ok := claudeHaftalikSifirlamaOku(simdi); ok && claudeReset > 0 {
		m[sagClaudeAd] = claudeReset
	}
}

func claudeOzelAltKomut(arg string) bool {
	switch arg {
	case durumAnahtar:
		durumSatiri()
		return true
	case uyariAnahtar:
		uyariKomutu()
		return true
	case uyariGosterAnahtar:
		uyariGoster()
		return true
	}
	return false
}

func claudeEsikAnahtarMi(k string) bool {
	return strings.HasPrefix(k, "claude-")
}

func claudeEsikAday(s string) string {
	if strings.HasPrefix(s, "claude-") && strings.Contains(s, ".") {
		return strings.ReplaceAll(s, ".", "-")
	}
	return ""
}
