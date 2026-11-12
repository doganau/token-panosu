// Model fiyat listesi: günde bir kez resmî fiyat sayfalarından (Anthropic + Google Gemini) ve LiteLLM'den yenilenir.
//
// Kaynak sırası (sonraki öncekinin üstüne yazar):
//   1. Yerleşik liste (aşağıda; resmî sayfalardan ve LiteLLM'den doğrulandı) — hiçbir kaynağa ulaşılamazsa bile eksiksiz çalışır
//   2. LiteLLM fiyat dosyası — Claude, OpenAI (ChatGPT / Codex / o-serisi) ve Google Gemini modellerinin tümü (tarihli adlar dahil)
//   3. Resmî fiyat sayfaları (platform.claude.com ve ai.google.dev markdown) — kendi sağlayıcılarında her zaman son sözü söyler
//
// Birim: $ / milyon token. Dizi sırası: [girdi, çıktı, cache yazma 5 dk, cache yazma 1 sa, cache okuma]
// İstem uzunluğuna göre fiyatlanan modellerde (ör. Haiku 5.5 >100B, Gemini Pro >200B, GPT-5.4+/6.x >272B) "u" alanı vardır:
// istemi (girdi + cache okuma + cache yazma) "esik" token'ı aşan her istek "f" fiyatlarından ödenir.
// İstemci böyle mesajları "<model>@uzun" adıyla ayrı satırda gönderir.

export const RESMI_ADRES = "https://platform.claude.com/docs/en/about-claude/pricing.md";
export const GEMINI_RESMI_ADRES = "https://ai.google.dev/gemini-api/docs/pricing.md.txt";
export const LITELLM_ADRES = "https://raw.githubusercontent.com/BerriAI/litellm/main/model_prices_and_context_window.json";
export const UZUN_EKI = "@uzun";

const CARBON_GECIS_MS = Date.parse("2026-12-01T00:00:00Z");
const FLASH_3_GECIS_MS = Date.parse("2027-01-01T00:00:00Z");

// Tarihe bağlı resmî fiyat geçişleri (ai.google.dev/gemini-api/docs/pricing.md.txt):
// - Gemini 4 Carbon / Argon: 30 Kas 2026 sonuna dek $2 / $10 / $0.10, 1 Ara 2026 itibarıyla $4 / $20 / $0.20 (düz tarife)
// - Gemini 3.8 / 3.7 / 3.6 Flash: 31 Ara 2026 sonuna dek $0.75 / $3.75 / $0.075, 1 Oca 2027 itibarıyla $1.50 / $7.50 / $0.15
export const DONEMLI = {
  "gemini-4-carbon": [
    { bitMs: CARBON_GECIS_MS, kayit: { b: [2, 10, 2, 2, 0.1] } },
    { basMs: CARBON_GECIS_MS, kayit: { b: [4, 20, 4, 4, 0.2] } },
  ],
  "gemini-4-argon": [
    { bitMs: CARBON_GECIS_MS, kayit: { b: [2, 10, 2, 2, 0.1] } },
    { basMs: CARBON_GECIS_MS, kayit: { b: [4, 20, 4, 4, 0.2] } },
  ],
  "gemini-3.8-flash": [
    { bitMs: FLASH_3_GECIS_MS, kayit: { b: [0.75, 3.75, 0.75, 0.75, 0.075] } },
    { basMs: FLASH_3_GECIS_MS, kayit: { b: [1.5, 7.5, 1.5, 1.5, 0.15] } },
  ],
  "gemini-3.7-flash": [
    { bitMs: FLASH_3_GECIS_MS, kayit: { b: [0.75, 3.75, 0.75, 0.75, 0.075] } },
    { basMs: FLASH_3_GECIS_MS, kayit: { b: [1.5, 7.5, 1.5, 1.5, 0.15] } },
  ],
  "gemini-3.6-flash": [
    { bitMs: FLASH_3_GECIS_MS, kayit: { b: [0.75, 3.75, 0.75, 0.75, 0.075] } },
    { basMs: FLASH_3_GECIS_MS, kayit: { b: [1.5, 7.5, 1.5, 1.5, 0.15] } },
  ],
};

