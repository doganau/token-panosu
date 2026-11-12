import { test } from "node:test";
import assert from "node:assert/strict";
import fs from "node:fs";
import { registerHooks } from "node:module";
import { fileURLToPath } from "node:url";
import { DatabaseSync } from "node:sqlite";

// Cloudflare Wrangler metin/JSON varlık içe aktarmalarını Node test ortamında doğal olarak çöz:
registerHooks({
  load(url, context, nextLoad) {
    if (url.endsWith(".html") || url.endsWith(".sh") || url.endsWith(".ps1")) {
      const src = fs.readFileSync(fileURLToPath(url), "utf8");
      return {
        format: "module",
        shortCircuit: true,
        source: `export default ${JSON.stringify(src)};`,
      };
    }
    if (url.endsWith("/manifest.json")) {
      const src = fs.readFileSync(fileURLToPath(url), "utf8");
      return {
        format: "module",
        shortCircuit: true,
        source: `const m = ${src};\nexport default m;\n`,
      };
    }
    return nextLoad(url, context);
  },
});

const manifestMod = await import("../istemci/manifest.json");
const MANIFEST = manifestMod.default;
const workerMod = await import("./index.js");
const worker = workerMod.default;
const { paketNormalize, pakettenSaglayicilar, cihazEtkinPaket, hedeftenPaketCikar } = workerMod;

const enc = new TextEncoder();
const hexToBytes = (h) => {
  const b = new Uint8Array(h.length / 2);
  for (let i = 0; i < b.length; i++) b[i] = parseInt(h.slice(i * 2, i * 2 + 2), 16);
  return b;
};
const toHex = (buf) => [...new Uint8Array(buf)].map((x) => x.toString(16).padStart(2, "0")).join("");

async function sha256Hex(bytes) {
  return toHex(await crypto.subtle.digest("SHA-256", bytes));
}

async function hmacHex(keyBytes, message) {
  const k = await crypto.subtle.importKey("raw", keyBytes, { name: "HMAC", hash: "SHA-256" }, false, ["sign"]);
  return toHex(await crypto.subtle.sign("HMAC", k, enc.encode(message)));
}

function createMockD1() {
  const sqlite = new DatabaseSync(":memory:");
  class BoundStmt {
    constructor(sql, params = []) {
      this.sql = sql;
      this.params = params;
    }
    bind(...vals) {
      return new BoundStmt(this.sql, vals.map((v) => (v === undefined ? null : v)));
    }
    async first(col) {
      const stmt = sqlite.prepare(this.sql);
      const row = stmt.get(...this.params);
      if (!row) return null;
      const obj = { ...row };
      return col ? obj[col] : obj;
    }
    async all() {
      const stmt = sqlite.prepare(this.sql);
      const rows = stmt.all(...this.params).map((r) => ({ ...r }));
      return { success: true, results: rows, meta: {} };
    }
    async run() {
      const stmt = sqlite.prepare(this.sql);
      const info = stmt.run(...this.params);
      return {
        success: true,
        results: [],
        meta: { changes: Number(info.changes || 0), last_row_id: Number(info.lastInsertRowid || 0) },
      };
    }
  }
  return {
    sqlite,
    prepare(sql) {
      return new BoundStmt(sql);
    },
    async batch(stmts) {
      const out = [];
      sqlite.exec("BEGIN");
      try {
        for (const s of stmts) {
          const trim = s.sql.trim().toUpperCase();
          if (trim.startsWith("SELECT")) out.push(await s.all());
          else out.push(await s.run());
        }
        sqlite.exec("COMMIT");
      } catch (e) {
        sqlite.exec("ROLLBACK");
        throw e;
      }
      return out;
    },
  };
}

const TEST_ORIGIN = "https://pano.test";
const TEST_VIEW_KEY = "super-gizli-kurulum-anahtari-12345";
const TEST_ADMIN_KEY = "yonetici-gizli-anahtar-0123456789abcdef";
const TEST_MUHUR_ANAHTAR = "a1b2c3d4e5f60718293a4b5c6d7e8f90a1b2c3d4e5f60718293a4b5c6d7e8f90";

function yeniOrtam() {
  const DB = createMockD1();
  // Fiyat indirme zaman damgasını taze işaretle ki testler ağa çıkmasın
  DB.sqlite.exec("CREATE TABLE IF NOT EXISTS fiyat_meta (id INTEGER PRIMARY KEY, updated_at INTEGER NOT NULL)");
  DB.sqlite.prepare("INSERT INTO fiyat_meta (id, updated_at) VALUES (1, ?)").run(Math.floor(Date.now() / 1000));
  return {
    DB,
    VIEW_KEY: TEST_VIEW_KEY,
    ADMIN_KEY: TEST_ADMIN_KEY,
    MUHUR_ANAHTAR: TEST_MUHUR_ANAHTAR,
    GITHUB_REPO: "doganau/token-panosu",
  };
}

const bosCtx = { waitUntil() {} };

