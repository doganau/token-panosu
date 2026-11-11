package main

// Sunucudan gelen kısa bildirimleri işletim sisteminin kendi bildirimiyle gösterir.
// Ek program ya da kütüphane gerekmez: macOS osascript, Linux notify-send, Windows PowerShell (toast).
// Gösterim başarısız olsa bile gönderimi bozmaz ve bildirim bir daha denenmez.

import (
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"time"
	"unicode"
	"unicode/utf16"
)

const (
	bildirimBaslikMaks = 60
	bildirimMetinMaks  = 240
	bildirimZamanAsimi = 10 * time.Second
)

type bildirimOge struct {
	ID     int64  `json:"id"`
	Baslik string `json:"baslik"`
	Metin  string `json:"metin"`
}

func sonBildirimYolu() string  { return filepath.Join(dizin, "son_bildirim") }
func bildirimTestYolu() string { return filepath.Join(dizin, "bildirim_test.log") }

// Gösterilen son bildirimin kimliği; dosya yoksa ok=false (sunucu kendi kaydını kullanır)
func sonBildirim() (int64, bool) {
	b, err := os.ReadFile(sonBildirimYolu())
	if err != nil {
		return 0, false
	}
	n, err := strconv.ParseInt(strings.TrimSpace(string(b)), 10, 64)
	if err != nil || n < 0 {
		return 0, false
	}
	return n, true
}

// Kontrol karakterlerini boşluğa çevirir, baştaki/sondaki boşluğu atar, rune sayısını sınırlar
func bildirimTemizle(s string, maks int) string {
	r := []rune(s)
	for i, c := range r {
		if unicode.IsControl(c) || c == ' ' || c == ' ' {
			r[i] = ' '
		}
	}
	r = []rune(strings.TrimSpace(string(r)))
	if len(r) > maks {
		r = []rune(strings.TrimSpace(string(r[:maks])))
	}
	return string(r)
}

// Gönderim yanıtındaki bildirimleri kimlik sırasıyla gösterir; her biri (başarısız olsa da) gösterilmiş sayılır
func bildirimleriIsle(liste []bildirimOge) {
	sort.Slice(liste, func(i, j int) bool { return liste[i].ID < liste[j].ID })
	son, _ := sonBildirim()
	for _, b := range liste {
		if b.ID <= son {
			continue
		}
		baslik, metin := bildirimTemizle(b.Baslik, bildirimBaslikMaks), bildirimTemizle(b.Metin, bildirimMetinMaks)
		if baslik == "" || metin == "" {
			logYaz("bildirim atlandı (boş): %d", b.ID)
		} else if testModu {
			if err := bildirimTestYaz(b.ID, baslik, metin); err != nil {
				logYaz("bildirim test dosyasına yazılamadı: %d: %v", b.ID, err)
			} else {
				logYaz("bildirim gösterildi: %d (test modu)", b.ID)
			}
		} else if err := bildirimGoster(baslik, metin); err != nil {
			logYaz("bildirim gösterilemedi: %d: %v", b.ID, err)
		} else {
			logYaz("bildirim gösterildi: %d", b.ID)
		}
		son = b.ID
		_ = os.MkdirAll(dizin, 0o700)
		_ = os.WriteFile(sonBildirimYolu(), []byte(strconv.FormatInt(son, 10)), 0o600)
	}
}

func bildirimTestYaz(id int64, baslik, metin string) error {
	f, err := os.OpenFile(bildirimTestYolu(), os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o600)
	if err != nil {
		return err
	}
	defer f.Close()
	_, err = fmt.Fprintf(f, "%d\t%s\t%s\n", id, baslik, metin)
	return err
}

