package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
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
