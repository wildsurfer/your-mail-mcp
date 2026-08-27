package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"regexp"
	"strconv"
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
//
// Expansion runs after JSON is parsed, one string field at a time, not by
// splicing text into the raw file before parsing it. A spliced-in secret
// containing a `"` or a `\` would land inside a JSON string unescaped and
// corrupt the document it is embedded in; expanding each already-parsed
// field sidesteps that entirely; the substituted value never has to be valid
// JSON, only a valid Go string.
func loadConfig(path string) (*Config, error) {
	raw, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		// No accounts file is a legal way to start: the server answers every
		// tool and status explains what to configure.
		return &Config{}, nil
	}
	if err != nil {
		return nil, fmt.Errorf("accounts file: %w", err)
	}
	var cfg Config
	if err := json.Unmarshal(raw, &cfg); err != nil {
		return nil, fmt.Errorf("accounts file: %w", err)
	}
	seen := map[string]bool{}
	for i := range cfg.Accounts {
		a := &cfg.Accounts[i]
		a.Name = expandBracedEnv(a.Name)
		a.Host = expandBracedEnv(a.Host)
		a.User = expandBracedEnv(a.User)
		a.Password = expandBracedEnv(a.Password)
		a.TLS = expandBracedEnv(a.TLS)
		for j, p := range a.Patterns {
			a.Patterns[j] = expandBracedEnv(p)
		}
		for j, f := range a.ExcludeFolders {
			a.ExcludeFolders[j] = expandBracedEnv(f)
		}
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
	SyncTimeout  time.Duration
	InitMirror   bool
}

