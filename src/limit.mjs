// Limit okumalarının saf (veritabanından bağımsız) mantığı. Hem Worker hem node testi kullanır.
//
// Kural özeti
//  - Haftalık yüzde bir pencere içinde düşmez; pencere = aynı seven_day.resets_at (en yakın saate yuvarlanır).
//    Etkin değer = pencerenin okumaları arasındaki EN BÜYÜK w_pct (t >= taban olanlar).
//    Boşta kalmış bir oturum eski yüzdesini yeni zaman damgasıyla yeniden gönderebilir; MAX bunu etkisiz kılar.
//  - Elle "Fable sıfırlaması" gerçekten düşürür: taban (limit_taban_t) öncesi okumalar yok sayılır.
//  - 5 saatlik değer aynı fikir: aynı h_reset'te en büyük h_pct, yalnızca h_reset > şimdi ise.

export const SAAT = 3600;
export const DEDUPE_SN = 1800; // değişmeyen değer en geç 30 dakikada bir yeniden kaydedilir

// Pencere anahtarı: reset zamanı en yakın saate yuvarlanır (küçük sapmaları yutar)
export const pencereAnahtari = (reset) => Math.round(reset / SAAT);

// Ham limit okumasından (istemci "l.r") geçmiş satırı üretir. haftaSonu: okumanın düştüğü haftanın bitişi.
export function satirOf(lim, haftaSonu) {
  const r = lim.r;
  const sayi = (k) => (Number.isFinite(r[k]) ? r[k] : null);
  const w_pct = sayi("seven_day.used_percentage");
  let w_reset = sayi("seven_day.resets_at");
  if (w_pct !== null && w_reset === null) w_reset = haftaSonu; // reset bilinmiyorsa pencere = o haftanın bitişi
  const h_pct = sayi("five_hour.used_percentage");
  const h_reset = h_pct !== null ? sayi("five_hour.resets_at") : null;
  return { t: lim.t, w_pct, w_reset, h_pct, h_reset };
}

// Aynı cihazın son satırıyla karşılaştırır: değer değiştiyse ya da 30 dk geçtiyse yeni satır yazılır.
export function yeniSatirMi(son, s) {
  if (!son) return true;
  if (s.t <= son.t) return false; // eski ya da aynı zamanlı okuma
  if (s.w_pct !== son.w_pct || s.w_reset !== son.w_reset || s.h_pct !== son.h_pct || s.h_reset !== son.h_reset) return true;
  return s.t - son.t >= DEDUPE_SN;
}

// Verilen satırlardan (bir haftanın) etkin haftalık değeri üretir.
// rows: [{t, w_pct, w_reset}], taban: bu t'den önceki okumalar yok sayılır.
// Dönüş: {pct, set_at, wr, seri} ya da null. seri = en güncel penceredeki yüzdenin yükseldiği anlar (monoton basamak).
export function etkinHafta(rows, taban = 0) {
  const ok = rows.filter((r) => Number.isFinite(r.w_pct) && Number.isFinite(r.w_reset) && r.t >= (taban || 0)).sort((a, b) => a.t - b.t);
  if (!ok.length) return null;
  const son = Math.max(...ok.map((r) => pencereAnahtari(r.w_reset))); // en yeni pencere (eski pencereden gecikmiş okuma sayılmaz)
  const pencere = ok.filter((r) => pencereAnahtari(r.w_reset) === son);
  let max = -Infinity, set_at = 0;
  const seri = [];
  for (const r of pencere) {
    if (r.t > set_at) set_at = r.t;
    if (r.w_pct > max) { max = r.w_pct; seri.push({ t: r.t, pct: r.w_pct }); }
  }
  return { pct: max, set_at, wr: son * SAAT, seri };
}

// Şu anki 5 saatlik pencere: h_reset > now olanlar arasında en yeni pencerenin en büyük h_pct'si.
export function etkin5(rows, now) {
  const ok = rows.filter((r) => Number.isFinite(r.h_pct) && Number.isFinite(r.h_reset) && r.h_reset > now);
  if (!ok.length) return null;
  const son = Math.max(...ok.map((r) => pencereAnahtari(r.h_reset)));
  const pencere = ok.filter((r) => pencereAnahtari(r.h_reset) === son);
  return { pct: Math.max(...pencere.map((r) => r.h_pct)), reset: Math.max(...pencere.map((r) => r.h_reset)) };
}
