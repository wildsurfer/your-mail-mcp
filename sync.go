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
		b.WriteString("TLSType " + tls + "\n")
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

		b.WriteString("\nChannel " + a.Name + "\n")
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
type Syncer struct {
	cfg          *Config
	maildir      string
	mbsyncConfig string
	initMirror   bool
	timeout      time.Duration

	runCmd  func(ctx context.Context, name string, args ...string) error
	reindex func(ctx context.Context) (int, error)

	running sync.Mutex

	mu     sync.Mutex
	status map[string]AccountStatus
}

func newSyncer(cfg *Config, maildir, mbsyncConfig string, nm *Notmuch) *Syncer {
	return &Syncer{
		cfg:          cfg,
		maildir:      maildir,
		mbsyncConfig: mbsyncConfig,
		timeout:      15 * time.Minute,
		status:       map[string]AccountStatus{},
		runCmd: func(ctx context.Context, name string, args ...string) error {
			cmd := exec.CommandContext(ctx, name, args...)
			out, err := cmd.CombinedOutput()
			if err != nil {
				return fmt.Errorf("%s: %w: %s", name, err, strings.TrimSpace(string(out)))
			}
			return nil
		},
		reindex: func(ctx context.Context) (int, error) {
			before, err := nm.count(ctx, "*")
			if err != nil {
				return 0, err
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

// Sync mirrors one account (or all of them, when account is "") and reindexes
// once. A failing account is recorded and skipped rather than aborting the
// pass: with several accounts configured, one expired password must not stop
// the rest.
func (s *Syncer) Sync(ctx context.Context, account, folder string) (int, error) {
	if !s.running.TryLock() {
		return 0, errSyncBusy
	}
	defer s.running.Unlock()

	if err := s.checkInitialised(); err != nil {
		return 0, err
	}

	for _, a := range s.cfg.Accounts {
		if account != "" && a.Name != account {
			continue
		}
		target := a.Name
		if folder != "" {
			target = a.Name + ":" + folder
		}
		// Each account gets its own deadline, so a provider that stops
		// responding mid-sync cannot hold the lock and stall every later
		// refresh.
		accountCtx, cancel := context.WithTimeout(ctx, s.timeout)
		err := s.runCmd(accountCtx, "mbsync", "-c", s.mbsyncConfig, target)
		cancel()
		s.record(a.Name, err)
		if err != nil {
			// A failing account is skipped, not silent: anyone watching
			// container logs should see it without knowing to call folders.
			fmt.Fprintf(os.Stderr, "sync: account %s: %v\n", a.Name, err)
		}
		if ctx.Err() != nil {
			return 0, ctx.Err()
		}
	}
	return s.reindex(ctx)
}

// checkInitialised refuses to sync into a maildir that looks like a missing
// volume rather than a first run.
//
// The question that matters is the one the reference implementation asked of
// /Volumes/2TB: is the storage actually there? An empty directory answers it
// only in combination with whether that directory is a mount point. A mounted
// volume that is empty is a genuine first run and needs no ceremony. A plain
// empty directory is ambiguous — it is equally a fresh maildir and a path
// typo whose real storage was never mounted — and that is the only case worth
// stopping for, since syncing into it re-downloads every account into a
// directory that vanishes the moment the mount appears.
func (s *Syncer) checkInitialised() error {
	if _, err := os.Stat(s.maildir); err != nil {
		return fmt.Errorf("maildir %s: %w", s.maildir, err)
	}
	entries, err := os.ReadDir(s.maildir)
	if s.initMirror || isMountPoint(s.maildir) || (err == nil && len(entries) > 0) {
		return nil
	}
	return fmt.Errorf("maildir %s is an empty plain directory, not a mount point: refusing to sync, since a path whose volume was never mounted looks exactly like this and would trigger a full re-download. Mount the storage there, or set INIT_MIRROR=1 if it really is meant to be a directory on this filesystem", s.maildir)
}

func (s *Syncer) record(account string, err error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	st := s.status[account]
	if err != nil {
		st.LastError = err.Error()
	} else {
		st.LastError = ""
		st.LastSync = time.Now()
	}
	s.status[account] = st
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
		return nil, nil, err
	}
	for _, b := range boxes {
		all = append(all, b.Mailbox)
		for _, attr := range b.Attrs {
			if attr == imap.MailboxAttrJunk || attr == imap.MailboxAttrTrash {
				special = append(special, b.Mailbox)
				break
			}
		}
	}
	_ = c.Logout().Wait()
	return special, all, nil
}

// excludedFolders returns the folders to keep out of search for one account, as
// notmuch folder paths. Config wins, then SPECIAL-USE, then the name list run
// over every discovered mailbox.
func excludedFolders(a Account, special, all []string) []string {
	names := a.ExcludeFolders
	if len(names) == 0 {
		names = special
	}
	if len(names) == 0 {
		names = wellKnownJunk(all)
	}
	out := make([]string, 0, len(names))
	for _, n := range names {
		out = append(out, a.Name+"/"+n)
	}
	sort.Strings(out)
	return out
}
