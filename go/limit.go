package main

// Limit okuması yalnızca bu bilgisayardaki dosyalardan yapılır; hiçbir Anthropic adresine bağlanılmaz,
// hiçbir oturum anahtarı okunmaz:
//   - Yalnızca Claude masaüstü uygulamasının kendi yazdığı plan-usage-history.json (uygulama açıkken
//     5-15 dakikada bir kendisi günceller; haftalık ve varsa 5 saatlik yüzde). Masaüstü uygulaması
//     olmayan bilgisayar (ör. yalnızca CLI) limit göndermez; limit hesabı ortak olduğu için başka
//     bilgisayarın okuması yeter.

import (
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strconv"
	"strings"
)

const haftaSn int64 = 7 * 86400

func sonrakiHaftaReset(referans, simdi int64) int64 {
	if referans <= 1_600_000_000 {
		return 0
	}
	if simdi <= 0 {
		return referans
	}
	if referans <= simdi {
		k := (simdi-referans)/haftaSn + 1
		referans += k * haftaSn
	} else if referans > simdi+haftaSn {
		k := (referans - simdi - 1) / haftaSn
		referans -= k * haftaSn
	}
	return referans
}

// Eski sürümlerin (16 ve öncesi) limit sorgusu ve durum satırı dosyaları: bulunursa silinir
func eskiSorguTemizle() {
	for _, ad := range []string{"limit_sorgu", "son_sorgu", "limit_api.json", "limit.json"} {
		_ = os.Remove(filepath.Join(dizin, ad))
	}
}

func claudeMasaustuLimitYolu() string {
	if testModu {
		return filepath.Join(dizin, "plan-usage-history.json")
	}
	if runtime.GOOS == "windows" {
		if appdata := os.Getenv("APPDATA"); appdata != "" {
			return filepath.Join(appdata, "Claude", "plan-usage-history.json")
		}
	}
	if ev, err := os.UserHomeDir(); err == nil {
		if runtime.GOOS == "darwin" {
			return filepath.Join(ev, "Library", "Application Support", "Claude", "plan-usage-history.json")
		}
		return filepath.Join(ev, ".config", "Claude", "plan-usage-history.json")
	}
	return ""
}

type masaustuGecmis struct {
	Version int `json:"version"`
	Samples []struct {
		T   int64                  `json:"t"` // ms
		Org string                 `json:"org"`
		U   map[string]json.Number `json:"u"`
	} `json:"samples"`
}

type claudeSifirlamaAralik struct {
	basDk   int64
	bitDk   int64
	sifirMi bool
}

func claudeDakikaKesisim(araliklar []claudeSifirlamaAralik) (int64, bool) {
	const haftaDk = 7 * 1440
	if len(araliklar) == 0 {
		return 0, false
	}
	uyumlu := make([]bool, haftaDk)
	adet := 0
	for m := 0; m < haftaDk; m++ {
		tamam := true
		for _, a := range araliklar {
			genislik := a.bitDk - a.basDk
			fark := (int64(m) - (a.basDk % haftaDk) + haftaDk) % haftaDk
			if fark > genislik {
				tamam = false
				break
			}
		}
		if tamam {
			uyumlu[m] = true
			adet++
		}
	}
	// En fazla 6 saatlik (360 dk) daralmış bir pencere varsa güvenilir kabul edilir
	if adet == 0 || adet > 360 {
		return 0, false
	}
	bas := -1
	for m := 0; m < haftaDk; m++ {
		onceki := (m - 1 + haftaDk) % haftaDk
		if uyumlu[m] && !uyumlu[onceki] {
			bas = m
			break
		}
	}
	if bas < 0 {
		return 0, false
	}
	uzunluk := 0
	sonTamSaat := -1
	for i := 0; i < haftaDk; i++ {
		m := (bas + i) % haftaDk
		if !uyumlu[m] {
			break
		}
		uzunluk++
		if m%60 == 0 {
			sonTamSaat = m
		}
	}
	if uzunluk == 0 || uzunluk > 360 {
		return 0, false
	}
	secilen := (bas + uzunluk/2) % haftaDk
	if sonTamSaat >= 0 {
		secilen = sonTamSaat
	} else if araliklar[len(araliklar)-1].sifirMi && uzunluk <= 60 {
		secilen = (bas + uzunluk - 1) % haftaDk
	}
	return int64(secilen) * 60, true
}

