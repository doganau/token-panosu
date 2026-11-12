package main

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"os"
	"path/filepath"
	"testing"
)

func TestMuhurCozumlemeVeTekBaytDegisimiAlgilama(t *testing.T) {
	eskiA, eskiB, eskiC, eskiD := mParcaA, mParcaB, mParcaC, mParcaD
	defer func() {
		mParcaA, mParcaB, mParcaC, mParcaD = eskiA, eskiB, eskiC, eskiD
	}()

	anaHex := "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"
	anaBayt, _ := hex.DecodeString(anaHex)
	hedef := hedefAdi()

	mac := hmac.New(sha256.New, anaBayt)
	mac.Write([]byte("tp-muhur-k1|40|" + hedef))
	beklenenKHedef := mac.Sum(nil)

	pad := []byte("0123456789abcdefFEDCBA9876543210")
	mh := sha256.New()
	mh.Write([]byte("tp-acilim-v1|" + hedef + "|"))
	mh.Write(pad)
	maske := mh.Sum(nil)

	sifreli := make([]byte, 32)
	for i := 0; i < 32; i++ {
		sifreli[i] = beklenenKHedef[i] ^ maske[i]
	}
	mParcaA = base64.RawURLEncoding.EncodeToString(pad[:16])
	mParcaB = base64.RawURLEncoding.EncodeToString(sifreli[:16])
	mParcaC = base64.RawURLEncoding.EncodeToString(pad[16:])
	mParcaD = base64.RawURLEncoding.EncodeToString(sifreli[16:])

	cozulen, err := muhurAnahtariCoz()
	if err != nil {
		t.Fatalf("muhurAnahtariCoz başarısız: %v", err)
	}
	if hex.EncodeToString(cozulen) != hex.EncodeToString(beklenenKHedef) {
		t.Fatalf("çözülen anahtar eşleşmiyor")
	}

	baslik := muhurBasliklari(
		"11112222333344445555666677778888",
		"aaaa1111bbbb2222cccc3333dddd4444eeee5555ffff66660000777788889999",
		"1700000000",
		[]byte(`{"v":2,"rows":[]}`),
	)
	if baslik == nil || len(baslik["x-seal"]) != 64 || len(baslik["x-exe-hash"]) != 64 || baslik["x-target"] != hedef {
		t.Fatalf("eksik mühür başlıkları: %+v", baslik)
	}

	// Tek baytlık ikili özeti değişiminde mührün tamamen değiştiğini doğrula
	sahteExeHash := "f" + baslik["x-exe-hash"][1:]
	if sahteExeHash == baslik["x-exe-hash"] {
		sahteExeHash = "0" + baslik["x-exe-hash"][1:]
	}
	sahteMuhur := muhurHesapla(
		cozulen,
		"11112222333344445555666677778888",
		"aaaa1111bbbb2222cccc3333dddd4444eeee5555ffff66660000777788889999",
		"1700000000",
		Surum,
		hedef,
		sahteExeHash,
		imzaMetniSha256([]byte(`{"v":2,"rows":[]}`)),
	)
	if sahteMuhur == baslik["x-seal"] {
		t.Fatalf("ikili özetindeki tek karakter değişimi mührü değiştirmedi!")
	}
}

func TestSatirdanBetikYollariKabukSarmalayiciCikarir(t *testing.T) {
	gec := t.TempDir()
	betik := filepath.Join(gec, "gizli_gonder.py")
	if err := os.WriteFile(betik, []byte("# test\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	komut := `/bin/sh -c "/usr/bin/python3 ` + betik + ` --sessiz >> /tmp/out.log 2>&1"`
	bulunan := satirdanBetikYollariCikar(komut)
	if len(bulunan) != 1 || bulunan[0] != betik {
		t.Fatalf("beklenen [%s], bulunan %+v", betik, bulunan)
	}
}
