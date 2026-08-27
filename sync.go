package main

import (
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/emersion/go-imap/v2"
	"github.com/emersion/go-imap/v2/imapclient"
)

// AccountStatus is what the folders tool reports so a broken account is
// visible without reading container logs. Defined here ahead of the syncer
// (Task 7/Task 10) because Server.status, added in this task, already needs
// the type to name its return value.
type AccountStatus struct {
	LastSync  time.Time
	LastError string
	// Failures counts consecutive failed syncs; NextRetry is when the
	// scheduled ticker may try this account again. A manual refresh of one
	// account ignores both.
	Failures  int
	NextRetry time.Time
	// Running, StartedAt and LastDuration let a client that asked for a
	// refresh and got "in progress" decide how long to wait.
	Running      bool
	StartedAt    time.Time
	LastDuration time.Duration
	// Complete is set the first time the full channel exits 0 within
	// SYNC_TIMEOUT, and never unset. Pulled and Total are mbsync's own
	// counter from the last pass, so a client can see how far a first
	// mirror has got without the server issuing any IMAP command.
	Complete bool
	Pulled   int
	Total    int
}

// genNotmuchConfig writes the index configuration. mail_root points at the
// maildir root so folder queries read "account/INBOX". Maildir flags are the
// single source of truth for read, flagged and draft state, so no tags are
// applied at index time.
func genNotmuchConfig(maildir, index string) string {
	return fmt.Sprintf(`[database]
path=%s
mail_root=%s

[new]
tags=
ignore=.mbsyncstate;.mbsyncstate.new;.mbsyncstate.journal;.mbsyncstate.lock;.uidvalidity;.isyncuidmap.db;.DS_Store

[search]
exclude_tags=deleted

[maildir]
synchronize_flags=true
`, index, maildir)
}

