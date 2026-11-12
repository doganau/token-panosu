//go:build !sag_gemini && (sag_claude || sag_chatgpt)

package main

const (
	sagGeminiEtkin = false
	sagGeminiAd    = ""
)

func geminiTopla(int64, int64, map[string]*digerDosyaKaydi, map[string]*digerDosyaKaydi, *digerSonuc, func(string, string, string)) []*digerDosyaKaydi {
	return nil
}

func geminiSaglayiciKaydiMi(string, *digerDosyaKaydi) bool { return false }
func geminiEsikAnahtarMi(string) bool                      { return false }
func geminiEsikAltModel(string) string                     { return "" }
func geminiOzelAltKomut(string) bool                       { return false }
func geminiKancaKur()                                      {}
func geminiKancaKaldir()                                   {}
func geminiKotaYenile(int64)                               {}
