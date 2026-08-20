package main

import (
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"html/template"
	"net/http"
	"net/url"
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
	failDelay  time.Duration

	// failMu serialises the failed-passphrase path in handleAuthorize. It is
	// separate from mu so that a run of wrong guesses cannot block real
	// traffic, but concurrent guesses still queue behind the delay instead
	// of all paying it in parallel.
	failMu sync.Mutex

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
		failDelay:  time.Second,
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

func (o *oauthServer) handleRegister(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}
	// RFC 7591 registration is JSON; the token endpoint is form-encoded. They
	// need different parsers, and mixing them up is a documented trap.
	var req struct {
		ClientName   string   `json:"client_name"`
		RedirectURIs []string `json:"redirect_uris"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 64<<10)).Decode(&req); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid_client_metadata"})
		return
	}
	if len(req.RedirectURIs) == 0 {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid_redirect_uri"})
		return
	}
	for _, u := range req.RedirectURIs {
		parsed, err := url.Parse(u)
		if err != nil || parsed.Scheme == "" {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid_redirect_uri"})
			return
		}
		if parsed.Scheme != "https" && !isLoopback(parsed) {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid_redirect_uri"})
			return
		}
	}
	id, err := o.registerClient(req.ClientName, req.RedirectURIs)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "server_error"})
		return
	}
	writeJSON(w, http.StatusCreated, map[string]any{
		"client_id":                  id,
		"client_id_issued_at":        time.Now().Unix(),
		"redirect_uris":              req.RedirectURIs,
		"token_endpoint_auth_method": "none",
		"grant_types":                []string{"authorization_code", "refresh_token"},
		"response_types":             []string{"code"},
	})
}

func isLoopback(u *url.URL) bool {
	h := u.Hostname()
	return h == "localhost" || h == "127.0.0.1" || h == "::1"
}

// redirectAllowed compares a redirect against the registered set. Loopback
// entries match on scheme and path with the host and port ignored, because
// native clients bind an ephemeral port per session and may use "localhost"
// or a loopback IP literal interchangeably (RFC 8252). Everything else must
// match scheme, host and path exactly.
//
// Userinfo, query strings and fragments on the candidate are rejected
// outright rather than compared: OAuth redirect URIs are meant to match
// their registration exactly (RFC 9700), a fragment on the redirect is
// forbidden outright by RFC 6749 3.1.2, and neither loopback port variance
// nor anything else in this project's flow needs any of the three, so their
// presence is treated as malformed rather than matched loosely.
func redirectAllowed(registered []string, candidate string) bool {
	c, err := url.Parse(candidate)
	if err != nil {
		return false
	}
	if c.User != nil || c.RawQuery != "" || c.Fragment != "" {
		return false
	}
	for _, r := range registered {
		p, err := url.Parse(r)
		if err != nil {
			continue
		}
		if p.Scheme != c.Scheme || p.Path != c.Path {
			continue
		}
		if isLoopback(p) && isLoopback(c) {
			return true
		}
		if p.Host == c.Host {
			return true
		}
	}
	return false
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

const codeTTL = 60 * time.Second

var consentPage = template.Must(template.New("consent").Parse(`<!doctype html>
<html><head><meta charset="utf-8"><title>Authorise access to your mail</title></head>
<body>
<h1>Authorise access to your mail</h1>
<p>{{.ClientName}} is asking to read your mail. It cannot send, delete or change anything.</p>
{{if .Failed}}<p><strong>That passphrase was not correct.</strong></p>{{end}}
<form method="post" action="/authorize">
  <input type="hidden" name="response_type" value="code">
  <input type="hidden" name="client_id" value="{{.ClientID}}">
  <input type="hidden" name="redirect_uri" value="{{.RedirectURI}}">
  <input type="hidden" name="state" value="{{.State}}">
  <input type="hidden" name="code_challenge" value="{{.Challenge}}">
  <input type="hidden" name="code_challenge_method" value="S256">
  <label>Passphrase <input type="password" name="passphrase" autofocus></label>
  <button type="submit">Authorise</button>
</form>
</body></html>`))

func (o *oauthServer) handleAuthorize(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil {
		http.Error(w, "bad request", http.StatusBadRequest)
		return
	}
	var (
		clientID  = r.Form.Get("client_id")
		redirect  = r.Form.Get("redirect_uri")
		state     = r.Form.Get("state")
		challenge = r.Form.Get("code_challenge")
		method    = r.Form.Get("code_challenge_method")
	)
	// Every check below happens before anything is echoed into a redirect. An
	// unvalidated redirect_uri turned into a Location header is an open
	// redirect, so failures here render an error page instead.
	c := o.client(clientID)
	if c == nil {
		http.Error(w, "unknown client", http.StatusBadRequest)
		return
	}
	if !redirectAllowed(c.RedirectURIs, redirect) {
		http.Error(w, "redirect_uri does not match this client's registration", http.StatusBadRequest)
		return
	}
	if r.Form.Get("response_type") != "code" {
		http.Error(w, "response_type must be code", http.StatusBadRequest)
		return
	}
	if method != "S256" || challenge == "" {
		http.Error(w, "code_challenge_method must be S256", http.StatusBadRequest)
		return
	}

	// The passphrase is typed into this page. If another site can frame it,
	// a clickjacking overlay can steer that click and submission.
	w.Header().Set("X-Frame-Options", "DENY")

	data := struct {
		ClientName, ClientID, RedirectURI, State, Challenge string
		Failed                                              bool
	}{c.Name, clientID, redirect, state, challenge, false}

	if r.Method == http.MethodGet {
		_ = consentPage.Execute(w, data)
		return
	}
	if r.Method != http.MethodPost {
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}

	given := r.Form.Get("passphrase")
	if subtle.ConstantTimeCompare([]byte(given), []byte(o.passphrase)) != 1 {
		// Held across the sleep so concurrent guesses queue up instead of
		// all paying the delay at once: the guess rate is capped no matter
		// how many requests arrive in parallel.
		o.failMu.Lock()
		time.Sleep(o.failDelay)
		o.failMu.Unlock()
		data.Failed = true
		w.WriteHeader(http.StatusUnauthorized)
		_ = consentPage.Execute(w, data)
		return
	}

	code := randomToken()
	o.mu.Lock()
	o.codes[code] = authCode{clientID: clientID, redirect: redirect, challenge: challenge, expires: time.Now().Add(codeTTL)}
	o.mu.Unlock()

	u, _ := url.Parse(redirect)
	q := u.Query()
	q.Set("code", code)
	if state != "" {
		q.Set("state", state)
	}
	u.RawQuery = q.Encode()
	http.Redirect(w, r, u.String(), http.StatusFound)
}

const accessTTL = time.Hour

func tokenError(w http.ResponseWriter, code string) {
	// RFC 6749 error codes, not custom ones: clients key their refresh
	// behaviour off invalid_grant specifically.
	writeJSON(w, http.StatusBadRequest, map[string]string{"error": code})
}

func (o *oauthServer) handleToken(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}
	// The token endpoint is form-encoded; registration is JSON. A JSON-only
	// body parser here returns 415 and breaks the flow.
	if err := r.ParseForm(); err != nil {
		tokenError(w, "invalid_request")
		return
	}
	switch r.Form.Get("grant_type") {
	case "authorization_code":
		o.grantCode(w, r)
	case "refresh_token":
		o.grantRefresh(w, r)
	default:
		tokenError(w, "unsupported_grant_type")
	}
}

func (o *oauthServer) grantCode(w http.ResponseWriter, r *http.Request) {
	code := r.Form.Get("code")

	o.mu.Lock()
	ac, ok := o.codes[code]
	delete(o.codes, code) // single use: dead on first attempt, whatever the outcome
	o.mu.Unlock()

	if !ok || time.Now().After(ac.expires) {
		tokenError(w, "invalid_grant")
		return
	}
	if ac.clientID != r.Form.Get("client_id") || ac.redirect != r.Form.Get("redirect_uri") {
		tokenError(w, "invalid_grant")
		return
	}
	sum := sha256.Sum256([]byte(r.Form.Get("code_verifier")))
	computed := base64.RawURLEncoding.EncodeToString(sum[:])
	if subtle.ConstantTimeCompare([]byte(computed), []byte(ac.challenge)) != 1 {
		tokenError(w, "invalid_grant")
		return
	}
	o.issue(w, ac.clientID)
}

func (o *oauthServer) grantRefresh(w http.ResponseWriter, r *http.Request) {
	presented := r.Form.Get("refresh_token")
	clientID := r.Form.Get("client_id")

	o.mu.Lock()
	rt, ok := o.state.Refresh[presented]
	// A wrong client_id must not consume a token that is otherwise valid:
	// unlike an authorization code, the refresh token itself is the secret,
	// so a mismatched client_id here proves nothing worth burning it over,
	// and doing so would let a stray or mistaken client_id deny the
	// rightful holder its next refresh.
	if ok && clientID != "" && clientID != rt.ClientID {
		ok = false
	}
	var saveErr error
	if ok {
		// Rotation: the presented token dies in the same response that
		// issues its replacement, which OAuth 2.1 requires for public
		// clients.
		delete(o.state.Refresh, presented)
		saveErr = o.save()
		if saveErr != nil {
			// The deletion never reached disk. Put the token back so memory
			// agrees with the last thing actually persisted: otherwise a
			// restart before some later save reloads the token as valid
			// again, while this process just told the client it was dead.
			o.state.Refresh[presented] = rt
		}
	}
	o.mu.Unlock()

	if !ok {
		tokenError(w, "invalid_grant")
		return
	}
	if saveErr != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "server_error"})
		return
	}
	o.issue(w, rt.ClientID)
}

func (o *oauthServer) issue(w http.ResponseWriter, clientID string) {
	access, refresh := randomToken(), randomToken()

	o.mu.Lock()
	o.access[access] = time.Now().Add(accessTTL)
	o.state.Refresh[refresh] = &refreshToken{ClientID: clientID, Issued: time.Now()}
	err := o.save()
	o.mu.Unlock()
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "server_error"})
		return
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"access_token":  access,
		"token_type":    "Bearer",
		"expires_in":    int(accessTTL.Seconds()),
		"refresh_token": refresh,
		"scope":         "mail.read",
	})
}

func (o *oauthServer) validAccessToken(token string) bool {
	o.mu.Lock()
	defer o.mu.Unlock()
	exp, ok := o.access[token]
	if !ok {
		return false
	}
	if time.Now().After(exp) {
		delete(o.access, token)
		return false
	}
	return true
}
