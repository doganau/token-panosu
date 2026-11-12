package main

import (
	"context"
	"errors"
	"fmt"
	"io"
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
	crypt32                       = syscall.NewLazyDLL("crypt32.dll")
	kernel32                      = syscall.NewLazyDLL("kernel32.dll")
	procProtect                   = crypt32.NewProc("CryptProtectData")
	procUnprotect                 = crypt32.NewProc("CryptUnprotectData")
	procLocalFree                 = kernel32.NewProc("LocalFree")
	procAttachConsole             = kernel32.NewProc("AttachConsole")
	procSetPriorityClass          = kernel32.NewProc("SetPriorityClass")
	procGetCurrentProc            = kernel32.NewProc("GetCurrentProcess")
	procCreatePseudoConsole       = kernel32.NewProc("CreatePseudoConsole")
	procClosePseudoConsole        = kernel32.NewProc("ClosePseudoConsole")
	procInitProcThreadAttrList    = kernel32.NewProc("InitializeProcThreadAttributeList")
	procUpdateProcThreadAttr      = kernel32.NewProc("UpdateProcThreadAttribute")
	procDeleteProcThreadAttrList  = kernel32.NewProc("DeleteProcThreadAttributeList")
	procCreateProcessW            = kernel32.NewProc("CreateProcessW")
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
  <RegistrationInfo><Description>Token Panosu gonderici: 5 dakikada bir Claude Code kullanimini gonderir</Description></RegistrationInfo>
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

type winStartupInfoEx struct {
	syscall.StartupInfo
	attributeList *byte
}

// agyPtyCalistir: Windows 10 1809+ / Windows 11 ConPTY (CreatePseudoConsole) ile görünmez uçbirim açar,
// hazir() true döner dönmez Ctrl+D Ctrl+D yazarak agy sürecini temizce sonlandırır.
func agyPtyCalistir(agyYol, calismaDizini string, sure time.Duration, hazir func() bool) {
	if procCreatePseudoConsole.Find() != nil || procInitProcThreadAttrList.Find() != nil ||
		procUpdateProcThreadAttr.Find() != nil || procCreateProcessW.Find() != nil {
		return
	}
	var inRead, inWrite, outRead, outWrite syscall.Handle
	if syscall.CreatePipe(&inRead, &inWrite, nil, 0) != nil {
		return
	}
	if syscall.CreatePipe(&outRead, &outWrite, nil, 0) != nil {
		_ = syscall.CloseHandle(inRead)
		_ = syscall.CloseHandle(inWrite)
		return
	}
	var hpc uintptr
	boyut := uintptr((24 << 16) | 100) // COORD{X: 100, Y: 24}
	hr, _, _ := procCreatePseudoConsole.Call(boyut, uintptr(inRead), uintptr(outWrite), 0, uintptr(unsafe.Pointer(&hpc)))
	_ = syscall.CloseHandle(inRead)
	_ = syscall.CloseHandle(outWrite)
	if hr != 0 || hpc == 0 {
		_ = syscall.CloseHandle(inWrite)
		_ = syscall.CloseHandle(outRead)
		return
	}

	var attrBoyut uintptr
	procInitProcThreadAttrList.Call(0, 1, 0, uintptr(unsafe.Pointer(&attrBoyut)))
	if attrBoyut == 0 {
		procClosePseudoConsole.Call(hpc)
		_ = syscall.CloseHandle(inWrite)
		_ = syscall.CloseHandle(outRead)
		return
	}
	attrBuf := make([]byte, attrBoyut)
	r1, _, _ := procInitProcThreadAttrList.Call(uintptr(unsafe.Pointer(&attrBuf[0])), 1, 0, uintptr(unsafe.Pointer(&attrBoyut)))
	if r1 == 0 {
		procClosePseudoConsole.Call(hpc)
		_ = syscall.CloseHandle(inWrite)
		_ = syscall.CloseHandle(outRead)
		return
	}
	defer procDeleteProcThreadAttrList.Call(uintptr(unsafe.Pointer(&attrBuf[0])))

	const procThreadAttrPseudoConsole = 0x00020016
	r2, _, _ := procUpdateProcThreadAttr.Call(
		uintptr(unsafe.Pointer(&attrBuf[0])),
		0,
		procThreadAttrPseudoConsole,
		hpc,
		unsafe.Sizeof(hpc),
		0,
		0,
	)
	if r2 == 0 {
		procClosePseudoConsole.Call(hpc)
		_ = syscall.CloseHandle(inWrite)
		_ = syscall.CloseHandle(outRead)
		return
	}

	var siEx winStartupInfoEx
	siEx.Cb = uint32(unsafe.Sizeof(siEx))
	siEx.attributeList = &attrBuf[0]

	komutUTF16 := utf16.Encode([]rune(syscall.EscapeArg(agyYol) + "\x00"))
	var dirPtr uintptr
	if calismaDizini != "" {
		if dp, err := syscall.UTF16PtrFromString(calismaDizini); err == nil {
			dirPtr = uintptr(unsafe.Pointer(dp))
		}
	}

	const extendedStartupInfoPresent = 0x00080000
	var pi syscall.ProcessInformation
	ok, _, _ := procCreateProcessW.Call(
		0,
		uintptr(unsafe.Pointer(&komutUTF16[0])),
		0,
		0,
		0,
		extendedStartupInfoPresent,
		0,
		dirPtr,
		uintptr(unsafe.Pointer(&siEx.StartupInfo)),
		uintptr(unsafe.Pointer(&pi)),
	)
	if ok == 0 {
		procClosePseudoConsole.Call(hpc)
		_ = syscall.CloseHandle(inWrite)
		_ = syscall.CloseHandle(outRead)
		return
	}
	_ = syscall.CloseHandle(pi.Thread)

	inDosya := os.NewFile(uintptr(inWrite), "conpty-in")
	outDosya := os.NewFile(uintptr(outRead), "conpty-out")
	go func() { _, _ = io.Copy(io.Discard, outDosya) }()

	bitti := make(chan struct{})
	go func() {
		_, _ = syscall.WaitForSingleObject(pi.Process, syscall.INFINITE)
		close(bitti)
	}()

	temizle := func() {
		procClosePseudoConsole.Call(hpc)
		_ = inDosya.Close()
		_ = outDosya.Close()
		_ = syscall.CloseHandle(pi.Process)
	}

	son := time.Now().Add(sure)
	for time.Now().Before(son) {
		select {
		case <-bitti:
			temizle()
			return
		default:
		}
		if hazir() {
			_, _ = inDosya.Write([]byte("\x04\x04"))
			select {
			case <-bitti:
			case <-time.After(600 * time.Millisecond):
				_ = syscall.TerminateProcess(pi.Process, 0)
				<-bitti
			}
			temizle()
			return
		}
		time.Sleep(100 * time.Millisecond)
	}

	_, _ = inDosya.Write([]byte("\x04\x04"))
	select {
	case <-bitti:
	case <-time.After(300 * time.Millisecond):
		_ = syscall.TerminateProcess(pi.Process, 0)
		select {
		case <-bitti:
		case <-time.After(500 * time.Millisecond):
		}
	}
	temizle()
}
