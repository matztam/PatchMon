package handler

import "testing"

func TestValidatePolicyReboot(t *testing.T) {
	ok := defaultPolicyRebootSettings()
	if msg := validatePolicyReboot(ok); msg != "" {
		t.Fatalf("defaults rejected: %s", msg)
	}
	bad := []func(*policyRebootSettings){
		func(s *policyRebootSettings) { s.RebootMode = "always" },
		func(s *policyRebootSettings) { s.WindowDays = nil },
		func(s *policyRebootSettings) { s.WindowDays = []int32{7} },
		func(s *policyRebootSettings) { s.WindowStart = "25:00" },
		func(s *policyRebootSettings) { s.WindowEnd = "x" },
		func(s *policyRebootSettings) { s.DelayMinutes = 61 },
		func(s *policyRebootSettings) { s.DelayMinutes = -1 },
		func(s *policyRebootSettings) { s.MaxConcurrent = 0 },
		func(s *policyRebootSettings) { s.MaxConcurrent = 101 },
	}
	for i, mutate := range bad {
		s := defaultPolicyRebootSettings()
		mutate(&s)
		if validatePolicyReboot(s) == "" {
			t.Errorf("case %d accepted: %+v", i, s)
		}
	}
}
