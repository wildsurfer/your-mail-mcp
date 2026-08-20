package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
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
// a registered path must not match, a candidate carrying userinfo, an extra
// query string or a fragment (forbidden by RFC 6749 3.1.2) must not slip
// past the registered URI, an unparsable registered entry must be skipped
// rather than crash the match, and IPv6 loopback literals count as loopback
// too.
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
		"https://claude.ai/api/mcp/auth_callback#evil",          // fragment must not be accepted (RFC 6749 3.1.2)
	}
	for _, u := range bad {
		if redirectAllowed(registered, u) {
			t.Errorf("redirectAllowed(%q) = true, want false", u)
		}
	}
}

func registeredClient(t *testing.T, o *oauthServer) string {
	t.Helper()
	id, err := o.registerClient("Claude", []string{"https://claude.ai/api/mcp/auth_callback"})
	if err != nil {
		t.Fatal(err)
	}
	return id
}

func authorizeURL(clientID string) string {
	q := url.Values{
		"response_type":         {"code"},
		"client_id":             {clientID},
		"redirect_uri":          {"https://claude.ai/api/mcp/auth_callback"},
		"state":                 {"xyz"},
		"code_challenge":        {"E9Melhoa2OwvFrEMTJguCHaoeK1t8URWbuGJSstw-cM"},
		"code_challenge_method": {"S256"},
	}
	return "/authorize?" + q.Encode()
}

func TestAuthorizeShowsAConsentForm(t *testing.T) {
	o := testOAuth(t)
	id := registeredClient(t, o)
	rec := httptest.NewRecorder()
	o.handleAuthorize(rec, httptest.NewRequest(http.MethodGet, authorizeURL(id), nil))

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d", rec.Code)
	}
	body := rec.Body.String()
	for _, want := range []string{"<form", "passphrase", id} {
		if !strings.Contains(body, want) {
			t.Errorf("consent page is missing %q", want)
		}
	}
}

func TestAuthorizeRejectsUnknownClientWithoutRedirecting(t *testing.T) {
	o := testOAuth(t)
	rec := httptest.NewRecorder()
	o.handleAuthorize(rec, httptest.NewRequest(http.MethodGet, authorizeURL("nope"), nil))
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", rec.Code)
	}
	if rec.Header().Get("Location") != "" {
		t.Error("an unknown client must never be redirected: that is an open redirect")
	}
}

func TestAuthorizeIssuesACodeOnlyWithThePassphrase(t *testing.T) {
	o := testOAuth(t)
	o.failDelay = 0
	id := registeredClient(t, o)

	form := url.Values{
		"response_type":         {"code"},
		"client_id":             {id},
		"redirect_uri":          {"https://claude.ai/api/mcp/auth_callback"},
		"state":                 {"xyz"},
		"code_challenge":        {"E9Melhoa2OwvFrEMTJguCHaoeK1t8URWbuGJSstw-cM"},
		"code_challenge_method": {"S256"},
		"passphrase":            {"wrong"},
	}
	req := httptest.NewRequest(http.MethodPost, "/authorize", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	rec := httptest.NewRecorder()
	o.handleAuthorize(rec, req)
	if rec.Code == http.StatusFound {
		t.Fatal("a wrong passphrase issued a code")
	}

	form.Set("passphrase", "hunter2")
	req = httptest.NewRequest(http.MethodPost, "/authorize", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	rec = httptest.NewRecorder()
	o.handleAuthorize(rec, req)
	if rec.Code != http.StatusFound {
		t.Fatalf("status = %d, want 302", rec.Code)
	}
	loc, err := url.Parse(rec.Header().Get("Location"))
	if err != nil {
		t.Fatal(err)
	}
	if loc.Query().Get("code") == "" {
		t.Error("no code in the redirect")
	}
	if loc.Query().Get("state") != "xyz" {
		t.Error("state was not echoed back")
	}
}

func TestAuthorizeRequiresS256(t *testing.T) {
	o := testOAuth(t)
	id := registeredClient(t, o)
	u := strings.Replace(authorizeURL(id), "code_challenge_method=S256", "code_challenge_method=plain", 1)
	rec := httptest.NewRecorder()
	o.handleAuthorize(rec, httptest.NewRequest(http.MethodGet, u, nil))
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400 for a non-S256 challenge method", rec.Code)
	}
}

// A bare GET with no query parameters at all must fail the same way an
// unknown client does: a 400 with no Location header, not a panic or a
// redirect to an empty string.
func TestAuthorizeRejectsEmptyRequestWithoutRedirecting(t *testing.T) {
	o := testOAuth(t)
	rec := httptest.NewRecorder()
	o.handleAuthorize(rec, httptest.NewRequest(http.MethodGet, "/authorize", nil))
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", rec.Code)
	}
	if rec.Header().Get("Location") != "" {
		t.Error("a parameterless request must never be redirected")
	}
}

