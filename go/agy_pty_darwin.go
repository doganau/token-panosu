//go:build darwin && (sag_gemini || (!sag_claude && !sag_chatgpt))

package main

import (
	"bytes"
	"errors"
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
		tiocptygrant = 0x20007454
		tiocptyunlk  = 0x20007452
		tiocptygname = 0x40807453
		tiocswinsz   = 0x80087467
	)
	if _, _, e := syscall.Syscall(syscall.SYS_IOCTL, master.Fd(), tiocptygrant, 0); e != 0 {
		_ = master.Close()
		return nil, nil, e
	}
	if _, _, e := syscall.Syscall(syscall.SYS_IOCTL, master.Fd(), tiocptyunlk, 0); e != 0 {
		_ = master.Close()
		return nil, nil, e
	}
	var adBuf [128]byte
	if _, _, e := syscall.Syscall(syscall.SYS_IOCTL, master.Fd(), tiocptygname, uintptr(unsafe.Pointer(&adBuf[0]))); e != 0 {
		_ = master.Close()
		return nil, nil, e
	}
	n := bytes.IndexByte(adBuf[:], 0)
	if n <= 0 {
		_ = master.Close()
		return nil, nil, errors.New("pty adı okunamadı")
	}
	ws := struct{ satir, sutun, x, y uint16 }{24, 100, 0, 0}
	_, _, _ = syscall.Syscall(syscall.SYS_IOCTL, master.Fd(), tiocswinsz, uintptr(unsafe.Pointer(&ws)))

	slave, err := os.OpenFile(string(adBuf[:n]), os.O_RDWR|syscall.O_NOCTTY, 0)
	if err != nil {
		_ = master.Close()
		return nil, nil, err
	}
	return master, slave, nil
}
