// Token Panosu: Claude Code kullanımını bilgisayar bazında toplayan Worker.
//
// Güvenlik modeli
//  - Her bilgisayar eşleşirken kendi 256-bit gizli anahtarını üretir; anahtar programın içinde yoktur.
//  - Her gönderim HMAC-SHA256 ile imzalanır: v1\n<cihaz>\n<zaman>\n<gövde sha256>
//  - Zaman damgası ±5 dk içinde ve bir öncekinden büyük olmalı (tekrar gönderim engeli).
//  - Eşleşme kodu yalnızca girişli kullanıcı ya da yönetici anahtarıyla üretilir; tek kullanımlık, 15 dk.
//  - Pano girişi: şifre (VIEW_KEY) -> HttpOnly + Secure + SameSite=Strict oturum çerezi (imzalı, 30 gün).
//    Başarısız denemeler IP başına 15 dk'da 10 ile sınırlı.
//
// Zaman modeli
//  - Veriler saatlik (UTC saat başı, epoch saniye) saklanır.
//  - Hafta Cumartesi 09:00 Türkiye (UTC+3) = Cumartesi 06:00 UTC başlar; günler 09:00 -> 09:00.
import APP from "./app.html";
import { satirOf, yeniSatirMi, etkinHafta, etkin5, pencereAnahtari, SAAT } from "./limit.mjs";
import { YERLESIK, birlestir, fiyatBul, kayitBul, eslesenModelBul, fiyatlariIndir, esikler, tabanAd, saglayiciOf } from "./fiyat.mjs";
import KUR_SH from "../istemci/kur.sh";
import KUR_PS1 from "../istemci/kur.ps1";
import MANIFEST from "../istemci/manifest.json"; // geliştiricinin Mac'inde imzalanır; sunucu imzalayamaz
// Uygulama ikilileri (dist/indir/*) Cloudflare statik varlık olarak sunulur: /indir/token-panosu-<sistem>-<mimari>
const sonSurumOf = () => (MANIFEST && MANIFEST.ikili && Number.isInteger(MANIFEST.ikili.surum) ? MANIFEST.ikili.surum : 0);
const SON_SURUM = sonSurumOf();
const SAGLAYICILAR = ["claude", "chatgpt", "gemini"];
const SAGLAYICI_AD = { claude: "Claude", chatgpt: "ChatGPT", gemini: "Gemini" };
const saglayiciSec = (s) => (SAGLAYICILAR.includes(s) ? s : "claude");
const VARSAYILAN_ORIGIN = "https://pano.ornek.com";
const RESMI_AJAN_RE = /^token-panosu\/\d+ \((darwin|windows|linux)\)$/;
const MUHUR_KONTROL_SURUM = 31;
const MUHUR_ILK_SURUM = 40;
const HEDEF_OS_HARITA = { darwin: "Darwin", windows: "Windows", linux: "Linux" };
const HEDEF_TAM_RE = /^(darwin|windows|linux)-(amd64|arm64)(?:-(c|g|m|cg|cm|gm))?$/;
const PAKET_HARFLER = ["c", "g", "m"];
const HARF_SAGLAYICI = { c: "claude", g: "chatgpt", m: "gemini" };
const SAGLAYICI_HARF = { claude: "c", chatgpt: "g", gemini: "m" };
const GECERLI_PAKETLER = new Set(["c", "g", "m", "cg", "cm", "gm", "cgm"]);

export function paketNormalize(girdi) {
  if (girdi === null || girdi === undefined) return "";
  let parcalar;
  if (Array.isArray(girdi)) {
    if (!girdi.length) return "";
    parcalar = girdi.map((x) => (typeof x === "string" ? x.trim().toLowerCase() : null));
    if (parcalar.some((x) => !x)) return "";
  } else if (typeof girdi === "string") {
    const s = girdi.trim().toLowerCase();
    if (!s) return "";
    if (/^[cgm]{1,3}$/.test(s)) {
      const set = new Set(s.split(""));
      return PAKET_HARFLER.filter((h) => set.has(h)).join("");
    }
    parcalar = s.split(/[\s,+]+/).filter(Boolean);
    if (!parcalar.length) return "";
  } else {
    return "";
  }
  const harfSet = new Set();
  for (const p of parcalar) {
    if (Object.hasOwn(SAGLAYICI_HARF, p)) harfSet.add(SAGLAYICI_HARF[p]);
    else if (/^[cgm]{1,3}$/.test(p)) {
      for (const ch of p) harfSet.add(ch);
    } else {
      return "";
    }
  }
  const sonuc = PAKET_HARFLER.filter((h) => harfSet.has(h)).join("");
  return GECERLI_PAKETLER.has(sonuc) ? sonuc : "";
}

export function pakettenSaglayicilar(paket) {
  if (paket === "" || paket === null || paket === undefined) return [...SAGLAYICILAR];
  const norm = paketNormalize(paket);
  if (!norm) return [];
  return [...norm].map((h) => HARF_SAGLAYICI[h]);
}

export function cihazEtkinPaket(cihazIzinli) {
  return paketNormalize(cihazIzinli) || "cgm";
}

export function hedeftenPaketCikar(xTarget) {
  const t = String(xTarget || "").trim().toLowerCase();
  const m = t.match(/^(?:darwin|windows|linux)-(?:amd64|arm64)(?:-(c|g|m|cg|cm|gm))?$/);
  if (!m) return "";
  return m[1] || "cgm";
}

const EPOSTA_RE = /^[^\s@<>"'\\]{1,64}@[^\s@<>"'\\]{1,120}\.[^\s@<>"'\\]{2,}$/;
const epostaTemizle = (v) => {
  if (typeof v !== "string") return "";
  const s = v.trim().toLowerCase();
  return s.length >= 5 && s.length <= 120 && EPOSTA_RE.test(s) ? s : "";
};

// İlk istekte eksik tablo/görünüm varsa idempotent olarak oluşturur (sıfır adımlı D1 kurulumu).
let sonEnvDb = null;
let semaSoz = null;
function semaHazirla(env) {
  if (env && env.DB && env.DB !== sonEnvDb) {
    sonEnvDb = env.DB;
    semaSoz = null;
    ayarBellek = null;
    ayarYuklendiAt = 0;
    fiyatYuklendi = 0;
    fiyatGuncellendi = 0;
    FIYAT_TABAN = YERLESIK;
    FIYAT = YERLESIK;
    kullaniciOnbellek.clear();
    okumaOnbellekTemizle(true);
  }
  if (semaSoz) return semaSoz;
  const ifadeler = [
    `CREATE TABLE IF NOT EXISTS devices (id TEXT PRIMARY KEY, label TEXT NOT NULL, secret TEXT NOT NULL, created_at INTEGER NOT NULL, last_ts INTEGER NOT NULL DEFAULT 0, last_seen INTEGER NOT NULL DEFAULT 0, client_ver INTEGER NOT NULL DEFAULT 0, flags TEXT NOT NULL DEFAULT '[]', srv_flags TEXT NOT NULL DEFAULT '[]', os TEXT NOT NULL DEFAULT '', limit_sorgu INTEGER NOT NULL DEFAULT 0, uye_id TEXT NOT NULL DEFAULT '', bildirim_son INTEGER NOT NULL DEFAULT 0, hesaplar TEXT NOT NULL DEFAULT '{}', hesap_eposta TEXT NOT NULL DEFAULT '{}', izinli_saglayici TEXT NOT NULL DEFAULT '', calisan_paket TEXT NOT NULL DEFAULT '')`,
    `CREATE TABLE IF NOT EXISTS haric (device_id TEXT NOT NULL, sid TEXT NOT NULL, added_at INTEGER NOT NULL, note TEXT NOT NULL DEFAULT '', PRIMARY KEY (device_id, sid))`,
    `CREATE TABLE IF NOT EXISTS limit_log (week INTEGER PRIMARY KEY, pct REAL NOT NULL, set_at INTEGER NOT NULL)`,
    `CREATE TABLE IF NOT EXISTS limit_okuma (id INTEGER PRIMARY KEY CHECK (id = 1), okundu_at INTEGER NOT NULL, device_id TEXT NOT NULL, ham TEXT NOT NULL)`,
    `CREATE TABLE IF NOT EXISTS hafta_ayar (week INTEGER PRIMARY KEY, fable_ayri INTEGER NOT NULL DEFAULT 0, limit_taban_t INTEGER NOT NULL DEFAULT 0)`,
    `CREATE TABLE IF NOT EXISTS limit_gecmis (device_id TEXT NOT NULL, t INTEGER NOT NULL, w_pct REAL, w_reset INTEGER, h_pct REAL, h_reset INTEGER, ham TEXT, PRIMARY KEY (device_id, t))`,
    `CREATE INDEX IF NOT EXISTS limit_gecmis_t ON limit_gecmis(t)`,
    `CREATE TABLE IF NOT EXISTS login_fail (ip TEXT PRIMARY KEY, count INTEGER NOT NULL, window_start INTEGER NOT NULL)`,
    `CREATE TABLE IF NOT EXISTS pair_codes (code TEXT PRIMARY KEY, label TEXT NOT NULL, expires_at INTEGER NOT NULL, uye_id TEXT NOT NULL DEFAULT '', izinli_saglayici TEXT NOT NULL DEFAULT '', used_at INTEGER NOT NULL DEFAULT 0)`,
    `CREATE TABLE IF NOT EXISTS usage_5m (device_id TEXT NOT NULL, bucket INTEGER NOT NULL, model TEXT NOT NULL, sid TEXT NOT NULL DEFAULT '', input INTEGER NOT NULL DEFAULT 0, output INTEGER NOT NULL DEFAULT 0, cw5m INTEGER NOT NULL DEFAULT 0, cw1h INTEGER NOT NULL DEFAULT 0, cr INTEGER NOT NULL DEFAULT 0, messages INTEGER NOT NULL DEFAULT 0, PRIMARY KEY (device_id, bucket, model, sid))`,
    `CREATE TABLE IF NOT EXISTS usage_h (device_id TEXT NOT NULL, hour INTEGER NOT NULL, model TEXT NOT NULL, sid TEXT NOT NULL DEFAULT '', input INTEGER NOT NULL DEFAULT 0, output INTEGER NOT NULL DEFAULT 0, cw5m INTEGER NOT NULL DEFAULT 0, cw1h INTEGER NOT NULL DEFAULT 0, cr INTEGER NOT NULL DEFAULT 0, messages INTEGER NOT NULL DEFAULT 0, updated_at INTEGER NOT NULL DEFAULT 0, PRIMARY KEY (device_id, hour, model, sid))`,
    `CREATE INDEX IF NOT EXISTS usage_5m_bucket ON usage_5m(bucket)`,
    `CREATE INDEX IF NOT EXISTS usage_h_hour ON usage_h(hour)`,
    `CREATE VIEW IF NOT EXISTS usage5_v AS SELECT u.* FROM usage_5m u WHERE NOT EXISTS (SELECT 1 FROM haric x WHERE x.device_id = u.device_id AND x.sid = u.sid)`,
    `CREATE VIEW IF NOT EXISTS usage_v AS SELECT u.* FROM usage_h u WHERE NOT EXISTS (SELECT 1 FROM haric x WHERE x.device_id = u.device_id AND x.sid = u.sid)`,
    `CREATE TABLE IF NOT EXISTS uyeler (id TEXT PRIMARY KEY, ad TEXT NOT NULL UNIQUE, created_at INTEGER NOT NULL)`,
    `CREATE TABLE IF NOT EXISTS bildirimler (id INTEGER PRIMARY KEY AUTOINCREMENT, baslik TEXT NOT NULL, metin TEXT NOT NULL, created_at INTEGER NOT NULL, bitis INTEGER NOT NULL)`,
    `CREATE TABLE IF NOT EXISTS fiyat_listesi (id INTEGER PRIMARY KEY CHECK (id = 1), veri TEXT NOT NULL, guncellendi INTEGER NOT NULL DEFAULT 0, denendi INTEGER NOT NULL DEFAULT 0, kaynak TEXT NOT NULL DEFAULT '', hata TEXT NOT NULL DEFAULT '')`,
    `CREATE TABLE IF NOT EXISTS hesap_kilit (saglayici TEXT PRIMARY KEY, hesap_ozet TEXT NOT NULL DEFAULT '', etiket TEXT NOT NULL DEFAULT '', kilitlendi_at INTEGER NOT NULL, cihaz_id TEXT NOT NULL DEFAULT '', eposta TEXT NOT NULL DEFAULT '')`,
    `CREATE TABLE IF NOT EXISTS saglayici_limit_gecmis (saglayici TEXT NOT NULL, device_id TEXT NOT NULL, t INTEGER NOT NULL, w_pct REAL, w_reset INTEGER, h_pct REAL, h_reset INTEGER, ham TEXT, PRIMARY KEY (saglayici, device_id, t))`,
    `CREATE INDEX IF NOT EXISTS saglayici_limit_gecmis_t ON saglayici_limit_gecmis(saglayici, t)`,
    `CREATE TABLE IF NOT EXISTS saglayici_limit_log (saglayici TEXT NOT NULL, week INTEGER NOT NULL, pct REAL NOT NULL, set_at INTEGER NOT NULL, PRIMARY KEY (saglayici, week))`,
    `CREATE TABLE IF NOT EXISTS sistem_ayar (id INTEGER PRIMARY KEY CHECK (id = 1), veri TEXT NOT NULL, guncellendi INTEGER NOT NULL DEFAULT 0)`,
    `CREATE TABLE IF NOT EXISTS kullanicilar (id TEXT PRIMARY KEY, kullanici_adi TEXT NOT NULL UNIQUE, ad TEXT NOT NULL DEFAULT '', rol TEXT NOT NULL DEFAULT 'uye', sifre TEXT NOT NULL DEFAULT '', uye_id TEXT NOT NULL DEFAULT '', durum TEXT NOT NULL DEFAULT 'davetli', oturum_surum INTEGER NOT NULL DEFAULT 1, olusturuldu INTEGER NOT NULL, son_giris INTEGER NOT NULL DEFAULT 0)`,
    `CREATE TABLE IF NOT EXISTS kullanici_ofis (kullanici_id TEXT NOT NULL, saglayici TEXT NOT NULL, rol TEXT NOT NULL DEFAULT 'uye', PRIMARY KEY (kullanici_id, saglayici))`,
    `CREATE TABLE IF NOT EXISTS hesap_token (ozet TEXT PRIMARY KEY, kullanici_id TEXT NOT NULL, tur TEXT NOT NULL, son INTEGER NOT NULL, olusturuldu INTEGER NOT NULL)`,
    `CREATE TABLE IF NOT EXISTS oto_hafta_sifirlama (saglayici TEXT PRIMARY KEY, reset_at INTEGER NOT NULL, okundu_at INTEGER NOT NULL, cihaz_id TEXT NOT NULL DEFAULT '')`,
  ];
  semaSoz = env.DB.batch(ifadeler.map((s) => env.DB.prepare(s)))
    .then(() => Promise.all([
      env.DB.prepare("ALTER TABLE devices ADD COLUMN hesap_eposta TEXT NOT NULL DEFAULT '{}'").run().catch(() => {}),
      env.DB.prepare("ALTER TABLE devices ADD COLUMN izinli_saglayici TEXT NOT NULL DEFAULT ''").run().catch(() => {}),
      env.DB.prepare("ALTER TABLE devices ADD COLUMN calisan_paket TEXT NOT NULL DEFAULT ''").run().catch(() => {}),
      env.DB.prepare("ALTER TABLE pair_codes ADD COLUMN izinli_saglayici TEXT NOT NULL DEFAULT ''").run().catch(() => {}),
      env.DB.prepare("ALTER TABLE pair_codes ADD COLUMN used_at INTEGER NOT NULL DEFAULT 0").run().catch(() => {}),
      env.DB.prepare("ALTER TABLE hesap_kilit ADD COLUMN eposta TEXT NOT NULL DEFAULT ''").run().catch(() => {}),
    ]))
    .catch((e) => {
      semaSoz = null;
      throw e;
    });
  return semaSoz;
}

// ---------- sistem ayarları (D1 sistem_ayar tablosu + bellek önbelleği) ----------
const VARSAYILAN_AYAR = {
  panoBaslik: "Token Panosu",
  zamanDilimi: "Europe/Istanbul",
  haftaMod: "otomatik", // "otomatik" (diskten) | "elle" (manuel gün/saat)
  haftaGun: 6, // 0=Pazar .. 6=Cumartesi (elle modda ve disk verisi yokken yedek olarak kullanılır)
  haftaSaat: 9, // 0..23
  payModu: "esit", // "esit" | "ozel"
  ozelPaylar: {}, // { [uyeId | "c:"+deviceId]: yuzdeAgirlik }
  hesapKilitAktif: { claude: true, chatgpt: true, gemini: true },
  muhurZorunlu: false, // tüm kayıtlı cihazlar mühürlü sürüme (>=31) ulaştığında otomatik true olur
  zorunluMinSurum: 0, // mühür zorunlu olduğunda kabul edilen en düşük sürüm
  ozelFiyatlar: {}, // { [model]: [girdi, cikti, cw5m, cw1h, cr] }
  ofis: {
    aktif: true,
    dayakAktif: true,
    asimBildirimAktif: true,
    asimEsik: 0,
    geceUykuAktif: true,
    geceBaslangic: 20,
    geceBitis: 6,
    zorlaVakit: "otomatik",
    sohbetAktif: true,
    sohbetSiklik: "normal",
    kediAktif: true,
    kediAd: "Tekir",
    gorunumler: {},
  },
  // Ofis (sağlayıcı) başına ayrı ayarlar: üye listesi (null = otomatik), kota paylaşımı ve ofis simülasyonu.
  // Boş bırakılan alanlar genel değerden gelir.
  saglayiciAyar: { claude: {}, chatgpt: {}, gemini: {} },
};

const OFIS_BOOL = ["aktif", "dayakAktif", "asimBildirimAktif", "geceUykuAktif", "sohbetAktif", "kediAktif"];
function ofisYamaTemizle(g) {
  if (!g || typeof g !== "object" || Array.isArray(g)) return {};
  const o = {};
  for (const k of OFIS_BOOL) if (typeof g[k] === "boolean") o[k] = g[k];
  if (g.asimEsik !== undefined) {
    const n = Number(g.asimEsik);
    if (Number.isFinite(n) && n >= 0 && n <= 50) o.asimEsik = Math.round(n * 10) / 10;
  }
  if (g.geceBaslangic !== undefined) { const n = tamSayiSinir(g.geceBaslangic, 0, 23, null); if (n !== null) o.geceBaslangic = n; }
  if (g.geceBitis !== undefined) { const n = tamSayiSinir(g.geceBitis, 0, 23, null); if (n !== null) o.geceBitis = n; }
  if (["otomatik", "sabah", "gunduz", "aksam", "gece"].includes(g.zorlaVakit)) o.zorlaVakit = g.zorlaVakit;
  if (["az", "normal", "sik"].includes(g.sohbetSiklik)) o.sohbetSiklik = g.sohbetSiklik;
  if (g.kediAd !== undefined) o.kediAd = temizMetin(g.kediAd, 24, "Tekir");
  return o;
}
function paylarTemizle(p) {
  const out = {};
  if (!p || typeof p !== "object" || Array.isArray(p)) return out;
  for (const [k, v] of Object.entries(p).slice(0, 80)) {
    if (!/^(c:)?[a-f0-9]{16,32}$/.test(k)) continue;
    const n = Number(v);
    if (Number.isFinite(n) && n >= 0 && n <= 1000) out[k] = Math.round(n * 100) / 100;
  }
  return out;
}
// Tek bir ofisin yaması: yalnızca gönderilen anahtarlar değişir (uyeler, payModu, ozelPaylar, ofis, haftaMod, haftaGun, haftaSaat)
function saglayiciYamaUygula(mevcut, gelen) {
  const o = { ...(mevcut || {}) };
  if (!gelen || typeof gelen !== "object" || Array.isArray(gelen)) return o;
  if (gelen.uyeler === null) delete o.uyeler;
  else if (Array.isArray(gelen.uyeler)) o.uyeler = [...new Set(gelen.uyeler.filter((x) => typeof x === "string" && /^(c:)?[a-f0-9]{16,32}$/.test(x)))].slice(0, 80);
  if (gelen.payModu === "esit" || gelen.payModu === "ozel") o.payModu = gelen.payModu;
  if (gelen.ozelPaylar !== undefined) o.ozelPaylar = paylarTemizle(gelen.ozelPaylar);
  if (gelen.ofis !== undefined) o.ofis = ofisYamaTemizle(gelen.ofis);
  if (gelen.haftaMod === "varsayilan" || gelen.haftaMod === null) delete o.haftaMod;
  else if (gelen.haftaMod === "otomatik" || gelen.haftaMod === "elle") o.haftaMod = gelen.haftaMod;
  if (gelen.haftaGun === null) delete o.haftaGun;
  else if (gelen.haftaGun !== undefined) { const n = tamSayiSinir(gelen.haftaGun, 0, 6, null); if (n !== null) o.haftaGun = n; }
  if (gelen.haftaSaat === null) delete o.haftaSaat;
  else if (gelen.haftaSaat !== undefined) { const n = tamSayiSinir(gelen.haftaSaat, 0, 23, null); if (n !== null) o.haftaSaat = n; }
  return o;
}
// Bir ofisin etkin ayarı: genel ayar + o ofisin yaması
function ayarSaglayiciIcin(ayar, sgl) {
  const a = ayar || VARSAYILAN_AYAR;
  const o = (a.saglayiciAyar && a.saglayiciAyar[sgl]) || {};
  const { saglayiciAyar: _sa, ...genel } = a;
  const ozelMod = o.haftaMod === "otomatik" || o.haftaMod === "elle" ? o.haftaMod : "varsayilan";
  return {
    ...genel,
    saglayici: sgl,
    haftaModOzel: ozelMod,
    haftaMod: ozelMod !== "varsayilan" ? ozelMod : (a.haftaMod === "elle" ? "elle" : "otomatik"),
    haftaGun: Number.isInteger(o.haftaGun) ? o.haftaGun : a.haftaGun,
    haftaSaat: Number.isInteger(o.haftaSaat) ? o.haftaSaat : a.haftaSaat,
    payModu: o.payModu || a.payModu,
    ozelPaylar: o.ozelPaylar || a.ozelPaylar || {},
    ofis: { ...(a.ofis || {}), ...(o.ofis || {}), gorunumler: (a.ofis && a.ofis.gorunumler) || {} },
    ofisUyeler: Array.isArray(o.uyeler) ? o.uyeler : null,
  };
}

function zamanDilimiGecerliMi(tz) {
  if (typeof tz !== "string" || tz.length < 2 || tz.length > 64) return false;
  try {
    new Intl.DateTimeFormat("tr-TR", { timeZone: tz }).format(new Date(0));
    return true;
  } catch {
    return false;
  }
}

function temizMetin(x, enFazla, varsayilan) {
  if (typeof x !== "string") return varsayilan;
  const s = x.replace(/[\u0000-\u001f\u007f-\u009f]/g, " ").replace(/\s+/g, " ").trim().slice(0, enFazla);
  return s || varsayilan;
}

function tamSayiSinir(x, alt, ust, varsayilan) {
  const n = Number(x);
  return Number.isInteger(n) && n >= alt && n <= ust ? n : varsayilan;
}

function gorunumTemizle(g) {
  if (!g || typeof g !== "object" || Array.isArray(g)) return null;
  const cikti = {};
  if (Number.isInteger(g.renk) && g.renk >= -1 && g.renk < 16) cikti.renk = g.renk;
  if (Number.isInteger(g.kiyafet) && g.kiyafet >= -1 && g.kiyafet < 8) cikti.kiyafet = g.kiyafet;
  if (Number.isInteger(g.sacTip) && g.sacTip >= -1 && g.sacTip < 8) cikti.sacTip = g.sacTip;
  if (Number.isInteger(g.sacRenk) && g.sacRenk >= -1 && g.sacRenk < 12) cikti.sacRenk = g.sacRenk;
  if (Number.isInteger(g.ten) && g.ten >= -1 && g.ten < 8) cikti.ten = g.ten;
  if (["oto", "evet", "hayir"].includes(g.gozluk)) cikti.gozluk = g.gozluk;
  if (["oto", "evet", "hayir"].includes(g.sakal)) cikti.sakal = g.sakal;
  return Object.keys(cikti).length ? cikti : null;
}

