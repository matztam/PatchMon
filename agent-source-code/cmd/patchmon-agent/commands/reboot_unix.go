//go:build !windows

package commands

import (
	"fmt"
	"os/exec"
	"strconv"
	"strings"
)

// scheduleReboot asks shutdown(8) to reboot after delayMinutes, or now.
// The message goes to logged-in users. Arguments are passed without a shell.
func scheduleReboot(delayMinutes int, message string) error {
	when := "now"
	if delayMinutes > 0 {
		when = "+" + strconv.Itoa(delayMinutes)
	}
	out, err := exec.Command("shutdown", "-r", when, message).CombinedOutput()
	if err != nil {
		return fmt.Errorf("shutdown -r %s: %w: %s", when, err, strings.TrimSpace(string(out)))
	}
	return nil
}
