package downloads

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"time"

	"safepatch/internal/application"
	"safepatch/internal/tenant"
)

type Handler struct {
	service *application.Downloads
	auth    application.Authenticator
	slots   chan struct{}
}

func NewHandler(service *application.Downloads, auth application.Authenticator) (*Handler, error) {
	if service == nil || auth == nil {
		return nil, ErrConfiguration
	}
	return &Handler{service, auth, make(chan struct{}, 16)}, nil
}
func (h *Handler) Register(mux *http.ServeMux) {
	mux.HandleFunc("GET /v1/downloads/public", h.catalog)
	mux.HandleFunc("GET /v1/downloads/public/{artifact}", h.public)
	mux.HandleFunc("POST /v1/organizations/{organization}/downloads/{artifact}", h.protected)
}
func headers(w http.ResponseWriter) {
	w.Header().Set("Cache-Control", "private, no-store")
	w.Header().Set("Pragma", "no-cache")
	w.Header().Set("Referrer-Policy", "no-referrer")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Vary", "Authorization, Cookie")
}
func failure(w http.ResponseWriter, status int) {
	headers(w)
	w.WriteHeader(status)
	_, _ = w.Write([]byte(`{"error":{"code":"DOWNLOAD_UNAVAILABLE"}}`))
}
func (h *Handler) catalog(w http.ResponseWriter, r *http.Request) {
	headers(w)
	if r.URL.RawQuery != "" {
		failure(w, 400)
		return
	}
	_ = json.NewEncoder(w).Encode(struct {
		Items []application.PublicDownload `json:"items"`
	}{h.service.PublicCatalog()})
}
func (h *Handler) issue(w http.ResponseWriter, r *http.Request, protected bool) {
	headers(w)
	if r.URL.RawQuery != "" || r.ContentLength != 0 || len(r.TransferEncoding) != 0 || !tenant.ValidID(r.PathValue("artifact")) {
		failure(w, 400)
		return
	}
	select {
	case h.slots <- struct{}{}:
		defer func() { <-h.slots }()
	default:
		failure(w, 429)
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 10*time.Second)
	defer cancel()
	var link application.DownloadLink
	var err error
	if protected {
		values := r.Header.Values("Authorization")
		if len(values) != 1 || !strings.HasPrefix(values[0], "Bearer ") || len(values[0]) > 4096 {
			failure(w, 401)
			return
		}
		credential := strings.TrimPrefix(values[0], "Bearer ")
		if credential == "" || strings.ContainsAny(credential, " \t\r\n,") {
			failure(w, 401)
			return
		}
		actor, e := h.auth.Authenticate(ctx, credential)
		if e != nil || actor.Validate() != nil {
			failure(w, 401)
			return
		}
		link, err = h.service.Protected(ctx, actor, r.PathValue("organization"), r.PathValue("artifact"))
		if err != nil {
			if errors.Is(err, application.ErrDownloadUnavailable) {
				failure(w, 503)
				return
			}
			failure(w, 403)
			return
		}
	} else {
		link, err = h.service.Public(ctx, r.PathValue("artifact"))
		if err != nil {
			if errors.Is(err, application.ErrDownloadUnavailable) {
				failure(w, 503)
				return
			}
			failure(w, 404)
			return
		}
	}
	_ = json.NewEncoder(w).Encode(link)
}
func (h *Handler) public(w http.ResponseWriter, r *http.Request)    { h.issue(w, r, false) }
func (h *Handler) protected(w http.ResponseWriter, r *http.Request) { h.issue(w, r, true) }
