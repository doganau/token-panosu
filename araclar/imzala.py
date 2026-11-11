#!/usr/bin/env python3
"""Uygulama sürümlerini imzalar (macOS, Linux, Windows ve CI uyumlu).

  python3 araclar/imzala.py anahtar-uret   # bir kez: RSA-3072 anahtar çifti üretir
  python3 araclar/imzala.py acik-anahtar   # mevcut özel anahtarın açık modülünü (acikNHex) yazdırır
  python3 araclar/imzala.py imzala         # dist/indir altındaki ikilileri imzalar, istemci/manifest.json yazar
  python3 araclar/imzala.py imzasiz        # imzasız kurulumlar için boş güncelleme manifestosu yazar

Özel anahtar arama sırası:
  1. TOKEN_PANOSU_IMZA_PEM ortam değişkeni (CI / GitHub Actions için)
  2. Proje kökündeki .imza-anahtari.pem dosyası (0600 izinli, git tarafından yok sayılır)
  3. macOS Anahtar Zinciri (servis: token-panosu-imza)
"""
import base64
import hashlib
import json
import os
import re
import shutil
import subprocess
import sys
import tempfile

KOK = os.path.dirname(os.path.dirname(os.path.abspath(__file__)))
INDIR = os.path.join(KOK, "dist", "indir")
ANA = os.path.join(KOK, "go", "main.go")
MANIFEST = os.path.join(KOK, "istemci", "manifest.json")
YEREL_PEM = os.path.join(KOK, ".imza-anahtari.pem")
SERVIS, HESAP = "token-panosu-imza", "ozel-anahtar"


def sh(*a, **k):
    return subprocess.run(a, capture_output=True, **k)


def keychain_var_mi():
    return sys.platform == "darwin" and shutil.which("security") is not None


def anahtar_oku(sessiz=False):
    ortam = os.environ.get("TOKEN_PANOSU_IMZA_PEM", "").strip()
    if ortam:
        return ortam.encode("utf-8") if "BEGIN" in ortam else base64.b64decode(ortam)
    if os.path.isfile(YEREL_PEM):
        return open(YEREL_PEM, "rb").read()
    if keychain_var_mi():
        r = sh("security", "find-generic-password", "-s", SERVIS, "-a", HESAP, "-w", text=True)
        if r.returncode == 0 and r.stdout.strip():
            return base64.b64decode(r.stdout.strip())
    if sessiz:
        return None
    sys.exit(
        "Özel imza anahtarı bulunamadı.\n"
        "Yeni kurulum yapıyorsanız bir kez çalıştırın: python3 araclar/imzala.py anahtar-uret"
    )


def modulus_hesapla(pem_bytes):
    with tempfile.TemporaryDirectory() as d:
        os.chmod(d, 0o700)
        pem = os.path.join(d, "k.pem")
        fd = os.open(pem, os.O_WRONLY | os.O_CREAT, 0o600)
        with os.fdopen(fd, "wb") as f:
            f.write(pem_bytes)
        r = sh("openssl", "rsa", "-in", pem, "-noout", "-modulus", text=True)
        if r.returncode != 0 or "=" not in r.stdout:
            sys.exit("Açık anahtar modülü okunamadı: " + r.stderr)
        return r.stdout.strip().split("=", 1)[1].lower()


def acik_anahtar():
    veri = anahtar_oku(sessiz=True)
    if not veri:
        sys.exit(1)
    print(modulus_hesapla(veri))