function ayarTemizle(ham, taban = VARSAYILAN_AYAR) {
  const h = ham && typeof ham === "object" && !Array.isArray(ham) ? ham : {};
  const t = taban || VARSAYILAN_AYAR;

  let ozelPaylar = { ...(t.ozelPaylar || {}) };
  if (h.ozelPaylar && typeof h.ozelPaylar === "object" && !Array.isArray(h.ozelPaylar)) {
    ozelPaylar = {};
    for (const [k, v] of Object.entries(h.ozelPaylar).slice(0, 80)) {
      if (!/^(c:)?[a-f0-9]{16,32}$/.test(k)) continue;
      const n = Number(v);
      if (Number.isFinite(n) && n >= 0 && n <= 1000) ozelPaylar[k] = Math.round(n * 100) / 100;
    }
  }

  const hkTaban = t.hesapKilitAktif || VARSAYILAN_AYAR.hesapKilitAktif;
  const hkGelen = h.hesapKilitAktif && typeof h.hesapKilitAktif === "object" ? h.hesapKilitAktif : {};
  const hesapKilitAktif = {
    claude: typeof hkGelen.claude === "boolean" ? hkGelen.claude : hkTaban.claude,
    chatgpt: typeof hkGelen.chatgpt === "boolean" ? hkGelen.chatgpt : hkTaban.chatgpt,
    gemini: typeof hkGelen.gemini === "boolean" ? hkGelen.gemini : hkTaban.gemini,
  };

  let ozelFiyatlar = { ...(t.ozelFiyatlar || {}) };
  if (h.ozelFiyatlar && typeof h.ozelFiyatlar === "object" && !Array.isArray(h.ozelFiyatlar)) {
    ozelFiyatlar = {};
    for (const [mAd, dizi] of Object.entries(h.ozelFiyatlar).slice(0, 120)) {
      const m = String(mAd || "").trim().toLowerCase();
      if (!/^[a-z0-9][a-z0-9.\-_]{0,63}$/.test(m) || !Array.isArray(dizi) || dizi.length !== 5) continue;
      const sayilar = dizi.map(Number);
      if (sayilar.every((x) => Number.isFinite(x) && x >= 0 && x < 10000)) {
        ozelFiyatlar[m] = sayilar.map((x) => Math.round(x * 1e6) / 1e6);
      }
    }
  }

  const ofTaban = t.ofis || VARSAYILAN_AYAR.ofis;
  const ofGelen = h.ofis && typeof h.ofis === "object" && !Array.isArray(h.ofis) ? h.ofis : {};
  let gorunumler = { ...(ofTaban.gorunumler || {}) };
  if (ofGelen.gorunumler && typeof ofGelen.gorunumler === "object" && !Array.isArray(ofGelen.gorunumler)) {
    gorunumler = {};
    for (const [k, v] of Object.entries(ofGelen.gorunumler).slice(0, 80)) {
      if (!/^(c:)?[a-f0-9]{16,32}$/.test(k)) continue;
      const temizG = gorunumTemizle(v);
      if (temizG) gorunumler[k] = temizG;
    }
  }
  const esikHam = ofGelen.asimEsik !== undefined ? Number(ofGelen.asimEsik) : ofTaban.asimEsik;
  const asimEsik = Number.isFinite(esikHam) && esikHam >= 0 && esikHam <= 50 ? Math.round(esikHam * 10) / 10 : ofTaban.asimEsik;

  let githubSurumKontrol = t.github_surum_kontrol && typeof t.github_surum_kontrol === "object" ? t.github_surum_kontrol : null;
  if (h.github_surum_kontrol === null) githubSurumKontrol = null;
  else if (h.github_surum_kontrol && typeof h.github_surum_kontrol === "object" && !Array.isArray(h.github_surum_kontrol)) {
    const gk = h.github_surum_kontrol;
    if (Number.isFinite(gk.t) && Number.isFinite(gk.son_surum) && typeof gk.indir_url === "string") {
      githubSurumKontrol = {
        t: Math.floor(gk.t),
        son_surum: Math.floor(gk.son_surum),
        indir_url: gk.indir_url.slice(0, 240),
      };
    }
  }

  const cikti = {
    panoBaslik: h.panoBaslik !== undefined ? temizMetin(h.panoBaslik, 48, "Token Panosu") : t.panoBaslik,
    zamanDilimi: h.zamanDilimi !== undefined && zamanDilimiGecerliMi(h.zamanDilimi) ? h.zamanDilimi : t.zamanDilimi,
    haftaMod: h.haftaMod === "elle" || h.haftaMod === "otomatik" ? h.haftaMod : (t.haftaMod === "elle" ? "elle" : "otomatik"),
    haftaGun: h.haftaGun !== undefined ? tamSayiSinir(h.haftaGun, 0, 6, t.haftaGun) : t.haftaGun,
    haftaSaat: h.haftaSaat !== undefined ? tamSayiSinir(h.haftaSaat, 0, 23, t.haftaSaat) : t.haftaSaat,
    payModu: h.payModu === "ozel" || h.payModu === "esit" ? h.payModu : t.payModu,
    ozelPaylar,
    hesapKilitAktif,
    muhurZorunlu: typeof h.muhurZorunlu === "boolean" ? h.muhurZorunlu : !!t.muhurZorunlu,
    zorunluMinSurum: h.zorunluMinSurum !== undefined ? tamSayiSinir(h.zorunluMinSurum, 0, 1000000, t.zorunluMinSurum || 0) : (t.zorunluMinSurum || 0),
    ozelFiyatlar,
    ofis: {
      aktif: typeof ofGelen.aktif === "boolean" ? ofGelen.aktif : ofTaban.aktif,
      dayakAktif: typeof ofGelen.dayakAktif === "boolean" ? ofGelen.dayakAktif : ofTaban.dayakAktif,
      asimBildirimAktif: typeof ofGelen.asimBildirimAktif === "boolean" ? ofGelen.asimBildirimAktif : ofTaban.asimBildirimAktif,
      asimEsik,
      geceUykuAktif: typeof ofGelen.geceUykuAktif === "boolean" ? ofGelen.geceUykuAktif : ofTaban.geceUykuAktif,
      geceBaslangic: ofGelen.geceBaslangic !== undefined ? tamSayiSinir(ofGelen.geceBaslangic, 0, 23, ofTaban.geceBaslangic) : ofTaban.geceBaslangic,
      geceBitis: ofGelen.geceBitis !== undefined ? tamSayiSinir(ofGelen.geceBitis, 0, 23, ofTaban.geceBitis) : ofTaban.geceBitis,
      zorlaVakit: ["otomatik", "sabah", "gunduz", "aksam", "gece"].includes(ofGelen.zorlaVakit) ? ofGelen.zorlaVakit : ofTaban.zorlaVakit,
      sohbetAktif: typeof ofGelen.sohbetAktif === "boolean" ? ofGelen.sohbetAktif : ofTaban.sohbetAktif,
      sohbetSiklik: ["az", "normal", "sik"].includes(ofGelen.sohbetSiklik) ? ofGelen.sohbetSiklik : ofTaban.sohbetSiklik,
      kediAktif: typeof ofGelen.kediAktif === "boolean" ? ofGelen.kediAktif : ofTaban.kediAktif,
      kediAd: ofGelen.kediAd !== undefined ? temizMetin(ofGelen.kediAd, 24, "Tekir") : ofTaban.kediAd,
      gorunumler,
    },
    saglayiciAyar: Object.fromEntries(SAGLAYICILAR.map((s) => {
      const taban = (t.saglayiciAyar && t.saglayiciAyar[s]) || {};
      const gelen = h.saglayiciAyar && typeof h.saglayiciAyar === "object" ? h.saglayiciAyar[s] : undefined;
      return [s, gelen === undefined ? { ...taban } : saglayiciYamaUygula(taban, gelen)];
    })),
  };
  if (githubSurumKontrol) cikti.github_surum_kontrol = githubSurumKontrol;
  return cikti;
}

let ayarBellek = null;
let ayarYuklendiAt = 0;
async function ayarYukle(env, zorla = false) {
  const now = Math.floor(Date.now() / 1000);
  if (!zorla && ayarBellek && now - ayarYuklendiAt < 60) return ayarBellek;
  let yuklenen = VARSAYILAN_AYAR;
  try {
    const r = await env.DB.prepare("SELECT veri FROM sistem_ayar WHERE id = 1").first();
    if (r && r.veri) yuklenen = ayarTemizle(JSON.parse(r.veri), VARSAYILAN_AYAR);
  } catch {}
  ayarBellek = yuklenen;
  ayarYuklendiAt = now;
  fiyatBirlestirUygula();
  return ayarBellek;
}

async function ayarKaydet(env, yama) {
  const mevcut = await ayarYukle(env, true);
  const yeni = ayarTemizle(yama, mevcut);
  const now = Math.floor(Date.now() / 1000);
  await env.DB.prepare("INSERT INTO sistem_ayar (id, veri, guncellendi) VALUES (1, ?, ?) ON CONFLICT(id) DO UPDATE SET veri = excluded.veri, guncellendi = excluded.guncellendi")
    .bind(JSON.stringify(yeni), now).run();
  ayarBellek = yeni;
  ayarYuklendiAt = now;
  okumaOnbellekTemizle(true);
  fiyatBirlestirUygula();
  return yeni;
}

// Gruplar için eşit ya da özel ağırlıklı limit payı yüzdeleri (toplamı 100)
function grupPaylariHesapla(gruplarListesi, ayar) {
  const payMap = new Map();
  const n = gruplarListesi.length;
  if (!n) return payMap;
  const esit = 100 / n;
  if (!ayar || ayar.payModu !== "ozel") {
    for (const g of gruplarListesi) payMap.set(g.id, esit);
    return payMap;
  }
  const hamAgirlik = gruplarListesi.map((g) => {
    const v = ayar.ozelPaylar ? Number(ayar.ozelPaylar[g.id]) : NaN;
    return Number.isFinite(v) && v >= 0 ? v : esit;
  });
  const toplam = hamAgirlik.reduce((a, b) => a + b, 0);
  gruplarListesi.forEach((g, i) => {
    payMap.set(g.id, toplam > 0 ? (hamAgirlik[i] / toplam) * 100 : esit);
  });
  return payMap;
}

// Fiyatlar: src/fiyat.mjs (günde bir kez resmî sayfadan yenilenir, D1 fiyat_listesi tablosunda durur).
// Özel model fiyatları (ayar.ozelFiyatlar) her zaman en yüksek önceliğe sahiptir.
let FIYAT_TABAN = YERLESIK;
let FIYAT = YERLESIK;
let fiyatYuklendi = 0;
let fiyatGuncellendi = 0;
const FIYAT_ONBELLEK_SN = 600;
const FIYAT_ESKI_SN = 36 * 3600; // bu kadar eskiyse (cron atlanmışsa) ilk istekte arka planda yenilenir
function fiyatBirlestirUygula() {
  const ozel = ayarBellek && ayarBellek.ozelFiyatlar ? ayarBellek.ozelFiyatlar : {};
  const ek = {};
  for (const [m, b] of Object.entries(ozel)) {
    if (Array.isArray(b) && b.length === 5) ek[m] = { b, ozel: true };
  }
  FIYAT = { ...FIYAT_TABAN, ...ek };
}
async function fiyatYukle(env, ctx) {
  await ayarYukle(env);
  const now = Math.floor(Date.now() / 1000);
  if (now - fiyatYuklendi < FIYAT_ONBELLEK_SN) {
    fiyatBirlestirUygula();
    return;
  }
  fiyatYuklendi = now;
  try {
    const r = await env.DB.prepare("SELECT veri, guncellendi FROM fiyat_listesi WHERE id = 1").first();
    if (r) {
      FIYAT_TABAN = birlestir(JSON.parse(r.veri));
      fiyatGuncellendi = r.guncellendi;
    }
  } catch {}
  fiyatBirlestirUygula();
  if (ctx && now - fiyatGuncellendi > FIYAT_ESKI_SN) ctx.waitUntil(fiyatYenile(env).catch(() => {}));
}
async function fiyatYenile(env) {
  await ayarYukle(env);
  const now = Math.floor(Date.now() / 1000);
  const { liste, kaynak, not } = await fiyatlariIndir();
  if (!liste) {
    await env.DB.prepare("INSERT INTO fiyat_listesi (id, veri, guncellendi, denendi, kaynak, hata) VALUES (1, '{}', 0, ?, '', ?) ON CONFLICT(id) DO UPDATE SET denendi = excluded.denendi, hata = excluded.hata")
      .bind(now, not || "kaynak yok").run();
    return { tamam: false, hata: not };
  }
  await env.DB.prepare("INSERT INTO fiyat_listesi (id, veri, guncellendi, denendi, kaynak, hata) VALUES (1, ?, ?, ?, ?, ?) ON CONFLICT(id) DO UPDATE SET veri = excluded.veri, guncellendi = excluded.guncellendi, denendi = excluded.denendi, kaynak = excluded.kaynak, hata = excluded.hata")
    .bind(JSON.stringify(liste), now, now, kaynak, not || "").run();
  FIYAT_TABAN = birlestir(liste);
  fiyatBirlestirUygula();
  fiyatGuncellendi = now;
  fiyatYuklendi = now;
  okumaOnbellekTemizle(false);
  return { tamam: true, kaynak, model: Object.keys(liste).length, not };
}
const costOf = (r, zamanMs) => {
  if (typeof r.usd === "number" && Number.isFinite(r.usd) && zamanMs === undefined) return r.usd;
  const z = zamanMs !== undefined
    ? zamanMs
    : (Number.isFinite(r.hour) ? r.hour * 1000 : (Number.isFinite(r.bucket) ? r.bucket * 1000 : undefined));
  const [i, o, w5, w1, cr] = fiyatBul(FIYAT, r.model, z);
  return (r.input * i + r.output * o + r.cw5m * w5 + r.cw1h * w1 + r.cr * cr) / 1e6;
};
// ---------- limit payı aşımı (sürüm 17+ istemciye uyarı) ----------
let asimOnbellek = null;
async function asimTablosu(env, now) {
  const tamAyar = await ayarYukle(env);
  const sistemAyar = ayarSaglayiciIcin(tamAyar, "claude");
  if (sistemAyar.ofis && sistemAyar.ofis.asimBildirimAktif === false) return new Map();
  if (asimOnbellek && now - asimOnbellek.t < 60) return asimOnbellek.m;
  const otoMap = await otoSifirlamaGetir(env, now);
  const wk = weekStartSaglayici(now, tamAyar, otoMap, "claude");
  const [devices, uyeler, kullanim, lim, fable] = await Promise.all([
    env.DB.prepare("SELECT id, label, uye_id FROM devices").all(),
    env.DB.prepare("SELECT id, ad FROM uyeler").all(),
    env.DB.prepare(
      `SELECT device_id, model, SUM(input) input, SUM(output) output, SUM(cw5m) cw5m, SUM(cw1h) cw1h, SUM(cr) cr
         FROM usage_v WHERE hour >= ? AND hour < ? GROUP BY device_id, model`
    ).bind(wk, wk + WEEK).all(),
    env.DB.prepare("SELECT pct FROM limit_log WHERE week = ?").bind(wk).first(),
    env.DB.prepare("SELECT ham FROM limit_gecmis WHERE t >= ? AND ham LIKE '%fable%' ORDER BY t DESC LIMIT 1").bind(wk).first(),
  ]);
  const m = new Map();
  const gruplar = new Map(uyeler.results.map((u) => [u.id, { id: u.id, ad: u.ad, devs: [], usd: 0, fusd: 0 }]));
  for (const d of devices.results) {
    const g = gruplar.get(d.uye_id);
    if (g) g.devs.push(d.id); else gruplar.set("c:" + d.id, { id: "c:" + d.id, ad: d.label, devs: [d.id], usd: 0, fusd: 0 });
  }
  const grupOf = new Map();
  for (const g of gruplar.values()) for (const id of g.devs) grupOf.set(id, g);
  for (const r of kullanim.results) {
    if (saglayiciOf(r.model) !== "claude") continue;
    const g = grupOf.get(r.device_id);
    if (!g) continue;
    const c = costOf(r);
    g.usd += c;
    if (/fable/i.test(r.model)) g.fusd += c;
  }
  const n = gruplar.size;
  if (!n) { asimOnbellek = { t: now, m }; return m; }
  // Claude ofisinde elle üye listesi seçildiyse yalnızca o üyeler (ve bilgisayarları) paylaşıma katılır
  const liste = sistemAyar.ofisUyeler ? [...gruplar.values()].filter((g) => sistemAyar.ofisUyeler.includes(g.id)) : [...gruplar.values()];
  const payMap = grupPaylariHesapla(liste, sistemAyar);
  const tolerans = (sistemAyar.ofis && Number.isFinite(sistemAyar.ofis.asimEsik)) ? sistemAyar.ofis.asimEsik : 0;
  const toplam = liste.reduce((a, g) => a + g.usd, 0);
  const toplamF = liste.reduce((a, g) => a + g.fusd, 0);
  let fpct = null;
  if (fable) {
    try {
      const f = JSON.parse(fable.ham);
      const k = Object.keys(f).find((x) => /fable/i.test(x) && x.endsWith("used_percentage") && Number.isFinite(f[x]));
      if (k) fpct = f[k];
    } catch {}
  }
  const yuvarla = (x) => Math.round(x * 10) / 10;
  for (const g of liste) {
    const hak = payMap.get(g.id) ?? (100 / n);
    if (hak <= 0 && g.usd <= 0 && g.fusd <= 0) continue;
    const a = { ad: g.ad };
    if (lim && toplam > 0 && g.usd > 0) {
      const used = lim.pct * (g.usd / toplam);
      if (used >= hak + tolerans - 0.005) a.w = [yuvarla(used), yuvarla(hak)];
    }
    if (fpct !== null && toplamF > 0 && g.fusd > 0) {
      const used = fpct * (g.fusd / toplamF);
      if (used >= hak + tolerans - 0.005) a.f = [yuvarla(used), yuvarla(hak)];
    }
    if (a.w || a.f) for (const id of g.devs) m.set(id, a);
  }
  asimOnbellek = { t: now, m };
  return m;
}

// "@uzun" satırları (istem eşiğini aşan mesajlar) maliyetleri hesaplandıktan sonra sade model adıyla birleştirilir
function sadeBirlestir(satirlar, anahtarlar) {
  const m = new Map();
  for (const r of satirlar) {
    const x = { ...r, model: tabanAd(r.model) };
    const k = anahtarlar.map((a) => x[a]).join("|");
    const v = m.get(k);
    if (!v) { m.set(k, x); continue; }
    for (const [a, d] of Object.entries(x)) if (!anahtarlar.includes(a) && typeof d === "number") v[a] = (v[a] || 0) + d;
  }
  return [...m.values()];
}

const WEEK = 604800;
const WEEK0 = 194400; // Varsayılan: 1970-01-03 Cumartesi 06:00 UTC (Europe/Istanbul Cumartesi 09:00)
const GUN_KISA = { Sun: 0, Mon: 1, Tue: 2, Wed: 3, Thu: 4, Fri: 5, Sat: 6 };
const dtfOnbellek = new Map();
function parcaOkuyucu(tz) {
  let fmt = dtfOnbellek.get(tz);
  if (!fmt) {
    fmt = new Intl.DateTimeFormat("en-US", {
      timeZone: tz, year: "numeric", month: "2-digit", day: "2-digit",
      weekday: "short", hour: "2-digit", minute: "2-digit", second: "2-digit", hourCycle: "h23",
    });
    dtfOnbellek.set(tz, fmt);
  }
  return (sec) => {
    const o = {};
    for (const p of fmt.formatToParts(new Date(sec * 1000))) o[p.type] = p.value;
    return {
      y: Number(o.year), m: Number(o.month), d: Number(o.day),
      wd: GUN_KISA[o.weekday] ?? 6, hr: Number(o.hour) % 24, mn: Number(o.minute), sc: Number(o.second),
    };
  };
}
function haftaBazOf(ayar, refSec = Math.floor(Date.now() / 1000)) {
  const a = ayar || ayarBellek || VARSAYILAN_AYAR;
  if (a.zamanDilimi === "Europe/Istanbul" && a.haftaGun === 6 && a.haftaSaat === 9) return WEEK0;
  try {
    const oku = parcaOkuyucu(a.zamanDilimi);
    const p = oku(refSec);
    let gunFark = (p.wd - a.haftaGun + 7) % 7;
    if (gunFark === 0 && p.hr < a.haftaSaat) gunFark = 7;
    const hedefUtcSn = Math.floor(Date.UTC(p.y, p.m - 1, p.d - gunFark, a.haftaSaat, 0, 0) / 1000);
    const ofset = (s) => {
      const q = oku(s);
      return Math.floor(Date.UTC(q.y, q.m - 1, q.d, q.hr, q.mn, q.sc) / 1000) - s;
    };
    const t1 = hedefUtcSn - ofset(hedefUtcSn);
    const t2 = Math.floor((hedefUtcSn - ofset(t1)) / 3600) * 3600;
    return ((t2 % WEEK) + WEEK) % WEEK;
  } catch {
    return WEEK0;
  }
}
const weekStartFor = (t, baz) => baz + Math.floor((t - baz) / WEEK) * WEEK;
const weekStart = (t, ayar) => weekStartFor(t, haftaBazOf(ayar, t));

function sonrakiHaftaSn(referans, simdi) {
  let r = Number(referans);
  if (!Number.isFinite(r) || r <= 1600000000) return 0;
  r = Math.round(r);
  if (!Number.isFinite(simdi) || simdi <= 0) return r;
  if (r <= simdi) {
    const k = Math.floor((simdi - r) / WEEK) + 1;
    r += k * WEEK;
  } else if (r > simdi + WEEK) {
    const k = Math.floor((r - simdi - 1) / WEEK);
    r -= k * WEEK;
  }
  return r;
}

let otoSifirlamaBellek = { t: 0, map: null };
async function otoSifirlamaGetir(env, now = Math.floor(Date.now() / 1000), zorla = false) {
  if (!zorla && otoSifirlamaBellek.map && now - otoSifirlamaBellek.t < 60) return otoSifirlamaBellek.map;
  const map = new Map();
  try {
    const { results } = await env.DB.prepare("SELECT saglayici, reset_at, okundu_at, cihaz_id FROM oto_hafta_sifirlama").all();
    for (const r of results || []) {
      if (SAGLAYICILAR.includes(r.saglayici) && Number.isFinite(r.reset_at) && r.reset_at > 1600000000) {
        map.set(r.saglayici, { reset_at: r.reset_at, okundu_at: r.okundu_at || 0, cihaz_id: r.cihaz_id || "" });
      }
    }
    if (!map.has("claude")) {
      const cl = await env.DB.prepare("SELECT device_id, t, w_reset, ham FROM limit_gecmis WHERE device_id != '' AND device_id != 'fable' AND w_reset IS NOT NULL ORDER BY t DESC LIMIT 1").first();
      if (cl && Number.isFinite(cl.w_reset)) {
        let gercekReset = false;
        try {
          const h = JSON.parse(cl.ham || "{}");
          gercekReset = Number.isFinite(h["seven_day.resets_at"]) && h["seven_day.resets_at"] <= cl.t + 10 * 86400;
        } catch {}
        if (gercekReset) map.set("claude", { reset_at: cl.w_reset, okundu_at: cl.t || 0, cihaz_id: cl.device_id || "" });
      }
    }
    if (!map.has("chatgpt") || !map.has("gemini")) {
      const { results: ek } = await env.DB.prepare("SELECT saglayici, device_id, t, w_reset, ham FROM saglayici_limit_gecmis WHERE device_id != '' AND w_reset IS NOT NULL AND w_reset <= t + 864000 ORDER BY t DESC LIMIT 20").all();
      for (const r of ek || []) {
        if (!SAGLAYICILAR.includes(r.saglayici) || map.has(r.saglayici)) continue;
        let gercekReset = false;
        try {
          const h = JSON.parse(r.ham || "{}");
          gercekReset = Number.isFinite(h["seven_day.resets_at"]);
        } catch {}
        if (gercekReset) map.set(r.saglayici, { reset_at: r.w_reset, okundu_at: r.t || 0, cihaz_id: r.device_id || "" });
      }
    }
  } catch {}
  otoSifirlamaBellek = { t: now, map };
  return map;
}

function haftaSifirlamaBilgisi(sistemAyar, otoMap, sgl, refSec = Math.floor(Date.now() / 1000)) {
  const eff = ayarSaglayiciIcin(sistemAyar, sgl);
  const tz = eff.zamanDilimi || "Europe/Istanbul";
  const oto = otoMap && typeof otoMap.get === "function" ? otoMap.get(sgl) : (otoMap && otoMap[sgl]) || null;
  const diskSonraki = oto && oto.reset_at > 1600000000 ? sonrakiHaftaSn(oto.reset_at, refSec) : 0;
  let oku = null;
  try { oku = parcaOkuyucu(tz); } catch { oku = parcaOkuyucu("Europe/Istanbul"); }
  const dp = diskSonraki > 0 ? oku(diskSonraki) : null;
  if (eff.haftaMod === "otomatik" && diskSonraki > 0) {
    const hizali = Math.round(diskSonraki / 3600) * 3600;
    const baz = ((hizali % WEEK) + WEEK) % WEEK;
    const hp = oku(hizali);
    return {
      saglayici: sgl,
      mod: "otomatik",
      ozelMod: eff.haftaModOzel,
      kaynak: "otomatik_disk",
      baz,
      gun: hp.wd,
      saat: hp.hr,
      sonrakiReset: diskSonraki,
      hizaliReset: hizali,
      diskVar: true,
      diskSonraki,
      diskGun: dp.wd,
      diskSaat: dp.hr,
      diskDakika: dp.mn,
      diskOkunduAt: oto.okundu_at || 0,
      diskCihazId: oto.cihaz_id || "",
      yedekGun: eff.haftaGun,
      yedekSaat: eff.haftaSaat,
    };
  }
  const baz = haftaBazOf(eff, refSec);
  const sonrakiReset = weekStartFor(refSec, baz) + WEEK;
  return {
    saglayici: sgl,
    mod: eff.haftaMod,
    ozelMod: eff.haftaModOzel,
    kaynak: eff.haftaMod === "otomatik" ? "otomatik_yedek" : "elle",
    baz,
    gun: eff.haftaGun,
    saat: eff.haftaSaat,
    sonrakiReset,
    hizaliReset: sonrakiReset,
    diskVar: diskSonraki > 0,
    diskSonraki: diskSonraki || null,
    diskGun: dp ? dp.wd : null,
    diskSaat: dp ? dp.hr : null,
    diskDakika: dp ? dp.mn : null,
    diskOkunduAt: oto ? oto.okundu_at || 0 : 0,
    diskCihazId: oto ? oto.cihaz_id || "" : "",
    yedekGun: eff.haftaGun,
    yedekSaat: eff.haftaSaat,
  };
}

function haftaSifirlamaHaritasi(sistemAyar, otoMap, refSec = Math.floor(Date.now() / 1000), cihazAdMap = null) {
  const out = {};
  for (const sgl of SAGLAYICILAR) {
    const b = haftaSifirlamaBilgisi(sistemAyar, otoMap, sgl, refSec);
    out[sgl] = cihazAdMap ? { ...b, diskCihazEtiket: (b.diskCihazId && cihazAdMap.get(b.diskCihazId)) || "" } : b;
  }
  return out;
}

function weekStartSaglayici(t, sistemAyar, otoMap, sgl) {
  return weekStartFor(t, haftaSifirlamaBilgisi(sistemAyar, otoMap, sgl, t).baz);
}

const MAX_SKEW = 300;
const SESSION_DAYS = 30;
const enc = new TextEncoder();

const json = (data, status = 200, extra = {}) =>
  new Response(JSON.stringify(data), {
    status,
    headers: { "content-type": "application/json; charset=utf-8", "cache-control": "no-store", ...extra },
  });
const hexToBytes = (h) => new Uint8Array(h.match(/../g).map((b) => parseInt(b, 16)));
const toHex = (buf) => [...new Uint8Array(buf)].map((b) => b.toString(16).padStart(2, "0")).join("");
const sha256Hex = async (bytes) => toHex(await crypto.subtle.digest("SHA-256", bytes));

async function safeEqual(a, b) {
  if (typeof a !== "string" || typeof b !== "string" || !a || !b) return false;
  const [x, y] = await Promise.all([sha256Hex(enc.encode(a)), sha256Hex(enc.encode(b))]);
  let diff = 0;
  for (let i = 0; i < x.length; i++) diff |= x.charCodeAt(i) ^ y.charCodeAt(i);
  return diff === 0;
}
async function hmacHex(keyStr, msg) {
  const key = await crypto.subtle.importKey("raw", enc.encode(keyStr), { name: "HMAC", hash: "SHA-256" }, false, ["sign"]);
  return toHex(await crypto.subtle.sign("HMAC", key, enc.encode(msg)));
}

// ---------- hesaplar ve oturum ----------
// Roller:
//   super    : en üst düzey yönetici; her şeyi yapar (kullanıcılar, bilgisayarlar, fiyatlar, tüm ofisler)
//   ofis     : kullanici_ofis tablosunda ofis başına "yonetici" (o ofisin kadrosu, payları, simülasyonu, limit yüzdesi)
//              ya da "uye" (yalnızca o ofisi görür)
//   herkes   : kendi profili, şifresi, oturumları ve kendi bilgisayarları (ekle / adlandır / sil)
// Oturum çerezi: <bitiş>.u<kullanıcı>.<sürüm>.<imza> — kullanıcının oturum sürümü artırılınca tüm oturumları düşer.
// Acil durum: VIEW_KEY (kurulum anahtarı) ile giriş süper yönetici oturumu açar; ilk hesabı kurmak için de gerekir.
const KULLANICI_ADI_RE = /^[\p{L}\p{N}][\p{L}\p{N}._@-]{1,63}$/u;
const OFIS_ROLLERI = ["uye", "yonetici"];
const PBKDF2_TUR = 20000;
const TOKEN_SURE = { davet: 7 * 86400, sifirla: 24 * 3600 };
const kullaniciAdiTemizle = (s) => String(s || "").trim().toLocaleLowerCase("tr-TR").slice(0, 64);

async function sifreOzetle(sifre, tuz = toHex(crypto.getRandomValues(new Uint8Array(16))), tur = PBKDF2_TUR) {
  const anahtar = await crypto.subtle.importKey("raw", enc.encode(sifre), "PBKDF2", false, ["deriveBits"]);
  const bit = await crypto.subtle.deriveBits({ name: "PBKDF2", hash: "SHA-256", salt: enc.encode(tuz), iterations: tur }, anahtar, 256);
  return `pbkdf2$${tur}$${tuz}$${toHex(bit)}`;
}
async function sifreDogrula(sifre, kayit) {
  const p = String(kayit || "").split("$");
  if (p.length !== 4 || p[0] !== "pbkdf2") return false;
  const tur = Number(p[1]);
  if (!Number.isSafeInteger(tur) || tur < 1000 || tur > 100000) return false;
  return safeEqual(await sifreOzetle(sifre, p[2], tur), kayit);
}
function sifreKurali(s) {
  if (typeof s !== "string" || s.length < 8) return "Şifre en az 8 karakter olmalı.";
  if (s.length > 200) return "Şifre çok uzun.";
  return null;
}

