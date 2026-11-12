package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"os/user"
	"path/filepath"
	"strings"
	"syscall"
	"time"
	"unicode/utf16"
	"unsafe"
)

const gorevAdi = "TokenPanosu"

var (
	crypt32              = syscall.NewLazyDLL("crypt32.dll")
	kernel32             = syscall.NewLazyDLL("kernel32.dll")
	procProtect          = crypt32.NewProc("CryptProtectData")
	procUnprotect        = crypt32.NewProc("CryptUnprotectData")
	procLocalFree        = kernel32.NewProc("LocalFree")
	procAttachConsole    = kernel32.NewProc("AttachConsole")
	procSetPriorityClass = kernel32.NewProc("SetPriorityClass")
	procGetCurrentProc   = kernel32.NewProc("GetCurrentProcess")
)

type veriBlogu struct {
	n uint32
	p *byte
}

func dpapi(veri []byte, koru bool) ([]byte, error) {
	if len(veri) == 0 {
		return nil, errors.New("boş veri")
	}
	girdi := veriBlogu{n: uint32(len(veri)), p: &veri[0]}
	var cikti veriBlogu
	const uiYok = 0x1 // CRYPTPROTECT_UI_FORBIDDEN
	var r uintptr
	if koru {
		ad, _ := syscall.UTF16PtrFromString("token-panosu")
		r, _, _ = procProtect.Call(uintptr(unsafe.Pointer(&girdi)), uintptr(unsafe.Pointer(ad)), 0, 0, 0, uiYok, uintptr(unsafe.Pointer(&cikti)))
	} else {
		r, _, _ = procUnprotect.Call(uintptr(unsafe.Pointer(&girdi)), 0, 0, 0, 0, uiYok, uintptr(unsafe.Pointer(&cikti)))
	}
	if r == 0 {
		return nil, errors.New("DPAPI işlemi başarısız")
	}
	defer procLocalFree.Call(uintptr(unsafe.Pointer(cikti.p)))
	return append([]byte(nil), unsafe.Slice(cikti.p, cikti.n)...), nil
}

func sistemAnahtarKaydet(_, gizli string) error {
	sifreli, err := dpapi([]byte(gizli), true)
	if err != nil {
		return err
	}
	return dosyaYaz(dosyaAnahtarYolu(), sifreli, 0o600)
}

func sistemAnahtarOku(_ string) (string, error) {
	b, err := os.ReadFile(dosyaAnahtarYolu())
	if err != nil {
		return "", err
	}
	acik, err := dpapi(b, false)
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(string(acik)), nil
}

func sistemAnahtarSil(_ string) {}

func dosyaAnahtarYaz(gizli string) error {
	return dosyaYaz(dosyaAnahtarYolu(), []byte(gizli), 0o600)
}

func dosyaAnahtarOku() (string, error) {
	b, err := os.ReadFile(dosyaAnahtarYolu())
	return strings.TrimSpace(string(b)), err
}

// Program pencere açmayan (GUI alt sistemi) olarak derlenir; komut satırından çalıştırıldığında
// çıktı bir boruya yönlendirilmemişse çağıran konsola bağlanır.
func pencereBagla() {
	h, err := syscall.GetStdHandle(syscall.STD_OUTPUT_HANDLE)
	if err == nil && h != 0 && h != syscall.InvalidHandle {
		return
	}
	const ebeveyn = ^uintptr(0) // ATTACH_PARENT_PROCESS
	if r, _, _ := procAttachConsole.Call(ebeveyn); r == 0 {
		return
	}
	if f, err := os.OpenFile("CONOUT$", os.O_RDWR, 0); err == nil {
		os.Stdout, os.Stderr = f, f
	}
}

func gizliPencere(c *exec.Cmd) {
	c.SysProcAttr = &syscall.SysProcAttr{HideWindow: true, CreationFlags: 0x08000000} // CREATE_NO_WINDOW
}

func oncelikDusur() {
	h, _, _ := procGetCurrentProc.Call()
	procSetPriorityClass.Call(h, 0x4000) // BELOW_NORMAL_PRIORITY_CLASS
}

// Windows'ta yük ortalaması yok: kontrol yapılmaz, öncelik düşük tutulur
func yukOrani() (float64, bool) { return 0, false }

// Çalışan .exe silinemez ama yeniden adlandırılabilir
func yerineKoy(gec, hedef string) error {
	eski := hedef + ".eski"
	_ = os.Remove(eski)
	if _, err := os.Stat(hedef); err == nil {
		if err := os.Rename(hedef, eski); err != nil {
			return err
		}
	}
	if err := os.Rename(gec, hedef); err != nil {
		_ = os.Rename(eski, hedef)
		return err
	}
	return nil
}

