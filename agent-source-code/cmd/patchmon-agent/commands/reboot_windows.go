//go:build windows

package commands

import (
	"fmt"
	"os/exec"
	"strconv"
	"strings"
)

// scheduleReboot asks shutdown.exe to restart after delayMinutes, or now.
// /d p:2:17 records it as a planned restart for security updates.
func scheduleReboot(delayMinutes int, message string) error {
	seconds := strconv.Itoa(delayMinutes * 60)
	out, err := exec.Command("shutdown.exe", "/r", "/t", seconds, "/d", "p:2:17", "/c", message).CombinedOutput()
	if err != nil {
		return fmt.Errorf("shutdown /r /t %s: %w: %s", seconds, err, strings.TrimSpace(string(out)))
	}
	return nil
}