const kullaniciOnbellek = new Map();
async function kullaniciGetir(env, id, zorla = false) {
  const now = Date.now();
  const k = kullaniciOnbellek.get(id);
  if (!zorla && k && now - k.t < 30000) return k.v;
  const u = await env.DB.prepare("SELECT id, kullanici_adi, ad, rol, uye_id, durum, oturum_surum, son_giris, olusturuldu FROM kullanicilar WHERE id = ?").bind(id).first();
  let ofisler = [];
  if (u) ofisler = (await env.DB.prepare("SELECT saglayici, rol FROM kullanici_ofis WHERE kullanici_id = ?").bind(id).all()).results || [];
  const v = u ? { ...u, ofisler } : null;
  kullaniciOnbellek.set(id, { t: now, v });
  if (kullaniciOnbellek.size > 500) kullaniciOnbellek.clear();
  return v;
}
let uyeOfisBellek = { t: 0, map: null };
const ESKI_CLAUDE_BAYRAKLARI = new Set(["config_dir", "ek_klasor", "api_ortam", "temizlik", "baska_kullanici", "gelecek", "editli", "buyuk", "azalan"]);
function bayrakSaglayici(kod) {
  const k = String(kod || "");
  for (const sgl of SAGLAYICILAR) {
    if (k.endsWith("_" + sgl)) return sgl;
  }
  if (ESKI_CLAUDE_BAYRAKLARI.has(k)) return "claude";
  return "";
}
async function uyeOfisHaritasiGetir(env, now) {
  if (uyeOfisBellek.map && now - uyeOfisBellek.t < 60) return uyeOfisBellek.map;
  const [kRows, oRows] = await Promise.all([
    env.DB.prepare("SELECT id, rol, uye_id, durum FROM kullanicilar WHERE durum != 'pasif' AND uye_id != ''").all(),
    env.DB.prepare("SELECT kullanici_id, saglayici FROM kullanici_ofis").all(),
  ]);
  const kulOfis = new Map();
  for (const r of oRows.results || []) {
    if (!SAGLAYICILAR.includes(r.saglayici)) continue;
    if (!kulOfis.has(r.kullanici_id)) kulOfis.set(r.kullanici_id, new Set());
    kulOfis.get(r.kullanici_id).add(r.saglayici);
  }
  const map = new Map();
  for (const u of kRows.results || []) {
    if (!map.has(u.uye_id)) map.set(u.uye_id, new Set());
    const hedef = map.get(u.uye_id);
    if (u.rol === "super") {
      for (const sgl of SAGLAYICILAR) hedef.add(sgl);
    } else {
      for (const sgl of kulOfis.get(u.id) || []) hedef.add(sgl);
    }
  }
  uyeOfisBellek = { t: now, map };
  return map;
}
function cihazKayitliOfisler(devId, uyeId, uyeOfisMap, sistemAyar) {
  const set = new Set();
  const uyeKayitli = !!(uyeId && uyeOfisMap && uyeOfisMap.has(uyeId));
  if (uyeKayitli) {
    for (const sgl of uyeOfisMap.get(uyeId)) set.add(sgl);
  }
  const sa = (sistemAyar && sistemAyar.saglayiciAyar) || {};
  for (const sgl of SAGLAYICILAR) {
    const secili = sa[sgl] && Array.isArray(sa[sgl].uyeler) ? sa[sgl].uyeler : null;
    if (secili && ((uyeId && secili.includes(uyeId)) || (devId && secili.includes("c:" + devId)))) {
      set.add(sgl);
    }
  }
  if (!uyeKayitli && set.size === 0) {
    for (const sgl of SAGLAYICILAR) set.add(sgl);
  }
  return set;
}

const kullaniciUnut = (id) => { kullaniciOnbellek.delete(id); uyeOfisBellek = { t: 0, map: null }; };

// Erişim nesnesi: admin (süper), gor (görebildiği ofisler), yon (yönetebildiği ofisler), kullanici
function erisimOlustur(kullanici, acilAdmin = false) {
  if (acilAdmin || (kullanici && kullanici.rol === "super")) {
    return { admin: true, gor: new Set(SAGLAYICILAR), yon: new Set(SAGLAYICILAR), kullanici: kullanici || null };
  }
  if (!kullanici || kullanici.durum !== "aktif") return { admin: false, gor: new Set(), yon: new Set(), kullanici: null };
  const gor = new Set(), yon = new Set();
  for (const o of kullanici.ofisler || []) {
    if (!SAGLAYICILAR.includes(o.saglayici)) continue;
    gor.add(o.saglayici);
    if (o.rol === "yonetici") yon.add(o.saglayici);
  }
  return { admin: false, gor, yon, kullanici };
}
const BOS_ERISIM = { admin: false, gor: new Set(), yon: new Set(), kullanici: null };
const girisVar = (e) => e.admin || !!e.kullanici;

async function oturumOlustur(env, kullanici) {
  const exp = Math.floor(Date.now() / 1000) + SESSION_DAYS * 86400;
  if (!kullanici) return `${exp}.admin.${await hmacHex(env.ADMIN_KEY, `oturum:v2:${exp}:admin:`)}`;
  const s = kullanici.oturum_surum || 1;
  return `${exp}.u${kullanici.id}.${s}.${await hmacHex(env.ADMIN_KEY, `oturum:v3:${exp}:${kullanici.id}:${s}`)}`;
}
async function erisimCoz(req, env) {
  if (await isAdminBearer(req, env)) return erisimOlustur(null, true);
  const ck = (req.headers.get("cookie") || "").match(/(?:^|;\s*)oturum=([^;]+)/);
  if (!ck) return BOS_ERISIM;
  const p = ck[1].split(".");
  if (!/^[0-9]+$/.test(p[0]) || Number(p[0]) < Date.now() / 1000) return BOS_ERISIM;
  // Acil durum yönetici oturumu (VIEW_KEY ile açılmış)
  if (p.length === 3 && p[1] === "admin" && /^[a-f0-9]{64}$/.test(p[2])) {
    return (await safeEqual(p[2], await hmacHex(env.ADMIN_KEY, `oturum:v2:${p[0]}:admin:`))) ? erisimOlustur(null, true) : BOS_ERISIM;
  }
  if (p.length !== 4 || !/^u[a-f0-9]{16,32}$/.test(p[1]) || !/^[0-9]{1,9}$/.test(p[2]) || !/^[a-f0-9]{64}$/.test(p[3])) return BOS_ERISIM;
  const id = p[1].slice(1);
  if (!(await safeEqual(p[3], await hmacHex(env.ADMIN_KEY, `oturum:v3:${p[0]}:${id}:${p[2]}`)))) return BOS_ERISIM;
  const u = await kullaniciGetir(env, id);
  if (!u || u.durum !== "aktif" || String(u.oturum_surum) !== p[2]) return BOS_ERISIM;
  return erisimOlustur(u);
}
const yetkili = (erisim, sgl) => erisim.gor.has(sgl);
async function isAdminBearer(req, env) {
  const auth = req.headers.get("authorization") || "";
  return auth.startsWith("Bearer ") && safeEqual(auth.slice(7), env.ADMIN_KEY);
}
const cookie = (val, maxAge) => `oturum=${val}; Path=/; HttpOnly; Secure; SameSite=Strict; Max-Age=${maxAge}`;

// Çerezle yapılan POST'larda başka siteden gelen istekleri reddet
function sameOrigin(req, url) {
  const o = req.headers.get("origin");
  return !o || o === url.origin;
}

async function denemeSiniri(env, anahtar) {
  const now = Math.floor(Date.now() / 1000);
  const f = await env.DB.prepare("SELECT count, window_start FROM login_fail WHERE ip = ?").bind(anahtar).first();
  return { asildi: !!(f && now - f.window_start < 900 && f.count >= 10), f, now };
}
async function denemeYaz(env, anahtar, f, now) {
  if (!f || now - f.window_start >= 900) await env.DB.prepare("INSERT OR REPLACE INTO login_fail (ip, count, window_start) VALUES (?,1,?)").bind(anahtar, now).run();
  else await env.DB.prepare("UPDATE login_fail SET count = count + 1 WHERE ip = ?").bind(anahtar).run();
}
async function oranDenetle(env, anahtar, limit = 30, pencereSn = 60) {
  const now = Math.floor(Date.now() / 1000);
  const f = await env.DB.prepare("SELECT count, window_start FROM login_fail WHERE ip = ?").bind(anahtar).first();
  if (f && now - f.window_start < pencereSn && f.count >= limit) return false;
  if (!f || now - f.window_start >= pencereSn) {
    await env.DB.prepare("INSERT OR REPLACE INTO login_fail (ip, count, window_start) VALUES (?,1,?)").bind(anahtar, now).run();
  } else {
    await env.DB.prepare("UPDATE login_fail SET count = count + 1 WHERE ip = ?").bind(anahtar).run();
  }
  return true;
}

async function handleLogin(req, env) {
  const ip = req.headers.get("cf-connecting-ip") || "yok";
  const { asildi, f, now } = await denemeSiniri(env, ip);
  if (asildi) return json({ hata: "Çok fazla deneme. 15 dakika sonra tekrar dene." }, 429);
  let body;
  try { body = await req.json(); } catch { return json({ hata: "json" }, 400); }
  const sifre = String(body.sifre || "");
  const ka = kullaniciAdiTemizle(body.kullanici_adi);
  if (ka) {
    const u = await env.DB.prepare("SELECT id, sifre, durum FROM kullanicilar WHERE kullanici_adi = ?").bind(ka).first();
    if (u && u.durum === "aktif" && (await sifreDogrula(sifre, u.sifre))) {
      await env.DB.batch([
        env.DB.prepare("DELETE FROM login_fail WHERE ip = ?").bind(ip),
        env.DB.prepare("UPDATE kullanicilar SET son_giris = ? WHERE id = ?").bind(now, u.id),
      ]);
      const k = await kullaniciGetir(env, u.id, true);
      return json({ tamam: true }, 200, { "set-cookie": cookie(await oturumOlustur(env, k), SESSION_DAYS * 86400) });
    }
    await denemeYaz(env, ip, f, now);
    if (u && u.durum === "davetli") return json({ hata: "Bu hesap henüz etkinleştirilmedi. Yöneticinin paylaştığı bağlantıyla şifreni belirle." }, 401);
    if (u && u.durum === "pasif") return json({ hata: "Bu hesap yönetici tarafından devre dışı bırakıldı." }, 401);
    return json({ hata: "Kullanıcı adı veya şifre yanlış." }, 401);
  }
  // Acil durum: kurulum anahtarı (VIEW_KEY)
  if (await safeEqual(sifre, env.VIEW_KEY)) {
    await env.DB.prepare("DELETE FROM login_fail WHERE ip = ?").bind(ip).run();
    return json({ tamam: true, acil: true }, 200, { "set-cookie": cookie(await oturumOlustur(env, null), SESSION_DAYS * 86400) });
  }
  await denemeYaz(env, ip, f, now);
  return json({ hata: "Kullanıcı adı ve şifreni gir." }, 401);
}

async function superVarMi(env) {
  return !!(await env.DB.prepare("SELECT 1 FROM kullanicilar WHERE rol = 'super' AND durum = 'aktif' LIMIT 1").first());
}

// İlk kurulum: hiç süper yönetici yoksa, kurulum anahtarını (VIEW_KEY) bilen kişi ilk hesabı açar
async function handleKurulum(req, env) {
  const ip = req.headers.get("cf-connecting-ip") || "yok";
  const { asildi, f, now } = await denemeSiniri(env, ip);
  if (asildi) return json({ hata: "Çok fazla deneme. 15 dakika sonra tekrar dene." }, 429);
  if (await superVarMi(env)) return json({ hata: "Kurulum zaten tamamlandı. Kullanıcı adın ve şifrenle giriş yap." }, 409);
  const b = await req.json();
  if (!(await safeEqual(String(b.anahtar || ""), env.VIEW_KEY))) { await denemeYaz(env, ip, f, now); return json({ hata: "Kurulum anahtarı yanlış." }, 401); }
  const ka = kullaniciAdiTemizle(b.kullanici_adi);
  if (!KULLANICI_ADI_RE.test(ka)) return json({ hata: "Kullanıcı adı en az 2 karakter olmalı ve boşluk içermemeli (örn. deniz)." }, 400);
  const hata = sifreKurali(b.sifre);
  if (hata) return json({ hata }, 400);
  const ad = temizMetin(b.ad, 60, ka);
  let uyeId = String(b.uye_id || "");
  let uyeVar = uyeId && (await env.DB.prepare("SELECT 1 FROM uyeler WHERE id = ?").bind(uyeId).first());
  if (!uyeVar) {
    const tumUyeler = (await env.DB.prepare("SELECT u.id, u.ad FROM uyeler u WHERE NOT EXISTS (SELECT 1 FROM kullanicilar k WHERE k.uye_id = u.id)").all()).results;
    const arananlar = new Set([ad.toLocaleLowerCase("tr-TR"), ka]);
    const eslesen = tumUyeler.find((u) => arananlar.has(String(u.ad || "").toLocaleLowerCase("tr-TR")));
    if (eslesen) { uyeId = eslesen.id; uyeVar = true; }
  }
  const id = toHex(crypto.getRandomValues(new Uint8Array(12)));
  const mevcut = await env.DB.prepare("SELECT id FROM kullanicilar WHERE kullanici_adi = ?").bind(ka).first();
  const sifre = await sifreOzetle(b.sifre);
  if (mevcut) {
    await env.DB.prepare("UPDATE kullanicilar SET rol = 'super', durum = 'aktif', sifre = ?, ad = ?, oturum_surum = oturum_surum + 1, son_giris = ? WHERE id = ?").bind(sifre, ad, now, mevcut.id).run();
  } else {
    await env.DB.prepare("INSERT INTO kullanicilar (id, kullanici_adi, ad, rol, sifre, uye_id, durum, oturum_surum, olusturuldu, son_giris) VALUES (?,?,?,?,?,?,?,1,?,?)")
      .bind(id, ka, ad, "super", sifre, uyeVar ? uyeId : "", "aktif", now, now).run();
  }
  const k = await kullaniciGetir(env, mevcut ? mevcut.id : id, true);
  return json({ tamam: true }, 200, { "set-cookie": cookie(await oturumOlustur(env, k), SESSION_DAYS * 86400) });
}

// ---------- tek kullanımlık şifre belirleme / sıfırlama bağlantıları ----------
// Davet bağlantısı 7 gün, sıfırlama bağlantısı 24 saat geçerlidir; veritabanında yalnızca SHA-256 özeti saklanır.
async function baglantiUret(env, origin, kullaniciId, tur) {
  const ham = toHex(crypto.getRandomValues(new Uint8Array(32)));
  const now = Math.floor(Date.now() / 1000);
  const son = now + TOKEN_SURE[tur];
  await env.DB.batch([
    env.DB.prepare("DELETE FROM hesap_token WHERE kullanici_id = ? OR son < ?").bind(kullaniciId, now),
    env.DB.prepare("INSERT INTO hesap_token (ozet, kullanici_id, tur, son, olusturuldu) VALUES (?,?,?,?,?)")
      .bind(await sha256Hex(enc.encode(ham)), kullaniciId, tur, son, now),
  ]);
  return { link: `${origin}/sifre?t=${ham}`, tur, son };
}
async function tokenCoz(env, ham) {
  if (!/^[a-f0-9]{64}$/.test(String(ham || ""))) return null;
  const r = await env.DB.prepare("SELECT t.ozet, t.kullanici_id, t.tur, t.son, k.kullanici_adi, k.ad, k.durum FROM hesap_token t JOIN kullanicilar k ON k.id = t.kullanici_id WHERE t.ozet = ?")
    .bind(await sha256Hex(enc.encode(ham))).first();
  if (!r || r.son < Math.floor(Date.now() / 1000) || r.durum === "pasif") return null;
  return r;
}
async function handleTokenBilgi(env, url) {
  const t = await tokenCoz(env, url.searchParams.get("t"));
  if (!t) return json({ gecerli: false });
  return json({ gecerli: true, tur: t.tur, kullanici_adi: t.kullanici_adi, ad: t.ad });
}
async function handleSifreBelirle(req, env) {
  const b = await req.json();
  const t = await tokenCoz(env, b.t);
  if (!t) return json({ hata: "Bağlantının süresi dolmuş ya da daha önce kullanılmış. Yöneticiden yeni bir bağlantı iste." }, 400);
  const hata = sifreKurali(b.sifre);
  if (hata) return json({ hata }, 400);
  const now = Math.floor(Date.now() / 1000);
  const ad = b.ad !== undefined ? temizMetin(b.ad, 60, t.ad || "") : t.ad;
  await env.DB.batch([
    env.DB.prepare("UPDATE kullanicilar SET sifre = ?, durum = 'aktif', ad = ?, oturum_surum = oturum_surum + 1, son_giris = ? WHERE id = ?").bind(await sifreOzetle(b.sifre), ad, now, t.kullanici_id),
    env.DB.prepare("DELETE FROM hesap_token WHERE kullanici_id = ?").bind(t.kullanici_id),
  ]);
  const k = await kullaniciGetir(env, t.kullanici_id, true);
  return json({ tamam: true }, 200, { "set-cookie": cookie(await oturumOlustur(env, k), SESSION_DAYS * 86400) });
}

// ---------- cihaz sağlayıcı izni ortak yardımcıları ----------
async function varsayilanUyePaketi(env, uyeId, ofisler) {
  if (uyeId) {
    const { results } = await env.DB.prepare("SELECT izinli_saglayici FROM devices WHERE uye_id = ?").bind(uyeId).all();
    if (results && results.length > 0) {
      const birlesim = new Set();
      let acikIzinVar = false;
      for (const d of results) {
        const p = paketNormalize(d.izinli_saglayici);
        if (p) {
          acikIzinVar = true;
          for (const sgl of pakettenSaglayicilar(p)) birlesim.add(sgl);
        }
      }
      if (acikIzinVar && birlesim.size > 0) return paketNormalize([...birlesim]) || "cgm";
    }
  }
  if (Array.isArray(ofisler) && ofisler.length > 0) {
    const p = paketNormalize(ofisler.map((o) => o && o.saglayici).filter(Boolean));
    if (p) return p;
  }
  return "cgm";
}

async function cihazIzinUygula(env, devId, hamIzin) {
  const id = String(devId || "").trim();
  if (!/^[a-f0-9]{32}$/.test(id)) return json({ hata: "Geçerli bir bilgisayar seçin." }, 400);
  const yeniPaket = paketNormalize(hamIzin);
  if (!yeniPaket) return json({ hata: "En az bir veri sağlayıcısı seçin." }, 400);
  const dv = await env.DB.prepare("SELECT id, flags, srv_flags, hesaplar, hesap_eposta FROM devices WHERE id = ?").bind(id).first();
  if (!dv) return json({ hata: "Bilgisayar bulunamadı." }, 404);
  const yeniSet = new Set(pakettenSaglayicilar(yeniPaket));
  const bayrakSuz = (ham) => {
    let arr = [];
    try { arr = JSON.parse(ham || "[]"); } catch {}
    if (!Array.isArray(arr)) return [];
    return arr.filter((f) => {
      if (!f || typeof f.k !== "string") return false;
      const p = bayrakSaglayici(f.k);
      return !p || yeniSet.has(p);
    });
  };
  const temizFlags = bayrakSuz(dv.flags);
  const temizSrvFlags = bayrakSuz(dv.srv_flags);
  const temizHesaplar = {};
  try {
    const hObj = JSON.parse(dv.hesaplar || "{}");
    if (hObj && typeof hObj === "object" && !Array.isArray(hObj)) {
      for (const sgl of SAGLAYICILAR) {
        if (yeniSet.has(sgl) && typeof hObj[sgl] === "string" && SID_RE.test(hObj[sgl])) temizHesaplar[sgl] = hObj[sgl];
      }
    }
  } catch {}
  const temizEpostalar = {};
  try {
    const epObj = JSON.parse(dv.hesap_eposta || "{}");
    if (epObj && typeof epObj === "object" && !Array.isArray(epObj)) {
      for (const sgl of SAGLAYICILAR) {
        const ep = epostaTemizle(epObj[sgl]);
        if (yeniSet.has(sgl) && ep) temizEpostalar[sgl] = ep;
      }
    }
  } catch {}
  await env.DB.prepare("UPDATE devices SET izinli_saglayici = ?, flags = ?, srv_flags = ?, hesaplar = ?, hesap_eposta = ? WHERE id = ?")
    .bind(yeniPaket, JSON.stringify(temizFlags), JSON.stringify(temizSrvFlags), JSON.stringify(temizHesaplar), JSON.stringify(temizEpostalar), id).run();
  okumaOnbellekTemizle(false);
  return json({ ok: true, tamam: true, device_id: id, izinli_saglayici: yeniPaket, paket: yeniPaket });
}

// ---------- kendi hesabım ----------
async function handleHesap(req, env, path, url, erisim) {
  const u = erisim.kullanici;
  if (!u) return json({ hata: "Bu işlem için kullanıcı hesabı gerekir (acil durum girişinde kullanılamaz)." }, 403);
  const now = Math.floor(Date.now() / 1000);
  const kendiCihaz = async (id) => u.uye_id && (await env.DB.prepare("SELECT id FROM devices WHERE id = ? AND uye_id = ?").bind(String(id || ""), u.uye_id).first());
  if (path === "/api/hesap" && req.method === "GET") {
    const hamCihazlar = u.uye_id
      ? (await env.DB.prepare("SELECT id, label, created_at, last_seen, client_ver, os, srv_flags, izinli_saglayici, calisan_paket FROM devices WHERE uye_id = ? ORDER BY created_at").bind(u.uye_id).all()).results
      : [];
    const cihazlar = hamCihazlar.map(({ srv_flags, izinli_saglayici, calisan_paket, ...c }) => ({
      ...c,
      izinli_saglayici: cihazEtkinPaket(izinli_saglayici),
      calisan_paket: calisan_paket || "",
      ozel_istemci: typeof srv_flags === "string" && srv_flags.includes('"ozel_istemci"'),
    }));
    const uye = u.uye_id ? await env.DB.prepare("SELECT id, ad FROM uyeler WHERE id = ?").bind(u.uye_id).first() : null;
    return json({ kullanici: { id: u.id, kullanici_adi: u.kullanici_adi, ad: u.ad, rol: u.rol, son_giris: u.son_giris, olusturuldu: u.olusturuldu }, ofisler: u.ofisler, uye, cihazlar, latest: sonSurumOf(), now });
  }
  if (path === "/api/hesap/profil" && req.method === "POST") {
    const b = await req.json();
    const ad = temizMetin(b.ad, 60, "");
    if (!ad) return json({ hata: "Adını yaz." }, 400);
    await env.DB.prepare("UPDATE kullanicilar SET ad = ? WHERE id = ?").bind(ad, u.id).run();
    kullaniciUnut(u.id);
    okumaOnbellekTemizle(false);
    return json({ tamam: true });
  }
  if (path === "/api/hesap/sifre" && req.method === "POST") {
    const b = await req.json();
    const kayit = await env.DB.prepare("SELECT sifre FROM kullanicilar WHERE id = ?").bind(u.id).first();
    if (!(await sifreDogrula(String(b.eski || ""), kayit && kayit.sifre))) return json({ hata: "Mevcut şifren yanlış." }, 400);
    const hata = sifreKurali(b.yeni);
    if (hata) return json({ hata }, 400);
    await env.DB.prepare("UPDATE kullanicilar SET sifre = ?, oturum_surum = oturum_surum + 1 WHERE id = ?").bind(await sifreOzetle(b.yeni), u.id).run();
    const k = await kullaniciGetir(env, u.id, true);
    return json({ tamam: true }, 200, { "set-cookie": cookie(await oturumOlustur(env, k), SESSION_DAYS * 86400) });
  }
  if (path === "/api/hesap/oturumlari-kapat" && req.method === "POST") {
    await env.DB.prepare("UPDATE kullanicilar SET oturum_surum = oturum_surum + 1 WHERE id = ?").bind(u.id).run();
    const k = await kullaniciGetir(env, u.id, true);
    return json({ tamam: true }, 200, { "set-cookie": cookie(await oturumOlustur(env, k), SESSION_DAYS * 86400) });
  }
  if (path === "/api/hesap/kod" && req.method === "POST") {
    const b = await req.json();
    const l = String(b.label || "").trim().slice(0, 40);
    if (!l) return json({ hata: "Bilgisayara bir ad ver." }, 400);
    let paket;
    if (b.izinli_saglayici !== undefined) {
      paket = paketNormalize(b.izinli_saglayici);
      if (!paket) return json({ hata: "En az bir veri sağlayıcısı seçin." }, 400);
    }
    let uyeId = u.uye_id;
    if (!uyeId || !(await env.DB.prepare("SELECT 1 FROM uyeler WHERE id = ?").bind(uyeId).first())) {
      // Hesabın bir masası (üye kaydı) yoksa kendi adıyla oluşturulur
      uyeId = toHex(crypto.getRandomValues(new Uint8Array(8)));
      let ad = u.ad || u.kullanici_adi;
      if (await env.DB.prepare("SELECT 1 FROM uyeler WHERE ad = ?").bind(ad).first()) ad = `${ad} (${u.kullanici_adi})`.slice(0, 40);
      await env.DB.batch([
        env.DB.prepare("INSERT INTO uyeler (id, ad, created_at) VALUES (?,?,?)").bind(uyeId, ad, now),
        env.DB.prepare("UPDATE kullanicilar SET uye_id = ? WHERE id = ?").bind(uyeId, u.id),
      ]);
      kullaniciUnut(u.id);
    }
    if (!paket) paket = await varsayilanUyePaketi(env, uyeId, u.ofisler);
    const alphabet = "ABCDEFGHJKMNPQRSTUVWXYZ23456789";
    const s = [...crypto.getRandomValues(new Uint8Array(10))].map((x) => alphabet[x % alphabet.length]).join("");
    const code = `${s.slice(0, 5)}-${s.slice(5)}`;
    await env.DB.batch([
      env.DB.prepare("DELETE FROM pair_codes WHERE expires_at < ?").bind(now),
      env.DB.prepare("INSERT INTO pair_codes (code, label, expires_at, uye_id, izinli_saglayici, used_at) VALUES (?,?,?,?,?,0)").bind(code, l, now + 900, uyeId, paket),
    ]);
    return json({ code, label: l, expires_at: now + 900, izinli_saglayici: paket, paket });
  }
  if (path === "/api/hesap/cihaz-izin" && req.method === "POST") {
    const b = await req.json();
    const devId = String(b.device_id || b.id || "").trim();
    if (!erisim.admin && !(await kendiCihaz(devId))) return json({ hata: "Bu bilgisayar senin hesabına bağlı değil." }, 403);
    return cihazIzinUygula(env, devId, b.izinli_saglayici);
  }
  if (path === "/api/hesap/cihaz-adlandir" && req.method === "POST") {
    const b = await req.json();
    const l = String(b.label || "").trim().slice(0, 40);
    if (!l) return json({ hata: "Ad boş olamaz." }, 400);
    if (!(await kendiCihaz(b.id))) return json({ hata: "Bu bilgisayar senin hesabına bağlı değil." }, 403);
    await env.DB.prepare("UPDATE devices SET label = ? WHERE id = ?").bind(l, String(b.id)).run();
    okumaOnbellekTemizle(false);
    return json({ tamam: true });
  }
  if (path === "/api/hesap/cihaz-sil" && req.method === "POST") {
    const b = await req.json();
    if (!(await kendiCihaz(b.id))) return json({ hata: "Bu bilgisayar senin hesabına bağlı değil." }, 403);
    const id = String(b.id);
    await env.DB.batch([
      env.DB.prepare("DELETE FROM devices WHERE id = ?").bind(id),
      env.DB.prepare("DELETE FROM usage_h WHERE device_id = ?").bind(id),
      env.DB.prepare("DELETE FROM usage_5m WHERE device_id = ?").bind(id),
      env.DB.prepare("DELETE FROM haric WHERE device_id = ?").bind(id),
    ]);
    okumaOnbellekTemizle(true);
    return json({ tamam: true });
  }
  return json({ hata: "bulunamadı" }, 404);
}