// genMbsyncrc writes one channel per account. The file is generated rather than
// mounted for two reasons: the four read-only directives cannot be edited into
// something that pushes, and a stray blank line cannot silently demote them to
// global options.
//
// The password is written into the file. The file lives on the ordinary
// container filesystem, in a directory only this process writes to, at mode
// 0600, so it is no more exposed than the environment it came from.
func genMbsyncrc(cfg *Config, maildir string) string {
	var b strings.Builder
	b.WriteString("# generated at startup; edits are discarded on restart\n")
	for _, a := range cfg.Accounts {
		tls := "IMAPS"
		if a.TLS == "starttls" {
			tls = "STARTTLS"
		} else if a.TLS == "none" {
			tls = "None"
		}
		local := filepath.Join(maildir, a.Name) + string(filepath.Separator)

		b.WriteString("\nIMAPAccount " + a.Name + "\n")
		b.WriteString("Host " + a.Host + "\n")
		b.WriteString("Port " + strconv.Itoa(a.Port) + "\n")
		b.WriteString("User " + a.User + "\n")
		b.WriteString("Pass " + quoteMbsync(a.Password) + "\n")
		// SSLType, not TLSType: TLSType only exists in isync 1.5+, while
		// SSLType works everywhere — 1.5 merely prints a deprecation notice.
		// The image ships 1.5.x, but the binary also runs outside it, on
		// distributions still carrying 1.4.x, and this is the one spelling
		// that works on both.
		b.WriteString("SSLType " + tls + "\n")
		if a.TLS == "none" {
			// mbsync refuses to send LOGIN over an unencrypted connection
			// unless told to, which leaves tls:none unable to authenticate at
			// all. Someone who sets tls:none has already accepted that the
			// password crosses the network in clear, so say so explicitly
			// rather than shipping an option that cannot work.
			b.WriteString("AuthMechs LOGIN\n")
		}
		// Pinned: providers throttle above one command in flight, and the cost
		// is first-sync speed only.
		b.WriteString("PipelineDepth 1\n")
		b.WriteString("Timeout 60\n")

		b.WriteString("\nIMAPStore " + a.Name + "-remote\n")
		b.WriteString("Account " + a.Name + "\n")

		b.WriteString("\nMaildirStore " + a.Name + "-local\n")
		b.WriteString("Path " + local + "\n")
		b.WriteString("Inbox " + filepath.Join(local, "INBOX") + "\n")
		// Pinned: reproduces the server hierarchy verbatim. mbsync discovers the
		// server's delimiter itself.
		b.WriteString("SubFolders Verbatim\n")

		b.WriteString("\nChannel " + a.Name + "-full\n")
		b.WriteString("Far :" + a.Name + "-remote:\n")
		b.WriteString("Near :" + a.Name + "-local:\n")
		pats := make([]string, len(a.Patterns))
		for i, p := range a.Patterns {
			pats[i] = quoteMbsync(p)
		}
		b.WriteString("Patterns " + strings.Join(pats, " ") + "\n")
		// The read-only guarantee. Do not add a blank line above this comment.
		b.WriteString("Sync Pull\n")
		b.WriteString("Create Near\n")
		b.WriteString("Remove None\n")
		b.WriteString("Expunge None\n")
		b.WriteString("SyncState *\n")
		b.WriteString("CopyArrivalDate yes\n")

		// A second, small channel so today's mail is searchable within
		// minutes of a first run, while the full mirror takes as long as
		// the provider's quota allows. MaxMessages fetches only the newest
		// UIDs and ignores the rest; notmuch merges the overlap by
		// Message-ID once the full channel catches up. Expiry under
		// MaxMessages is near-side only and Expunge None keeps even that
		// from deleting a file.
		recentLocal := filepath.Join(maildir, a.Name+"-recent") + string(filepath.Separator)
		b.WriteString("\nMaildirStore " + a.Name + "-recent-local\n")
		b.WriteString("Path " + recentLocal + "\n")
		b.WriteString("Inbox " + filepath.Join(recentLocal, "INBOX") + "\n")
		b.WriteString("SubFolders Verbatim\n")
		b.WriteString("\nChannel " + a.Name + "-recent\n")
		b.WriteString("Far :" + a.Name + "-remote:\n")
		b.WriteString("Near :" + a.Name + "-recent-local:\n")
		b.WriteString("Patterns \"INBOX\"\n")
		b.WriteString("MaxMessages 1000\n")
		b.WriteString("Sync Pull\n")
		b.WriteString("Create Near\n")
		b.WriteString("Remove None\n")
		b.WriteString("Expunge None\n")
		b.WriteString("SyncState *\n")
		b.WriteString("CopyArrivalDate yes\n")
		// The group runs recent first, then full, on one connection at a
		// time: mbsync processes group members sequentially. The group is
		// named after the account, not "-group", so mbsync -c cfg <name>
		// keeps working unchanged; mbsync does not allow a Group and a
		// Channel to share a name, which is why the full channel above is
		// named "-full" instead of bare <name>.
		b.WriteString("\nGroup " + a.Name + "\n")
		b.WriteString("Channels " + a.Name + "-recent " + a.Name + "-full\n")
	}
	return b.String()
}