func loadEnv() (*env, error) {
	e := &env{
		Config:     os.Getenv("CONFIG"),
		Maildir:    os.Getenv("MAILDIR"),
		Index:      os.Getenv("INDEX"),
		PublicURL:  os.Getenv("PUBLIC_URL"),
		ListenAddr: os.Getenv("LISTEN_ADDR"),
		Passphrase: os.Getenv("OAUTH_PASSPHRASE"),
		// 10 minutes is the one cadence a provider actually publishes:
		// Google's recommended IMAP client settings say "check for new
		// messages every 10 minutes". iCloud documents nothing but is
		// known to throttle eager clients.
		SyncInterval: 10 * time.Minute,
		SyncTimeout:  time.Hour,
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
	if err := parseDurationEnv("SYNC_INTERVAL", &e.SyncInterval); err != nil {
		return nil, err
	}
	if err := parseDurationEnv("SYNC_TIMEOUT", &e.SyncTimeout); err != nil {
		return nil, err
	}
	return e, nil
}

// parseDurationEnv overrides *d with the named environment variable, if set,
// and rejects a non-positive duration: a zero or negative SYNC_INTERVAL
// makes time.NewTicker panic, and a non-positive SYNC_TIMEOUT would give
// every account's sync a context that is already expired.
func parseDurationEnv(name string, d *time.Duration) error {
	s := os.Getenv(name)
	if s == "" {
		return nil
	}
	v, err := time.ParseDuration(s)
	if err != nil {
		return fmt.Errorf("%s: %w", name, err)
	}
	if v <= 0 {
		return fmt.Errorf("%s: must be positive, got %s", name, v)
	}
	*d = v
	return nil
}

// discoveryInterval bounds how often SPECIAL-USE discovery re-runs. It is
// its own ticker, well above e.SyncInterval: discovery does a full IMAP
// LOGIN per account, and junk/trash folder names do not move, so retrying
// every sync tick (every few minutes, by default) would roughly double the
// login rate against every provider for no benefit. Hourly is ample.
const discoveryInterval = time.Hour

// applyConfiguredExclusions sets exclusions for every account with an
// explicit exclude_folders list. It touches no network — excludedFolders
// already prefers a configured list over anything discovery could return —
// so it is safe and cheap to run synchronously at startup, before the
// listener opens; see run.
func applyConfiguredExclusions(cfg *Config, srv *Server) {
	for _, a := range cfg.Accounts {
		if len(a.ExcludeFolders) > 0 {
			srv.setExcluded(a.Name, excludedFolders(a, nil, nil))
		}
	}
}

// refreshExclusions runs SPECIAL-USE discovery for every account and updates
// srv's exclusions. Called once at startup and again on its own ticker (see
// run), so a discovery failure is not permanent for the life of the process.
//
// An account with exclude_folders configured skips discovery entirely: the
// answer is already known (excludedFolders prefers the configured list
// regardless of what discovery finds), so there is nothing here worth an
// IMAP LOGIN for.
//
// On a failed discovery attempt for an account with no configured
// exclude_folders, setExcluded is skipped rather than called with an empty
// list: special and all are both nil on error, and excludedFolders would
// otherwise return no folders, silently wiping out whatever a previous
// successful attempt had established.
func refreshExclusions(ctx context.Context, cfg *Config, srv *Server) {
	for _, a := range cfg.Accounts {
		if len(a.ExcludeFolders) > 0 {
			srv.setExcluded(a.Name, excludedFolders(a, nil, nil))
			continue
		}
		special, all, err := discoverSpecialUse(ctx, a)
		if err != nil {
			fmt.Fprintf(os.Stderr, "special-use discovery: account %s: %v\n", a.Name, err)
			continue
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

// detachedSync returns the entry point the refresh tool calls to start a
// pass. The pass outlives the tool call that asked for it, since refresh
// waits refreshWait and then reports that it is still running, so it runs
// under base, the process context, and ignores the request context it is
// handed. The SDK cancels a handler's context the moment the handler
// returns, and mbsync runs under that context, so without this every refresh
// that reported "in progress" would kill the download it had just started.
// Shutdown still stops it: base is cancelled with the process.
func detachedSync(base context.Context, s *Syncer) func(context.Context, string) (int, error) {
	return func(_ context.Context, account string) (int, error) { return s.Sync(base, account) }
}

// serveSocket accepts local MCP sessions on a Unix socket. Each connection is
// one session on the shared server, so it sees the same tools and the same
// syncer as HTTP. There is no auth on the socket: reaching it means running
// a process inside the container, which is the boundary.
func serveSocket(ctx context.Context, path string, m *mcp.Server) error {
	_ = os.Remove(path)
	l, err := net.Listen("unix", path)
	if err != nil {
		return err
	}
	defer os.Remove(path)
	go func() {
		<-ctx.Done()
		l.Close()
	}()
	for {
		conn, err := l.Accept()
		if err != nil {
			if ctx.Err() != nil {
				return nil
			}
			return err
		}
		go func() {
			defer conn.Close()
			sess, err := m.Connect(ctx, &mcp.IOTransport{Reader: conn, Writer: conn}, nil)
			if err != nil {
				return
			}
			// IOTransport does not propagate ctx cancellation into the
			// session (only a carrier like a one-shot HTTP request does),
			// so shutdown is driven here instead: closing the session
			// unblocks Wait below.
			go func() {
				<-ctx.Done()
				_ = sess.Close()
			}()
			sess.Wait()
		}()
	}
}

// startStdioSession runs an MCP session over transport in the background and
// cancels the shared run() context when the session ends, whatever the
// reason: transport EOF, a transport error, or ctx being cancelled from
// elsewhere. That cancellation is what lets a stdio client's disconnect end
// the whole process, even when PublicURL is set and the HTTP listener would
// otherwise have kept it running.
func startStdioSession(ctx context.Context, cancel context.CancelFunc, m *mcp.Server, transport mcp.Transport) {
	go func() {
		defer cancel()
		_ = m.Run(ctx, transport)
	}()
}

// pickMode maps the command line onto the three ways the binary runs. With
// no argument it behaves as stdio, so a bare "docker run -i image" speaks
// MCP on stdin, which is what every client and directory expects.
func pickMode(args []string, socketExists bool) string {
	if len(args) > 0 && args[0] == "serve" {
		return "serve"
	}
	if socketExists {
		return "bridge"
	}
	return "stdio-daemon"
}

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	sock := filepath.Join(os.Getenv("INDEX"), "mcp.sock")
	_, statErr := os.Stat(sock)
	var err error
	switch pickMode(os.Args[1:], statErr == nil) {
	case "serve":
		err = run(ctx, false)
	case "bridge":
		err = bridge(ctx, sock)
	default:
		err = run(ctx, true)
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, "your-mail-mcp:", err)
		os.Exit(1)
	}
}

func run(ctx context.Context, stdio bool) error {
	// One cancellable context for the whole run, shared by every goroutine
	// below including, when stdio is true, the HTTP shutdown goroutine
	// further down: stdin closing must be able to end the process even when
	// PublicURL is set, not just the local part of it.
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()

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
	syncer := newSyncer(cfg, e.Maildir, e.Index, mbsyncPath, nm)
	syncer.initMirror = e.InitMirror
	syncer.timeout = e.SyncTimeout

	srv := newServer(cfg, nm, e.Maildir)
	srv.publicURL = strings.TrimSuffix(e.PublicURL, "/")
	srv.index = e.Index
	srv.sync = detachedSync(ctx, syncer)
	srv.syncWait = syncer.Wait
	srv.syncKick = syncer.Kick
	srv.status = syncer.Status
	srv.syncBusy = syncer.Busy
	syncer.interval = e.SyncInterval

	// Config-tier exclusions touch no network, so they are applied inline
	// before the listener opens. Full discovery needs a live IMAP login per
	// account and must not delay startup waiting on an unreachable provider,
	// so it runs in a goroutine instead; repeated on its own ticker (below)
	// so an account that was unreachable at startup is not left unprotected
	// for the rest of the process's life. Trade-off: a discovered-tier
	// account is briefly unprotected right after a restart, until its first
	// discovery pass completes; a configured-tier account never is.
	applyConfiguredExclusions(cfg, srv)
	go refreshExclusions(ctx, cfg, srv)

	go runTicker(ctx, discoveryInterval, func(ctx context.Context) {
		refreshExclusions(ctx, cfg, srv)
	})

	sync := func(ctx context.Context) {
		if _, err := syncer.Sync(ctx, ""); err != nil && !errors.Is(err, errSyncBusy) {
			fmt.Fprintln(os.Stderr, "sync:", err)
		}
	}
	// Sync once at startup, not only on the tick. Without this the mailbox
	// stays empty for a whole SYNC_INTERVAL after the server comes up, which
	// on a first run looks exactly like a broken configuration. It runs in a
	// goroutine because a first mirror of a large mailbox takes far longer
	// than the listener should wait to open.
	go sync(ctx)
	// Not runTicker: a manual refresh resets the schedule, so that a pass
	// asked for by hand is not followed by a scheduled one moments later.
	go func() {
		t := time.NewTicker(e.SyncInterval)
		defer t.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-syncer.kick:
				t.Reset(e.SyncInterval)
			case <-t.C:
				sync(ctx)
			}
		}
	}()

	m := mcp.NewServer(&mcp.Implementation{Name: "your-mail-mcp", Version: "0.3.0"}, nil)
	srv.registerTools(m)

	sock := filepath.Join(e.Index, "mcp.sock")
	go func() {
		if err := serveSocket(ctx, sock, m); err != nil {
			fmt.Fprintln(os.Stderr, "socket:", err)
		}
	}()

	if stdio {
		// stdin is one more session. When the client closes it, the whole
		// process goes: a daemon that outlives its client is the orphan bug
		// every stdio server ships. startStdioSession cancels the shared ctx
		// above, so this reaches the HTTP shutdown goroutine too when
		// PublicURL is set, not just the early return right below.
		startStdioSession(ctx, cancel, m, &mcp.StdioTransport{})
		if e.PublicURL == "" {
			<-ctx.Done()
			return nil
		}
	}

	if e.PublicURL == "" {
		fmt.Fprintln(os.Stderr, "PUBLIC_URL unset: no HTTP listener, local sessions only")
		<-ctx.Done()
		return nil
	}
	o, err := newOAuth(filepath.Join(e.Index, "oauth.json"), e.PublicURL, e.Passphrase)
	if err != nil {
		return err
	}
	httpSrv := &http.Server{
		Addr:              e.ListenAddr,
		Handler:           newHTTPHandler(o, m, srv),
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
func bearerOK(o *oauthServer, r *http.Request) bool {
	token := strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer ")
	return token != "" && token != r.Header.Get("Authorization") && o.validAccessToken(token)
}

func requireBearer(o *oauthServer, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !bearerOK(o, r) {
			w.Header().Set("WWW-Authenticate",
				fmt.Sprintf("Bearer resource_metadata=%q", o.publicURL+"/.well-known/oauth-protected-resource/mcp"))
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		next.ServeHTTP(w, r)
	})
}

func newHTTPHandler(o *oauthServer, m *mcp.Server, srv *Server) http.Handler {
	mux := http.NewServeMux()
	// Discovery. Both documents are unauthenticated by necessity: a client
	// has to read them before it can obtain a token, and they carry only
	// endpoint URLs and supported algorithms.
	//
	// RFC 8414: where to register, authorize and exchange.
	mux.HandleFunc("/.well-known/oauth-authorization-server", o.handleASMetadata)
	// RFC 9728: which authorization server protects this resource. The URL is
	// built by inserting the well-known segment between the host and the
	// resource's path, so for a resource at PUBLIC_URL/mcp the canonical
	// location is the suffixed one. The bare path is the form for a resource
	// at the root, served as a fallback for a client that does not insert.
	mux.HandleFunc("/.well-known/oauth-protected-resource/mcp", o.handlePRMetadata)
	mux.HandleFunc("/.well-known/oauth-protected-resource", o.handlePRMetadata)
	mux.HandleFunc("/register", o.handleRegister)
	mux.HandleFunc("/authorize", o.handleAuthorize)
	mux.HandleFunc("/token", o.handleToken)

	streamable := mcp.NewStreamableHTTPHandler(func(*http.Request) *mcp.Server { return m }, nil)
	mux.Handle("/mcp", requireBearer(o, streamable))
	// Raw attachment bytes for anything too big for a tool response. A
	// bearer token works for scripted callers; a signed link works in a
	// plain browser (the attachment tool hands one out when it refuses an
	// oversized part).
	mux.HandleFunc("GET /attachment/{id}/{part}", func(w http.ResponseWriter, r *http.Request) {
		id := r.PathValue("id")
		part, err := strconv.Atoi(r.PathValue("part"))
		if err != nil {
			http.Error(w, "bad part", http.StatusBadRequest)
			return
		}
		if !srv.validAttachmentSig(r, id, part) && !bearerOK(o, r) {
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		srv.serveAttachment(w, r, id, part)
	})
	return mux
}