// Çalışan program kendi klasörünü hemen silemez: diğer dosyaları sil, klasörü kısa süre sonra kaldır
func klasoruSil(d string) {
	girdiler, _ := os.ReadDir(d)
	for _, g := range girdiler {
		_ = os.RemoveAll(filepath.Join(d, g.Name()))
	}
	c := exec.Command("cmd", "/c", "ping 127.0.0.1 -n 4 > nul & rmdir /s /q \""+d+"\"")
	gizliPencere(c)
	_ = c.Start()
}

func utf16BOM(s string) []byte {
	kod := utf16.Encode([]rune(s))
	b := make([]byte, 2, 2+2*len(kod))
	b[0], b[1] = 0xFF, 0xFE
	for _, k := range kod {
		b = append(b, byte(k), byte(k>>8))
	}
	return b
}

func gorevXML(ikili, kullanici string) string {
	return fmt.Sprintf(`<?xml version="1.0" encoding="UTF-16"?>
<Task version="1.2" xmlns="http://schemas.microsoft.com/windows/2004/02/mit/task">
  <RegistrationInfo><Description>Token Panosu gonderici: 5 dakikada bir Token Panosu kullanim verisini gonderir</Description></RegistrationInfo>
  <Triggers>
    <TimeTrigger>
      <StartBoundary>%s</StartBoundary>
      <Enabled>true</Enabled>
      <Repetition><Interval>PT5M</Interval><StopAtDurationEnd>false</StopAtDurationEnd></Repetition>
    </TimeTrigger>
    <LogonTrigger><Enabled>true</Enabled><UserId>%s</UserId><Delay>PT1M</Delay></LogonTrigger>
  </Triggers>
  <Principals>
    <Principal id="Author"><UserId>%s</UserId><LogonType>InteractiveToken</LogonType><RunLevel>LeastPrivilege</RunLevel></Principal>
  </Principals>
  <Settings>
    <MultipleInstancesPolicy>IgnoreNew</MultipleInstancesPolicy>
    <DisallowStartIfOnBatteries>false</DisallowStartIfOnBatteries>
    <StopIfGoingOnBatteries>false</StopIfGoingOnBatteries>
    <AllowHardTerminate>true</AllowHardTerminate>
    <StartWhenAvailable>true</StartWhenAvailable>
    <RunOnlyIfNetworkAvailable>false</RunOnlyIfNetworkAvailable>
    <IdleSettings><StopOnIdleEnd>false</StopOnIdleEnd><RestartOnIdle>false</RestartOnIdle></IdleSettings>
    <AllowStartOnDemand>true</AllowStartOnDemand>
    <Enabled>true</Enabled>
    <Hidden>true</Hidden>
    <RunOnlyIfIdle>false</RunOnlyIfIdle>
    <WakeToRun>false</WakeToRun>
    <ExecutionTimeLimit>PT5M</ExecutionTimeLimit>
    <Priority>7</Priority>
  </Settings>
  <Actions Context="Author">
    <Exec><Command>%s</Command><Arguments>gonder --sessiz</Arguments></Exec>
  </Actions>
</Task>
`, time.Now().Format("2006-01-02T15:04:05"), xmlKacis(kullanici), xmlKacis(kullanici), xmlKacis(ikili))
}

// Görev Zamanlayıcı: oturum açıkken 5 dakikada bir ve her oturum açılışında; pilde de çalışır
func zamanlayiciKur(ikili string) error {
	u, err := user.Current()
	if err != nil {
		return err
	}
	xmlYol := filepath.Join(dizin, "gorev.xml")
	if err := os.WriteFile(xmlYol, utf16BOM(gorevXML(ikili, u.Username)), 0o600); err != nil {
		return err
	}
	defer os.Remove(xmlYol)
	c := exec.Command("schtasks", "/Create", "/F", "/TN", gorevAdi, "/XML", xmlYol)
	gizliPencere(c)
	if out, err := c.CombinedOutput(); err != nil {
		return fmt.Errorf("schtasks: %v %s", err, strings.TrimSpace(string(out)))
	}
	return nil
}

func zamanlayiciKaldir() {
	c := exec.Command("schtasks", "/Delete", "/F", "/TN", gorevAdi)
	gizliPencere(c)
	_ = c.Run()
}

// Önceki durum satırı komutunu cmd ile çalıştırır. Başında tırnak olan komutlar için cmd /C kuralı
// gereği tamamı bir çift tırnağa sarılır; pencere açılmaz, stdin/stdout çağıranın verdiği borulardır.
func kabukKomutu(ctx context.Context, s string) *exec.Cmd {
	c := exec.CommandContext(ctx, "cmd")
	c.SysProcAttr = &syscall.SysProcAttr{HideWindow: true, CreationFlags: 0x08000000, CmdLine: `cmd /C "` + s + `"`}
	return c
}

func yerelYolMu(string) bool { return true }
