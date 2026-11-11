package commands

import (
	"context"
	"regexp"
	"strings"
	"time"
	"unicode"

	"patchmon-agent/internal/client"
	"patchmon-agent/internal/logutil"
)

// rebootIntegration is the config.yml switch that allows the server to reboot
// this host. Like ssh-proxy-enabled it can only be set locally: applyConfig
// never touches it, so a compromised server cannot grant itself the right.
const rebootIntegration = "reboot-enabled"

const (
	maxRebootDelayMinutes = 60
	maxRebootMessageLen   = 200
	rebootMessagePrefix   = "PatchMon: "
	rebootAckTimeout      = 30 * time.Second
	// Gives the "accepted" ack time to reach the server before an immediate
	// reboot tears the network down.
	immediateRebootGrace = 2 * time.Second
)

// validRebootRequestID matches the server's UUIDs; the ID becomes part of a URL.
var validRebootRequestID = regexp.MustCompile(`^[A-Za-z0-9-]{1,64}$`)

var (
	// rebootExecutor schedules the OS reboot; replaced in tests.
	rebootExecutor = scheduleReboot
	// rebootAcker reports the outcome of a reboot request; replaced in tests.
	rebootAcker = sendRebootAck
	// rebootSleep is replaced in tests so they do not wait.
	rebootSleep = time.Sleep
)

// rebootRequest is a validated reboot command from the server.
type rebootRequest struct {
	requestID    string
	delayMinutes int
	message      string
}

func newRebootRequest(m wsMsg) rebootRequest {
	return rebootRequest{
		requestID:    m.rebootRequestID,
		delayMinutes: clampRebootDelay(m.rebootDelayMinutes),
		message:      sanitizeRebootMessage(m.rebootMessage),
	}
}

func clampRebootDelay(d int) int {
	if d < 0 {
		return 0
	}
	if d > maxRebootDelayMinutes {
		return maxRebootDelayMinutes
	}
	return d
}

// sanitizeRebootMessage keeps printable characters only, collapses whitespace
// and prefixes the result so it can never be read as a command-line option.
func sanitizeRebootMessage(s string) string {
	var b strings.Builder
	lastSpace := false
	for _, r := range s {
		switch {
		case unicode.IsSpace(r):
			if !lastSpace && b.Len() > 0 {
				b.WriteRune(' ')
			}
			lastSpace = true
		case unicode.IsPrint(r):
			b.WriteRune(r)
			lastSpace = false
		}
	}
	msg := strings.TrimSpace(b.String())
	if msg == "" {
		msg = "Reboot requested by PatchMon"
	}
	if r := []rune(msg); len(r) > maxRebootMessageLen {
		msg = string(r[:maxRebootMessageLen])
	}
	return rebootMessagePrefix + msg
}

// rebootEnabledHint tells the operator how to allow reboots on this host.
func rebootEnabledHint() string {
	return "Reboot is not enabled on this host. To allow it, edit " + cfgManager.GetConfigFile() +
		" and set:\n\nintegrations:\n    " + rebootIntegration + ": true\n\n" +
		"then restart the agent. This cannot be pushed from the server for security reasons."
}

// handleRebootRequest checks the local permission and running work, reports the
// decision to the server and, if accepted, schedules the reboot.
func handleRebootRequest(req rebootRequest) {
	log := logger.WithField("reboot_request_id", logutil.Sanitize(req.requestID))
	if !validRebootRequestID.MatchString(req.requestID) {
		log.Warn("reboot request with missing or invalid request_id ignored")
		return
	}
	if !cfgManager.IsIntegrationEnabled(rebootIntegration) {
		log.Warn("Reboot requested but " + rebootIntegration + " is not set in config.yml")
		ackReboot(req.requestID, "rejected", rebootEnabledHint())
		return
	}
	if reason := beginReboot(); reason != "" {
		log.Warn("Reboot refused: " + reason)
		ackReboot(req.requestID, "rejected", reason)
		return
	}

	if req.delayMinutes > 0 {
		if err := rebootExecutor(req.delayMinutes, req.message); err != nil {
			endReboot()
			log.WithError(err).Error("Failed to schedule reboot")
			ackReboot(req.requestID, "failed", "could not schedule reboot: "+err.Error())
			return
		}
		log.WithField("delay_minutes", req.delayMinutes).Info("Reboot scheduled")
		ackReboot(req.requestID, "accepted", "")
		return
	}

	ackReboot(req.requestID, "accepted", "")
	rebootSleep(immediateRebootGrace)
	log.Info("Rebooting now")
	if err := rebootExecutor(0, req.message); err != nil {
		endReboot()
		log.WithError(err).Error("Failed to reboot")
		ackReboot(req.requestID, "failed", "could not reboot: "+err.Error())
	}
}

func ackReboot(requestID, status, reason string) {
	ctx, cancel := context.WithTimeout(context.Background(), rebootAckTimeout)
	defer cancel()
	if err := rebootAcker(ctx, requestID, status, reason); err != nil {
		logger.WithError(err).WithField("status", status).Warn("Failed to report reboot status to server")
	}
}

func sendRebootAck(ctx context.Context, requestID, status, reason string) error {
	return client.New(cfgManager, logger).SendRebootAck(ctx, requestID, status, reason)
}

// reportRebootCapability tells the server whether this host allows reboots,
// so the UI and the policy scheduler know before sending a request.
func reportRebootCapability(ctx context.Context) {
	enabled := cfgManager.IsIntegrationEnabled(rebootIntegration)
	ctx, cancel := context.WithTimeout(ctx, rebootAckTimeout)
	defer cancel()
	if err := client.New(cfgManager, logger).SendRebootCapability(ctx, enabled); err != nil {
		logger.WithError(err).Debug("Failed to report reboot capability (server may not support it)")
		return
	}
	logger.WithField("enabled", enabled).Debug("Reboot capability reported")
}
