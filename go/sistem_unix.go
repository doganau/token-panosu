//go:build darwin || linux

package main

import (
	"context"
	"io"
	"os"
	"os/exec"
	"strings"
	"syscall"
	"time"
)

func pencereBagla()            {}
func gizliPencere(c *exec.Cmd) {}

// Sürecin önceliğini düşür (zamanlayıcı zaten düşük öncelikle başlatır; elle çalıştırmada da nazik olsun)
func oncelikDusur() { _ = syscall.Setpriority(syscall.PRIO_PROCESS, 0, 10) }

func yerineKoy(gec, hedef string) error { return os.Rename(gec, hedef) }

func klasoruSil(d string) { _ = os.RemoveAll(d) }

// Test modunda ya da Linux'ta anahtar 0600 izinli dosyada durur
func dosyaAnahtarYaz(gizli string) error {
	return dosyaYaz(dosyaAnahtarYolu(), []byte(gizli), 0o600)
}

func dosyaAnahtarOku() (string, error) {
	b, err := os.ReadFile(dosyaAnahtarYolu())
	return strings.TrimSpace(string(b)), err
}

// Önceki durum satırı komutunu kabuktan çalıştırır
func kabukKomutu(ctx context.Context, s string) *exec.Cmd {
	return exec.CommandContext(ctx, "sh", "-c", s)
}

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
			case <-time.After(600 * time.Millisecond):
				if cmd.Process != nil {
					_ = cmd.Process.Kill()
				}
				<-bitti
			}
			_ = master.Close()
			return
		}
		time.Sleep(100 * time.Millisecond)
	}

	_, _ = master.Write([]byte("\x04\x04"))
	select {
	case <-bitti:
	case <-time.After(300 * time.Millisecond):
		if cmd.Process != nil {
			_ = cmd.Process.Kill()
		}
		select {
		case <-bitti:
		case <-time.After(500 * time.Millisecond):
		}
	}
	_ = master.Close()
}
