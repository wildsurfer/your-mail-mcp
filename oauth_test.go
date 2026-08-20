package main

import (
	"context"
	"crypto/sha256"
	"crypto/tls"
	"encoding/base64"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
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

// TestRegisterRejectsPastCap covers I5: /register is unauthenticated by
// necessity, and each registration appends to the state file and fsyncs it,
// with no cap and no rate limit — an unbounded flood grows a file on the
// same volume as the Xapian index and stalls every authenticated request
// behind the write lock (the same o.mu validAccessToken takes).
func TestRegisterRejectsPastCap(t *testing.T) {
	o := testOAuth(t)
	body := `{"client_name":"x","redirect_uris":["https://claude.ai/api/mcp/auth_callback"]}`

	for i := 0; i < maxRegisteredClients; i++ {
		rec := httptest.NewRecorder()
		o.handleRegister(rec, httptest.NewRequest(http.MethodPost, "/register", strings.NewReader(body)))
		if rec.Code != http.StatusCreated {
			t.Fatalf("registration %d: status = %d, want 201", i, rec.Code)
		}
	}

	rec := httptest.NewRecorder()
	o.handleRegister(rec, httptest.NewRequest(http.MethodPost, "/register", strings.NewReader(body)))
	if rec.Code != http.StatusTooManyRequests {
		t.Fatalf("registration past the cap: status = %d, want 429", rec.Code)
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

// TestFailedPassphraseIsLogged covers M6: a sustained guessing run against
// an internet-exposed deployment was invisible to the operator, since a
// wrong passphrase was silently re-rendered with nothing written anywhere.
func TestFailedPassphraseIsLogged(t *testing.T) {
	o := testOAuth(t)
	o.failDelay = 0
	id := registeredClient(t, o)

	form := url.Values{
		"response_type":         {"code"},
		"client_id":             {id},
		"redirect_uri":          {"https://claude.ai/api/mcp/auth_callback"},
		"code_challenge":        {"E9Melhoa2OwvFrEMTJguCHaoeK1t8URWbuGJSstw-cM"},
		"code_challenge_method": {"S256"},
		"passphrase":            {"wrong"},
	}
	req := httptest.NewRequest(http.MethodPost, "/authorize", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.RemoteAddr = "203.0.113.5:12345"

	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	old := os.Stderr
	os.Stderr = w
	o.handleAuthorize(httptest.NewRecorder(), req)
	w.Close()
	os.Stderr = old

	out, err := io.ReadAll(r)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(out), "203.0.113.5") {
		t.Errorf("a failed passphrase attempt was not logged with the remote address: %s", out)
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

const verifier = "dBjftJeZ4CVP-mB92K27uhbUJU1p1r_wW1gFWFOEjXk"

func challengeFor(v string) string {
	sum := sha256.Sum256([]byte(v))
	return base64.RawURLEncoding.EncodeToString(sum[:])
}

// issueCode drives the authorize endpoint and returns the code it produced.
func issueCode(t *testing.T, o *oauthServer, clientID string) string {
	t.Helper()
	form := url.Values{
		"response_type":         {"code"},
		"client_id":             {clientID},
		"redirect_uri":          {"https://claude.ai/api/mcp/auth_callback"},
		"code_challenge":        {challengeFor(verifier)},
		"code_challenge_method": {"S256"},
		"passphrase":            {"hunter2"},
	}
	req := httptest.NewRequest(http.MethodPost, "/authorize", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	rec := httptest.NewRecorder()
	o.handleAuthorize(rec, req)
	if rec.Code != http.StatusFound {
		t.Fatalf("authorize status = %d", rec.Code)
	}
	loc, err := url.Parse(rec.Header().Get("Location"))
	if err != nil {
		t.Fatal(err)
	}
	return loc.Query().Get("code")
}

func postToken(t *testing.T, o *oauthServer, form url.Values) (int, map[string]any) {
	t.Helper()
	req := httptest.NewRequest(http.MethodPost, "/token", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	rec := httptest.NewRecorder()
	o.handleToken(rec, req)
	var out map[string]any
	_ = json.Unmarshal(rec.Body.Bytes(), &out)
	return rec.Code, out
}

func TestTokenExchangeAndRefreshRotation(t *testing.T) {
	o := testOAuth(t)
	o.failDelay = 0
	id := registeredClient(t, o)
	code := issueCode(t, o, id)

	status, out := postToken(t, o, url.Values{
		"grant_type":    {"authorization_code"},
		"code":          {code},
		"client_id":     {id},
		"redirect_uri":  {"https://claude.ai/api/mcp/auth_callback"},
		"code_verifier": {verifier},
	})
	if status != http.StatusOK {
		t.Fatalf("token status = %d, body = %v", status, out)
	}
	access, _ := out["access_token"].(string)
	refresh, _ := out["refresh_token"].(string)
	if access == "" || refresh == "" {
		t.Fatalf("missing tokens: %v", out)
	}
	if !o.validAccessToken(access) {
		t.Error("the issued access token does not validate")
	}

	// A code is single use.
	status, out = postToken(t, o, url.Values{
		"grant_type":    {"authorization_code"},
		"code":          {code},
		"client_id":     {id},
		"redirect_uri":  {"https://claude.ai/api/mcp/auth_callback"},
		"code_verifier": {verifier},
	})
	if status != http.StatusBadRequest || out["error"] != "invalid_grant" {
		t.Errorf("replayed code: status %d, error %v; want 400 invalid_grant", status, out["error"])
	}

	// Refresh rotates: the new token works, the old one does not.
	status, out = postToken(t, o, url.Values{
		"grant_type":    {"refresh_token"},
		"refresh_token": {refresh},
		"client_id":     {id},
	})
	if status != http.StatusOK {
		t.Fatalf("refresh status = %d, body = %v", status, out)
	}
	rotated, _ := out["refresh_token"].(string)
	if rotated == "" || rotated == refresh {
		t.Fatalf("refresh token was not rotated: %v", out)
	}
	status, out = postToken(t, o, url.Values{
		"grant_type":    {"refresh_token"},
		"refresh_token": {refresh},
		"client_id":     {id},
	})
	if status != http.StatusBadRequest || out["error"] != "invalid_grant" {
		t.Errorf("the old refresh token still works: status %d, %v", status, out)
	}
}

func TestTokenRejectsAWrongVerifier(t *testing.T) {
	o := testOAuth(t)
	o.failDelay = 0
	id := registeredClient(t, o)
	code := issueCode(t, o, id)

	status, out := postToken(t, o, url.Values{
		"grant_type":    {"authorization_code"},
		"code":          {code},
		"client_id":     {id},
		"redirect_uri":  {"https://claude.ai/api/mcp/auth_callback"},
		"code_verifier": {"not-the-verifier"},
	})
	if status != http.StatusBadRequest || out["error"] != "invalid_grant" {
		t.Fatalf("PKCE was not enforced: status %d, %v", status, out)
	}
}

func TestTokenRejectsAnEmptyVerifier(t *testing.T) {
	o := testOAuth(t)
	o.failDelay = 0
	id := registeredClient(t, o)
	code := issueCode(t, o, id)

	status, out := postToken(t, o, url.Values{
		"grant_type":   {"authorization_code"},
		"code":         {code},
		"client_id":    {id},
		"redirect_uri": {"https://claude.ai/api/mcp/auth_callback"},
		// code_verifier omitted entirely
	})
	if status != http.StatusBadRequest || out["error"] != "invalid_grant" {
		t.Fatalf("empty verifier: status %d, %v", status, out)
	}
}

func TestTokenRejectsAnExpiredCode(t *testing.T) {
	o := testOAuth(t)
	o.failDelay = 0
	id := registeredClient(t, o)
	code := issueCode(t, o, id)

	o.mu.Lock()
	ac := o.codes[code]
	ac.expires = time.Now().Add(-time.Second)
	o.codes[code] = ac
	o.mu.Unlock()

	status, out := postToken(t, o, url.Values{
		"grant_type":    {"authorization_code"},
		"code":          {code},
		"client_id":     {id},
		"redirect_uri":  {"https://claude.ai/api/mcp/auth_callback"},
		"code_verifier": {verifier},
	})
	if status != http.StatusBadRequest || out["error"] != "invalid_grant" {
		t.Fatalf("expired code: status %d, %v", status, out)
	}
}

// A code is scoped to the client that requested it. Presenting it with a
// different (also registered) client_id must fail even with the right
// verifier and redirect_uri: otherwise one client could redeem a code that
// was never issued to it.
func TestTokenRejectsCodeRedeemedByAnotherClient(t *testing.T) {
	o := testOAuth(t)
	o.failDelay = 0
	id := registeredClient(t, o)
	otherID := registeredClient(t, o)
	code := issueCode(t, o, id)

	status, out := postToken(t, o, url.Values{
		"grant_type":    {"authorization_code"},
		"code":          {code},
		"client_id":     {otherID},
		"redirect_uri":  {"https://claude.ai/api/mcp/auth_callback"},
		"code_verifier": {verifier},
	})
	if status != http.StatusBadRequest || out["error"] != "invalid_grant" {
		t.Fatalf("code redeemed by the wrong client: status %d, %v", status, out)
	}
}

// A code must not survive a failed exchange attempt. If a wrong verifier
// left the code alive, an attacker holding a stolen code could try
// verifiers one at a time until one worked; deleting on first use, whatever
// the outcome, forecloses that.
func TestTokenFailedExchangeConsumesTheCode(t *testing.T) {
	o := testOAuth(t)
	o.failDelay = 0
	id := registeredClient(t, o)
	code := issueCode(t, o, id)

	status, out := postToken(t, o, url.Values{
		"grant_type":    {"authorization_code"},
		"code":          {code},
		"client_id":     {id},
		"redirect_uri":  {"https://claude.ai/api/mcp/auth_callback"},
		"code_verifier": {"not-the-verifier"},
	})
	if status != http.StatusBadRequest || out["error"] != "invalid_grant" {
		t.Fatalf("wrong verifier: status %d, %v", status, out)
	}

	status, out = postToken(t, o, url.Values{
		"grant_type":    {"authorization_code"},
		"code":          {code},
		"client_id":     {id},
		"redirect_uri":  {"https://claude.ai/api/mcp/auth_callback"},
		"code_verifier": {verifier},
	})
	if status != http.StatusBadRequest || out["error"] != "invalid_grant" {
		t.Errorf("a code survived a failed exchange attempt: status %d, %v", status, out)
	}
}

// A refresh token belongs to whichever client it was issued to, but a
// public client may not resend client_id on refresh. Omitting it entirely
// must still work: the refresh token itself is the secret here.
func TestTokenRefreshWithNoClientIDSucceeds(t *testing.T) {
	o := testOAuth(t)
	o.failDelay = 0
	id := registeredClient(t, o)
	code := issueCode(t, o, id)

	_, out := postToken(t, o, url.Values{
		"grant_type":    {"authorization_code"},
		"code":          {code},
		"client_id":     {id},
		"redirect_uri":  {"https://claude.ai/api/mcp/auth_callback"},
		"code_verifier": {verifier},
	})
	refresh, _ := out["refresh_token"].(string)
	if refresh == "" {
		t.Fatalf("no refresh token issued: %v", out)
	}

	status, out := postToken(t, o, url.Values{
		"grant_type":    {"refresh_token"},
		"refresh_token": {refresh},
	})
	if status != http.StatusOK {
		t.Fatalf("refresh without client_id: status %d, %v", status, out)
	}
}

// A wrong client_id on an otherwise-valid refresh token must be rejected,
// but must not destroy the token: nothing about presenting a wrong
// client_id proves the caller does not also hold the right one, and
// burning the token on that attempt would let anyone who intercepts (or
// simply mistypes) a client_id deny the rightful client its refresh.
func TestTokenRefreshWithWrongClientIDDoesNotBurnTheToken(t *testing.T) {
	o := testOAuth(t)
	o.failDelay = 0
	id := registeredClient(t, o)
	otherID := registeredClient(t, o)
	code := issueCode(t, o, id)

	_, out := postToken(t, o, url.Values{
		"grant_type":    {"authorization_code"},
		"code":          {code},
		"client_id":     {id},
		"redirect_uri":  {"https://claude.ai/api/mcp/auth_callback"},
		"code_verifier": {verifier},
	})
	refresh, _ := out["refresh_token"].(string)

	status, out := postToken(t, o, url.Values{
		"grant_type":    {"refresh_token"},
		"refresh_token": {refresh},
		"client_id":     {otherID},
	})
	if status != http.StatusBadRequest || out["error"] != "invalid_grant" {
		t.Fatalf("wrong client_id: status %d, %v", status, out)
	}

	status, out = postToken(t, o, url.Values{
		"grant_type":    {"refresh_token"},
		"refresh_token": {refresh},
		"client_id":     {id},
	})
	if status != http.StatusOK {
		t.Errorf("a wrong client_id on one attempt burned a valid refresh token: status %d, %v", status, out)
	}
}

// Two failed passphrase attempts arriving at the same time must not both
// pay the delay concurrently: that would let an attacker guess at a rate
// bounded only by however many requests it can fire in parallel. A
// dedicated mutex held across the delay forces them to queue, so the wall
// time for both to finish is at least two delays.
func TestAuthorizeSerializesFailedPassphraseAttempts(t *testing.T) {
	o := testOAuth(t)
	o.failDelay = 50 * time.Millisecond
	id := registeredClient(t, o)

	form := url.Values{
		"response_type":         {"code"},
		"client_id":             {id},
		"redirect_uri":          {"https://claude.ai/api/mcp/auth_callback"},
		"code_challenge":        {"E9Melhoa2OwvFrEMTJguCHaoeK1t8URWbuGJSstw-cM"},
		"code_challenge_method": {"S256"},
		"passphrase":            {"wrong"},
	}

	attempt := func(done chan<- struct{}) {
		req := httptest.NewRequest(http.MethodPost, "/authorize", strings.NewReader(form.Encode()))
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		rec := httptest.NewRecorder()
		o.handleAuthorize(rec, req)
		close(done)
	}

	start := time.Now()
	done1, done2 := make(chan struct{}), make(chan struct{})
	go attempt(done1)
	go attempt(done2)
	<-done1
	<-done2
	elapsed := time.Since(start)

	if elapsed < 2*o.failDelay {
		t.Errorf("two concurrent wrong-passphrase attempts finished in %v, want at least %v (serialized)", elapsed, 2*o.failDelay)
	}
}

// If the disk write that rotates a refresh token fails, the rotation must
// not take effect anywhere a restart could observe: not in the response to
// the client, and not by silently leaving the old token dead in memory
// while the last thing actually persisted still lists it as valid. Both
// must fail closed together, or a crash between the two leaves a
// rotated-away token alive again after a restart.
func TestTokenRefreshFailsClosedWhenPersistFails(t *testing.T) {
	dir := t.TempDir()
	o, err := newOAuth(filepath.Join(dir, "oauth.json"), "https://mail.example.com", "hunter2")
	if err != nil {
		t.Fatal(err)
	}
	o.failDelay = 0
	id := registeredClient(t, o)
	code := issueCode(t, o, id)

	_, out := postToken(t, o, url.Values{
		"grant_type":    {"authorization_code"},
		"code":          {code},
		"client_id":     {id},
		"redirect_uri":  {"https://claude.ai/api/mcp/auth_callback"},
		"code_verifier": {verifier},
	})
	refresh, _ := out["refresh_token"].(string)
	if refresh == "" {
		t.Fatalf("no refresh token issued: %v", out)
	}

	// Make the next save fail: strip write permission from the directory
	// holding the state file, so creating the ".tmp" file for the atomic
	// rename fails.
	if err := os.Chmod(dir, 0o500); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(dir, 0o755) })

	status, out := postToken(t, o, url.Values{
		"grant_type":    {"refresh_token"},
		"refresh_token": {refresh},
		"client_id":     {id},
	})
	if status != http.StatusInternalServerError || out["error"] != "server_error" {
		t.Fatalf("persist failure: status %d, %v; want 500 server_error", status, out)
	}

	// Restore persistence and confirm the old token is still the one on
	// record: it must not have been silently rotated in memory without
	// that rotation being saved.
	if err := os.Chmod(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	status, out = postToken(t, o, url.Values{
		"grant_type":    {"refresh_token"},
		"refresh_token": {refresh},
		"client_id":     {id},
	})
	if status != http.StatusOK {
		t.Errorf("the old refresh token did not survive a failed persist: status %d, %v", status, out)
	}
}

// cimdHost serves a metadata document whose client_id is its own URL, the
// binding CIMD depends on. Returned URL is the client_id to present.
func cimdHost(t *testing.T, name string, redirects []string) string {
	t.Helper()
	var ts *httptest.Server
	ts = httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, http.StatusOK, map[string]any{
			"client_id":     ts.URL + "/meta.json",
			"client_name":   name,
			"redirect_uris": redirects,
		})
	}))
	t.Cleanup(ts.Close)
	return ts.URL + "/meta.json"
}

func TestASMetadataAdvertisesCIMD(t *testing.T) {
	o := testOAuth(t)
	rec := httptest.NewRecorder()
	o.handleASMetadata(rec, httptest.NewRequest(http.MethodGet, "/.well-known/oauth-authorization-server", nil))
	var doc struct {
		CIMD bool     `json:"client_id_metadata_document_supported"`
		Auth []string `json:"token_endpoint_auth_methods_supported"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &doc); err != nil {
		t.Fatal(err)
	}
	// Claude selects CIMD only when both of these hold.
	if !doc.CIMD {
		t.Error("client_id_metadata_document_supported must be true")
	}
	if !containsString(doc.Auth, "none") {
		t.Error("token_endpoint_auth_methods_supported must include none")
	}
}

func TestCIMDFullFlow(t *testing.T) {
	o := testOAuth(t)
	o.failDelay = 0
	o.allowPrivateCIMD = true // the metadata host below is loopback
	o.cimdHTTP.Transport.(*http.Transport).TLSClientConfig = &tls.Config{InsecureSkipVerify: true}
	clientID := cimdHost(t, "CIMD Test Client", []string{"https://claude.ai/api/mcp/auth_callback"})

	// consent page renders with the fetched client name
	q := url.Values{
		"response_type": {"code"}, "client_id": {clientID},
		"redirect_uri":          {"https://claude.ai/api/mcp/auth_callback"},
		"code_challenge":        {challengeFor(verifier)},
		"code_challenge_method": {"S256"},
	}
	rec := httptest.NewRecorder()
	o.handleAuthorize(rec, httptest.NewRequest(http.MethodGet, "/authorize?"+q.Encode(), nil))
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), "CIMD Test Client") {
		t.Fatalf("consent page: status %d, name shown: %v", rec.Code, strings.Contains(rec.Body.String(), "CIMD Test Client"))
	}

	// passphrase yields a code, and the code exchanges with the URL client_id
	q.Set("passphrase", "hunter2")
	req := httptest.NewRequest(http.MethodPost, "/authorize", strings.NewReader(q.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	rec = httptest.NewRecorder()
	o.handleAuthorize(rec, req)
	if rec.Code != http.StatusFound {
		t.Fatalf("authorize status = %d", rec.Code)
	}
	loc, _ := url.Parse(rec.Header().Get("Location"))
	status, out := postToken(t, o, url.Values{
		"grant_type": {"authorization_code"}, "code": {loc.Query().Get("code")},
		"client_id":     {clientID},
		"redirect_uri":  {"https://claude.ai/api/mcp/auth_callback"},
		"code_verifier": {verifier},
	})
	if status != http.StatusOK {
		t.Fatalf("token exchange with a CIMD client_id failed: %d %v", status, out)
	}
	// and nothing was persisted: CIMD's point is no registration
	if o.client(clientID) != nil {
		t.Error("a CIMD client must not be stored in the registration map")
	}
}

func TestCIMDRejectsMismatchedBinding(t *testing.T) {
	o := testOAuth(t)
	o.allowPrivateCIMD = true
	o.cimdHTTP.Transport.(*http.Transport).TLSClientConfig = &tls.Config{InsecureSkipVerify: true}
	// document claims a different client_id than the URL it lives at
	ts := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, http.StatusOK, map[string]any{
			"client_id":     "https://evil.example.com/meta.json",
			"redirect_uris": []string{"https://claude.ai/api/mcp/auth_callback"},
		})
	}))
	defer ts.Close()
	if _, err := o.cimdClient(context.Background(), ts.URL+"/meta.json"); err == nil {
		t.Fatal("a document whose client_id differs from its URL must be rejected")
	}
}

func TestCIMDRefusesInternalAddressesByDefault(t *testing.T) {
	o := testOAuth(t) // allowPrivateCIMD stays false: the shipped configuration
	ts := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Error("the SSRF guard let a request through to a loopback address")
	}))
	defer ts.Close()
	if _, err := o.cimdClient(context.Background(), ts.URL+"/meta.json"); err == nil {
		t.Fatal("fetching client metadata from a loopback address must be refused")
	}
}

func TestCIMDRejectsNonURLClientIDs(t *testing.T) {
	o := testOAuth(t)
	for _, id := range []string{"just-an-id", "http://plain.example.com/meta.json", "https://host/meta.json#frag"} {
		if _, err := o.cimdClient(context.Background(), id); err == nil {
			t.Errorf("cimdClient(%q) should have been rejected", id)
		}
	}
}
