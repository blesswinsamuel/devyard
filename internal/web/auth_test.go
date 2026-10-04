package web

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"time"

	"golang.org/x/crypto/bcrypt"
)

// authHandler returns a handler with password auth enabled and a stub API.
func authHandler(t *testing.T, password string) (http.Handler, *Authenticator) {
	t.Helper()
	a, err := NewAuthenticator(string(hashOf(t, password)))
	if err != nil {
		t.Fatal(err)
	}
	h := Handler(Options{
		API:   http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusOK) }),
		Hosts: func() HostPolicy { return HostPolicy{DomainSuffix: "localhost"} },
		Auth:  a,
	})
	return h, a
}

func login(h http.Handler, password string) *httptest.ResponseRecorder {
	body := strings.NewReader(`{"password":` + strconv.Quote(password) + `}`)
	req := httptest.NewRequest(http.MethodPost, "/auth/login", body)
	req.Host = "localhost:9090"
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	return rec
}

func TestAuthenticatorRejectsNonBcryptHash(t *testing.T) {
	if _, err := NewAuthenticator("hunter2"); err == nil {
		t.Fatal("expected error for a non-bcrypt hash")
	}
	if _, err := NewAuthenticator(""); err == nil {
		t.Fatal("expected error for an empty hash")
	}
}

func TestAuthGate(t *testing.T) {
	h, _ := authHandler(t, "s3cret")

	// The API and /ws/attach need a login; the login endpoint and the SPA
	// (any other path) do not.
	for _, tc := range []struct {
		method, path string
		want         int
	}{
		{http.MethodPost, apiPath + "GetDaemon", http.StatusUnauthorized},
		{http.MethodGet, "/ws/attach", http.StatusUnauthorized},
		{http.MethodPost, "/auth/login", http.StatusUnsupportedMediaType}, // wrong content type
		{http.MethodGet, "/auth/login", http.StatusMethodNotAllowed},
		{http.MethodGet, "/some/spa/route", -1}, // served, not gated: any code but 401
	} {
		req := httptest.NewRequest(tc.method, tc.path, nil)
		req.Host = "localhost:9090"
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)
		if tc.want == -1 {
			if rec.Code == http.StatusUnauthorized {
				t.Errorf("%s %s: SPA route gated: code = 401", tc.method, tc.path)
			}
		} else if rec.Code != tc.want {
			t.Errorf("%s %s: code = %d, want %d", tc.method, tc.path, rec.Code, tc.want)
		}
	}

	// The API 401 is a Connect-protocol error body: connect clients decode
	// it as an unauthenticated error instead of a transport failure.
	req := httptest.NewRequest(http.MethodPost, apiPath+"GetDaemon", nil)
	req.Host = "localhost:9090"
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	var e struct {
		Code    string `json:"code"`
		Message string `json:"message"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &e); err != nil {
		t.Fatalf("decode error body %q: %v", rec.Body, err)
	}
	if e.Code != "unauthenticated" {
		t.Errorf("error code = %q, want unauthenticated", e.Code)
	}
}

func TestAuthLoginRoundTrip(t *testing.T) {
	h, _ := authHandler(t, "s3cret")

	if rec := login(h, "wrong"); rec.Code != http.StatusUnauthorized {
		t.Fatalf("wrong password: code = %d", rec.Code)
	}
	rec := login(h, "s3cret")
	if rec.Code != http.StatusNoContent {
		t.Fatalf("login: code = %d %s", rec.Code, rec.Body)
	}
	cookies := rec.Result().Cookies()
	if len(cookies) != 1 || cookies[0].Name != cookieName || cookies[0].Value == "" {
		t.Fatalf("login cookies: %v", cookies)
	}
	for _, c := range cookies {
		if !c.HttpOnly || c.SameSite != http.SameSiteLaxMode {
			t.Errorf("cookie %s: HttpOnly=%v SameSite=%v", c.Name, c.HttpOnly, c.SameSite)
		}
	}

	// The cookie unlocks the API.
	req := httptest.NewRequest(http.MethodPost, apiPath+"GetDaemon", nil)
	req.Host = "localhost:9090"
	req.AddCookie(cookies[0])
	r2 := httptest.NewRecorder()
	h.ServeHTTP(r2, req)
	if r2.Code != http.StatusOK {
		t.Fatalf("API with cookie: code = %d %s", r2.Code, r2.Body)
	}
}

func TestAuthCookieValidation(t *testing.T) {
	h, a := authHandler(t, "s3cret")
	call := func(cookie string) int {
		req := httptest.NewRequest(http.MethodPost, apiPath+"GetDaemon", nil)
		req.Host = "localhost:9090"
		if cookie != "" {
			req.Header.Set("Cookie", cookieName+"="+cookie)
		}
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)
		return rec.Code
	}
	tok := a.token(a.now().Add(sessionTTL))
	if call(tok) != http.StatusOK {
		t.Fatal("valid token rejected")
	}
	if call(tok+"x") != http.StatusUnauthorized { // tampered signature
		t.Fatal("tampered token accepted")
	}
	if call("9999999999.deadbeef") != http.StatusUnauthorized {
		t.Fatal("forged token accepted")
	}
	if call(tok[:strings.Index(tok, ".")]+".") != http.StatusUnauthorized {
		t.Fatal("token without a signature accepted")
	}
	expired := a.token(a.now().Add(-time.Second))
	if call(expired) != http.StatusUnauthorized {
		t.Fatal("expired token accepted")
	}

	// Tokens from another daemon (a different HMAC key) are rejected.
	other, err := NewAuthenticator(string(hashOf(t, "s3cret")))
	if err != nil {
		t.Fatal(err)
	}
	if call(other.token(a.now().Add(sessionTTL))) != http.StatusUnauthorized {
		t.Fatal("foreign-daemon token accepted")
	}
}

func hashOf(t *testing.T, password string) []byte {
	t.Helper()
	hash, err := bcrypt.GenerateFromPassword([]byte(password), bcrypt.MinCost)
	if err != nil {
		t.Fatal(err)
	}
	return hash
}
