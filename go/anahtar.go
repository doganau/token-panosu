package main

import (
	"os"
	"path/filepath"
	"strings"
)

func dosyaAnahtarYolu() string { return filepath.Join(dizin, "anahtar") }

// Gizli anahtar: macOS'ta Anahtar Zinciri, Windows'ta DPAPI ile şifreli dosya, Linux'ta 0600 dosya.
// Test modunda (TOKEN_PANOSU_DIZIN) her sistemde test klasöründeki dosya kullanılır.
func anahtarKaydet(cihaz, gizli string) error {
	if testModu {
		return dosyaAnahtarYaz(gizli)
	}
	return sistemAnahtarKaydet(cihaz, gizli)
}

func anahtarOku(cihaz string) (string, error) {
	if testModu {
		return dosyaAnahtarOku()
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
