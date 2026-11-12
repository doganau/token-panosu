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

var (
	httpIstemci  = &http.Client{Timeout: 20 * time.Second}
	indirIstemci = &http.Client{Timeout: 120 * time.Second}
)

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
	istemci := httpIstemci
	if sinir > 1<<20 {
		istemci = indirIstemci
	}
	res, err := istemci.Do(req)
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
func imzaMetniSha256(b []byte) string {
	oz := sha256.Sum256(b)
	return hex.EncodeToString(oz[:])
}

func imzaHMAC(gizliHex, cihaz, ts string, govde []byte) string {
	anahtar, _ := hex.DecodeString(gizliHex)
	m := hmac.New(sha256.New, anahtar)
	fmt.Fprintf(m, "v1\n%s\n%s\n%s", cihaz, ts, imzaMetniSha256(govde))
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

func hedefAdi() string { return hedefAdiPaket(paketKodu()) }

func indirmeAdi(hedef string) string {
	if strings.HasPrefix(hedef, "windows-") {
		// token-panosu-windows-amd64-cm.exe biçimi için uzantı en sona eklenir
		return "token-panosu-" + hedef + ".exe"
	}
	return "token-panosu-" + hedef
}

type manifestGirdi struct {
	Dosya  string `json:"dosya"`
	Sha256 string `json:"sha256"`
	Imza   string `json:"imza"`
}

type manifest struct {
	Ikili *struct {
		Surum    int                      `json:"surum"`
		Dosyalar map[string]manifestGirdi `json:"dosyalar"`
		Hedefler map[string]manifestGirdi `json:"hedefler"`
	} `json:"ikili"`
}

// Sunucudaki imzalı sürüm daha yeniyse veya sunucunun bildirdiği sağlayıcı paketi mevcut
// ikiliden farklıysa ilgili izole varyantı indirir, RSA-3072 + SHA-256 + "surum" denetiminden
// geçirir, kaldırılan sağlayıcının kancalarını temizler ve programı atomik olarak değiştirir.
func guncelle() (bool, error) {
	if acikNHex == "" || acikNHex == "devre-disi" {
		return false, nil
	}
	a, err := ayarOku()
	if err != nil {
		return false, err
	}
	b, err := al(a.URL+"/guncelle/manifest.json", 256<<10)
	if err != nil {
		return false, err
	}
	var m manifest
	if err := json.Unmarshal(b, &m); err != nil {
		return false, errors.New("manifest biçimi geçersiz")
	}
	if m.Ikili == nil {
		return false, nil
	}
	mevcutHedef := hedefAdi()
	istenenPaket := paketKodu()
	if hp := paketTemizle(a.HedefPaket); hp != "" {
		istenenPaket = hp
	}
	istenenHedef := hedefAdiPaket(istenenPaket)
	ayniSurumPaketDegisimi := m.Ikili.Surum == Surum && istenenHedef != mevcutHedef
	if m.Ikili.Surum < Surum || (m.Ikili.Surum == Surum && !ayniSurumPaketDegisimi) {
		return false, nil
	}

	harita := make(map[string]manifestGirdi, len(m.Ikili.Dosyalar)+len(m.Ikili.Hedefler))
	for k, v := range m.Ikili.Dosyalar {
		harita[k] = v
	}
	for k, v := range m.Ikili.Hedefler {
		harita[k] = v
	}

	taban := runtime.GOOS + "-" + runtime.GOARCH
	secilenHedef := istenenHedef
	g, ok := harita[secilenHedef]
	if !ok && secilenHedef != taban {
		// Yalnızca sunucu manifestinde hiçbir varyant derlemesi yoksa (eski OSS sunucusu) taban hedefe düş
		cmGirdi, cmVar := harita[taban+"-cm"]
		varyantYok := !cmVar || (cmGirdi.Dosya == "" && cmGirdi.Sha256 == "")
		if varyantYok {
			if ayniSurumPaketDegisimi && mevcutHedef == taban {
				return false, nil
			}
			if tg, tok := harita[taban]; tok && !ayniSurumPaketDegisimi {
				secilenHedef = taban
				g = tg
				ok = true
				istenenPaket = "cgm"
			}
		}
	}
	if !ok || g.Sha256 == "" || g.Imza == "" {
		return false, fmt.Errorf("%s için yeni sürüm yok", istenenHedef)
	}
	yuk := fmt.Sprintf("token-panosu-ikili-v1\n%d\n%s\n%s\n", m.Ikili.Surum, secilenHedef, g.Sha256)
	if !imzaDogru([]byte(yuk), g.Imza) {
		return false, errors.New("güncelleme imzası geçersiz, reddedildi")
	}
	dosyaAdi := strings.TrimSpace(g.Dosya)
	if dosyaAdi == "" || strings.ContainsAny(dosyaAdi, `/\`) || strings.Contains(dosyaAdi, "..") {
		dosyaAdi = indirmeAdi(secilenHedef)
	}
	ikili, err := al(a.URL+"/indir/"+dosyaAdi, 40<<20)
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
	// Bir sağlayıcı devre dışı bırakılıyorsa, yeni izole ikiliye geçmeden önce kancalarını temizle
	if sagClaudeEtkin && !strings.Contains(istenenPaket, "c") {
		durumSatiriKaldir()
		uyariKaldir()
	}
	if sagGeminiEtkin && !strings.Contains(istenenPaket, "m") {
		geminiKancaKaldir()
	}
	if err := yerineKoy(gec, hedefYol); err != nil {
		os.Remove(gec)
		return false, err
	}
	logYaz("güncellendi: sürüm %d (%s) -> %d (%s)", Surum, mevcutHedef, m.Ikili.Surum, secilenHedef)
	return true, nil
}

// Pencere açmadan alt süreç (Windows'ta gizli)
func komut(ctx context.Context, ad string, arg ...string) *exec.Cmd {
	c := exec.CommandContext(ctx, ad, arg...)
	gizliPencere(c)
	return c
}

func eskiyiTemizle() { _ = os.Remove(ikiliYolu() + ".eski") }