async function kurVeGirisYap(env) {
  const r1 = await worker.fetch(
    new Request(`${TEST_ORIGIN}/api/kurulum`, {
      method: "POST",
      headers: { "content-type": "application/json", origin: TEST_ORIGIN },
      body: JSON.stringify({
        anahtar: TEST_VIEW_KEY,
        kullanici_adi: "admin",
        ad: "Deniz Yönetici",
        sifre: "CokGucluSifre123!",
      }),
    }),
    env,
    bosCtx
  );
  assert.equal(r1.status, 200);
  const setCookie = r1.headers.get("set-cookie") || "";
  const cerez = setCookie.split(";")[0];
  assert.ok(cerez.startsWith("oturum="));
  return cerez;
}

async function gonderIstegiOlustur({ device, secret, ts, bodyObj, clientVer, target, exeSha, muhurAnahtar = TEST_MUHUR_ANAHTAR, sahteMuhur = "" }) {
  const bodyStr = JSON.stringify(bodyObj);
  const rawBytes = enc.encode(bodyStr);
  const rawSha = await sha256Hex(rawBytes);
  const sig = await hmacHex(hexToBytes(secret), `v1\n${device}\n${ts}\n${rawSha}`);
  const headers = {
    "content-type": "application/json",
    "x-device": device,
    "x-timestamp": String(ts),
    "x-signature": sig,
  };
  if (target && exeSha) {
    const hedefOs = target.split("-")[0];
    headers["x-target"] = target;
    headers["x-exe-hash"] = exeSha;
    headers["user-agent"] = `token-panosu/${clientVer} (${hedefOs})`;
    headers["accept-encoding"] = "gzip";
    if (sahteMuhur) {
      headers["x-seal"] = sahteMuhur;
    } else {
      const kHedefHex = await hmacHex(hexToBytes(muhurAnahtar), `tp-muhur-k1|${clientVer}|${target}`);
      const sealPayload = `muhur-v1\n${device}\n${ts}\n${clientVer}\n${target}\n${exeSha}\n${rawSha}\n${secret.slice(0, 16).toLowerCase()}`;
      headers["x-seal"] = await hmacHex(hexToBytes(kHedefHex), sealPayload);
    }
  }
  return new Request(`${TEST_ORIGIN}/api/gonder`, { method: "POST", headers, body: bodyStr });
}

test("kanonik sağlayıcı paket yardımcıları (paketNormalize, pakettenSaglayicilar, cihazEtkinPaket, hedeftenPaketCikar)", () => {
  assert.equal(paketNormalize("mc"), "cm");
  assert.equal(paketNormalize("claude,gemini"), "cm");
  assert.equal(paketNormalize(["gemini", "claude"]), "cm");
  assert.equal(paketNormalize(["chatgpt"]), "g");
  assert.equal(paketNormalize("cgm"), "cgm");
  assert.equal(paketNormalize(""), "");
  assert.equal(paketNormalize("bilinmeyen"), "");
  assert.equal(paketNormalize(["claude", "yanlis"]), "");

  assert.deepEqual(pakettenSaglayicilar("cm"), ["claude", "gemini"]);
  assert.deepEqual(pakettenSaglayicilar("g"), ["chatgpt"]);
  assert.deepEqual(pakettenSaglayicilar(""), ["claude", "chatgpt", "gemini"]);

  assert.equal(cihazEtkinPaket("mc"), "cm");
  assert.equal(cihazEtkinPaket(""), "cgm");

  assert.equal(hedeftenPaketCikar("darwin-arm64-cm"), "cm");
  assert.equal(hedeftenPaketCikar("windows-amd64-m"), "m");
  assert.equal(hedeftenPaketCikar("linux-amd64"), "cgm");
  assert.equal(hedeftenPaketCikar("darwin-arm64-xyz"), "");
});

