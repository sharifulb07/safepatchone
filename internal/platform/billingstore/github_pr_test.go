package billingstore

import (
	"context"
	"database/sql/driver"
	"encoding/json"
	"errors"
	"safepatch/internal/application"
	"safepatch/internal/authorization"
	"safepatch/internal/domain/feature"
	"safepatch/internal/entitlement"
	"safepatch/internal/platform/githubapp"
	"safepatch/internal/tenant"
	"safepatch/internal/trust"
	"strings"
	"testing"
)

func prJob() application.PullRequestJob {
	return application.PullRequestJob{ID: "pr_" + strings.Repeat("a", 64), Binding: application.PullRequestBinding{OrganizationID: "org", RepositoryID: "gh_1", PolicyHash: strings.Repeat("d", 64), Actor: authorization.Principal{ID: "user", Kind: authorization.User}}, Request: application.PullRequest{InstallationID: 7, RepositoryID: 1, Number: 2, BaseSHA: strings.Repeat("a", 40), HeadSHA: strings.Repeat("b", 40)}}
}
func TestGitHubDeliveryReceiptIsAtomicAndRejectsIdentityReuse(t *testing.T) {
	j := prJob()
	raw, _ := json.Marshal(j)
	hash := strings.Repeat("c", 64)
	for _, mode := range []string{"new", "duplicate", "collision", "transaction_failure", "queue_limit"} {
		t.Run(mode, func(t *testing.T) {
			steps := []step{{query: "BEGIN"}, {query: "FOR UPDATE", args: []driver.Value{"org"}, rows: [][]driver.Value{{true}}}}
			lookup := step{query: "SELECT payload_hash,job_id", args: []driver.Value{"delivery"}}
			if mode == "duplicate" || mode == "collision" {
				old := hash
				if mode == "collision" {
					old = strings.Repeat("d", 64)
				}
				lookup.rows = [][]driver.Value{{old, j.ID}}
			}
			steps = append(steps, lookup)
			switch mode {
			case "duplicate":
				steps = append(steps, step{query: "COMMIT"})
			case "collision":
				steps = append(steps, step{query: "ROLLBACK"})
			default:
				steps = append(steps, step{query: "d.received_at", rows: [][]driver.Value{{int64(0)}}})
				count := int64(0)
				if mode == "queue_limit" {
					count = 100
				}
				steps = append(steps, step{query: "status<>'DONE'", rows: [][]driver.Value{{count}}})
				if mode == "queue_limit" {
					steps = append(steps, step{query: "ROLLBACK"})
				} else {
					steps = append(steps, step{query: "INSERT INTO github_pr_jobs", args: []driver.Value{j.ID, "org", "gh_1", string(raw)}}, step{query: "SELECT canonical_job", rows: [][]driver.Value{{string(raw)}}})
					insert := step{query: "INSERT INTO github_pr_deliveries", args: []driver.Value{"delivery", hash, j.ID, stamp()}}
					end := "COMMIT"
					if mode == "transaction_failure" {
						insert.err = errors.New("write failed")
						end = "ROLLBACK"
					}
					steps = append(steps, insert, step{query: end})
				}
			}
			db, _ := scripted(t, steps...)
			store, _ := githubapp.NewStore(db.db, stamp)
			err := store.Accept(t.Context(), "delivery", hash, j)
			if (err == nil) != (mode == "new" || mode == "duplicate") {
				t.Fatal(err)
			}
		})
	}
}

type prAccess struct{ allowed bool }

func (a prAccess) Resolve(_ context.Context, _ authorization.Principal, s tenant.Scope) (tenant.Scope, error) {
	return s, nil
}
func (a prAccess) Allowed(context.Context, authorization.Principal, string, authorization.Key) (bool, error) {
	return a.allowed, nil
}

type prGrants struct{}

func (prGrants) Has(context.Context, string, feature.Key) (bool, error) { return true, nil }
func (prGrants) Limit(context.Context, string, feature.Key) (int64, error) {
	return 0, entitlement.ErrLimitUnspecified
}
func (prGrants) Resolve(context.Context, string, feature.Key) (entitlement.Resolution, error) {
	return entitlement.Resolution{}, nil
}

type prClient struct {
	decision trust.Decision
	fail     bool
	calls    int
}

func (c *prClient) Current(_ context.Context, p application.PullRequest) (application.PullRequest, bool, error) {
	return p, true, nil
}
func (c *prClient) Publish(_ context.Context, _ application.PullRequestJob, r application.ChangeResult) error {
	c.calls++
	c.decision = r.Decision
	if c.fail {
		return errors.New("GitHub unavailable")
	}
	return nil
}
func TestGitHubCheckRetryDoesNotRerunVerificationAndRechecksPermission(t *testing.T) {
	for _, mode := range []string{"retry", "check_failure", "permission_revoked"} {
		t.Run(mode, func(t *testing.T) {
			j := prJob()
			raw, _ := json.Marshal(j)
			result, _ := json.Marshal(application.ChangeResult{ChangeID: j.Change().ID(), Decision: trust.Pass, EvidenceReference: "immutable"})
			steps := []step{{query: "organization_id=$5"}, {query: "BEGIN"}, {query: "organization_id=$2 AND status='READY' FOR UPDATE", args: []driver.Value{j.ID, "org"}, rows: [][]driver.Value{{string(raw), string(result)}}}, {query: "g.installation_id=$1", args: []driver.Value{int64(7), "gh_1"}, rows: [][]driver.Value{{"org", "gh_1", "user", strings.Repeat("d", 64)}}}}
			if mode == "check_failure" {
				steps = append(steps, step{query: "ROLLBACK"})
			} else {
				steps = append(steps, step{query: "organization_id=$2 AND status='READY'", args: []driver.Value{j.ID, "org"}}, step{query: "COMMIT"})
			}
			db, _ := scripted(t, steps...)
			store, _ := githubapp.NewStore(db.db, stamp)
			access := prAccess{allowed: mode != "permission_revoked"}
			admission, _ := application.NewPullRequestAdmission(store, access, prGrants{}, access)
			client := &prClient{fail: mode == "check_failure"}
			worker, _ := githubapp.NewWorker(store, admission, client, func(context.Context, application.PullRequestJob) (*application.ChangeWorkflow, error) {
				t.Fatal("verification rerun during Check delivery")
				return nil, nil
			})
			err := worker.Process(t.Context(), j.Binding.OrganizationID, j.ID)
			if (err != nil) != (mode == "check_failure") || client.calls != 1 {
				t.Fatal(err, client.calls)
			}
			if mode == "permission_revoked" && client.decision != trust.Unknown {
				t.Fatal("revocation published success")
			}
		})
	}
}
