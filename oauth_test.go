package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
)

func testOAuth(t *testing.T) *oauthServer {
	t.Helper()
	o, err := newOAuth(filepath.Join(t.TempDir(), "oauth.json"), "https://mail.example.com", "hunter2")
	if err != nil {
		t.Fatal(err)
	}
	return o
}

func TestASMetadataAdvertisesWhatClaudeRequires(t *testing.T) {
	o := testOAuth(t)
	rec := httptest.NewRecorder()
	o.handleASMetadata(rec, httptest.NewRequest(http.MethodGet, "/.well-known/oauth-authorization-server", nil))

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d", rec.Code)
	}
	var doc struct {
		Issuer               string   `json:"issuer"`
		Authorization        string   `json:"authorization_endpoint"`
		Token                string   `json:"token_endpoint"`
		Registration         string   `json:"registration_endpoint"`
		CodeChallengeMethods []string `json:"code_challenge_methods_supported"`
		GrantTypes           []string `json:"grant_types_supported"`
		TokenEndpointAuth    []string `json:"token_endpoint_auth_methods_supported"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &doc); err != nil {
		t.Fatal(err)
	}
	if doc.Issuer != "https://mail.example.com" {
		t.Errorf("issuer = %q", doc.Issuer)
	}
	if doc.Registration != "https://mail.example.com/register" {
		t.Errorf("registration_endpoint = %q", doc.Registration)
	}
	if len(doc.CodeChallengeMethods) != 1 || doc.CodeChallengeMethods[0] != "S256" {
		t.Errorf("code_challenge_methods_supported = %v, want [S256]", doc.CodeChallengeMethods)
	}
	if !containsString(doc.GrantTypes, "refresh_token") {
		t.Errorf("grant_types_supported = %v, want it to include refresh_token", doc.GrantTypes)
	}
	if !containsString(doc.TokenEndpointAuth, "none") {
		t.Errorf("token_endpoint_auth_methods_supported = %v, want it to include none for public clients", doc.TokenEndpointAuth)
	}
}

func TestOAuthStateSurvivesRestart(t *testing.T) {
	path := filepath.Join(t.TempDir(), "oauth.json")
	o, err := newOAuth(path, "https://mail.example.com", "hunter2")
	if err != nil {
		t.Fatal(err)
	}
	id, err := o.registerClient("test client", []string{"https://claude.ai/api/mcp/auth_callback"})
	if err != nil {
		t.Fatal(err)
	}

	reopened, err := newOAuth(path, "https://mail.example.com", "hunter2")
	if err != nil {
		t.Fatal(err)
	}
	if reopened.client(id) == nil {
		t.Fatal("registered client did not survive a restart")
	}
}

func containsString(list []string, s string) bool {
	for _, v := range list {
		if v == s {
			return true
		}
	}
	return false
}

func TestRegisterCreatesAPublicClient(t *testing.T) {
	o := testOAuth(t)
	body := `{"client_name":"Claude","redirect_uris":["https://claude.ai/api/mcp/auth_callback"]}`
	req := httptest.NewRequest(http.MethodPost, "/register", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	o.handleRegister(rec, req)

	if rec.Code != http.StatusCreated {
		t.Fatalf("status = %d, body = %s", rec.Code, rec.Body)
	}
	var out struct {
		ClientID     string   `json:"client_id"`
		Secret       string   `json:"client_secret"`
		RedirectURIs []string `json:"redirect_uris"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
		t.Fatal(err)
	}
	if out.ClientID == "" {
		t.Error("no client_id returned")
	}
	if out.Secret != "" {
		t.Error("a public client must not be issued a secret")
	}
	if o.client(out.ClientID) == nil {
		t.Error("client was not stored")
	}
}

func TestRegisterRejectsMissingRedirects(t *testing.T) {
	o := testOAuth(t)
	req := httptest.NewRequest(http.MethodPost, "/register", strings.NewReader(`{"client_name":"x"}`))
	rec := httptest.NewRecorder()
	o.handleRegister(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", rec.Code)
	}
}

// Claude Code uses a loopback redirect on an ephemeral port, and RFC 8252
// requires the port to be ignored when matching.
func TestRedirectAllowedIgnoresLoopbackPort(t *testing.T) {
	registered := []string{"http://localhost/callback", "https://claude.ai/api/mcp/auth_callback"}
	ok := []string{
		"http://localhost:3118/callback",
		"http://127.0.0.1:51234/callback",
		"https://claude.ai/api/mcp/auth_callback",
	}
	for _, u := range ok {
		if !redirectAllowed(registered, u) {
			t.Errorf("redirectAllowed(%q) = false, want true", u)
		}
	}
	bad := []string{
		"https://evil.example.com/callback",
		"http://localhost:3118/other",
		"https://claude.ai/api/mcp/auth_callback/../evil",
	}
	for _, u := range bad {
		if redirectAllowed(registered, u) {
			t.Errorf("redirectAllowed(%q) = true, want false", u)
		}
	}
}

// Cases beyond the brief: a registered loopback entry must not authorize a
// non-loopback host, schemes must match exactly, a path that is a prefix of
// a registered path must not match, a candidate carrying userinfo or an
// extra query string must not slip past the registered URI, an unparsable
// registered entry must be skipped rather than crash the match, and IPv6
// loopback literals count as loopback too.
func TestRedirectAllowedEdgeCases(t *testing.T) {
	registered := []string{
		"http://localhost/callback",
		"https://claude.ai/api/mcp/auth_callback",
		"://not a valid url",
	}
	ok := []string{
		"http://[::1]:9000/callback", // IPv6 loopback literal, any port
	}
	for _, u := range ok {
		if !redirectAllowed(registered, u) {
			t.Errorf("redirectAllowed(%q) = false, want true", u)
		}
	}
	bad := []string{
		"http://evil.example.com/callback",                      // loopback entry must not authorize a non-loopback host
		"ws://localhost/callback",                               // scheme must match exactly, not just "not https"
		"https://claude.ai/api/mcp/auth_callback2",              // path that is a prefix of the registered path
		"https://attacker@claude.ai/api/mcp/auth_callback",      // userinfo must not be accepted
		"https://claude.ai/api/mcp/auth_callback?next=evil.com", // extra query string must not be accepted
	}
	for _, u := range bad {
		if redirectAllowed(registered, u) {
			t.Errorf("redirectAllowed(%q) = true, want false", u)
		}
	}
}
