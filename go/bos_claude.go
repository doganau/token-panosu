//go:build !sag_claude && (sag_gemini || sag_chatgpt)

package main

const (
	sagClaudeEtkin = false
	sagClaudeAd    = ""
)

func topla(int64) sonuc {
	s := sonuc{satirlar: []Satir{}, dilimler: []Dilim{}, bulgular: []Bulgu{}}
	if b := dosyaIzniBulgusu(); b != nil {
		s.bulgular = append(s.bulgular, *b)
	}
	return s
}

func limitBirlesik(int64) (limitDosyasi, bool)      { return limitDosyasi{}, false }
func durumSatiriKaldir()                            {}
func uyariKur()                                     {}
func uyariKaldir()                                  {}
func asimIsle(*asimDurumu, int64)                   {}
func claudeEkBilgi() (string, string, string)       { return "", "", "" }
func claudeHaftaSifirlamaEkle(int64, map[string]int64) {}
func claudeOzelAltKomut(string) bool                { return false }
func claudeEsikAnahtarMi(string) bool               { return false }
func claudeEsikAday(string) string                  { return "" }
