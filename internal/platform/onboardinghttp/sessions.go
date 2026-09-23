package onboardinghttp

import (
	"context"
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"database/sql"
	"encoding/base64"
	"encoding/hex"
	"fmt"
	"safepatch/internal/authorization"
	flow "safepatch/internal/domain/onboarding"
	"time"
)

type Sessions struct {
	db   *sql.DB
	aead cipher.AEAD
	now  func() time.Time
}

func NewSessions(db *sql.DB, key []byte, now func() time.Time) (*Sessions, error) {
	if db == nil || len(key) != 32 || now == nil {
		return nil, flow.ErrInvalid
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, err
	}
	aead, err := cipher.NewGCM(block)
	if err != nil {
		return nil, err
	}
	return &Sessions{db, aead, now}, nil
}
func random() string {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		panic("secure randomness unavailable")
	}
	return base64.RawURLEncoding.EncodeToString(b)
}
func digest(v string) string { sum := sha256.Sum256([]byte(v)); return hex.EncodeToString(sum[:]) }
func (s *Sessions) seal(value, binding string) []byte {
	nonce := make([]byte, s.aead.NonceSize())
	if _, err := rand.Read(nonce); err != nil {
		panic("secure randomness unavailable")
	}
	return s.aead.Seal(nonce, nonce, []byte(value), []byte(binding))
}
func (s *Sessions) open(raw []byte, binding string) (string, error) {
	n := s.aead.NonceSize()
	if len(raw) < n || len(raw) > 8192 {
		return "", flow.ErrInvalid
	}
	v, err := s.aead.Open(nil, raw[:n], raw[n:], []byte(binding))
	if err != nil {
		return "", flow.ErrUnavailable
	}
	return string(v), nil
}
func (s *Sessions) begin(ctx context.Context) (state, browser, challenge string, err error) {
	state, browser, verifier := random(), random(), random()
	hash := sha256.Sum256([]byte(verifier))
	challenge = base64.RawURLEncoding.EncodeToString(hash[:])
	_, err = s.db.ExecContext(ctx, `INSERT INTO login_attempts(state_hash,browser_hash,encrypted_verifier,expires_at) VALUES($1,$2,$3,$4)`, digest(state), digest(browser), s.seal(verifier, digest(state)), s.now().Add(10*time.Minute))
	return state, browser, challenge, err
}
func (s *Sessions) consume(ctx context.Context, state, browser string) (string, error) {
	if len(state) != 43 || len(browser) != 43 {
		return "", flow.ErrInvalid
	}
	var raw []byte
	err := s.db.QueryRowContext(ctx, `DELETE FROM login_attempts WHERE state_hash=$1 AND browser_hash=$2 AND expires_at>$3 RETURNING encrypted_verifier`, digest(state), digest(browser), s.now()).Scan(&raw)
	if err != nil {
		return "", flow.ErrInvalid
	}
	return s.open(raw, digest(state))
}

// login returns a new opaque session and CSRF token. It never trusts an email or
// browser-submitted user ID. Existing sessions are not extended by a new login.
func (s *Sessions) login(ctx context.Context, subject, name, token string, expires time.Time) (string, string, time.Time, error) {
	user := "github_" + subject
	provider := "github:" + subject
	session, csrf := random(), random()
	until := s.now().Add(8 * time.Hour)
	if expires.Before(until) {
		until = expires
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return "", "", time.Time{}, err
	}
	defer tx.Rollback()
	_, err = tx.ExecContext(ctx, `INSERT INTO users(user_id,display_name,active,created_at) VALUES($1,$2,true,$3) ON CONFLICT(user_id) DO NOTHING`, user, name, s.now().UTC().Truncate(time.Microsecond))
	if err != nil {
		return "", "", time.Time{}, err
	}
	var active bool
	if err = tx.QueryRowContext(ctx, `SELECT active FROM users WHERE user_id=$1 FOR UPDATE`, user).Scan(&active); err != nil || !active {
		return "", "", time.Time{}, flow.ErrInvalid
	}
	identity, err := tx.ExecContext(ctx, `INSERT INTO login_identities(provider_subject,user_id,encrypted_token,token_expires_at) VALUES($1,$2,$3,$4) ON CONFLICT(provider_subject) DO UPDATE SET encrypted_token=EXCLUDED.encrypted_token,token_expires_at=EXCLUDED.token_expires_at WHERE login_identities.user_id=EXCLUDED.user_id`, provider, user, s.seal(token, user), expires)
	if err != nil {
		return "", "", time.Time{}, err
	}
	if count, e := identity.RowsAffected(); e != nil || count != 1 {
		return "", "", time.Time{}, flow.ErrInvalid
	}
	_, err = tx.ExecContext(ctx, `INSERT INTO login_sessions(token_hash,user_id,csrf_hash,expires_at) VALUES($1,$2,$3,$4)`, digest(session), user, digest(csrf), until)
	if err != nil {
		return "", "", time.Time{}, err
	}
	if err = tx.Commit(); err != nil {
		return "", "", time.Time{}, err
	}
	return session, csrf, until, nil
}

// Authenticate validates an explicitly supplied bearer session. This adapter
// never reads cookies; browser cookie routes retain their existing CSRF checks.
func (s *Sessions) Authenticate(ctx context.Context, credential string) (authorization.Principal, error) {
	return s.authenticate(ctx, credential, "", false)
}

func (s *Sessions) authenticate(ctx context.Context, session, csrf string, mutating bool) (authorization.Principal, error) {
	if len(session) != 43 {
		return authorization.Principal{}, flow.ErrInvalid
	}
	var id, want string
	err := s.db.QueryRowContext(ctx, `SELECT s.user_id,s.csrf_hash FROM login_sessions s JOIN users u ON u.user_id=s.user_id WHERE s.token_hash=$1 AND s.expires_at>$2 AND u.active`, digest(session), s.now()).Scan(&id, &want)
	if err != nil || (mutating && (len(csrf) != 43 || subtle.ConstantTimeCompare([]byte(want), []byte(digest(csrf))) != 1)) {
		return authorization.Principal{}, flow.ErrInvalid
	}
	p := authorization.Principal{ID: id, Kind: authorization.User}
	if p.Validate() != nil {
		return authorization.Principal{}, flow.ErrInvalid
	}
	return p, nil
}
func (s *Sessions) token(ctx context.Context, user string) (string, error) {
	var raw []byte
	err := s.db.QueryRowContext(ctx, `SELECT i.encrypted_token FROM login_identities i JOIN users u ON u.user_id=i.user_id WHERE i.user_id=$1 AND i.token_expires_at>$2 AND u.active`, user, s.now()).Scan(&raw)
	if err != nil {
		return "", flow.ErrUnavailable
	}
	return s.open(raw, user)
}
func (s *Sessions) logout(ctx context.Context, token string) error {
	_, err := s.db.ExecContext(ctx, `DELETE FROM login_sessions WHERE token_hash=$1`, digest(token))
	return err
}

// Cleanup is safe to schedule; it does not log secrets or revoke live sessions.
func (s *Sessions) Cleanup(ctx context.Context) error {
	for _, table := range []string{"login_attempts", "login_sessions"} {
		if _, err := s.db.ExecContext(ctx, fmt.Sprintf("DELETE FROM %s WHERE expires_at<=$1", table), s.now()); err != nil {
			return err
		}
	}
	return nil
}
