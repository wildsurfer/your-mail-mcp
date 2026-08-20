package main

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
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
		"no accounts": {
			`{"accounts":[]}`,
			"no accounts",
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
	if e.SyncInterval != 5*time.Minute {
		t.Errorf("SyncInterval = %v, want the 5m default", e.SyncInterval)
	}
	if e.ListenAddr != ":8080" {
		t.Errorf("ListenAddr = %q, want the :8080 default", e.ListenAddr)
	}
}