// ---------- kullanıcı yönetimi (yalnızca süper yönetici) ----------
function ofisRolleriTemizle(o) {
  const out = {};
  if (!o || typeof o !== "object") return out;
  for (const s of SAGLAYICILAR) if (OFIS_ROLLERI.includes(o[s])) out[s] = o[s];
  return out;
}
async function kullaniciYonetimi(req, env, path, url, erisim) {
  const now = Math.floor(Date.now() / 1000);
  if (path === "/api/yonetim/kullanicilar" && req.method === "GET") {
    const [k, o, t] = await Promise.all([
      env.DB.prepare("SELECT id, kullanici_adi, ad, rol, uye_id, durum, son_giris, olusturuldu FROM kullanicilar ORDER BY olusturuldu").all(),
      env.DB.prepare("SELECT kullanici_id, saglayici, rol FROM kullanici_ofis").all(),
      env.DB.prepare("SELECT kullanici_id, tur, son FROM hesap_token WHERE son > ?").bind(now).all(),
    ]);
    const ofis = new Map(), bekleyen = new Map();
    for (const r of o.results || []) { if (!ofis.has(r.kullanici_id)) ofis.set(r.kullanici_id, {}); ofis.get(r.kullanici_id)[r.saglayici] = r.rol; }
    for (const r of t.results || []) bekleyen.set(r.kullanici_id, { tur: r.tur, son: r.son });
    return json({
      kullanicilar: (k.results || []).map((x) => ({ ...x, ofisler: ofis.get(x.id) || {}, bekleyen: bekleyen.get(x.id) || null })),
      ben: erisim.kullanici ? erisim.kullanici.id : null,
      now,
    });
  }
  if (path === "/api/yonetim/kullanici" && req.method === "POST") {
    const b = await req.json();
    const ka = kullaniciAdiTemizle(b.kullanici_adi);
    if (!KULLANICI_ADI_RE.test(ka)) return json({ hata: "Kullanıcı adı en az 2 karakter olmalı; yalnızca harf, rakam, nokta, tire ve alt çizgi içerebilir." }, 400);
    const ad = temizMetin(b.ad, 60, ka);
    const rol = b.rol === "super" ? "super" : "uye";
    const ofisler = ofisRolleriTemizle(b.ofisler);
    let uyeId = String(b.uye_id || "");
    if (uyeId === "yeni") {
      uyeId = toHex(crypto.getRandomValues(new Uint8Array(8)));
      let uad = ad;
      if (await env.DB.prepare("SELECT 1 FROM uyeler WHERE ad = ?").bind(uad).first()) uad = `${ad} (${ka})`.slice(0, 40);
      await env.DB.prepare("INSERT INTO uyeler (id, ad, created_at) VALUES (?,?,?)").bind(uyeId, uad, now).run();
    } else if (uyeId && !(await env.DB.prepare("SELECT 1 FROM uyeler WHERE id = ?").bind(uyeId).first())) {
      return json({ hata: "Seçilen masa (üye) bulunamadı." }, 400);
    }
    let id = String(b.id || "");
    const ayni = await env.DB.prepare("SELECT id FROM kullanicilar WHERE kullanici_adi = ?").bind(ka).first();
    if (ayni && ayni.id !== id) return json({ hata: "Bu kullanıcı adı zaten kullanılıyor." }, 409);
    const yeni = !id;
    if (yeni) {
      id = toHex(crypto.getRandomValues(new Uint8Array(12)));
      await env.DB.prepare("INSERT INTO kullanicilar (id, kullanici_adi, ad, rol, sifre, uye_id, durum, oturum_surum, olusturuldu, son_giris) VALUES (?,?,?,?,'',?,'davetli',1,?,0)")
        .bind(id, ka, ad, rol, uyeId, now).run();
    } else {
      const eski = await env.DB.prepare("SELECT id, rol, durum FROM kullanicilar WHERE id = ?").bind(id).first();
      if (!eski) return json({ hata: "Kullanıcı bulunamadı." }, 404);
      if (eski.rol === "super" && rol !== "super" && erisim.kullanici && erisim.kullanici.id === id) return json({ hata: "Kendi süper yönetici yetkini kaldıramazsın." }, 400);
      let durum = eski.durum;
      if (b.durum === "pasif" || (b.durum === "aktif" && eski.durum === "pasif")) durum = b.durum;
      if (durum === "pasif" && erisim.kullanici && erisim.kullanici.id === id) return json({ hata: "Kendi hesabını devre dışı bırakamazsın." }, 400);
      await env.DB.prepare("UPDATE kullanicilar SET kullanici_adi = ?, ad = ?, rol = ?, uye_id = ?, durum = ?, oturum_surum = CASE WHEN ? THEN oturum_surum + 1 ELSE oturum_surum END WHERE id = ?")
        .bind(ka, ad, rol, uyeId, durum, durum === "pasif" && eski.durum !== "pasif" ? 1 : 0, id).run();
    }
    await env.DB.batch([
      env.DB.prepare("DELETE FROM kullanici_ofis WHERE kullanici_id = ?").bind(id),
      ...Object.entries(ofisler).map(([s, r]) => env.DB.prepare("INSERT INTO kullanici_ofis (kullanici_id, saglayici, rol) VALUES (?,?,?)").bind(id, s, r)),
    ]);
    kullaniciUnut(id);
    const baglanti = yeni ? await baglantiUret(env, url.origin, id, "davet") : null;
    return json({ tamam: true, id, kullanici_adi: ka, baglanti });
  }
  if (path === "/api/yonetim/kullanici-baglanti" && req.method === "POST") {
    const b = await req.json();
    const u = await kullaniciGetir(env, String(b.id || ""), true);
    if (!u) return json({ hata: "Kullanıcı bulunamadı." }, 404);
    if (u.durum === "pasif") return json({ hata: "Devre dışı hesaba bağlantı üretilemez." }, 400);
    const tur = u.durum === "davetli" ? "davet" : "sifirla";
    return json({ tamam: true, ...(await baglantiUret(env, url.origin, u.id, tur)) });
  }
  if (path === "/api/yonetim/kullanici-oturum-kapat" && req.method === "POST") {
    const b = await req.json();
    const r = await env.DB.prepare("UPDATE kullanicilar SET oturum_surum = oturum_surum + 1 WHERE id = ?").bind(String(b.id || "")).run();
    kullaniciUnut(String(b.id || ""));
    return r.meta.changes ? json({ tamam: true }) : json({ hata: "Kullanıcı bulunamadı." }, 404);
  }
  if (path === "/api/yonetim/kullanici-sil" && req.method === "POST") {
    const b = await req.json();
    const id = String(b.id || "");
    if (erisim.kullanici && erisim.kullanici.id === id) return json({ hata: "Kendi hesabını silemezsin." }, 400);
    await env.DB.batch([
      env.DB.prepare("DELETE FROM kullanici_ofis WHERE kullanici_id = ?").bind(id),
      env.DB.prepare("DELETE FROM hesap_token WHERE kullanici_id = ?").bind(id),
      env.DB.prepare("DELETE FROM kullanicilar WHERE id = ?").bind(id),
    ]);
    kullaniciUnut(id);
    return json({ tamam: true }); // masası (üye kaydı), bilgisayarları ve geçmiş verileri silinmez
  }
  return null;
}

// ---------- veri alma ----------
const MODEL_RE = /^[a-z0-9][a-z0-9.\-_]{0,63}(@uzun)?$/; // @uzun: istem eşiğini aşan mesajlar (sürüm 16+)
const isCount = (n) => Number.isSafeInteger(n) && n >= 0 && n <= 1e12;

// Oturum etiketi (sid): istemci gönderir; hariç tutma kararı yalnızca sunucuda, yöneticinin verdiği kayıtlara göre verilir
const SID_RE = /^[a-f0-9]{16}$/;
const UUID_RE = /^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$/;
async function sidOf(oturum) {
  return (await sha256Hex(enc.encode("token-panosu-sid|" + oturum))).slice(0, 16);
}

// Limit okuması (istemci v12+): Claude Code'un hesap geneli rate_limits değerleri. Geçersizse sessizce yok sayılır.
const LIMIT_KEY_RE = /^[a-z0-9_.]{1,60}$/;
const LIMIT_MIN_SURUM = 17;
function limitOkumaV(l, now) {
  if (!l || typeof l !== "object" || Array.isArray(l)) return null;
  if (!Number.isSafeInteger(l.t) || l.t < now - 8 * 86400 || l.t > now + 300) return null;
  const r = l.r;
  if (!r || typeof r !== "object" || Array.isArray(r)) return null;
  const keys = Object.keys(r);
  if (keys.length === 0 || keys.length > 30) return null;
  const temiz = {};
  for (const k of keys) {
    const v = r[k];
    if (!LIMIT_KEY_RE.test(k) || typeof v !== "number" || !Number.isFinite(v)) return null;
    if ((k === "used_percentage" || k.endsWith(".used_percentage")) && (v < 0 || v > 100)) return null;
    temiz[k] = v;
  }
  return { t: l.t, r: temiz };
}

// limit_log'u (haftanın etkin değeri) geçmişten yeniden hesaplar: en yeni penceredeki en büyük okuma, taban sonrası.
// Tek SQL ifadesi: ek okuma gerektirmez. Okuma yoksa satıra dokunmaz.
function limitLogYenile(env, hs) {
  return env.DB.prepare(
    `INSERT INTO limit_log (week, pct, set_at)
     SELECT ?1, MAX(w_pct), MAX(t) FROM limit_gecmis
      WHERE t >= MAX(?1, COALESCE((SELECT limit_taban_t FROM hafta_ayar WHERE week = ?1), 0)) AND t < ?1 + ?2
        AND w_pct IS NOT NULL AND w_reset IS NOT NULL
        AND CAST(ROUND(w_reset / 3600.0) AS INTEGER) = (
          SELECT MAX(CAST(ROUND(w_reset / 3600.0) AS INTEGER)) FROM limit_gecmis
           WHERE t >= MAX(?1, COALESCE((SELECT limit_taban_t FROM hafta_ayar WHERE week = ?1), 0)) AND t < ?1 + ?2
             AND w_pct IS NOT NULL AND w_reset IS NOT NULL)
     HAVING COUNT(*) > 0
     ON CONFLICT(week) DO UPDATE SET pct = excluded.pct, set_at = excluded.set_at`
  ).bind(hs, WEEK);
}

function saglayiciLimitLogYenile(env, sgl, hs) {
  return env.DB.prepare(
    `INSERT INTO saglayici_limit_log (saglayici, week, pct, set_at)
     SELECT ?1, ?2, MAX(w_pct), MAX(t) FROM saglayici_limit_gecmis
      WHERE saglayici = ?1 AND t >= ?2 AND t < ?2 + ?3
        AND w_pct IS NOT NULL AND w_reset IS NOT NULL
        AND CAST(ROUND(w_reset / 3600.0) AS INTEGER) = (
          SELECT MAX(CAST(ROUND(w_reset / 3600.0) AS INTEGER)) FROM saglayici_limit_gecmis
           WHERE saglayici = ?1 AND t >= ?2 AND t < ?2 + ?3
             AND w_pct IS NOT NULL AND w_reset IS NOT NULL)
     HAVING COUNT(*) > 0
     ON CONFLICT(saglayici, week) DO UPDATE SET pct = excluded.pct, set_at = excluded.set_at`
  ).bind(sgl, hs, WEEK);
}

function fableKesilmisSatir() { return false; }
function fableKesimUygula(rows, dilim) { return { rows, dilim }; }

function beklenenIkiliSha256(surum, hedef) {
  if (!MANIFEST || !hedef) return "";
  if (MANIFEST.ikili && MANIFEST.ikili.surum === surum && MANIFEST.ikili.dosyalar && MANIFEST.ikili.dosyalar[hedef]) {
    return String(MANIFEST.ikili.dosyalar[hedef].sha256 || "").toLowerCase();
  }
  const g = MANIFEST.gecmis && MANIFEST.gecmis[String(surum)];
  return g && typeof g[hedef] === "string" ? g[hedef].toLowerCase() : "";
}

async function dogrulaMuhur(req, env, device, rowSecret, ts, rawSha, clientVer, osAdi, muhurZorunlu = false) {
  const muhurAna = typeof env.MUHUR_ANAHTAR === "string" ? env.MUHUR_ANAHTAR.trim().toLowerCase() : "";
  const sSurum = sonSurumOf();
  if (!/^[0-9a-f]{64}$/.test(muhurAna) || !sSurum || sSurum < MUHUR_KONTROL_SURUM) {
    return { aktif: false, gecerli: true, muhurlu: false, muaf: false, paket: "" };
  }
  // Mühür zorunluluğu henüz devreye girmemişken mühür öncesi (<31) eski betikleri geçici olarak muaf tut:
  if (clientVer < MUHUR_KONTROL_SURUM && !muhurZorunlu) {
    return { aktif: true, gecerli: true, muhurlu: false, muaf: true, paket: "" };
  }
  const xTarget = (req.headers.get("x-target") || "").trim().toLowerCase();
  const xExe = (req.headers.get("x-exe-hash") || "").trim().toLowerCase();
  const xSeal = (req.headers.get("x-seal") || "").trim().toLowerCase();
  const ua = (req.headers.get("user-agent") || "").trim();
  const ae = (req.headers.get("accept-encoding") || "").toLowerCase();
  if (!HEDEF_TAM_RE.test(xTarget) || !/^[0-9a-f]{64}$/.test(xExe) || !/^[0-9a-f]{64}$/.test(xSeal)) {
    return {
      aktif: true,
      gecerli: false,
      muhurlu: false,
      paket: "",
      kod: "eksik_muhur",
      metin: `Resmî gönderici mührü eksik (sürüm v${clientVer}, istemci: ${(ua || "bilinmiyor").slice(0, 40)}): veri alınmadı`,
    };
  }
  const hedefOs = xTarget.split("-")[0];
  if (HEDEF_OS_HARITA[hedefOs] !== osAdi || ua !== `token-panosu/${clientVer} (${hedefOs})` || !ae.includes("gzip")) {
    return {
      aktif: true,
      gecerli: false,
      muhurlu: false,
      paket: "",
      kod: "sahte_baslik",
      metin: `Gönderici ağ/sistem parmak izi resmî ikiliyle uyuşmuyor (${ua.slice(0, 40)}): veri alınmadı`,
    };
  }
  const beklenenSha = beklenenIkiliSha256(clientVer, xTarget);
  if (!beklenenSha || xExe !== beklenenSha) {
    return {
      aktif: true,
      gecerli: false,
      muhurlu: false,
      paket: "",
      kod: "degismis_ikili",
      metin: `Gönderici programında değişiklik algılandı (v${clientVer} ${xTarget} özeti resmî imzalı sürümle uyuşmuyor): veri alınmadı`,
    };
  }
  const anaKey = await crypto.subtle.importKey("raw", hexToBytes(muhurAna), { name: "HMAC", hash: "SHA-256" }, false, ["sign"]);
  const kHedefBuf = await crypto.subtle.sign("HMAC", anaKey, enc.encode(`tp-muhur-k1|${clientVer}|${xTarget}`));
  const sealKey = await crypto.subtle.importKey("raw", kHedefBuf, { name: "HMAC", hash: "SHA-256" }, false, ["verify"]);
  const payload = `muhur-v1\n${device}\n${ts}\n${clientVer}\n${xTarget}\n${beklenenSha}\n${rawSha}\n${String(rowSecret || "").slice(0, 16).toLowerCase()}`;
  const tamam = await crypto.subtle.verify("HMAC", sealKey, hexToBytes(xSeal), enc.encode(payload));
  if (!tamam) {
    return {
      aktif: true,
      gecerli: false,
      muhurlu: false,
      paket: "",
      kod: "gecersiz_muhur",
      metin: `Gönderici kriptografik mührü doğrulanamadı (özel derleme veya değiştirilmiş kod): veri alınmadı`,
    };
  }
  return { aktif: true, gecerli: true, muhurlu: true, muaf: false, paket: hedeftenPaketCikar(xTarget) };
}

