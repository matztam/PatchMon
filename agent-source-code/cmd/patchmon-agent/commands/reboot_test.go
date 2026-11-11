package commands

import (
	"context"
	"errors"
	"os"
	"strings"
	"testing"
	"time"

	"patchmon-agent/pkg/models"
)

type rebootAck struct{ status, reason string }

type rebootCall struct {
	delay   int
	message string
}

// stubReboot replaces the executor, the acker and the sleep, and records the
// order in which acks and the reboot happen.
func stubReboot(t *testing.T, execErr error) (*[]rebootAck, *[]rebootCall, *[]string) {
	t.Helper()
	var acks []rebootAck
	var calls []rebootCall
	var order []string
	prevExec, prevAck, prevSleep := rebootExecutor, rebootAcker, rebootSleep
	rebootExecutor = func(d int, m string) error {
		calls = append(calls, rebootCall{d, m})
		order = append(order, "reboot")
		return execErr
	}
	rebootAcker = func(_ context.Context, _, status, reason string) error {
		acks = append(acks, rebootAck{status, reason})
		order = append(order, "ack:"+status)
		return nil
	}
	rebootSleep = func(time.Duration) {}
	t.Cleanup(func() {
		rebootExecutor, rebootAcker, rebootSleep = prevExec, prevAck, prevSleep
		patchUpdateMu.Lock()
		activePatchRuns, agentUpdateDeferred, agentUpdating, rebootPending = 0, false, false, false
		patchUpdateMu.Unlock()
	})
	return &acks, &calls, &order
}

func enableReboot(t *testing.T) {
	t.Helper()
	m := withTestConfig(t, "false")
	if err := m.SetIntegrationEnabled(rebootIntegration, true); err != nil {
		t.Fatal(err)
	}
}

const testRequestID = "3f2b8c1e-0d4a-4c6b-9a7e-5b1f2e3d4c5a"

func TestRebootRejectedWhenNotEnabled(t *testing.T) {
	withTestConfig(t, "false")
	acks, calls, _ := stubReboot(t, nil)

	handleRebootRequest(rebootRequest{requestID: testRequestID, delayMinutes: 1, message: "x"})

	if len(*calls) != 0 {
		t.Fatalf("rebooted without permission: %v", *calls)
	}
	if len(*acks) != 1 || (*acks)[0].status != "rejected" || !strings.Contains((*acks)[0].reason, rebootIntegration) {
		t.Fatalf("acks = %+v", *acks)
	}
}

func TestRebootRejectedDuringPatchRun(t *testing.T) {
	enableReboot(t)
	acks, calls, _ := stubReboot(t, nil)
	end, ok := beginPatchRun()
	if !ok {
		t.Fatal("patch run refused")
	}
	defer end()

	handleRebootRequest(rebootRequest{requestID: testRequestID, delayMinutes: 1})

	if len(*calls) != 0 || len(*acks) != 1 || (*acks)[0].status != "rejected" {
		t.Fatalf("calls=%v acks=%+v", *calls, *acks)
	}
}

func TestRebootRejectedDuringAgentUpdate(t *testing.T) {
	enableReboot(t)
	acks, calls, _ := stubReboot(t, nil)
	patchUpdateMu.Lock()
	agentUpdating = true
	patchUpdateMu.Unlock()

	handleRebootRequest(rebootRequest{requestID: testRequestID, delayMinutes: 1})

	if len(*calls) != 0 || len(*acks) != 1 || (*acks)[0].status != "rejected" {
		t.Fatalf("calls=%v acks=%+v", *calls, *acks)
	}
}

