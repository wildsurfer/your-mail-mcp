package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"
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
// The password is written into the file. The file lives in a tmpfs inside the
// container with mode 0600, so it is no more exposed than the environment it
// came from.
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
		b.WriteString("Port " + itoa(a.Port) + "\n")
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

func itoa(i int) string { return strconv.Itoa(i) }

// markerFile records that this maildir has been initialised. Without it, a
// mistyped or unmounted volume would look like an empty mailbox and mbsync
// would re-download every account into a directory that disappears the moment
// the real volume mounts.
const markerFile = ".your-mail-mcp-initialised"

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

// checkInitialised refuses to sync into a maildir that has never been
// initialised, unless initMirror opts in. Passing this check leaves the
// marker behind so later syncs need no flag.
func (s *Syncer) checkInitialised() error {
	path := filepath.Join(s.maildir, markerFile)
	if _, err := os.Stat(path); err == nil {
		return nil
	}
	if !s.initMirror {
		return fmt.Errorf("maildir %s has no %s marker: refusing to sync, since an unmounted or mistyped volume would look empty and trigger a full re-download; set INIT_MIRROR=1 for the first sync", s.maildir, markerFile)
	}
	return os.WriteFile(path, []byte(time.Now().UTC().Format(time.RFC3339)+"\n"), 0o600)
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