async function handleIngest(req, env, ctx) {
  const device = req.headers.get("x-device") || "";
  const tsRaw = req.headers.get("x-timestamp") || "";
  const sig = req.headers.get("x-signature") || "";
  if (!/^[a-f0-9]{32}$/.test(device) || !/^\d{10}$/.test(tsRaw) || !/^[a-f0-9]{64}$/.test(sig)) {
    return json({ hata: "eksik başlık" }, 400);
  }
  const ts = Number(tsRaw);
  const now = Math.floor(Date.now() / 1000);
  if (Math.abs(now - ts) > MAX_SKEW) return json({ hata: "zaman damgası geçersiz" }, 401);

  const raw = new Uint8Array(await req.arrayBuffer());
  if (raw.length > 1024 * 1024) return json({ hata: "gövde çok büyük" }, 413);

  const row = await env.DB.prepare("SELECT secret, label, client_ver, srv_flags, izinli_saglayici, calisan_paket FROM devices WHERE id = ?").bind(device).first();
  if (!row) return json({ hata: "imza geçersiz" }, 401);
  const rawSha = await sha256Hex(raw);
  const msg = `v1\n${device}\n${ts}\n${rawSha}`;
  const key = await crypto.subtle.importKey("raw", hexToBytes(row.secret), { name: "HMAC", hash: "SHA-256" }, false, ["verify"]);
  if (!(await crypto.subtle.verify("HMAC", key, hexToBytes(sig), enc.encode(msg)))) return json({ hata: "imza geçersiz" }, 401);

  let body;
  try { body = JSON.parse(new TextDecoder().decode(raw)); } catch { return json({ hata: "json" }, 400); }
  if (body.v !== 2 || !Array.isArray(body.rows) || body.rows.length > 8000) {
    return json({ hata: "eski istemci sürümü, yeniden kurun" }, 426);
  }
  if (body.m !== undefined && (!Array.isArray(body.m) || body.m.length > 4000)) return json({ hata: "geçersiz m" }, 400);
  const clientVer = Number.isSafeInteger(body.s) && body.s >= 0 && body.s < 1e6 ? body.s : 0;
  // bs: istemcinin gösterdiği son bildirim kimliği (isteğe bağlı; geçersizse yok sayılır)
  const bs = Number.isSafeInteger(body.bs) && body.bs >= 0 ? body.bs : null;
  const osAdi = ["Darwin", "Windows", "Linux"].includes(body.o) ? body.o : null; // yalnızca bilinen üç değer

  const sistemAyarIlk = await ayarYukle(env);
  const mevcutCihazSurum = Number(row.client_ver) || 0;
  const etkinPaket = cihazEtkinPaket(row.izinli_saglayici);
  const izinliSet = new Set(pakettenSaglayicilar(etkinPaket));

  // Mühürlü sürüme (>=31 veya >=40) geçmiş bir bilgisayarda arka planda unutulmuş eski bir betik çalışırsa,
  // mühür zorunluluğu henüz kilitlenmemişken cihazın sürümünü ve verisini ezmesini engelle:
  if (
    !sistemAyarIlk.muhurZorunlu &&
    ((mevcutCihazSurum >= MUHUR_ILK_SURUM && clientVer < MUHUR_ILK_SURUM) ||
      (mevcutCihazSurum >= MUHUR_KONTROL_SURUM && clientVer < MUHUR_KONTROL_SURUM))
  ) {
    return json({ hata: "bu bilgisayar yeni sürüme geçti; eski betik devre dışı" }, 409);
  }

  const minZorunlu = sistemAyarIlk.muhurZorunlu
    ? Math.max(MUHUR_KONTROL_SURUM, sistemAyarIlk.zorunluMinSurum || MUHUR_KONTROL_SURUM)
    : 0;
  const muhur = await dogrulaMuhur(req, env, device, row.secret, ts, rawSha, clientVer, osAdi, !!sistemAyarIlk.muhurZorunlu);
  const ortamMudahale = Array.isArray(body.c) && body.c.find((f) => f && (f.k === "ortam_mudahale" || f.k === "sahte_ev") && typeof f.t === "string");

  const reddetVeIsaretle = async (kod, metin) => {
    const updRed = await env.DB.prepare("UPDATE devices SET last_ts = ?, last_seen = ? WHERE id = ? AND last_ts < ?")
      .bind(ts, now, device, ts).run();
    if (!updRed.meta.changes) return json({ hata: "tekrar gönderim" }, 409);
    let eskiF = [];
    try { eskiF = JSON.parse(row.srv_flags || "[]"); } catch {}
    const korunan = Array.isArray(eskiF)
      ? eskiF.filter((f) => f && f.k !== "mudahale_istemci" && f.k !== "eski_surum_red" && f.k !== "ozel_istemci")
      : [];
    korunan.push({ k: kod, s: "s", at: now, t: String(metin || "").slice(0, 180) });
    await env.DB.prepare("UPDATE devices SET srv_flags = ?, last_seen = ? WHERE id = ?")
      .bind(JSON.stringify(korunan), now, device).run();
    okumaOnbellekTemizle(false);
    return json({ hata: metin }, 403);
  };

  // 1) Mühürlü sürüm (>=31) iddiasıyla gelip mührü/ikili özeti tutmayan istekler her zaman anında reddedilir ve işaretlenir
  if (muhur.aktif && clientVer >= MUHUR_KONTROL_SURUM && !muhur.gecerli) {
    return reddetVeIsaretle("mudahale_istemci", muhur.metin);
  }
  // 2) Çalışma ortamı kurcalanmışsa (sahte HOME, DYLD/LD_PRELOAD, TOKEN_PANOSU_DIZIN) veri alınmaz ve işaretlenir
  if (ortamMudahale && (muhur.aktif || sistemAyarIlk.muhurZorunlu)) {
    return reddetVeIsaretle("mudahale_istemci", `${ortamMudahale.t.slice(0, 140)} — veri alınmadı`);
  }
  // 3) Tüm cihazlar hedef sürüme ulaştıktan sonra (muhurZorunlu === true): eski sürümler ve mühürsüz betikler reddedilir ve işaretlenir
  if (sistemAyarIlk.muhurZorunlu) {
    if (clientVer < minZorunlu) {
      return reddetVeIsaretle(
        "eski_surum_red",
        `Eski veya yetkisiz gönderici sürümü (v${clientVer} < zorunlu v${minZorunlu}): veri alınmadı`
      );
    }
    if (muhur.aktif && (!muhur.gecerli || !muhur.muhurlu)) {
      return reddetVeIsaretle("mudahale_istemci", muhur.metin || `Resmî gönderici mührü zorunlu (v${clientVer}): veri alınmadı`);
    }
  }

  const upd = await env.DB.prepare("UPDATE devices SET last_ts = ?, last_seen = ? WHERE id = ? AND last_ts < ?")
    .bind(ts, now, device, ts).run();
  if (!upd.meta.changes) return json({ hata: "tekrar gönderim" }, 409);
  const minH = now - 40 * 86400;
  const maxH = now + 3600;

  // Tüm satırlar yazılmadan önce doğrulanır; izinli olmayan sağlayıcıların satırları düşürülür
  const sidOk = (x) => (x === undefined || x === "" ? "" : SID_RE.test(x) ? x : null);
  const rowsV = [];
  for (const r of body.rows) {
    const vals = [r.input, r.output, r.cw5m, r.cw1h, r.cr, r.messages];
    const sid = sidOk(r.sid);
    if (sid === null || !Number.isSafeInteger(r.h) || r.h % 3600 !== 0 || r.h < minH || r.h > maxH || !MODEL_RE.test(r.model) || !vals.every(isCount)) {
      return json({ hata: "geçersiz satır" }, 400);
    }
    if (!izinliSet.has(saglayiciOf(r.model))) continue;
    rowsV.push({ h: r.h, model: r.model, sid, vals });
  }
  const dilimV = [];
  for (const r of body.m || []) {
    const vals = [r.input, r.output, r.cw5m, r.cw1h, r.cr, r.messages];
    const sid = sidOk(r.sid);
    if (sid === null || !Number.isSafeInteger(r.t) || r.t % 300 !== 0 || r.t < now - 4 * 3600 || r.t > now + 300 || !MODEL_RE.test(r.model) || !vals.every(isCount)) {
      return json({ hata: "geçersiz dilim" }, 400);
    }
    if (!izinliSet.has(saglayiciOf(r.model))) continue;
    dilimV.push({ t: r.t, model: r.model, sid, vals });
  }

  // Ortak hesap kilidi: yerel dosyalardan gelen e-posta adreslerini ve SHA-256 hesap özetlerini kontrol eder.
  // İzinli olmayan sağlayıcıların hesap/e-posta/sıfırlama verileri yok sayılır ve kilit uyumsuzluğu üretmez.
  const gelenHesap = {};
  if (body.hesap && typeof body.hesap === "object" && !Array.isArray(body.hesap)) {
    for (const sgl of SAGLAYICILAR) {
      if (!izinliSet.has(sgl)) continue;
      if (typeof body.hesap[sgl] === "string" && SID_RE.test(body.hesap[sgl])) {
        gelenHesap[sgl] = body.hesap[sgl];
      }
    }
  }
  const gelenEposta = {};
  if (body.eposta && typeof body.eposta === "object" && !Array.isArray(body.eposta)) {
    for (const sgl of SAGLAYICILAR) {
      if (!izinliSet.has(sgl)) continue;
      const ep = epostaTemizle(body.eposta[sgl]);
      if (ep) gelenEposta[sgl] = ep;
    }
  }
  const gelenHaftaReset = {};
  if (body.hr && typeof body.hr === "object" && !Array.isArray(body.hr)) {
    for (const sgl of SAGLAYICILAR) {
      if (!izinliSet.has(sgl)) continue;
      // Sürüm 27 öncesi göndericiler Claude ara havuz düşüşünü (%91 -> %44) haftalık sıfırlama sanabildiği için yok sayılır.
      if (sgl === "claude" && clientVer < 27) continue;
      // Sürüm 29 öncesi göndericiler Antigravity 3P (Claude) havuzunun sıfırlamasını Gemini haftalık sıfırlaması sanabildiği için yok sayılır.
      if (sgl === "gemini" && clientVer < 29) continue;
      const rv = Number(body.hr[sgl]);
      if (Number.isSafeInteger(rv) && rv > now - 14 * 86400 && rv <= now + 14 * 86400) {
        const sr = sonrakiHaftaSn(rv, now);
        if (sr > 0) gelenHaftaReset[sgl] = sr;
      }
    }
  }

  // Sunucudaki mevcut satırlar (gönderim penceresi): hem azalma kontrolü hem de yalnızca değişeni yazmak için.
  // D1 ücretsiz planında günlük yazma sınırı var; her 5 dakikada yüzlerce aynı satırı yeniden yazmamak gerekir.
  const pencereBasi = Math.floor(now / 3600) * 3600 - 191 * 3600;
  const dilimBasi = Math.floor((now - 4 * 3600) / 300) * 300;
  const [sistemAyar, prevH, prev5, old, kilitRows, uyeOfisMap, otoMap] = await Promise.all([
    ayarYukle(env),
    env.DB.prepare("SELECT hour, model, sid, input, output, cw5m, cw1h, cr, messages FROM usage_h WHERE device_id = ? AND hour >= ?")
      .bind(device, pencereBasi).all(),
    env.DB.prepare("SELECT bucket, model, sid, input, output, cw5m, cw1h, cr, messages FROM usage_5m WHERE device_id = ? AND bucket >= ?")
      .bind(device, dilimBasi).all(),
    env.DB.prepare("SELECT uye_id, srv_flags, limit_sorgu, bildirim_son, hesaplar, hesap_eposta FROM devices WHERE id = ?").bind(device).first(),
    env.DB.prepare("SELECT saglayici, hesap_ozet, eposta FROM hesap_kilit").all(),
    uyeOfisHaritasiGetir(env, now),
    otoSifirlamaGetir(env, now),
  ]);

  const kayitliOfisler = cihazKayitliOfisler(device, (old && old.uye_id) || "", uyeOfisMap, sistemAyar);

  const kilitMap = new Map((kilitRows.results || []).map((k) => [k.saglayici, { ozet: k.hesap_ozet || "", eposta: epostaTemizle(k.eposta) }]));
  const engelliSaglayici = new Set();
  const yeniKilitStmts = [];
  for (const sgl of SAGLAYICILAR) {
    if (!izinliSet.has(sgl)) continue;
    if (sistemAyar.hesapKilitAktif && sistemAyar.hesapKilitAktif[sgl] === false) continue;
    const h = gelenHesap[sgl] || "";
    const ep = gelenEposta[sgl] || "";
    if (!h && !ep) continue;
    const kilitli = kilitMap.get(sgl);
    if (!kilitli) {
      kilitMap.set(sgl, { ozet: h, eposta: ep });
      yeniKilitStmts.push(
        env.DB.prepare("INSERT INTO hesap_kilit (saglayici, hesap_ozet, eposta, etiket, kilitlendi_at, cihaz_id) VALUES (?,?,?,?,?,?) ON CONFLICT(saglayici) DO NOTHING")
          .bind(sgl, h, ep, row.label || "", now, device)
      );
    } else {
      if (!kilitli.eposta && ep && kilitli.ozet && h === kilitli.ozet) {
        kilitli.eposta = ep;
        yeniKilitStmts.push(env.DB.prepare("UPDATE hesap_kilit SET eposta = ? WHERE saglayici = ? AND eposta = ''").bind(ep, sgl));
      } else if (!kilitli.ozet && h && kilitli.eposta && ep === kilitli.eposta) {
        kilitli.ozet = h;
        yeniKilitStmts.push(env.DB.prepare("UPDATE hesap_kilit SET hesap_ozet = ? WHERE saglayici = ? AND hesap_ozet = ''").bind(h, sgl));
      }
      let uyumlu = true;
      if (kilitli.eposta && ep) uyumlu = ep === kilitli.eposta;
      else if (kilitli.ozet && h) uyumlu = h === kilitli.ozet;
      else if (kilitli.eposta && !ep && kilitli.ozet && !h) uyumlu = true;
      if (!uyumlu) engelliSaglayici.add(sgl);
    }
  }

  const kabulRowsE = engelliSaglayici.size ? rowsV.filter((r) => !engelliSaglayici.has(saglayiciOf(r.model))) : rowsV;
  const kabulDilimE = engelliSaglayici.size ? dilimV.filter((r) => !engelliSaglayici.has(saglayiciOf(r.model))) : dilimV;
  const { rows: kabulRowsV, dilim: kabulDilimV } = fableKesimUygula(kabulRowsE, kabulDilimE, prevH.results);

  const vkey = (v) => v.join(",");
  const eskiH = new Map(prevH.results.map((r) => [`${r.hour}|${r.model}|${r.sid}`, vkey([r.input, r.output, r.cw5m, r.cw1h, r.cr, r.messages])]));
  const eski5 = new Map(prev5.results.map((r) => [`${r.bucket}|${r.model}|${r.sid}`, vkey([r.input, r.output, r.cw5m, r.cw1h, r.cr, r.messages])]));

  // Önceden gönderilmiş saatlerin sonradan azalması (ofis bazında, hariç tutmadan bağımsız): kayıtlar silinmiş ya da değiştirilmiş olabilir.
  // Antigravity modelleri (agy-*) saat bazında "agy-*" anahtarıyla gruplanır ki v31 ("agy-gemini") -> v40 ("agy-gemini-4-carbon" vb.)
  // geçişinde model adı ayrıntılanırken sahte azalan_gemini uyarısı üretilmesin.
  const lostBySgl = { claude: 0, chatgpt: 0, gemini: 0 };
  const winFrom = Math.floor(now / 3600) * 3600 - 7 * 86400;
  const tokOf = (v) => v[0] + v[1] + v[2] + v[3] + v[4]; // mesaj sayısı hariç
  const anomaliModelAnahtari = (model) => {
    const t = tabanAd(model);
    return t.startsWith("agy-") ? "agy-*" : t;
  };
  const inc = new Map();
  for (const r of kabulRowsV) {
    const k = r.h + "|" + anomaliModelAnahtari(r.model);
    inc.set(k, (inc.get(k) || 0) + tokOf(r.vals));
  }
  const prevTok = new Map();
  for (const r of prevH.results) {
    const sgl = saglayiciOf(r.model);
    if (!izinliSet.has(sgl) || r.hour < winFrom || (r.sid && r.sid.startsWith("manuel:")) || engelliSaglayici.has(sgl) || fableKesilmisSatir(r.model, r.hour)) continue;
    const k = r.hour + "|" + anomaliModelAnahtari(r.model);
    prevTok.set(k, (prevTok.get(k) || 0) + tokOf([r.input, r.output, r.cw5m, r.cw1h, r.cr]));
  }
  for (const [k, tok] of prevTok) {
    const now_ = inc.get(k) || 0;
    if (tok - now_ > 5000 && now_ < tok * 0.95) {
      const modelAd = k.slice(k.indexOf("|") + 1);
      const sgl = saglayiciOf(modelAd);
      lostBySgl[sgl] = (lostBySgl[sgl] || 0) + (tok - now_);
    }
  }
  let oldFlags = [];
  try { oldFlags = JSON.parse((old && old.srv_flags) || "[]"); } catch {}
  let eskiHesaplar = {};
  try {
    const parsed = JSON.parse((old && old.hesaplar) || "{}");
    if (parsed && typeof parsed === "object" && !Array.isArray(parsed)) {
      for (const sgl of SAGLAYICILAR) {
        if (izinliSet.has(sgl) && typeof parsed[sgl] === "string" && SID_RE.test(parsed[sgl])) eskiHesaplar[sgl] = parsed[sgl];
      }
    }
  } catch {}
  const cihazHesaplar = { ...eskiHesaplar, ...gelenHesap };
  const cihazEpostalar = {};
  try {
    const parsedEp = JSON.parse((old && old.hesap_eposta) || "{}");
    if (parsedEp && typeof parsedEp === "object" && !Array.isArray(parsedEp)) {
      for (const sgl of SAGLAYICILAR) {
        if (!izinliSet.has(sgl)) continue;
        const ep = epostaTemizle(parsedEp[sgl]);
        if (ep) cihazEpostalar[sgl] = ep;
      }
    }
  } catch {}
  for (const sgl of SAGLAYICILAR) {
    if (!izinliSet.has(sgl)) continue;
    if (gelenEposta[sgl]) cihazEpostalar[sgl] = gelenEposta[sgl];
    else if (gelenHesap[sgl] && eskiHesaplar[sgl] && gelenHesap[sgl] !== eskiHesaplar[sgl]) delete cihazEpostalar[sgl];
  }

  // Kontrol bulguları: yalnızca izinli, kayıtlı olduğu ve ortak hesapla kullandığı ofislerin bulguları geçerlidir.
  // Farklı kişisel hesap kullanımı şüpheli/dikkat sayılmaz (s: "b" = bilgi).
  const cflags = [];
  const yeniYapiskan = [];
  if (Array.isArray(body.c)) {
    for (const f of body.c.slice(0, 20)) {
      if (f && /^[a-z0-9_]{1,32}$/.test(f.k) && (f.s === "s" || f.s === "d") && typeof f.t === "string") {
        const p = bayrakSaglayici(f.k);
        if (p && (!izinliSet.has(p) || !kayitliOfisler.has(p) || engelliSaglayici.has(p))) continue;
        const temiz = { k: f.k, s: f.s, t: f.t.slice(0, 140) };
        cflags.push(temiz);
        if ((f.k.startsWith("silinen_") || f.k.startsWith("kuculen_")) && f.s === "s") {
          yeniYapiskan.push({ ...temiz, at: now });
        }
      }
    }
  }
  const sflags = [];
  const saatFarki = Math.abs(now - ts);
  if (saatFarki > 240) {
    sflags.push({ k: "saat", s: "s", t: `Bilgisayar saati sunucudan ${saatFarki} sn farklı: sistem saatiyle oynanmış olabilir` });
  } else if (saatFarki > 120) {
    sflags.push({ k: "saat", s: "d", t: `Bilgisayar saati sunucudan ${saatFarki} sn farklı` });
  }
  for (const sgl of SAGLAYICILAR) {
    if (!izinliSet.has(sgl) || !kayitliOfisler.has(sgl) || engelliSaglayici.has(sgl)) continue;
    const ad = SAGLAYICI_AD[sgl] || sgl;
    const kod = "azalan_" + sgl;
    const kayip = lostBySgl[sgl] || 0;
    const koru = oldFlags.find((f) => (f.k === kod || (sgl === "claude" && f.k === "azalan")) && f.at && now - f.at < 48 * 3600);
    if (kayip > 0) {
      const bin = Math.max(1, Math.round(kayip / 1000));
      sflags.push({ k: kod, s: "s", at: now, t: `${ad} ofisinde önceden gönderilmiş kayıtlardan ${bin} bin token eksildi: kayıt dosyaları silinmiş ya da değiştirilmiş olabilir` });
    } else if (koru) {
      sflags.push({ ...koru, k: kod });
    }
    for (const onek of ["silinen_", "kuculen_"]) {
      const yKod = onek + sgl;
      const yeni = yeniYapiskan.find((x) => x.k === yKod);
      if (yeni) sflags.push(yeni);
      else {
        const yKoru = oldFlags.find((f) => f.k === yKod && f.at && now - f.at < 48 * 3600);
        if (yKoru) sflags.push(yKoru);
      }
    }
  }
  for (const sgl of engelliSaglayici) {
    const ad = SAGLAYICI_AD[sgl] || sgl;
    const kilitli = kilitMap.get(sgl) || { ozet: "", eposta: "" };
    const gelenAd = gelenEposta[sgl] || cihazEpostalar[sgl] || (gelenHesap[sgl] ? "#" + gelenHesap[sgl].slice(0, 8) : "ayrı hesap");
    const ortakAd = kilitli.eposta || (kilitli.ozet ? "#" + kilitli.ozet.slice(0, 8) : "ortak hesap");
    sflags.push({
      k: "farkli_hesap_" + sgl,
      s: "b",
      t: `Ayrı ${ad} hesabı (${gelenAd}): ortak hesap (${ortakAd}) dışında olduğu için ortak kotaya dahil edilmedi`,
    });
  }
  const uaHam = (req.headers.get("user-agent") || "").trim();
  if (uaHam && !RESMI_AJAN_RE.test(uaHam)) {
    sflags.push({
      k: "ozel_istemci",
      s: "b",
      t: `Resmî uygulama yerine özel betik (${uaHam.slice(0, 48)}) çalışıyor; otomatik güncellenemez.`,
    });
  }
  // Kod/ikili müdahalesi denemesi yapıldıysa 48 saat boyunca görünür tut (hemen eski haline döndürse bile gizlenemesin)
  const eskiMudahale = oldFlags.find((f) => f && f.k === "mudahale_istemci" && f.at && now - f.at < 48 * 3600);
  if (eskiMudahale) sflags.push(eskiMudahale);
  const gelenCalisanPaket = muhur.muhurlu ? muhur.paket : "";
  const yazilacak = [
    ...yeniKilitStmts,
    env.DB.prepare("UPDATE devices SET client_ver = ?, os = COALESCE(?, os), flags = ?, srv_flags = ?, bildirim_son = MAX(bildirim_son, ?), hesaplar = ?, hesap_eposta = ?, calisan_paket = ? WHERE id = ?")
      .bind(clientVer, osAdi, JSON.stringify(cflags), JSON.stringify(sflags), bs === null ? 0 : bs, JSON.stringify(cihazHesaplar), JSON.stringify(cihazEpostalar), gelenCalisanPaket, device),
  ];
  // Sid'siz eski satırlar (eski istemciler): istemci bu pencereyi etiketli olarak yeniden gönderir; çift sayılmasın diye silinir
  if (kabulRowsV.some((r) => r.sid) && prevH.results.some((r) => r.sid === "")) {
    yazilacak.push(env.DB.prepare("DELETE FROM usage_h WHERE device_id = ? AND sid = '' AND hour >= ?").bind(device, pencereBasi));
  }
  if (kabulDilimV.some((r) => r.sid) && prev5.results.some((r) => r.sid === "")) {
    yazilacak.push(env.DB.prepare("DELETE FROM usage_5m WHERE device_id = ? AND sid = '' AND bucket >= ?").bind(device, dilimBasi));
  }
  // v31 -> v40 geçişi: eski "agy-gemini" (veya değişen agy-*) satırlarının aynı sohbet (sid) için "agy-gemini-4-carbon" vb.
  // spesifik model adlarıyla yenilenmesi sırasında çift sayım oluşmaması için artık gönderilmeyen eski agy-* satırlarını temizle:
  const gelenAnahtarSet = new Set(kabulRowsV.map((r) => `${r.h}|${r.model}|${r.sid}`));
  const gelenSidSet = new Set();
  const gelenSpesifikAgySaatSet = new Set();
  for (const r of kabulRowsV) {
    if (r.model.startsWith("agy-")) {
      if (r.sid) gelenSidSet.add(r.sid);
      if (tabanAd(r.model) !== "agy-gemini") gelenSpesifikAgySaatSet.add(r.h);
    }
  }
  if (gelenSidSet.size > 0 || gelenSpesifikAgySaatSet.size > 0) {
    for (const p of prevH.results) {
      if (!p.model.startsWith("agy-") || (p.sid && p.sid.startsWith("manuel:")) || fableKesilmisSatir(p.model, p.hour)) continue;
      if (gelenAnahtarSet.has(`${p.hour}|${p.model}|${p.sid}`)) continue;
      if ((p.sid && gelenSidSet.has(p.sid)) || (!p.sid && tabanAd(p.model) === "agy-gemini" && gelenSpesifikAgySaatSet.has(p.hour))) {
        yazilacak.push(
          env.DB.prepare("DELETE FROM usage_h WHERE device_id = ? AND hour = ? AND model = ? AND sid = ?")
            .bind(device, p.hour, p.model, p.sid)
        );
      }
    }
  }
  const gelenDilimAnahtarSet = new Set(kabulDilimV.map((r) => `${r.t}|${r.model}|${r.sid}`));
  const gelenDilimSidSet = new Set();
  let gelenSpesifikAgyDilimVar = false;
  for (const r of kabulDilimV) {
    if (r.model.startsWith("agy-")) {
      if (r.sid) gelenDilimSidSet.add(r.sid);
      if (tabanAd(r.model) !== "agy-gemini") gelenSpesifikAgyDilimVar = true;
    }
  }
  if (gelenDilimSidSet.size > 0 || gelenSpesifikAgyDilimVar) {
    for (const p of prev5.results) {
      if (!p.model.startsWith("agy-") || fableKesilmisSatir(p.model, p.bucket)) continue;
      if (gelenDilimAnahtarSet.has(`${p.bucket}|${p.model}|${p.sid}`)) continue;
      if ((p.sid && gelenDilimSidSet.has(p.sid)) || (tabanAd(p.model) === "agy-gemini" && gelenSpesifikAgyDilimVar)) {
        yazilacak.push(
          env.DB.prepare("DELETE FROM usage_5m WHERE device_id = ? AND bucket = ? AND model = ? AND sid = ?")
            .bind(device, p.bucket, p.model, p.sid)
        );
      }
    }
  }
  // Devir eşlemesi (sürüm 15+): devam ettirilen sohbetlerin eski sid'li satırları kök sid altında yeniden gönderilir;
  // çift sayılmasın diye bu pencerede eski sid'li satırlar silinir. Geçersiz dv yok sayılır (istek reddedilmez).
  // Silme yeni satırların yazılmasından önce, aynı batch'in başında çalışır.
  // Hariç tutma kararı sohbete aittir: etiket köke taşınınca karar da taşınır. Eski sid hariçteyse kök sid de hariç
  // listesine eklenir (aksi halde hariç tutulan sohbetin devamı kök altında sayılmaya başlardı). İstemci zaten istediği
  // sid'i bildirebildiği için bu yeni bir yetki vermez; mevcut satırlar ezilmez (DO NOTHING).
  const gonderilenSid = new Set(kabulRowsV.map((r) => r.sid).concat(kabulDilimV.map((r) => r.sid)));
  const eskiSidler = [];
  const gecerliCiftler = []; // gonderilenSid filtresinden bağımsız: tüm geçerli [eski, kök] çiftleri
  if (body.dv && typeof body.dv === "object" && !Array.isArray(body.dv)) {
    const girdiler = Object.entries(body.dv);
    if (girdiler.length > 0 && girdiler.length <= 200) {
      for (const [eski, kok] of girdiler) {
        if (typeof kok === "string" && SID_RE.test(eski) && SID_RE.test(kok) && eski !== kok) {
          gecerliCiftler.push([eski, kok]);
          if (!gonderilenSid.has(eski)) eskiSidler.push(eski);
        }
      }
    }
  }
  // Hariç devri: ek okuma yalnızca dv doluysa
  if (gecerliCiftler.length > 0) {
    const kokOf = new Map(gecerliCiftler);
    const eskiler = [...kokOf.keys()];
    const haricKokler = new Set();
    for (let i = 0; i < eskiler.length; i += 90) {
      const parca = eskiler.slice(i, i + 90);
      const { results } = await env.DB.prepare(`SELECT sid FROM haric WHERE device_id = ? AND sid IN (${parca.map(() => "?").join(",")})`)
        .bind(device, ...parca).all();
      for (const r of results) haricKokler.add(kokOf.get(r.sid));
    }
    for (const kok of haricKokler) {
      yazilacak.push(env.DB.prepare("INSERT INTO haric (device_id, sid, added_at, note) VALUES (?,?,?,?) ON CONFLICT(device_id, sid) DO NOTHING")
        .bind(device, kok, now, "devam eden sohbetten devralındı"));
    }
  }
  // D1: ifade başına en çok 100 bağlı değişken; 90'ar sid'lik parçalarla tek ifadede toplanır
  for (let i = 0; i < eskiSidler.length; i += 90) {
    const parca = eskiSidler.slice(i, i + 90);
    const yer = parca.map(() => "?").join(",");
    yazilacak.push(env.DB.prepare(`DELETE FROM usage_h WHERE device_id = ? AND hour >= ? AND sid IN (${yer})`).bind(device, pencereBasi, ...parca));
    yazilacak.push(env.DB.prepare(`DELETE FROM usage_5m WHERE device_id = ? AND bucket >= ? AND sid IN (${yer})`).bind(device, dilimBasi, ...parca));
  }
  // 14 saatten eski 5-dakikalık dilimleri saatte bir temizle (/api/hafta son 12 saati, gönderim son 4 saati kullanır)
  if (now % 3600 < 300) {
    yazilacak.push(env.DB.prepare("DELETE FROM usage_5m WHERE device_id = ? AND bucket < ?").bind(device, now - 14 * 3600));
  }

  const lim = !izinliSet.has("claude") || body.l === undefined || clientVer < LIMIT_MIN_SURUM || engelliSaglayici.has("claude") ? null : limitOkumaV(body.l, now);
  if (lim && clientVer < 27) delete lim.r["seven_day.resets_at"];
  if (!gelenHaftaReset.claude && lim && Number.isFinite(lim.r["seven_day.resets_at"]) && lim.r["seven_day.resets_at"] <= lim.t + 10 * 86400) {
    const sr = sonrakiHaftaSn(lim.r["seven_day.resets_at"], now);
    if (sr > 0) gelenHaftaReset.claude = sr;
  }
  const kabulPlim = {};
  if (body.lp && typeof body.lp === "object" && !Array.isArray(body.lp) && clientVer >= 18) {
    for (const sgl of ["chatgpt", "gemini"]) {
      if (!izinliSet.has(sgl) || engelliSaglayici.has(sgl) || body.lp[sgl] === undefined) continue;
      const plim = limitOkumaV(body.lp[sgl], now);
      if (!plim) continue;
      if (sgl === "gemini" && clientVer < 29) delete plim.r["seven_day.resets_at"];
      kabulPlim[sgl] = plim;
      if (!gelenHaftaReset[sgl] && Number.isFinite(plim.r["seven_day.resets_at"]) && plim.r["seven_day.resets_at"] <= plim.t + 10 * 86400) {
        const sr = sonrakiHaftaSn(plim.r["seven_day.resets_at"], now);
        if (sr > 0) gelenHaftaReset[sgl] = sr;
      }
    }
  }
  // Diskten okunan haftalık sıfırlama zamanı: yalnızca izinli, kayıtlı olduğu ofiste ve ortak hesabı kullanan cihazlar güncelleyebilir
  for (const sgl of SAGLAYICILAR) {
    const yeniReset = gelenHaftaReset[sgl];
    if (!yeniReset || !izinliSet.has(sgl) || !kayitliOfisler.has(sgl) || engelliSaglayici.has(sgl)) continue;
    const mevcutOto = otoMap.get(sgl);
    const degisti = !mevcutOto || Math.abs((mevcutOto.reset_at || 0) - yeniReset) >= 60 || now - (mevcutOto.okundu_at || 0) >= 3600;
    otoMap.set(sgl, { reset_at: yeniReset, okundu_at: now, cihaz_id: device });
    if (degisti) {
      yazilacak.push(
        env.DB.prepare(
          `INSERT INTO oto_hafta_sifirlama (saglayici, reset_at, okundu_at, cihaz_id) VALUES (?,?,?,?)
           ON CONFLICT(saglayici) DO UPDATE SET reset_at = excluded.reset_at, okundu_at = excluded.okundu_at, cihaz_id = excluded.cihaz_id`
        ).bind(sgl, yeniReset, now, device)
      );
      const yeniHs = weekStartSaglayici(now, sistemAyar, otoMap, sgl);
      yazilacak.push(sgl === "claude" ? limitLogYenile(env, yeniHs) : saglayiciLimitLogYenile(env, sgl, yeniHs));
    }
  }

  await env.DB.batch(yazilacak);

  // Tüm kayıtlı bilgisayarlar hedef mühürlü sürüme (>=40 ve güncel sürüme) geçtiği anda kilidi otomatik devreye al:
  const sSurum = sonSurumOf();
  const hedefSurum = Math.max(MUHUR_ILK_SURUM, sSurum);
  if (muhur.aktif && muhur.gecerli && muhur.muhurlu && clientVer >= hedefSurum && sSurum >= MUHUR_ILK_SURUM) {
    if (!sistemAyar.muhurZorunlu || (sistemAyar.zorunluMinSurum || 0) < hedefSurum) {
      const sayim = await env.DB.prepare(
        "SELECT COUNT(*) AS top, SUM(CASE WHEN client_ver < ? THEN 1 ELSE 0 END) AS eskiHedef FROM devices"
      ).bind(hedefSurum).first();
      if (sayim && Number(sayim.top) > 0 && Number(sayim.eskiHedef) === 0) {
        await ayarKaydet(env, { muhurZorunlu: true, zorunluMinSurum: hedefSurum });
      }
    }
  }

  // Limit okuması: cihaz başına geçmiş tablosuna yazılır (değişince ya da 30 dk'da bir). Ek okuma: cihazın son satırı (1 sorgu).
  // Etkin haftalık değer = pencere içindeki en büyük okuma (boştaki bir oturum eski yüzdesini taze damgayla gönderebilir).
  // Geçersizse gönderim yine de kabul edilir.
  // Sürüm 17+: yalnızca Claude masaüstü uygulamasının kendi dosyasından okunan değer kabul edilir.
  // Eski sürümler durum satırından ya da kullanım adresinden okuyordu (eski/yanlış değer gelebiliyordu): yok sayılır.
  if (lim) {
    const hs = weekStartSaglayici(lim.t, sistemAyar, otoMap, "claude");
    const s = satirOf(lim, hs + WEEK);
    if (s.w_pct !== null || s.h_pct !== null) {
      const son = await env.DB.prepare("SELECT t, w_pct, w_reset, h_pct, h_reset FROM limit_gecmis WHERE device_id = ? ORDER BY t DESC LIMIT 1").bind(device).first();
      if (yeniSatirMi(son, s)) {
        const ls = [
          env.DB.prepare("INSERT OR IGNORE INTO limit_gecmis (device_id, t, w_pct, w_reset, h_pct, h_reset, ham) VALUES (?,?,?,?,?,?,?)")
            .bind(device, s.t, s.w_pct, s.w_reset, s.h_pct, s.h_reset, JSON.stringify(lim.r)),
          env.DB.prepare(
            `INSERT INTO limit_okuma (id, okundu_at, device_id, ham) VALUES (1,?,?,?)
             ON CONFLICT(id) DO UPDATE SET okundu_at = excluded.okundu_at, device_id = excluded.device_id, ham = excluded.ham
             WHERE excluded.okundu_at > limit_okuma.okundu_at`
          ).bind(lim.t, device, JSON.stringify(lim.r)),
        ];
        if (s.w_pct !== null) ls.push(limitLogYenile(env, hs));
        await env.DB.batch(ls);
      }
    }
  }

  // Ek sağlayıcı limit okumaları (sürüm 18+: ör. Codex yerel rate_limits, Antigravity agy statusLine / userStatus)
  for (const sgl of ["chatgpt", "gemini"]) {
    const plim = kabulPlim[sgl];
    if (!plim) continue;
    const hs = weekStartSaglayici(plim.t, sistemAyar, otoMap, sgl);
    const s = satirOf(plim, hs + WEEK);
    const ekPencereVar = Number.isFinite(plim.r["thirty_day.used_percentage"]) ||
      Number.isFinite(plim.r["agy_gemini.used_percentage"]) ||
      Number.isFinite(plim.r["agy_claude.used_percentage"]);
    if (s.w_pct === null && s.h_pct === null && !ekPencereVar) continue;
    const hamStr = JSON.stringify(plim.r);
    const son = await env.DB.prepare("SELECT t, w_pct, w_reset, h_pct, h_reset, ham FROM saglayici_limit_gecmis WHERE saglayici = ? AND device_id = ? ORDER BY t DESC LIMIT 1")
      .bind(sgl, device).first();
    if (yeniSatirMi(son, s) || (son && s.t > son.t && son.ham !== hamStr)) {
      const ls = [
        env.DB.prepare("INSERT OR IGNORE INTO saglayici_limit_gecmis (saglayici, device_id, t, w_pct, w_reset, h_pct, h_reset, ham) VALUES (?,?,?,?,?,?,?,?)")
          .bind(sgl, device, s.t, s.w_pct, s.w_reset, s.h_pct, s.h_reset, hamStr),
      ];
      if (s.w_pct !== null) ls.push(saglayiciLimitLogYenile(env, sgl, hs));
      await env.DB.batch(ls);
    }
  }

  // Yalnızca yeni ya da değişmiş satırlar yazılır
  const yeniH = kabulRowsV.filter((r) => eskiH.get(`${r.h}|${r.model}|${r.sid}`) !== vkey(r.vals));
  const yeni5 = kabulDilimV.filter((r) => eski5.get(`${r.t}|${r.model}|${r.sid}`) !== vkey(r.vals));

  const m5 = yeni5.map((r) => env.DB.prepare(
    `INSERT INTO usage_5m (device_id, bucket, model, sid, input, output, cw5m, cw1h, cr, messages)
     VALUES (?,?,?,?,?,?,?,?,?,?)
     ON CONFLICT(device_id, bucket, model, sid) DO UPDATE SET
       input=excluded.input, output=excluded.output, cw5m=excluded.cw5m, cw1h=excluded.cw1h,
       cr=excluded.cr, messages=excluded.messages`
  ).bind(device, r.t, r.model, r.sid, ...r.vals));
  const stmts = yeniH.map((r) => env.DB.prepare(
    `INSERT INTO usage_h (device_id, hour, model, sid, input, output, cw5m, cw1h, cr, messages, updated_at)
     VALUES (?,?,?,?,?,?,?,?,?,?,?)
     ON CONFLICT(device_id, hour, model, sid) DO UPDATE SET
       input=excluded.input, output=excluded.output, cw5m=excluded.cw5m, cw1h=excluded.cw1h,
       cr=excluded.cr, messages=excluded.messages, updated_at=excluded.updated_at`
  ).bind(device, r.h, r.model, r.sid, ...r.vals, now));
  for (let i = 0; i < m5.length; i += 400) await env.DB.batch(m5.slice(i, i + 400));
  for (let i = 0; i < stmts.length; i += 400) await env.DB.batch(stmts.slice(i, i + 400));
  okumaOnbellekTemizle(false);
  // bildirimler (sürüm 14+; eskiler yok sayar): tek küçük okuma. Eşik = istemcinin bildirdiği ile sunucudaki kaydın büyüğü (tekrar gösterimi önler)
  const esik = Math.max(bs === null ? 0 : bs, (old && old.bildirim_son) || 0);
  const { results: bild } = await env.DB.prepare("SELECT id, baslik, metin FROM bildirimler WHERE id > ? AND bitis > ? ORDER BY id LIMIT 3").bind(esik, now).all();
  await fiyatYukle(env, ctx);
  let asim = null;
  try { asim = (await asimTablosu(env, now)).get(device) || null; } catch {}
  return json({ tamam: true, satir: rowsV.length, yazilan: stmts.length + m5.length, b: bild, esik: esikler(FIYAT, now * 1000), asim, paket: etkinPaket });
}

