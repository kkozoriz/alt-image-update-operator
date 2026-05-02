package check

import (
	"strings"
	"testing"
)

func TestParseAltAptSimulationLogDetectsUpdates(t *testing.T) {
	tests := []struct {
		name     string
		logs     string
		evidence string
	}{
		{
			name: "inst line",
			logs: `
Reading Package Lists...
Inst glibc-core [2.35-alt1] (2.35-alt2 p10:updates [x86_64])
Conf glibc-core (2.35-alt2 p10:updates [x86_64])
`,
			evidence: "Inst glibc-core",
		},
		{
			name: "upgraded section",
			logs: `
The following packages will be upgraded:
  openssl libssl3
2 upgraded, 0 newly installed, 0 removed and 0 not upgraded.
`,
			evidence: "The following packages will be upgraded",
		},
		{
			name: "new packages section",
			logs: `
The following NEW packages will be installed:
  ca-certificates
0 upgraded, 1 newly installed, 0 removed and 0 not upgraded.
`,
			evidence: "The following NEW packages will be installed",
		},
		{
			name: "removed packages section",
			logs: `
The following packages will be REMOVED:
  obsolete-lib
0 upgraded, 0 newly installed, 1 removed and 0 not upgraded.
`,
			evidence: "The following packages will be REMOVED",
		},
		{
			name: "summary counts",
			logs: `
Reading Package Lists...
1 upgraded, 0 newly installed, 0 removed and 4 not upgraded.
`,
			evidence: "1 upgraded, 0 newly installed, 0 removed",
		},
		{
			name: "remv line",
			logs: `
Remv old-package [1.0-alt1]
`,
			evidence: "Remv old-package",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := ParseAltAptSimulationLog(tt.logs, true)

			if result.Outcome != OutcomeUpdatesAvailable {
				t.Fatalf("Outcome = %q, want %q: %#v", result.Outcome, OutcomeUpdatesAvailable, result)
			}
			if !result.UpdatesAvailable {
				t.Fatalf("UpdatesAvailable = false, want true")
			}
			if result.Reason != ReasonAptChangesDetected {
				t.Fatalf("Reason = %q, want %q", result.Reason, ReasonAptChangesDetected)
			}
			if len(result.Evidence) != 1 {
				t.Fatalf("Evidence length = %d, want 1: %#v", len(result.Evidence), result.Evidence)
			}
			if !strings.Contains(result.Evidence[0], tt.evidence) {
				t.Fatalf("Evidence = %q, want substring %q", result.Evidence[0], tt.evidence)
			}
		})
	}
}

func TestParseAltAptSimulationLogReturnsNoUpdates(t *testing.T) {
	tests := []struct {
		name string
		logs string
	}{
		{
			name: "successful no updates summary",
			logs: `
Reading Package Lists...
Building Dependency Tree...
0 upgraded, 0 newly installed, 0 removed and 0 not upgraded.
`,
		},
		{
			name: "empty successful logs",
			logs: "",
		},
		{
			name: "noisy logs without apt markers",
			logs: `
stdout: fetch metadata
stderr: W: Some index files failed to download. They have been ignored, or old ones used instead.
controller: finished check pod log collection
`,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := ParseAltAptSimulationLog(tt.logs, true)

			if result.Outcome != OutcomeNoUpdates {
				t.Fatalf("Outcome = %q, want %q: %#v", result.Outcome, OutcomeNoUpdates, result)
			}
			if result.UpdatesAvailable {
				t.Fatalf("UpdatesAvailable = true, want false")
			}
			if result.Reason != ReasonNoAptChanges {
				t.Fatalf("Reason = %q, want %q", result.Reason, ReasonNoAptChanges)
			}
			if len(result.Evidence) != 0 {
				t.Fatalf("Evidence = %#v, want empty", result.Evidence)
			}
		})
	}
}

func TestParseAltAptSimulationLogDistinguishesFailedJob(t *testing.T) {
	result := ParseAltAptSimulationLog("0 upgraded, 0 newly installed, 0 removed and 0 not upgraded.", false)

	if result.Outcome != OutcomeCheckFailed {
		t.Fatalf("Outcome = %q, want %q: %#v", result.Outcome, OutcomeCheckFailed, result)
	}
	if result.UpdatesAvailable {
		t.Fatalf("UpdatesAvailable = true, want false")
	}
	if result.Reason != ReasonCheckJobFailed {
		t.Fatalf("Reason = %q, want %q", result.Reason, ReasonCheckJobFailed)
	}
}

func TestParseAltAptSimulationLogDetectsAptErrors(t *testing.T) {
	tests := []string{
		"E: Failed to fetch http://mirror.example/alt/Sisyphus x86_64 release file",
		"Sub-process /usr/bin/dpkg returned an error code (1)",
		"apt-get dist-upgrade failed",
	}

	for _, logs := range tests {
		t.Run(logs, func(t *testing.T) {
			result := ParseAltAptSimulationLog(logs, true)

			if result.Outcome != OutcomeCheckFailed {
				t.Fatalf("Outcome = %q, want %q: %#v", result.Outcome, OutcomeCheckFailed, result)
			}
			if result.Reason != ReasonAptCommandFailed {
				t.Fatalf("Reason = %q, want %q", result.Reason, ReasonAptCommandFailed)
			}
			if len(result.Evidence) != 1 {
				t.Fatalf("Evidence length = %d, want 1: %#v", len(result.Evidence), result.Evidence)
			}
		})
	}
}

func TestParseAltAptSimulationLogHandlesMixedStdoutStderr(t *testing.T) {
	logs := `
stderr: W: Some index files failed to download. They have been ignored, or old ones used instead.
stdout: Reading Package Lists...
stdout: The following packages will be upgraded:
stdout:   apt rpm
stderr: note: simulation mode, no packages will be installed
`

	result := ParseAltAptSimulationLog(logs, true)

	if result.Outcome != OutcomeUpdatesAvailable {
		t.Fatalf("Outcome = %q, want %q: %#v", result.Outcome, OutcomeUpdatesAvailable, result)
	}
	if !result.UpdatesAvailable {
		t.Fatalf("UpdatesAvailable = false, want true")
	}
}
