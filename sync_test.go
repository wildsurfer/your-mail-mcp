package main

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
)

func TestGenMbsyncrcIsPullOnlyForEveryAccount(t *testing.T) {
	cfg := &Config{Accounts: []Account{
		{Name: "work", Host: "imap.gmail.com", Port: 993, User: "me", Password: "p1", TLS: "imaps", Patterns: []string{"*"}},
		{Name: "home", Host: "mail.example.org", Port: 143, User: "me2", Password: "p2", TLS: "starttls", Patterns: []string{"INBOX", "Archive"}},
	}}
	out := genMbsyncrc(cfg, "/mail")

	for _, directive := range []string{"Sync Pull", "Create Near", "Remove None", "Expunge None"} {
		if got := strings.Count(out, directive); got != 2 {
			t.Errorf("%q appears %d times, want once per account (2)", directive, got)
		}
	}
	if !strings.Contains(out, "PipelineDepth 1") {
		t.Error("pipeline depth must be pinned to 1")
	}
	if !strings.Contains(out, "SubFolders Verbatim") {
		t.Error("SubFolders must be pinned to Verbatim")
	}
	if strings.Contains(out, "AuthMechs") {
		t.Error("AuthMechs must be left unset so mbsync negotiates")
	}
	if !strings.Contains(out, "TLSType IMAPS") || !strings.Contains(out, "TLSType STARTTLS") {
		t.Error("both TLS modes should appear, one per account")
	}
	if !strings.Contains(out, `Patterns "INBOX" "Archive"`) {
		t.Error("per-account patterns are missing or not quoted")
	}
	if !strings.Contains(out, "Path /mail/work/") || !strings.Contains(out, "Path /mail/home/") {
		t.Error("each account must have its own directory under the maildir root")
	}
}

// A blank line ends a section in mbsyncrc. One inside a Channel block turns the
// four read-only directives into inert global options, silently.
func TestGenMbsyncrcHasNoBlankLinesInsideSections(t *testing.T) {
	cfg := &Config{Accounts: []Account{
		{Name: "work", Host: "h", Port: 993, User: "u", Password: "p", TLS: "imaps", Patterns: []string{"*"}},
	}}
	inSection := false
	for i, line := range strings.Split(genMbsyncrc(cfg, "/mail"), "\n") {
		switch {
		case strings.TrimSpace(line) == "":
			inSection = false
		case strings.HasPrefix(line, "#"):
		case strings.HasPrefix(line, "IMAPAccount"), strings.HasPrefix(line, "IMAPStore"),
			strings.HasPrefix(line, "MaildirStore"), strings.HasPrefix(line, "Channel"):
			inSection = true
		default:
			if !inSection {
				t.Fatalf("line %d is a directive outside any section: %q", i+1, line)
			}
		}
	}
}

func TestGenMbsyncrcEscapesPasswords(t *testing.T) {
	cfg := &Config{Accounts: []Account{
		{Name: "work", Host: "h", Port: 993, User: "u", Password: `pa"ss\word`, TLS: "imaps", Patterns: []string{"*"}},
	}}
	if !strings.Contains(genMbsyncrc(cfg, "/mail"), `Pass "pa\"ss\\word"`) {
		t.Error("password quoting is wrong; mbsync would read a truncated secret")
	}
}

func TestGenMbsyncrcQuotesPatterns(t *testing.T) {
	cfg := &Config{Accounts: []Account{
		{Name: "work", Host: "h", Port: 993, User: "u", Password: "p", TLS: "imaps", Patterns: []string{"Sent Items", "INBOX"}},
	}}
	out := genMbsyncrc(cfg, "/mail")
	if !strings.Contains(out, `Patterns "Sent Items" "INBOX"`) {
		t.Error("patterns must be individually quoted; got: " + out)
	}
}