async function handleKurulumPaket(req, env, url) {
  const ip = req.headers.get("cf-connecting-ip") || "yok";
  if (!(await oranDenetle(env, "kurpaket:" + ip, 30, 60))) {
    return json({ ok: false, paket: "cgm", hata: "çok fazla istek" }, 429);
  }
  const kod = String(url.searchParams.get("kod") || "").toUpperCase().trim();
  if (!/^[A-Z0-9]{5}-[A-Z0-9]{5}$/.test(kod)) {
    return json({ ok: false, paket: "cgm" });
  }
  const now = Math.floor(Date.now() / 1000);
  const row = await env.DB.prepare("SELECT izinli_saglayici, used_at, expires_at FROM pair_codes WHERE code = ?").bind(kod).first();
  if (row && (!row.used_at || Number(row.used_at) === 0) && Number(row.expires_at) > now) {
    return json({ ok: true, paket: cihazEtkinPaket(row.izinli_saglayici) });
  }
  return json({ ok: false, paket: "cgm" });
}

async function handlePair(req, env) {
  const rawBuf = new Uint8Array(await req.arrayBuffer());
  let body;
  try { body = JSON.parse(new TextDecoder().decode(rawBuf)); } catch { return json({ hata: "json" }, 400); }
  const code = String(body.code || "").toUpperCase().trim();
  const secret = String(body.secret || "").trim().toLowerCase();
  const devId = String(body.device_id || req.headers.get("x-device") || "").trim().toLowerCase();
  const now = Math.floor(Date.now() / 1000);

  // Onarım kipi: eşleşme kodu verilmemişse mevcut cihazın kimliği + gizli anahtarı (veya HMAC imzası) doğrulanır
  // ve kayıtlı izinli_saglayici korunarak hedef paket döndürülür.
  if (!code && /^[a-f0-9]{32}$/.test(devId)) {
    const mevcut = await env.DB.prepare("SELECT id, label, secret, izinli_saglayici FROM devices WHERE id = ?").bind(devId).first();
    if (!mevcut) return json({ hata: "cihaz bulunamadı" }, 401);
    let dogrulandi = false;
    if (/^[a-f0-9]{64}$/.test(secret) && (await safeEqual(secret, String(mevcut.secret || "").toLowerCase()))) {
      dogrulandi = true;
    } else {
      const tsRaw = req.headers.get("x-timestamp") || "";
      const sig = req.headers.get("x-signature") || "";
      if (/^\d{10}$/.test(tsRaw) && /^[a-f0-9]{64}$/.test(sig) && Math.abs(now - Number(tsRaw)) <= MAX_SKEW) {
        const rawSha = await sha256Hex(rawBuf);
        const msg = `v1\n${devId}\n${Number(tsRaw)}\n${rawSha}`;
        const key = await crypto.subtle.importKey("raw", hexToBytes(mevcut.secret), { name: "HMAC", hash: "SHA-256" }, false, ["verify"]);
        dogrulandi = await crypto.subtle.verify("HMAC", key, hexToBytes(sig), enc.encode(msg));
      }
    }
    if (!dogrulandi) return json({ hata: "imza geçersiz" }, 401);
    const paket = cihazEtkinPaket(mevcut.izinli_saglayici);
    return json({ ok: true, tamam: true, device_id: mevcut.id, label: mevcut.label, izinli_saglayici: paket, paket });
  }

  if (!/^[A-Z0-9]{5}-[A-Z0-9]{5}$/.test(code) || !/^[a-f0-9]{64}$/.test(secret)) return json({ hata: "geçersiz" }, 400);
  const pc = await env.DB.prepare(
    "UPDATE pair_codes SET used_at = ? WHERE code = ? AND (used_at = 0 OR used_at IS NULL) AND expires_at > ? RETURNING label, uye_id, izinli_saglayici"
  ).bind(now, code, now).first();
  if (!pc) return json({ hata: "kod geçersiz ya da süresi dolmuş" }, 401);

  const kodIzin = paketNormalize(pc.izinli_saglayici);
  const eskiSecret = String(body.eski_secret || "").trim().toLowerCase();
  if (/^[a-f0-9]{32}$/.test(devId) && /^[a-f0-9]{64}$/.test(eskiSecret)) {
    const mevcut = await env.DB.prepare("SELECT id, secret, izinli_saglayici FROM devices WHERE id = ?").bind(devId).first();
    if (mevcut && (await safeEqual(eskiSecret, String(mevcut.secret || "").toLowerCase()))) {
      const korunanIzin = kodIzin || paketNormalize(mevcut.izinli_saglayici);
      const paket = cihazEtkinPaket(korunanIzin);
      await env.DB.prepare("UPDATE devices SET label = ?, secret = ?, uye_id = COALESCE(NULLIF(?, ''), uye_id), izinli_saglayici = ? WHERE id = ?")
        .bind(pc.label, secret, pc.uye_id || "", korunanIzin, devId).run();
      okumaOnbellekTemizle(true);
      return json({ device_id: devId, label: pc.label, izinli_saglayici: paket, paket });
    }
  }

  const id = toHex(crypto.getRandomValues(new Uint8Array(16)));
  const kaydedilenIzin = kodIzin || "";
  const paket = cihazEtkinPaket(kaydedilenIzin);
  await env.DB.prepare(
    "INSERT INTO devices (id, label, secret, created_at, uye_id, bildirim_son, izinli_saglayici) VALUES (?,?,?,?,?,(SELECT COALESCE(MAX(id), 0) FROM bildirimler),?)"
  ).bind(id, pc.label, secret, now, pc.uye_id || "", kaydedilenIzin).run();
  okumaOnbellekTemizle(true);
  return json({ device_id: id, label: pc.label, izinli_saglayici: paket, paket });
}

// ---------- D1 okuma önbelleği (ücretsiz katman günlük 5M rows_read sınırını korumak için) ----------
const haftaYanitOnbellek = new Map();
let ilkSaatOnbellek = { t: 0, results: null };
let railGecmisOnbellek = { t: 0, anahtar: "", results: null };

function okumaOnbellekTemizle(gecmisDahil = false) {
  asimOnbellek = null;
  haftaYanitOnbellek.clear();
  if (gecmisDahil) {
    ilkSaatOnbellek = { t: 0, results: null };
    railGecmisOnbellek = { t: 0, anahtar: "", results: null };
    uyeOfisBellek = { t: 0, map: null };
    otoSifirlamaBellek = { t: 0, map: null };
  }
}

async function ilkSaatleriGetir(env, now) {
  if (ilkSaatOnbellek.results && now - ilkSaatOnbellek.t < 3600) return ilkSaatOnbellek.results;
  const r = await env.DB.prepare("SELECT model, MIN(hour) h FROM usage_v GROUP BY model").all();
  ilkSaatOnbellek = { t: now, results: r.results || [] };
  return ilkSaatOnbellek.results;
}

async function railGecmisGetir(env, baz, from, ustSinir, now) {
  if (from >= ustSinir) return [];
  const anahtar = `${baz}|${from}|${ustSinir}`;
  if (railGecmisOnbellek.results && railGecmisOnbellek.anahtar === anahtar && now - railGecmisOnbellek.t < 300) {
    return railGecmisOnbellek.results;
  }
  const r = await env.DB.prepare(
    `SELECT ((hour - CAST(? AS INTEGER)) / CAST(? AS INTEGER)) AS w, device_id, model,
            SUM(input) input, SUM(output) output, SUM(cw5m) cw5m, SUM(cw1h) cw1h, SUM(cr) cr
       FROM usage_v WHERE hour >= ? AND hour < ? GROUP BY w, device_id, model`
  ).bind(baz, WEEK, from, ustSinir).all();
  railGecmisOnbellek = { t: now, anahtar, results: r.results || [] };
  return railGecmisOnbellek.results;
}

// ---------- okuma ----------
async function handleWeek(env, url, ctx) {
  await fiyatYukle(env, ctx);
  const now = Math.floor(Date.now() / 1000);
  const [sistemAyar, otoMap] = await Promise.all([ayarYukle(env), otoSifirlamaGetir(env, now)]);
  const saglayici = saglayiciSec(url.searchParams.get("saglayici"));
  const claudeMu = saglayici === "claude";
  const sglOk = (r) => saglayiciOf(r.model) === saglayici;
  const hsHarita = haftaSifirlamaHaritasi(sistemAyar, otoMap, now);
  const hsBilgi = hsHarita[saglayici];
  const baz = hsBilgi.baz;
  const curMap = Object.fromEntries(SAGLAYICILAR.map((s) => [s, weekStartFor(now, hsHarita[s].baz)]));
  const current = curMap[saglayici];
  let start = Number(url.searchParams.get("start") || current);
  if (!Number.isSafeInteger(start)) start = current;
  start = weekStartFor(start, baz);
  const end = start + WEEK;
  const ozetImza = SAGLAYICILAR.map((s) => `${s}:${hsHarita[s].baz}:${hsHarita[s].kaynak}`).join(",");
  const onbellekAnahtari = `${saglayici}|${start}|${ozetImza}`;
  const onbellekKaydi = haftaYanitOnbellek.get(onbellekAnahtari);
  if (onbellekKaydi && now - onbellekKaydi.t < 45) {
    return json({ ...onbellekKaydi.veri, now });
  }

  const sorguAlt = start === current ? Math.min(start, ...SAGLAYICILAR.map((s) => curMap[s])) : start;
  const sorguUst = start === current ? Math.max(end, ...SAGLAYICILAR.map((s) => curMap[s] + WEEK)) : end;
  const railFrom = Math.min(current - 11 * WEEK, start);
  const recentFrom = Math.floor(now / 3600) * 3600 - 47 * 3600;
  const liveFrom = Math.floor(now / 3600) * 3600 - 3600;
  const recentHaftaIcinde = start === current && recentFrom >= sorguAlt;

  const [devices, railGecmis, railSimdiki, firstList, lim, hourly, recentSorgu, five, uyeler, okuma, ayar, h5rows, fableOkuma, gecmisW, fableW, kilitRows, uyeOfisMap] = await Promise.all([
    env.DB.prepare("SELECT id, label, created_at, last_seen, client_ver, os, flags, srv_flags, uye_id, hesaplar, hesap_eposta, izinli_saglayici, calisan_paket FROM devices ORDER BY created_at").all(),
    railGecmisGetir(env, baz, railFrom, current, now),
    start === current
      ? Promise.resolve(null)
      : env.DB.prepare(
          `SELECT ((hour - CAST(? AS INTEGER)) / CAST(? AS INTEGER)) AS w, device_id, model,
                  SUM(input) input, SUM(output) output, SUM(cw5m) cw5m, SUM(cw1h) cw1h, SUM(cr) cr
             FROM usage_v WHERE hour >= ? GROUP BY w, device_id, model`
        ).bind(baz, WEEK, current).all(),
    ilkSaatleriGetir(env, now),
    claudeMu
      ? env.DB.prepare("SELECT pct, set_at FROM limit_log WHERE week = ?").bind(start).first()
      : env.DB.prepare("SELECT pct, set_at FROM saglayici_limit_log WHERE saglayici = ? AND week = ?").bind(saglayici, start).first(),
    env.DB.prepare(
      `SELECT device_id, hour, model, SUM(input) input, SUM(output) output, SUM(cw5m) cw5m, SUM(cw1h) cw1h, SUM(cr) cr, SUM(messages) messages
         FROM usage_v WHERE hour >= ? AND hour < ? GROUP BY device_id, hour, model`
    ).bind(sorguAlt, sorguUst).all(),
    recentHaftaIcinde
      ? Promise.resolve(null)
      : env.DB.prepare(
          `SELECT device_id, hour, model, SUM(input) input, SUM(output) output, SUM(cw5m) cw5m, SUM(cw1h) cw1h, SUM(cr) cr, SUM(messages) messages
             FROM usage_v WHERE hour >= ? GROUP BY device_id, hour, model`
        ).bind(recentFrom).all(), // grafik: son 24 saat + önceki 24 saat
    env.DB.prepare(
      `SELECT device_id, bucket, model, SUM(input) input, SUM(output) output, SUM(cw5m) cw5m, SUM(cw1h) cw1h, SUM(cr) cr, SUM(messages) messages
         FROM usage5_v WHERE bucket >= ? GROUP BY device_id, bucket, model`
    ).bind(Math.floor(now / 300) * 300 - 143 * 300).all(), // grafik: son 6 saat + önceki 6 saat
    env.DB.prepare("SELECT id, ad, created_at FROM uyeler ORDER BY created_at, ad").all(),
    claudeMu
      ? env.DB.prepare("SELECT okundu_at, ham FROM limit_okuma WHERE id = 1").first()
      : env.DB.prepare("SELECT t AS okundu_at, ham FROM saglayici_limit_gecmis WHERE saglayici = ? ORDER BY t DESC LIMIT 1").bind(saglayici).first(),
    claudeMu
      ? env.DB.prepare("SELECT limit_taban_t FROM hafta_ayar WHERE week = ?").bind(start).first()
      : Promise.resolve(null),
    // 5 saatlik pencere: son 6 saatin okumaları (cihaz sayısı x birkaç satır)
    claudeMu
      ? env.DB.prepare("SELECT device_id, t, h_pct, h_reset FROM limit_gecmis WHERE t >= ? AND h_pct IS NOT NULL AND h_reset > ?").bind(now - 6 * 3600, now).all()
      : env.DB.prepare("SELECT device_id, t, h_pct, h_reset FROM saglayici_limit_gecmis WHERE saglayici = ? AND t >= ? AND h_pct IS NOT NULL AND h_reset > ?").bind(saglayici, now - 6 * 3600, now).all(),
    // Fable yüzdesi panodan elle girilir: bu haftanın en yeni girişi (yalnızca Claude)
    claudeMu
      ? env.DB.prepare("SELECT t, ham FROM limit_gecmis WHERE t >= ? AND ham LIKE '%fable%' ORDER BY t DESC LIMIT 1").bind(current).first()
      : Promise.resolve(null),
    // Gün seçimi için: haftanın limit okumaları (o günün sonundaki yüzde) ve Fable okumaları
    claudeMu
      ? env.DB.prepare("SELECT t, w_pct, w_reset FROM limit_gecmis WHERE t >= ? AND t < ? AND w_pct IS NOT NULL ORDER BY t LIMIT 5000").bind(start, end).all()
      : env.DB.prepare("SELECT t, w_pct, w_reset FROM saglayici_limit_gecmis WHERE saglayici = ? AND t >= ? AND t < ? AND w_pct IS NOT NULL ORDER BY t LIMIT 5000").bind(saglayici, start, end).all(),
    claudeMu
      ? env.DB.prepare("SELECT t, ham FROM limit_gecmis WHERE t >= ? AND t < ? AND ham LIKE '%fable%' ORDER BY t LIMIT 3000").bind(start, end).all()
      : Promise.resolve({ results: [] }),
    env.DB.prepare("SELECT saglayici, hesap_ozet, eposta, etiket, kilitlendi_at, cihaz_id FROM hesap_kilit").all(),
    uyeOfisHaritasiGetir(env, now),
  ]);

  // Seçili ofisin kendi [start, end) aralığındaki saatlik satırlar
  const haftaHourly = hourly.results.filter((r) => r.hour >= start && r.hour < end);
  // Günlük toplamları (rows) ve son 2 saatlik canlı akışı (live) ek D1 taraması yapmadan saatlik veriden türet
  const gunlukHarita = new Map();
  for (const r of haftaHourly) {
    const d = Math.floor((r.hour - start) / 86400);
    const k = `${r.device_id}|${d}|${r.model}`;
    let g = gunlukHarita.get(k);
    if (!g) {
      g = { device_id: r.device_id, d, model: r.model, input: 0, output: 0, cw5m: 0, cw1h: 0, cr: 0, messages: 0, usd: 0 };
      gunlukHarita.set(k, g);
    }
    g.input += r.input; g.output += r.output; g.cw5m += r.cw5m; g.cw1h += r.cw1h; g.cr += r.cr; g.messages += r.messages || 0;
    g.usd += costOf(r);
  }
  const rowsResults = [...gunlukHarita.values()];
  const recentResults = recentHaftaIcinde ? hourly.results.filter((r) => r.hour >= recentFrom) : (recentSorgu ? recentSorgu.results : []);
  const simdikiW = Math.floor((current - baz) / WEEK);
  const railResults = start === current
    ? railGecmis.concat(rowsResults.map((r) => ({ ...r, w: simdikiW })))
    : railGecmis.concat(railSimdiki ? railSimdiki.results : []);

  const devIds = new Set(devices.results.map((d) => d.id));
  const cihazAdMap = new Map(devices.results.map((d) => [d.id, d.label]));
  const saglayiciOzeti = {
    claude: { usd: 0, tok: 0 },
    chatgpt: { usd: 0, tok: 0 },
    gemini: { usd: 0, tok: 0 },
  };
  for (const r of hourly.results) {
    if (!devIds.has(r.device_id)) continue;
    const s = saglayiciOf(r.model);
    const o = saglayiciOzeti[s];
    if (!o) continue;
    const altSinir = start === current ? curMap[s] : start;
    const ustSinir = altSinir + WEEK;
    if (r.hour < altSinir || r.hour >= ustSinir) continue;
    o.usd += costOf(r);
    o.tok += r.input + r.output + r.cw5m + r.cw1h + r.cr;
  }

  const hesapKilit = {};
  for (const k of kilitRows.results || []) {
    hesapKilit[k.saglayici] = { ozet: k.hesap_ozet || "", eposta: epostaTemizle(k.eposta), etiket: k.etiket, kilitlendi_at: k.kilitlendi_at, cihaz_id: k.cihaz_id };
  }

  const tabanT = (ayar && ayar.limit_taban_t) || 0;
  const etkinW = etkinHafta(gecmisW.results, tabanT);
  // Fable serisi: elle girilir; her girişte değer neyse o (düzeltme için düşebilir)
  const fseri = [];
  for (const r of fableW.results) {
    if (r.t < tabanT) continue;
    let f;
    try { f = JSON.parse(r.ham); } catch { continue; }
    const k = Object.keys(f).find((x) => /fable/i.test(x) && x.endsWith("used_percentage") && Number.isFinite(f[x]));
    if (k && (!fseri.length || f[k] !== fseri[fseri.length - 1].pct)) fseri.push({ t: r.t, pct: f[k] });
  }
  // Son okuma yalnızca içinde bulunulan hafta için anlamlı (5 saatlik ve Fable değerleri geçmiş haftaya taşınmaz)
  let ham = null;
  if (start === current && okuma) { try { ham = JSON.parse(okuma.ham); } catch {} }
  if (start === current && fableOkuma) {
    try {
      const f = JSON.parse(fableOkuma.ham);
      ham = ham || {};
      for (const k of Object.keys(f)) if (/fable/i.test(k)) ham[k] = f[k];
    } catch {}
  }
  // pct/set_at: limit_log (veya sıfırlama saati/modu değiştiğinde anında etkinW). 5 saatlik: aynı h_reset'teki en büyük okuma, yalnızca h_reset > şimdi.
  const sonManuel5 = h5rows.results.reduce((mx, r) => (r.device_id === "" && r.t > mx ? r.t : mx), 0);
  const h5Filtreli = sonManuel5 ? h5rows.results.filter((r) => r.t >= sonManuel5) : h5rows.results;
  const e5 = start === current ? etkin5(h5Filtreli, now) : null;
  const limitOut = lim || ham || etkinW || e5
    ? {
        seri: etkinW ? etkinW.seri : [],
        fseri,
        ft: start === current && fableOkuma ? fableOkuma.t : null,
        pct: lim ? lim.pct : (etkinW ? etkinW.pct : null),
        set_at: lim ? lim.set_at : (etkinW ? etkinW.set_at : null),
        h: e5 ? e5.pct : null,
        hr: e5 ? e5.reset : null,
        wr: ham && Number.isFinite(ham["seven_day.resets_at"]) ? ham["seven_day.resets_at"] : (etkinW ? etkinW.wr : hsBilgi.sonrakiReset),
        elle: !!(ham && ham.manual),
        ham,
      }
    : null;
  // dilim satırı: model başına; detay paneli için ayrıntılı alanlar (toplamlar usd/tok toplanarak aynı kalır)
  const dilim = (key) => (r) => ({ device_id: r.device_id, [key]: r[key], model: r.model, usd: costOf(r), tok: r.input + r.output + r.cw5m + r.cw1h + r.cr, input: r.input, output: r.output, cw: r.cw5m + r.cw1h, cr: r.cr, messages: r.messages || 0 });
  const hrow = dilim("hour");

  const usage = sadeBirlestir(rowsResults.filter(sglOk).map((r) => ({ ...r, usd: costOf(r) })), ["device_id", "d", "model"]);
  const weeks = new Map();
  for (const r of railResults) {
    if (!sglOk(r)) continue;
    const ws = baz + r.w * WEEK;
    if (!weeks.has(ws)) weeks.set(ws, { start: ws, usd: 0, byDevice: {}, byDeviceTok: {} });
    const w = weeks.get(ws);
    const c = typeof r.usd === "number" ? r.usd : costOf(r, (ws + 3 * 86400) * 1000);
    const t = (r.input || 0) + (r.output || 0) + (r.cw5m || 0) + (r.cw1h || 0) + (r.cr || 0);
    w.usd += c;
    w.byDevice[r.device_id] = (w.byDevice[r.device_id] || 0) + c;
    w.byDeviceTok[r.device_id] = (w.byDeviceTok[r.device_id] || 0) + t;
  }
  const liveMap = new Map();
  for (const r of recentResults) {
    if (r.hour < liveFrom || !sglOk(r)) continue;
    const cur = liveMap.get(r.device_id) || { device_id: r.device_id, output: 0, tok: 0 };
    cur.output += r.output;
    cur.tok += r.input + r.output + r.cw5m + r.cw1h + r.cr;
    liveMap.set(r.device_id, cur);
  }
  let firstH = null;
  for (const r of firstList || []) {
    if (sglOk(r) && Number.isFinite(r.h) && (firstH === null || r.h < firstH)) firstH = r.h;
  }
  const haftaSifirlamaTum = haftaSifirlamaHaritasi(sistemAyar, otoMap, now, cihazAdMap);
  const sSurum = sonSurumOf();
  const hedefMinSurum = Math.max(MUHUR_ILK_SURUM, sSurum);
  const muhurEtkinSunucu = typeof env.MUHUR_ANAHTAR === "string" && /^[0-9a-f]{64}$/i.test(env.MUHUR_ANAHTAR.trim()) && sSurum >= MUHUR_KONTROL_SURUM;
  const bekleyenCihazlar = devices.results.filter((d) => (d.client_ver || 0) < hedefMinSurum).map((d) => d.label);
  const yanitVeri = {
    now, start, end, current, latest: sSurum,
    muhurDurum: {
      aktif: muhurEtkinSunucu,
      zorunlu: !!(sistemAyar.muhurZorunlu && (sistemAyar.zorunluMinSurum || 0) >= MUHUR_ILK_SURUM),
      minSurum: sistemAyar.muhurZorunlu ? Math.max(MUHUR_KONTROL_SURUM, sistemAyar.zorunluMinSurum || MUHUR_KONTROL_SURUM) : hedefMinSurum,
      hedefSurum: hedefMinSurum,
      bekleyen: bekleyenCihazlar,
    },
    saglayici,
    saglayiciOzeti,
    hesapKilit,
    haftaSifirlama: haftaSifirlamaTum,
    taban_t: tabanT,
    ayar: ayarSaglayiciIcin(sistemAyar, saglayici),
    firstData: firstH ? weekStartFor(firstH, baz) : current,
    devices: devices.results.map(({ flags, srv_flags, hesaplar, hesap_eposta, izinli_saglayici, calisan_paket, ...d }) => {
      const kayitli = cihazKayitliOfisler(d.id, d.uye_id, uyeOfisMap, sistemAyar);
      const etkinIzin = cihazEtkinPaket(izinli_saglayici);
      const izinliSetD = new Set(pakettenSaglayicilar(etkinIzin));
      let hamF = [];
      try { hamF = [...JSON.parse(flags || "[]"), ...JSON.parse(srv_flags || "[]")]; } catch {}
      const farkliSet = new Set();
      for (const x of hamF) {
        if (x && typeof x.k === "string" && x.k.startsWith("farkli_hesap_")) {
          farkliSet.add(x.k.slice("farkli_hesap_".length));
        }
      }
      const gorulenK = new Set();
      const f = [];
      for (const x of hamF) {
        if (!x || typeof x.k !== "string" || gorulenK.has(x.k)) continue;
        gorulenK.add(x.k);
        if (x.k.startsWith("farkli_hesap_")) {
          const p = x.k.slice("farkli_hesap_".length);
          if (!izinliSetD.has(p)) continue;
          const metin = String(x.t || "").replace(/^Farklı (Claude|ChatGPT|Gemini) hesabı algılandı/, "Ayrı $1 hesabı");
          f.push({ k: x.k, s: "b", p, t: metin });
          continue;
        }
        const p = bayrakSaglayici(x.k);
        if (p && (!izinliSetD.has(p) || !kayitli.has(p) || farkliSet.has(p))) continue;
        f.push({ k: x.k, s: x.s, p, t: x.t });
      }
      let hObj = {};
      try {
        const p = JSON.parse(hesaplar || "{}");
        if (p && typeof p === "object" && !Array.isArray(p)) {
          for (const sgl of SAGLAYICILAR) if (izinliSetD.has(sgl) && p[sgl]) hObj[sgl] = p[sgl];
        }
      } catch {}
      let epObj = {};
      try {
        const pe = JSON.parse(hesap_eposta || "{}");
        if (pe && typeof pe === "object" && !Array.isArray(pe)) {
          for (const sgl of SAGLAYICILAR) {
            if (!izinliSetD.has(sgl)) continue;
            const ep = epostaTemizle(pe[sgl]);
            if (ep) epObj[sgl] = ep;
          }
        }
      } catch {}
      return {
        ...d,
        izinli_saglayici: etkinIzin,
        calisan_paket: calisan_paket || "",
        flags: f,
        kayitliOfisler: [...kayitli],
        hesaplar: hObj,
        hesapEposta: epObj,
      };
    }),
    uyeler: uyeler.results,
    usage,
    weeks: [...weeks.values()].sort((a, b) => a.start - b.start),
    live: [...liveMap.values()],
    limit: limitOut,
    hourly: sadeBirlestir(haftaHourly.filter(sglOk).map(hrow), ["device_id", "hour", "model"]),
    recent: sadeBirlestir(recentResults.filter(sglOk).map(hrow), ["device_id", "hour", "model"]),
    recent5: sadeBirlestir(five.results.filter(sglOk).map(dilim("bucket")), ["device_id", "bucket", "model"]),
  };
  if (haftaYanitOnbellek.size > 24) haftaYanitOnbellek.clear();
  haftaYanitOnbellek.set(onbellekAnahtari, { t: now, veri: yanitVeri });
  return json(yanitVeri);
}

