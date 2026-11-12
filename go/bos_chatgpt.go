//go:build !sag_chatgpt && (sag_claude || sag_gemini)

package main

const (
	sagChatgptEtkin = false
	sagChatgptAd    = ""
)

func chatgptTopla(int64, int64, map[string]*digerDosyaKaydi, map[string]*digerDosyaKaydi, *digerSonuc, func(string, string, string)) []*digerDosyaKaydi {
	return nil
}

func chatgptSaglayiciKaydiMi(string, *digerDosyaKaydi) bool { return false }
func chatgptEsikAnahtarMi(string) bool                      { return false }
func chatgptEsikAltModel(string) string                     { return "" }
