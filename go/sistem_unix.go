//go:build darwin || linux

package main

import (
	"context"
	"os"
	"os/exec"
	"strings"
	"syscall"
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
