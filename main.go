package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"regexp"
	"strings"
	"syscall"
	"time"
	"unicode"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// Account is one IMAP account to mirror. Everything except name, host, user
// and password has a default; see the design spec for why the mbsync knobs
// that used to live here (pipeline depth, SubFolders, AuthMechs) are pinned.
type Account struct {
	Name           string   `json:"name"`
	Host           string   `json:"host"`
	Port           int      `json:"port"`
	User           string   `json:"user"`
	Password       string   `json:"password"`
	TLS            string   `json:"tls"`
	Patterns       []string `json:"patterns"`
	ExcludeFolders []string `json:"exclude_folders"`
}

type Config struct {
	Accounts []Account `json:"accounts"`
}

// hasWhitespaceOrControl returns true if s contains any whitespace or control character.
func hasWhitespaceOrControl(s string) bool {
	for _, r := range s {
		if unicode.IsSpace(r) || unicode.IsControl(r) {
			return true
		}
	}
	return false
}

// envRef matches ${VAR} references only. os.ExpandEnv also expands a bare
// $VAR, which mangles a literal "$" in a value that was never meant to be a
// reference (a password of "p$ssw0rd" becomes "p"); braces-only expansion
// leaves a bare "$" untouched.
var envRef = regexp.MustCompile(`\$\{(\w+)\}`)

func expandBracedEnv(s string) string {
	return envRef.ReplaceAllStringFunc(s, func(m string) string {
		return os.Getenv(envRef.FindStringSubmatch(m)[1])
	})
}

// loadConfig reads the accounts file, expands ${VAR} references against the
// environment so secrets never sit in the file, then validates and defaults.
func loadConfig(path string) (*Config, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("accounts file: %w", err)
	}
	var cfg Config
	if err := json.Unmarshal([]byte(expandBracedEnv(string(raw))), &cfg); err != nil {
		return nil, fmt.Errorf("accounts file: %w", err)
	}
	if len(cfg.Accounts) == 0 {
		return nil, fmt.Errorf("accounts file: no accounts defined")
	}
	seen := map[string]bool{}
	for i := range cfg.Accounts {
		a := &cfg.Accounts[i]
		if a.Name == "" || strings.ContainsAny(a.Name, `/\ "'`) {
			return nil, fmt.Errorf("account %d: name must be non-empty and free of spaces, quotes and slashes", i)
		}
		if seen[a.Name] {
			return nil, fmt.Errorf("account %q: duplicate name", a.Name)
		}
		seen[a.Name] = true
		if a.Host == "" {
			return nil, fmt.Errorf("account %q: host is required", a.Name)
		}
		if hasWhitespaceOrControl(a.Host) {
			return nil, fmt.Errorf("account %q: host must not contain whitespace or control characters", a.Name)
		}
		if a.User == "" {
			return nil, fmt.Errorf("account %q: user is required", a.Name)
		}
		if hasWhitespaceOrControl(a.User) {
			return nil, fmt.Errorf("account %q: user must not contain whitespace or control characters", a.Name)
		}
		if a.Password == "" {
			return nil, fmt.Errorf("account %q: password is empty; is the referenced environment variable set?", a.Name)
		}
		switch a.TLS {
		case "":
			a.TLS = "imaps"
		case "imaps", "starttls", "none":
		default:
			return nil, fmt.Errorf("account %q: tls must be imaps, starttls or none", a.Name)
		}
		if a.Port == 0 {
			if a.TLS == "imaps" {
				a.Port = 993
			} else {
				a.Port = 143
			}
		}
		if len(a.Patterns) == 0 {
			a.Patterns = []string{"*"}
		}
	}
	return &cfg, nil
}

type env struct {
	Config       string
	Maildir      string
	Index        string
	PublicURL    string
	ListenAddr   string
	Passphrase   string
	SyncInterval time.Duration
	InitMirror   bool
}

func loadEnv() (*env, error) {
	e := &env{
		Config:       os.Getenv("CONFIG"),
		Maildir:      os.Getenv("MAILDIR"),
		Index:        os.Getenv("INDEX"),
		PublicURL:    os.Getenv("PUBLIC_URL"),
		ListenAddr:   os.Getenv("LISTEN_ADDR"),
		Passphrase:   os.Getenv("OAUTH_PASSPHRASE"),
		SyncInterval: 5 * time.Minute,
		InitMirror:   os.Getenv("INIT_MIRROR") == "1",
	}
	for name, v := range map[string]string{"CONFIG": e.Config, "MAILDIR": e.Maildir, "INDEX": e.Index} {
		if v == "" {
			return nil, fmt.Errorf("%s is required", name)
		}
	}
	if e.ListenAddr == "" {
		e.ListenAddr = ":8080"
	}
	if s := os.Getenv("SYNC_INTERVAL"); s != "" {
		d, err := time.ParseDuration(s)
		if err != nil {
			return nil, fmt.Errorf("SYNC_INTERVAL: %w", err)
		}
		e.SyncInterval = d
	}
	return e, nil
}

