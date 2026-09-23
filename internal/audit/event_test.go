package audit_test

import (
	"safepatch/internal/audit"
	"safepatch/internal/authorization"
	"testing"
	"time"
)

func TestAuditRejectsUnsafeFieldsAndUnknownOutcomes(t *testing.T) {
	valid := audit.Event{ID: "event", OrganizationID: "org", Actor: authorization.Principal{ID: "user", Kind: authorization.User}, Action: "proof.verify", ResourceID: "repo", Outcome: "UNKNOWN", OccurredAt: time.Now()}
	if valid.Validate() != nil {
		t.Fatal("valid rejected")
	}
	for _, field := range []string{"action", "id", "actor", "outcome", "time"} {
		e := valid
		switch field {
		case "action":
			e.Action = "token\nsecret"
		case "id":
			e.ID = "../other"
		case "actor":
			e.Actor.Kind = "LLM"
		case "outcome":
			e.Outcome = "PASS"
		case "time":
			e.OccurredAt = time.Time{}
		}
		if e.Validate() == nil {
			t.Fatal(field)
		}
	}
}
