package main

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

// newFixture builds a maildir containing the given messages, writes a notmuch
// config for it and indexes it. msgs maps "account/Folder" to raw messages.
// Tests that need a real index call this; it skips when notmuch is absent so a
// clone without notmuch installed still runs the rest of the suite.
func newFixture(t *testing.T, msgs map[string][]string) (maildir, index, config string) {
	t.Helper()
	if _, err := exec.LookPath("notmuch"); err != nil {
		t.Skip("notmuch is not installed")
	}
	root := t.TempDir()
	maildir = filepath.Join(root, "mail")
	index = filepath.Join(root, "index")
	config = filepath.Join(root, "notmuch-config")

	for box, bodies := range msgs {
		for _, sub := range []string{"cur", "new", "tmp"} {
			if err := os.MkdirAll(filepath.Join(maildir, box, sub), 0o700); err != nil {
				t.Fatal(err)
			}
		}
		for i, body := range bodies {
			name := fmt.Sprintf("%d.fixture:2,S", i)
			if err := os.WriteFile(filepath.Join(maildir, box, "cur", name), []byte(body), 0o600); err != nil {
				t.Fatal(err)
			}
		}
	}
	if err := os.MkdirAll(index, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(config, []byte(genNotmuchConfig(maildir, index)), 0o600); err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command("notmuch", "new", "--quiet")
	cmd.Env = append(os.Environ(), "NOTMUCH_CONFIG="+config)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("notmuch new: %v\n%s", err, out)
	}
	return maildir, index, config
}

func message(from, to, subject, msgID, body string) string {
	return fmt.Sprintf("From: %s\r\nTo: %s\r\nSubject: %s\r\nMessage-ID: <%s>\r\nDate: Tue, 18 Aug 2026 10:00:00 +0000\r\n\r\n%s\r\n",
		from, to, subject, msgID, body)
}