// refreshExclusions runs SPECIAL-USE discovery for every account and updates
// srv's exclusions. Called once at startup and again on every sync tick (see
// run), so a discovery failure is not permanent for the life of the process.
func refreshExclusions(ctx context.Context, cfg *Config, srv *Server) {
	for _, a := range cfg.Accounts {
		special, all, err := discoverSpecialUse(ctx, a)
		if err != nil {
			fmt.Fprintf(os.Stderr, "special-use discovery: account %s: %v\n", a.Name, err)
		}
		srv.setExcluded(a.Name, excludedFolders(a, special, all))
	}
}

// runTicker calls fn every interval until ctx is cancelled. It does not fire
// overlapping passes itself; fn (the syncer) is responsible for refusing a
// concurrent run, since the ticker has no way to know how long fn will take.
func runTicker(ctx context.Context, every time.Duration, fn func(context.Context)) {
	t := time.NewTicker(every)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			fn(ctx)
		}
	}
}

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, "your-mail-mcp:", err)
		os.Exit(1)
	}
}

func run() error {
	e, err := loadEnv()
	if err != nil {
		return err
	}
	cfg, err := loadConfig(e.Config)
	if err != nil {
		return err
	}

	// Generated configs live in a directory only this process writes, so the
	// read-only mbsync directives cannot be edited underneath us.
	runtimeDir, err := os.MkdirTemp("", "your-mail-mcp")
	if err != nil {
		return err
	}
	defer os.RemoveAll(runtimeDir)
	mbsyncPath := filepath.Join(runtimeDir, "mbsyncrc")
	notmuchPath := filepath.Join(runtimeDir, "notmuch-config")
	if err := os.WriteFile(mbsyncPath, []byte(genMbsyncrc(cfg, e.Maildir)), 0o600); err != nil {
		return err
	}
	if err := os.WriteFile(notmuchPath, []byte(genNotmuchConfig(e.Maildir, e.Index)), 0o600); err != nil {
		return err
	}

	nm := newNotmuch(notmuchPath)
	syncer := newSyncer(cfg, e.Maildir, mbsyncPath, nm)
	syncer.initMirror = e.InitMirror

	srv := newServer(cfg, nm, e.Maildir)
	srv.sync = syncer.Sync
	srv.status = syncer.Status

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	// Built before the discovery loop below: a missing OAUTH_PASSPHRASE or
	// PUBLIC_URL should exit the process immediately, not after logging in
	// to every configured account first.
	o, err := newOAuth(filepath.Join(e.Index, "oauth.json"), e.PublicURL, e.Passphrase)
	if err != nil {
		return err
	}

	// Discover each account's junk and trash folders so search excludes them
	// by default. A failing discovery is not fatal: the account simply has
	// nothing excluded until the operator sets exclude_folders. Repeated on
	// every sync tick (below) so an account that was unreachable at startup
	// is not left unprotected for the rest of the process's life.
	refreshExclusions(ctx, cfg, srv)

	go runTicker(ctx, e.SyncInterval, func(ctx context.Context) {
		refreshExclusions(ctx, cfg, srv)
		if _, err := syncer.Sync(ctx, "", ""); err != nil && !errors.Is(err, errSyncBusy) {
			fmt.Fprintln(os.Stderr, "sync:", err)
		}
	})

	m := mcp.NewServer(&mcp.Implementation{Name: "your-mail-mcp", Version: "0.1.0"}, nil)
	srv.registerTools(m)

	httpSrv := &http.Server{
		Addr:              e.ListenAddr,
		Handler:           newHTTPHandler(srv, o, m),
		ReadHeaderTimeout: 10 * time.Second,
	}
	go func() {
		<-ctx.Done()
		shutdown, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		_ = httpSrv.Shutdown(shutdown)
	}()
	fmt.Fprintf(os.Stderr, "listening on %s\n", e.ListenAddr)
	if err := httpSrv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
		return err
	}
	return nil
}

// requireBearer refuses anything without a live access token. The 401 carries a
// resource_metadata pointer: Claude does not honour that header on a 200, and
// without it the client has to probe for the metadata, which costs round trips
// and fails outright on hosts that do not serve /.well-known paths.
func requireBearer(o *oauthServer, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		token := strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer ")
		if token == "" || token == r.Header.Get("Authorization") || !o.validAccessToken(token) {
			w.Header().Set("WWW-Authenticate",
				fmt.Sprintf("Bearer resource_metadata=%q", o.publicURL+"/.well-known/oauth-protected-resource"))
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		next.ServeHTTP(w, r)
	})
}

func newHTTPHandler(srv *Server, o *oauthServer, m *mcp.Server) http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("/.well-known/oauth-authorization-server", o.handleASMetadata)
	mux.HandleFunc("/.well-known/oauth-protected-resource", o.handlePRMetadata)
	// Claude probes this path variant before the bare one.
	mux.HandleFunc("/.well-known/oauth-protected-resource/mcp", o.handlePRMetadata)
	mux.HandleFunc("/register", o.handleRegister)
	mux.HandleFunc("/authorize", o.handleAuthorize)
	mux.HandleFunc("/token", o.handleToken)

	streamable := mcp.NewStreamableHTTPHandler(func(*http.Request) *mcp.Server { return m }, nil)
	mux.Handle("/mcp", requireBearer(o, streamable))
	return mux
}