export const YERLESIK = {
  // Anthropic Claude ($ / MTok: [girdi, çıktı, cache yazma 5 dk, cache yazma 1 sa, cache okuma])
  "claude-fable-5-2": { b: [8, 40, 10, 16, 0.2] },
  "claude-mythos-5-2": { b: [8, 40, 10, 16, 0.2] },
  "claude-fable-5-1": { b: [10, 50, 12.5, 20, 0.25] },
  "claude-mythos-5-1": { b: [10, 50, 12.5, 20, 0.25] },
  "claude-fable-5": { b: [10, 50, 12.5, 20, 1] },
  "claude-mythos-5": { b: [10, 50, 12.5, 20, 1] },
  "claude-mythos-preview": { b: [25, 125, 31.25, 50, 2.5] },
  "claude-opus-5-5": { b: [4, 20, 5, 8, 0.2] },
  "claude-opus-5": { b: [5, 25, 6.25, 10, 0.5] },
  "claude-opus-4-8": { b: [5, 25, 6.25, 10, 0.5] },
  "claude-opus-4-7": { b: [5, 25, 6.25, 10, 0.5] },
  "claude-opus-4-6": { b: [5, 25, 6.25, 10, 0.5] },
  "claude-opus-4-5": { b: [5, 25, 6.25, 10, 0.5] },
  "claude-opus-4-1": { b: [15, 75, 18.75, 30, 1.5] },
  "claude-opus-4": { b: [15, 75, 18.75, 30, 1.5] },
  "claude-sonnet-5-5": { b: [2, 10, 2.5, 4, 0.1] },
  "claude-sonnet-5": { b: [2, 10, 2.5, 4, 0.2] },
  "claude-sonnet-4-6": { b: [3, 15, 3.75, 6, 0.3] },
  "claude-sonnet-4-5": { b: [3, 15, 3.75, 6, 0.3] },
  "claude-sonnet-4": { b: [3, 15, 3.75, 6, 0.3] },
  "claude-haiku-5-5": { b: [0.1, 0.5, 0.125, 0.2, 0.01], u: { esik: 100000, f: [0.5, 2.5, 0.625, 1, 0.05] } },
  "claude-haiku-4-5": { b: [1, 5, 1.25, 2, 0.1] },
  "claude-3-7-sonnet": { b: [3, 15, 3.75, 6, 0.3] },
  "claude-3-5-sonnet": { b: [3, 15, 3.75, 6, 0.3] },
  "claude-3-5-haiku": { b: [0.8, 4, 1, 1.6, 0.08] },
  "claude-3-opus": { b: [15, 75, 18.75, 30, 1.5] },

  // OpenAI ChatGPT / Codex API eşdeğeri ($ / MTok)
  "gpt-6.1-sol": { b: [2, 10, 2, 2, 0.1], u: { esik: 272000, f: [4, 15, 4, 4, 0.2] } },
  "gpt-6.1-terra": { b: [1, 5, 1, 1, 0.05], u: { esik: 272000, f: [2, 7.5, 2, 2, 0.1] } },
  "gpt-6": { b: [2, 10, 9.375, 9.375, 0.2], u: { esik: 272000, f: [4, 15, 18.75, 18.75, 0.4] } },
  "gpt-6-pro": { b: [60, 240, 93.75, 93.75, 15], u: { esik: 272000, f: [120, 360, 187.5, 187.5, 30] } },
  "gpt-6-mini": { b: [1.2, 6, 1.875, 1.875, 0.12], u: { esik: 272000, f: [2.4, 9, 3.75, 3.75, 0.24] } },
  "gpt-6-sol": { b: [2, 10, 2, 2, 0.2], u: { esik: 272000, f: [4, 15, 4, 4, 0.4] } },
  "gpt-6-luna": { b: [0.1, 0.5, 0.1, 0.1, 0.01], u: { esik: 272000, f: [0.2, 0.75, 0.2, 0.2, 0.02] } },
  "gpt-6-astra": { b: [10, 50, 10, 10, 1], u: { esik: 272000, f: [20, 75, 20, 20, 2] } },
  "gpt-6-rosalind": { b: [15, 75, 15, 15, 1.5], u: { esik: 272000, f: [30, 112.5, 30, 30, 3] } },
  "gpt-6-cyber": { b: [20, 100, 20, 20, 2], u: { esik: 272000, f: [40, 150, 40, 40, 4] } },
  "gpt-5.6": { b: [4, 20, 4.6875, 4.6875, 0.4], u: { esik: 272000, f: [8, 30, 9.375, 9.375, 0.8] } },
  "gpt-5.6-pro": { b: [30, 180, 46.875, 46.875, 7.5], u: { esik: 272000, f: [60, 270, 93.75, 93.75, 15] } },
  "gpt-5.6-mini": { b: [0.75, 4.5, 1.1875, 1.1875, 0.075] },
  "gpt-5.6-sol": { b: [4, 20, 4, 4, 0.4], u: { esik: 272000, f: [8, 30, 8, 8, 0.8] } },
  "gpt-5.6-terra": { b: [2, 12, 2, 2, 0.2], u: { esik: 272000, f: [4, 18, 4, 4, 0.4] } },
  "gpt-5.6-luna": { b: [0.2, 1.2, 0.2, 0.2, 0.02], u: { esik: 272000, f: [0.4, 1.8, 0.4, 0.4, 0.04] } },
  "gpt-5.6-cyber": { b: [12.5, 75, 12.5, 12.5, 1.25], u: { esik: 272000, f: [25, 112.5, 25, 25, 2.5] } },
  "gpt-5.5": { b: [5, 30, 5, 5, 0.5], u: { esik: 272000, f: [10, 45, 10, 10, 1] } },
  "gpt-5.5-pro": { b: [30, 180, 30, 30, 3], u: { esik: 272000, f: [60, 270, 60, 60, 6] } },
  "gpt-5.5-cyber": { b: [12.5, 75, 12.5, 12.5, 1.25] },
  "gpt-5.4": { b: [2.5, 15, 2.5, 2.5, 0.25], u: { esik: 272000, f: [5, 22.5, 5, 5, 0.5] } },
  "gpt-5.4-pro": { b: [30, 180, 30, 30, 7.5], u: { esik: 272000, f: [60, 270, 60, 60, 15] } },
  "gpt-5.4-mini": { b: [0.75, 4.5, 0.75, 0.75, 0.075] },
  "gpt-5.4-nano": { b: [0.2, 1.25, 0.2, 0.2, 0.02] },
  "gpt-5.3-codex": { b: [1.75, 14, 1.75, 1.75, 0.175] },
  "gpt-5.3": { b: [1.75, 14, 1.75, 1.75, 0.175] },
  "gpt-5.2-codex": { b: [1.75, 14, 1.75, 1.75, 0.175] },
  "gpt-5.2": { b: [1.75, 14, 1.75, 1.75, 0.175] },
  "gpt-5.2-pro": { b: [21, 168, 21, 21, 5.25] },
  "gpt-5.1-codex": { b: [1.25, 10, 1.25, 1.25, 0.125] },
  "gpt-5.1-codex-max": { b: [1.25, 10, 1.25, 1.25, 0.125] },
  "gpt-5.1-codex-mini": { b: [0.25, 2, 0.25, 0.25, 0.025] },
  "gpt-5.1": { b: [1.25, 10, 1.25, 1.25, 0.125] },
  "gpt-5-codex": { b: [1.25, 10, 1.25, 1.25, 0.125] },
  "gpt-5": { b: [1.25, 10, 1.25, 1.25, 0.125] },
  "gpt-5-pro": { b: [15, 120, 15, 15, 3.75] },
  "gpt-5-mini": { b: [0.25, 2, 0.25, 0.25, 0.025] },
  "gpt-5-nano": { b: [0.05, 0.4, 0.05, 0.05, 0.005] },
  "gpt-4.1": { b: [2, 8, 2, 2, 0.5] },
  "gpt-4.1-mini": { b: [0.4, 1.6, 0.4, 0.4, 0.1] },
  "gpt-4.1-nano": { b: [0.1, 0.4, 0.1, 0.1, 0.025] },
  "gpt-4o": { b: [2.5, 10, 2.5, 2.5, 1.25] },
  "gpt-4o-mini": { b: [0.15, 0.6, 0.15, 0.15, 0.075] },
  "codex-mini": { b: [1.5, 6, 1.5, 1.5, 0.375] },
  "o4-mini": { b: [1.1, 4.4, 1.1, 1.1, 0.275] },
  "o3-pro": { b: [20, 80, 20, 20, 5] },
  "o3": { b: [2, 8, 2, 2, 0.5] },
  "o3-mini": { b: [1.1, 4.4, 1.1, 1.1, 0.55] },
  "o1-pro": { b: [150, 600, 150, 150, 75] },
  "o1": { b: [15, 60, 15, 15, 7.5] },
  "gpt-oss-120b": { b: [0.15, 0.6, 0.15, 0.15, 0.015] },
  "gpt-oss-20b": { b: [0.075, 0.3, 0.075, 0.075, 0.0075] },

  // Google Gemini API resmî fiyat tablosu ($ / MTok — ai.google.dev/gemini-api/docs/pricing.md.txt)
  "gemini-4-carbon": { b: [2, 10, 2, 2, 0.1] },
  "gemini-4-argon": { b: [2, 10, 2, 2, 0.1] },
  "gemini-3.8-flash": { b: [0.75, 3.75, 0.75, 0.75, 0.075] },
  "gemini-3.8-flash-cyber": { b: [1.5, 7.5, 1.5, 1.5, 0.15] },
  "gemini-3.7-flash": { b: [0.75, 3.75, 0.75, 0.75, 0.075] },
  "gemini-3.6-flash": { b: [0.75, 3.75, 0.75, 0.75, 0.075] },
  "gemini-3.5-flash": { b: [1.5, 9, 1.5, 1.5, 0.15] },
  "gemini-3.5-flash-lite": { b: [0.3, 2.5, 0.3, 0.3, 0.03] },
  "gemini-omni-1.2-flash": { b: [1.5, 9, 1.5, 1.5, 0.15] },
  "gemini-omni-1.1-flash": { b: [1.5, 9, 1.5, 1.5, 0.15] },
  "gemini-3.1-pro": { b: [2, 12, 2, 2, 0.2], u: { esik: 200000, f: [4, 18, 4, 4, 0.4] } },
  "gemini-3.1-pro-preview": { b: [2, 12, 2, 2, 0.2], u: { esik: 200000, f: [4, 18, 4, 4, 0.4] } },
  "gemini-3.1-flash-lite": { b: [0.25, 1.5, 0.25, 0.25, 0.025] },
  "gemini-3.1-flash-lite-preview": { b: [0.25, 1.5, 0.25, 0.25, 0.025] },
  "gemini-3-pro": { b: [2, 12, 2, 2, 0.2], u: { esik: 200000, f: [4, 18, 4, 4, 0.4] } },
  "gemini-3-pro-preview": { b: [2, 12, 2, 2, 0.2], u: { esik: 200000, f: [4, 18, 4, 4, 0.4] } },
  "gemini-3-flash": { b: [0.5, 3, 0.5, 0.5, 0.05] },
  "gemini-3-flash-preview": { b: [0.5, 3, 0.5, 0.5, 0.05] },
  "gemini-2.5-pro": { b: [1.25, 10, 1.25, 1.25, 0.125], u: { esik: 200000, f: [2.5, 15, 2.5, 2.5, 0.25] } },
  "gemini-2.5-flash": { b: [0.3, 2.5, 0.3, 0.3, 0.03] },
  "gemini-2.5-flash-lite": { b: [0.1, 0.4, 0.1, 0.1, 0.01] },
  "gemini-2.0-flash": { b: [0.1, 0.4, 0.1, 0.1, 0.025] },
  "gemini-pro-latest": { b: [2, 12, 2, 2, 0.2], u: { esik: 200000, f: [4, 18, 4, 4, 0.4] } },
  "gemini-flash-latest": { b: [0.5, 3, 0.5, 0.5, 0.05] },
  "gemini-flash-lite-latest": { b: [0.25, 1.5, 0.25, 0.25, 0.025] },
};

