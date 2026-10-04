// Password auth for the dashboard: web.password_hash gates the API and
// /ws/attach behind a login, while the SPA and /auth/login stay reachable.
// `devyard auth` manages the hash; saving the global config from the
// settings UI must not wipe it.
package web_test

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/cookiejar"
	"os"
	"strconv"
	"strings"
	"testing"
	"time"

	"connectrpc.com/connect"
	"golang.org/x/crypto/bcrypt"

	v1 "github.com/blesswinsamuel/devyard/internal/gen/proto/devyard/v1"
	"github.com/blesswinsamuel/devyard/test/e2e/harness"
)

const testPassword = "e2e-s3cret"

// gatedSandbox returns a daemon whose global config sets password_hash.
func gatedSandbox(t *testing.T) *harness.Sandbox {
	t.Helper()
	hash, err := bcrypt.GenerateFromPassword([]byte(testPassword), bcrypt.MinCost)
	if err != nil {
		t.Fatal(err)
	}
	return harness.New(t, harness.WithGlobalConfig(fmt.Sprintf(`web:
  host: 127.0.0.1
  port: 0
  password_hash: %s
proxy:
  host: 127.0.0.1
  port: 0
`, hash)))
}

func loginClient(t *testing.T, d *harness.Daemon, password string) (*http.Client, int) {
	t.Helper()
	jar, err := cookiejar.New(nil)
	if err != nil {
		t.Fatal(err)
	}
	hc := &http.Client{Jar: jar, Timeout: harness.Scale(30 * time.Second)}
	body := strings.NewReader(`{"password":` + strconv.Quote(password) + `}`)
	r := harness.HTTPDo(hc, http.MethodPost, d.WebURL()+"/auth/login", "", jsonHeader(), body)
	return hc, r.Code
}

func TestWebPasswordGate(t *testing.T) {
	t.Parallel()
	sb := gatedSandbox(t)
	d := sb.Daemon()

	// The API 401s with a Connect-protocol error body.
	r := harness.HTTPDo(nil, http.MethodPost, d.WebURL()+rpcPath, "", jsonHeader(), strings.NewReader("{}"))
	if r.Code != http.StatusUnauthorized {
		t.Fatalf("RPC without login: %d, want 401", r.Code)
	}
	var e struct {
		Code    string `json:"code"`
		Message string `json:"message"`
	}
	if err := json.Unmarshal([]byte(r.Body), &e); err != nil || e.Code != "unauthenticated" {
		t.Fatalf("RPC error body: %q (decode err %v)", clip(r.Body), err)
	}

	// The websocket 401s before the upgrade.
	ctx, cancel := context.WithTimeout(context.Background(), harness.Scale(10*time.Second))
	defer cancel()
	if conn, resp, err := d.DialWS(ctx, harness.WSDialOpts{}); err == nil {
		_ = conn.CloseNow()
		t.Fatal("websocket accepted without login")
	} else if resp == nil || resp.StatusCode != http.StatusUnauthorized {
		code := 0
		if resp != nil {
			code = resp.StatusCode
		}
		t.Fatalf("websocket without login: status %d, want 401 (%v)", code, err)
	}

	// A wrong password does not grant a cookie.
	if _, code := loginClient(t, d, "wrong"); code != http.StatusUnauthorized {
		t.Fatalf("wrong password: %d, want 401", code)
	}

	// The right password unlocks the API (and the websocket).
	hc, code := loginClient(t, d, testPassword)
	if code != http.StatusNoContent {
		t.Fatalf("login: %d, want 204", code)
	}
	if r := harness.HTTPDo(hc, http.MethodPost, d.WebURL()+rpcPath, "", jsonHeader(), strings.NewReader("{}")); r.Code != http.StatusOK {
		t.Fatalf("RPC after login: %d %q, want 200", r.Code, clip(r.Body))
	}
	if conn, _, err := d.DialWS(ctx, harness.WSDialOpts{Client: hc}); err != nil {
		t.Fatalf("websocket after login: %v", err)
	} else {
		_ = conn.CloseNow()
	}

	// The SPA and the login endpoint are not gated.
	for _, path := range []string{"/", "/projects/x"} {
		if r := harness.HTTPDo(nil, http.MethodGet, d.WebURL()+path, "", nil, nil); r.Code == http.StatusUnauthorized {
			t.Errorf("GET %s gated: 401", path)
		}
	}
}

