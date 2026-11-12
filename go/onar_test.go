package main

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

func TestOnarKomutuOzelPythonBetiginiTasir(t *testing.T) {
	sahteEv := t.TempDir()
	t.Setenv("HOME", sahteEv)
	t.Setenv("USERPROFILE", sahteEv)
	t.Setenv("CLAUDE_CONFIG_DIR", "")
	t.Setenv("CODEX_HOME", "")
	t.Setenv("GEMINI_CLI_HOME", "")

	gecDizin := t.TempDir()
	laDizin := filepath.Join(t.TempDir(), "LaunchAgents")
	betikDizin := filepath.Join(t.TempDir(), "ozel-betik")
	if err := os.MkdirAll(laDizin, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(betikDizin, 0o755); err != nil {
		t.Fatal(err)
	}

	eskiDizin, eskiTest, eskiLA := dizin, testModu, testLaunchAgentsDizini
	dizin, testModu, testLaunchAgentsDizini = gecDizin, true, laDizin
	defer func() {
		dizin, testModu, testLaunchAgentsDizini = eskiDizin, eskiTest, eskiLA
	}()

	beklenenCihaz := "11112222333344445555666677778888"
	beklenenGizli := "aaaa1111bbbb2222cccc3333dddd4444eeee5555ffff66660000777788889999"

	// Sahte sunucu: /api/gonder isteğinde HMAC doğrulaması yapar
	gelenCihaz := ""
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/gonder" {
			http.NotFound(w, r)
			return
		}
		cihaz := r.Header.Get("x-device")
		ts := r.Header.Get("x-timestamp")
		imza := r.Header.Get("x-signature")
		govde, _ := io.ReadAll(r.Body)
		if cihaz != beklenenCihaz || imza != imzaHMAC(beklenenGizli, cihaz, ts, govde) {
			w.WriteHeader(401)
			_, _ = w.Write([]byte(`{"hata":"imza geçersiz"}`))
			return
		}
		gelenCihaz = cihaz
		w.Header().Set("content-type", "application/json")
		_, _ = w.Write([]byte(`{"yazilan":0}`))
	}))
	defer srv.Close()

	// Özel Python gönderici betiği ve ona işaret eden yabancı LaunchAgent plist'i
	betikYol := filepath.Join(betikDizin, "ozel_sender.py")
	betikIcerik := `#!/usr/bin/env python3
import urllib.request
DEVICE_ID = "` + beklenenCihaz + `"
SECRET = "` + beklenenGizli + `"
URL = "https://ornek-eski.workers.dev/api/gonder"
`
	if err := os.WriteFile(betikYol, []byte(betikIcerik), 0o700); err != nil {
		t.Fatal(err)
	}
	icBetik := filepath.Join(gecDizin, "gonderici_ozel.py")
	if err := os.WriteFile(icBetik, []byte("# ozel python\n"), 0o700); err != nil {
		t.Fatal(err)
	}
	yabanciPlist := filepath.Join(laDizin, "com.ornek.tokenpanosu.custom.plist")
	plistMetin := `<?xml version="1.0" encoding="UTF-8"?>
<plist version="1.0"><dict>
  <key>Label</key><string>com.ornek.tokenpanosu.custom</string>
  <key>ProgramArguments</key>
  <array><string>/usr/bin/python3</string><string>` + betikYol + `</string></array>
</dict></plist>`
	if err := os.WriteFile(yabanciPlist, []byte(plistMetin), 0o644); err != nil {
		t.Fatal(err)
	}

	// Test ikilisi kopyalanabilsin diye hedef oluştur
	_ = os.WriteFile(ikiliYolu(), []byte("sahte-ikili"), 0o755)

	if err := onarKomutu(bayraklar{url: srv.URL}); err != nil {
		t.Fatalf("onarKomutu başarısız: %v", err)
	}
	if gelenCihaz != beklenenCihaz {
		t.Fatalf("sunucuya gönderilen cihaz = %q, beklenen %q", gelenCihaz, beklenenCihaz)
	}
	if _, err := os.Stat(yabanciPlist); !os.IsNotExist(err) {
		t.Fatalf("yabancı LaunchAgent plist silinmedi: %v", err)
	}
	if _, err := os.Stat(icBetik); !os.IsNotExist(err) {
		t.Fatalf("klasör içindeki özel .py betik devre dışı bırakılmadı")
	}
	b, err := os.ReadFile(ayarYolu())
	if err != nil {
		t.Fatalf("ayar.json yazılamadı: %v", err)
	}
	var a Ayar
	if err := json.Unmarshal(b, &a); err != nil {
		t.Fatal(err)
	}
	if a.Cihaz != beklenenCihaz || a.URL != srv.URL {
		t.Fatalf("ayar = %+v", a)
	}
}

