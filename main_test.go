package main

import (
	"context"
	"net"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

func writeConfig(t *testing.T, body string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "accounts.json")
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestLoadConfigExpandsSecretsAndFillsDefaults(t *testing.T) {
	t.Setenv("WORK_PASS", "s3cret")
	path := writeConfig(t, `{"accounts":[
		{"name":"work","host":"imap.gmail.com","user":"me@example.com","password":"${WORK_PASS}"}
	]}`)

	cfg, err := loadConfig(path)
	if err != nil {
		t.Fatalf("loadConfig: %v", err)
	}
	if len(cfg.Accounts) != 1 {
		t.Fatalf("got %d accounts, want 1", len(cfg.Accounts))
	}
	a := cfg.Accounts[0]
	if a.Password != "s3cret" {
		t.Errorf("password = %q, want the expanded value", a.Password)
	}
	if a.Port != 993 {
		t.Errorf("port = %d, want default 993", a.Port)
	}
	if a.TLS != "imaps" {
		t.Errorf("tls = %q, want default imaps", a.TLS)
	}
	if len(a.Patterns) != 1 || a.Patterns[0] != "*" {
		t.Errorf("patterns = %v, want [*]", a.Patterns)
	}
}

// TestLoadConfigPreservesLiteralDollarSign covers M4: os.ExpandEnv expands a
// bare $VAR as well as ${VAR}, so a literal password like "p$ssw0rd" was
// silently mangled to "p" (ssw0rd read as an unset variable name and
// expanded to nothing) with no error, and the account then failed to
// authenticate with an IMAP error that names nothing useful.
func TestLoadConfigPreservesLiteralDollarSign(t *testing.T) {
	path := writeConfig(t, `{"accounts":[
		{"name":"work","host":"imap.gmail.com","user":"me@example.com","password":"p$ssw0rd"}
	]}`)
	cfg, err := loadConfig(path)
	if err != nil {
		t.Fatalf("loadConfig: %v", err)
	}
	if cfg.Accounts[0].Password != "p$ssw0rd" {
		t.Errorf("password = %q, want the literal value preserved", cfg.Accounts[0].Password)
	}
}

// TestLoadConfigExpandsSpecialCharactersSafely covers F5: expansion used to
// splice the raw environment value into the file's text before parsing it as
// JSON. A value containing a `"` or a `\` then landed inside a JSON string
// unescaped and broke the document it was embedded in, so the whole accounts
// file failed to parse over a password some IMAP provider was happy to
// accept. Expanding per already-parsed field means the substituted value
// only has to be a valid Go string, never valid JSON.
func TestLoadConfigExpandsSpecialCharactersSafely(t *testing.T) {
	want := `p"ss\word`
	t.Setenv("WORK_PASS", want)
	path := writeConfig(t, `{"accounts":[
		{"name":"work","host":"imap.gmail.com","user":"me@example.com","password":"${WORK_PASS}"}
	]}`)

	cfg, err := loadConfig(path)
	if err != nil {
		t.Fatalf("loadConfig: %v", err)
	}
	if cfg.Accounts[0].Password != want {
		t.Errorf("password = %q, want %q byte-for-byte", cfg.Accounts[0].Password, want)
	}
}

func TestLoadConfigAllowsMissingFileAndNoAccounts(t *testing.T) {
	cfg, err := loadConfig(filepath.Join(t.TempDir(), "absent.json"))
	if err != nil || len(cfg.Accounts) != 0 {
		t.Fatalf("missing file: cfg=%v err=%v", cfg, err)
	}
	p := filepath.Join(t.TempDir(), "empty.json")
	if err := os.WriteFile(p, []byte(`{"accounts":[]}`), 0o600); err != nil {
		t.Fatal(err)
	}
	cfg, err = loadConfig(p)
	if err != nil || len(cfg.Accounts) != 0 {
		t.Fatalf("empty list: cfg=%v err=%v", cfg, err)
	}
}

func TestLoadConfigRejectsBadInput(t *testing.T) {
	cases := map[string]struct{ body, want string }{
		"duplicate names": {
			`{"accounts":[{"name":"a","host":"h","user":"u","password":"p"},
			              {"name":"a","host":"h","user":"u","password":"p"}]}`,
			"duplicate",
		},
		"slash in name": {
			`{"accounts":[{"name":"a/b","host":"h","user":"u","password":"p"}]}`,
			"name",
		},
		"missing host": {
			`{"accounts":[{"name":"a","user":"u","password":"p"}]}`,
			"host",
		},
		"unset secret": {
			`{"accounts":[{"name":"a","host":"h","user":"u","password":"${NOT_SET_ANYWHERE}"}]}`,
			"password",
		},
		"unknown tls": {
			`{"accounts":[{"name":"a","host":"h","user":"u","password":"p","tls":"wat"}]}`,
			"tls",
		},
		"space in host": {
			`{"accounts":[{"name":"a","host":"imap gmail.com","user":"u","password":"p"}]}`,
			"host",
		},
		"tab in user": {
			`{"accounts":[{"name":"a","host":"h","user":"me@example.com\ttab","password":"p"}]}`,
			"user",
		},
		"space in user": {
			`{"accounts":[{"name":"a","host":"h","user":"me user","password":"p"}]}`,
			"user",
		},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			_, err := loadConfig(writeConfig(t, tc.body))
			if err == nil {
				t.Fatal("want an error, got nil")
			}
			if !strings.Contains(err.Error(), tc.want) {
				t.Errorf("error %q does not mention %q", err, tc.want)
			}
		})
	}
}