func TestWebPasswordCLIRoundtrip(t *testing.T) {
	t.Parallel()
	sb := harness.New(t)

	// `devyard auth set-password` writes the hash to the global config.
	sb.CLIWith(harness.RunOpts{Stdin: strings.NewReader("clipw\n")}, "auth", "set-password", "--stdin").MustSucceed(t)
	d := sb.Daemon()

	data, err := os.ReadFile(sb.GlobalConfigPath())
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), "password_hash: $2") {
		t.Fatalf("global config after set-password:\n%s", data)
	}
	hashBefore := string(data)

	// The gate is on: the API needs the new password.
	if r := harness.HTTPDo(nil, http.MethodPost, d.WebURL()+rpcPath, "", jsonHeader(), strings.NewReader("{}")); r.Code != http.StatusUnauthorized {
		t.Fatalf("RPC without login: %d, want 401", r.Code)
	}
	if hc, code := loginClient(t, d, "clipw"); code != http.StatusNoContent {
		t.Fatalf("login with CLI-set password: %d", code)
	} else if r := harness.HTTPDo(hc, http.MethodPost, d.WebURL()+rpcPath, "", jsonHeader(), strings.NewReader("{}")); r.Code != http.StatusOK {
		t.Fatalf("RPC after login: %d", r.Code)
	}

	// Saving the global config from the settings UI (over the control
	// socket) must not wipe the hash.
	ctx := d.Ctx()
	g, err := d.Client().GetGlobalConfig(ctx, connect.NewRequest(&v1.GetGlobalConfigRequest{}))
	if err != nil {
		t.Fatal(err)
	}
	cfg := g.Msg.GetConfig()
	cfg.Web.AllowedHosts = []string{"box.example"}
	if _, err := d.Client().UpdateGlobalConfig(ctx, connect.NewRequest(&v1.UpdateGlobalConfigRequest{Config: cfg})); err != nil {
		t.Fatal(err)
	}
	if data, err = os.ReadFile(sb.GlobalConfigPath()); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), "password_hash: $2") || !strings.Contains(string(data), "box.example") {
		t.Fatalf("global config after settings save lost the password hash:\n%s", data)
	}
	if string(data) == hashBefore {
		t.Fatalf("settings save did not write allowed_hosts:\n%s", data)
	}

	// `devyard auth clear` (daemon running) goes through the RPC and opens
	// the dashboard immediately, without a restart.
	sb.CLI("auth", "clear").MustSucceed(t)
	if r := harness.HTTPDo(nil, http.MethodPost, d.WebURL()+rpcPath, "", jsonHeader(), strings.NewReader("{}")); r.Code != http.StatusOK {
		t.Fatalf("RPC after clear: %d, want 200 (open dashboard, no restart)", r.Code)
	}
}

// SetWebPassword/ClearWebPassword apply without a restart: the live gate
// is swapped, and the new key invalidates every existing session cookie.
func TestWebPasswordRPC(t *testing.T) {
	t.Parallel()
	sb := gatedSandbox(t)
	d := sb.Daemon()

	// Login and grab a session cookie under the current password.
	hc, code := loginClient(t, d, testPassword)
	if code != http.StatusNoContent {
		t.Fatalf("login: %d", code)
	}
	if r := harness.HTTPDo(hc, http.MethodPost, d.WebURL()+rpcPath, "", jsonHeader(), strings.NewReader("{}")); r.Code != http.StatusOK {
		t.Fatalf("RPC after login: %d", r.Code)
	}

	// Change the password over the control socket.
	ctx := d.Ctx()
	if _, err := d.Client().SetWebPassword(ctx, connect.NewRequest(&v1.SetWebPasswordRequest{Password: "rotated"})); err != nil {
		t.Fatal(err)
	}

	// Applied immediately: the API is gated again and the old session
	// cookie no longer works (the gate has a fresh key).
	if r := harness.HTTPDo(nil, http.MethodPost, d.WebURL()+rpcPath, "", jsonHeader(), strings.NewReader("{}")); r.Code != http.StatusUnauthorized {
		t.Fatalf("RPC after SetWebPassword: %d, want 401 (immediate)", r.Code)
	}
	if r := harness.HTTPDo(hc, http.MethodPost, d.WebURL()+rpcPath, "", jsonHeader(), strings.NewReader("{}")); r.Code != http.StatusUnauthorized {
		t.Fatalf("old session cookie still works after a password change: %d, want 401", r.Code)
	}
	if _, code := loginClient(t, d, testPassword); code != http.StatusUnauthorized {
		t.Fatalf("old password still accepted: %d", code)
	}
	hc2, code := loginClient(t, d, "rotated")
	if code != http.StatusNoContent {
		t.Fatalf("login with the new password: %d", code)
	}
	if r := harness.HTTPDo(hc2, http.MethodPost, d.WebURL()+rpcPath, "", jsonHeader(), strings.NewReader("{}")); r.Code != http.StatusOK {
		t.Fatalf("RPC with the new password: %d", r.Code)
	}

	// The hash survives a settings save, and the UI-visible status flag
	// reflects it.
	g, err := d.Client().GetGlobalConfig(ctx, connect.NewRequest(&v1.GetGlobalConfigRequest{}))
	if err != nil {
		t.Fatal(err)
	}
	if !g.Msg.GetConfig().GetWeb().GetPasswordSet() {
		t.Fatal("GetGlobalConfig reports no password set")
	}
	cfg := g.Msg.GetConfig()
	cfg.Web.AllowedHosts = []string{"box.example"}
	if _, err := d.Client().UpdateGlobalConfig(ctx, connect.NewRequest(&v1.UpdateGlobalConfigRequest{Config: cfg})); err != nil {
		t.Fatal(err)
	}
	if _, code := loginClient(t, d, "rotated"); code != http.StatusNoContent {
		t.Fatalf("login after settings save: %d (hash wiped?)", code)
	}

	// Clearing opens the dashboard immediately.
	if _, err := d.Client().SetWebPassword(ctx, connect.NewRequest(&v1.SetWebPasswordRequest{Clear: true})); err != nil {
		t.Fatal(err)
	}
	if r := harness.HTTPDo(nil, http.MethodPost, d.WebURL()+rpcPath, "", jsonHeader(), strings.NewReader("{}")); r.Code != http.StatusOK {
		t.Fatalf("RPC after clear RPC: %d, want 200 (no restart)", r.Code)
	}
}