func TestTccGuvenliBetikYolu(t *testing.T) {
	sahteEv := t.TempDir()
	t.Setenv("HOME", sahteEv)
	t.Setenv("USERPROFILE", sahteEv)

	// TCC korumalı kullanıcı klasörleri her işletim sisteminde reddedilmeli
	tccYasak := []string{
		"~/Desktop/x.py",
		"~/Documents/x.py",
		"~/Downloads/x.py",
		"~/Pictures/x.py",
		"~/Movies/x.py",
		"~/Music/x.py",
		"~/Library/Mobile Documents/com~apple~CloudDocs/x.py",
		"~/Library/CloudStorage/GoogleDrive/x.py",
		filepath.Join(sahteEv, "Desktop", "x.py"),
		filepath.Join(sahteEv, "Documents", "alt", "x.py"),
	}
	for _, y := range tccYasak {
		if tccGuvenliBetikYoluMu(y) {
			t.Errorf("TCC korumalı yol reddedilmeliydi: %q", y)
		}
	}

	if runtime.GOOS == "darwin" {
		// macOS'ta /home (autofs), /Volumes, /net, /Network sözlüksel katmanda (/home'a hiç dokunmadan!) reddedilmeli
		agYasak := []string{
			"/home/foo/bar.py",
			"/System/Volumes/Data/home/foo/bar.py",
			"/Volumes/Share/x.py",
			"/net/sunucu/x.py",
			"/Network/Servers/x.py",
		}
		for _, y := range agYasak {
			if yerelYolMu(y) {
				t.Errorf("yerelYolMu ağ yolunu reddetmeliydi: %q", y)
			}
			if tccGuvenliBetikYoluMu(y) {
				t.Errorf("tccGuvenliBetikYoluMu ağ yolunu reddetmeliydi: %q", y)
			}
		}

		// Ağ diskine işaret eden yerel sembolik bağlantı da hedefe dokunmadan reddedilmeli
		gec := t.TempDir()
		bag := filepath.Join(gec, "ag_baglantisi.py")
		if err := os.Symlink("/Volumes/Share/uzak.py", bag); err == nil {
			if yerelYolMu(bag) || tccGuvenliBetikYoluMu(bag) {
				t.Fatalf("/Volumes hedefine işaret eden sembolik bağlantı reddedilmeliydi")
			}
		}
		masaustuBag := filepath.Join(gec, "masaustu_bag.py")
		if err := os.Symlink(filepath.Join(sahteEv, "Desktop", "gizli.py"), masaustuBag); err == nil {
			if tccGuvenliBetikYoluMu(masaustuBag) {
				t.Fatalf("Desktop hedefine işaret eden sembolik bağlantı reddedilmeliydi")
			}
		}
	}

	// Yerel güvenli geçici klasördeki betik kabul edilmeli
	guvenli := filepath.Join(t.TempDir(), "normal_gonder.py")
	_ = os.WriteFile(guvenli, []byte("# ok\n"), 0o600)
	if !tccGuvenliBetikYoluMu(guvenli) {
		t.Fatalf("yerel geçici betik yolu kabul edilmeliydi: %q", guvenli)
	}
}
