import test from "node:test";
import assert from "node:assert/strict";
import {
  YERLESIK,
  UZUN_EKI,
  saglayiciOf,
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
  assert.deepEqual(fiyatBul(liste, "agy-gpt-oss-120b"), [0.15, 0.6, 0.15, 0.15, 0.075]);
  assert.equal(eslesenModelBul(liste, "claude-opus-5.5").anahtar, "claude-opus-5-5");
  assert.equal(kayitBul(liste, "claude-haiku-5-5-20261001").u.esik, 100000);

  const e = esikler(YERLESIK);
  assert.equal(e["claude-haiku-5-5"], 100000);
  assert.equal(e["gemini-3.1-pro"], 200000);
  assert.equal(e["gpt-6.1-sol"], 272000);
});