// Antigravity (Antigravity uygulaması, Antigravity IDE ve agy CLI) kullanımı "agy-<model>" adıyla gelir.
// Hangi model seçilmiş olursa olsun kota Google (Antigravity) aboneliğinden düştüğü için Gemini sağlayıcısına sayılır;
// API karşılığı fiyatı ise alttaki gerçek modelin (ör. claude-opus-5-5, gemini-4-argon, gpt-oss-120b) fiyatıyla hesaplanır.
export const AGY_EKI = "agy-";
export function saglayiciOf(model) {
  const m = String(model || "").toLowerCase();
  if (m.startsWith(AGY_EKI)) return "gemini";
  if (m.startsWith("gemini-")) return "gemini";
  if (m.startsWith("claude-")) return "claude";
  return "chatgpt";
}

// Antigravity içinde çalıştırılan 3. parti modeller (Claude ve GPT/OpenAI):
// Gemini modellerinin haftalık kotasına değil, ayrı özel "Claude & GPT" haftalık kotasına etki eder.
export function gemini3pModelMi(model) {
  let m = String(model || "").toLowerCase();
  if (m.endsWith(UZUN_EKI)) m = m.slice(0, -UZUN_EKI.length);
  return /^agy-(?:claude|gpt|o[134](?:$|-)|openai|codex)/.test(m);
}

