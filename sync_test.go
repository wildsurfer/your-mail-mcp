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

	// Once per channel, two channels per account.
	for _, directive := range []string{"Sync Pull", "Create Near", "Remove None", "Expunge None"} {
		if got := strings.Count(out, directive); got != 2*len(cfg.Accounts) {
			t.Errorf("%q appears %d times, want once per channel, two channels per account (%d)", directive, got, 2*len(cfg.Accounts))
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

func TestGenMbsyncrcExpungeLocal(t *testing.T) {
	cfg := &Config{Accounts: []Account{{
		Name: "work", Host: "h", Port: 993, User: "u", Password: "p",
		TLS: "imaps", Patterns: []string{"*"}, ExpungeLocal: true,
	}}}
	out := genMbsyncrc(cfg, "/mail")
	for _, directive := range []string{"Sync Pull", "Create Near", "Remove None", "Expunge Near"} {
		if got := strings.Count(out, directive); got != 2 {
			t.Errorf("%q appears %d times, want once per channel", directive, got)
		}
	}
	for _, forbidden := range []string{"Push", "Create Far", "Create Both", "Remove Near", "Remove Far", "Remove Both", "Expunge Far", "Expunge Both", "Expunge None"} {
		if strings.Contains(out, forbidden) {
			t.Errorf("generated configuration contains %q", forbidden)
		}
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

func TestGenMbsyncrcAddsRecentChannelPerAccount(t *testing.T) {
	cfg := &Config{Accounts: []Account{{Name: "work", Host: "h", User: "u", Password: "p"}}}
	got := genMbsyncrc(cfg, "/mail")
	for _, want := range []string{
		"Channel work-recent\n", "Patterns \"INBOX\"\n", "MaxMessages 1000\n", "ExpireUnread yes\n",
		"MaildirStore work-recent-local\n", "Path /mail/work-recent/\n",
		"Channel work-full\n",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("missing %q in:\n%s", want, got)
		}
	}
	// mbsync returns one exit code for a whole Group, which would hide a
	// succeeding full channel behind a failing recent one; the two channels
	// run as separate invocations instead, so no Group line exists.
	if strings.Contains(got, "Group") {
		t.Fatalf("no Group stanza expected:\n%s", got)
	}
	// The read-only guarantee applies to the recent channel too.
	recent := got[strings.Index(got, "Channel work-recent"):]
	for _, d := range []string{"Sync Pull\n", "Create Near\n", "Remove None\n", "Expunge None\n"} {
		if !strings.Contains(recent, d) {
			t.Fatalf("recent channel lacks %q", d)
		}
	}
}

func TestCompleteSetOnceFullPassSucceeds(t *testing.T) {
	s, _ := testSyncer(t)
	s.runCmd = func(_ context.Context, _ string, args ...string) (string, error) {
		if strings.HasPrefix(args[len(args)-1], "home") {
			return "", nil
		}
		return "", errors.New("down")
	}
	_, _ = s.Sync(context.Background(), "")
	st := s.Status()
	if !st["home"].Complete {
		t.Fatalf("home should be complete: %+v", st["home"])
	}
	if st["work"].Complete {
		t.Fatalf("work failed and must not be complete: %+v", st["work"])
	}
	s.runCmd = func(context.Context, string, ...string) (string, error) { return "", context.DeadlineExceeded }
	_, _ = s.Sync(context.Background(), "home")
	if !s.Status()["home"].Complete {
		t.Fatal("Complete is set once and never unset")
	}
}

// TestCompleteWhenRecentFailsButFullSucceeds guards the reason recent and
// full run as two invocations rather than one mbsync Group: a Group's exit
// code covers the whole group, so a failing recent channel would have
// looked exactly like a failing full channel and Complete would never be
// set for an account whose full mirror actually finished.
func TestCompleteWhenRecentFailsButFullSucceeds(t *testing.T) {
	s, _ := testSyncer(t)
	s.runCmd = func(_ context.Context, _ string, args ...string) (string, error) {
		if args[len(args)-1] == "home-recent" {
			return "", errors.New("recent: down")
		}
		return "", nil
	}
	_, _ = s.Sync(context.Background(), "home")
	st := s.Status()["home"]
	if !st.Complete || st.LastError != "" {
		t.Fatalf("a failed recent channel must not stop the full channel from completing: %+v", st)
	}
}

func TestRecentChannelStopsOnceComplete(t *testing.T) {
	s, calls := testSyncer(t)
	for i := 0; i < 2; i++ {
		if _, err := s.Sync(context.Background(), "home"); err != nil {
			t.Fatal(err)
		}
	}
	want := []string{"home-recent", "home-full", "home-full"}
	if strings.Join(*calls, " ") != strings.Join(want, " ") {
		t.Fatalf("want recent only until the full mirror completes, got %v", *calls)
	}
}

// The recent store is scaffolding; once the full channel has covered INBOX
// it holds only duplicate files, so a clean full pass deletes it. Under
// expunge_local this is also what keeps remotely-deleted mail from
// surviving in the second store.
func TestRecentStoreRemovedOnceComplete(t *testing.T) {
	s, _ := testSyncer(t)
	cur := filepath.Join(s.maildir, "home-recent", "INBOX", "cur")
	if err := os.MkdirAll(cur, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(cur, "msg:2,S"), []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	// A full pass: home's full channel succeeds, work's fails.
	if _, err := s.Sync(context.Background(), ""); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(s.maildir, "home-recent")); !os.IsNotExist(err) {
		t.Errorf("recent store still present after the full channel completed: %v", err)
	}
	if _, err := os.Stat(filepath.Join(s.maildir, "work-recent")); err != nil {
		t.Errorf("incomplete account's recent store must survive: %v", err)
	}
}

// Complete used to live only in memory, so every restart reran the recent
// channel — and, now that completion deletes its store, would have
// re-downloaded 1000 messages per restart just to delete them again.
func TestCompleteSurvivesRestart(t *testing.T) {
	s, _ := testSyncer(t)
	if _, err := s.Sync(context.Background(), "home"); err != nil {
		t.Fatal(err)
	}
	restarted := newSyncer(s.cfg, s.maildir, s.index, "/tmp/mbsyncrc", nil)
	var calls []string
	restarted.runCmd = func(_ context.Context, _ string, args ...string) (string, error) {
		calls = append(calls, args[len(args)-1])
		return "", nil
	}
	restarted.reindex = func(context.Context) (int, error) { return 0, nil }
	if !restarted.Status()["home"].Complete {
		t.Fatal("Complete did not survive the restart")
	}
	if restarted.Status()["work"].Complete {
		t.Fatal("the never-completed account must stay incomplete")
	}
	if _, err := restarted.Sync(context.Background(), "home"); err != nil {
		t.Fatal(err)
	}
	if strings.Join(calls, " ") != "home-full" {
		t.Fatalf("recent channel ran after a restart of a complete account: %v", calls)
	}
}

func TestNotCompleteWhenFullFails(t *testing.T) {
	s, _ := testSyncer(t)
	s.runCmd = func(_ context.Context, _ string, args ...string) (string, error) {
		if args[len(args)-1] == "home-full" {
			return "", errors.New("full: down")
		}
		return "", nil
	}
	_, _ = s.Sync(context.Background(), "home")
	if s.Status()["home"].Complete {
		t.Fatal("a failed full channel must not be marked complete")
	}
}

// TestSyncCreatesBothStoreRoots guards the failure only the live test caught:
// mbsync creates mailboxes inside a store but never the store's own root, and
// the recent channel is a second store with a second root. It uses the
// account whose full channel fails, since a full channel that succeeds now
// deletes the recent root again in the same pass.
func TestSyncCreatesBothStoreRoots(t *testing.T) {
	s, _ := testSyncer(t)
	_, _ = s.Sync(context.Background(), "work")
	for _, dir := range []string{"work", "work-recent"} {
		if _, err := os.Stat(filepath.Join(s.maildir, dir)); err != nil {
			t.Errorf("mbsync store root: %v", err)
		}
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
	var callsMu sync.Mutex
	s.runCmd = func(_ context.Context, _ string, args ...string) (string, error) {
		callsMu.Lock()
		calls = append(calls, args[len(args)-1])
		callsMu.Unlock()
		// Only work's full channel fails, so a test can still see work's
		// recent channel run (and, separately, exercise recent-fails cases).
		if args[len(args)-1] == "work-full" {
			return "", errors.New("AUTHENTICATIONFAILED")
		}
		return "", nil
	}
	s.reindex = func(context.Context) (int, error) { return 3, nil }
	return s, &calls
}

func TestSyncContinuesAfterOneAccountFails(t *testing.T) {
	s, calls := testSyncer(t)

	added, err := s.Sync(context.Background(), "")
	if err != nil {
		t.Fatalf("a failing account must not fail the pass: %v", err)
	}
	if added != 3 {
		t.Errorf("added = %d, want the reindex result 3", added)
	}
	if len(*calls) != 4 {
		t.Fatalf("mbsync ran %d times, want twice per account (recent, full): %v", len(*calls), *calls)
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
// indistinguishable from a quiet inbox. testSyncer's runCmd fails work's
// full channel.
func TestSyncFailsWhenEveryAttemptedAccountFails(t *testing.T) {
	s, _ := testSyncer(t)
	if _, err := s.Sync(context.Background(), "work"); err == nil {
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
	s.runCmd = func(context.Context, string, ...string) (string, error) {
		return "", errors.New("AUTHENTICATIONFAILED")
	}
	if _, err := s.Sync(context.Background(), ""); err == nil {
		t.Fatal("want an error: the only account failed")
	}
	if _, err := os.Stat(filepath.Join(index, "xapian")); err != nil {
		t.Errorf("notmuch new did not create the database despite the sync failing: %v", err)
	}
}

func TestSyncOneAccountIsAllFolders(t *testing.T) {
	s, calls := testSyncer(t)
	if _, err := s.Sync(context.Background(), "home"); err != nil {
		t.Fatal(err)
	}
	if len(*calls) != 2 || (*calls)[0] != "home-recent" || (*calls)[1] != "home-full" {
		t.Fatalf("want the recent channel then the full channel for the whole account, got %v", *calls)
	}
}

// blockingSyncer is a testSyncer whose runCmd parks until release is closed,
// sending on entered once per invocation so a test can wait until a pass is
// genuinely in flight before acting on it. n is how many invocations may
// enter before the test drains the channel.
func blockingSyncer(t *testing.T, n int) (s *Syncer, release, entered chan struct{}) {
	t.Helper()
	s, _ = testSyncer(t)
	release, entered = make(chan struct{}), make(chan struct{}, n)
	s.runCmd = func(context.Context, string, ...string) (string, error) {
		entered <- struct{}{}
		<-release
		return "", nil
	}
	return s, release, entered
}

func TestSyncWaitJoinsRunningPass(t *testing.T) {
	s, release, entered := blockingSyncer(t, 1)
	go func() { _, _ = s.Sync(context.Background(), "home") }()
	<-entered
	if s.Wait(context.Background(), 30*time.Millisecond) {
		t.Fatal("Wait returned done while the pass was still running")
	}
	st := s.Status()["home"]
	if !st.Running || st.StartedAt.IsZero() {
		t.Fatalf("running pass not reflected in status: %+v", st)
	}
	close(release)
	if !s.Wait(context.Background(), time.Second) {
		t.Fatal("Wait did not return done after the pass finished")
	}
	st = s.Status()["home"]
	if st.Running || st.LastDuration <= 0 {
		t.Fatalf("finished pass not reflected in status: %+v", st)
	}
}

// TestWaitOutlivesARefusedConcurrentSync is the ownership rule in Sync: the
// channel Wait blocks on belongs to the call that created it, and a second
// call refused with errSyncBusy must leave it alone. Closing it there would
// tell refresh the pass had finished the instant it collided with one.
func TestWaitOutlivesARefusedConcurrentSync(t *testing.T) {
	s, release, entered := blockingSyncer(t, 1)
	go func() { _, _ = s.Sync(context.Background(), "home") }()
	<-entered
	if _, err := s.Sync(context.Background(), "home"); !errors.Is(err, errSyncBusy) {
		t.Fatalf("second sync of a busy account returned %v, want errSyncBusy", err)
	}
	if s.Wait(context.Background(), 30*time.Millisecond) {
		t.Fatal("a refused sync ended the wait for the pass it collided with")
	}
	close(release)
	if !s.Wait(context.Background(), time.Second) {
		t.Fatal("Wait did not return done after the pass finished")
	}
}

// TestScheduledPassIsBusyWhenEveryAccountIs is finding 1: a pass over all
// accounts skips a busy account and carries on, which is right for the
// ticker but told refresh "0 new message(s)" while the first mirror was 3%
// done. A pass that ran nothing of its own reports busy, so refresh joins
// the pass in flight instead.
func TestScheduledPassIsBusyWhenEveryAccountIs(t *testing.T) {
	// Both accounts, so the second pass finds every lock taken.
	s, release, entered := blockingSyncer(t, 2)
	go func() { _, _ = s.Sync(context.Background(), "") }()
	<-entered
	<-entered

	if _, err := s.Sync(context.Background(), ""); !errors.Is(err, errSyncBusy) {
		t.Fatalf("a pass with every account already syncing returned %v, want errSyncBusy", err)
	}
	if s.Wait(context.Background(), 30*time.Millisecond) {
		t.Fatal("Wait returned done while the pass every account was busy with ran on")
	}
	close(release)
	if !s.Wait(context.Background(), time.Second) {
		t.Fatal("Wait did not return done after the pass finished")
	}
}

// TestWaitCoversEveryPassInFlight is finding 3: two passes can overlap on
// different accounts, and the first to finish must not report the second
// one done.
func TestWaitCoversEveryPassInFlight(t *testing.T) {
	s, _ := testSyncer(t)
	first, second := make(chan struct{}), make(chan struct{})
	entered := make(chan struct{}, 2)
	s.runCmd = func(_ context.Context, _ string, args ...string) (string, error) {
		name := args[len(args)-1]
		entered <- struct{}{}
		if strings.HasPrefix(name, "work") {
			<-first
		} else {
			<-second
		}
		return "", nil
	}
	go func() { _, _ = s.Sync(context.Background(), "work") }()
	<-entered
	go func() { _, _ = s.Sync(context.Background(), "home") }()
	<-entered

	close(first)
	if s.Wait(context.Background(), 50*time.Millisecond) {
		t.Fatal("Wait returned done while the second pass was still running")
	}
	close(second)
	if !s.Wait(context.Background(), time.Second) {
		t.Fatal("Wait did not return done after both passes finished")
	}
}

func TestDeadlineIsNotAFailure(t *testing.T) {
	s, _ := testSyncer(t)
	s.timeout = 20 * time.Millisecond
	s.runCmd = func(ctx context.Context, _ string, _ ...string) (string, error) {
		<-ctx.Done()
		// What exec.CommandContext actually returns when it kills mbsync on
		// the deadline. Not the sentinel, which is the whole point.
		return "", errors.New("mbsync: signal: killed")
	}
	for i := 0; i < 3; i++ {
		_, _ = s.Sync(context.Background(), "home")
	}
	if st := s.Status()["home"]; st.Failures != 0 || !st.NextRetry.IsZero() {
		t.Fatalf("a SYNC_TIMEOUT expiry must not back off: %+v", st)
	}
}

// TestDeadlineTranslatesARealKilledProcess runs the real exec path under a
// deadline, because the stubbed test above only proves the translation
// handles the string it is given. exec returns "signal: killed" when it
// kills the child, not the context error, and that is what production sees.
func TestDeadlineTranslatesARealKilledProcess(t *testing.T) {
	if _, err := exec.LookPath("sleep"); err != nil {
		t.Skip("sleep is not installed")
	}
	s, _ := testSyncer(t)
	s.timeout = 50 * time.Millisecond
	s.runCmd = func(ctx context.Context, _ string, _ ...string) (string, error) {
		return execCommand(ctx, "sleep", "5")
	}
	for i := 0; i < 2; i++ {
		_, _ = s.Sync(context.Background(), "home")
	}
	st := s.Status()["home"]
	if st.Failures != 0 || !st.NextRetry.IsZero() || !strings.Contains(st.LastError, "deadline") {
		t.Fatalf("a killed mbsync must record as a deadline, not a failure: %+v", st)
	}
}

func TestWaitReturnsOnCancelledContext(t *testing.T) {
	s, release, entered := blockingSyncer(t, 1)
	go func() { _, _ = s.Sync(context.Background(), "home") }()
	<-entered
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	start := time.Now()
	if s.Wait(ctx, time.Minute) {
		t.Fatal("Wait reported done while the pass was still running")
	}
	if time.Since(start) > time.Second {
		t.Fatal("Wait ignored the cancelled context")
	}
	close(release)
	if !s.Wait(context.Background(), time.Second) {
		t.Fatal("Wait did not return done after the pass finished")
	}
}

func TestKickNeverBlocks(t *testing.T) {
	s, _ := testSyncer(t)
	// Nobody is draining the channel; a second kick must not wedge refresh.
	s.Kick()
	s.Kick()
	select {
	case <-s.kick:
	default:
		t.Fatal("a kick was dropped instead of queued")
	}
}

func TestSyncIsSerialisedPerAccount(t *testing.T) {
	s, _ := testSyncer(t)
	release := make(chan struct{})
	entered := make(chan struct{})
	var once sync.Once
	s.runCmd = func(_ context.Context, _ string, args ...string) (string, error) {
		if strings.HasPrefix(args[len(args)-1], "home") {
			once.Do(func() { close(entered) })
			<-release
		}
		return "", nil
	}
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		_, _ = s.Sync(context.Background(), "home")
	}()
	<-entered

	// The same account is refused while its sync runs...
	if _, err := s.Sync(context.Background(), "home"); !errors.Is(err, errSyncBusy) {
		t.Fatalf("concurrent sync of the same account returned %v, want errSyncBusy", err)
	}
	// ...but a different account proceeds in parallel.
	if _, err := s.Sync(context.Background(), "work"); errors.Is(err, errSyncBusy) {
		t.Fatal("a different account was refused while home synced")
	}
	if !s.Busy() {
		t.Error("Busy() = false while an account is mid-sync")
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
	if _, err := s.Sync(context.Background(), ""); err == nil {
		t.Fatal("want a refusal for an empty plain directory")
	}

	s.initMirror = true
	if _, err := s.Sync(context.Background(), ""); err != nil {
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
		if _, err := s.Sync(context.Background(), ""); err == nil {
			t.Fatal("want a refusal: the sentinel says a mirror existed here before")
		}
	})

	t.Run("no sentinel proceeds on a mount-point stub without INIT_MIRROR", func(t *testing.T) {
		s, _ := testSyncer(t)
		if err := os.RemoveAll(filepath.Join(s.maildir, "work")); err != nil {
			t.Fatal(err)
		}
		s.mountPoint = func(string) bool { return true }
		if _, err := s.Sync(context.Background(), ""); err != nil {
			t.Fatalf("no sentinel + a real mount point should proceed with no opt-in: %v", err)
		}
	})

	t.Run("no sentinel still proceeds under INIT_MIRROR", func(t *testing.T) {
		s, _ := testSyncer(t)
		if err := os.RemoveAll(filepath.Join(s.maildir, "work")); err != nil {
			t.Fatal(err)
		}
		s.initMirror = true
		if _, err := s.Sync(context.Background(), ""); err != nil {
			t.Fatalf("no sentinel + INIT_MIRROR should proceed: %v", err)
		}
	})

	t.Run("a successful pass over a non-empty maildir writes the sentinel", func(t *testing.T) {
		s, _ := testSyncer(t)
		sentinel := filepath.Join(s.index, mirrorSentinel)
		if _, err := os.Stat(sentinel); err == nil {
			t.Fatal("test precondition: sentinel should not exist yet")
		}
		if _, err := s.Sync(context.Background(), "home"); err != nil {
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
	if _, err := s.Sync(context.Background(), ""); err != nil {
		t.Fatalf("a populated maildir needs no opt-in: %v", err)
	}
	if len(*calls) == 0 {
		t.Error("no account was synced")
	}
}

func TestSyncReportsAMissingMaildir(t *testing.T) {
	s, _ := testSyncer(t)
	s.maildir = filepath.Join(s.maildir, "definitely-not-here")
	_, err := s.Sync(context.Background(), "")
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
	s.runCmd = func(context.Context, string, ...string) (string, error) { return "", nil }
	if _, err := s.Sync(context.Background(), ""); err != nil {
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
	if _, err := s.Sync(context.Background(), "home"); err != nil {
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

	// testSyncer's runCmd fails work's full channel. Two scheduled passes:
	// the first failure retries normally, the second sets a backoff.
	for i := 0; i < 2; i++ {
		if _, err := s.Sync(context.Background(), ""); err != nil {
			t.Fatalf("pass %d: %v", i, err)
		}
	}
	workRuns := 0
	for _, c := range *calls {
		if strings.HasPrefix(c, "work") {
			workRuns++
		}
	}
	if workRuns != 4 {
		t.Fatalf("work ran %d times across two passes' recent and full channels, want 4", workRuns)
	}
	if s.Status()["work"].NextRetry.IsZero() {
		t.Fatal("two consecutive failures set no backoff")
	}

	// The third scheduled pass skips the backed-off account.
	before := len(*calls)
	if _, err := s.Sync(context.Background(), ""); err != nil {
		t.Fatal(err)
	}
	for _, c := range (*calls)[before:] {
		if strings.HasPrefix(c, "work") {
			t.Fatal("a backed-off account was synced by the scheduled pass")
		}
	}

	// A manual refresh of that account ignores the backoff.
	before = len(*calls)
	_, _ = s.Sync(context.Background(), "work")
	if len(*calls) != before+2 {
		t.Fatal("a manual refresh must bypass backoff")
	}

	// The healthy account never backs off.
	if !s.Status()["home"].NextRetry.IsZero() {
		t.Error("the healthy account has a backoff")
	}
}