// TestRefreshExclusionsAppliesConfiguredFoldersEvenWhenDiscoveryFails covers
// the rest of I4: discovery ran once, at startup only, so an account
// unreachable at that moment had nothing excluded for the entire life of the
// process. refreshExclusions is the extracted helper both the startup call
// and the sync ticker now share, so a later successful tick heals it; this
// checks the helper itself still applies a configured exclude_folders list
// even when the discovery connection fails outright (port 1 refuses
// immediately), matching excludedFolders' existing config-wins behavior.
func TestRefreshExclusionsAppliesConfiguredFoldersEvenWhenDiscoveryFails(t *testing.T) {
	cfg := &Config{Accounts: []Account{
		{Name: "work", Host: "127.0.0.1", Port: 1, TLS: "none", User: "u", Password: "p", ExcludeFolders: []string{"Rubbish"}},
	}}
	srv := newServer(cfg, nil, t.TempDir())

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	refreshExclusions(ctx, cfg, srv)

	got := srv.excludedFor("work")
	if len(got) != 1 || got[0] != "work/Rubbish" {
		t.Errorf("excluded = %v, want [work/Rubbish] applied despite the discovery connection failing", got)
	}
}

// TestRefreshExclusionsLeavesGoodListOnFailedDiscovery covers N4: a failed
// discovery tick called setExcluded with the empty list excludedFolders
// falls back to when there is nothing else, wiping out whatever a previous
// successful tick had established. One network blip on the discovery ticker
// therefore turned junk/trash exclusion off silently until a later tick
// happened to succeed. An account with no exclude_folders configured is the
// case that degrades, since a configured list always wins regardless.
func TestRefreshExclusionsLeavesGoodListOnFailedDiscovery(t *testing.T) {
	cfg := &Config{Accounts: []Account{
		{Name: "work", Host: "127.0.0.1", Port: 1, TLS: "none", User: "u", Password: "p"},
	}}
	srv := newServer(cfg, nil, t.TempDir())
	srv.setExcluded("work", []string{"work/Spam", "work/Trash"})

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	refreshExclusions(ctx, cfg, srv)

	got := srv.excludedFor("work")
	if len(got) != 2 || got[0] != "work/Spam" || got[1] != "work/Trash" {
		t.Errorf("excluded = %v, want the previous good list left intact after a failed discovery tick", got)
	}
}

// TestDiscoveryIntervalIsMuchLongerThanSync covers N5: discovery used to run
// on every sync tick, adding a full IMAP LOGIN per account every few minutes
// against providers that throttle. discoveryInterval is its own, much
// longer, ticker (see run) rather than a re-run condition tied to sync,
// since junk/trash folder names do not move; this guards against it
// silently regressing back to a short interval.
func TestDiscoveryIntervalIsMuchLongerThanSync(t *testing.T) {
	if discoveryInterval < time.Hour {
		t.Errorf("discoveryInterval = %v, want at least 1h so discovery does not add an IMAP login on every sync tick", discoveryInterval)
	}
}