func TestDelayedRebootSchedulesThenAcks(t *testing.T) {
	enableReboot(t)
	acks, calls, order := stubReboot(t, nil)

	handleRebootRequest(rebootRequest{requestID: testRequestID, delayMinutes: 5, message: "PatchMon: hi"})

	if len(*calls) != 1 || (*calls)[0].delay != 5 || (*calls)[0].message != "PatchMon: hi" {
		t.Fatalf("calls = %v", *calls)
	}
	if strings.Join(*order, ",") != "reboot,ack:accepted" {
		t.Fatalf("order = %v", *order)
	}
	if len(*acks) != 1 {
		t.Fatalf("acks = %+v", *acks)
	}
	// While the reboot is pending, patch runs and further reboots are refused.
	if _, ok := beginPatchRun(); ok {
		t.Fatal("patch run accepted while a reboot is pending")
	}
	if reason := beginReboot(); reason == "" {
		t.Fatal("second reboot accepted")
	}
}

func TestImmediateRebootAcksFirst(t *testing.T) {
	enableReboot(t)
	_, _, order := stubReboot(t, nil)

	handleRebootRequest(rebootRequest{requestID: testRequestID, delayMinutes: 0})

	if strings.Join(*order, ",") != "ack:accepted,reboot" {
		t.Fatalf("order = %v", *order)
	}
}

func TestFailedRebootReportsAndReleases(t *testing.T) {
	enableReboot(t)
	acks, _, _ := stubReboot(t, errors.New("no shutdown"))

	handleRebootRequest(rebootRequest{requestID: testRequestID, delayMinutes: 2})

	if len(*acks) != 1 || (*acks)[0].status != "failed" {
		t.Fatalf("acks = %+v", *acks)
	}
	end, ok := beginPatchRun()
	if !ok {
		t.Fatal("patch runs still blocked after a failed reboot")
	}
	end()
}

func TestRebootIgnoresInvalidRequestID(t *testing.T) {
	enableReboot(t)
	acks, calls, _ := stubReboot(t, nil)

	for _, id := range []string{"", "../../hosts", strings.Repeat("a", 65)} {
		handleRebootRequest(rebootRequest{requestID: id, delayMinutes: 1})
	}
	if len(*acks) != 0 || len(*calls) != 0 {
		t.Fatalf("acks=%+v calls=%v", *acks, *calls)
	}
}

func TestNewRebootRequestSanitizes(t *testing.T) {
	r := newRebootRequest(wsMsg{rebootRequestID: "id", rebootDelayMinutes: 999, rebootMessage: "-h\n\tnow\x00\x1b[31m " + strings.Repeat("x", 300)})
	if r.delayMinutes != maxRebootDelayMinutes {
		t.Fatalf("delay = %d", r.delayMinutes)
	}
	if !strings.HasPrefix(r.message, rebootMessagePrefix+"-h now") {
		t.Fatalf("message = %q", r.message)
	}
	if strings.ContainsAny(r.message, "\n\t\x00\x1b") {
		t.Fatalf("control characters kept: %q", r.message)
	}
	if n := len([]rune(r.message)); n != len(rebootMessagePrefix)+maxRebootMessageLen {
		t.Fatalf("message length = %d", n)
	}
	if got := newRebootRequest(wsMsg{rebootDelayMinutes: -3}); got.delayMinutes != 0 || got.message != rebootMessagePrefix+"Reboot requested by PatchMon" {
		t.Fatalf("defaults = %+v", got)
	}
}

// A server must not be able to switch on the local-only integrations.
func TestSyncIntegrationStatusIgnoresLocalOnly(t *testing.T) {
	m := withTestConfig(t, "false")

	syncIntegrationStatus(&models.IntegrationStatusResponse{
		Success: true,
		Integrations: map[string]bool{
			"reboot-enabled":    true,
			"ssh-proxy-enabled": true,
			"rdp-proxy-enabled": true,
			"docker":            true,
		},
	})

	for _, name := range []string{"reboot-enabled", "ssh-proxy-enabled", "rdp-proxy-enabled"} {
		if m.IsIntegrationEnabled(name) {
			t.Errorf("%s was enabled by the server", name)
		}
	}
	if !m.IsIntegrationEnabled("docker") {
		t.Error("docker should still sync from the server")
	}
	data, _ := os.ReadFile(m.GetConfigFile())
	if strings.Contains(string(data), "reboot-enabled: true") {
		t.Errorf("config.yml contains reboot-enabled: true:\n%s", data)
	}
}