// ---------- limit analizi ----------
// Seçili hafta için: etkin okuma serisi (monoton basamak), ham okumalar ve tüm cihazların saatlik toplamları.
async function handleLimitAnaliz(env, url, ctx) {
  await fiyatYukle(env, ctx);
  const now = Math.floor(Date.now() / 1000);
  const [sistemAyar, otoMap] = await Promise.all([ayarYukle(env), otoSifirlamaGetir(env, now)]);
  const saglayici = saglayiciSec(url.searchParams.get("saglayici"));
  const claudeMu = saglayici === "claude";
  const sglOk = (r) => saglayiciOf(r.model) === saglayici;
  const hsBilgi = haftaSifirlamaBilgisi(sistemAyar, otoMap, saglayici, now);
  const baz = hsBilgi.baz;
  const current = weekStartFor(now, baz);
  let start = Number(url.searchParams.get("start") || current);
  if (!Number.isSafeInteger(start)) start = current;
  start = weekStartFor(start, baz);
  const end = start + WEEK;
  const railFrom = current - 11 * WEEK;
  const [gecmis, ayar, saatlik, devices, rail, firstList] = await Promise.all([
    claudeMu
      ? env.DB.prepare("SELECT device_id, t, w_pct, w_reset FROM limit_gecmis WHERE t >= ? AND t < ? AND w_pct IS NOT NULL ORDER BY t LIMIT 5000").bind(start, end).all()
      : env.DB.prepare("SELECT device_id, t, w_pct, w_reset FROM saglayici_limit_gecmis WHERE saglayici = ? AND t >= ? AND t < ? AND w_pct IS NOT NULL ORDER BY t LIMIT 5000").bind(saglayici, start, end).all(),
    claudeMu
      ? env.DB.prepare("SELECT limit_taban_t FROM hafta_ayar WHERE week = ?").bind(start).first()
      : Promise.resolve(null),
    env.DB.prepare(
      `SELECT hour, model, SUM(input) input, SUM(output) output, SUM(cw5m) cw5m, SUM(cw1h) cw1h, SUM(cr) cr
         FROM usage_v WHERE hour >= ? AND hour < ? GROUP BY hour, model`
    ).bind(start, end).all(),
    env.DB.prepare("SELECT id, label FROM devices").all(),
    env.DB.prepare(
      `SELECT ((hour - CAST(? AS INTEGER)) / CAST(? AS INTEGER)) AS w, model,
              SUM(input) input, SUM(output) output, SUM(cw5m) cw5m, SUM(cw1h) cw1h, SUM(cr) cr
         FROM usage_v WHERE hour >= ? GROUP BY w, model`
    ).bind(baz, WEEK, Math.min(railFrom, start)).all(),
    ilkSaatleriGetir(env, now),
  ]);
  const taban = (ayar && ayar.limit_taban_t) || 0;
  const etkin = etkinHafta(gecmis.results, taban);
  const adlar = new Map(devices.results.map((d) => [d.id, d.label]));
  // Saatlik toplamlar: Fable'lı ve Fable'sız USD ayrı tutulur
  const saat = new Map();
  for (const r of saatlik.results) {
    if (!sglOk(r)) continue;
    let h = saat.get(r.hour);
    if (!h) saat.set(r.hour, (h = { hour: r.hour, usd: 0, usd_fablesiz: 0, tok: 0, input: 0, output: 0, cw: 0, cr: 0 }));
    const c = costOf(r);
    h.usd += c;
    if (!/fable/i.test(r.model)) h.usd_fablesiz += c;
    h.tok += r.input + r.output + r.cw5m + r.cw1h + r.cr;
    h.input += r.input; h.output += r.output; h.cw += r.cw5m + r.cw1h; h.cr += r.cr;
  }
  const weeks = new Map();
  for (const r of rail.results) {
    if (!sglOk(r)) continue;
    const ws = baz + r.w * WEEK;
    weeks.set(ws, (weeks.get(ws) || 0) + costOf(r, (ws + 3 * 86400) * 1000));
  }
  let firstH = null;
  for (const r of firstList || []) {
    if (sglOk(r) && Number.isFinite(r.h) && (firstH === null || r.h < firstH)) firstH = r.h;
  }
  const ham = gecmis.results.slice(-100).reverse().map((r) => ({
    t: r.t, device: r.device_id === "" ? "Elle düzeltme" : adlar.get(r.device_id) || "Silinmiş bilgisayar", w_pct: r.w_pct,
    eski: !!(etkin && (pencereAnahtari(r.w_reset) !== etkin.wr / SAAT || r.t < taban)), // etkin pencerenin dışında kalan okuma
  }));
  return json({
    now, start, end, current, taban_t: taban, saglayici,
    ayar: ayarSaglayiciIcin(sistemAyar, saglayici),
    firstData: firstH ? weekStartFor(firstH, baz) : current,
    weeks: [...weeks.entries()].sort((a, b) => a[0] - b[0]).map(([s, usd]) => ({ start: s, usd })),
    etkin: etkin ? { pct: etkin.pct, set_at: etkin.set_at, wr: etkin.wr } : null,
    seri: etkin ? etkin.seri : [],
    ham,
    saatlik: [...saat.values()].sort((a, b) => a.hour - b.hour),
  });
}