func TestRunTickerFiresUntilCancelled(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	ticks := make(chan struct{}, 4)
	go runTicker(ctx, time.Millisecond, func(context.Context) { ticks <- struct{}{} })

	for i := 0; i < 2; i++ {
		select {
		case <-ticks:
		case <-time.After(2 * time.Second):
			t.Fatal("ticker did not fire")
		}
	}
	cancel()
}

func TestLoadEnvRequiresTheEssentials(t *testing.T) {
	t.Setenv("CONFIG", "")
	if _, err := loadEnv(); err == nil {
		t.Fatal("want an error when CONFIG is unset")
	}

	t.Setenv("CONFIG", "/tmp/accounts.json")
	t.Setenv("MAILDIR", "/mail")
	t.Setenv("INDEX", "/index")
	e, err := loadEnv()
	if err != nil {
		t.Fatal(err)
	}
	if e.SyncInterval != 10*time.Minute {
		t.Errorf("SyncInterval = %v, want the 10m default", e.SyncInterval)
	}
	if e.ListenAddr != ":8080" {
		t.Errorf("ListenAddr = %q, want the :8080 default", e.ListenAddr)
	}
	if e.SyncTimeout != time.Hour {
		t.Errorf("SyncTimeout = %v, want the 1h default", e.SyncTimeout)
	}
}

func requiredEnvForLoadEnv(t *testing.T) {
	t.Helper()
	t.Setenv("CONFIG", "/tmp/accounts.json")
	t.Setenv("MAILDIR", "/mail")
	t.Setenv("INDEX", "/index")
}

// TestLoadEnvRejectsNonPositiveDurations covers F4: a zero or negative
// SYNC_INTERVAL reaches time.NewTicker, which panics rather than returning
// an error, so a typo like "SYNC_INTERVAL=0" crashed the whole process
// instead of failing to start cleanly. SYNC_TIMEOUT feeds a per-account
// context.WithTimeout instead, where a non-positive value would just hand
// every sync a context that expires before it starts.
func TestLoadEnvRejectsNonPositiveDurations(t *testing.T) {
	for _, name := range []string{"SYNC_INTERVAL", "SYNC_TIMEOUT"} {
		for _, bad := range []string{"0", "0s", "-5m"} {
			t.Run(name+"="+bad, func(t *testing.T) {
				requiredEnvForLoadEnv(t)
				t.Setenv(name, bad)
				_, err := loadEnv()
				if err == nil {
					t.Fatalf("%s=%s: want an error, got nil", name, bad)
				}
				if !strings.Contains(err.Error(), name) {
					t.Errorf("error %q does not name %s", err, name)
				}
			})
		}
	}
}

// TestLoadEnvAcceptsAndAppliesSyncTimeout covers the rest of F8: SYNC_TIMEOUT
// overrides the 1h default and is threaded from loadEnv into the Syncer (see
// run in main.go); this checks the loadEnv half.
func TestLoadEnvAcceptsAndAppliesSyncTimeout(t *testing.T) {
	requiredEnvForLoadEnv(t)
	t.Setenv("SYNC_TIMEOUT", "30m")
	e, err := loadEnv()
	if err != nil {
		t.Fatal(err)
	}
	if e.SyncTimeout != 30*time.Minute {
		t.Errorf("SyncTimeout = %v, want 30m", e.SyncTimeout)
	}
}

func TestServeSocketAnswersToolsList(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	sock := filepath.Join(t.TempDir(), "mcp.sock")
	m := mcp.NewServer(&mcp.Implementation{Name: "t", Version: "0"}, nil)
	newServer(&Config{}, nil, t.TempDir()).registerTools(m)
	go func() { _ = serveSocket(ctx, sock, m) }()
	var conn net.Conn
	var err error
	for i := 0; i < 50; i++ {
		if conn, err = net.Dial("unix", sock); err == nil {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	if err != nil {
		t.Fatalf("socket never came up: %v", err)
	}
	defer conn.Close()
	c := mcp.NewClient(&mcp.Implementation{Name: "c", Version: "0"}, nil)
	sess, err := c.Connect(ctx, &mcp.IOTransport{Reader: conn, Writer: conn}, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer sess.Close()
	res, err := sess.ListTools(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Tools) != 11 {
		t.Fatalf("want 11 tools over the socket, got %d", len(res.Tools))
	}
}
