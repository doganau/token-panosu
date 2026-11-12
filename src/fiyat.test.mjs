import test from "node:test";
import assert from "node:assert/strict";
import {
  YERLESIK,
  DONEMLI,
  UZUN_EKI,
  saglayiciOf,
  gemini3pModelMi,
  resmiAyristir,
  geminiResmiAyristir,
  litellmAyristir,
  birlestir,
  eslesenModelBul,
  kayitBul,
  fiyatBul,
  esikler,
} from "./fiyat.mjs";

test("saglayiciOf tüm sağlayıcıları ve agy- önekini doğru ayırır", () => {
  assert.equal(saglayiciOf("claude-opus-5-5"), "claude");
  assert.equal(saglayiciOf("gpt-6.1-sol"), "chatgpt");
  assert.equal(saglayiciOf("o4-mini"), "chatgpt");
  assert.equal(saglayiciOf("codex-mini"), "chatgpt");
  assert.equal(saglayiciOf("gemini-3.8-flash"), "gemini");
  assert.equal(saglayiciOf("agy-claude-opus-5-5"), "gemini");
  assert.equal(saglayiciOf("agy-gemini-4-argon"), "gemini");
  assert.equal(saglayiciOf("agy-gpt-oss-120b"), "gemini");
});

test("resmiAyristir Anthropic tablo ve >100K uzun bağlam satırlarını okur", () => {
  const md = `
## Model pricing

| Model | Base input | 5m cache writes | 1h cache writes | Cache hits | Output |
| --- | --- | --- | --- | --- | --- |
| Claude Opus 5.5 | $4 / MTok | $5 / MTok | $8 / MTok | $0.20 / MTok | $20 / MTok |
| Claude Haiku 5.5 | $0.10 / MTok | $0.125 / MTok | $0.20 / MTok | $0.01 / MTok | $0.50 / MTok |
| Claude Haiku 5.5 (for prompts over 100,000 tokens) | $0.50 / MTok | $0.625 / MTok | $1 / MTok | $0.05 / MTok | $2.50 / MTok |
`;
  const r = resmiAyristir(md);
  assert.deepEqual(r["claude-opus-5-5"], { b: [4, 20, 5, 8, 0.2] });
  assert.deepEqual(r["claude-haiku-5-5"], {
    b: [0.1, 0.5, 0.125, 0.2, 0.01],
    u: { esik: 100000, f: [0.5, 2.5, 0.625, 1, 0.05] },
  });
});

