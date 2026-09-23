// Package githubapp contains GitHub-specific transport and durable delivery
// adapters. Shared verification and commercial decisions stay in Go application.
package githubapp

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"safepatch/internal/application"
	"safepatch/internal/domain/jsonobject"
	"safepatch/internal/entitlement"
	"safepatch/internal/evidence"
	"slices"
	"strings"
	"time"
)

const MaxWebhookBytes = 1024 * 1024

type Acceptor interface {
	Accept(context.Context, string, string, application.PullRequest) error
}
type Webhook struct {
	secrets [][]byte
	accept  Acceptor
}

func NewWebhook(secrets []string, accept Acceptor) (http.Handler, error) {
	if len(secrets) == 0 || len(secrets) > 2 || accept == nil {
		return nil, application.ErrChangeWorkflow
	}
	h := &Webhook{accept: accept}
	for _, s := range secrets {
		if len(s) < 32 || len(s) > 1024 {
			return nil, application.ErrChangeWorkflow
		}
		h.secrets = append(h.secrets, []byte(s))
	}
	return h, nil
}
func (h *Webhook) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	if r.Method != "POST" {
		w.Header().Set("Allow", "POST")
		http.Error(w, "POST required", 405)
		return
	}
	raw, err := io.ReadAll(http.MaxBytesReader(w, r.Body, MaxWebhookBytes))
	if err != nil {
		http.Error(w, "invalid body", 413)
		return
	}
	signatures := r.Header.Values("X-Hub-Signature-256")
	if len(signatures) != 1 || !strings.HasPrefix(signatures[0], "sha256=") {
		http.Error(w, "invalid signature", 400)
		return
	}
	sig, err := hex.DecodeString(strings.TrimPrefix(signatures[0], "sha256="))
	if err != nil || len(sig) != 32 {
		http.Error(w, "invalid signature", 400)
		return
	}
	verified := false
	for _, secret := range h.secrets {
		mac := hmac.New(sha256.New, secret)
		mac.Write(raw)
		verified = hmac.Equal(mac.Sum(nil), sig) || verified
	}
	if !verified {
		http.Error(w, "invalid signature", 400)
		return
	}
	body, err := jsonobject.Normalize(raw, MaxWebhookBytes)
	if err != nil {
		http.Error(w, "invalid event", 400)
		return
	}
	if len(r.Header.Values("X-GitHub-Event")) != 1 {
		http.Error(w, "invalid event", 400)
		return
	}
	if r.Header.Get("X-GitHub-Event") != "pull_request" {
		w.WriteHeader(204)
		return
	}
	var payload struct {
		Action       string `json:"action"`
		Number       int64  `json:"number"`
		Installation struct {
			ID int64 `json:"id"`
		} `json:"installation"`
		Repository struct {
			ID      int64 `json:"id"`
			Private *bool `json:"private"`
		} `json:"repository"`
		PR struct {
			Number int64  `json:"number"`
			State  string `json:"state"`
			Base   struct {
				SHA  string `json:"sha"`
				Repo struct {
					ID int64 `json:"id"`
				} `json:"repo"`
			} `json:"base"`
			Head struct {
				SHA string `json:"sha"`
			} `json:"head"`
		} `json:"pull_request"`
	}
	if json.Unmarshal(body, &payload) != nil {
		http.Error(w, "invalid event", 400)
		return
	}
	if !slices.Contains([]string{"opened", "reopened", "synchronize", "ready_for_review", "edited"}, payload.Action) {
		w.WriteHeader(204)
		return
	}
	p := application.PullRequest{InstallationID: payload.Installation.ID, RepositoryID: payload.Repository.ID, Number: payload.Number, BaseSHA: payload.PR.Base.SHA, HeadSHA: payload.PR.Head.SHA}
	delivery := r.Header.Get("X-GitHub-Delivery")
	if p.Validate() != nil || payload.Repository.Private == nil || payload.PR.Base.Repo.ID != p.RepositoryID || payload.PR.Number != p.Number || payload.PR.State != "open" || !evidence.ValidIdentifier(delivery) || len(r.Header.Values("X-GitHub-Delivery")) != 1 {
		http.Error(w, "invalid event", 400)
		return
	}
	p.Private = *payload.Repository.Private
	ctx, cancel := context.WithTimeout(r.Context(), 8*time.Second)
	defer cancel()
	err = h.accept.Accept(ctx, delivery, evidence.Digest(raw), p)
	if err != nil {
		if errors.Is(err, application.ErrPermissionDenied) || errors.Is(err, entitlement.ErrNotEntitled) {
			http.Error(w, "access denied", 403)
		} else {
			http.Error(w, "delivery unavailable", 503)
		}
		return
	}
	w.WriteHeader(202)
}
