package main

// İstem uzunluğuna göre fiyatlanan modeller.
// Her istek kendi başına değerlendirilir ve istem uzunluğuna girdi, cache okuma ve cache yazma token'larının
// hepsi dahildir.
//
// Sunucu eşikleri gönderim cevabında verir (fiyat listesinden). Bu bilgisayar eşiği aşan mesajları
// "<model>@uzun" adıyla ayrı satırda toplar; sunucu o satırı yüksek fiyattan hesaplar ve ekranda
// model adını sade gösterir. Eşik bilgisi yoksa hiçbir şey değişmez.

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
)

const uzunEki = "@uzun"

func esikYolu() string { return filepath.Join(dizin, "esikler.json") }

func esikleriOku() map[string]int64 {
	m := map[string]int64{}
	if b, err := os.ReadFile(esikYolu()); err == nil {
		_ = json.Unmarshal(b, &m)
	}
	return m
}

func esikGecerliAnahtar(k string) bool {
	if len(k) == 0 || len(k) > 58 {
		return false
	}
	if sagClaudeEtkin && claudeEsikAnahtarMi(k) {
		return true
	}
	if sagChatgptEtkin && chatgptEsikAnahtarMi(k) {
		return true
	}
	if sagGeminiEtkin && geminiEsikAnahtarMi(k) {
		return true
	}
	return false
}

func esikleriKaydet(m map[string]int64) {
	temiz := map[string]int64{}
	for k, v := range m {
		if esikGecerliAnahtar(k) && v > 0 && v < 100_000_000 {
			temiz[k] = v
		}
		if len(temiz) >= 200 {
			break
		}
	}
	b, err := json.Marshal(temiz)
	if err != nil {
		return
	}
	if eski, err := os.ReadFile(esikYolu()); err == nil && string(eski) == string(b) {
		return
	}
	_ = os.MkdirAll(dizin, 0o700)
	_ = dosyaYaz(esikYolu(), b, 0o600)
}

func rakamMi(s string) bool {
	if len(s) == 0 {
		return false
	}
	for i := 0; i < len(s); i++ {
		if s[i] < '0' || s[i] > '9' {
			return false
		}
	}
	return true
}

func tarihEkiAt(s string) string {
	// -YYYYMMDD
	if n := len(s); n > 9 && s[n-9] == '-' && rakamMi(s[n-8:]) {
		return s[:n-9]
	}
	// -YYYY-MM-DD
	if n := len(s); n > 11 && s[n-11] == '-' && s[n-6] == '-' && s[n-3] == '-' &&
		rakamMi(s[n-10:n-6]) && rakamMi(s[n-5:n-3]) && rakamMi(s[n-2:]) {
		return s[:n-11]
	}
	return s
}

// Modelin eşiği: tam ad, alt model öneki, tarih veya -preview/-latest eki atılmış ad.
func esikBul(esikler map[string]int64, model string) int64 {
	if len(esikler) == 0 || model == "" {
		return 0
	}
	if v, ok := esikler[model]; ok {
		return v
	}
	if sagGeminiEtkin {
		if alt := geminiEsikAltModel(model); alt != "" {
			return esikBul(esikler, alt)
		}
	}
	if sagChatgptEtkin {
		if alt := chatgptEsikAltModel(model); alt != "" {
			return esikBul(esikler, alt)
		}
	}
	s := tarihEkiAt(model)
	if v, ok := esikler[s]; ok {
		return v
	}
	for _, ek := range []string{"-preview", "-latest"} {
		if strings.HasSuffix(s, ek) {
			if v, ok := esikler[strings.TrimSuffix(s, ek)]; ok {
				return v
			}
		}
	}
	if sagClaudeEtkin {
		if aday := claudeEsikAday(s); aday != "" {
			if v, ok := esikler[aday]; ok {
				return v
			}
		}
	}
	return 0
}
