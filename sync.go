package main

import (
	"fmt"
	"path/filepath"
	"strconv"
	"strings"
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
		b.WriteString("Patterns " + strings.Join(a.Patterns, " ") + "\n")
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
