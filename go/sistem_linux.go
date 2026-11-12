package main

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
)

func sistemAnahtarKaydet(_, gizli string) error { return dosyaAnahtarYaz(gizli) }
func sistemAnahtarOku(_ string) (string, error) { return dosyaAnahtarOku() }
func sistemAnahtarSil(_ string)                 {}

func yukOrani() (float64, bool) {
	b, err := os.ReadFile("/proc/loadavg")
	if err != nil {
		return 0, false
	}
	f := strings.Fields(string(b))
	if len(f) == 0 {
		return 0, false
	}
	v, err := strconv.ParseFloat(f[0], 64)
	if err != nil {
		return 0, false
	}
	return v / float64(runtime.NumCPU()), true
}

func systemdDizini() string { return filepath.Join(evDizini(), ".config", "systemd", "user") }

func systemdVar() bool {
	if _, err := exec.LookPath("systemctl"); err != nil {
		return false
	}
	err := exec.Command("systemctl", "--user", "show-environment").Run()
	return err == nil
}

func systemdKacis(s string) string {
	return `"` + strings.NewReplacer(`\`, `\\`, `"`, `\"`, "%", "%%").Replace(s) + `"`
}

// systemd kullanıcı zamanlayıcısı (oturum açılınca başlar, 5 dakikada bir çalışır);
// systemd yoksa crontab (*/5 ve @reboot).
func zamanlayiciKur(ikili string) error {
	if systemdVar() {
		d := systemdDizini()
		if err := os.MkdirAll(d, 0o755); err != nil {
			return err
		}
		servis := fmt.Sprintf("[Unit]\nDescription=Token Panosu gonderici\n\n[Service]\nType=oneshot\nNice=10\nIOSchedulingClass=idle\nExecStart=%s gonder --sessiz\nTimeoutStartSec=300\n", systemdKacis(ikili))
		zaman := "[Unit]\nDescription=Token Panosu 5 dakikada bir\n\n[Timer]\nOnBootSec=1min\nOnUnitActiveSec=5min\nPersistent=true\n\n[Install]\nWantedBy=timers.target\n"
		if err := os.WriteFile(filepath.Join(d, "token-panosu.service"), []byte(servis), 0o644); err != nil {
			return err
		}
		if err := os.WriteFile(filepath.Join(d, "token-panosu.timer"), []byte(zaman), 0o644); err != nil {
			return err
		}
		if err := exec.Command("systemctl", "--user", "daemon-reload").Run(); err != nil {
			return err
		}
		return exec.Command("systemctl", "--user", "enable", "--now", "token-panosu.timer").Run()
	}
	if _, err := exec.LookPath("crontab"); err != nil {
		return fmt.Errorf("ne systemd ne de crontab bulundu")
	}
	eski, _ := exec.Command("crontab", "-l").Output()
	var b strings.Builder
	for _, l := range strings.Split(string(eski), "\n") {
		if l != "" && !strings.Contains(l, "token-panosu") {
			b.WriteString(l + "\n")
		}
	}
	q := strconv.Quote(ikili)
	fmt.Fprintf(&b, "*/5 * * * * nice -n 10 %s gonder --sessiz # token-panosu\n@reboot sleep 60; nice -n 10 %s gonder --sessiz # token-panosu\n", q, q)
	c := exec.Command("crontab", "-")
	c.Stdin = strings.NewReader(b.String())
	return c.Run()
}

func zamanlayiciKaldir() {
	if _, err := exec.LookPath("systemctl"); err == nil {
		_ = exec.Command("systemctl", "--user", "disable", "--now", "token-panosu.timer").Run()
		for _, ad := range []string{"token-panosu.service", "token-panosu.timer"} {
			_ = os.Remove(filepath.Join(systemdDizini(), ad))
		}
		_ = exec.Command("systemctl", "--user", "daemon-reload").Run()
	}
	if _, err := exec.LookPath("crontab"); err == nil {
		eski, _ := exec.Command("crontab", "-l").Output()
		if strings.Contains(string(eski), "token-panosu") {
			var b strings.Builder
			for _, l := range strings.Split(string(eski), "\n") {
				if l != "" && !strings.Contains(l, "token-panosu") {
					b.WriteString(l + "\n")
				}
			}
		c := exec.Command("crontab", "-")
		c.Stdin = strings.NewReader(b.String())
		_ = c.Run()
	}
	}
}

func yerelYolMu(string) bool { return true }