func TestGenMbsyncrcQuotesWildcardPattern(t *testing.T) {
	cfg := &Config{Accounts: []Account{
		{Name: "work", Host: "h", Port: 993, User: "u", Password: "p", TLS: "imaps", Patterns: []string{"*"}},
	}}
	out := genMbsyncrc(cfg, "/mail")
	if !strings.Contains(out, `Patterns "*"`) {
		t.Error("wildcard pattern must be quoted")
	}
}

func TestGenMbsyncrcQuotesNegationPattern(t *testing.T) {
	cfg := &Config{Accounts: []Account{
		{Name: "work", Host: "h", Port: 993, User: "u", Password: "p", TLS: "imaps", Patterns: []string{"!Trash"}},
	}}
	out := genMbsyncrc(cfg, "/mail")
	if !strings.Contains(out, `Patterns "!Trash"`) {
		t.Error("negation pattern must be quoted")
	}
}

func testSyncer(t *testing.T) (*Syncer, *[]string) {
	t.Helper()
	maildir := t.TempDir()
	if err := os.WriteFile(filepath.Join(maildir, markerFile), []byte("ok"), 0o600); err != nil {
		t.Fatal(err)
	}
	cfg := &Config{Accounts: []Account{{Name: "work"}, {Name: "home"}}}
	s := newSyncer(cfg, maildir, "/tmp/mbsyncrc", nil)
	var calls []string
	s.runCmd = func(_ context.Context, _ string, args ...string) error {
		calls = append(calls, args[len(args)-1])
		if strings.HasPrefix(args[len(args)-1], "work") {
			return errors.New("AUTHENTICATIONFAILED")
		}
		return nil
	}
	s.reindex = func(context.Context) (int, error) { return 3, nil }
	return s, &calls
}

func TestSyncContinuesAfterOneAccountFails(t *testing.T) {
	s, calls := testSyncer(t)

	added, err := s.Sync(context.Background(), "", "")
	if err != nil {
		t.Fatalf("a failing account must not fail the pass: %v", err)
	}
	if added != 3 {
		t.Errorf("added = %d, want the reindex result 3", added)
	}
	if len(*calls) != 2 {
		t.Fatalf("mbsync ran %d times, want once per account: %v", len(*calls), *calls)
	}

	st := s.Status()
	if st["work"].LastError == "" {
		t.Error("the failing account has no recorded error")
	}
	if st["home"].LastError != "" {
		t.Errorf("the healthy account recorded an error: %q", st["home"].LastError)
	}
	if st["home"].LastSync.IsZero() {
		t.Error("the healthy account has no last-sync time")
	}
}

func TestSyncOneAccountAndFolder(t *testing.T) {
	s, calls := testSyncer(t)
	if _, err := s.Sync(context.Background(), "home", "INBOX"); err != nil {
		t.Fatal(err)
	}
	if len(*calls) != 1 || (*calls)[0] != "home:INBOX" {
		t.Errorf("calls = %v, want [home:INBOX]", *calls)
	}
}

func TestSyncIsSerialised(t *testing.T) {
	s, _ := testSyncer(t)
	release := make(chan struct{})
	entered := make(chan struct{})
	s.runCmd = func(context.Context, string, ...string) error {
		close(entered)
		<-release
		return nil
	}
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		_, _ = s.Sync(context.Background(), "home", "")
	}()
	<-entered

	if _, err := s.Sync(context.Background(), "work", ""); !errors.Is(err, errSyncBusy) {
		t.Fatalf("second concurrent sync returned %v, want errSyncBusy", err)
	}
	close(release)
	wg.Wait()
}

func TestSyncRefusesAnUninitialisedMaildir(t *testing.T) {
	s, _ := testSyncer(t)
	if err := os.Remove(filepath.Join(s.maildir, markerFile)); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Sync(context.Background(), "", ""); err == nil {
		t.Fatal("want a refusal when the maildir is not initialised")
	}

	s.initMirror = true
	if _, err := s.Sync(context.Background(), "", ""); err != nil {
		t.Fatalf("INIT_MIRROR should allow the first sync: %v", err)
	}
	if _, err := os.Stat(filepath.Join(s.maildir, markerFile)); err != nil {
		t.Error("the first sync should leave the marker behind")
	}
}

