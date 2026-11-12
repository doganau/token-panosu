//go:build (darwin || linux) && (sag_gemini || (!sag_claude && !sag_chatgpt))

package main

import (
	"io"
	"os/exec"
	"syscall"
	"time"
)

// agyPtyCalistir: agy CLI'ı arka planda geçici bir sözde uçbirimde (PTY) başlatır,
// hazir() true döner dönmez Ctrl+D Ctrl+D göndererek temizce kapatır.
func agyPtyCalistir(agyYol, calismaDizini string, sure time.Duration, hazir func() bool) {
	master, slave, err := ptyAc()
	if err != nil {
		return
	}
	ortam := agyOrtam()
	baslat := func(ctty bool) (*exec.Cmd, error) {
		c := exec.Command(agyYol)
		c.Dir = calismaDizini
		c.Env = ortam
		c.Stdin = slave
		c.Stdout = slave
		c.Stderr = slave
		if ctty {
			c.SysProcAttr = &syscall.SysProcAttr{Setsid: true, Setctty: true, Ctty: 0}
		} else {
			c.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
		}
		return c, c.Start()
	}
	cmd, err := baslat(true)
	if err != nil {
		cmd, err = baslat(false)
	}
	_ = slave.Close()
	if err != nil {
		_ = master.Close()
		return
	}

	go func() { _, _ = io.Copy(io.Discard, master) }()
	bitti := make(chan struct{})
	go func() {
		_ = cmd.Wait()
		close(bitti)
	}()

	zorlaKapat := func() {
		_ = master.Close()
		if cmd.Process != nil {
			if cmd.Process.Pid > 0 {
				_ = syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
			}
			_ = cmd.Process.Kill()
		}
		select {
		case <-bitti:
		case <-time.After(500 * time.Millisecond):
		}
	}

	son := time.Now().Add(sure)
	for time.Now().Before(son) {
		select {
		case <-bitti:
			_ = master.Close()
			return
		default:
		}
		if hazir() {
			_, _ = master.Write([]byte("\x04\x04"))
			select {
			case <-bitti:
				_ = master.Close()
			case <-time.After(600 * time.Millisecond):
				zorlaKapat()
			}
			return
		}
		time.Sleep(100 * time.Millisecond)
	}

	_, _ = master.Write([]byte("\x04\x04"))
	select {
	case <-bitti:
		_ = master.Close()
	case <-time.After(300 * time.Millisecond):
		zorlaKapat()
	}
}