// Gerçek bildirim gösterir (10 sn sınırı). Hata dönerse çağıran günlüğe yazar.
func bildirimGoster(baslik, metin string) error {
	baslik, metin = bildirimTemizle(baslik, bildirimBaslikMaks), bildirimTemizle(metin, bildirimMetinMaks)
	ctx, iptal := context.WithTimeout(context.Background(), bildirimZamanAsimi)
	defer iptal()
	var c *exec.Cmd
	switch runtime.GOOS {
	case "darwin":
		c = komut(ctx, "/usr/bin/osascript", "-e", appleBildirimBetigi(baslik, metin))
	case "windows":
		ps := os.Getenv("SystemRoot")
		if ps != "" {
			ps = filepath.Join(ps, "System32", "WindowsPowerShell", "v1.0", "powershell.exe")
		}
		if _, err := os.Stat(ps); err != nil {
			ps = "powershell.exe"
		}
		c = komut(ctx, ps, "-NoProfile", "-NonInteractive", "-WindowStyle", "Hidden", "-EncodedCommand", psKodla(toastBetigi))
		// Metinler betiğe değil ortam değişkenine konur: betik sabittir, metin içinde kod çalışamaz
		c.Env = append(os.Environ(), "TP_BASLIK="+baslik, "TP_METIN="+metin)
	default:
		yol, err := exec.LookPath("notify-send")
		if err != nil {
			return errors.New("notify-send bulunamadı, atlandı")
		}
		c = komut(ctx, yol, "-a", "Token Panosu", "--", baslik, metin)
		c.Env = os.Environ()
		if os.Getenv("DBUS_SESSION_BUS_ADDRESS") == "" {
			// systemd kullanıcı zamanlayıcısı ve cron oturum veriyolu ortamını taşımaz
			if yol := fmt.Sprintf("/run/user/%d/bus", os.Getuid()); fileVar(yol) {
				c.Env = append(c.Env, "DBUS_SESSION_BUS_ADDRESS=unix:path="+yol)
			}
		}
	}
	out, err := c.CombinedOutput()
	if ctx.Err() != nil {
		return errors.New("10 sn içinde tamamlanmadı")
	}
	if err != nil {
		m := strings.TrimSpace(string(out))
		if len(m) > 160 {
			m = m[:160]
		}
		return fmt.Errorf("%v %s", err, m)
	}
	return nil
}

func fileVar(yol string) bool {
	_, err := os.Stat(yol)
	return err == nil
}

// AppleScript dizgesi: ters bölü ve çift tırnak kaçırılır, satır sonları boşluk olur
func appleKacis(s string) string {
	s = strings.NewReplacer("\r\n", " ", "\r", " ", "\n", " ", `\`, `\\`, `"`, `\"`).Replace(s)
	return `"` + s + `"`
}

func appleBildirimBetigi(baslik, metin string) string {
	return "display notification " + appleKacis(metin) + " with title " + appleKacis(baslik)
}

// Windows: sabit betik. Başlık ve metin TP_BASLIK / TP_METIN ortam değişkenlerinden okunur,
// XML'e CreateTextNode ile düz metin olarak eklenir (kaçış gerekmez). Uygulama kimliği PowerShell'in kendisidir:
// kayıt gerektirmeden bildirim gösterebilir.
const toastBetigi = `$ErrorActionPreference = 'Stop'
[void][Windows.UI.Notifications.ToastNotificationManager, Windows.UI.Notifications, ContentType = WindowsRuntime]
[void][Windows.Data.Xml.Dom.XmlDocument, Windows.Data.Xml.Dom.XmlDocument, ContentType = WindowsRuntime]
$x = [Windows.UI.Notifications.ToastNotificationManager]::GetTemplateContent([Windows.UI.Notifications.ToastTemplateType]::ToastText02)
$t = $x.GetElementsByTagName('text')
[void]$t.Item(0).AppendChild($x.CreateTextNode($env:TP_BASLIK))
[void]$t.Item(1).AppendChild($x.CreateTextNode($env:TP_METIN))
$n = New-Object Windows.UI.Notifications.ToastNotification($x)
$id = '{1AC14E77-02E7-4E5D-B744-2EB1AE5198B7}\WindowsPowerShell\v1.0\powershell.exe'
[Windows.UI.Notifications.ToastNotificationManager]::CreateToastNotifier($id).Show($n)
`

// -EncodedCommand: UTF-16LE, base64
func psKodla(s string) string {
	k := utf16.Encode([]rune(s))
	b := make([]byte, 0, 2*len(k))
	for _, u := range k {
		b = append(b, byte(u), byte(u>>8))
	}
	return base64.StdEncoding.EncodeToString(b)
}
