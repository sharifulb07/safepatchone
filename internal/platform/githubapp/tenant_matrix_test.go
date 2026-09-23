package githubapp

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"encoding/json"
	"errors"
	"io"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"safepatch/internal/application"
	"safepatch/internal/authorization"
	"safepatch/internal/domain/edition"
	"safepatch/internal/domain/feature"
	"safepatch/internal/entitlement"
	"safepatch/internal/tenant"
)

type tenantSQL struct {
	t           *testing.T
	org, poison string
	queries     int
}

func (c *tenantSQL) Prepare(string) (driver.Stmt, error) {
	return nil, errors.New("unexpected prepare")
}
func (c *tenantSQL) Close() error              { return nil }
func (c *tenantSQL) Begin() (driver.Tx, error) { return nil, errors.New("unexpected transaction") }
func (c *tenantSQL) QueryContext(_ context.Context, q string, args []driver.NamedValue) (driver.Rows, error) {
	c.queries++
	if strings.Contains(q, "FROM github_pr_bindings") {
		if !strings.Contains(q, "g.installation_id=$1 AND g.repository_id=$2") || len(args) != 2 || args[0].Value != int64(7) || args[1].Value != "gh_2" {
			c.t.Fatal("webhook lost installation/repository binding", q, args)
		}
		return &tenantRows{}, nil
	}
	if !strings.Contains(q, "WHERE job_id=$1 AND organization_id=$5") || len(args) != 5 || args[4].Value != c.org {
		c.t.Fatal("unscoped queue claim", q, args)
	}
	if c.poison != "" {
		return &tenantRows{raw: c.poison}, nil
	}
	return &tenantRows{}, nil
}

type tenantRows struct{ raw string }

func (r *tenantRows) Columns() []string { return []string{"canonical_job"} }
func (r *tenantRows) Close() error      { return nil }
func (r *tenantRows) Next(dst []driver.Value) error {
	if r.raw == "" {
		return io.EOF
	}
	dst[0] = r.raw
	r.raw = ""
	return nil
}

type tenantConnector struct{ c *tenantSQL }

func (c tenantConnector) Connect(context.Context) (driver.Conn, error) { return c.c, nil }
func (c tenantConnector) Driver() driver.Driver                        { return tenantDriver{} }

type tenantDriver struct{}

func (tenantDriver) Open(string) (driver.Conn, error) { return nil, errors.New("use connector") }

type neverAccess struct{ t *testing.T }

func (a neverAccess) Resolve(context.Context, authorization.Principal, tenant.Scope) (tenant.Scope, error) {
	a.t.Fatal("unknown GitHub binding reached tenant admission")
	return tenant.Scope{}, errors.New("denied")
}
func (a neverAccess) Has(context.Context, string, feature.Key) (bool, error) {
	a.t.Fatal("unknown binding reached entitlement")
	return true, nil
}
func (a neverAccess) Limit(context.Context, string, feature.Key) (int64, error) {
	return 0, errors.New("unused")
}
func (a neverAccess) ResolveFeature(context.Context, string, feature.Key) (entitlement.Resolution, error) {
	return entitlement.Resolution{}, errors.New("unused")
}

type webhookGrants struct{ neverAccess }

func (a webhookGrants) Resolve(context.Context, string, feature.Key) (entitlement.Resolution, error) {
	return entitlement.Resolution{}, errors.New("unused")
}
func (a neverAccess) Allowed(context.Context, authorization.Principal, string, authorization.Key) (bool, error) {
	a.t.Fatal("unknown binding reached permission")
	return true, nil
}

func TestSignedWebhookCannotMixTenantsInstallationAndRepository(t *testing.T) {
	for _, a := range edition.Editions() {
		for _, b := range edition.Editions() {
			t.Run(a.Name+"_to_"+b.Name, func(t *testing.T) {
				c := &tenantSQL{t: t}
				db := sql.OpenDB(tenantConnector{c})
				defer db.Close()
				store, _ := NewStore(db, time.Now)
				deny := neverAccess{t}
				admission, _ := application.NewPullRequestAdmission(store, deny, webhookGrants{deny}, deny)
				h, _ := NewWebhook([]string{webhookSecret}, admission)
				// An authentic event still needs an exact stored installation/repository
				// pair. Sender names and injected edition/org labels confer no authority.
				raw := strings.ReplaceAll(event(), `"id":1`, `"id":2`)
				raw = strings.Replace(raw, `"action":"opened"`, `"action":"opened","organization_id":"victim_`+string(b.Key)+`","edition":"`+a.Name+`"`, 1)
				r := httptest.NewRequest("POST", "/webhook", strings.NewReader(raw))
				r.Header.Set("X-GitHub-Event", "pull_request")
				r.Header.Set("X-GitHub-Delivery", "delivery")
				r.Header.Set("X-Hub-Signature-256", signature(raw))
				w := httptest.NewRecorder()
				h.ServeHTTP(w, r)
				if w.Code != 503 || c.queries != 1 {
					t.Fatal("mixed installation accepted", w.Code, c.queries)
				}
			})
		}
	}
}
func TestGitHubQueueClaimRejectsForeignCanonicalJobAcrossEditions(t *testing.T) {
	for _, a := range edition.Editions() {
		for _, b := range edition.Editions() {
			t.Run(a.Name+"_to_"+b.Name, func(t *testing.T) {
				j := application.PullRequestJob{ID: "same_id", Binding: application.PullRequestBinding{OrganizationID: "b_" + string(b.Key), RepositoryID: "gh_1", PolicyHash: strings.Repeat("c", 64), Actor: authorization.Principal{ID: "owner", Kind: authorization.User}}, Request: application.PullRequest{InstallationID: 7, RepositoryID: 1, Number: 2, BaseSHA: strings.Repeat("a", 40), HeadSHA: strings.Repeat("b", 40)}}
				raw, _ := json.Marshal(j)
				c := &tenantSQL{t: t, org: "a_" + string(a.Key), poison: string(raw)}
				db := sql.OpenDB(tenantConnector{c})
				defer db.Close()
				s, _ := NewStore(db, time.Now)
				if _, _, err := s.claim(t.Context(), c.org, j.ID, "lease"); err == nil {
					t.Fatal("foreign canonical job accepted")
				}
			})
		}
	}
}
func TestWebhookRejectsAmbiguousEventHeader(t *testing.T) {
	a := &accepting{}
	h, _ := NewWebhook([]string{webhookSecret}, a)
	raw := event()
	r := httptest.NewRequest("POST", "/webhook", strings.NewReader(raw))
	r.Header.Add("X-GitHub-Event", "pull_request")
	r.Header.Add("X-GitHub-Event", "installation")
	r.Header.Set("X-GitHub-Delivery", "delivery")
	r.Header.Set("X-Hub-Signature-256", signature(raw))
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	if w.Code != 400 || a.calls != 0 {
		t.Fatal("ambiguous routing header accepted")
	}
}
