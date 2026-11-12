//go:build windows && (sag_gemini || (!sag_claude && !sag_chatgpt))

package main

import (
	"io"
	"os"
	"syscall"
	"time"
	"unicode/utf16"
	"unsafe"
)

var (
	procCreatePseudoConsole      = kernel32.NewProc("CreatePseudoConsole")
	procClosePseudoConsole       = kernel32.NewProc("ClosePseudoConsole")
	procInitProcThreadAttrList   = kernel32.NewProc("InitializeProcThreadAttributeList")
	procUpdateProcThreadAttr     = kernel32.NewProc("UpdateProcThreadAttribute")
	procDeleteProcThreadAttrList = kernel32.NewProc("DeleteProcThreadAttributeList")
	procCreateProcessW           = kernel32.NewProc("CreateProcessW")
)

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
