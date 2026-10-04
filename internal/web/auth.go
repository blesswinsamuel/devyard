// Password authentication for the dashboard. It is opt-in: the gate
// exists only when web.password_hash is set (`devyard auth set-password`
// or the settings UI), and a nil *Authenticator leaves the dashboard
// open. The daemon holds the authenticator behind a getter (see
// web.Options.Auth) and can change or clear the password without a
// restart.
//
// Login (POST /auth/login, JSON {"password": ...}) checks bcrypt and sets an
// HttpOnly cookie holding an HMAC-signed expiry: no server state, and
// sessions from a previous daemon (or password) stop working after a swap.
// The API and /ws/attach require the cookie; the SPA is served either way
// and shows a login screen. Connect errors are returned as
// Connect-protocol JSON bodies so connect clients decode a proper
// unauthenticated error.
package web

import (
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"

	"golang.org/x/crypto/bcrypt"
)

const (
	// cookieName is the dashboard session cookie.
	cookieName = "devyard_auth"
	// sessionTTL is how long a login stays valid.
	sessionTTL = 30 * 24 * time.Hour
	// loginBodyLimit caps the login JSON body.
	loginBodyLimit = 1 << 20
)

// Authenticator gates the dashboard behind a password.
type Authenticator struct {
	hash []byte // bcrypt hash of the dashboard password
	key  []byte // per-daemon HMAC-SHA256 session key
	now  func() time.Time
}

// NewAuthenticator validates passwordHash (a bcrypt hash, as written by
// `devyard auth set-password`) and returns the gate.
func NewAuthenticator(passwordHash string) (*Authenticator, error) {
	hash := []byte(passwordHash)
	if _, err := bcrypt.Cost(hash); err != nil {
		return nil, fmt.Errorf("web: password_hash is not a bcrypt hash (set it with `devyard auth set-password`): %w", err)
	}
	key := make([]byte, 32)
	if _, err := rand.Read(key); err != nil {
		return nil, fmt.Errorf("web: session key: %w", err)
	}
	return &Authenticator{hash: hash, key: key, now: time.Now}, nil
}

// authWrap requires a login cookie for the API and /ws/attach, consulting
// get per request: a nil authenticator (no password configured) disables
// the gate. Everything else (the SPA and /auth/login) passes through: the
// SPA renders the login screen itself.
func authWrap(get func() *Authenticator, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if a := get(); a == nil || a.authorized(r) || !protectedPath(r.URL.Path) {
			next.ServeHTTP(w, r)
			return
		}
		if r.URL.Path == "/ws/attach" {
			http.Error(w, "devyard: login required", http.StatusUnauthorized)
			return
		}
		writeConnectError(w, http.StatusUnauthorized, connectCodeUnauthenticated, "login required (POST /auth/login)")
	})
}

// serveLoginRoute routes POST /auth/login: a 404 when no password is
// configured, the login check otherwise.
func serveLoginRoute(get func() *Authenticator) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		a := get()
		if a == nil {
			http.Error(w, "devyard: no password set", http.StatusNotFound)
			return
		}
		a.serveLogin(w, r)
	}
}

func protectedPath(p string) bool {
	return p == "/ws/attach" || strings.HasPrefix(p, apiPath)
}

// authorized reports whether the request carries a valid session cookie.
func (a *Authenticator) authorized(r *http.Request) bool {
	c, err := r.Cookie(cookieName)
	if err != nil {
		return false
	}
	expiry, sig, ok := strings.Cut(c.Value, ".")
	if !ok {
		return false
	}
	sec, err := strconv.ParseInt(expiry, 10, 64)
	if err != nil || sec <= a.now().Unix() {
		return false
	}
	mac := hmac.New(sha256.New, a.key)
	_, _ = mac.Write([]byte(expiry))
	got, err := hex.DecodeString(sig)
	if err != nil {
		return false
	}
	return hmac.Equal(mac.Sum(nil), got)
}

// token returns "<unix expiry>.<hex hmac>" for a session ending at t.
func (a *Authenticator) token(t time.Time) string {
	expiry := strconv.FormatInt(t.Unix(), 10)
	mac := hmac.New(sha256.New, a.key)
	_, _ = mac.Write([]byte(expiry))
	return expiry + "." + hex.EncodeToString(mac.Sum(nil))
}

// serveLogin handles POST /auth/login: check the password, set the cookie.
func (a *Authenticator) serveLogin(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "devyard: POST /auth/login", http.StatusMethodNotAllowed)
		return
	}
	// JSON only: a cross-origin form can't POST JSON without a CORS
	// preflight, which never succeeds here.
	if ct := r.Header.Get("Content-Type"); !strings.HasPrefix(ct, "application/json") {
		http.Error(w, "devyard: login body must be JSON", http.StatusUnsupportedMediaType)
		return
	}
	var body struct {
		Password string `json:"password"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, loginBodyLimit)).Decode(&body); err != nil {
		http.Error(w, "devyard: invalid login body", http.StatusBadRequest)
		return
	}
	if bcrypt.CompareHashAndPassword(a.hash, []byte(body.Password)) != nil {
		writeConnectError(w, http.StatusUnauthorized, connectCodeUnauthenticated, "wrong password")
		return
	}
	http.SetCookie(w, &http.Cookie{
		Name:     cookieName,
		Value:    a.token(a.now().Add(sessionTTL)),
		Path:     "/",
		MaxAge:   int(sessionTTL / time.Second),
		HttpOnly: true,
		Secure:   r.TLS != nil,
		SameSite: http.SameSiteLaxMode,
	})
	w.WriteHeader(http.StatusNoContent)
}

// connectCodeUnauthenticated is the Connect protocol error code string.
const connectCodeUnauthenticated = "unauthenticated"

// writeConnectError writes a Connect-protocol error JSON body, which
// connect clients decode into an error with that code.
func writeConnectError(w http.ResponseWriter, status int, code, msg string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(map[string]string{"code": code, "message": msg})
}