// The state value is attacker-controlled (it round-trips through the
// client) and lands inside an HTML attribute on the consent page. html/template
// must escape it there, or a crafted state breaks out of the attribute and
// runs script in the context of the consent form the user is about to type
// their passphrase into.
func TestAuthorizeEscapesStateInThePage(t *testing.T) {
	o := testOAuth(t)
	id := registeredClient(t, o)
	q := url.Values{
		"response_type":         {"code"},
		"client_id":             {id},
		"redirect_uri":          {"https://claude.ai/api/mcp/auth_callback"},
		"state":                 {`"><script>alert(1)</script>`},
		"code_challenge":        {"E9Melhoa2OwvFrEMTJguCHaoeK1t8URWbuGJSstw-cM"},
		"code_challenge_method": {"S256"},
	}
	rec := httptest.NewRecorder()
	o.handleAuthorize(rec, httptest.NewRequest(http.MethodGet, "/authorize?"+q.Encode(), nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d", rec.Code)
	}
	if strings.Contains(rec.Body.String(), "<script>alert(1)</script>") {
		t.Error("state broke out of its attribute unescaped")
	}
}

// The same state value must survive to the redirect after a successful
// authorization, in the exact form the client sent it, not corrupted by
// whatever percent-encoding the query string requires.
func TestAuthorizeStateSurvivesRoundTripToTheRedirect(t *testing.T) {
	o := testOAuth(t)
	o.failDelay = 0
	id := registeredClient(t, o)
	const weirdState = "a b&c=d#e"

	form := url.Values{
		"response_type":         {"code"},
		"client_id":             {id},
		"redirect_uri":          {"https://claude.ai/api/mcp/auth_callback"},
		"state":                 {weirdState},
		"code_challenge":        {"E9Melhoa2OwvFrEMTJguCHaoeK1t8URWbuGJSstw-cM"},
		"code_challenge_method": {"S256"},
		"passphrase":            {"hunter2"},
	}
	req := httptest.NewRequest(http.MethodPost, "/authorize", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	rec := httptest.NewRecorder()
	o.handleAuthorize(rec, req)
	if rec.Code != http.StatusFound {
		t.Fatalf("status = %d, want 302", rec.Code)
	}
	loc, err := url.Parse(rec.Header().Get("Location"))
	if err != nil {
		t.Fatal(err)
	}
	if got := loc.Query().Get("state"); got != weirdState {
		t.Errorf("state = %q, want %q", got, weirdState)
	}
}

// Nothing about a POST is a session: every submission is independently
// validated and independently minted. Two correct submissions of the same
// form must not collide on, or reuse, the same code.
func TestAuthorizeEachPostMintsAFreshCode(t *testing.T) {
	o := testOAuth(t)
	o.failDelay = 0
	id := registeredClient(t, o)

	form := url.Values{
		"response_type":         {"code"},
		"client_id":             {id},
		"redirect_uri":          {"https://claude.ai/api/mcp/auth_callback"},
		"state":                 {"xyz"},
		"code_challenge":        {"E9Melhoa2OwvFrEMTJguCHaoeK1t8URWbuGJSstw-cM"},
		"code_challenge_method": {"S256"},
		"passphrase":            {"hunter2"},
	}
	codeOf := func() string {
		req := httptest.NewRequest(http.MethodPost, "/authorize", strings.NewReader(form.Encode()))
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		rec := httptest.NewRecorder()
		o.handleAuthorize(rec, req)
		if rec.Code != http.StatusFound {
			t.Fatalf("status = %d, want 302", rec.Code)
		}
		loc, err := url.Parse(rec.Header().Get("Location"))
		if err != nil {
			t.Fatal(err)
		}
		return loc.Query().Get("code")
	}
	first, second := codeOf(), codeOf()
	if first == "" || second == "" {
		t.Fatal("expected a code from both submissions")
	}
	if first == second {
		t.Error("two independent submissions must not share a code")
	}
}

// The consent form is where the passphrase is typed. If another site can
// frame it, a clickjacking overlay can trick a click into submitting it.
func TestAuthorizeConsentPageCannotBeFramed(t *testing.T) {
	o := testOAuth(t)
	id := registeredClient(t, o)
	rec := httptest.NewRecorder()
	o.handleAuthorize(rec, httptest.NewRequest(http.MethodGet, authorizeURL(id), nil))
	if got := rec.Header().Get("X-Frame-Options"); got != "DENY" {
		t.Errorf("X-Frame-Options = %q, want DENY", got)
	}
}
