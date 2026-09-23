package trust

import (
	"safepatch/internal/proof/quorum"
	"slices"
)

// QuorumCoverage maps satisfied proof requirements to their exact supporting
// observations. A result with contradictions or incomplete collection can never
// claim complete coverage. The kernel still re-evaluates the authoritative policy
// and checks evidence, freshness, intent and provenance independently.
func QuorumCoverage(result quorum.QuorumResult) (Coverage, error) {
	f, err := result.Fields()
	if err != nil {
		return Coverage{}, err
	}
	c := Coverage{Complete: f.Complete && f.Decision == quorum.Satisfied, Required: []string{}, Observed: map[string][]string{}}
	for _, req := range f.Requirements {
		c.Required = append(c.Required, req.ID)
		c.Observed[req.ID] = []string{}
		if req.Decision != quorum.Satisfied {
			continue
		}
		for _, o := range req.Observations {
			if o.Reason == "SUPPORTS" && slices.Contains(req.SatisfiedFamilies, o.Family) {
				c.Observed[req.ID] = append(c.Observed[req.ID], o.EvidenceID)
			}
		}
		slices.Sort(c.Observed[req.ID])
		c.Observed[req.ID] = slices.Compact(c.Observed[req.ID])
	}
	return c, nil
}