test("geminiResmiAyristir Standard tablosunu, >200k dilimini, tarih geçişini ve uyarı kutusu yalıtımını işler", () => {
  const md = `
# Gemini Developer API Pricing

## Gemini 4 Argon {% id="gemini-4-argon" %}

{% columns %}
{% left-column %}
Model code: \`gemini-4-argon\`
{% /left-column %}
{% right-column %}
{% tabs %}
{% tab title="Standard" %}
{% table %}
* Feature
* Free Tier
* Paid Tier, per 1M tokens in USD
---
* Input price
* Not available
* $2.00 through November 30, 2026. $4.00 starting December 1, 2026.
---
* Output price (including thinking tokens)
* Not available
* $10.00 through November 30, 2026. $20.00 starting December 1, 2026.
---
* Context caching price
* Not available
* $0.10 through November 30, 2026. $0.20 starting December 1, 2026.
  $1.00 / 1,000,000 tokens per hour (storage price)
{% /table %}
{% /tab %}
{% /tabs %}
{% /right-column %}
{% /columns %}

## Gemini 3.1 Pro Preview {% id="gemini-3.1-pro-preview" %}

{% columns %}
{% left-column %}
Model code: \`gemini-3.1-pro-preview\`, \`gemini-3.1-pro-preview-customtools\`
{% /left-column %}
{% right-column %}
{% tabs %}
{% tab title="Standard" %}
{% table %}
* Feature
* Free Tier
* Paid Tier
---
* Input price
* Not available
* $2.00, prompts <= 200k tokens
  $4.00, prompts > 200k tokens
---
* Output price
* Not available
* $12.00, prompts <= 200k tokens
  $18.00, prompts > 200k tokens
---
* Context caching price
* Not available
* $0.20, prompts <= 200k tokens
  $0.40, prompts > 200k tokens
  $4.50 / 1,000,000 tokens per hour (storage price)
{% /table %}
{% /tab %}
{% /tabs %}
{% /right-column %}
{% /columns %}

## Gemini 2.5 Pro {% id="gemini-2.5-pro" %}

Note: Migrate to \`gemini-3.8-flash\` before retirement.

{% columns %}
{% left-column %}
Model code: \`gemini-2.5-pro\`
{% /left-column %}
{% right-column %}
{% tabs %}
{% tab title="Standard" %}
{% table %}
* Feature
* Free Tier
* Paid Tier
---
* Input price
* Free of charge
* $1.25, prompts <= 200k tokens
  $2.50, prompts > 200k tokens
---
* Output price
* Free of charge
* $10.00, prompts <= 200k tokens
  $15.00, prompts > 200k tokens
---
* Context caching price
* Not available
* $0.125, prompts <= 200k tokens
  $0.25, prompts > 200k tokens
{% /table %}
{% /tab %}
{% /tabs %}
{% /right-column %}
{% /columns %}
`;

  const kasim = geminiResmiAyristir(md, Date.parse("2026-11-11T12:00:00Z"));
  assert.deepEqual(kasim["gemini-4-argon"], { b: [2, 10, 2, 2, 0.1] });
  assert.deepEqual(kasim["gemini-3.1-pro-preview"], {
    b: [2, 12, 2, 2, 0.2],
    u: { esik: 200000, f: [4, 18, 4, 4, 0.4] },
  });
  assert.deepEqual(kasim["gemini-3.1-pro"], kasim["gemini-3.1-pro-preview"]);
  assert.equal(kasim["gemini-3.8-flash"], undefined, "uyarı metnindeki göç önerisi başka modele sızmamalı");

  const aralik = geminiResmiAyristir(md, Date.parse("2026-12-05T12:00:00Z"));
  assert.deepEqual(aralik["gemini-4-argon"], { b: [4, 20, 4, 4, 0.2] });
});

test("litellmAyristir Claude, OpenAI ve Gemini modellerini ve uzun bağlam katmanlarını okur", () => {
  const ham = {
    "claude-sonnet-5-5": {
      litellm_provider: "anthropic",
      mode: "chat",
      input_cost_per_token: 0.000002,
      output_cost_per_token: 0.00001,
      cache_creation_input_token_cost: 0.0000025,
      cache_creation_input_token_cost_above_1hr: 0.000004,
      cache_read_input_token_cost: 0.0000001,
    },
    "gpt-6.1-sol": {
      litellm_provider: "openai",
      mode: "chat",
      input_cost_per_token: 0.000002,
      output_cost_per_token: 0.00001,
      cache_creation_input_token_cost_above_12hr: 0.000005,
      cache_read_input_token_cost: 0.0000001,
      input_cost_per_token_above_272k_tokens: 0.000004,
      output_cost_per_token_above_272k_tokens: 0.000015,
      cache_read_input_token_cost_above_272k_tokens: 0.0000002,
    },
    "gemini/gemini-3.8-flash": {
      litellm_provider: "gemini",
      mode: "chat",
      input_cost_per_token: 0.00000075,
      output_cost_per_token: 0.00000375,
      cache_read_input_token_cost: 0.000000075,
    },
    "gpt-4o-realtime-preview": {
      litellm_provider: "openai",
      mode: "chat",
      input_cost_per_token: 0.000005,
      output_cost_per_token: 0.00002,
    },
  };
  const r = litellmAyristir(ham);
  assert.deepEqual(r["claude-sonnet-5-5"], { b: [2, 10, 2.5, 4, 0.1] });
  assert.deepEqual(r["gpt-6.1-sol"], {
    b: [2, 10, 2, 2, 0.1],
    u: { esik: 272000, f: [4, 15, 4, 4, 0.2] },
  });
  assert.deepEqual(r["gemini-3.8-flash"], { b: [0.75, 3.75, 0.75, 0.75, 0.075] });
  assert.equal(r["gpt-4o-realtime-preview"], undefined);
});

