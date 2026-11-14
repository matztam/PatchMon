package compliance

// fork: compliance remediation runs fix scripts from SCAP content as root, so
// it needs a local opt-in (compliance-remediation-enabled in config.yml) that
// the server cannot set. The agent wires RemediationAllowed to that setting;
// until it does, remediation is refused.

import "errors"

// ErrRemediationNotEnabled is returned when remediation was asked for but the
// host does not allow it.
var ErrRemediationNotEnabled = errors.New("compliance remediation is not enabled on this host (set integrations: compliance-remediation-enabled: true in config.yml and restart the agent)")

// RemediationAllowed reports whether this host allows compliance remediation.
var RemediationAllowed = func() bool { return false }