func claudeGecmisSifirlamaBul(g masaustuGecmis, aktifOrg string, simdi int64) (int64, bool) {
	if len(g.Samples) == 0 {
		return 0, false
	}
	son := g.Samples[len(g.Samples)-1]
	for _, k := range []string{"sdr", "sd_reset", "seven_day.resets_at", "seven_day_resets_at"} {
		if v, var_ := son.U[k]; var_ {
			if n, err := v.Int64(); err == nil && n > 1_600_000_000 {
				if n > 1_000_000_000_000 {
					n /= 1000
				}
				if r := sonrakiHaftaReset(n, simdi); r > 0 {
					return r, true
				}
			}
		}
	}

	type ornek struct {
		sn int64
		sd float64
	}
	var ornekler []ornek
	for _, s := range g.Samples {
		if s.T <= 0 {
			continue
		}
		if aktifOrg != "" && strings.TrimSpace(s.Org) != "" && strings.TrimSpace(s.Org) != aktifOrg {
			continue
		}
		val, var_ := s.U["sd"]
		if !var_ {
			continue
		}
		f, err := val.Float64()
		if err != nil || f < 0 || f > 100 {
			continue
		}
		ornekler = append(ornekler, ornek{sn: s.T / 1000, sd: f})
	}
	if len(ornekler) < 2 {
		return 0, false
	}
	sort.Slice(ornekler, func(i, j int) bool { return ornekler[i].sn < ornekler[j].sn })

	var araliklar []claudeSifirlamaAralik
	for i := 1; i < len(ornekler); i++ {
		onceki, simdiki := ornekler[i-1], ornekler[i]
		if simdiki.sn <= onceki.sn || simdiki.sn-onceki.sn > 5*86400 {
			continue
		}
		dusus := onceki.sd - simdiki.sd
		// Gerçek haftalık sıfırlama sayacı 0%'a indirir; örnekler arası boşluk kısayken (ör. 5 dk)
		// ara havuz ayrımı nedeniyle oluşan kısmi düşüşler (ör. %91 -> %44) haftalık sıfırlama değildir.
		gecenSaat := float64(simdiki.sn-onceki.sn) / 3600.0
		azamiSonraki := 12.0 + gecenSaat*1.2
		if azamiSonraki > 50 {
			azamiSonraki = 50
		}
		if (dusus >= 10 || (onceki.sd >= 3 && simdiki.sd == 0)) && simdiki.sd <= azamiSonraki {
			araliklar = append(araliklar, claudeSifirlamaAralik{
				basDk:   onceki.sn / 60,
				bitDk:   simdiki.sn / 60,
				sifirMi: simdiki.sd == 0,
			})
		}
	}
	if len(araliklar) == 0 {
		return 0, false
	}
	if len(araliklar) > 8 {
		araliklar = araliklar[len(araliklar)-8:]
	}
	// En yeni düşüşten geriye doğru tutarlı kesişimi büyüt (abonelik günü değiştiyse eski uyumsuz düşüşte durur)
	aktif := []claudeSifirlamaAralik{araliklar[len(araliklar)-1]}
	for i := len(araliklar) - 2; i >= 0; i-- {
		aday := append([]claudeSifirlamaAralik{araliklar[i]}, aktif...)
		if _, ok := claudeDakikaKesisim(aday); ok {
			aktif = aday
		} else {
			// Tek başına son aralık 6 saatten genişse bir öncekiyle kesişip daralabilir
			const haftaDk = 7 * 1440
			herIkisiVar := false
			for m := 0; m < haftaDk; m++ {
				tumu := true
				for _, a := range aday {
					if ((int64(m)-(a.basDk%haftaDk)+haftaDk)%haftaDk) > (a.bitDk - a.basDk) {
						tumu = false
						break
					}
				}
				if tumu {
					herIkisiVar = true
					break
				}
			}
			if herIkisiVar {
				aktif = aday
			} else {
				break
			}
		}
	}
	fazSn, ok := claudeDakikaKesisim(aktif)
	if !ok {
		return 0, false
	}
	// Unix epoch 0 = Perşembe 00:00 UTC; fazSn zaten Unix saniyesi mod haftaSn'dir
	sonOrnek := ornekler[len(ornekler)-1].sn
	bazHafta := (sonOrnek / haftaSn) * haftaSn
	adayReset := bazHafta + fazSn
	if r := sonrakiHaftaReset(adayReset, simdi); r > 0 {
		return r, true
	}
	return 0, false
}

