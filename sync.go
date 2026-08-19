package main

import "fmt"

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