test("eslesenModelBul, fiyatBul ve esikler agy-*, openai-*, tarihli ad ve @uzun satırlarını doğru çözer", () => {
  const liste = birlestir({});
  assert.equal(eslesenModelBul(liste, "agy-claude-opus-5-5").anahtar, "claude-opus-5-5");
  assert.deepEqual(fiyatBul(liste, "agy-claude-opus-5-5"), [4, 20, 5, 8, 0.2]);

  assert.equal(eslesenModelBul(liste, "agy-gemini-3.1-pro").anahtar, "gemini-3.1-pro");
  assert.deepEqual(fiyatBul(liste, "agy-gemini-3.1-pro"), [2, 12, 2, 2, 0.2]);
  assert.deepEqual(fiyatBul(liste, "agy-gemini-3.1-pro" + UZUN_EKI), [4, 18, 4, 4, 0.4]);

  assert.equal(eslesenModelBul(liste, "gpt-6.1-sol-2026-11-01").anahtar, "gpt-6.1-sol");
  assert.deepEqual(fiyatBul(liste, "gpt-6.1-sol-2026-11-01" + UZUN_EKI), [4, 15, 4, 4, 0.2]);

  assert.equal(eslesenModelBul(liste, "openai-gpt-5.3-codex").anahtar, "gpt-5.3-codex");
  assert.deepEqual(fiyatBul(liste, "agy-gpt-oss-120b"), [0.15, 0.6, 0.15, 0.15, 0.015]);
  assert.deepEqual(fiyatBul(liste, "agy-gpt-oss-20b"), [0.075, 0.3, 0.075, 0.075, 0.0075]);
  assert.equal(eslesenModelBul(liste, "claude-opus-5.5").anahtar, "claude-opus-5-5");
  assert.equal(kayitBul(liste, "claude-haiku-5-5-20261001").u.esik, 100000);

  const e = esikler(YERLESIK, "2026-11-15T00:00:00Z");
  assert.equal(e["claude-haiku-5-5"], 100000);
  assert.equal(e["gemini-3.1-pro"], 200000);
  assert.equal(e["gpt-6.1-sol"], 272000);
});

test("gemini3pModelMi Antigravity içindeki Claude ve GPT modellerini Gemini modellerinden kesin ayırır", () => {
  assert.equal(gemini3pModelMi("agy-claude-opus-5-5"), true);
  assert.equal(gemini3pModelMi("agy-claude-sonnet-5-5@uzun"), true);
  assert.equal(gemini3pModelMi("agy-gpt-oss-120b"), true);
  assert.equal(gemini3pModelMi("agy-gpt-5.4"), true);
  assert.equal(gemini3pModelMi("agy-o3"), true);
  assert.equal(gemini3pModelMi("agy-codex-mini"), true);

  assert.equal(gemini3pModelMi("agy-gemini-4-carbon"), false);
  assert.equal(gemini3pModelMi("agy-gemini-4-argon"), false);
  assert.equal(gemini3pModelMi("agy-gemini-3.8-flash"), false);
  assert.equal(gemini3pModelMi("gemini-3.1-pro"), false);
  assert.equal(gemini3pModelMi("claude-opus-5-5"), false);
  assert.equal(gemini3pModelMi("gpt-6.1-sol"), false);
});

