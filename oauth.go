package main

import (
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"strings"
	"sync"
	"time"
)

type oauthClient struct {
	ID           string   `json:"id"`
	Name         string   `json:"name"`
	RedirectURIs []string `json:"redirect_uris"`
}

type refreshToken struct {
	ClientID string    `json:"client_id"`
	Issued   time.Time `json:"issued"`
}

type oauthState struct {
	Clients map[string]*oauthClient  `json:"clients"`
	Refresh map[string]*refreshToken `json:"refresh"`
}

type authCode struct {
	clientID  string
	redirect  string
	challenge string
	expires   time.Time
}

type oauthServer struct {
	path       string
	publicURL  string
	passphrase string

	mu    sync.Mutex
	state oauthState

	// In-memory only. A restart drops access tokens and pending codes; the
	// client gets a 401 and refreshes, which is its documented behaviour.
	codes  map[string]authCode
	access map[string]time.Time
}

func newOAuth(statePath, publicURL, passphrase string) (*oauthServer, error) {
	if publicURL == "" {
		return nil, fmt.Errorf("PUBLIC_URL is required for OAuth metadata")
	}
	if passphrase == "" {
		return nil, fmt.Errorf("OAUTH_PASSPHRASE is required")
	}
	o := &oauthServer{
		path:       statePath,
		publicURL:  strings.TrimSuffix(publicURL, "/"),
		passphrase: passphrase,
		state:      oauthState{Clients: map[string]*oauthClient{}, Refresh: map[string]*refreshToken{}},
		codes:      map[string]authCode{},
		access:     map[string]time.Time{},
	}
	raw, err := os.ReadFile(statePath)
	if err == nil {
		if err := json.Unmarshal(raw, &o.state); err != nil {
			return nil, fmt.Errorf("oauth state: %w", err)
		}
		if o.state.Clients == nil {
			o.state.Clients = map[string]*oauthClient{}
		}
		if o.state.Refresh == nil {
			o.state.Refresh = map[string]*refreshToken{}
		}
	} else if !os.IsNotExist(err) {
		return nil, err
	}
	return o, nil
}

// save writes the state file. Callers hold o.mu.
func (o *oauthServer) save() error {
	raw, err := json.MarshalIndent(o.state, "", "  ")
	if err != nil {
		return err
	}
	tmp := o.path + ".tmp"
	f, err := os.OpenFile(tmp, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o600)
	if err != nil {
		return err
	}
	if _, err := f.Write(raw); err != nil {
		f.Close()
		return err
	}
	if err := f.Sync(); err != nil {
		f.Close()
		return err
	}
	if err := f.Close(); err != nil {
		return err
	}
	return os.Rename(tmp, o.path)
}

func randomToken() string {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		panic(err) // crypto/rand failing is not a recoverable condition
	}
	return base64.RawURLEncoding.EncodeToString(b)
}

func (o *oauthServer) registerClient(name string, redirects []string) (string, error) {
	o.mu.Lock()
	defer o.mu.Unlock()
	id := randomToken()
	o.state.Clients[id] = &oauthClient{ID: id, Name: name, RedirectURIs: redirects}
	return id, o.save()
}

func (o *oauthServer) client(id string) *oauthClient {
	o.mu.Lock()
	defer o.mu.Unlock()
	return o.state.Clients[id]
}

func writeJSON(w http.ResponseWriter, code int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(v)
}

func (o *oauthServer) handleASMetadata(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, map[string]any{
		"issuer":                                o.publicURL,
		"authorization_endpoint":                o.publicURL + "/authorize",
		"token_endpoint":                        o.publicURL + "/token",
		"registration_endpoint":                 o.publicURL + "/register",
		"response_types_supported":              []string{"code"},
		"grant_types_supported":                 []string{"authorization_code", "refresh_token"},
		"code_challenge_methods_supported":      []string{"S256"},
		"token_endpoint_auth_methods_supported": []string{"none"},
		"scopes_supported":                      []string{"mail.read"},
	})
}