def anahtar_uret():
    dosyaya = "--dosya" in sys.argv or not keychain_var_mi()
    if os.path.isfile(YEREL_PEM):
        sys.exit(f"Anahtar zaten var: {YEREL_PEM}")
    if not dosyaya and keychain_var_mi():
        if sh("security", "find-generic-password", "-s", SERVIS, "-a", HESAP).returncode == 0:
            sys.exit("Anahtar zaten Anahtar Zinciri'nde var. Üzerine yazmak kurulu cihazları kopartır.")
    with tempfile.TemporaryDirectory() as d:
        os.chmod(d, 0o700)
        pem = os.path.join(d, "k.pem")
        r = sh("openssl", "genrsa", "-out", pem, "3072")
        if r.returncode:
            sys.exit(r.stderr.decode())
        veri = open(pem, "rb").read()
    mod = modulus_hesapla(veri)
    if not dosyaya and keychain_var_mi():
        r = sh("security", "add-generic-password", "-s", SERVIS, "-a", HESAP, "-w", base64.b64encode(veri).decode())
        if r.returncode == 0:
            print("Anahtar macOS Anahtar Zinciri'ne kaydedildi.")
            print("Açık anahtar (acikNHex):", mod)
            return
    fd = os.open(YEREL_PEM, os.O_WRONLY | os.O_CREAT | os.O_EXCL, 0o600)
    with os.fdopen(fd, "wb") as f:
        f.write(veri)
    print(f"Anahtar üretildi ve güvenli izinlerle kaydedildi: {YEREL_PEM}")
    print("Açık anahtar (acikNHex):", mod)


def imzasiz():
    json.dump({"ikili": None}, open(MANIFEST, "w"), indent=1)
    print("İmzasız kurulum manifestosu yazıldı (otomatik ikili güncelleme devre dışı).")


def imzala():
    m = re.search(r"^\s*Surum\s*=\s*(\d+)", open(ANA, encoding="utf-8").read(), re.M)
    if not m:
        sys.exit("go/main.go içinde Surum bulunamadı")
    surum = int(m.group(1))
    if not os.path.isdir(INDIR):
        sys.exit("dist/indir bulunamadı: önce sh araclar/derle.sh")
    dosyalar = sorted(f for f in os.listdir(INDIR) if f.startswith("token-panosu-") and not f.endswith(".txt"))
    if not dosyalar:
        sys.exit("dist/indir boş: önce sh araclar/derle.sh")
    sonuc = {}
    with tempfile.TemporaryDirectory() as d:
        os.chmod(d, 0o700)
        pem, pub = os.path.join(d, "k.pem"), os.path.join(d, "a.pem")
        fd = os.open(pem, os.O_WRONLY | os.O_CREAT, 0o600)
        with os.fdopen(fd, "wb") as f:
            f.write(anahtar_oku())
        if sh("openssl", "rsa", "-in", pem, "-pubout", "-out", pub).returncode:
            sys.exit("açık anahtar çıkarılamadı")
        for ad in dosyalar:
            hedef = ad[len("token-panosu-"):].removesuffix(".exe")
            veri = open(os.path.join(INDIR, ad), "rb").read()
            ozet = hashlib.sha256(veri).hexdigest()
            if open(os.path.join(INDIR, ad + ".sha256.txt")).read().strip() != ozet:
                sys.exit(ad + ": .sha256.txt uyuşmuyor, yeniden derle")
            yuk = ("token-panosu-ikili-v1\n%d\n%s\n%s\n" % (surum, hedef, ozet)).encode()
            p, s = os.path.join(d, "p.bin"), os.path.join(d, "s.bin")
            open(p, "wb").write(yuk)
            if sh("openssl", "dgst", "-sha256", "-sign", pem, "-out", s, p).returncode:
                sys.exit("imzalanamadı: " + ad)
            if sh("openssl", "dgst", "-sha256", "-verify", pub, "-signature", s, p).returncode:
                sys.exit("imza doğrulanamadı: " + ad)
            sonuc[hedef] = {"sha256": ozet, "imza": open(s, "rb").read().hex()}
    json.dump({"ikili": {"surum": surum, "dosyalar": sonuc}}, open(MANIFEST, "w"), indent=1)
    print("İmzalandı: sürüm %d, %d dosya (%s)" % (surum, len(sonuc), ", ".join(sorted(sonuc))))


if __name__ == "__main__":
    komutlar = {
        "anahtar-uret": anahtar_uret,
        "acik-anahtar": acik_anahtar,
        "imzala": imzala,
        "imzasiz": imzasiz,
    }
    komutlar.get(sys.argv[1] if len(sys.argv) > 1 else "", lambda: print(__doc__))()