test("kurulum-paket, eşleştirme (kurulum + onarım) ve cihaz izin güncellemesi akışı", async () => {
  const env = yeniOrtam();
  const cerez = await kurVeGirisYap(env);

  // 1) Yönetici sadece Claude + Gemini ("cm") izinli eşleşme kodu üretir
  const kodRes = await worker.fetch(
    new Request(`${TEST_ORIGIN}/api/yonetim/kod`, {
      method: "POST",
      headers: { "content-type": "application/json", origin: TEST_ORIGIN, cookie: cerez },
      body: JSON.stringify({ label: "Deniz MacBook", izinli_saglayici: ["claude", "gemini"] }),
    }),
    env,
    bosCtx
  );
  assert.equal(kodRes.status, 200);
  const kodJson = await kodRes.json();
  assert.equal(kodJson.izinli_saglayici, "cm");
  assert.match(kodJson.code, /^[A-Z0-9]{5}-[A-Z0-9]{5}$/);

  // 2) kur.sh / kur.ps1 tek komut kurulumda ilk bayttan önce paketi sorgular
  const kpRes = await worker.fetch(
    new Request(`${TEST_ORIGIN}/api/kurulum-paket?kod=${kodJson.code}`, {
      headers: { "cf-connecting-ip": "203.0.113.10" },
    }),
    env,
    bosCtx
  );
  assert.equal(kpRes.status, 200);
  assert.deepEqual(await kpRes.json(), { ok: true, paket: "cm" });

  // Geçersiz kod için ok:false, paket:"cgm"
  const kpYok = await worker.fetch(
    new Request(`${TEST_ORIGIN}/api/kurulum-paket?kod=AAAAA-BBBBB`, {
      headers: { "cf-connecting-ip": "203.0.113.10" },
    }),
    env,
    bosCtx
  );
  assert.deepEqual(await kpYok.json(), { ok: false, paket: "cgm" });

  // 3) Eşleştirme (/api/eslestir) cihazı "cm" izniyle kaydeder ve paket:"cm" döndürür
  const secret1 = "11".repeat(32);
  const esRes = await worker.fetch(
    new Request(`${TEST_ORIGIN}/api/eslestir`, {
      method: "POST",
      headers: { "content-type": "application/json" },
      body: JSON.stringify({ code: kodJson.code, secret: secret1 }),
    }),
    env,
    bosCtx
  );
  assert.equal(esRes.status, 200);
  const esJson = await esRes.json();
  assert.equal(esJson.paket, "cm");
  assert.equal(esJson.izinli_saglayici, "cm");

  // Kullanılmış kod için kurulum-paket artık ok:false dönmeli
  const kpKullanilmis = await worker.fetch(
    new Request(`${TEST_ORIGIN}/api/kurulum-paket?kod=${kodJson.code}`, {
      headers: { "cf-connecting-ip": "203.0.113.10" },
    }),
    env,
    bosCtx
  );
  assert.deepEqual(await kpKullanilmis.json(), { ok: false, paket: "cgm" });

  // 4) Onarım kipi (kod olmadan mevcut device_id + secret ile): mevcut "cm" iznini korur!
  const onarRes = await worker.fetch(
    new Request(`${TEST_ORIGIN}/api/eslestir`, {
      method: "POST",
      headers: { "content-type": "application/json" },
      body: JSON.stringify({ device_id: esJson.device_id, secret: secret1 }),
    }),
    env,
    bosCtx
  );
  assert.equal(onarRes.status, 200);
  const onarJson = await onarRes.json();
  assert.equal(onarJson.paket, "cm");

  // 5) Cihaz üzerinde ChatGPT'den kalma bayrak/hesap varken izin "m" (sadece Gemini) yapılınca temizlenir
  env.DB.sqlite
    .prepare("UPDATE devices SET srv_flags = ?, flags = ?, hesaplar = ?, hesap_eposta = ? WHERE id = ?")
    .run(
      JSON.stringify([
        { k: "farkli_hesap_claude", s: "w", at: 1000, t: "Ayrı Claude hesabı" },
        { k: "farkli_hesap_gemini", s: "w", at: 1000, t: "Ayrı Gemini hesabı" },
      ]),
      JSON.stringify([{ k: "azalan_claude", s: "s", at: 1000, t: "Azalan" }]),
      JSON.stringify({ claude: "aaaaaaaaaaaaaaaa", gemini: "bbbbbbbbbbbbbbbb" }),
      JSON.stringify({ claude: "c@ornek.com", gemini: "g@ornek.com" }),
      esJson.device_id
    );

  const izinRes = await worker.fetch(
    new Request(`${TEST_ORIGIN}/api/hesap/cihaz-izin`, {
      method: "POST",
      headers: { "content-type": "application/json", origin: TEST_ORIGIN, cookie: cerez },
      body: JSON.stringify({ device_id: esJson.device_id, izinli_saglayici: "m" }),
    }),
    env,
    bosCtx
  );
  assert.equal(izinRes.status, 200);
  const izinJson = await izinRes.json();
  assert.equal(izinJson.ok, true);
  assert.equal(izinJson.device_id, esJson.device_id);
  assert.equal(izinJson.izinli_saglayici, "m");

  const guncelCihaz = env.DB.sqlite
    .prepare("SELECT izinli_saglayici, srv_flags, flags, hesaplar, hesap_eposta FROM devices WHERE id = ?")
    .get(esJson.device_id);
  assert.equal(guncelCihaz.izinli_saglayici, "m");
  assert.deepEqual(JSON.parse(guncelCihaz.hesaplar), { gemini: "bbbbbbbbbbbbbbbb" });
  assert.deepEqual(JSON.parse(guncelCihaz.hesap_eposta), { gemini: "g@ornek.com" });
  assert.deepEqual(
    JSON.parse(guncelCihaz.srv_flags).map((f) => f.k),
    ["farkli_hesap_gemini"]
  );
  assert.deepEqual(JSON.parse(guncelCihaz.flags), []);

  // 6) kurulum-paket oran sınırı (60 saniyede 30 istekten fazlası 429)
  let sonDurum = 200;
  for (let i = 0; i < 32; i++) {
    const r = await worker.fetch(
      new Request(`${TEST_ORIGIN}/api/kurulum-paket?kod=XXXXX-YYYYY`, {
        headers: { "cf-connecting-ip": "198.51.100.77" },
      }),
      env,
      bosCtx
    );
    sonDurum = r.status;
  }
  assert.equal(sonDurum, 429);
});