const AILELER = ["fable", "mythos", "opus", "sonnet", "haiku"];
const MODEL_AD_RE = /^[a-z0-9][a-z0-9.\-_]{0,57}$/;
const ATLA_MODALITE_RE = /(?:^|-)(?:tts|transcribe|realtime|audio|live|image|embedding|moderation|whisper|dall-e|sora|veo|lyria|imagen|banana|robotics|computer-use|search)(?:$|-)/;

const yuvarla6 = (x) => (Number.isFinite(x) ? Math.round(x * 1e6) / 1e6 : NaN);
const gecerliFiyat = (a) => Array.isArray(a) && a.length === 5 && a.every((x) => Number.isFinite(x) && x >= 0 && x <= 2000) && a[0] > 0 && a[1] > 0;
const gecerliKayit = (k) => k && gecerliFiyat(k.b) && (!k.u || (Number.isSafeInteger(k.u.esik) && k.u.esik > 0 && gecerliFiyat(k.u.f)));

// "Claude Opus 5.5" -> "claude-opus-5-5"
export function adDonustur(goruntu) {
  const s = goruntu.toLowerCase().replace(/\s+/g, " ").trim();
  if (!/^claude [a-z]+ \d+(\.\d+)?$/.test(s)) return null;
  return s.replace(/ /g, "-").replace(/\./g, "-");
}

const dolar = (h) => {
  const m = /\$\s*([0-9]+(?:\.[0-9]+)?)\s*\/\s*MTok/i.exec(h);
  return m ? Number(m[1]) : NaN;
};

// Anthropic resmî sayfasının "Model pricing" tablosu: | Model | Base input | 5m cache writes | 1h cache writes | Cache hits | Output |
export function resmiAyristir(md) {
  const bas = md.indexOf("## Model pricing");
  if (bas < 0) return {};
  const bolum = md.slice(bas, md.indexOf("\n## ", bas + 5) > 0 ? md.indexOf("\n## ", bas + 5) : undefined);
  const satirlar = bolum.split("\n").filter((l) => l.trim().startsWith("|"));
  if (satirlar.length < 3) return {};
  const baslik = satirlar[0].split("|").map((x) => x.trim().toLowerCase());
  const sutun = (re) => baslik.findIndex((h) => re.test(h));
  const iG = sutun(/base input/), i5 = sutun(/5m cache/), i1 = sutun(/1h cache/), iO = sutun(/cache hits/), iC = sutun(/^output/);
  if ([iG, i5, i1, iO, iC].some((i) => i < 0)) return {};
  const sonuc = {};
  for (const l of satirlar.slice(2)) {
    const h = l.split("|").map((x) => x.trim());
    let ad = h[1] || "";
    const uzun = /for prompts over ([0-9,]+) tokens/i.exec(ad);
    ad = ad.replace(/<sup>.*?<\/sup>/g, "").replace(/\(\[[^\]]*\]\([^)]*\)\)/g, "").replace(/\([^)]*\)/g, "");
    const id = adDonustur(ad);
    if (!id) continue;
    const f = [dolar(h[iG]), dolar(h[iC]), dolar(h[i5]), dolar(h[i1]), dolar(h[iO])];
    if (!gecerliFiyat(f)) continue;
    if (uzun) {
      const esik = Number(uzun[1].replace(/,/g, ""));
      sonuc[id] = { ...(sonuc[id] || {}), u: { esik, f } };
    } else {
      sonuc[id] = { ...(sonuc[id] || {}), b: f };
    }
  }
  for (const k of Object.keys(sonuc)) if (!gecerliKayit(sonuc[k])) delete sonuc[k];
  return sonuc;
}

