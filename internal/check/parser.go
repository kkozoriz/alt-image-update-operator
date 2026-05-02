package check

import (
	"fmt"
	"regexp"
	"strconv"
	"strings"
)

// Outcome is the conservative classification of an ALT apt simulation check.
type Outcome string

const (
	// OutcomeUpdatesAvailable means apt reported package changes to apply.
	OutcomeUpdatesAvailable Outcome = "UpdatesAvailable"
	// OutcomeNoUpdates means the check completed and no package-change markers were found.
	OutcomeNoUpdates Outcome = "NoUpdates"
	// OutcomeCheckFailed means the Kubernetes Job or apt command failed.
	OutcomeCheckFailed Outcome = "CheckFailed"
)

const (
	ReasonAptChangesDetected = "AptChangesDetected"
	ReasonNoAptChanges       = "NoAptChanges"
	ReasonCheckJobFailed     = "CheckJobFailed"
	ReasonAptCommandFailed   = "AptCommandFailed"
)

var (
	instLinePattern = regexp.MustCompile(`^(Inst|Remv)\s+\S+`)
	sectionPatterns = []*regexp.Regexp{
		regexp.MustCompile(`(?i)^The following packages? will be upgraded:?$`),
		regexp.MustCompile(`(?i)^The following NEW packages? will be installed:?$`),
		regexp.MustCompile(`(?i)^The following packages? will be REMOVED:?$`),
	}
	summaryPattern   = regexp.MustCompile(`(?i)\b(\d+)\s+upgraded,\s+(\d+)\s+newly\s+installed,\s+(\d+)\s+removed\b`)
	aptErrorPatterns = []*regexp.Regexp{
		regexp.MustCompile(`^E:\s+`),
		regexp.MustCompile(`(?i)\bsub-process\b.*\breturned an error code\b`),
		regexp.MustCompile(`(?i)\bapt-get\b.*\bfailed\b`),
	}
)

// Result contains the parser outcome and the log evidence that produced it.
type Result struct {
	Outcome          Outcome
	UpdatesAvailable bool
	Reason           string
	Message          string
	Evidence         []string
}

// ParseAltAptSimulationLog classifies apt-get -s dist-upgrade output.
//
// logs should contain the combined stdout/stderr captured from the Check Job.
// jobSucceeded must reflect the Kubernetes Job result; a failed Job is not
// treated as "no updates", even if its logs have no package-change markers.
func ParseAltAptSimulationLog(logs string, jobSucceeded bool) Result {
	if !jobSucceeded {
		return Result{
			Outcome: OutcomeCheckFailed,
			Reason:  ReasonCheckJobFailed,
			Message: "ALT apt simulation job did not complete successfully",
		}
	}

	for _, line := range splitLogLines(logs) {
		trimmed := normalizeLogLine(line)
		if trimmed == "" {
			continue
		}

		if matchesAny(aptErrorPatterns, trimmed) {
			return Result{
				Outcome:  OutcomeCheckFailed,
				Reason:   ReasonAptCommandFailed,
				Message:  "ALT apt simulation output contains an apt error",
				Evidence: []string{trimmed},
			}
		}

		if instLinePattern.MatchString(trimmed) || matchesAny(sectionPatterns, trimmed) {
			return updatesResult(trimmed)
		}

		if evidence, ok := summaryWithChanges(trimmed); ok {
			return updatesResult(evidence)
		}
	}

	return Result{
		Outcome:          OutcomeNoUpdates,
		UpdatesAvailable: false,
		Reason:           ReasonNoAptChanges,
		Message:          "ALT apt simulation completed without package changes",
	}
}

func splitLogLines(logs string) []string {
	return strings.Split(strings.ReplaceAll(logs, "\r\n", "\n"), "\n")
}

func normalizeLogLine(line string) string {
	trimmed := strings.TrimSpace(line)
	lower := strings.ToLower(trimmed)
	for _, prefix := range []string{"stdout:", "stderr:"} {
		if strings.HasPrefix(lower, prefix) {
			return strings.TrimSpace(trimmed[len(prefix):])
		}
	}
	return trimmed
}

func matchesAny(patterns []*regexp.Regexp, line string) bool {
	for _, pattern := range patterns {
		if pattern.MatchString(line) {
			return true
		}
	}
	return false
}

func summaryWithChanges(line string) (string, bool) {
	matches := summaryPattern.FindStringSubmatch(line)
	if len(matches) == 0 {
		return "", false
	}

	for _, count := range matches[1:] {
		parsed, err := strconv.Atoi(count)
		if err != nil {
			return "", false
		}
		if parsed > 0 {
			return fmt.Sprintf("%s upgraded, %s newly installed, %s removed", matches[1], matches[2], matches[3]), true
		}
	}

	return "", false
}

func updatesResult(evidence string) Result {
	return Result{
		Outcome:          OutcomeUpdatesAvailable,
		UpdatesAvailable: true,
		Reason:           ReasonAptChangesDetected,
		Message:          "ALT apt simulation reported package changes",
		Evidence:         []string{evidence},
	}
}