test("resmî Gemini & Carbon/Argon fiyatları, seviye ekleri, eski takma adlar ve 2026/2027 DONEMLI geçişleri", () => {
  const kasim = "2026-11-30T23:59:59Z";
  const aralik = "2026-12-01T00:00:00Z";
  const ocak2027 = "2027-01-01T00:00:00Z";
  const listeKasim = birlestir({ "agy-gemini": { b: [2, 10, 2, 2, 0.1] } }, kasim);
  const listeAralik = birlestir({ "agy-gemini": { b: [2, 10, 2, 2, 0.1] } }, aralik);
  const liste2027 = birlestir({}, ocak2027);

  // YERLESIK içinde agy-gemini sabit tuzağı bulunmamalı; eski agy-gemini satırları gemini-4-argon dönemli kaydına yönlenmeli
  assert.equal(YERLESIK["agy-gemini"], undefined);
  assert.ok(DONEMLI["gemini-4-carbon"]);
  assert.ok(DONEMLI["gemini-4-argon"]);
  assert.ok(DONEMLI["gemini-3.8-flash"]);

  // Kasım 2026 (2026-12-01T00:00:00Z öncesi)
  assert.equal(eslesenModelBul(listeKasim, "agy-gemini", kasim).anahtar, "gemini-4-argon");
  assert.deepEqual(fiyatBul(listeKasim, "agy-gemini", kasim), [2, 10, 2, 2, 0.1]);
  assert.deepEqual(fiyatBul(listeKasim, "agy-gemini-4-carbon", kasim), [2, 10, 2, 2, 0.1]);
  assert.deepEqual(fiyatBul(listeKasim, "agy-gemini-4-argon", kasim), [2, 10, 2, 2, 0.1]);

  // 1 Aralık 2026 ve sonrası (düz $4 / $20 / $0.20, sahte >200k katmanı yok)
  assert.equal(eslesenModelBul(listeAralik, "agy-gemini", aralik).anahtar, "gemini-4-argon");
  assert.deepEqual(fiyatBul(listeAralik, "agy-gemini", aralik), [4, 20, 4, 4, 0.2]);
  assert.deepEqual(fiyatBul(listeAralik, "agy-gemini-4-carbon", aralik), [4, 20, 4, 4, 0.2]);
  assert.deepEqual(fiyatBul(listeAralik, "agy-gemini-4-argon", aralik), [4, 20, 4, 4, 0.2]);
  assert.equal(esikler(listeAralik, aralik)["gemini-4-carbon"], undefined);
  assert.equal(esikler(listeAralik, aralik)["gemini-4-argon"], undefined);

  // Resmî Gemini Flash 3.x fiyatları (2026: $0.75 / $3.75 / $0.075 -> 2027: $1.50 / $7.50 / $0.15) ve 3.5 Flash ($1.50 / $9 / $0.15)
  assert.equal(eslesenModelBul(listeKasim, "agy-gemini-3.8-flash", kasim).anahtar, "gemini-3.8-flash");
  assert.deepEqual(fiyatBul(listeKasim, "agy-gemini-3.8-flash", kasim), [0.75, 3.75, 0.75, 0.75, 0.075]);
  assert.equal(eslesenModelBul(listeKasim, "agy-gemini-3.8-flash-tiered", kasim).anahtar, "gemini-3.8-flash");
  assert.deepEqual(fiyatBul(listeKasim, "agy-gemini-3.8-flash-tiered", kasim), [0.75, 3.75, 0.75, 0.75, 0.075]);
  assert.deepEqual(fiyatBul(liste2027, "agy-gemini-3.8-flash", ocak2027), [1.5, 7.5, 1.5, 1.5, 0.15]);
  assert.deepEqual(fiyatBul(liste2027, "gemini-3.7-flash", ocak2027), [1.5, 7.5, 1.5, 1.5, 0.15]);
  assert.equal(eslesenModelBul(listeKasim, "agy-gemini-3.5-flash", kasim).anahtar, "gemini-3.5-flash");
  assert.deepEqual(fiyatBul(listeKasim, "agy-gemini-3.5-flash", kasim), [1.5, 9, 1.5, 1.5, 0.15]);
  assert.deepEqual(fiyatBul(listeKasim, "agy-gemini-3.5-flash-lite", kasim), [0.3, 2.5, 0.3, 0.3, 0.03]);
  assert.equal(eslesenModelBul(listeKasim, "agy-claude-opus-5-5", kasim).anahtar, "claude-opus-5-5");
  assert.equal(eslesenModelBul(listeKasim, "agy-claude-opus-5-5-high", kasim).anahtar, "claude-opus-5-5");
  assert.equal(eslesenModelBul(listeKasim, "agy-claude-sonnet-5-5-thinking", kasim).anahtar, "claude-sonnet-5-5");

  // Eski dahili model takma adları
  assert.equal(eslesenModelBul(listeKasim, "gemini-3-flash-a", kasim).anahtar, "gemini-3.5-flash");
  assert.equal(eslesenModelBul(listeKasim, "gemini-3-flash-b", kasim).anahtar, "gemini-3.5-flash");
  assert.equal(eslesenModelBul(listeKasim, "gemini-default", kasim).anahtar, "gemini-3.5-flash");
  assert.equal(eslesenModelBul(listeKasim, "agy-gemini-default", kasim).anahtar, "gemini-3.5-flash");
  assert.equal(eslesenModelBul(listeKasim, "gemini-pro-a", kasim).anahtar, "gemini-3.1-pro");
  assert.equal(eslesenModelBul(listeKasim, "gemini-pro-b", kasim).anahtar, "gemini-3.1-pro");
  assert.equal(eslesenModelBul(listeKasim, "agy-gemini-pro-a", kasim).anahtar, "gemini-3.1-pro");

  // Diğer tüm resmî Gemini fiyatları
  assert.deepEqual(YERLESIK["gemini-3.1-pro"], { b: [2, 12, 2, 2, 0.2], u: { esik: 200000, f: [4, 18, 4, 4, 0.4] } });
  assert.deepEqual(YERLESIK["gemini-3.1-flash-lite"], { b: [0.25, 1.5, 0.25, 0.25, 0.025] });
  assert.deepEqual(YERLESIK["gemini-3-pro"], { b: [2, 12, 2, 2, 0.2], u: { esik: 200000, f: [4, 18, 4, 4, 0.4] } });
  assert.deepEqual(YERLESIK["gemini-3-flash"], { b: [0.5, 3, 0.5, 0.5, 0.05] });
  assert.deepEqual(YERLESIK["gemini-2.5-pro"], { b: [1.25, 10, 1.25, 1.25, 0.125], u: { esik: 200000, f: [2.5, 15, 2.5, 2.5, 0.25] } });
  assert.deepEqual(YERLESIK["gemini-2.5-flash"], { b: [0.3, 2.5, 0.3, 0.3, 0.03] });
  assert.deepEqual(YERLESIK["gemini-2.5-flash-lite"], { b: [0.1, 0.4, 0.1, 0.1, 0.01] });

  // OpenAI LiteLLM önbellek (cw / cr) eşitlemesi
  assert.equal(YERLESIK["gpt-5-pro"].b[4], 3.75);
  assert.equal(YERLESIK["gpt-5.2-pro"].b[4], 5.25);
  assert.equal(YERLESIK["gpt-5.4-pro"].b[4], 7.5);
  assert.equal(YERLESIK["gpt-5.6-pro"].b[4], 7.5);
  assert.equal(YERLESIK["gpt-5.6-pro"].b[2], 46.875);
  assert.equal(YERLESIK["gpt-6-pro"].b[4], 15);
  assert.equal(YERLESIK["gpt-6-pro"].b[2], 93.75);
  assert.equal(YERLESIK["gpt-5.6"].b[2], 4.6875);
  assert.equal(YERLESIK["gpt-5.6-mini"].b[2], 1.1875);
  assert.equal(YERLESIK["gpt-6"].b[2], 9.375);
  assert.equal(YERLESIK["gpt-6-mini"].b[2], 1.875);
});