test("handleIngest: izinli_saglayici filtreleme, v31->v40 Carbon çift sayım önleme ve mühür geçiş güvenliği", async () => {
  const eskiIkili = MANIFEST.ikili;
  const eskiGecmis = MANIFEST.gecmis;
  const shaV31Base = "31".repeat(32);
  const shaV40Base = "40".repeat(32);
  const shaV40Cm = "4c".repeat(32);

  MANIFEST.gecmis = {
    ...(eskiGecmis || {}),
    "31": { "darwin-arm64": shaV31Base },
  };
  MANIFEST.ikili = {
    surum: 40,
    dosyalar: {
      "darwin-arm64": { ad: "token-panosu-darwin-arm64", sha256: shaV40Base, boyut: 1000 },
      "darwin-arm64-cm": { ad: "token-panosu-darwin-arm64-cm", sha256: shaV40Cm, boyut: 900 },
    },
  };

  try {
    const env = yeniOrtam();
    const cerez = await kurVeGirisYap(env);
    const now = Math.floor(Date.now() / 1000);
    const saat = Math.floor(now / 3600) * 3600 - 3600;
    const dilim = Math.floor(now / 300) * 300 - 300;

    const devA = "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa";
    const secA = "aa".repeat(32);
    const devMert = "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb";
    const secMert = "bb".repeat(32);

    // A cihazı "cm" (yalnızca Claude + Gemini), B cihazı (Mert) henüz v20'de
    env.DB.sqlite
      .prepare(
        "INSERT INTO devices (id, label, secret, created_at, client_ver, izinli_saglayici) VALUES (?,?,?,?,?,?), (?,?,?,?,?,?)"
      )
      .run(devA, "Ece Mac", secA, now - 1000, 31, "cm", devMert, "Mert Mac", secMert, now - 1000, 20, "cgm");

    // ChatGPT için farklı bir ortak hesap kilidi tanımlayalım: A cihazında ChatGPT izinsiz olduğundan
    // A cihazından yanlışlıkla ChatGPT hesabı gelse bile kilit uyuşmazlığı tetiklememeli ve satırı düşürmeli!
    env.DB.sqlite
      .prepare("INSERT INTO hesap_kilit (saglayici, hesap_ozet, eposta, etiket, kilitlendi_at, cihaz_id) VALUES (?,?,?,?,?,?)")
      .run("chatgpt", "ffffffffffffffff", "ortak-gpt@ornek.com", "Ortak", now - 500, devMert);

    // 1) Mert'in v20 mühürsüz betiği geçiş aşamasında (enDusuk < 40 iken) kabul edilmeli!
    const mertRes1 = await worker.fetch(
      await gonderIstegiOlustur({
        device: devMert,
        secret: secMert,
        ts: now - 30,
        clientVer: 20,
        bodyObj: {
          v: 2,
          s: 20,
          o: "Darwin",
          rows: [{ h: saat, model: "claude-sonnet-4-6", sid: "0000000000000001", input: 1000, output: 500, cw5m: 0, cw1h: 0, cr: 0, messages: 1 }],
        },
      }),
      env,
      bosCtx
    );
    assert.equal(mertRes1.status, 200);

    // 2) A cihazı önce v31 (geçerli gecmis["31"] mührüyle) "agy-gemini" + izinsiz "gpt-5" satırı yollar:
    //    - v31 kabul edilmeli
    //    - "gpt-5" satırı ve ChatGPT hesabı sessizce düşürülmeli ("cm" izni gereği)
    //    - Yanıt paket: "cm" dönmeli!
    const sidOrtak = "00000000000000a1";
    const resV31 = await worker.fetch(
      await gonderIstegiOlustur({
        device: devA,
        secret: secA,
        ts: now - 20,
        clientVer: 31,
        target: "darwin-arm64",
        exeSha: shaV31Base,
        bodyObj: {
          v: 2,
          s: 31,
          o: "Darwin",
          hesap: { claude: "1111111111111111", chatgpt: "2222222222222222", gemini: "3333333333333333" },
          eposta: { claude: "ekip@ornek.com", chatgpt: "kisisel@baska.com", gemini: "ekip@ornek.com" },
          rows: [
            { h: saat, model: "agy-gemini", sid: sidOrtak, input: 100_000, output: 20_000, cw5m: 0, cw1h: 0, cr: 50_000, messages: 4 },
            { h: saat, model: "gpt-5", sid: "0000000000000099", input: 99_000, output: 10_000, cw5m: 0, cw1h: 0, cr: 0, messages: 2 },
          ],
          m: [
            { t: dilim, model: "agy-gemini", sid: sidOrtak, input: 100_000, output: 20_000, cw5m: 0, cw1h: 0, cr: 50_000, messages: 4 },
            { t: dilim, model: "gpt-5", sid: "0000000000000099", input: 99_000, output: 10_000, cw5m: 0, cw1h: 0, cr: 0, messages: 2 },
          ],
        },
      }),
      env,
      bosCtx
    );
    assert.equal(resV31.status, 200);
    const v31Json = await resV31.json();
    assert.equal(v31Json.paket, "cm");

    // ChatGPT verisi yazılmamış olmalı:
    const gptSayi = env.DB.sqlite.prepare("SELECT COUNT(*) AS c FROM usage_h WHERE device_id = ? AND model = 'gpt-5'").get(devA).c;
    assert.equal(gptSayi, 0);

    // 3) Sahte mühürle v40 taklidi yapan istek anında 403 ile reddedilmeli!
    const sahteRes = await worker.fetch(
      await gonderIstegiOlustur({
        device: devA,
        secret: secA,
        ts: now - 15,
        clientVer: 40,
        target: "darwin-arm64-cm",
        exeSha: shaV40Cm,
        sahteMuhur: "ff".repeat(32),
        bodyObj: { v: 2, s: 40, o: "Darwin", rows: [] },
      }),
      env,
      bosCtx
    );
    assert.equal(sahteRes.status, 403);

    // 4) A cihazı v40 izole ikilisine ("darwin-arm64-cm") yükseltildiğinde aynı sid için artık gerçek modelleri
    //    ("agy-gemini-4-carbon" + "agy-claude-opus-5-5") yollar:
    //    - Eski "agy-gemini" satırı usage_h ve usage_5m'den SİLİNMELİ (çift sayım olmamalı!)
    //    - Sahte "azalan_gemini" bayrağı OLUŞMAMALI!
    //    - calisan_paket = "cm" kaydedilmeli!
    const resV40 = await worker.fetch(
      await gonderIstegiOlustur({
        device: devA,
        secret: secA,
        ts: now - 10,
        clientVer: 40,
        target: "darwin-arm64-cm",
        exeSha: shaV40Cm,
        bodyObj: {
          v: 2,
          s: 40,
          o: "Darwin",
          rows: [
            { h: saat, model: "agy-gemini-4-carbon", sid: sidOrtak, input: 60_000, output: 12_000, cw5m: 0, cw1h: 0, cr: 30_000, messages: 2 },
            { h: saat, model: "agy-claude-opus-5-5", sid: sidOrtak, input: 40_000, output: 8_000, cw5m: 0, cw1h: 0, cr: 20_000, messages: 2 },
          ],
          m: [
            { t: dilim, model: "agy-gemini-4-carbon", sid: sidOrtak, input: 60_000, output: 12_000, cw5m: 0, cw1h: 0, cr: 30_000, messages: 2 },
            { t: dilim, model: "agy-claude-opus-5-5", sid: sidOrtak, input: 40_000, output: 8_000, cw5m: 0, cw1h: 0, cr: 20_000, messages: 2 },
          ],
        },
      }),
      env,
      bosCtx
    );
    assert.equal(resV40.status, 200);
    const v40Json = await resV40.json();
    assert.equal(v40Json.paket, "cm");

    const kalanSatirlar = env.DB.sqlite
      .prepare("SELECT model, input, output FROM usage_h WHERE device_id = ? ORDER BY model")
      .all(devA)
      .map((r) => ({ ...r }));
    assert.deepEqual(kalanSatirlar, [
      { model: "agy-claude-opus-5-5", input: 40_000, output: 8_000 },
      { model: "agy-gemini-4-carbon", input: 60_000, output: 12_000 },
    ]);

    const kalanDilimler = env.DB.sqlite
      .prepare("SELECT model FROM usage_5m WHERE device_id = ? ORDER BY model")
      .all(devA)
      .map((r) => r.model);
    assert.deepEqual(kalanDilimler, ["agy-claude-opus-5-5", "agy-gemini-4-carbon"]);

    const devADurum = env.DB.sqlite.prepare("SELECT calisan_paket, srv_flags FROM devices WHERE id = ?").get(devA);
    assert.equal(devADurum.calisan_paket, "cm");
    const srvFlagsA = JSON.parse(devADurum.srv_flags);
    assert.equal(
      srvFlagsA.some((f) => f.k && f.k.startsWith("azalan")),
      false,
      `Sahte azalan bayrağı oluşmamalı: ${devADurum.srv_flags}`
    );
    assert.equal(
      srvFlagsA.some((f) => f.k === "farkli_hesap_chatgpt"),
      false,
      "İzinsiz sağlayıcı için hesap kilidi uyarısı oluşmamalı"
    );

    // Mert hâlâ v20'de olduğu için muhurZorunlu henüz devreye girmemiş olmalı:
    const haftaRes1 = await worker.fetch(
      new Request(`${TEST_ORIGIN}/api/hafta?saglayici=gemini`, { headers: { cookie: cerez } }),
      env,
      bosCtx
    );
    assert.equal(haftaRes1.status, 200);
    const haftaJson1 = await haftaRes1.json();
    assert.equal(haftaJson1.muhurDurum.zorunlu, false);
    const devAOut = haftaJson1.devices.find((d) => d.id === devA);
    assert.equal(devAOut.izinli_saglayici, "cm");
    assert.equal(devAOut.calisan_paket, "cm");

    // 5) Şimdi Mert da v40'a yükseltilir -> TÜM cihazlar >=40 olduğu anda muhurZorunlu = true ve zorunluMinSurum = 40 kilitlenir!
    const mertV40Res = await worker.fetch(
      await gonderIstegiOlustur({
        device: devMert,
        secret: secMert,
        ts: now - 5,
        clientVer: 40,
        target: "darwin-arm64",
        exeSha: shaV40Base,
        bodyObj: { v: 2, s: 40, o: "Darwin", rows: [] },
      }),
      env,
      bosCtx
    );
    assert.equal(mertV40Res.status, 200);

    // Artık v20 de, eski v31 de kesin olarak 403 ile reddedilmeli!
    const eskiV20Red = await worker.fetch(
      await gonderIstegiOlustur({
        device: devMert,
        secret: secMert,
        ts: now - 3,
        clientVer: 20,
        bodyObj: { v: 2, s: 20, o: "Darwin", rows: [] },
      }),
      env,
      bosCtx
    );
    assert.equal(eskiV20Red.status, 403);

    const eskiV31Red = await worker.fetch(
      await gonderIstegiOlustur({
        device: devA,
        secret: secA,
        ts: now - 2,
        clientVer: 31,
        target: "darwin-arm64",
        exeSha: shaV31Base,
        bodyObj: { v: 2, s: 31, o: "Darwin", rows: [] },
      }),
      env,
      bosCtx
    );
    assert.equal(eskiV31Red.status, 403);
  } finally {
    MANIFEST.ikili = eskiIkili;
    MANIFEST.gecmis = eskiGecmis;
  }
});

