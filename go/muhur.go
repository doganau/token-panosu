package main

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"os/user"
	"path/filepath"
	"runtime"
	"strings"
)

// Resmî derleme sırasında araclar/derle.sh + araclar/imzala.py tarafından gömülen
// maskelenmiş mühür parçaları. Açık kaynak kodunda boştur; özel anahtar yalnızca
// yayıncının anahtar deposunda bulunur.
var (
	mParcaA string
	mParcaB string
	mParcaC string
	mParcaD string
)

func muhurEtkin() bool {
	return mParcaA != "" && mParcaB != "" && mParcaC != "" && mParcaD != ""
}

func muhurAnahtariCoz() ([]byte, error) {
	if !muhurEtkin() {
		return nil, errors.New("mühür etkin değil")
	}
	pa, err1 := base64.RawURLEncoding.DecodeString(mParcaA)
	pb, err2 := base64.RawURLEncoding.DecodeString(mParcaB)
	pc, err3 := base64.RawURLEncoding.DecodeString(mParcaC)
	pd, err4 := base64.RawURLEncoding.DecodeString(mParcaD)
	if err1 != nil || err2 != nil || err3 != nil || err4 != nil ||
		len(pa) != 16 || len(pb) != 16 || len(pc) != 16 || len(pd) != 16 {
		return nil, errors.New("mühür parçaları geçersiz")
	}
	pad := make([]byte, 32)
	copy(pad[:16], pa)
	copy(pad[16:], pc)

	sifreli := make([]byte, 32)
	copy(sifreli[:16], pb)
	copy(sifreli[16:], pd)

	h := sha256.New()
	h.Write([]byte("tp-acilim-v1|"))
	h.Write([]byte(hedefAdi()))
	h.Write([]byte("|"))
	h.Write(pad)
	maske := h.Sum(nil)

	anahtar := make([]byte, 32)
	for i := 0; i < 32; i++ {
		anahtar[i] = sifreli[i] ^ maske[i]
	}
	return anahtar, nil
}

func calisanIkiliSha256() (string, error) {
	var yol string
	if runtime.GOOS == "linux" {
		if p, err := os.Readlink("/proc/self/exe"); err == nil && p != "" {
			yol = strings.TrimSuffix(p, " (deleted)")
		}
	}
	if yol == "" {
		exe, err := os.Executable()
		if err != nil {
			return "", err
		}
		if g, err := filepath.EvalSymlinks(exe); err == nil && g != "" {
			yol = g
		} else {
			yol = exe
		}
	}
	f, err := os.Open(yol)
	if err != nil {
		return "", err
	}
	defer f.Close()
	oz := sha256.New()
	if _, err := io.Copy(oz, f); err != nil {
		return "", err
	}
	return hex.EncodeToString(oz.Sum(nil)), nil
}

func muhurHesapla(kHedef []byte, cihaz, gizliHex, ts string, surum int, hedef, exeHash, govdeHash string) string {
	gizliOn := ""
	if len(gizliHex) >= 16 {
		gizliOn = strings.ToLower(gizliHex[:16])
	}
	m := hmac.New(sha256.New, kHedef)
	fmt.Fprintf(m, "muhur-v1\n%s\n%s\n%d\n%s\n%s\n%s\n%s",
		cihaz, ts, surum, hedef, exeHash, govdeHash, gizliOn)
	return hex.EncodeToString(m.Sum(nil))
}

func muhurBasliklari(cihaz, gizliHex, ts string, govde []byte) map[string]string {
	kHedef, err := muhurAnahtariCoz()
	if err != nil {
		return nil
	}
	exeHash, err := calisanIkiliSha256()
	if err != nil || len(exeHash) != 64 {
		return nil
	}
	hedef := hedefAdi()
	govdeHash := imzaMetniSha256(govde)
	return map[string]string{
		"x-target":   hedef,
		"x-exe-hash": exeHash,
		"x-seal":     muhurHesapla(kHedef, cihaz, gizliHex, ts, Surum, hedef, exeHash, govdeHash),
	}
}

// gercekEvDizini: mühürlü resmî derlemede sahte HOME / USERPROFILE yönlendirmelerine
// aldanmadan işletim sistemindeki gerçek kullanıcı ev dizinini döndürür.
func gercekEvDizini() string {
	if !testModu && muhurEtkin() {
		if u, err := user.Current(); err == nil && u != nil && u.HomeDir != "" {
			return u.HomeDir
		}
	}
	if h, err := os.UserHomeDir(); err == nil && h != "" {
		return h
	}
	if u, err := user.Current(); err == nil && u != nil && u.HomeDir != "" {
		return u.HomeDir
	}
	return "."
}

func ortamButunlukBulgular() []Bulgu {
	if testModu {
		return nil
	}
	var sonuc []Bulgu
	ekle := func(kod, metin string) {
		for _, x := range sonuc {
			if x.K == kod {
				return
			}
		}
		sonuc = append(sonuc, Bulgu{K: kod, S: "s", T: metin})
	}
	if muhurEtkin() && os.Getenv("TOKEN_PANOSU_DIZIN") != "" {
		ekle("ortam_mudahale", "TOKEN_PANOSU_DIZIN ortam değişkeniyle gönderici çalışma dizini değiştirilmeye çalışıldı")
	}
	for _, k := range []string{"DYLD_INSERT_LIBRARIES", "DYLD_FORCE_FLAT_NAMESPACE", "LD_PRELOAD"} {
		if strings.TrimSpace(os.Getenv(k)) != "" {
			ekle("ortam_mudahale", k+" ayarlı: gönderici sürecine dış kütüphane enjekte edilmeye çalışıldı")
			break
		}
	}
	if u, err := user.Current(); err == nil && u != nil && u.HomeDir != "" {
		ortamEv := os.Getenv("HOME")
		if runtime.GOOS == "windows" {
			ortamEv = os.Getenv("USERPROFILE")
		}
		if ortamEv != "" && gercekYol(ortamEv) != gercekYol(u.HomeDir) {
			ekle("sahte_ev", "Kullanıcı ev dizini (HOME) gerçek hesaptan farklı bir klasöre yönlendirilmiş: kayıtlar gizleniyor olabilir")
		}
	}
	return sonuc
}
