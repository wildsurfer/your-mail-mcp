package main

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

type bearerRoundTripper struct {
	token string
	base  http.RoundTripper
}

func (b bearerRoundTripper) RoundTrip(r *http.Request) (*http.Response, error) {
	r = r.Clone(r.Context())
	r.Header.Set("Authorization", "Bearer "+b.token)
	return b.base.RoundTrip(r)
}

func TestEndToEndRegisterAuthorizeTokenCall(t *testing.T) {
	srv := testServer(t) // two fixture accounts, from mcp_test.go
	o := testOAuth(t)
	o.failDelay = 0

	m := mcp.NewServer(&mcp.Implementation{Name: "your-mail-mcp", Version: "test"}, nil)
	srv.registerTools(m)

	ts := httptest.NewServer(http.NotFoundHandler())
	defer ts.Close()
	o.publicURL = ts.URL
	ts.Config.Handler = newHTTPHandler(o, m)

	client := ts.Client()
	client.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }

	// 1. Unauthenticated calls must be refused with a pointer to the metadata.
	resp, err := client.Post(ts.URL+"/mcp", "application/json", strings.NewReader(`{}`))
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("unauthenticated status = %d, want 401", resp.StatusCode)
	}
	// B4: the pointer must be the suffixed canonical path — the resource is
	// PUBLIC_URL/mcp, and RFC 9728 builds the well-known location by
	// inserting the well-known segment between the host and that path, not
	// the bare fallback served at the root for a client that does not.
	wantMeta := ts.URL + "/.well-known/oauth-protected-resource/mcp"
	if got := resp.Header.Get("WWW-Authenticate"); !strings.Contains(got, `resource_metadata="`+wantMeta+`"`) {
		t.Fatalf("WWW-Authenticate = %q, want it to point at %q", got, wantMeta)
	}

	// 2. Dynamic client registration.
	resp, err = client.Post(ts.URL+"/register", "application/json",
		strings.NewReader(`{"client_name":"test","redirect_uris":["https://claude.ai/api/mcp/auth_callback"]}`))
	if err != nil {
		t.Fatal(err)
	}
	var reg struct {
		ClientID string `json:"client_id"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&reg); err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()

	// 3. Consent, which yields a code.
	form := url.Values{
		"response_type":         {"code"},
		"client_id":             {reg.ClientID},
		"redirect_uri":          {"https://claude.ai/api/mcp/auth_callback"},
		"code_challenge":        {challengeFor(verifier)},
		"code_challenge_method": {"S256"},
		"passphrase":            {"hunter2"},
	}
	resp, err = client.PostForm(ts.URL+"/authorize", form)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	loc, err := url.Parse(resp.Header.Get("Location"))
	if err != nil {
		t.Fatal(err)
	}
	code := loc.Query().Get("code")
	if code == "" {
		t.Fatal("no authorization code")
	}

	// 4. Token exchange.
	resp, err = client.PostForm(ts.URL+"/token", url.Values{
		"grant_type":    {"authorization_code"},
		"code":          {code},
		"client_id":     {reg.ClientID},
		"redirect_uri":  {"https://claude.ai/api/mcp/auth_callback"},
		"code_verifier": {verifier},
	})
	if err != nil {
		t.Fatal(err)
	}
	var tok struct {
		AccessToken string `json:"access_token"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&tok); err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if tok.AccessToken == "" {
		t.Fatal("no access token")
	}

	// 5. A real MCP session over the authenticated transport.
	ctx := context.Background()
	hc := &http.Client{Transport: bearerRoundTripper{token: tok.AccessToken, base: http.DefaultTransport}}
	c := mcp.NewClient(&mcp.Implementation{Name: "test-client", Version: "test"}, nil)
	session, err := c.Connect(ctx, &mcp.StreamableClientTransport{Endpoint: ts.URL + "/mcp", HTTPClient: hc}, nil)
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	defer session.Close()

	tools, err := session.ListTools(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(tools.Tools) != 10 {
		t.Errorf("got %d tools, want 10", len(tools.Tools))
	}

	res, err := session.CallTool(ctx, &mcp.CallToolParams{
		Name:      "count",
		Arguments: map[string]any{"query": "*", "account": "personal"},
	})
	if err != nil {
		t.Fatal(err)
	}
	got := res.Content[0].(*mcp.TextContent).Text
	if !strings.Contains(got, "1") {
		t.Errorf("count over the authenticated transport = %q, want 1", got)
	}
}

func TestProtectedResourceMetadataMatchesTheServerURL(t *testing.T) {
	o := testOAuth(t)
	rec := httptest.NewRecorder()
	o.handlePRMetadata(rec, httptest.NewRequest(http.MethodGet, "/.well-known/oauth-protected-resource", nil))

	var doc struct {
		Resource             string   `json:"resource"`
		AuthorizationServers []string `json:"authorization_servers"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &doc); err != nil {
		t.Fatal(err)
	}
	if doc.Resource != "https://mail.example.com/mcp" {
		t.Errorf("resource = %q, want the MCP URL as the user enters it", doc.Resource)
	}
	if len(doc.AuthorizationServers) != 1 || doc.AuthorizationServers[0] != "https://mail.example.com" {
		t.Errorf("authorization_servers = %v", doc.AuthorizationServers)
	}
}
