-- Güncel tam şema (uzaktaki D1'den alındı). Yeni bir veritabanı için yalnızca bu dosya yeterli.
CREATE TABLE IF NOT EXISTS devices (
  id TEXT PRIMARY KEY,
  label TEXT NOT NULL,
  secret TEXT NOT NULL,
  created_at INTEGER NOT NULL,
  last_ts INTEGER NOT NULL DEFAULT 0,
  last_seen INTEGER NOT NULL DEFAULT 0,
  client_ver INTEGER NOT NULL DEFAULT 0,
  flags TEXT NOT NULL DEFAULT '[]',
  srv_flags TEXT NOT NULL DEFAULT '[]',
  os TEXT NOT NULL DEFAULT '',
  limit_sorgu INTEGER NOT NULL DEFAULT 0,
  uye_id TEXT NOT NULL DEFAULT '',
  bildirim_son INTEGER NOT NULL DEFAULT 0,
  hesaplar TEXT NOT NULL DEFAULT '{}',
  hesap_eposta TEXT NOT NULL DEFAULT '{}'
);
CREATE TABLE IF NOT EXISTS haric (
  device_id TEXT NOT NULL,
  sid TEXT NOT NULL,
  added_at INTEGER NOT NULL,
  note TEXT NOT NULL DEFAULT '',
  PRIMARY KEY (device_id, sid)
);
CREATE TABLE IF NOT EXISTS limit_log (week INTEGER PRIMARY KEY, pct REAL NOT NULL, set_at INTEGER NOT NULL);
CREATE TABLE IF NOT EXISTS limit_okuma (
  id INTEGER PRIMARY KEY CHECK (id = 1),
  okundu_at INTEGER NOT NULL,
  device_id TEXT NOT NULL,
  ham TEXT NOT NULL
);
CREATE TABLE IF NOT EXISTS hafta_ayar (
  week INTEGER PRIMARY KEY,
  fable_ayri INTEGER NOT NULL DEFAULT 0,
  limit_taban_t INTEGER NOT NULL DEFAULT 0
);
-- Limit okuma geçmişi: etkin haftalık değer = aynı w_reset penceresindeki en büyük w_pct (t >= limit_taban_t)
CREATE TABLE IF NOT EXISTS limit_gecmis (
  device_id TEXT NOT NULL,
  t INTEGER NOT NULL,
  w_pct REAL,
  w_reset INTEGER,
  h_pct REAL,
  h_reset INTEGER,
  ham TEXT,
  PRIMARY KEY (device_id, t)
);
CREATE INDEX IF NOT EXISTS limit_gecmis_t ON limit_gecmis(t);
CREATE TABLE IF NOT EXISTS login_fail (
  ip TEXT PRIMARY KEY,
  count INTEGER NOT NULL,
  window_start INTEGER NOT NULL
);
CREATE TABLE IF NOT EXISTS pair_codes (
  code TEXT PRIMARY KEY,
  label TEXT NOT NULL,
  expires_at INTEGER NOT NULL,
  uye_id TEXT NOT NULL DEFAULT ''
);
CREATE TABLE IF NOT EXISTS "usage_5m" (
  device_id TEXT NOT NULL,
  bucket INTEGER NOT NULL,
  model TEXT NOT NULL,
  sid TEXT NOT NULL DEFAULT '',
  input INTEGER NOT NULL DEFAULT 0,
  output INTEGER NOT NULL DEFAULT 0,
  cw5m INTEGER NOT NULL DEFAULT 0,
  cw1h INTEGER NOT NULL DEFAULT 0,
  cr INTEGER NOT NULL DEFAULT 0,
  messages INTEGER NOT NULL DEFAULT 0,
  PRIMARY KEY (device_id, bucket, model, sid)
);
CREATE TABLE IF NOT EXISTS "usage_h" (
  device_id TEXT NOT NULL,
  hour INTEGER NOT NULL,
  model TEXT NOT NULL,
  sid TEXT NOT NULL DEFAULT '',
  input INTEGER NOT NULL DEFAULT 0,
  output INTEGER NOT NULL DEFAULT 0,
  cw5m INTEGER NOT NULL DEFAULT 0,
  cw1h INTEGER NOT NULL DEFAULT 0,
  cr INTEGER NOT NULL DEFAULT 0,
  messages INTEGER NOT NULL DEFAULT 0,
  updated_at INTEGER NOT NULL DEFAULT 0,
  PRIMARY KEY (device_id, hour, model, sid)
);
CREATE INDEX IF NOT EXISTS usage_5m_bucket ON usage_5m(bucket);
CREATE INDEX IF NOT EXISTS usage_h_hour ON usage_h(hour);
CREATE VIEW IF NOT EXISTS usage5_v AS
  SELECT u.* FROM usage_5m u
  WHERE NOT EXISTS (SELECT 1 FROM haric x WHERE x.device_id = u.device_id AND x.sid = u.sid);
CREATE VIEW IF NOT EXISTS usage_v AS
  SELECT u.* FROM usage_h u
  WHERE NOT EXISTS (SELECT 1 FROM haric x WHERE x.device_id = u.device_id AND x.sid = u.sid);
CREATE TABLE IF NOT EXISTS uyeler (
  id TEXT PRIMARY KEY,
  ad TEXT NOT NULL UNIQUE,
  created_at INTEGER NOT NULL
);
CREATE TABLE IF NOT EXISTS bildirimler (
  id INTEGER PRIMARY KEY AUTOINCREMENT,
  baslik TEXT NOT NULL,
  metin TEXT NOT NULL,
  created_at INTEGER NOT NULL,
  bitis INTEGER NOT NULL
);
CREATE TABLE IF NOT EXISTS fiyat_listesi (
  id INTEGER PRIMARY KEY CHECK (id = 1),
  veri TEXT NOT NULL,
  guncellendi INTEGER NOT NULL DEFAULT 0,
  denendi INTEGER NOT NULL DEFAULT 0,
  kaynak TEXT NOT NULL DEFAULT '',
  hata TEXT NOT NULL DEFAULT ''
);
CREATE TABLE IF NOT EXISTS hesap_kilit (
  saglayici TEXT PRIMARY KEY,
  hesap_ozet TEXT NOT NULL DEFAULT '',
  etiket TEXT NOT NULL DEFAULT '',
  kilitlendi_at INTEGER NOT NULL,
  cihaz_id TEXT NOT NULL DEFAULT '',
  eposta TEXT NOT NULL DEFAULT ''
);
CREATE TABLE IF NOT EXISTS saglayici_limit_gecmis (
  saglayici TEXT NOT NULL,
  device_id TEXT NOT NULL,
  t INTEGER NOT NULL,
  w_pct REAL,
  w_reset INTEGER,
  h_pct REAL,
  h_reset INTEGER,
  ham TEXT,
  PRIMARY KEY (saglayici, device_id, t)
);
CREATE INDEX IF NOT EXISTS saglayici_limit_gecmis_t ON saglayici_limit_gecmis(saglayici, t);
CREATE TABLE IF NOT EXISTS saglayici_limit_log (
  saglayici TEXT NOT NULL,
  week INTEGER NOT NULL,
  pct REAL NOT NULL,
  set_at INTEGER NOT NULL,
  PRIMARY KEY (saglayici, week)
);
CREATE TABLE IF NOT EXISTS sistem_ayar (
  id INTEGER PRIMARY KEY CHECK (id = 1),
  veri TEXT NOT NULL,
  guncellendi INTEGER NOT NULL DEFAULT 0
);
-- Kullanıcı hesapları, ofis rolleri ve tek kullanımlık şifre belirleme / sıfırlama bağlantıları
CREATE TABLE IF NOT EXISTS kullanicilar (
  id TEXT PRIMARY KEY,
  kullanici_adi TEXT NOT NULL UNIQUE,
  ad TEXT NOT NULL DEFAULT '',
  rol TEXT NOT NULL DEFAULT 'uye',
  sifre TEXT NOT NULL DEFAULT '',
  uye_id TEXT NOT NULL DEFAULT '',
  durum TEXT NOT NULL DEFAULT 'davetli',
  oturum_surum INTEGER NOT NULL DEFAULT 1,
  olusturuldu INTEGER NOT NULL,
  son_giris INTEGER NOT NULL DEFAULT 0
);
CREATE TABLE IF NOT EXISTS kullanici_ofis (
  kullanici_id TEXT NOT NULL,
  saglayici TEXT NOT NULL,
  rol TEXT NOT NULL DEFAULT 'uye',
  PRIMARY KEY (kullanici_id, saglayici)
);
CREATE TABLE IF NOT EXISTS hesap_token (
  ozet TEXT PRIMARY KEY,
  kullanici_id TEXT NOT NULL,
  tur TEXT NOT NULL,
  son INTEGER NOT NULL,
  olusturuldu INTEGER NOT NULL
);
CREATE TABLE IF NOT EXISTS oto_hafta_sifirlama (
  saglayici TEXT PRIMARY KEY,
  reset_at INTEGER NOT NULL,
  okundu_at INTEGER NOT NULL,
  cihaz_id TEXT NOT NULL DEFAULT ''
);