// ---------- yönetim ----------
// Taban yazılır, limit_log yenilenir. Tabandan sonra okuma yoksa (ve taban > 0) haftanın türetilmiş satırı kalkar: panel "okunmadı" der.
async function limitTabanYaz(env, wk, t) {
  await env.DB.prepare("INSERT INTO hafta_ayar (week, fable_ayri, limit_taban_t) VALUES (?,0,?) ON CONFLICT(week) DO UPDATE SET limit_taban_t = excluded.limit_taban_t")
    .bind(wk, t).run();
  const ls = [];
  if (t > 0) {
    ls.push(env.DB.prepare(
      `DELETE FROM limit_log WHERE week = ?1 AND NOT EXISTS (SELECT 1 FROM limit_gecmis WHERE t >= MAX(?1, ?3) AND t < ?1 + ?2 AND w_pct IS NOT NULL AND w_reset IS NOT NULL)`
    ).bind(wk, WEEK, t));
  }
  ls.push(limitLogYenile(env, wk));
  await env.DB.batch(ls);
}
function csvKacis(deger) {
  const s = String(deger == null ? "" : deger);
  return /[",\r\n]/.test(s) ? `"${s.replace(/"/g, '""')}"` : s;
}

// Ofis yöneticilerinin yapabileceği yönetim işlemleri (geri kalan her şey süper yönetici ister)
async function ofisIzni(req, path, erisim) {
  if (path === "/api/yonetim/ayarlar" && req.method === "GET") return erisim.yon.size > 0;
  if (req.method !== "POST" || !erisim.yon.size) return false;
  let b;
  try { b = await req.clone().json(); } catch { return false; }
  if (!b || typeof b !== "object") return false;
  if (path === "/api/yonetim/ayarlar") {
    const anahtarlar = Object.keys(b);
    if (anahtarlar.length !== 1 || anahtarlar[0] !== "saglayiciAyar" || !b.saglayiciAyar || typeof b.saglayiciAyar !== "object") return false;
    const sgller = Object.keys(b.saglayiciAyar);
    return sgller.length > 0 && sgller.every((s) => SAGLAYICILAR.includes(s) && erisim.yon.has(s));
  }
  if (path === "/api/yonetim/claude-yuzde" || path === "/api/yonetim/fable-yuzde" || path === "/api/yonetim/limit-taban") return erisim.yon.has("claude");
  if (path === "/api/yonetim/saglayici-yuzde" || path === "/api/yonetim/hesap-kilit-ayarla" || path === "/api/yonetim/hesap-kilit-sifirla") {
    return erisim.yon.has(String(b.saglayici || ""));
  }
  return false;
}
function ayarKapsamaGore(ayar, erisim) {
  const { github_surum_kontrol: _gk, ...temiz } = ayar || {};
  if (erisim.admin) return temiz;
  return { ...temiz, saglayiciAyar: Object.fromEntries(SAGLAYICILAR.filter((s) => erisim.yon.has(s)).map((s) => [s, (temiz.saglayiciAyar || {})[s] || {}])) };
}

async function handleSistemGuncelleme(env, url, erisim) {
  if (!erisim.admin) return json({ hata: "Bu işlem yalnızca tam yetkili sistem yöneticisi içindir." }, 403);
  const now = Math.floor(Date.now() / 1000);
  const mevcutSurum = Math.max(MUHUR_ILK_SURUM, sonSurumOf());
  const hamRepo = String(env.GITHUB_REPO || "doganau/token-panosu").trim();
  const repo = /^[A-Za-z0-9_.-]+\/[A-Za-z0-9_.-]+$/.test(hamRepo) ? hamRepo : "doganau/token-panosu";
  const varsayilanUrl = `https://github.com/${repo}/releases`;
  const zorla = url.searchParams.get("zorla") === "1";

  const ayar = await ayarYukle(env);
  const onbellek = ayar.github_surum_kontrol;
  if (!zorla && onbellek && Number.isFinite(onbellek.t) && now - onbellek.t < 6 * 3600) {
    const ss = Number.isFinite(onbellek.son_surum) ? onbellek.son_surum : mevcutSurum;
    const iu = typeof onbellek.indir_url === "string" && onbellek.indir_url.startsWith("https://") ? onbellek.indir_url : varsayilanUrl;
    return json({
      mevcut_surum: mevcutSurum,
      son_surum: ss,
      guncel: ss <= mevcutSurum,
      indir_url: iu,
      kontrol_edildi: onbellek.t,
    });
  }

  let sonSurum = mevcutSurum;
  let indirUrl = varsayilanUrl;
  try {
    const yanit = await fetch(`https://api.github.com/repos/${repo}/releases/latest`, {
      headers: { "user-agent": "token-panosu", accept: "application/vnd.github+json" },
      signal: AbortSignal.timeout(5000),
    });
    if (yanit.ok) {
      const veri = await yanit.json();
      const m = String((veri && (veri.tag_name || veri.name)) || "").match(/v?(\d+)/i);
      if (m) sonSurum = Number(m[1]);
      if (veri && typeof veri.html_url === "string" && veri.html_url.startsWith("https://")) {
        indirUrl = veri.html_url;
      }
    } else {
      const hamGo = await fetch(`https://raw.githubusercontent.com/${repo}/main/go/main.go`, {
        headers: { "user-agent": "token-panosu" },
        signal: AbortSignal.timeout(4000),
      });
      if (hamGo.ok) {
        const metin = await hamGo.text();
        const mg = metin.match(/^\s*Surum\s*=\s*(\d+)/m);
        if (mg && Number(mg[1]) > sonSurum) {
          sonSurum = Number(mg[1]);
          indirUrl = `https://github.com/${repo}`;
        }
      }
    }
  } catch {
    // Çevrimdışı veya henüz GitHub sürümü yoksa sessizce güncel kabul et (fail-open)
  }

  try {
    const mevcutAyar = await ayarYukle(env, true);
    const yeniAyar = ayarTemizle({ github_surum_kontrol: { t: now, son_surum: sonSurum, indir_url: indirUrl } }, mevcutAyar);
    await env.DB.prepare("INSERT INTO sistem_ayar (id, veri, guncellendi) VALUES (1, ?, ?) ON CONFLICT(id) DO UPDATE SET veri = excluded.veri, guncellendi = excluded.guncellendi")
      .bind(JSON.stringify(yeniAyar), now).run();
    ayarBellek = yeniAyar;
    ayarYuklendiAt = now;
  } catch {}

  return json({
    mevcut_surum: mevcutSurum,
    son_surum: sonSurum,
    guncel: sonSurum <= mevcutSurum,
    indir_url: indirUrl,
    kontrol_edildi: now,
  });
}

async function handleAdmin(req, env, path, url, erisim) {
  const now = Math.floor(Date.now() / 1000);
  const admin = erisim.admin;
  if (!admin && !(await ofisIzni(req, path, erisim))) {
    return json({ hata: "Bu işlem için yetkin yok. Süper yöneticiden ya da bu ofisin yöneticisinden iste." }, 403);
  }
  const [sistemAyar, otoMap] = await Promise.all([ayarYukle(env), otoSifirlamaGetir(env, now)]);
  const haftaSifirlamaKapsamli = async (ayarNesnesi) => {
    const [tazeOto, devRows] = await Promise.all([
      otoSifirlamaGetir(env, now, true),
      env.DB.prepare("SELECT id, label FROM devices").all(),
    ]);
    const adMap = new Map((devRows.results || []).map((d) => [d.id, d.label]));
    const tum = haftaSifirlamaHaritasi(ayarNesnesi, tazeOto, now, adMap);
    return admin ? tum : Object.fromEntries(SAGLAYICILAR.filter((s) => erisim.yon.has(s)).map((s) => [s, tum[s]]));
  };
  if (path === "/api/yonetim/ayarlar" && req.method === "GET") {
    const tazeAyar = await ayarYukle(env, true);
    return json({
      ayar: ayarKapsamaGore(tazeAyar, erisim),
      varsayilan: VARSAYILAN_AYAR,
      haftaSifirlama: await haftaSifirlamaKapsamli(tazeAyar),
      admin,
      now,
    });
  }
  if (admin) {
    const k = await kullaniciYonetimi(req, env, path, url, erisim);
    if (k) return k;
  }
  if (path === "/api/yonetim/ayarlar" && req.method === "POST") {
    const b = await req.json();
    if (b && b.varsayilanaDon === true) {
      if (!admin) return json({ hata: "Bu işlem yalnızca tam yetkili sistem yöneticisi içindir." }, 403);
      const korunanMuhur = { muhurZorunlu: !!sistemAyar.muhurZorunlu, zorunluMinSurum: sistemAyar.zorunluMinSurum || 0 };
      const yeniVarsayilan = await ayarKaydet(env, { ...VARSAYILAN_AYAR, ...korunanMuhur });
      const hs = await haftaSifirlamaKapsamli(yeniVarsayilan);
      const guncelOto = otoSifirlamaBellek.map || otoMap;
      await env.DB.batch(SAGLAYICILAR.map((s) => {
        const wk = weekStartSaglayici(now, yeniVarsayilan, guncelOto, s);
        return s === "claude" ? limitLogYenile(env, wk) : saglayiciLimitLogYenile(env, s, wk);
      }));
      return json({ tamam: true, ayar: yeniVarsayilan, haftaSifirlama: hs, now });
    }
    if (b && typeof b === "object" && !admin) {
      delete b.muhurZorunlu;
      delete b.zorunluMinSurum;
    }
    const yeni = await ayarKaydet(env, b);
    const hs = await haftaSifirlamaKapsamli(yeni);
    const guncelOto = otoSifirlamaBellek.map || otoMap;
    await env.DB.batch(SAGLAYICILAR.map((s) => {
      const wk = weekStartSaglayici(now, yeni, guncelOto, s);
      return s === "claude" ? limitLogYenile(env, wk) : saglayiciLimitLogYenile(env, s, wk);
    }));
    return json({ tamam: true, ayar: ayarKapsamaGore(yeni, erisim), haftaSifirlama: hs, now });
  }
  if (path === "/api/yonetim/kod" && req.method === "POST") {
    const b = await req.json();
    const l = String(b.label || "").trim().slice(0, 40);
    if (!l) return json({ hata: "Bilgisayara bir ad ver." }, 400);
    const uye = String(b.uye_id || "");
    if (uye && !(await env.DB.prepare("SELECT 1 FROM uyeler WHERE id = ?").bind(uye).first())) return json({ hata: "Üye bulunamadı." }, 400);
    let paket;
    if (b.izinli_saglayici !== undefined) {
      paket = paketNormalize(b.izinli_saglayici);
      if (!paket) return json({ hata: "En az bir veri sağlayıcısı seçin." }, 400);
    } else {
      paket = uye ? await varsayilanUyePaketi(env, uye, []) : "cgm";
    }
    const alphabet = "ABCDEFGHJKMNPQRSTUVWXYZ23456789";
    const s = [...crypto.getRandomValues(new Uint8Array(10))].map((x) => alphabet[x % alphabet.length]).join("");
    const code = `${s.slice(0, 5)}-${s.slice(5)}`;
    await env.DB.prepare("DELETE FROM pair_codes WHERE expires_at < ?").bind(now).run();
    await env.DB.prepare("INSERT INTO pair_codes (code, label, expires_at, uye_id, izinli_saglayici, used_at) VALUES (?,?,?,?,?,0)").bind(code, l, now + 900, uye, paket).run();
    return json({ code, label: l, expires_at: now + 900, izinli_saglayici: paket, paket });
  }
  if (path === "/api/yonetim/fiyatlar" && req.method === "GET") {
    await fiyatYukle(env);
    const [r, ilkSaatler] = await Promise.all([
      env.DB.prepare("SELECT guncellendi, denendi, kaynak, hata FROM fiyat_listesi WHERE id = 1").first(),
      ilkSaatleriGetir(env, now),
    ]);
    const ozelHarita = sistemAyar.ozelFiyatlar || {};
    const gorulenSet = new Set();
    for (const s of ilkSaatler) {
      const t = tabanAd(String(s.model || "")).toLowerCase();
      if (t) gorulenSet.add(t);
    }
    const kullanilan = [...gorulenSet].sort().map((m) => {
      const es = eslesenModelBul(FIYAT, m);
      const k = kayitBul(FIYAT, m);
      return {
        model: m,
        saglayici: saglayiciOf(m),
        eslesen: es.anahtar,
        b: fiyatBul(FIYAT, m),
        u: k && k.u ? k.u : null,
        ozel: Object.hasOwn(ozelHarita, m) || Object.hasOwn(ozelHarita, es.anahtar),
      };
    });
    return json({ ...(r || {}), liste: FIYAT, taban: FIYAT_TABAN, ozel: ozelHarita, kullanilan });
  }
  if (path === "/api/yonetim/fiyatlar/yenile" && req.method === "POST") return json(await fiyatYenile(env));
  // Elle Fable sıfırlaması sonrası: bu t'den önceki okumalar etkin değerde yok sayılır (0 = kaldır)
  if (path === "/api/yonetim/limit-taban" && req.method === "POST") {
    const b = await req.json();
    if (!Number.isSafeInteger(b.t) || b.t < 0 || b.t > now + 300) return json({ hata: "t geçersiz." }, 400);
    const wk = weekStartSaglayici(Number.isSafeInteger(b.week) ? b.week : now, sistemAyar, otoMap, "claude");
    await limitTabanYaz(env, wk, b.t);
    asimOnbellek = null;
    return json({ tamam: true, week: wk, taban_t: b.t });
  }
  if (path === "/api/yonetim/cihazlar" && req.method === "GET") {
    const { results } = await env.DB.prepare("SELECT id, label, created_at, last_seen, client_ver, os, uye_id, izinli_saglayici, calisan_paket FROM devices ORDER BY created_at").all();
    return json((results || []).map((d) => ({
      ...d,
      izinli_saglayici: cihazEtkinPaket(d.izinli_saglayici),
      calisan_paket: d.calisan_paket || "",
    })));
  }
  if (path === "/api/yonetim/cihaz-izin" && req.method === "POST") {
    const b = await req.json();
    return cihazIzinUygula(env, b.device_id || b.id, b.izinli_saglayici);
  }
  if (path === "/api/yonetim/cihaz-adlandir" && req.method === "POST") {
    const { id, label } = await req.json();
    const l = String(label || "").trim().slice(0, 40);
    if (!l) return json({ hata: "Ad boş olamaz." }, 400);
    const r = await env.DB.prepare("UPDATE devices SET label = ? WHERE id = ?").bind(l, String(id)).run();
    asimOnbellek = null;
    return r.meta.changes ? json({ tamam: true }) : json({ hata: "bulunamadı" }, 404);
  }
  if (path === "/api/yonetim/uye" && req.method === "POST") {
    const ad = String((await req.json()).ad || "").trim().slice(0, 40);
    if (!ad) return json({ hata: "Üyeye bir ad ver." }, 400);
    if (await env.DB.prepare("SELECT 1 FROM uyeler WHERE ad = ?").bind(ad).first()) return json({ hata: "Bu adla bir üye zaten var." }, 409);
    const id = toHex(crypto.getRandomValues(new Uint8Array(8)));
    await env.DB.prepare("INSERT INTO uyeler (id, ad, created_at) VALUES (?,?,?)").bind(id, ad, now).run();
    asimOnbellek = null;
    return json({ id, ad });
  }
  if (path === "/api/yonetim/uye-adlandir" && req.method === "POST") {
    const b = await req.json();
    const ad = String(b.ad || "").trim().slice(0, 40);
    if (!ad) return json({ hata: "Ad boş olamaz." }, 400);
    if (await env.DB.prepare("SELECT 1 FROM uyeler WHERE ad = ? AND id != ?").bind(ad, String(b.id)).first()) return json({ hata: "Bu adla bir üye zaten var." }, 409);
    const r = await env.DB.prepare("UPDATE uyeler SET ad = ? WHERE id = ?").bind(ad, String(b.id)).run();
    asimOnbellek = null;
    return r.meta.changes ? json({ tamam: true }) : json({ hata: "bulunamadı" }, 404);
  }
  if (path === "/api/yonetim/uye-sil" && req.method === "POST") {
    const id = String((await req.json()).id || "");
    if (await env.DB.prepare("SELECT 1 FROM devices WHERE uye_id = ?").bind(id).first()) return json({ hata: "Önce bu üyenin bilgisayarlarını başka üyeye taşı." }, 409);
    await env.DB.prepare("DELETE FROM uyeler WHERE id = ?").bind(id).run();
    asimOnbellek = null;
    return json({ tamam: true });
  }
  if (path === "/api/yonetim/cihaz-uye" && req.method === "POST") {
    const b = await req.json();
    const uye = String(b.uye_id || "");
    if (uye && !(await env.DB.prepare("SELECT 1 FROM uyeler WHERE id = ?").bind(uye).first())) return json({ hata: "Üye bulunamadı." }, 400);
    const r = await env.DB.prepare("UPDATE devices SET uye_id = ? WHERE id = ?").bind(uye, String(b.id)).run();
    asimOnbellek = null;
    return r.meta.changes ? json({ tamam: true }) : json({ hata: "bulunamadı" }, 404);
  }
  // Claude haftalık ve/veya 5 saatlik limit yüzdesi elle düzeltilebilir veya sıfırlanabilir.
  if (path === "/api/yonetim/claude-yuzde" && req.method === "POST") {
    const b = await req.json();
    const wk = weekStartSaglayici(now, sistemAyar, otoMap, "claude");
    if (b.pct === null && (b.h_pct === null || b.h_pct === undefined)) {
      const stmts = [
        env.DB.prepare("DELETE FROM limit_gecmis WHERE device_id = '' AND t >= ? AND (w_pct IS NOT NULL OR h_pct IS NOT NULL)").bind(wk),
        env.DB.prepare("DELETE FROM limit_log WHERE week = ?").bind(wk),
      ];
      if (b.tabanSifirla !== false) {
        stmts.push(env.DB.prepare("INSERT INTO hafta_ayar (week, fable_ayri, limit_taban_t) VALUES (?,0,0) ON CONFLICT(week) DO UPDATE SET limit_taban_t = 0").bind(wk));
      }
      stmts.push(limitLogYenile(env, wk));
      const sonCihaz = await env.DB.prepare("SELECT device_id, t, ham FROM limit_gecmis WHERE device_id != '' AND device_id != 'fable' AND w_pct IS NOT NULL ORDER BY t DESC LIMIT 1").first();
      if (sonCihaz && sonCihaz.ham) {
        stmts.push(env.DB.prepare("INSERT INTO limit_okuma (id, okundu_at, device_id, ham) VALUES (1,?,?,?) ON CONFLICT(id) DO UPDATE SET okundu_at = excluded.okundu_at, device_id = excluded.device_id, ham = excluded.ham")
          .bind(sonCihaz.t, sonCihaz.device_id, sonCihaz.ham));
      } else {
        stmts.push(env.DB.prepare("DELETE FROM limit_okuma WHERE id = 1 AND device_id = ''"));
      }
      await env.DB.batch(stmts);
      asimOnbellek = null;
      return json({ tamam: true });
    }
    const wVar = b.pct !== undefined && b.pct !== null;
    const hVar = b.h_pct !== undefined && b.h_pct !== null;
    if (!wVar && !hVar) return json({ hata: "En az bir yüzde değeri girin." }, 400);
    if (wVar && (typeof b.pct !== "number" || !Number.isFinite(b.pct) || b.pct < 0 || b.pct > 100)) {
      return json({ hata: "Haftalık yüzde 0 ile 100 arasında olmalı." }, 400);
    }
    if (hVar && (typeof b.h_pct !== "number" || !Number.isFinite(b.h_pct) || b.h_pct < 0 || b.h_pct > 100)) {
      return json({ hata: "5 saatlik yüzde 0 ile 100 arasında olmalı." }, 400);
    }
    const pct = wVar ? Math.round(b.pct * 10) / 10 : null;
    const hPct = hVar ? Math.round(b.h_pct * 10) / 10 : null;
    const [sonOtoW, sonOtoH] = await Promise.all([
      env.DB.prepare("SELECT MAX(w_reset) AS wr FROM limit_gecmis WHERE t >= ? AND t < ? AND device_id != '' AND device_id != 'fable' AND w_reset IS NOT NULL").bind(wk, wk + WEEK).first(),
      env.DB.prepare("SELECT MAX(h_reset) AS hr FROM limit_gecmis WHERE t >= ? AND h_reset > ?").bind(now - 6 * 3600, now).first(),
    ]);
    const wr = wVar ? (sonOtoW && Number.isFinite(sonOtoW.wr) ? sonOtoW.wr : wk + WEEK) : null;
    const hSaat = typeof b.h_saat === "number" && Number.isFinite(b.h_saat) && b.h_saat >= 0.1 && b.h_saat <= 5 ? b.h_saat : null;
    const hr = hVar ? (hSaat ? Math.round(now + hSaat * 3600) : (sonOtoH && Number.isFinite(sonOtoH.hr) ? sonOtoH.hr : now + 5 * 3600)) : null;
    const hamObj = { manual: 1 };
    if (wVar) { hamObj["seven_day.used_percentage"] = pct; hamObj["seven_day.resets_at"] = wr; }
    if (hVar) { hamObj["five_hour.used_percentage"] = hPct; hamObj["five_hour.resets_at"] = hr; }
    const stmts = [
      env.DB.prepare("DELETE FROM limit_gecmis WHERE device_id = '' AND t >= ? AND (w_pct IS NOT NULL OR h_pct IS NOT NULL)").bind(wk),
      env.DB.prepare("INSERT OR REPLACE INTO limit_gecmis (device_id, t, w_pct, w_reset, h_pct, h_reset, ham) VALUES ('', ?, ?, ?, ?, ?, ?)")
        .bind(now, pct, wr, hPct, hr, JSON.stringify(hamObj)),
      env.DB.prepare("INSERT INTO limit_okuma (id, okundu_at, device_id, ham) VALUES (1,?,'',?) ON CONFLICT(id) DO UPDATE SET okundu_at = excluded.okundu_at, device_id = excluded.device_id, ham = excluded.ham")
        .bind(now, JSON.stringify(hamObj)),
    ];
    if (wVar) {
      if (b.tabanKorun !== true) {
        stmts.push(env.DB.prepare("INSERT INTO hafta_ayar (week, fable_ayri, limit_taban_t) VALUES (?,0,?) ON CONFLICT(week) DO UPDATE SET limit_taban_t = excluded.limit_taban_t").bind(wk, now));
      }
      stmts.push(
        env.DB.prepare("INSERT INTO limit_log (week, pct, set_at) VALUES (?,?,?) ON CONFLICT(week) DO UPDATE SET pct = excluded.pct, set_at = excluded.set_at").bind(wk, pct, now)
      );
    }
    await env.DB.batch(stmts);
    asimOnbellek = null;
    return json({ tamam: true });
  }
  // Fable haftalık yüzdesi elle girilir (Claude'un üst menüsünde görünen değer). null: bu haftanın elle girişini sil.
  if (path === "/api/yonetim/fable-yuzde" && req.method === "POST") {
    const b = await req.json();
    const wk = weekStartSaglayici(now, sistemAyar, otoMap, "claude");
    if (b.pct === null) {
      await env.DB.prepare("DELETE FROM limit_gecmis WHERE device_id IN ('', 'fable') AND t >= ? AND w_pct IS NULL AND ham LIKE '%fable%'").bind(wk).run();
    } else {
      if (typeof b.pct !== "number" || !Number.isFinite(b.pct) || b.pct < 0 || b.pct > 100) return json({ hata: "Yüzde 0 ile 100 arasında olmalı." }, 400);
      const pct = Math.round(b.pct * 10) / 10;
      await env.DB.prepare("INSERT OR REPLACE INTO limit_gecmis (device_id, t, w_pct, w_reset, h_pct, h_reset, ham) VALUES ('fable', ?, NULL, NULL, NULL, NULL, ?)")
        .bind(now, JSON.stringify({ "model_fable.used_percentage": pct })).run();
    }
    asimOnbellek = null;
    return json({ tamam: true });
  }
  // ChatGPT / Gemini haftalık limit yüzdesi elle girilebilir veya sıfırlanabilir.
  if (path === "/api/yonetim/saglayici-yuzde" && req.method === "POST") {
    const b = await req.json();
    const sgl = String(b.saglayici || "");
    if (sgl !== "chatgpt" && sgl !== "gemini") return json({ hata: "Geçersiz sağlayıcı." }, 400);
    const wk = weekStartSaglayici(now, sistemAyar, otoMap, sgl);
    if (b.pct === null) {
      await env.DB.batch([
        env.DB.prepare("DELETE FROM saglayici_limit_gecmis WHERE saglayici = ? AND device_id = '' AND t >= ?").bind(sgl, wk),
        env.DB.prepare("DELETE FROM saglayici_limit_log WHERE saglayici = ? AND week = ?").bind(sgl, wk),
        saglayiciLimitLogYenile(env, sgl, wk),
      ]);
      return json({ tamam: true });
    }
    if (typeof b.pct !== "number" || !Number.isFinite(b.pct) || b.pct < 0 || b.pct > 100) {
      return json({ hata: "Yüzde 0 ile 100 arasında olmalı." }, 400);
    }
    const pct = Math.round(b.pct * 10) / 10;
    const sonOto = await env.DB.prepare("SELECT MAX(w_reset) AS wr FROM saglayici_limit_gecmis WHERE saglayici = ? AND t >= ? AND t < ? AND device_id != '' AND w_reset IS NOT NULL")
      .bind(sgl, wk, wk + WEEK).first();
    const wr = sonOto && Number.isFinite(sonOto.wr) ? sonOto.wr : wk + WEEK;
    const stmts = [
      env.DB.prepare("DELETE FROM saglayici_limit_gecmis WHERE saglayici = ? AND device_id = '' AND t >= ?").bind(sgl, wk),
    ];
    if (b.eskiSil === true) {
      stmts.push(env.DB.prepare("DELETE FROM saglayici_limit_gecmis WHERE saglayici = ? AND t >= ? AND w_pct > ?").bind(sgl, wk, pct));
    }
    stmts.push(
      env.DB.prepare("INSERT OR REPLACE INTO saglayici_limit_gecmis (saglayici, device_id, t, w_pct, w_reset, h_pct, h_reset, ham) VALUES (?, '', ?, ?, ?, NULL, NULL, ?)")
        .bind(sgl, now, pct, wr, JSON.stringify({ "seven_day.used_percentage": pct, "seven_day.resets_at": wr, manual: 1 })),
      env.DB.prepare("INSERT INTO saglayici_limit_log (saglayici, week, pct, set_at) VALUES (?,?,?,?) ON CONFLICT(saglayici, week) DO UPDATE SET pct = excluded.pct, set_at = excluded.set_at")
        .bind(sgl, wk, pct, now)
    );
    await env.DB.batch(stmts);
    return json({ tamam: true });
  }
  // Ortak hesap kilidini sıfırla: bir sonraki gönderimde gelen ilk geçerli hesap yeni ortak hesap olur.
  if (path === "/api/yonetim/hesap-kilit-sifirla" && req.method === "POST") {
    const b = await req.json();
    const sgl = String(b.saglayici || "");
    if (!SAGLAYICILAR.includes(sgl)) return json({ hata: "Geçersiz sağlayıcı." }, 400);
    const { results: devs } = await env.DB.prepare("SELECT id, srv_flags FROM devices WHERE srv_flags LIKE ?")
      .bind(`%farkli_hesap_${sgl}%`).all();
    const stmts = [env.DB.prepare("DELETE FROM hesap_kilit WHERE saglayici = ?").bind(sgl)];
    for (const dv of devs) {
      let sf = [];
      try { sf = JSON.parse(dv.srv_flags || "[]").filter((x) => x.k !== "farkli_hesap_" + sgl); } catch {}
      stmts.push(env.DB.prepare("UPDATE devices SET srv_flags = ? WHERE id = ?").bind(JSON.stringify(sf), dv.id));
    }
    await env.DB.batch(stmts);
    okumaOnbellekTemizle(false);
    return json({ tamam: true });
  }
  // Ortak hesap kilidini listeden seçilen veya elle girilen bir e-posta adresine (veya 16-hex özete) sabitle.
  if (path === "/api/yonetim/hesap-kilit-ayarla" && req.method === "POST") {
    const b = await req.json();
    const sgl = String(b.saglayici || "");
    if (!SAGLAYICILAR.includes(sgl)) return json({ hata: "Geçersiz sağlayıcı." }, 400);
    let eposta = epostaTemizle(b.eposta);
    const ozetHam = String(b.ozet || "").trim().toLowerCase().replace(/^#/, "");
    let ozet = SID_RE.test(ozetHam) ? ozetHam : "";
    if (!eposta && epostaTemizle(b.ozet)) eposta = epostaTemizle(b.ozet);
    if (!eposta && !ozet) return json({ hata: "Geçerli bir e-posta adresi seçin veya yazın." }, 400);

    const { results: devs } = await env.DB.prepare("SELECT id, label, hesaplar, hesap_eposta, srv_flags FROM devices").all();
    let cihazId = String(b.cihaz_id || "");
    let bulunanEtiket = "";
    for (const dv of devs) {
      let hObj = {}, epObj = {};
      try { hObj = JSON.parse(dv.hesaplar || "{}") || {}; } catch {}
      try { epObj = JSON.parse(dv.hesap_eposta || "{}") || {}; } catch {}
      const dOz = typeof hObj[sgl] === "string" && SID_RE.test(hObj[sgl]) ? hObj[sgl] : "";
      const dEp = epostaTemizle(epObj[sgl]);
      if (eposta && dEp === eposta) {
        if (!ozet && dOz) ozet = dOz;
        if (!cihazId) cihazId = dv.id;
        if (!bulunanEtiket) bulunanEtiket = dv.label || "";
      } else if (!eposta && ozet && dOz === ozet) {
        if (dEp) eposta = dEp;
        if (!cihazId) cihazId = dv.id;
        if (!bulunanEtiket) bulunanEtiket = dv.label || "";
      }
    }
    const etiket = temizMetin(b.etiket, 40, bulunanEtiket || "Elle tanımlandı");
    const ad = SAGLAYICI_AD[sgl] || sgl;
    const ortakAd = eposta || ("#" + ozet.slice(0, 8));
    const kilitAktif = !(sistemAyar.hesapKilitAktif && sistemAyar.hesapKilitAktif[sgl] === false);

    const stmts = [
      env.DB.prepare("INSERT INTO hesap_kilit (saglayici, hesap_ozet, eposta, etiket, kilitlendi_at, cihaz_id) VALUES (?,?,?,?,?,?) ON CONFLICT(saglayici) DO UPDATE SET hesap_ozet = excluded.hesap_ozet, eposta = excluded.eposta, etiket = excluded.etiket, kilitlendi_at = excluded.kilitlendi_at, cihaz_id = excluded.cihaz_id")
        .bind(sgl, ozet, eposta, etiket, now, cihazId),
    ];
    for (const dv of devs) {
      let hObj = {}, epObj = {}, sf = [];
      try { hObj = JSON.parse(dv.hesaplar || "{}") || {}; } catch {}
      try { epObj = JSON.parse(dv.hesap_eposta || "{}") || {}; } catch {}
      try { sf = JSON.parse(dv.srv_flags || "[]") || []; } catch {}
      const dOz = typeof hObj[sgl] === "string" && SID_RE.test(hObj[sgl]) ? hObj[sgl] : "";
      const dEp = epostaTemizle(epObj[sgl]);
      const yeniSf = sf.filter((x) => x.k !== "farkli_hesap_" + sgl);
      if (kilitAktif && (dEp || dOz)) {
        let uyumlu = true;
        if (eposta && dEp) uyumlu = dEp === eposta;
        else if (ozet && dOz) uyumlu = dOz === ozet;
        if (!uyumlu) {
          const gelenAd = dEp || ("#" + dOz.slice(0, 8));
          yeniSf.push({
            k: "farkli_hesap_" + sgl,
            s: "b",
            t: `Ayrı ${ad} hesabı (${gelenAd}): ortak hesap (${ortakAd}) dışında olduğu için ortak kotaya dahil edilmedi`,
          });
        }
      }
      if (JSON.stringify(yeniSf) !== JSON.stringify(sf)) {
        stmts.push(env.DB.prepare("UPDATE devices SET srv_flags = ? WHERE id = ?").bind(JSON.stringify(yeniSf), dv.id));
      }
    }
    await env.DB.batch(stmts);
    okumaOnbellekTemizle(false);
    return json({ tamam: true, eposta, ozet });
  }
  // Manuel kullanım ekleme / listeleme / silme (usage_h içinde sid = "manuel:...")
  if (path === "/api/yonetim/manuel-kullanim" && req.method === "GET") {
    await fiyatYukle(env);
    const { results } = await env.DB.prepare(
      `SELECT u.device_id, COALESCE(d.label, 'Silinmiş bilgisayar') AS cihaz_ad, u.hour, u.model, u.sid,
              u.input, u.output, u.cw5m, u.cw1h, u.cr, u.messages, u.updated_at
         FROM usage_h u LEFT JOIN devices d ON d.id = u.device_id
        WHERE u.sid LIKE 'manuel:%' ORDER BY u.hour DESC, u.updated_at DESC LIMIT 200`
    ).all();
    return json(results.map((r) => ({ ...r, usd: costOf(r), tok: r.input + r.output + r.cw5m + r.cw1h + r.cr })));
  }
  if (path === "/api/yonetim/manuel-kullanim" && req.method === "POST") {
    await fiyatYukle(env);
    const b = await req.json();
    const deviceId = String(b.device_id || "");
    const model = String(b.model || "").trim().toLowerCase();
    const hour = Number(b.hour);
    if (!/^[a-f0-9]{32}$/.test(deviceId)) return json({ hata: "Geçerli bir bilgisayar seçin." }, 400);
    if (!(await env.DB.prepare("SELECT 1 FROM devices WHERE id = ?").bind(deviceId).first())) return json({ hata: "Bilgisayar bulunamadı." }, 404);
    if (!MODEL_RE.test(model)) return json({ hata: "Model adı geçersiz (yalnızca küçük harf, rakam, ., -, _)." }, 400);
    if (!Number.isSafeInteger(hour) || hour % 3600 !== 0 || hour < now - 366 * 86400 || hour > now + 7 * 86400) {
      return json({ hata: "Tarih/saat geçersiz." }, 400);
    }
    let inp = 0, out = 0, cw5 = 0, cw1 = 0, cr = 0, msg = 1;
    if (b.mod === "token") {
      inp = Number(b.input || 0); out = Number(b.output || 0);
      cw5 = Number(b.cw5m || 0); cw1 = Number(b.cw1h || 0); cr = Number(b.cr || 0);
      msg = Number(b.messages == null ? 1 : b.messages);
      if (![inp, out, cw5, cw1, cr, msg].every(isCount) || inp + out + cw5 + cw1 + cr <= 0) {
        return json({ hata: "En az bir token alanı 0'dan büyük tam sayı olmalı." }, 400);
      }
    } else {
      const usd = Number(b.usd);
      if (!Number.isFinite(usd) || usd <= 0 || usd > 100000) return json({ hata: "USD tutarı 0 ile 100.000 arasında olmalı." }, 400);
      const [fIn, fOut] = fiyatBul(FIYAT, model);
      if (fOut > 0) {
        out = Math.max(1, Math.round((usd * 1e6) / fOut));
      } else if (fIn > 0) {
        inp = Math.max(1, Math.round((usd * 1e6) / fIn));
      } else {
        return json({ hata: "Bu modelin birim fiyatı 0 olduğu için token kipiyle ekleyin veya önce özel model fiyatı tanımlayın." }, 400);
      }
    }
    const sid = "manuel:" + toHex(crypto.getRandomValues(new Uint8Array(5)));
    await env.DB.prepare(
      `INSERT INTO usage_h (device_id, hour, model, sid, input, output, cw5m, cw1h, cr, messages, updated_at)
       VALUES (?,?,?,?,?,?,?,?,?,?,?)`
    ).bind(deviceId, hour, model, sid, inp, out, cw5, cw1, cr, msg, now).run();
    asimOnbellek = null;
    return json({ tamam: true, sid, usd: costOf({ model, input: inp, output: out, cw5m: cw5, cw1h: cw1, cr }) });
  }
  if (path === "/api/yonetim/manuel-kullanim-sil" && req.method === "POST") {
    const b = await req.json();
    const sid = String(b.sid || "");
    if (!/^manuel:[a-f0-9]{10}$/.test(sid)) return json({ hata: "Yalnızca manuel kayıtlar silinebilir." }, 400);
    const r = await env.DB.prepare("DELETE FROM usage_h WHERE device_id = ? AND hour = ? AND model = ? AND sid = ?")
      .bind(String(b.device_id || ""), Number(b.hour || 0), String(b.model || ""), sid).run();
    asimOnbellek = null;
    return r.meta.changes ? json({ tamam: true }) : json({ hata: "Kayıt bulunamadı." }, 404);
  }
  // Veri dışa aktarma (gizli anahtar içermeden CSV ya da JSON)
  if (path === "/api/yonetim/disa-aktar" && req.method === "GET") {
    await fiyatYukle(env);
    const bicim = url.searchParams.get("bicim") === "csv" ? "csv" : "json";
    const kapsam = url.searchParams.get("kapsam") === "hafta" ? "hafta" : "tumu";
    const disSgl = saglayiciSec(url.searchParams.get("saglayici"));
    const baz = haftaSifirlamaBilgisi(sistemAyar, otoMap, disSgl, now).baz;
    const wk = weekStartFor(Number(url.searchParams.get("start")) || now, baz);
    const [devs, uyes, satirlar] = await Promise.all([
      env.DB.prepare("SELECT id, label, os, client_ver, uye_id, created_at, last_seen FROM devices ORDER BY created_at").all(),
      env.DB.prepare("SELECT id, ad FROM uyeler ORDER BY created_at").all(),
      kapsam === "hafta"
        ? env.DB.prepare(
            `SELECT device_id, hour, model, SUM(input) input, SUM(output) output, SUM(cw5m) cw5m, SUM(cw1h) cw1h, SUM(cr) cr, SUM(messages) messages
               FROM usage_v WHERE hour >= ? AND hour < ? GROUP BY device_id, hour, model ORDER BY hour DESC`
          ).bind(wk, wk + WEEK).all()
        : env.DB.prepare(
            `SELECT device_id, hour, model, SUM(input) input, SUM(output) output, SUM(cw5m) cw5m, SUM(cw1h) cw1h, SUM(cr) cr, SUM(messages) messages
               FROM usage_v GROUP BY device_id, hour, model ORDER BY hour DESC LIMIT 50000`
          ).all(),
    ]);
    const uyeAd = new Map(uyes.results.map((u) => [u.id, u.ad]));
    const devMap = new Map(devs.results.map((d) => [d.id, d]));
    const kayitlar = satirlar.results.map((r) => {
      const d = devMap.get(r.device_id);
      const temizModel = tabanAd(r.model);
      return {
        saat_utc: new Date(r.hour * 1000).toISOString(),
        hour: r.hour,
        uye: (d && uyeAd.get(d.uye_id)) || (d ? d.label : r.device_id),
        bilgisayar: d ? d.label : r.device_id,
        saglayici: saglayiciOf(r.model),
        model: temizModel,
        usd: Math.round(costOf(r) * 1e6) / 1e6,
        input: r.input,
        output: r.output,
        cw5m: r.cw5m,
        cw1h: r.cw1h,
        cr: r.cr,
        messages: r.messages,
      };
    });
    const damga = new Date(now * 1000).toISOString().slice(0, 10);
    if (bicim === "csv") {
      const baslik = ["saat_utc", "hour", "uye", "bilgisayar", "saglayici", "model", "usd", "input", "output", "cw5m", "cw1h", "cr", "messages"];
      const govde = "\uFEFF" + [baslik.join(","), ...kayitlar.map((k) => baslik.map((a) => csvKacis(k[a])).join(","))].join("\r\n");
      return new Response(govde, {
        headers: {
          "content-type": "text/csv; charset=utf-8",
          "content-disposition": `attachment; filename="token-panosu-${kapsam}-${damga}.csv"`,
          "cache-control": "no-store",
        },
      });
    }
    return new Response(JSON.stringify({ disa_aktarildi: new Date(now * 1000).toISOString(), kapsam, ayar: sistemAyar, uyeler: uyes.results, bilgisayarlar: devs.results, kayitlar }, null, 2), {
      headers: {
        "content-type": "application/json; charset=utf-8",
        "content-disposition": `attachment; filename="token-panosu-${kapsam}-${damga}.json"`,
        "cache-control": "no-store",
      },
    });
  }
  if (path === "/api/yonetim/cihaz-sil" && req.method === "POST") {
    const { id } = await req.json();
    await env.DB.batch([
      env.DB.prepare("DELETE FROM devices WHERE id = ?").bind(String(id)),
      env.DB.prepare("DELETE FROM usage_h WHERE device_id = ?").bind(String(id)),
      env.DB.prepare("DELETE FROM usage_5m WHERE device_id = ?").bind(String(id)),
      env.DB.prepare("DELETE FROM haric WHERE device_id = ?").bind(String(id)),
    ]);
    asimOnbellek = null;
    return json({ tamam: true });
  }
  if (path === "/api/yonetim/bildirimler" && req.method === "GET") {
    // ulasan: bildirimden önce eklenmiş ve onu almış (ya da sonrasını almış) bilgisayarlar; hedef: bildirim anında var olanlar
    const { results } = await env.DB.prepare(
      `SELECT b.id, b.baslik, b.metin, b.created_at, b.bitis,
        (SELECT COUNT(*) FROM devices d WHERE d.created_at < b.created_at AND d.bildirim_son >= b.id) AS ulasan,
        (SELECT COUNT(*) FROM devices d WHERE d.created_at < b.created_at) AS hedef
       FROM bildirimler b ORDER BY b.id DESC LIMIT 20`).all();
    const t = await env.DB.prepare("SELECT COUNT(*) AS n FROM devices").first();
    return json({ liste: results, toplam: t ? t.n : 0, now });
  }
  if (path === "/api/yonetim/bildirim" && req.method === "POST") {
    const b = await req.json();
    const temiz = (x) => String(x == null ? "" : x).replace(/[\u0000-\u001f\u007f-\u009f\u2028\u2029]/g, " ").replace(/ {2,}/g, " ").trim();
    const baslik = temiz(b.baslik), metin = temiz(b.metin);
    if (baslik.length < 1 || baslik.length > 60) return json({ hata: "Başlık 1 ile 60 karakter arasında olmalı." }, 400);
    if (metin.length < 1 || metin.length > 240) return json({ hata: "Metin 1 ile 240 karakter arasında olmalı." }, 400);
    const r = await env.DB.prepare("INSERT INTO bildirimler (baslik, metin, created_at, bitis) VALUES (?,?,?,?)").bind(baslik, metin, now, now + 7 * 86400).run();
    return json({ tamam: true, id: r.meta.last_row_id });
  }
  if (path === "/api/yonetim/bildirim-sil" && req.method === "POST") {
    const b = await req.json();
    if (!Number.isSafeInteger(b.id) || b.id < 1) return json({ hata: "geçersiz bildirim" }, 400);
    const r = await env.DB.prepare("DELETE FROM bildirimler WHERE id = ?").bind(b.id).run();
    return r.meta.changes ? json({ tamam: true }) : json({ hata: "bulunamadı" }, 404);
  }
  if (path === "/api/yonetim/haric" && req.method === "GET") {
    const { results } = await env.DB.prepare("SELECT sid, added_at, note FROM haric WHERE device_id = ? ORDER BY added_at")
      .bind(String(url.searchParams.get("id") || "")).all();
    return json(results);
  }
  if (path === "/api/yonetim/haric" && req.method === "POST") {
    const b = await req.json();
    const id = String(b.id || "");
    const oturum = String(b.oturum || "").trim().toLowerCase();
    if (!/^[a-f0-9]{32}$/.test(id)) return json({ hata: "geçersiz bilgisayar" }, 400);
    if (!UUID_RE.test(oturum)) return json({ hata: "Oturum kimliği UUID biçiminde olmalı." }, 400);
    if (!(await env.DB.prepare("SELECT id FROM devices WHERE id = ?").bind(id).first())) return json({ hata: "bulunamadı" }, 404);
    const sid = await sidOf(oturum); // ham oturum kimliği saklanmaz ve kaydedilmez
    const not = String(b.not || "").trim().slice(0, 140);
    await env.DB.prepare(
      "INSERT INTO haric (device_id, sid, added_at, note) VALUES (?,?,?,?) ON CONFLICT(device_id, sid) DO UPDATE SET note = excluded.note"
    ).bind(id, sid, now, not).run();
    asimOnbellek = null;
    return json({ tamam: true, sid: sid.slice(0, 8) });
  }
  if (path === "/api/yonetim/haric-sil" && req.method === "POST") {
    const b = await req.json();
    const id = String(b.id || "");
    const sid = String(b.sid || "");
    if (!SID_RE.test(sid)) return json({ hata: "geçersiz oturum" }, 400);
    const r = await env.DB.prepare("DELETE FROM haric WHERE device_id = ? AND sid = ?").bind(id, sid).run();
    asimOnbellek = null;
    return r.meta.changes ? json({ tamam: true }) : json({ hata: "bulunamadı" }, 404);
  }
  return json({ hata: "bulunamadı" }, 404);
}

const SEC_HEADERS = {
  "content-security-policy":
    "default-src 'none'; script-src 'unsafe-inline'; style-src 'unsafe-inline' https://fonts.googleapis.com; font-src https://fonts.gstatic.com; connect-src 'self'; img-src 'self' data:; base-uri 'none'; form-action 'self'; frame-ancestors 'none'",
  "x-content-type-options": "nosniff",
  "referrer-policy": "no-referrer",
  "x-frame-options": "DENY",
};
const APP_ROUTES = /^\/(kurulum|giris|sifre|pano(\/.*)?)?$/;

export default {
  async fetch(req, env, ctx) {
    const url = new URL(req.url);
    const p = url.pathname;
    try {
      if ((req.method === "GET" || req.method === "HEAD") && APP_ROUTES.test(p)) {
        return new Response(req.method === "HEAD" ? null : APP, { headers: { "content-type": "text/html; charset=utf-8", "cache-control": "no-store", ...SEC_HEADERS } });
      }
      if (p === "/guncelle/manifest.json" && (req.method === "GET" || req.method === "HEAD")) {
        return new Response(req.method === "HEAD" ? null : JSON.stringify({ ikili: MANIFEST.ikili || null }), { headers: { "content-type": "application/json", "cache-control": "no-store" } });
      }
      if ((p === "/kur.sh" || p === "/kur.ps1") && (req.method === "GET" || req.method === "HEAD")) {
        const betik = (p === "/kur.sh" ? KUR_SH : KUR_PS1).replace(/^\uFEFF/, "").replaceAll(VARSAYILAN_ORIGIN, url.origin);
        return new Response(req.method === "HEAD" ? null : betik, {
          headers: { "content-type": "text/plain; charset=utf-8", "cache-control": "no-store", "x-content-type-options": "nosniff" },
        });
      }
      await semaHazirla(env);
      if (p === "/api/kurulum-paket" && req.method === "GET") return await handleKurulumPaket(req, env, url);
      if (p === "/api/gonder" && req.method === "POST") return await handleIngest(req, env, ctx);
      if (p === "/api/eslestir" && req.method === "POST") return await handlePair(req, env);
      if (p === "/api/giris" && req.method === "POST") {
        if (!sameOrigin(req, url)) return json({ hata: "yetkisiz" }, 403);
        return await handleLogin(req, env);
      }
      if (p === "/api/cikis" && req.method === "POST") return json({ tamam: true }, 200, { "set-cookie": cookie("", 0) });
      if (req.method === "POST" && !sameOrigin(req, url)) return json({ hata: "yetkisiz" }, 403);
      if (p === "/api/kurulum" && req.method === "POST") return await handleKurulum(req, env);
      if (p === "/api/token" && req.method === "GET") return await handleTokenBilgi(env, url);
      if (p === "/api/sifre-belirle" && req.method === "POST") return await handleSifreBelirle(req, env);

      const erisim = await erisimCoz(req, env);
      const authed = girisVar(erisim);
      if (p === "/api/oturum" && req.method === "GET") {
        const sa = await ayarYukle(env);
        const k = erisim.kullanici;
        return json({
          giris: authed, admin: erisim.admin, gor: [...erisim.gor], yon: [...erisim.yon],
          kullanici: k ? { id: k.id, kullanici_adi: k.kullanici_adi, ad: k.ad, rol: k.rol, uye_id: k.uye_id } : null,
          acil: authed && !k,
          kurulumGerekli: !(await env.DB.prepare("SELECT 1 FROM kullanicilar WHERE rol = 'super' AND durum != 'pasif' LIMIT 1").first()),
          panoBaslik: sa.panoBaslik, zamanDilimi: sa.zamanDilimi,
        });
      }

      // Buradan sonrası giriş ister
      if (!authed) return json({ hata: "giriş gerekli" }, 401);
      if (p === "/api/sistem-guncelleme" && req.method === "GET") return await handleSistemGuncelleme(env, url, erisim);
      if ((p === "/api/hafta" || p === "/api/limit-analiz") && req.method === "GET") {
        const sgl = saglayiciSec(url.searchParams.get("saglayici"));
        if (!yetkili(erisim, sgl)) return json({ hata: `${SAGLAYICI_AD[sgl]} ofisine erişimin yok. Süper yöneticiden bu ofise eklenmeyi iste.`, kilitli: sgl }, 403);
        return p === "/api/hafta" ? await handleWeek(env, url, ctx) : await handleLimitAnaliz(env, url, ctx);
      }
      if (p === "/api/hesap" || p.startsWith("/api/hesap/")) return await handleHesap(req, env, p, url, erisim);
      if (p.startsWith("/api/yonetim/")) {
        const res = await handleAdmin(req, env, p, url, erisim);
        if (req.method === "POST" && res.status < 400) okumaOnbellekTemizle(true);
        return res;
      }
      return json({ hata: "bulunamadı" }, 404);
    } catch (e) {
      console.error(p, e && e.stack ? e.stack : e);
      return json({ hata: "sunucu hatası" }, 500);
    }
  },
  // Günlük fiyat yenileme (wrangler.toml: [triggers] crons)
  async scheduled(event, env, ctx) {
    ctx.waitUntil(semaHazirla(env).then(() => fiyatYenile(env)));
  },
};
