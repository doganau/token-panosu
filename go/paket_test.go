package main

import (
	"crypto"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"runtime"
	"strings"
	"testing"
)

func TestPaketVeHedefKimligiSozlesmesi(t *testing.T) {
	kod := paketKodu()
	if !paketGecerliMi(kod) {
		t.Fatalf("paketKodu() geçersiz kod döndürdü: %q", kod)
	}
	if paketGecerliMi("") || paketGecerliMi("xyz") || paketGecerliMi("mc") {
		t.Fatalf("geçersiz paket kodları reddedilmeliydi")
	}

	taban := runtime.GOOS + "-" + runtime.GOARCH
	if got := hedefAdiPaket(""); got != taban {
		t.Fatalf("boş paket taban hedef döndürmeli: got=%q want=%q", got, taban)
	}
	if got := hedefAdiPaket("cgm"); got != taban {
		t.Fatalf("cgm paketi taban hedef döndürmeli: got=%q want=%q", got, taban)
	}
	if got := hedefAdiPaket("cm"); got != taban+"-cm" {
		t.Fatalf("cm paketi sonek almalı: got=%q want=%q", got, taban+"-cm")
	}
	if got := hedefAdiPaket("m"); got != taban+"-m" {
		t.Fatalf("m paketi sonek almalı: got=%q want=%q", got, taban+"-m")
	}
	if got := hedefAdi(); got != hedefAdiPaket(kod) {
		t.Fatalf("hedefAdi() = %q, beklenen %q", got, hedefAdiPaket(kod))
	}
}

func TestAyniSurumDinamikPaketDegisimi(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("kabuk ikilisi duman testi Unix üzerinde çalışır")
	}
	eskiDizin, eskiTestModu, eskiAcikN := dizin, testModu, acikNHex
	dizin = t.TempDir()
	testModu = true
	defer func() {
		dizin = eskiDizin
		testModu = eskiTestModu
		acikNHex = eskiAcikN
	}()

	ev := t.TempDir()
	t.Setenv("HOME", ev)
	t.Setenv("USERPROFILE", ev)

	// Mevcut paket "m" ise hedefi "c" seç, değilse "m" seç ki her derleme etiketinde farklı varyant test edilsin
	hedefPaket := "m"
	if paketKodu() == "m" {
		hedefPaket = "c"
	}
	beklenenHedef := hedefAdiPaket(hedefPaket)

	ozelAnahtar, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	acikNHex = fmt.Sprintf("%x", ozelAnahtar.PublicKey.N)

	sahteYeniIkili := []byte(fmt.Sprintf("#!/bin/sh\nif [ \"$1\" = \"surum\" ]; then echo %d; exit 0; fi\necho %s\n", Surum, hedefPaket))
	oz := sha256.Sum256(sahteYeniIkili)
	shaHex := hex.EncodeToString(oz[:])

	yuk := fmt.Sprintf("token-panosu-ikili-v1\n%d\n%s\n%s\n", Surum, beklenenHedef, shaHex)
	yukOz := sha256.Sum256([]byte(yuk))
	imzaBayt, err := rsa.SignPKCS1v15(rand.Reader, ozelAnahtar, crypto.SHA256, yukOz[:])
	if err != nil {
		t.Fatal(err)
	}
	imzaHex := hex.EncodeToString(imzaBayt)
	dosyaAdi := indirmeAdi(beklenenHedef)

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/guncelle/manifest.json":
			m := map[string]any{
				"ikili": map[string]any{
					"surum": Surum, // Aynı sürüm (40 == 40), yalnızca paket değişti!
					"hedefler": map[string]any{
						beklenenHedef: map[string]string{
							"dosya":  dosyaAdi,
							"sha256": shaHex,
							"imza":   imzaHex,
						},
					},
				},
			}
			_ = json.NewEncoder(w).Encode(m)
		case "/indir/" + dosyaAdi:
			_, _ = w.Write(sahteYeniIkili)
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()

	_ = os.WriteFile(ikiliYolu(), []byte("#!/bin/sh\necho eski\n"), 0o755)
	if err := ayarYaz(Ayar{
		URL:        srv.URL,
		Cihaz:      strings.Repeat("b", 32),
		Etiket:     "paket-test",
		HedefPaket: hedefPaket,
	}); err != nil {
		t.Fatal(err)
	}

	oldu, err := guncelle()
	if err != nil || !oldu {
		t.Fatalf("aynı sürümde farklı hedef paket ikili değişimini tetiklemeliydi: oldu=%v err=%v", oldu, err)
	}
	yerlesen, err := os.ReadFile(ikiliYolu())
	if err != nil || string(yerlesen) != string(sahteYeniIkili) {
		t.Fatalf("yeni varyant ikilisi hedef konuma yazılmadı")
	}
}
