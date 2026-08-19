package main

import (
	"strings"
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
	if !strings.Contains(out, "Patterns INBOX Archive") {
		t.Error("per-account patterns are missing")
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