// Google Gemini resmî fiyat sayfasındaki (pricing.md.txt) Standard sekmesi tablolarını ayrıştırır.
function geminiHucreFiyat(hucre, simdiMs) {
  const temiz = String(hucre || "")
    .replace(/\$[0-9.,]+\s*\/\s*1,000,000\s*tokens\s*per\s*hour[^\n]*/gi, "")
    .replace(/\$[0-9.,]+[^\n]*?\(\s*audio[^)]*\)/gi, "");
  const uzunEs = /\$([0-9]+(?:\.[0-9]+)?)\s*,\s*prompts\s*>\s*(\d+)k/i.exec(temiz);
  const kisaEs = /\$([0-9]+(?:\.[0-9]+)?)\s*,\s*prompts\s*<=\s*(\d+)k/i.exec(temiz);
  if (kisaEs) {
    return {
      taban: Number(kisaEs[1]),
      uzun: uzunEs ? Number(uzunEs[1]) : NaN,
      esik: uzunEs ? Number(uzunEs[2]) * 1000 : 0,
    };
  }
  // Tarih geçişli fiyatlar: "$2.00 through November 30, 2026. $4.00 starting December 1, 2026."
  const gecis = /\$([0-9]+(?:\.[0-9]+)?)\s+starting\s+([A-Za-z]+\s+\d{1,2},\s*\d{4})/i.exec(temiz);
  if (gecis) {
    const basMs = Date.parse(gecis[2] + " 00:00:00 UTC");
    if (Number.isFinite(basMs) && simdiMs >= basMs) {
      return { taban: Number(gecis[1]), uzun: NaN, esik: 0 };
    }
  }
  const ilk = /\$([0-9]+(?:\.[0-9]+)?)/.exec(temiz);
  return { taban: ilk ? Number(ilk[1]) : NaN, uzun: NaN, esik: 0 };
}

