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
import hmac
import json
import os
import re
import secrets
import shutil
import subprocess
import sys
import tempfile

KOK = os.path.dirname(os.path.dirname(os.path.abspath(__file__)))
INDIR = os.path.join(KOK, "dist", "indir")
ANA = os.path.join(KOK, "go", "main.go")
MANIFEST = os.path.join(KOK, "istemci", "manifest.json")
YEREL_PEM = os.path.join(KOK, ".imza-anahtari.pem")
YEREL_MUHUR = os.path.join(KOK, ".muhur-anahtari.key")
SERVIS, HESAP = "token-panosu-imza", "ozel-anahtar"
MUHUR_SERVIS, MUHUR_HESAP = "token-panosu-muhur", "muhur-anahtari"

TEMEL_HEDEFLER = [
    ("darwin", "arm64", ""),
    ("darwin", "amd64", ""),
    ("linux", "amd64", ""),
    ("linux", "arm64", ""),
    ("windows", "amd64", ".exe"),
    ("windows", "arm64", ".exe"),
]
PAKET_VARYANTLARI = ["", "-c", "-g", "-m", "-cg", "-cm", "-gm"]
HEDEF_DOSYALAR = {
    f"{sis}-{mim}{ek}": f"token-panosu-{sis}-{mim}{ek}{uzanti}"
    for sis, mim, uzanti in TEMEL_HEDEFLER
    for ek in PAKET_VARYANTLARI
}
TEMEL_HEDEF_ANAHTARLARI = [f"{sis}-{mim}" for sis, mim, _ in TEMEL_HEDEFLER]


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


def muhur_oku_veya_uret(olustur=True):
    ortam = os.environ.get("TOKEN_PANOSU_MUHUR_HEX", "").strip().lower()
    if re.fullmatch(r"[0-9a-f]{64}", ortam):
        return ortam
    if os.path.isfile(YEREL_MUHUR):
        deger = open(YEREL_MUHUR, "r", encoding="utf-8").read().strip().lower()
        if re.fullmatch(r"[0-9a-f]{64}", deger):
            return deger
    if keychain_var_mi():
        r = sh("security", "find-generic-password", "-s", MUHUR_SERVIS, "-a", MUHUR_HESAP, "-w", text=True)
        deger = r.stdout.strip().lower() if r.returncode == 0 else ""
        if re.fullmatch(r"[0-9a-f]{64}", deger):
            return deger
    if not olustur:
        return ""
    yeni = secrets.token_hex(32)
    if keychain_var_mi():
        r = sh("security", "add-generic-password", "-U", "-s", MUHUR_SERVIS, "-a", MUHUR_HESAP, "-w", yeni)
        if r.returncode == 0:
            return yeni
    fd = os.open(YEREL_MUHUR, os.O_WRONLY | os.O_CREAT | os.O_TRUNC, 0o600)
    with os.fdopen(fd, "w", encoding="utf-8") as f:
        f.write(yeni + "\n")
    return yeni


def muhur_anahtar():
    # Yalnızca imzalı kurulumlarda (imza anahtarı varsa) otomatik mühür anahtarı üretir
    if not anahtar_oku(sessiz=True) and not os.environ.get("TOKEN_PANOSU_MUHUR_HEX") and not os.path.isfile(YEREL_MUHUR):
        sys.exit(1)
    deger = muhur_oku_veya_uret(olustur=True)
    if not deger:
        sys.exit(1)
    print(deger)


def _b64u(b):
    return base64.urlsafe_b64encode(b).decode("ascii").rstrip("=")