test("GET /api/sistem-guncelleme GitHub sürüm kontrolünü önbellekler ve çevrimdışıyken sessizce açık kalır", async () => {
  const env = yeniOrtam();
  const cerez = await kurVeGirisYap(env);
  const orijinalFetch = globalThis.fetch;
  let disCagriSayisi = 0;

  globalThis.fetch = async (url) => {
    disCagriSayisi++;
    assert.match(String(url), /api\.github\.com\/repos\/doganau\/token-panosu\/releases\/latest/);
    return new Response(
      JSON.stringify({
        tag_name: "v42",
        html_url: "https://github.com/doganau/token-panosu/releases/tag/v42",
      }),
      { status: 200, headers: { "content-type": "application/json" } }
    );
  };

  try {
    const r1 = await worker.fetch(
      new Request(`${TEST_ORIGIN}/api/sistem-guncelleme`, { headers: { cookie: cerez } }),
      env,
      bosCtx
    );
    assert.equal(r1.status, 200);
    const j1 = await r1.json();
    assert.equal(j1.son_surum, 42);
    assert.equal(j1.guncel, false);
    assert.equal(j1.indir_url, "https://github.com/doganau/token-panosu/releases/tag/v42");
    assert.equal(disCagriSayisi, 1);

    // İkinci çağrı 6 saatlik sistem_ayar önbelleğinden dönmeli (ağ isteği atmamalı)
    const r2 = await worker.fetch(
      new Request(`${TEST_ORIGIN}/api/sistem-guncelleme`, { headers: { cookie: cerez } }),
      env,
      bosCtx
    );
    assert.equal(r2.status, 200);
    const j2 = await r2.json();
    assert.equal(j2.son_surum, 42);
    assert.equal(disCagriSayisi, 1);

    // Çevrimdışı durumda zorla=1 çağrılırsa hata fırlatmadan güvenli yanıt dönmeli
    globalThis.fetch = async () => {
      throw new Error("network unreachable");
    };
    const r3 = await worker.fetch(
      new Request(`${TEST_ORIGIN}/api/sistem-guncelleme?zorla=1`, { headers: { cookie: cerez } }),
      env,
      bosCtx
    );
    assert.equal(r3.status, 200);
    const j3 = await r3.json();
    assert.equal(typeof j3.mevcut_surum, "number");
  } finally {
    globalThis.fetch = orijinalFetch;
  }
});