func quoteMbsync(s string) string {
	r := strings.NewReplacer(`\`, `\\`, `"`, `\"`)
	return `"` + r.Replace(s) + `"`
}

// isMountPoint reports whether path is the root of a mounted filesystem, by
// comparing its device number with its parent's. A bind mount, a Docker volume
// and a mounted disk all differ from their parent; a plain directory does not.
func isMountPoint(path string) bool {
	fi, err := os.Stat(path)
	if err != nil {
		return false
	}
	dir := filepath.Dir(path)
	if dir == path {
		return true // the filesystem root is its own parent
	}
	parent, err := os.Stat(dir)
	if err != nil {
		return false
	}
	a, ok1 := fi.Sys().(*syscall.Stat_t)
	b, ok2 := parent.Sys().(*syscall.Stat_t)
	if !ok1 || !ok2 {
		return false
	}
	return a.Dev != b.Dev
}

var errSyncBusy = errors.New("sync already running")

// Syncer runs mbsync and reindexes. running.TryLock is the single-sync-at-a-time
// guard: two overlapping mbsync runs against the same maildir corrupt the near
// side, so a second concurrent call is refused rather than queued.
// mirrorSentinel is the marker file written into the index directory after a
// sync pass leaves the maildir non-empty. It lives in INDEX rather than in
// the maildir itself deliberately: a marker inside the maildir vanishes
// along with it if that volume goes missing, which is exactly the case it
// needs to detect, so it has to live on the other side of the volume
// boundary to still be there when the maildir isn't.
const mirrorSentinel = "mirror-exists"

type Syncer struct {
	cfg          *Config
	maildir      string
	index        string
	mbsyncConfig string
	initMirror   bool
	timeout      time.Duration
	// interval is the scheduled sync cadence, used as the backoff base so a
	// provider that keeps refusing (quota, outage) is retried at 2x, 4x...
	// the normal cadence, capped at an hour, instead of hammered every tick.
	interval time.Duration

	runCmd  func(ctx context.Context, name string, args ...string) (string, error)
	reindex func(ctx context.Context) (int, error)
	// mountPoint defaults to the package-level isMountPoint; overridden in
	// tests, since a real mount point is not reproducible in one.
	mountPoint func(path string) bool

	// accountLocks serialises syncs of one account: two overlapping mbsync
	// runs on the same store corrupt the near side. Different accounts are
	// different stores and different connections, so they run in parallel —
	// a provider that parks one account's connection no longer starves the
	// others. reindexing stays serialised: notmuch new takes the database
	// write lock.
	accountLocks map[string]*sync.Mutex
	reindexing   sync.Mutex

	mu     sync.Mutex
	status map[string]AccountStatus
	// done is closed when the pass in flight finishes; nil when none is.
	// Wait uses it so a refresh that arrives mid-pass joins rather than
	// starts another. Guarded by mu.
	done chan struct{}
	// inflight counts the Sync calls sharing done. Guarded by mu.
	inflight int
	// kick is read by the ticker loop in run() to reset its schedule after
	// a manual refresh. Buffered so Kick never blocks.
	kick chan struct{}
}

func newSyncer(cfg *Config, maildir, index, mbsyncConfig string, nm *Notmuch) *Syncer {
	return &Syncer{
		cfg:          cfg,
		maildir:      maildir,
		index:        index,
		mbsyncConfig: mbsyncConfig,
		timeout:      time.Hour,
		interval:     10 * time.Minute,
		accountLocks: accountLocks(cfg),
		kick:         make(chan struct{}, 1),
		mountPoint:   isMountPoint,
		status:       map[string]AccountStatus{},
		runCmd: func(ctx context.Context, name string, args ...string) (string, error) {
			cmd := exec.CommandContext(ctx, name, args...)
			out, err := cmd.CombinedOutput()
			if err != nil {
				return string(out), fmt.Errorf("%s: %w: %s", name, err, strings.TrimSpace(string(out)))
			}
			return string(out), nil
		},
		reindex: func(ctx context.Context) (int, error) {
			// A missing database is the normal state on a first run: notmuch
			// new is what creates it. The count is only here to report how
			// many messages arrived, so failing to count must not stop the
			// indexing that the whole sync exists to do.
			before, err := nm.count(ctx, "*")
			if err != nil {
				before = 0
			}
			if _, err := nm.run(ctx, "new", "--quiet"); err != nil {
				return 0, err
			}
			after, err := nm.count(ctx, "*")
			if err != nil {
				return 0, err
			}
			return after - before, nil
		},
	}
}

// Sync mirrors every folder of one account (or of all of them, when account
// is "") and reindexes once. A failing account is recorded and skipped rather than aborting the
// pass: with several accounts configured, one expired password must not stop
// the rest.
func (s *Syncer) Sync(ctx context.Context, account string) (int, error) {
	if err := s.checkInitialised(); err != nil {
		return 0, err
	}

	// The pass in flight is one channel, whichever call started it, so a
	// second caller that arrives mid-pass waits for the same finish rather
	// than starting another. inflight counts the calls holding it, and only
	// the last one out closes it. A count rather than a single owner
	// because two things break otherwise: a call refused with errSyncBusy
	// below would close the channel of the pass it collided with, and an
	// owner that finishes first would report done while a slower pass on
	// another account, started later, is still downloading.
	s.mu.Lock()
	if s.done == nil {
		s.done = make(chan struct{})
	}
	done := s.done
	s.inflight++
	s.mu.Unlock()
	defer func() {
		s.mu.Lock()
		s.inflight--
		if s.inflight == 0 {
			close(done)
			s.done = nil
		}
		s.mu.Unlock()
	}()

	var (
		wg                      sync.WaitGroup
		resMu                   sync.Mutex
		attempted, failed, busy int
		lastErr                 error
	)
	for _, a := range s.cfg.Accounts {
		if account != "" && a.Name != account {
			continue
		}
		if account == "" {
			s.mu.Lock()
			retry := s.status[a.Name].NextRetry
			s.mu.Unlock()
			if time.Now().Before(retry) {
				fmt.Fprintf(os.Stderr, "sync: account %s: backing off until %s\n",
					a.Name, retry.UTC().Format(time.RFC3339))
				continue
			}
		}
		lock := s.accountLocks[a.Name]
		if lock == nil {
			return 0, fmt.Errorf("unknown account %q", account)
		}
		if !lock.TryLock() {
			// A named account is the caller's whole request; refusing tells
			// them. On a scheduled pass a busy account just isn't due.
			if account != "" {
				return 0, errSyncBusy
			}
			busy++
			continue
		}
		wg.Add(1)
		go func(a Account) {
			defer wg.Done()
			defer lock.Unlock()
			s.mu.Lock()
			st := s.status[a.Name]
			st.Running, st.StartedAt = true, time.Now()
			s.status[a.Name] = st
			s.mu.Unlock()
			out, err := s.syncAccount(ctx, a.Name)
			s.record(a.Name, out, err)
			resMu.Lock()
			defer resMu.Unlock()
			attempted++
			if err != nil {
				failed++
				lastErr = err
				// A failing account is skipped, not silent: anyone watching
				// container logs should see it without knowing to call folders.
				fmt.Fprintf(os.Stderr, "sync: account %s: %v\n", a.Name, err)
			}
		}(a)
	}
	wg.Wait()
	if ctx.Err() != nil {
		return 0, ctx.Err()
	}
	// reindex always runs, even when every account just failed: notmuch new
	// is what creates the database on a first run, and skipping it here
	// would leave every other tool call failing with a raw, unwrapped
	// "no such database" error instead of the graceful empty result an
	// index that merely has nothing new in it returns. Serialised: notmuch
	// new takes the database write lock.
	s.markMirrored()
	s.reindexing.Lock()
	added, reindexErr := s.reindex(ctx)
	s.reindexing.Unlock()
	// A pass where every attempted account failed is not a success — without
	// this, it looked exactly like a quiet inbox to refreshTool, which
	// reports "0 new message(s)" for both. A multi-account pass with at
	// least one success keeps today's skip-and-continue semantics.
	if attempted > 0 && failed == attempted {
		return 0, lastErr
	}
	// Every eligible account was already syncing, so this pass ran nothing
	// of its own. Report that the same way a named busy account does: a
	// refresh then joins the pass in flight, instead of reporting the zero
	// this pass would otherwise return while a first mirror is 3% done. The
	// ticker ignores errSyncBusy, so its skip-and-continue is unchanged.
	if attempted == 0 && busy > 0 {
		return 0, errSyncBusy
	}
	return added, reindexErr
}

// syncAccount runs one mbsync for one account under its own deadline, so a
// provider that stops responding mid-sync cannot stall anything but itself.
func (s *Syncer) syncAccount(ctx context.Context, name string) (string, error) {
	// mbsync creates mailboxes inside a store, but not the store's own
	// root, so a first run against a fresh volume fails with "cannot open
	// store" until this directory exists.
	if err := os.MkdirAll(filepath.Join(s.maildir, name), 0o700); err != nil {
		return "", err
	}
	accountCtx, cancel := context.WithTimeout(ctx, s.timeout)
	defer cancel()
	out, err := s.runCmd(accountCtx, "mbsync", "-c", s.mbsyncConfig, name)
	if err != nil && accountCtx.Err() != nil && ctx.Err() == nil {
		// exec kills mbsync when the deadline passes, so what comes back is
		// an ExitError reading "signal: killed", never the context error.
		// Translate it, or record cannot tell an interrupted download from
		// a provider refusing, and backs off a mirror that was working.
		return out, fmt.Errorf("sync deadline %s reached: %w", s.timeout, context.DeadlineExceeded)
	}
	return out, err
}

// markMirrored records, in the index directory, that a mirror exists at
// s.maildir — see mirrorSentinel and checkInitialised. Best-effort: a
// failure to write it just means the next empty-maildir check falls back to
// the mount-point heuristic, not a failure of the sync pass that already
// succeeded.
func (s *Syncer) markMirrored() {
	entries, err := os.ReadDir(s.maildir)
	if err != nil || len(entries) == 0 {
		return
	}
	_ = os.WriteFile(filepath.Join(s.index, mirrorSentinel), nil, 0o600)
}

// checkInitialised refuses to sync into a maildir that looks like a missing
// volume rather than a first run.
//
// The question that matters is the one the reference implementation asked of
// /Volumes/2TB: is the storage actually there? An empty directory answers it
// only in combination with two other signals. The first is whether that
// directory is a mount point: a mounted volume that is empty is a genuine
// first run and needs no ceremony. That check alone is not enough in a
// container, though, where a bind-mounted or named volume does not reliably
// change device number from the container's point of view, so an actually
// missing mail volume can still look mounted. The second signal, checked
// first because it is the stronger evidence of the two, is mirrorSentinel: a
// marker in the INDEX directory (a separate volume, so it survives the
// maildir vanishing) recording that a previous pass here left the maildir
// non-empty. An empty maildir next to that memory means the mail volume went
// missing, not that this is day one — refuse regardless of what the
// mount-point check says. Only once both signals come back negative does the
// remaining case — a plain empty directory, equally a fresh maildir and a
// path typo whose real storage was never mounted — get refused, since
// syncing into it re-downloads every account into a directory that vanishes
// the moment the mount appears.
//
// Residual gap: if the maildir and the index vanish together (the whole data
// volume is gone, not just the mail one), the sentinel disappears with it and
// this still reads as a first run. There is no signal left inside the
// container to catch that case.
func (s *Syncer) checkInitialised() error {
	if _, err := os.Stat(s.maildir); err != nil {
		return fmt.Errorf("maildir %s: %w", s.maildir, err)
	}
	entries, err := os.ReadDir(s.maildir)
	if s.initMirror || (err == nil && len(entries) > 0) {
		return nil
	}
	if _, err := os.Stat(filepath.Join(s.index, mirrorSentinel)); err == nil {
		return fmt.Errorf("maildir %s is empty but index %s remembers a previous mirror: refusing to sync, since this looks like a missing volume rather than a first run. Mount the storage there, or set INIT_MIRROR=1 if it really is meant to start over", s.maildir, s.index)
	}
	if s.mountPoint(s.maildir) {
		return nil
	}
	return fmt.Errorf("maildir %s is an empty plain directory, not a mount point: refusing to sync, since a path whose volume was never mounted looks exactly like this and would trigger a full re-download. Mount the storage there, or set INIT_MIRROR=1 if it really is meant to be a directory on this filesystem", s.maildir)
}

// progressLine matches mbsync's per-pass counter: N: +pulled/total is the
// new-message tally for the near side. It is the only progress figure that
// needs no IMAP command of our own.
var progressLine = regexp.MustCompile(`N: \+(\d+)/(\d+)`)

func parseProgress(out string) (pulled, total int, ok bool) {
	m := progressLine.FindAllStringSubmatch(out, -1)
	if len(m) == 0 {
		return 0, 0, false
	}
	last := m[len(m)-1]
	pulled, _ = strconv.Atoi(last[1])
	total, _ = strconv.Atoi(last[2])
	return pulled, total, true
}

func (s *Syncer) record(account, out string, err error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	st := s.status[account]
	st.Running = false
	st.LastDuration = time.Since(st.StartedAt)
	if p, t, ok := parseProgress(out); ok {
		st.Pulled, st.Total = p, t
	}
	if errors.Is(err, context.DeadlineExceeded) {
		// SYNC_TIMEOUT expired mid-download. That is progress interrupted,
		// not a refusal: mbsync journals per message, and the next pass
		// resumes. Backing off here would halve a multi-day first mirror.
		st.LastError = "sync deadline reached; will resume next pass"
		s.status[account] = st
		return
	}
	if err != nil {
		st.LastError = err.Error()
		st.Failures++
		// The first failure retries at the normal cadence: transient blips
		// should not slow a healthy account. From the second consecutive
		// failure the delay doubles each time, capped at an hour, which
		// turns a day-long provider lockout into ~30 attempts, not ~300.
		if st.Failures >= 2 {
			delay := s.interval
			for i := 2; i <= st.Failures && delay < time.Hour; i++ {
				delay *= 2
			}
			if delay > time.Hour {
				delay = time.Hour
			}
			st.NextRetry = time.Now().Add(delay)
		}
	} else {
		st.LastError = ""
		st.LastSync = time.Now()
		st.Failures = 0
		st.NextRetry = time.Time{}
		st.Complete = true
	}
	s.status[account] = st
}

func accountLocks(cfg *Config) map[string]*sync.Mutex {
	locks := make(map[string]*sync.Mutex, len(cfg.Accounts))
	for _, a := range cfg.Accounts {
		locks[a.Name] = &sync.Mutex{}
	}
	return locks
}

// Busy reports whether any account is syncing right now. It probes the
// per-account locks rather than keeping a flag that could drift.
func (s *Syncer) Busy() bool {
	for _, l := range s.accountLocks {
		if !l.TryLock() {
			return true
		}
		l.Unlock()
	}
	return false
}

// Wait blocks up to d for the pass in flight. Returns true when no pass is
// running by the time it returns, false when one still is.
func (s *Syncer) Wait(ctx context.Context, d time.Duration) bool {
	s.mu.Lock()
	done := s.done
	s.mu.Unlock()
	if done == nil {
		return true
	}
	select {
	case <-done:
		return true
	case <-time.After(d):
		return false
	case <-ctx.Done():
		return false
	}
}

// Kick asks the ticker loop to restart its interval from now, so a manual
// refresh is not followed by a scheduled pass moments later.
func (s *Syncer) Kick() {
	select {
	case s.kick <- struct{}{}:
	default:
	}
}

// Status returns a copy of the per-account sync state, safe to read while a
// sync is in progress.
func (s *Syncer) Status() map[string]AccountStatus {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make(map[string]AccountStatus, len(s.status))
	for k, v := range s.status {
		out[k] = v
	}
	return out
}

// junkNames is the fallback for servers that do not advertise RFC 6154. It is a
// list of common English spellings and nothing more: folder names are localised
// (Papierkorb, Corbeille, Papelera, Корзина), so this cannot be complete and is
// not meant to be. SPECIAL-USE is the real mechanism; a user on a server that
// lacks it and does not speak English sets exclude_folders.
var junkNames = []string{"junk", "spam", "trash", "deleted messages", "deleted items", "bulk mail"}

// discoveryDialTimeout bounds connecting to an account's IMAP server during
// SPECIAL-USE discovery.
const discoveryDialTimeout = 30 * time.Second

// discoveryTimeout bounds the rest of the conversation once the connection is
// up. Neither Login nor List has a deadline of its own: a server that accepts
// the connection and then goes quiet (a stateful firewall dropping the flow,
// a provider throttling by stalling, a load balancer holding the socket)
// would otherwise hang here forever, and this loop runs before the process
// starts listening, so nothing else in the server can make progress either.
const discoveryTimeout = 30 * time.Second

func wellKnownJunk(folders []string) []string {
	var out []string
	for _, f := range folders {
		name := strings.ToLower(f)
		// Strip a leading namespace so [Gmail]/Spam and INBOX.Trash match.
		if i := strings.LastIndexAny(name, "/."); i >= 0 {
			name = name[i+1:]
		}
		for _, known := range junkNames {
			if name == known {
				out = append(out, f)
				break
			}
		}
	}
	return out
}

// discoverSpecialUse asks the server which mailboxes carry \Junk and \Trash. It
// returns those alongside every mailbox name LIST returned, so a caller with no
// SPECIAL-USE result can still fall back to name matching over the full list.
//
// This is the only IMAP conversation in the process: it connects, issues LIST,
// reads folder attributes and closes. It never selects a mailbox and never
// fetches a message, and the client does not escape this function.
func discoverSpecialUse(ctx context.Context, a Account) (special, all []string, err error) {
	addr := net.JoinHostPort(a.Host, strconv.Itoa(a.Port))
	// Bounds the connect: without it, an unreachable or silently-dropping
	// host would hang here with no way to cancel it, since this loop runs at
	// startup before the server begins listening.
	dialer := &net.Dialer{Timeout: discoveryDialTimeout}
	var c *imapclient.Client
	switch a.TLS {
	case "starttls":
		c, err = imapclient.DialStartTLS(addr, &imapclient.Options{Dialer: dialer})
	case "none":
		c, err = imapclient.DialInsecure(addr, &imapclient.Options{Dialer: dialer})
	default:
		c, err = imapclient.DialTLS(addr, &imapclient.Options{TLSConfig: &tls.Config{ServerName: a.Host}, Dialer: dialer})
	}
	if err != nil {
		return nil, nil, err
	}
	defer c.Close()

	// Closing the connection is the only way to unblock a pending Login or
	// List call. This ties that to both the caller's context (shutdown) and
	// a fixed deadline (a server that stops responding mid-conversation), so
	// a stalled discovery cannot make the process unkillable.
	stop := context.AfterFunc(ctx, func() { c.Close() })
	defer stop()
	timer := time.AfterFunc(discoveryTimeout, func() { c.Close() })
	defer timer.Stop()

	if err := c.Login(a.User, a.Password).Wait(); err != nil {
		return nil, nil, err
	}
	boxes, err := c.List("", "*", &imap.ListOptions{ReturnSpecialUse: true}).Collect()
	if err != nil {
		// iCloud advertises SPECIAL-USE yet rejects the LIST-EXTENDED RETURN
		// syntax with a parse error (observed live, 2026-08-20). A plain LIST
		// still carries the special-use attributes on servers implementing
		// RFC 6154 without LIST-EXTENDED, and even a bare mailbox list feeds
		// the name fallback, so retry once without the option.
		boxes, err = c.List("", "*", nil).Collect()
	}
	if err != nil {
		return nil, nil, err
	}
	for _, b := range boxes {
		name := translateDelim(b.Mailbox, b.Delim)
		all = append(all, name)
		for _, attr := range b.Attrs {
			if attr == imap.MailboxAttrJunk || attr == imap.MailboxAttrTrash {
				special = append(special, name)
				break
			}
		}
	}
	_ = c.Logout().Wait()
	return special, all, nil
}

// translateDelim converts an IMAP mailbox name's hierarchy delimiter — the
// character the LIST response for that mailbox reported, which is server
// and namespace specific (Dovecot's "." in INBOX.Trash, most others' "/") —
// to the "/" a maildir path and a notmuch folder: query both use. delim == 0
// means the server did not report one; that and delim == '/' are both
// no-ops.
func translateDelim(name string, delim rune) string {
	if delim == 0 || delim == '/' {
		return name
	}
	return strings.ReplaceAll(name, string(delim), "/")
}

// excludedFolders returns the folders to keep out of search for one account, as
// notmuch folder paths. Config wins, then SPECIAL-USE, then the name list run
// over every discovered mailbox.
func excludedFolders(a Account, special, all []string) []string {
	names := a.ExcludeFolders
	if len(names) == 0 {
		// Union of both discovery signals, deduplicated. The tiers used to be
		// exclusive, and the field showed why that loses mail it should not:
		// iCloud marks only \Trash in its LIST response, so a special-use
		// list of one folder won its tier and "Junk" — present by name —
		// stayed searchable. A server that marks some folders still gets the
		// name matching for the rest.
		seen := map[string]bool{}
		for _, n := range append(append([]string{}, special...), wellKnownJunk(all)...) {
			if !seen[n] {
				seen[n] = true
				names = append(names, n)
			}
		}
	}
	out := make([]string, 0, len(names))
	for _, n := range names {
		out = append(out, a.Name+"/"+n)
	}
	sort.Strings(out)
	return out
}
