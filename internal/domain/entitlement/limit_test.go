package entitlement

import (
	"encoding/json"
	"math"
	"testing"
)

func TestEntitlementLimitStatesAndKeys(t *testing.T) {
	keys := LimitKeys()
	if len(keys) != 11 {
		t.Fatal(keys)
	}
	seen := map[LimitKey]bool{}
	for _, key := range keys {
		if seen[key] {
			t.Fatal("duplicate key", key)
		}
		seen[key] = true
		for _, state := range []LimitState{Unlimited, Integer, Disabled} {
			l := EntitlementLimit{Key: key, State: state}
			if err := l.Validate(); err != nil {
				t.Fatal(err)
			}
			data, err := json.Marshal(l)
			if err != nil {
				t.Fatal(err)
			}
			var copy EntitlementLimit
			if err := json.Unmarshal(data, &copy); err != nil || copy != l {
				t.Fatal(copy, err)
			}
		}
	}
	keys[0] = "poisoned"
	if LimitKeys()[0] != LimitRepositories {
		t.Fatal("mutable catalog")
	}
	if err := (EntitlementLimit{Key: LimitRepositories, State: Integer, Value: math.MaxInt64}).Validate(); err != nil {
		t.Fatal(err)
	}
	for _, l := range []EntitlementLimit{
		{}, {Key: "unknown", State: Unlimited}, {Key: LimitRepositories, State: "UNKNOWN"},
		{Key: LimitRepositories, State: Integer, Value: -1},
		{Key: LimitRepositories, State: Unlimited, Value: 1},
		{Key: LimitRepositories, State: Disabled, Value: -1},
	} {
		if l.Validate() == nil {
			t.Fatal("invalid limit accepted", l)
		}
	}
}
