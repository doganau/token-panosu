package main

import (
	"bytes"
	"context"
	"crypto"
	"crypto/hmac"
	"crypto/rsa"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math/big"
	"net/http"
	"os"
	"os/exec"
	"runtime"
	"strconv"
	"strings"
	"time"
)

// Güncellemeleri doğrulayan açık anahtar (RSA-3072, e=65537). Özel anahtar yalnızca
// geliştiricinin anahtar deposundadır; sunucu ele geçirilse bile bu anahtarla
// doğrulanamayan program bilgisayarlara kurulmaz.
// Kendi kurulumunuzda -ldflags "-X main.acikNHex=..." ile gömülür (araclar/derle.sh otomatik okur).
var acikNHex = "devre-disi"

var httpIstemci = &http.Client{Timeout: 20 * time.Second}

func kullaniciAjani() string { return fmt.Sprintf("token-panosu/%d (%s)", Surum, runtime.GOOS) }

func postJSON(adres string, govde []byte, basliklar map[string]string) (int, []byte, error) {
	req, err := http.NewRequest("POST", adres, bytes.NewReader(govde))
	if err != nil {
		return 0, nil, err
	}
	req.Header.Set("content-type", "application/json")
	req.Header.Set("user-agent", kullaniciAjani())
	for k, v := range basliklar {
		req.Header.Set(k, v)
	}
	res, err := httpIstemci.Do(req)
	if err != nil {
		return 0, nil, err
	}
	defer res.Body.Close()
	b, _ := io.ReadAll(io.LimitReader(res.Body, 1<<20))
	return res.StatusCode, b, nil
}

func al(adres string, sinir int64) ([]byte, error) {
	req, err := http.NewRequest("GET", adres, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("user-agent", kullaniciAjani())
	res, err := httpIstemci.Do(req)
	if err != nil {
		return nil, err
	}
	defer res.Body.Close()
	if res.StatusCode != 200 {
		return nil, fmt.Errorf("%s: HTTP %d", adres, res.StatusCode)
	}
	b, err := io.ReadAll(io.LimitReader(res.Body, sinir+1))
	if err != nil {
		return nil, err
	}
	if int64(len(b)) > sinir {
		return nil, errors.New("yanıt çok büyük")
	}
	return b, nil
}

// İmza: HMAC-SHA256(gizli, "v1\n<cihaz>\n<zaman>\n<sha256(gövde)>")
func imzaHMAC(gizliHex, cihaz, ts string, govde []byte) string {
	anahtar, _ := hex.DecodeString(gizliHex)
	oz := sha256.Sum256(govde)
	m := hmac.New(sha256.New, anahtar)
	fmt.Fprintf(m, "v1\n%s\n%s\n%s", cihaz, ts, hex.EncodeToString(oz[:]))
	return hex.EncodeToString(m.Sum(nil))
}

func imzaDogru(yuk []byte, imzaHex string) bool {
	n, ok := new(big.Int).SetString(acikNHex, 16)
	if !ok {
		return false
	}
	imza, err := hex.DecodeString(imzaHex)
	if err != nil {
		return false
	}
	oz := sha256.Sum256(yuk)
	return rsa.VerifyPKCS1v15(&rsa.PublicKey{N: n, E: 65537}, crypto.SHA256, oz[:], imza) == nil
}

func hedefAdi() string { return runtime.GOOS + "-" + runtime.GOARCH }

func indirmeAdi(hedef string) string {
	ad := "token-panosu-" + hedef
	if strings.HasPrefix(hedef, "windows-") {
		ad += ".exe"
	}
	return ad
}

type manifest struct {
	Ikili *struct {
		Surum    int `json:"surum"`
		Dosyalar map[string]struct {
			Sha256 string `json:"sha256"`
			Imza   string `json:"imza"`
		} `json:"dosyalar"`
	} `json:"ikili"`
}

// Sunucudaki imzalı sürüm daha yeniyse indirir, doğrular ve programı değiştirir.
// Eski ya da aynı sürüme asla geçmez; imzası, özeti ya da sürümü tutmayan dosyayı reddeder.
func guncelle() (bool, error) {
	if acikNHex == "" || acikNHex == "devre-disi" {
		return false, nil
	}
	a, err := ayarOku()
	if err != nil {
		return false, err
	}
	b, err := al(a.URL+"/guncelle/manifest.json", 64<<10)
	if err != nil {
		return false, err
	}
	var m manifest
	if err := json.Unmarshal(b, &m); err != nil {
		return false, errors.New("manifest biçimi geçersiz")
	}
	if m.Ikili == nil || m.Ikili.Surum <= Surum {
		return false, nil
	}
	hedef := hedefAdi()
	g, ok := m.Ikili.Dosyalar[hedef]
	if !ok {
		return false, fmt.Errorf("%s için yeni sürüm yok", hedef)
	}
	yuk := fmt.Sprintf("token-panosu-ikili-v1\n%d\n%s\n%s\n", m.Ikili.Surum, hedef, g.Sha256)
	if !imzaDogru([]byte(yuk), g.Imza) {
		return false, errors.New("güncelleme imzası geçersiz, reddedildi")
	}
	ikili, err := al(a.URL+"/indir/"+indirmeAdi(hedef), 40<<20)
	if err != nil {
		return false, err
	}
	oz := sha256.Sum256(ikili)
	if hex.EncodeToString(oz[:]) != g.Sha256 {
		return false, errors.New("indirilen dosyanın özeti imzalı özetle uyuşmuyor, reddedildi")
	}
	hedefYol := ikiliYolu()
	gec := hedefYol + ".yeni"
	if err := os.WriteFile(gec, ikili, 0o755); err != nil {
		return false, err
	}
	// Yeni program gerçekten çalışıyor ve duyurulan sürümü bildiriyor mu?
	ctx, iptal := context.WithTimeout(context.Background(), 15*time.Second)
	defer iptal()
	cikti, err := komut(ctx, gec, "surum").Output()
	if err != nil || strings.TrimSpace(string(cikti)) != strconv.Itoa(m.Ikili.Surum) {
		os.Remove(gec)
		return false, errors.New("yeni sürüm denetimden geçmedi, reddedildi")
	}
	if err := yerineKoy(gec, hedefYol); err != nil {
		os.Remove(gec)
		return false, err
	}
	logYaz("güncellendi: sürüm %d -> %d", Surum, m.Ikili.Surum)
	return true, nil
}

// Pencere açmadan alt süreç (Windows'ta gizli)
func komut(ctx context.Context, ad string, arg ...string) *exec.Cmd {
	c := exec.CommandContext(ctx, ad, arg...)
	gizliPencere(c)
	return c
}

func eskiyiTemizle() { _ = os.Remove(ikiliYolu() + ".eski") }
