package main

import (
	"encoding/binary"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"syscall"
)

const (
	keychainServis = "token-panosu"
	launchdEtiket  = "com.tokenpanosu.gonderici"
)

func plistYolu() string {
	return filepath.Join(evDizini(), "Library", "LaunchAgents", launchdEtiket+".plist")
}

func sistemAnahtarKaydet(cihaz, gizli string) error {
	out, err := exec.Command("/usr/bin/security", "add-generic-password", "-U",
		"-s", keychainServis, "-a", cihaz, "-w", gizli).CombinedOutput()
	if err != nil {
		return fmt.Errorf("%v: %s", err, strings.TrimSpace(string(out)))
	}
	return nil
}

func sistemAnahtarOku(cihaz string) (string, error) {
	out, err := exec.Command("/usr/bin/security", "find-generic-password",
		"-s", keychainServis, "-a", cihaz, "-w").Output()
	if err != nil {
		return "", errors.New("Anahtar Zinciri'nde anahtar bulunamadı")
	}
	return strings.TrimSpace(string(out)), nil
}

func sistemAnahtarSil(cihaz string) {
	_ = exec.Command("/usr/bin/security", "delete-generic-password", "-s", keychainServis, "-a", cihaz).Run()
}

// 1 dakikalık yük ortalaması / çekirdek sayısı
func yukOrani() (float64, bool) {
	s, err := syscall.Sysctl("vm.loadavg")
	if err != nil {
		return 0, false
	}
	b := []byte(s)
	for len(b) < 24 { // Sysctl sondaki sıfır baytı kırpar
		b = append(b, 0)
	}
	ld := float64(binary.LittleEndian.Uint32(b[0:4]))
	olcek := float64(binary.LittleEndian.Uint64(b[16:24]))
	if olcek <= 0 {
		return 0, false
	}
	return ld / olcek / float64(runtime.NumCPU()), true
}

func plistIcerik(ikili string) string {
	return fmt.Sprintf(`<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0">
<dict>
  <key>Label</key><string>%s</string>
  <key>ProgramArguments</key>
  <array><string>%s</string><string>gonder</string><string>--sessiz</string></array>
  <key>StartInterval</key><integer>300</integer>
  <key>RunAtLoad</key><true/>
  <key>ProcessType</key><string>Background</string>
  <key>LowPriorityIO</key><true/>
  <key>Nice</key><integer>10</integer>
  <key>StandardOutPath</key><string>/dev/null</string>
  <key>StandardErrorPath</key><string>%s</string>
</dict>
</plist>
`, launchdEtiket, xmlKacis(ikili), xmlKacis(logYolu()))
}

// LaunchAgent: oturum açılınca (yeniden başlatmadan sonra da) kendiliğinden yüklenir, 5 dakikada bir çalışır
func zamanlayiciKur(ikili string) error {
	yol := plistYolu()
	if err := os.MkdirAll(filepath.Dir(yol), 0o755); err != nil {
		return err
	}
	alan := fmt.Sprintf("gui/%d", os.Getuid())
	_ = exec.Command("/bin/launchctl", "bootout", alan, yol).Run()
	if err := os.WriteFile(yol, []byte(plistIcerik(ikili)), 0o644); err != nil {
		return err
	}
	if out, err := exec.Command("/bin/launchctl", "bootstrap", alan, yol).CombinedOutput(); err != nil {
		return fmt.Errorf("launchctl: %v %s", err, strings.TrimSpace(string(out)))
	}
	return nil
}

func zamanlayiciKaldir() {
	yol := plistYolu()
	if _, err := os.Stat(yol); err == nil {
		_ = exec.Command("/bin/launchctl", "bootout", fmt.Sprintf("gui/%d", os.Getuid()), yol).Run()
		_ = os.Remove(yol)
	}
}