func TestWellKnownJunkMatchesCommonNames(t *testing.T) {
	got := wellKnownJunk([]string{
		"INBOX", "Archive", "Junk", "Deleted Messages", "[Gmail]/Spam", "INBOX.Trash", "Projects",
		"SPAM", "Deleted Items",
	})
	want := map[string]bool{
		"Junk": true, "Deleted Messages": true, "[Gmail]/Spam": true, "INBOX.Trash": true,
		"SPAM": true, "Deleted Items": true,
	}
	if len(got) != len(want) {
		t.Fatalf("wellKnownJunk = %v, want %d entries", got, len(want))
	}
	for _, f := range got {
		if !want[f] {
			t.Errorf("wellKnownJunk returned unexpected folder %q", f)
		}
	}
}

// Config wins even when SPECIAL-USE discovery also found something: "SpecialJunk"
// is a discovered special-use mailbox, but the account's own ExcludeFolders must
// take priority over it.
func TestExcludedFoldersPrefersConfigAndPrefixesTheAccount(t *testing.T) {
	a := Account{Name: "work", ExcludeFolders: []string{"Rubbish"}}
	got := excludedFolders(context.Background(), a, []string{"SpecialJunk"}, []string{"INBOX", "Rubbish", "Junk"})
	for _, want := range []string{"work/Rubbish"} {
		if !contains(got, want) {
			t.Errorf("excludedFolders = %v, want it to contain %q", got, want)
		}
	}
	if contains(got, "work/SpecialJunk") {
		t.Errorf("excludedFolders = %v, config must win over discovered SPECIAL-USE folders", got)
	}
	for _, f := range got {
		if !strings.HasPrefix(f, "work/") {
			t.Errorf("folder %q is not prefixed with the account name", f)
		}
	}
}

// With no config and no SPECIAL-USE result, the third tier (name matching over
// the full LIST) must fire: junk-shaped names are excluded, localised ones that
// the English name list cannot recognise are left alone.
func TestExcludedFoldersFallsBackToWellKnownNamesWhenSpecialUseIsEmpty(t *testing.T) {
	a := Account{Name: "work"}
	all := []string{"INBOX", "Archive", "[Gmail]/Spam", "INBOX.Trash", "Papierkorb"}
	got := excludedFolders(context.Background(), a, nil, all)
	for _, want := range []string{"work/[Gmail]/Spam", "work/INBOX.Trash"} {
		if !contains(got, want) {
			t.Errorf("excludedFolders = %v, want it to contain %q", got, want)
		}
	}
	if contains(got, "work/Papierkorb") {
		t.Errorf("excludedFolders = %v, the English name list must not match a localised name", got)
	}
	if contains(got, "work/INBOX") || contains(got, "work/Archive") {
		t.Errorf("excludedFolders = %v, non-junk folders must not be excluded", got)
	}
}

// With no configured exclusions and a non-empty SPECIAL-USE result, tier 2
// must pass special through unfiltered and prefixed, not run it through
// wellKnownJunk. "Deleted Messages" sits in all but not in special: a
// regression that ran all (rather than special) through wellKnownJunk would
// pull it in too and fail the length check below.
func TestExcludedFoldersPassesThroughSpecialUseWhenNoConfig(t *testing.T) {
	a := Account{Name: "work"}
	special := []string{"Junk", "Trash"}
	all := []string{"INBOX", "Junk", "Trash", "Archive", "Deleted Messages"}
	got := excludedFolders(context.Background(), a, special, all)
	want := []string{"work/Junk", "work/Trash"}
	if len(got) != len(want) {
		t.Fatalf("excludedFolders = %v, want exactly %v", got, want)
	}
	for i, w := range want {
		if got[i] != w {
			t.Errorf("excludedFolders = %v, want exactly %v", got, want)
			break
		}
	}
}

func contains(list []string, s string) bool {
	for _, v := range list {
		if v == s {
			return true
		}
	}
	return false
}