// claudeHaftalikSifirlamaOku: plan-usage-history.json içinden haftalık sıfırlama zamanını salt okunur çıkarır.
func claudeHaftalikSifirlamaOku(simdi int64) (int64, bool) {
	yol := claudeMasaustuLimitYolu()
	if yol == "" {
		return 0, false
	}
	b, err := os.ReadFile(yol)
	if err != nil {
		return 0, false
	}
	var g masaustuGecmis
	if json.Unmarshal(b, &g) != nil || len(g.Samples) == 0 {
		return 0, false
	}
	son := g.Samples[len(g.Samples)-1]
	if son.T <= 0 {
		return 0, false
	}
	if simdi > 0 && simdi-(son.T/1000) > 14*86400 {
		return 0, false
	}
	return claudeGecmisSifirlamaBul(g, strings.TrimSpace(son.Org), simdi)
}

func limitMasaustuOku(simdi int64) (limitDosyasi, bool) {
	yol := claudeMasaustuLimitYolu()
	if yol == "" {
		return limitDosyasi{}, false
	}
	b, err := os.ReadFile(yol)
	if err != nil {
		return limitDosyasi{}, false
	}
	var g masaustuGecmis
	if json.Unmarshal(b, &g) != nil || len(g.Samples) == 0 {
		return limitDosyasi{}, false
	}
	son := g.Samples[len(g.Samples)-1]
	if son.T <= 0 || len(son.U) == 0 {
		return limitDosyasi{}, false
	}
	sn := son.T / 1000
	if simdi > 0 && simdi-sn > 24*3600 { // 24 saatten eskiyse atla
		return limitDosyasi{}, false
	}
	eslesmeler := map[string]string{
		"sd":  "seven_day.used_percentage",
		"fh":  "five_hour.used_percentage",
		"sdo": "seven_day_opus.used_percentage",
		"xu":  "extra_usage.used_percentage",
		"so":  "seven_day_oauth_apps.used_percentage",
		"sn":  "seven_day_native_oauth_apps.used_percentage",
	}
	cikti := map[string]json.Number{}
	for k, hedef := range eslesmeler {
		if val, var_ := son.U[k]; var_ {
			if f, err := val.Float64(); err == nil && f >= 0 && f <= 100 {
				cikti[hedef] = val
			}
		}
	}
	if len(cikti) == 0 {
		return limitDosyasi{}, false
	}
	if _, sdVar := cikti["seven_day.used_percentage"]; sdVar {
		if wr, ok := claudeGecmisSifirlamaBul(g, strings.TrimSpace(son.Org), simdi); ok {
			cikti["seven_day.resets_at"] = json.Number(strconv.FormatInt(wr, 10))
		}
	}
	return limitDosyasi{T: sn, R: limitSinirla(cikti)}, true
}

// Gönderilecek limit: yalnızca Claude masaüstü geçmişi
func limitBirlesik(simdi int64) (limitDosyasi, bool) {
	return limitMasaustuOku(simdi)
}