export function geminiResmiAyristir(md, simdiMs = Date.now()) {
  const sonuc = {};
  const parcalar = String(md || "").split(/\n##\s+/);
  for (const parca of parcalar.slice(1)) {
    const ilkSatir = parca.split("\n", 1)[0] || "";
    const idEs = /\{%\s*id="(gemini-[a-z0-9.\-]+)"\s*%\}/i.exec(ilkSatir);
    if (!idEs) continue;
    const anaId = idEs[1].toLowerCase();
    if (!MODEL_AD_RE.test(anaId) || ATLA_MODALITE_RE.test(anaId)) continue;

    // Yalnızca başlığın altındaki left-column içindeki gerçek model kimliklerini al (uyarı kutularındaki göç önerilerini alma)
    const solSutun = /\{%\s*left-column[^%]*%\}([\s\S]*?)\{%\s*\/left-column\s*%\}/i.exec(parca);
    const adayIdler = new Set([anaId]);
    if (solSutun) {
      for (const m of solSutun[1].matchAll(/`(gemini-[a-z0-9.\-]+)`/gi)) {
        const kod = m[1].toLowerCase();
        if (MODEL_AD_RE.test(kod) && !ATLA_MODALITE_RE.test(kod) && !kod.endsWith("-customtools")) adayIdler.add(kod);
      }
    }

    const stdIdx = parca.indexOf('{% tab title="Standard" %}');
    const kaynakMetin = stdIdx >= 0 ? parca.slice(stdIdx, parca.indexOf("{% /tab %}", stdIdx) > 0 ? parca.indexOf("{% /tab %}", stdIdx) : undefined) : parca;
    const tblBas = kaynakMetin.indexOf("{% table %}");
    if (tblBas < 0) continue;
    const tblSon = kaynakMetin.indexOf("{% /table %}", tblBas);
    const tbl = kaynakMetin.slice(tblBas + 11, tblSon > 0 ? tblSon : undefined);

    let g = null, c = null, ok = null;
    for (const blok of tbl.split(/\n\s*---\s*\n/)) {
      const maddeler = [];
      for (const sat of blok.split("\n")) {
        const m = /^\s*\*\s+(.*)$/.exec(sat);
        if (m) maddeler.push(m[1].trim());
        else if (maddeler.length > 0 && sat.trim() && !sat.trim().startsWith("{%")) {
          maddeler[maddeler.length - 1] += "\n" + sat.trim();
        }
      }
      if (maddeler.length < 3) continue;
      const ozellik = maddeler[0].toLowerCase();
      const ucretli = maddeler[2];
      if (ozellik.startsWith("input price")) g = geminiHucreFiyat(ucretli, simdiMs);
      else if (ozellik.startsWith("output price")) c = geminiHucreFiyat(ucretli, simdiMs);
      else if (ozellik.startsWith("context caching")) ok = geminiHucreFiyat(ucretli, simdiMs);
    }
    if (!g || !c || !Number.isFinite(g.taban) || !Number.isFinite(c.taban)) continue;
    const cr = ok && Number.isFinite(ok.taban) ? ok.taban : yuvarla6(g.taban * 0.1);
    const kayit = { b: [g.taban, c.taban, g.taban, g.taban, cr] };
    if (g.esik > 0 && Number.isFinite(g.uzun) && Number.isFinite(c.uzun)) {
      const crUzun = ok && Number.isFinite(ok.uzun) ? ok.uzun : yuvarla6(g.uzun * 0.1);
      const uf = [g.uzun, c.uzun, g.uzun, g.uzun, crUzun];
      if (gecerliFiyat(uf)) kayit.u = { esik: g.esik, f: uf };
    }
    if (!gecerliKayit(kayit)) continue;
    for (const kod of adayIdler) {
      sonuc[kod] = kayit;
      if (kod.endsWith("-preview")) {
        const kisa = kod.slice(0, -"-preview".length);
        if (MODEL_AD_RE.test(kisa) && !sonuc[kisa]) sonuc[kisa] = kayit;
      }
    }
  }
  return sonuc;
}

// LiteLLM: $/token -> $/MTok. Anthropic (claude-*), OpenAI (gpt-*, o1*, o3*, o4*, codex-*) ve Google Gemini (gemini/*) modellerini alır.
export function litellmAyristir(veri) {
  const sonuc = {};
  const m = (x) => (Number.isFinite(x) ? Math.round(x * 1e6 * 1e6) / 1e6 : NaN);
  for (const [hamAd, v] of Object.entries(veri || {})) {
    if (!v || typeof v !== "object") continue;
    if (v.mode && v.mode !== "chat" && v.mode !== "responses" && v.mode !== "completion") continue;
    const saglayici = v.litellm_provider;
    let ad = "";
    if (saglayici === "anthropic") {
      if (!/^claude-[a-z0-9.\-]+$/.test(hamAd)) continue;
      ad = hamAd;
    } else if (saglayici === "openai") {
      const k = hamAd.startsWith("openai/") ? hamAd.slice(7) : hamAd;
      if (!/^(?:gpt-[a-z0-9.\-]+|o[134](?:-[a-z0-9.\-]+)?|codex-[a-z0-9.\-]+)$/.test(k)) continue;
      if (k.startsWith("gpt-3.5") || k.startsWith("gpt-4-0") || k.startsWith("gpt-4-1106") || k.startsWith("gpt-4-32k") || k.startsWith("gpt-4-vision")) continue;
      if (k.endsWith("-chat-latest")) continue;
      ad = k;
    } else if (saglayici === "gemini") {
      const k = hamAd.startsWith("gemini/") ? hamAd.slice(7) : hamAd;
      if (!/^gemini-[a-z0-9.\-]+$/.test(k)) continue;
      if (k.startsWith("gemini-1.") || k.startsWith("gemini-1-") || k.endsWith("-customtools")) continue;
      ad = k;
    } else {
      continue;
    }
    if (!MODEL_AD_RE.test(ad) || ATLA_MODALITE_RE.test(ad)) continue;

    const inTok = v.input_cost_per_token;
    const outTok = v.output_cost_per_token;
    if (!Number.isFinite(inTok) || !Number.isFinite(outTok) || inTok <= 0 || outTok <= 0) continue;

    const girdi = m(inTok), cikti = m(outTok);
    let cw5, cw1, cr;
    if (saglayici === "anthropic") {
      cw5 = m(v.cache_creation_input_token_cost ?? inTok * 1.25);
      cw1 = m(v.cache_creation_input_token_cost_above_1hr ?? inTok * 2);
      cr = m(v.cache_read_input_token_cost ?? inTok * 0.1);
    } else if (saglayici === "openai") {
      // OpenAI standart önbellek yazma için ek ücret almaz (normal girdi fiyatı)
      const cwHam = v.cache_creation_input_token_cost ?? inTok;
      cw5 = m(cwHam);
      cw1 = m(v.cache_creation_input_token_cost_above_1hr ?? cwHam);
      cr = m(v.cache_read_input_token_cost ?? inTok * 0.25);
    } else {
      const cwHam = v.cache_creation_input_token_cost ?? inTok;
      cw5 = m(cwHam);
      cw1 = m(v.cache_creation_input_token_cost_above_1hr ?? cwHam);
      cr = m(v.cache_read_input_token_cost ?? inTok * 0.1);
    }

    const k = { b: [girdi, cikti, cw5, cw1, cr] };
    for (const anahtar of Object.keys(v)) {
      const t = /^input_cost_per_token_above_(\d+)k_tokens$/.exec(anahtar);
      if (!t) continue;
      const s = t[1] + "k_tokens";
      const inU = v[anahtar];
      const outU = v[`output_cost_per_token_above_${s}`];
      if (!Number.isFinite(inU) || !Number.isFinite(outU)) continue;
      let uCw5, uCw1, uCr;
      if (saglayici === "anthropic") {
        uCw5 = m(v[`cache_creation_input_token_cost_above_${s}`] ?? inU * 1.25);
        uCw1 = m(v[`cache_creation_input_token_cost_above_1hr_above_${s}`] ?? inU * 2);
        uCr = m(v[`cache_read_input_token_cost_above_${s}`] ?? inU * 0.1);
      } else if (saglayici === "openai") {
        const uCwHam = v[`cache_creation_input_token_cost_above_${s}`] ?? inU;
        uCw5 = m(uCwHam);
        uCw1 = m(v[`cache_creation_input_token_cost_above_1hr_above_${s}`] ?? uCwHam);
        uCr = m(v[`cache_read_input_token_cost_above_${s}`] ?? (cr > 0 && girdi > 0 ? inU * (cr / girdi) : inU * 0.1));
      } else {
        const uCwHam = v[`cache_creation_input_token_cost_above_${s}`] ?? inU;
        uCw5 = m(uCwHam);
        uCw1 = m(v[`cache_creation_input_token_cost_above_1hr_above_${s}`] ?? uCwHam);
        uCr = m(v[`cache_read_input_token_cost_above_${s}`] ?? inU * 0.1);
      }
      const f = [m(inU), m(outU), uCw5, uCw1, uCr];
      if (gecerliFiyat(f)) k.u = { esik: Number(t[1]) * 1000, f };
    }
    if (gecerliKayit(k)) sonuc[ad] = k;
  }
  if (sonuc["codex-mini-latest"] && !sonuc["codex-mini"]) sonuc["codex-mini"] = sonuc["codex-mini-latest"];
  return sonuc;
}

// Liste yeterince dolu ve tanıdık mı (sayfa biçimi değişirse yarım liste kaydedilmesin)
const claudeTamMi = (l, enAz) =>
  Object.keys(l).length >= enAz && AILELER.filter((a) => a !== "mythos").every((a) => Object.keys(l).some((k) => k.includes(a)));
const geminiTamMi = (l) => Object.keys(l).length >= 5 && Object.keys(l).some((k) => k.includes("flash"));

export async function fiyatlariIndir(fetchFn = fetch) {
  const notlar = [];
  let claudeResmi = {}, geminiResmi = {}, litellm = {};
  const kaynaklar = [];

  const [rClaude, rGemini, rLite] = await Promise.allSettled([
    fetchFn(RESMI_ADRES, { headers: { accept: "text/markdown, text/plain" }, cf: { cacheTtl: 0 } }),
    fetchFn(GEMINI_RESMI_ADRES, { headers: { accept: "text/markdown, text/plain" }, cf: { cacheTtl: 0 } }),
    fetchFn(LITELLM_ADRES),
  ]);

  if (rClaude.status === "fulfilled" && rClaude.value.ok) {
    claudeResmi = resmiAyristir(await rClaude.value.text());
    if (claudeTamMi(claudeResmi, 8)) kaynaklar.push("Anthropic resmî");
    else { notlar.push("Anthropic resmî tablo okunamadı"); claudeResmi = {}; }
  } else {
    notlar.push(rClaude.status === "fulfilled" ? `Anthropic ${rClaude.value.status}` : "Anthropic sayfasına ulaşılamadı");
  }

  if (rGemini.status === "fulfilled" && rGemini.value.ok) {
    geminiResmi = geminiResmiAyristir(await rGemini.value.text());
    if (geminiTamMi(geminiResmi)) kaynaklar.push("Gemini resmî");
    else { notlar.push("Gemini resmî tablo okunamadı"); geminiResmi = {}; }
  } else {
    notlar.push(rGemini.status === "fulfilled" ? `Gemini ${rGemini.value.status}` : "Gemini sayfasına ulaşılamadı");
  }

  if (rLite.status === "fulfilled" && rLite.value.ok) {
    litellm = litellmAyristir(await rLite.value.json());
    if (claudeTamMi(litellm, 20)) kaynaklar.push("LiteLLM (Claude/OpenAI/Gemini)");
    else { notlar.push("LiteLLM listesi eksik"); litellm = {}; }
  } else {
    notlar.push(rLite.status === "fulfilled" ? `LiteLLM ${rLite.value.status}` : "LiteLLM'e ulaşılamadı");
  }

  if (!kaynaklar.length) return { liste: null, kaynak: "", not: notlar.join("; ") };
  return {
    liste: { ...litellm, ...geminiResmi, ...claudeResmi },
    kaynak: kaynaklar.join(" + "),
    not: notlar.join("; "),
  };
}

const ESKI_MODEL_ESLEME = {
  "gemini-3-flash-a": "gemini-3.5-flash",
  "gemini-3-flash-b": "gemini-3.5-flash",
  "gemini-default": "gemini-3.5-flash",
  "gemini-pro-a": "gemini-3.1-pro",
  "gemini-pro-b": "gemini-3.1-pro",
};
const SEVIYE_EKI_RE = /(?:-(?:tiered|medium|high|low|thinking|minimal))+$/;

function zamanMsCoz(zaman) {
  if (zaman === undefined || zaman === null) return Date.now();
  if (typeof zaman === "number") return Number.isFinite(zaman) ? (zaman > 0 && zaman < 1e11 ? zaman * 1000 : zaman) : Date.now();
  if (zaman instanceof Date) return zaman.getTime();
  const p = Date.parse(String(zaman));
  return Number.isFinite(p) ? p : Date.now();
}

export function donemliKayit(anahtar, tabanKayit, zaman) {
  if (tabanKayit && tabanKayit.ozel) return tabanKayit;
  const dilimler = Object.hasOwn(DONEMLI, anahtar) ? DONEMLI[anahtar] : null;
  if (!dilimler) return tabanKayit;
  const ms = zamanMsCoz(zaman);
  for (const d of dilimler) {
    if ((d.basMs === undefined || ms >= d.basMs) && (d.bitMs === undefined || ms < d.bitMs)) {
      return d.kayit;
    }
  }
  return tabanKayit;
}

// Etkin liste: yerleşik < indirilen (dönemli resmi geçişler uygulanmış haliyle)
export const birlestir = (indirilen, zaman) => {
  const ham = { ...YERLESIK, ...(indirilen || {}) };
  if (Object.hasOwn(ham, "agy-gemini") && !(indirilen && indirilen["agy-gemini"] && indirilen["agy-gemini"].ozel)) {
    delete ham["agy-gemini"];
  }
  for (const anahtar of Object.keys(DONEMLI)) {
    if (!ham[anahtar] || !ham[anahtar].ozel) {
      ham[anahtar] = donemliKayit(anahtar, ham[anahtar], zaman);
    }
  }
  return ham;
};

export const tabanAd = (model) => (model.endsWith(UZUN_EKI) ? model.slice(0, -UZUN_EKI.length) : model);

const surumNo = (ad) => (ad.match(/\d+/g) || []).map(Number);
const surumKarsilastir = (a, b) => {
  const x = surumNo(a), y = surumNo(b);
  for (let i = 0; i < Math.max(x.length, y.length); i++) if ((x[i] || 0) !== (y[i] || 0)) return (x[i] || 0) - (y[i] || 0);
  return 0;
};

// Model adına göre eşleşen katalog anahtarı ve fiyat kaydı: tam ad, alt model (agy-/openai-), seviye/tarih eki atılmış ad, aile yedeği
export function eslesenModelBul(liste, model, zaman) {
  const l = liste || YERLESIK;
  const ad = String(tabanAd(String(model || ""))).toLowerCase();
  const don = (anahtar) => ({ anahtar, kayit: donemliKayit(anahtar, l[anahtar] || YERLESIK[anahtar], zaman) });

  if (ad === "agy-gemini" && !(l[ad] && l[ad].ozel)) return eslesenModelBul(l, "gemini-4-argon", zaman);
  if (Object.hasOwn(ESKI_MODEL_ESLEME, ad)) return eslesenModelBul(l, ESKI_MODEL_ESLEME[ad], zaman);
  if (l[ad]) return don(ad);

  if (ad.startsWith(AGY_EKI)) {
    const alt = ad.slice(AGY_EKI.length);
    if (
      Object.hasOwn(ESKI_MODEL_ESLEME, alt) ||
      alt.startsWith("claude-") ||
      alt.startsWith("gemini-") ||
      alt.startsWith("gpt-") ||
      alt.startsWith("o1") ||
      alt.startsWith("o3") ||
      alt.startsWith("o4") ||
      alt.startsWith("codex-")
    ) {
      return eslesenModelBul(l, alt, zaman);
    }
    return don(l["gemini-4-argon"] || YERLESIK["gemini-4-argon"] ? "gemini-4-argon" : "gemini-3.1-pro");
  }

  if (ad.startsWith("openai-")) {
    const alt = ad.slice("openai-".length);
    if (alt) return eslesenModelBul(l, alt, zaman);
  }

  const EkiAtilmis = ad.replace(SEVIYE_EKI_RE, "");
  if (EkiAtilmis && EkiAtilmis !== ad) return eslesenModelBul(l, EkiAtilmis, zaman);

  let s = ad.replace(/-\d{8}$/, "").replace(/-\d{4}-\d{2}-\d{2}$/, "").replace(SEVIYE_EKI_RE, "");
  if (Object.hasOwn(ESKI_MODEL_ESLEME, s)) return eslesenModelBul(l, ESKI_MODEL_ESLEME[s], zaman);
  if (l[s]) return don(s);
  // Claude'da noktalı ya da tireli sürüm farkını normalize et (ör. claude-opus-5.5 -> claude-opus-5-5)
  if (s.startsWith("claude-") && s.includes(".")) {
    const tireli = s.replace(/\./g, "-");
    if (l[tireli]) return don(tireli);
  }
  while (s.includes("-")) {
    if (l[s]) return don(s);
    s = s.slice(0, s.lastIndexOf("-"));
  }

  const sgl = saglayiciOf(ad);
  if (sgl === "gemini") {
    if (ad.includes("flash-lite")) return don("gemini-3.1-flash-lite");
    if (ad.includes("flash")) return don("gemini-3.8-flash");
    if (ad.includes("carbon")) return don("gemini-4-carbon");
    if (ad.includes("argon") || ad.includes("gemini-4")) return don("gemini-4-argon");
    return don("gemini-3.1-pro");
  }
  if (sgl === "chatgpt") {
    if (ad.includes("gpt-oss")) return don("gpt-oss-120b");
    if (ad.includes("nano")) return don("gpt-5.4-nano");
    if (ad.includes("o4-mini")) return don("o4-mini");
    if (ad.includes("o3-pro")) return don("o3-pro");
    if (ad.includes("o3-mini")) return don("o3-mini");
    if (ad.includes("o3")) return don("o3");
    if (ad.includes("o1-pro")) return don("o1-pro");
    if (ad.includes("o1")) return don("o1");
    if (ad.includes("codex-mini")) return don("gpt-5.1-codex-mini");
    if (ad.includes("codex")) return don("gpt-5.3-codex");
    if (ad.includes("mini")) return don("gpt-5.4-mini");
    if (ad.includes("luna")) return don("gpt-6-luna");
    if (ad.includes("terra")) return don("gpt-6.1-terra");
    if (ad.includes("pro")) return don("gpt-5.4-pro");
    if (ad.startsWith("gpt-6")) return don("gpt-6");
    return don("gpt-5.4");
  }
  for (const a of AILELER) {
    if (!ad.includes(a)) continue;
    const aday = Object.keys(l).filter((k) => k.startsWith("claude-") && k.includes(a)).sort(surumKarsilastir).pop();
    if (aday) return don(aday);
  }
  return don("claude-opus-5-5");
}

export function kayitBul(liste, model, zaman) {
  return eslesenModelBul(liste, model, zaman).kayit;
}

// Satırın fiyat dizisi: "@uzun" satırları (varsa) yüksek fiyattan
export function fiyatBul(liste, model, zaman) {
  const k = kayitBul(liste, model, zaman);
  return String(model || "").endsWith(UZUN_EKI) && k.u ? k.u.f : k.b;
}

// İstemciye gönderilen eşikler: {"claude-haiku-5-5": 100000, "gemini-3.1-pro": 200000, "gpt-5.4": 272000, ...}
export function esikler(liste, zaman) {
  const e = {};
  for (const [ad, hamK] of Object.entries(liste || {})) {
    const k = donemliKayit(ad, hamK, zaman);
    if (k && k.u) e[ad] = k.u.esik;
  }
  return e;
}
