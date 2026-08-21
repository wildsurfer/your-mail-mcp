package main

import (
	"context"
	"errors"
	"io"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"
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
	if !strings.Contains(out, "SSLType IMAPS") || !strings.Contains(out, "SSLType STARTTLS") {
		t.Error("both TLS modes should appear, one per account")
	}
	// TLSType only exists in isync 1.5+; a 1.4.x mbsync refuses to parse a
	// file containing it at all, and the binary must run on both.
	if strings.Contains(out, "TLSType") {
		t.Error("TLSType is unknown to isync 1.4.x; the generated config must say SSLType")
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
	root := t.TempDir()
	maildir := filepath.Join(root, "mail")
	index := filepath.Join(root, "index")
	// A non-empty maildir is an existing mirror, which the guard passes.
	if err := os.MkdirAll(filepath.Join(maildir, "work"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(index, 0o700); err != nil {
		t.Fatal(err)
	}
	cfg := &Config{Accounts: []Account{{Name: "work"}, {Name: "home"}}}
	s := newSyncer(cfg, maildir, index, "/tmp/mbsyncrc", nil)
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

// TestSyncFailsWhenEveryAttemptedAccountFails covers F7: only a
// context-cancellation error stopped Sync from returning nil, so a pass
// where every attempted account failed still reported success — refreshTool
// then told the model "0 new message(s)" for what was really an outage,
// indistinguishable from a quiet inbox. testSyncer's runCmd fails for any
// target beginning "work".
func TestSyncFailsWhenEveryAttemptedAccountFails(t *testing.T) {
	s, _ := testSyncer(t)
	if _, err := s.Sync(context.Background(), "work", ""); err == nil {
		t.Fatal("want an error when the only attempted account fails")
	}
}

// TestSyncStillReindexesWhenEveryAccountFails guards a regression the F7
// fix above introduced during this fix wave, caught by the live end-to-end
// test: an early return skipping reindex on a totally-failed pass left the
// notmuch database uncreated on a first run, so every other tool call then
// failed with a raw "no such database" error instead of a graceful empty
// result. notmuch new must still run — only Sync's return value changes.
func TestSyncStillReindexesWhenEveryAccountFails(t *testing.T) {
	if _, err := exec.LookPath("notmuch"); err != nil {
		t.Skip("notmuch is not installed")
	}
	root := t.TempDir()
	maildir := filepath.Join(root, "mail")
	index := filepath.Join(root, "index")
	for _, sub := range []string{"cur", "new", "tmp"} {
		if err := os.MkdirAll(filepath.Join(maildir, "acct", "INBOX", sub), 0o700); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.MkdirAll(index, 0o700); err != nil {
		t.Fatal(err)
	}
	config := filepath.Join(root, "notmuch-config")
	if err := os.WriteFile(config, []byte(genNotmuchConfig(maildir, index)), 0o600); err != nil {
		t.Fatal(err)
	}

	s := newSyncer(&Config{Accounts: []Account{{Name: "acct"}}}, maildir, index, "/tmp/none", newNotmuch(config))
	s.runCmd = func(context.Context, string, ...string) error { return errors.New("AUTHENTICATIONFAILED") }
	if _, err := s.Sync(context.Background(), "", ""); err == nil {
		t.Fatal("want an error: the only account failed")
	}
	if _, err := os.Stat(filepath.Join(index, "xapian")); err != nil {
		t.Errorf("notmuch new did not create the database despite the sync failing: %v", err)
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

func TestSyncRefusesAnEmptyPlainDirectory(t *testing.T) {
	s, _ := testSyncer(t)
	// Strip it back to an empty plain directory: no mount, no content. That is
	// the ambiguous case — a fresh maildir and a path whose volume was never
	// mounted are indistinguishable — and the only one worth stopping for.
	if err := os.Remove(filepath.Join(s.maildir, "work")); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Sync(context.Background(), "", ""); err == nil {
		t.Fatal("want a refusal for an empty plain directory")
	}

	s.initMirror = true
	if _, err := s.Sync(context.Background(), "", ""); err != nil {
		t.Fatalf("INIT_MIRROR should allow it: %v", err)
	}
}

// TestCheckInitialisedSentinel covers F3: the mount-point check alone does
// not reliably tell an actually-missing mail volume from a genuine first run
// inside a container, where a bind-mounted or named volume does not always
// change device number from the container's point of view. mirrorSentinel is
// the second, container-effective signal: a marker in the INDEX directory —
// a separate volume from the maildir, so it survives the maildir vanishing —
// recording that a previous pass left the maildir non-empty.
func TestCheckInitialisedSentinel(t *testing.T) {
	t.Run("sentinel present refuses an empty maildir even when it looks mounted", func(t *testing.T) {
		s, _ := testSyncer(t)
		if err := os.RemoveAll(filepath.Join(s.maildir, "work")); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(s.index, mirrorSentinel), nil, 0o600); err != nil {
			t.Fatal(err)
		}
		// Stubbed true: the mount-point check alone is not reliable inside a
		// container (a bind-mounted or named volume does not always change
		// device number from the container's point of view), so this must
		// still refuse on the sentinel's word alone.
		s.mountPoint = func(string) bool { return true }
		if _, err := s.Sync(context.Background(), "", ""); err == nil {
			t.Fatal("want a refusal: the sentinel says a mirror existed here before")
		}
	})

	t.Run("no sentinel proceeds on a mount-point stub without INIT_MIRROR", func(t *testing.T) {
		s, _ := testSyncer(t)
		if err := os.RemoveAll(filepath.Join(s.maildir, "work")); err != nil {
			t.Fatal(err)
		}
		s.mountPoint = func(string) bool { return true }
		if _, err := s.Sync(context.Background(), "", ""); err != nil {
			t.Fatalf("no sentinel + a real mount point should proceed with no opt-in: %v", err)
		}
	})

	t.Run("no sentinel still proceeds under INIT_MIRROR", func(t *testing.T) {
		s, _ := testSyncer(t)
		if err := os.RemoveAll(filepath.Join(s.maildir, "work")); err != nil {
			t.Fatal(err)
		}
		s.initMirror = true
		if _, err := s.Sync(context.Background(), "", ""); err != nil {
			t.Fatalf("no sentinel + INIT_MIRROR should proceed: %v", err)
		}
	})

	t.Run("a successful pass over a non-empty maildir writes the sentinel", func(t *testing.T) {
		s, _ := testSyncer(t)
		sentinel := filepath.Join(s.index, mirrorSentinel)
		if _, err := os.Stat(sentinel); err == nil {
			t.Fatal("test precondition: sentinel should not exist yet")
		}
		if _, err := s.Sync(context.Background(), "home", ""); err != nil {
			t.Fatal(err)
		}
		if _, err := os.Stat(sentinel); err != nil {
			t.Errorf("sentinel was not written after a successful pass: %v", err)
		}
	})
}

func TestSyncAcceptsAnEmptyMaildirWithoutCeremony(t *testing.T) {
	s, calls := testSyncer(t)
	// A maildir that already holds an account directory is an existing mirror.
	// No marker file, no flag: the guard must not ask for either.
	if s.initMirror {
		t.Fatal("test precondition: initMirror should be false")
	}
	if _, err := s.Sync(context.Background(), "", ""); err != nil {
		t.Fatalf("a populated maildir needs no opt-in: %v", err)
	}
	if len(*calls) == 0 {
		t.Error("no account was synced")
	}
}

func TestSyncReportsAMissingMaildir(t *testing.T) {
	s, _ := testSyncer(t)
	s.maildir = filepath.Join(s.maildir, "definitely-not-here")
	_, err := s.Sync(context.Background(), "", "")
	if err == nil {
		t.Fatal("want an error when the maildir does not exist")
	}
	if !strings.Contains(err.Error(), "definitely-not-here") {
		t.Errorf("error should name the path: %v", err)
	}
}

func TestIsMountPointDistinguishesAPlainDirectory(t *testing.T) {
	// A temp directory is never a mount point; the filesystem root always is,
	// since its parent resolves to itself and stat reports the same device.
	if isMountPoint(t.TempDir()) {
		t.Error("a plain temp directory reported as a mount point")
	}
	if !isMountPoint("/") {
		t.Error("the filesystem root should report as a mount point")
	}
	if isMountPoint(filepath.Join(t.TempDir(), "missing")) {
		t.Error("a path that does not exist reported as a mount point")
	}
}

func TestDiscoverSpecialUseRespectsContextCancellation(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	go func() {
		conn, err := ln.Accept()
		if err != nil {
			return
		}
		defer conn.Close()
		_, _ = io.Copy(io.Discard, conn) // silent: never responds, returns once the client closes
	}()

	host, portStr, err := net.SplitHostPort(ln.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	port, err := strconv.Atoi(portStr)
	if err != nil {
		t.Fatal(err)
	}
	a := Account{Name: "stall", Host: host, Port: port, TLS: "none", User: "u", Password: "p"}

	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()

	done := make(chan struct{})
	go func() {
		discoverSpecialUse(ctx, a)
		close(done)
	}()

	select {
	case <-done:
	case <-time.After(3 * time.Second):
		t.Fatal("discoverSpecialUse did not return promptly after its context was cancelled; a stalled connection blocks it")
	}
}

// TestTranslateDelimConvertsToSlash covers F2: discoverSpecialUse used to
// return b.Mailbox verbatim, so a server whose hierarchy delimiter is not
// "/" (Dovecot's "." in INBOX.Trash is the common case) returned a name
// that never matches the "/" path SubFolders Verbatim actually wrote to
// disk, and excludedFolders' folder: query could never hit it.
func TestTranslateDelimConvertsToSlash(t *testing.T) {
	cases := []struct {
		name  string
		delim rune
		want  string
	}{
		{"INBOX.Trash", '.', "INBOX/Trash"},
		{"INBOX/Trash", '/', "INBOX/Trash"},
		{"INBOX.Trash", 0, "INBOX.Trash"},
	}
	for _, c := range cases {
		if got := translateDelim(c.name, c.delim); got != c.want {
			t.Errorf("translateDelim(%q, %q) = %q, want %q", c.name, c.delim, got, c.want)
		}
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
	got := excludedFolders(a, []string{"SpecialJunk"}, []string{"INBOX", "Rubbish", "Junk"})
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
	got := excludedFolders(a, nil, all)
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
// wellKnownJunk. Both special values are names wellKnownJunk would reject
// (a localised name and a name that isn't junk-shaped at all), so a
// regression that filtered special through the name list would drop them
// and fail this test.
func TestExcludedFoldersPassesThroughSpecialUseWhenNoConfig(t *testing.T) {
	// The regression this guards: special-use names must reach the result
	// unfiltered. "Papierkorb" and "Custom Archive" match nothing in the
	// name list, so a refactor that ran special through wellKnownJunk would
	// drop them. The union may add name-matched folders on top ("Deleted
	// Messages" here); it must never remove a special-use one.
	a := Account{Name: "work"}
	special := []string{"Papierkorb", "Custom Archive"}
	all := []string{"INBOX", "Papierkorb", "Custom Archive", "Deleted Messages"}
	got := excludedFolders(a, special, all)
	for _, want := range []string{"work/Papierkorb", "work/Custom Archive", "work/Deleted Messages"} {
		found := false
		for _, g := range got {
			if g == want {
				found = true
			}
		}
		if !found {
			t.Errorf("excludedFolders = %v, missing %q", got, want)
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

func TestReindexSurvivesAMissingDatabase(t *testing.T) {
	if _, err := exec.LookPath("notmuch"); err != nil {
		t.Skip("notmuch is not installed")
	}
	// A first run has an empty index directory: notmuch new is what creates
	// the database, so anything that reads it beforehand fails.
	root := t.TempDir()
	maildir := filepath.Join(root, "mail")
	index := filepath.Join(root, "index")
	for _, sub := range []string{"cur", "new", "tmp"} {
		if err := os.MkdirAll(filepath.Join(maildir, "acct", "INBOX", sub), 0o700); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.MkdirAll(index, 0o700); err != nil {
		t.Fatal(err)
	}
	config := filepath.Join(root, "notmuch-config")
	if err := os.WriteFile(config, []byte(genNotmuchConfig(maildir, index)), 0o600); err != nil {
		t.Fatal(err)
	}

	s := newSyncer(&Config{Accounts: []Account{{Name: "acct"}}}, maildir, index, "/tmp/none", newNotmuch(config))
	s.runCmd = func(context.Context, string, ...string) error { return nil }
	if _, err := s.Sync(context.Background(), "", ""); err != nil {
		t.Fatalf("first sync against an empty index failed: %v", err)
	}
	if _, err := os.Stat(filepath.Join(index, "xapian")); err != nil {
		t.Errorf("notmuch new did not create the database: %v", err)
	}
}

func TestSyncCreatesTheAccountDirectory(t *testing.T) {
	s, _ := testSyncer(t)
	// mbsync opens the store root; it does not create it. A fresh volume has
	// nothing under the maildir, so the server has to make it.
	if _, err := os.Stat(filepath.Join(s.maildir, "home")); err == nil {
		t.Fatal("test precondition: the account directory should not exist yet")
	}
	if _, err := s.Sync(context.Background(), "home", ""); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(s.maildir, "home")); err != nil {
		t.Errorf("account directory was not created: %v", err)
	}
}

func TestGenMbsyncrcForcesLoginOnlyWhenUnencrypted(t *testing.T) {
	// mbsync will not send LOGIN over a plain connection unless forced, so
	// tls:none needs it explicitly or it cannot authenticate at all.
	plain := genMbsyncrc(&Config{Accounts: []Account{
		{Name: "a", Host: "h", Port: 143, User: "u", Password: "p", TLS: "none", Patterns: []string{"*"}},
	}}, "/mail")
	if !strings.Contains(plain, "AuthMechs LOGIN") {
		t.Error("tls:none must force LOGIN, or mbsync refuses to authenticate")
	}

	// With TLS, leave the mechanism to mbsync.
	encrypted := genMbsyncrc(&Config{Accounts: []Account{
		{Name: "a", Host: "h", Port: 993, User: "u", Password: "p", TLS: "imaps", Patterns: []string{"*"}},
	}}, "/mail")
	if strings.Contains(encrypted, "AuthMechs") {
		t.Error("an encrypted connection should negotiate its own mechanism")
	}
}

func TestExcludedFoldersUnionsSpecialUseWithKnownNames(t *testing.T) {
	// iCloud in the field: LIST marks only \Trash ("Deleted Messages"), while
	// the junk folder is present by name alone. Both must be excluded.
	a := Account{Name: "icloud"}
	got := excludedFolders(a, []string{"Deleted Messages"}, []string{"INBOX", "Junk", "Deleted Messages", "Archive"})
	want := map[string]bool{"icloud/Deleted Messages": true, "icloud/Junk": true}
	if len(got) != len(want) {
		t.Fatalf("excludedFolders = %v, want exactly %v", got, want)
	}
	for _, f := range got {
		if !want[f] {
			t.Errorf("unexpected exclusion %q", f)
		}
	}
}

func TestSyncBacksOffAfterConsecutiveFailures(t *testing.T) {
	s, calls := testSyncer(t)

	// testSyncer's runCmd fails for targets beginning "work". Two scheduled
	// passes: the first failure retries normally, the second sets a backoff.
	for i := 0; i < 2; i++ {
		if _, err := s.Sync(context.Background(), "", ""); err != nil {
			t.Fatalf("pass %d: %v", i, err)
		}
	}
	workRuns := 0
	for _, c := range *calls {
		if strings.HasPrefix(c, "work") {
			workRuns++
		}
	}
	if workRuns != 2 {
		t.Fatalf("work ran %d times in two passes, want 2", workRuns)
	}
	if s.Status()["work"].NextRetry.IsZero() {
		t.Fatal("two consecutive failures set no backoff")
	}

	// The third scheduled pass skips the backed-off account.
	if _, err := s.Sync(context.Background(), "", ""); err != nil {
		t.Fatal(err)
	}
	for _, c := range (*calls)[len(*calls)-1:] {
		if strings.HasPrefix(c, "work") {
			t.Fatal("a backed-off account was synced by the scheduled pass")
		}
	}

	// A manual refresh of that account ignores the backoff.
	before := len(*calls)
	_, _ = s.Sync(context.Background(), "work", "INBOX")
	if len(*calls) != before+1 {
		t.Fatal("a manual refresh must bypass backoff")
	}

	// The healthy account never backs off.
	if !s.Status()["home"].NextRetry.IsZero() {
		t.Error("the healthy account has a backoff")
	}
}