test("Gemini ve Claude & GPT haftalık kota havuzları /api/gonder, /api/hafta ve /api/limit-analiz uçlarında tam yalıtılır", async () => {
  const env = yeniOrtam();
  const cerez = await kurVeGirisYap(env);
  const eskiIkili = MANIFEST.ikili;
  const eskiGecmis = MANIFEST.gecmis;

  const shaV40Cm = "4".repeat(64);
  const shaV41Cm = "5".repeat(64);
  MANIFEST.ikili = {
    surum: 41,
    dosyalar: {
      "darwin-arm64-cm": { sha256: shaV41Cm, boy: 2048 },
    },
  };
  MANIFEST.gecmis = {
    "40": { "darwin-arm64-cm": shaV40Cm },
  };

  try {
    const uyeMertRes = await worker.fetch(
      new Request(`${TEST_ORIGIN}/api/yonetim/uye`, {
        method: "POST",
        headers: { "content-type": "application/json", cookie: cerez, origin: TEST_ORIGIN },
        body: JSON.stringify({ ad: "Mert" }),
      }),
      env,
      bosCtx
    );
    const uyeMert = (await uyeMertRes.json()).id;

    const uyeDoganRes = await worker.fetch(
      new Request(`${TEST_ORIGIN}/api/yonetim/uye`, {
        method: "POST",
        headers: { "content-type": "application/json", cookie: cerez, origin: TEST_ORIGIN },
        body: JSON.stringify({ ad: "Doğan" }),
      }),
      env,
      bosCtx
    );
    const uyeDogan = (await uyeDoganRes.json()).id;

    async function cihazEkle(label, uyeId) {
      const kodRes = await worker.fetch(
        new Request(`${TEST_ORIGIN}/api/yonetim/kod`, {
          method: "POST",
          headers: { "content-type": "application/json", cookie: cerez, origin: TEST_ORIGIN },
          body: JSON.stringify({ label, uye_id: uyeId, izinli_saglayici: ["claude", "gemini"] }),
        }),
        env,
        bosCtx
      );
      const { code } = await kodRes.json();
      const sec = toHex(crypto.getRandomValues(new Uint8Array(32)));
      const esRes = await worker.fetch(
        new Request(`${TEST_ORIGIN}/api/eslestir`, {
          method: "POST",
          headers: { "content-type": "application/json" },
          body: JSON.stringify({ code, secret: sec }),
        }),
        env,
        bosCtx
      );
      const { device_id } = await esRes.json();
      return { id: device_id, sec };
    }

    const cMert = await cihazEkle("mert", uyeMert);
    const cMac = await cihazEkle("MacBook Pro", uyeDogan);

    const now = Math.floor(Date.now() / 1000);
    const saatBasi = Math.floor(now / 3600) * 3600 - 3600;
    const cumartesiReset = now + 2 * 86400; // 2 gün sonra (Gemini 7 günlük sıfırlama)
    const saliReset = now + 5 * 86400; // 5 gün sonra (Claude & GPT 7 günlük sıfırlama)
    const besSaatReset = now + 4 * 3600; // 4 saat sonra (5 saatlik pencere)

    // 1) Mert (v41) gerçek Gemini haftalık kotasını (%71.4, Cumartesi sıfırlama) ve eski "agy-gemini" + "agy-gemini-4-carbon" satırlarını gönderir:
    const rMert = await worker.fetch(
      await gonderIstegiOlustur({
        device: cMert.id,
        secret: cMert.sec,
        ts: now - 30,
        clientVer: 41,
        target: "darwin-arm64-cm",
        exeSha: shaV41Cm,
        bodyObj: {
          v: 2,
          s: 41,
          o: "Darwin",
          hesap: { gemini: "a1b2c3d4e5f60718" },
          eposta: { gemini: "ortak@ornek.com" },
          hr: { gemini: cumartesiReset },
          lp: {
            gemini: {
              t: now - 30,
              r: {
                "seven_day.used_percentage": 71.4,
                "seven_day.resets_at": cumartesiReset,
                "agy_gemini.used_percentage": 71.4,
                "agy_gemini.resets_at": cumartesiReset,
              },
            },
          },
          rows: [
            { h: saatBasi, model: "agy-gemini", sid: "1111111111111111", input: 100_000, output: 10_000, cw5m: 0, cw1h: 0, cr: 0, messages: 2 },
            { h: saatBasi, model: "agy-gemini-4-carbon", sid: "2222222222222222", input: 500_000, output: 50_000, cw5m: 0, cw1h: 0, cr: 0, messages: 5 },
          ],
        },
      }),
      env,
      bosCtx
    );
    assert.equal(rMert.status, 200);

    // "agy-gemini" veritabanına asla ham yazılmamalı, "agy-gemini-4-argon" olarak birleştirilmeli:
    const mertModeller = env.DB.sqlite.prepare("SELECT model FROM usage_h WHERE device_id = ? ORDER BY model").all(cMert.id).map((x) => x.model);
    assert.deepEqual(mertModeller, ["agy-gemini-4-argon", "agy-gemini-4-carbon"]);

    // 2) MacBook Pro (henüz v40) 5 saatlik Gemini (%2.3) + 7 günlük Claude & GPT (%97.7, Salı sıfırlama) sızıntılı yükünü gönderir:
    const rMac = await worker.fetch(
      await gonderIstegiOlustur({
        device: cMac.id,
        secret: cMac.sec,
        ts: now - 10,
        clientVer: 40,
        target: "darwin-arm64-cm",
        exeSha: shaV40Cm,
        bodyObj: {
          v: 2,
          s: 40,
          o: "Darwin",
          hesap: { gemini: "a1b2c3d4e5f60718" },
          eposta: { gemini: "ortak@ornek.com" },
          hr: { gemini: saliReset },
          lp: {
            gemini: {
              t: now - 10,
              r: {
                "five_hour.used_percentage": 2.3,
                "five_hour.resets_at": besSaatReset,
                "seven_day.used_percentage": 97.7,
                "seven_day.resets_at": saliReset,
                "agy_gemini.used_percentage": 2.3,
                "agy_gemini.resets_at": besSaatReset,
                "agy_gemini_5h.used_percentage": 2.3,
                "agy_gemini_5h.resets_at": besSaatReset,
                "agy_claude.used_percentage": 97.7,
                "agy_claude.resets_at": saliReset,
              },
            },
          },
          rows: [
            { h: saatBasi, model: "agy-claude-opus-5-5", sid: "3333333333333333", input: 1_000_000, output: 100_000, cw5m: 0, cw1h: 0, cr: 0, messages: 8 },
            { h: saatBasi, model: "agy-gpt-oss-120b", sid: "4444444444444444", input: 200_000, output: 50_000, cw5m: 0, cw1h: 0, cr: 0, messages: 3 },
          ],
        },
      }),
      env,
      bosCtx
    );
    assert.equal(rMac.status, 200);

    // oto_hafta_sifirlama.gemini Salı'ya (saliReset) kaymamalı, Mert'in Cumartesi sıfırlamasında kalmalı!
    const otoRow = env.DB.sqlite.prepare("SELECT reset_at FROM oto_hafta_sifirlama WHERE saglayici = 'gemini'").get();
    assert.ok(Math.abs(otoRow.reset_at - cumartesiReset) < 60, `Gemini sıfırlaması Salı'ya kaydı: ${otoRow.reset_at} vs ${cumartesiReset}`);

    // /api/hafta?saglayici=gemini yanıtında ana Gemini yüzdesi %71.4, 5 saatlik %2.3 ve ayrı Claude & GPT yüzdesi %97.7 olmalı!
    const haftaRes = await worker.fetch(
      new Request(`${TEST_ORIGIN}/api/hafta?saglayici=gemini`, { headers: { cookie: cerez } }),
      env,
      bosCtx
    );
    assert.equal(haftaRes.status, 200);
    const hafta = await haftaRes.json();
    assert.equal(hafta.limit.pct, 71.4);
    assert.equal(hafta.limit.h, 2.3);
    assert.equal(hafta.limit.cpct, 97.7);
    assert.ok(hafta.limit.agy3pPencere);
    assert.ok(hafta.limit.agy3pPencere.byDeviceUsd[cMac.id] > 5.9, "MacBook Pro Claude & GPT harcaması 3P penceresinde görünmeli");
    assert.equal(hafta.limit.agy3pPencere.byDeviceUsd[cMert.id] || 0, 0, "Mert'in Gemini modelleri 3P penceresine girmemeli");

    // /api/limit-analiz?saglayici=gemini saatlik toplamlarında usd_fablesiz yalnızca Gemini modellerini içermeli (Claude & GPT hariç):
    const analizRes = await worker.fetch(
      new Request(`${TEST_ORIGIN}/api/limit-analiz?saglayici=gemini`, { headers: { cookie: cerez } }),
      env,
      bosCtx
    );
    assert.equal(analizRes.status, 200);
    const analiz = await analizRes.json();
    assert.equal(analiz.etkin.pct, 71.4);
    assert.equal(analiz.saatlik.length, 1);
    const s0 = analiz.saatlik[0];
    // Mert Gemini modelleri: 0.6M input * $2 + 0.06M output * $10 = $1.2 + $0.6 = $1.80
    // MacBook Pro Claude Opus 5.5 (1M * $4 + 0.1M * $20 = $6.00) + GPT-OSS 120B (0.2M * $0.15 + 0.05M * $0.60 = $0.06) = $6.06
    assert.ok(Math.abs(s0.usd_fablesiz - 1.8) < 1e-6, `Gemini modelleri USD 1.80 bekleniyordu, gelen: ${s0.usd_fablesiz}`);
    assert.ok(Math.abs(s0.usd - 7.86) < 1e-6, `Toplam USD 7.86 bekleniyordu, gelen: ${s0.usd}`);
    assert.equal(s0.tok_ana, 660_000);
    assert.equal(s0.tok, 2_010_000);
  } finally {
    MANIFEST.ikili = eskiIkili;
    MANIFEST.gecmis = eskiGecmis;
  }
});