def muhur_bayrak():
    if len(sys.argv) < 4:
        sys.exit("kullanım: imzala.py muhur-bayrak <sürüm> <hedef>")
    surum, hedef = sys.argv[2].strip(), sys.argv[3].strip()
    if hedef not in HEDEF_DOSYALAR:
        sys.exit(f"geçersiz hedef: {hedef}")
    if not anahtar_oku(sessiz=True) and not os.environ.get("TOKEN_PANOSU_MUHUR_HEX") and not os.path.isfile(YEREL_MUHUR):
        return
    ana = muhur_oku_veya_uret(olustur=True)
    if not ana:
        return
    k_hedef = hmac.new(bytes.fromhex(ana), f"tp-muhur-k1|{surum}|{hedef}".encode("utf-8"), hashlib.sha256).digest()
    pad = os.urandom(32)
    maske = hashlib.sha256(b"tp-acilim-v1|" + hedef.encode("utf-8") + b"|" + pad).digest()
    sifreli = bytes(a ^ b for a, b in zip(k_hedef, maske))
    p_a, p_b = _b64u(pad[:16]), _b64u(sifreli[:16])
    p_c, p_d = _b64u(pad[16:]), _b64u(sifreli[16:])
    print(f"-X main.mParcaA={p_a} -X main.mParcaB={p_b} -X main.mParcaC={p_c} -X main.mParcaD={p_d}")


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
    mevcut_dosyalar = {f for f in os.listdir(INDIR) if f.startswith("token-panosu-") and not f.endswith(".txt")}
    if not mevcut_dosyalar:
        sys.exit("dist/indir boş: önce sh araclar/derle.sh")
    bilinen_dosyalar = set(HEDEF_DOSYALAR.values())
    bilinmeyen = sorted(mevcut_dosyalar - bilinen_dosyalar)
    if bilinmeyen:
        sys.exit("dist/indir içinde tanınmayan hedef ikilisi: " + ", ".join(bilinmeyen))
    eksik_temel = [h for h in TEMEL_HEDEF_ANAHTARLARI if HEDEF_DOSYALAR[h] not in mevcut_dosyalar]
    if eksik_temel:
        sys.exit("dist/indir içinde eksik temel hedef ikilileri: " + ", ".join(eksik_temel))
    hedef_ciftleri = [(h, ad) for h, ad in sorted(HEDEF_DOSYALAR.items()) if ad in mevcut_dosyalar]
    sonuc = {}
    with tempfile.TemporaryDirectory() as d:
        os.chmod(d, 0o700)
        pem, pub = os.path.join(d, "k.pem"), os.path.join(d, "a.pem")
        fd = os.open(pem, os.O_WRONLY | os.O_CREAT, 0o600)
        with os.fdopen(fd, "wb") as f:
            f.write(anahtar_oku())
        if sh("openssl", "rsa", "-in", pem, "-pubout", "-out", pub).returncode:
            sys.exit("açık anahtar çıkarılamadı")
        for hedef, ad in hedef_ciftleri:
            veri = open(os.path.join(INDIR, ad), "rb").read()
            ozet = hashlib.sha256(veri).hexdigest()
            sha_dosya = os.path.join(INDIR, ad + ".sha256.txt")
            if not os.path.isfile(sha_dosya) or open(sha_dosya).read().strip() != ozet:
                sys.exit(ad + ": .sha256.txt uyuşmuyor veya eksik, yeniden derle")
            yuk = ("token-panosu-ikili-v1\n%d\n%s\n%s\n" % (surum, hedef, ozet)).encode()
            p, s = os.path.join(d, "p.bin"), os.path.join(d, "s.bin")
            open(p, "wb").write(yuk)
            if sh("openssl", "dgst", "-sha256", "-sign", pem, "-out", s, p).returncode:
                sys.exit("imzalanamadı: " + ad)
            if sh("openssl", "dgst", "-sha256", "-verify", pub, "-signature", s, p).returncode:
                sys.exit("imza doğrulanamadı: " + ad)
            sonuc[hedef] = {"sha256": ozet, "imza": open(s, "rb").read().hex()}
    gecmis = {}
    if os.path.isfile(MANIFEST):
        try:
            eski = json.load(open(MANIFEST, encoding="utf-8"))
            if isinstance(eski.get("gecmis"), dict):
                for ks, kd in eski["gecmis"].items():
                    if str(ks).isdigit() and int(ks) >= 31 and int(ks) != surum and isinstance(kd, dict):
                        gecmis[str(ks)] = kd
            eski_ikili = eski.get("ikili")
            if isinstance(eski_ikili, dict) and isinstance(eski_ikili.get("surum"), int):
                es = eski_ikili["surum"]
                if es >= 31 and es != surum and isinstance(eski_ikili.get("dosyalar"), dict):
                    gecmis[str(es)] = {
                        h: d["sha256"]
                        for h, d in eski_ikili["dosyalar"].items()
                        if isinstance(d, dict) and isinstance(d.get("sha256"), str)
                    }
        except Exception:
            pass
    # Geçiş uyumluluğu için v31+ dahil en son 10 mühürlü sürümün özet geçmişini koru
    if len(gecmis) > 10:
        for ks in sorted(gecmis.keys(), key=lambda x: int(x))[:-10]:
            gecmis.pop(ks, None)
    cikti = {"ikili": {"surum": surum, "dosyalar": sonuc}}
    if gecmis:
        cikti["gecmis"] = gecmis
    json.dump(cikti, open(MANIFEST, "w"), indent=1)
    print("İmzalandı: sürüm %d, %d dosya (%s)" % (surum, len(sonuc), ", ".join(sorted(sonuc))))


if __name__ == "__main__":
    komutlar = {
        "anahtar-uret": anahtar_uret,
        "acik-anahtar": acik_anahtar,
        "muhur-anahtar": muhur_anahtar,
        "muhur-bayrak": muhur_bayrak,
        "imzala": imzala,
        "imzasiz": imzasiz,
    }
    komutlar.get(sys.argv[1] if len(sys.argv) > 1 else "", lambda: print(__doc__))()
