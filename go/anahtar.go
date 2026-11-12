package main

import (
	"os"
	"path/filepath"
	"strings"
)

func dosyaAnahtarYolu() string { return filepath.Join(dizin, "anahtar") }

func gecerliHexAnahtar(s string, uzunluk int) bool {
	if len(s) != uzunluk {
		return false
	}
	for i := 0; i < len(s); i++ {
		c := s[i]
		if !((c >= '0' && c <= '9') || (c >= 'a' && c <= 'f')) {
			return false
		}
	}
	return true
}

// Gizli anahtar: macOS'ta Anahtar Zinciri, Windows'ta DPAPI ile şifreli dosya, Linux'ta 0600 dosya.
// Anahtar Zinciri / DPAPI kilitli veya kum havuzu (IDE terminali vb.) nedeniyle reddedilirse
// eşleşme kodunun yanmaması için 0600 izinli yerel dosyaya güvenli geri dönüş yapar.
func anahtarKaydet(cihaz, gizli string) error {
	gizli = strings.ToLower(strings.TrimSpace(gizli))
	if testModu {
		return dosyaAnahtarYaz(gizli)
	}
	if err := sistemAnahtarKaydet(cihaz, gizli); err == nil {
		return nil
	}
	return dosyaAnahtarYaz(gizli)
}

func anahtarOku(cihaz string) (string, error) {
	if testModu {
		return dosyaAnahtarOku()
	}
	if s, err := sistemAnahtarOku(cihaz); err == nil {
		s = strings.ToLower(strings.TrimSpace(s))
		if gecerliHexAnahtar(s, 64) {
			return s, nil
		}
	}
	s, err := dosyaAnahtarOku()
	if err == nil {
		s = strings.ToLower(strings.TrimSpace(s))
		if gecerliHexAnahtar(s, 64) {
			return s, nil
		}
	}
	return sistemAnahtarOku(cihaz)
}

func anahtarSil(cihaz string) {
	if !testModu {
		sistemAnahtarSil(cihaz)
	}
	_ = os.Remove(dosyaAnahtarYolu())
}

// XML (plist, görev tanımı) içine yazılan yollar için kaçış
func xmlKacis(s string) string {
	return strings.NewReplacer("&", "&amp;", "<", "&lt;", ">", "&gt;", `"`, "&quot;").Replace(s)
}
