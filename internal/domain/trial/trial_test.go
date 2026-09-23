package trial_test

import (
	"errors"
	"testing"
	"time"

	"safepatch/internal/domain/edition"
	"safepatch/internal/domain/trial"
)

func TestOffersAndFiniteValidation(t *testing.T) {
	p, err := trial.NewPolicy("developer-2026-v2", "team-2026-v2", "", 0)
	if err != nil {
		t.Fatal(err)
	}
	for _, key := range []edition.Key{edition.Developer, edition.Team} {
		o, err := p.Offer(key)
		if err != nil || o.Days != 14 {
			t.Fatal(o, err)
		}
	}
	for _, key := range []edition.Key{edition.Business, edition.Community, edition.Enterprise, "unknown"} {
		if _, err := p.Offer(key); !errors.Is(err, trial.ErrUnavailable) {
			t.Fatal(key, err)
		}
	}
	for _, days := range []int{-1, 0, 1, 30, 365, 366} {
		p, err := trial.NewPolicy("developer-2026-v2", "team-2026-v2", "business-2026-v2", days)
		if (err == nil) != (days >= 1 && days <= 365) {
			t.Fatal(days, err)
		}
		if err == nil {
			o, _ := p.Offer(edition.Business)
			if o.Days != days {
				t.Fatal(o)
			}
		}
	}
	if _, err := trial.NewPolicy("team-2026-v2", "team-2026-v2", "", 0); err == nil {
		t.Fatal("foreign bundle")
	}
	o, _ := p.Offer(edition.Team)
	at := time.Date(2026, 9, 7, 12, 0, 0, 0, time.UTC)
	v, err := trial.New("org", o, at)
	if err != nil || v.ExpiresAt.Sub(at) != 14*trial.Day {
		t.Fatal(v, err)
	}
	for _, mutate := range []func(*trial.Trial){
		func(v *trial.Trial) { v.OrganizationID = "other" },
		func(v *trial.Trial) { v.ExpiresAt = v.ExpiresAt.Add(time.Second) },
		func(v *trial.Trial) { v.StartsAt = time.Time{} },
		func(v *trial.Trial) { v.Edition = edition.Enterprise },
	} {
		bad := v
		mutate(&bad)
		if bad.Validate() == nil {
			t.Fatal("accepted invalid trial", bad)
		}
	}
}
