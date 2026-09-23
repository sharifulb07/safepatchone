package trust

import "safepatch/internal/evidence"

// Decision is a diagnostic CI decision, never production authorization.
type Decision string

const (
	Pass    Decision = "PASS"
	Fail    Decision = "FAIL"
	Unknown Decision = "UNKNOWN"
)

func Decide(complete bool, statuses ...evidence.Status) Decision {
	if !complete || len(statuses) == 0 {
		return Unknown
	}
	failed := false
	for _, status := range statuses {
		switch status {
		case evidence.StatusPass:
		case evidence.StatusFail:
			failed = true
		default:
			return Unknown
		}
	}
	if failed {
		return Fail
	}
	return Pass
}
