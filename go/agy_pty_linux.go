//go:build linux && (sag_gemini || (!sag_claude && !sag_chatgpt))

package main

import (
	"fmt"
	"os"
	"syscall"
	"unsafe"
)

func ptyAc() (*os.File, *os.File, error) {
	master, err := os.OpenFile("/dev/ptmx", os.O_RDWR|syscall.O_NOCTTY|syscall.O_CLOEXEC, 0)
	if err != nil {
		return nil, nil, err
	}
	const (
		tiocsptlck = 0x40045431
		tiocgptn   = 0x80045430
		tiocswinsz = 0x5414
	)
	var kilit int32
	if _, _, e := syscall.Syscall(syscall.SYS_IOCTL, master.Fd(), tiocsptlck, uintptr(unsafe.Pointer(&kilit))); e != 0 {
		_ = master.Close()
		return nil, nil, e
	}
	var ptn uint32
	if _, _, e := syscall.Syscall(syscall.SYS_IOCTL, master.Fd(), tiocgptn, uintptr(unsafe.Pointer(&ptn))); e != 0 {
		_ = master.Close()
		return nil, nil, e
	}
	ws := struct{ satir, sutun, x, y uint16 }{24, 100, 0, 0}
	_, _, _ = syscall.Syscall(syscall.SYS_IOCTL, master.Fd(), tiocswinsz, uintptr(unsafe.Pointer(&ws)))

	slave, err := os.OpenFile(fmt.Sprintf("/dev/pts/%d", ptn), os.O_RDWR|syscall.O_NOCTTY, 0)
	if err != nil {
		_ = master.Close()
		return nil, nil, err
	}
	return master, slave, nil
}
