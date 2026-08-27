package main

import (
	"context"
	"os"
	"strings"
	"testing"
)

// TestNotmuchRunUsesMinimalEnvironment covers M3: notmuch subprocesses
// inherited the full process environment, including mail account passwords
// that have no business being visible to notmuch.
func TestNotmuchRunUsesMinimalEnvironment(t *testing.T) {
	t.Setenv("WORK_PASS", "hunter2")

	tmpdir := t.TempDir()
	fake := tmpdir + "/notmuch"
	if err := os.WriteFile(fake, []byte("#!/bin/sh\nenv\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	oldPath := os.Getenv("PATH")
	t.Setenv("PATH", tmpdir+":"+oldPath)

	n := newNotmuch("/some/config")
	out, err := n.run(context.Background(), "count", "*")
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(out), "WORK_PASS") {
		t.Errorf("notmuch subprocess inherited an unrelated environment variable:\n%s", out)
	}
	if !strings.Contains(string(out), "NOTMUCH_CONFIG=/some/config") {
		t.Errorf("notmuch subprocess is missing NOTMUCH_CONFIG:\n%s", out)
	}
}

func TestValidateQuery(t *testing.T) {
	ok := []string{
		"from:alice",
		"tag:unread and date:yesterday..today",
		`subject:"re: lunch tomorrow"`,
		"folder:work/INBOX",
		"*",
		"invoice",
		"thread:0000000000000abc",
	}
	for _, q := range ok {
		if err := validateQuery(q); err != nil {
			t.Errorf("validateQuery(%q) = %v, want nil", q, err)
		}
	}

	bad := []string{
		"fom:alice",    // typo, would silently match nothing
		"sender:alice", // not a notmuch prefix
		"folder:INBOX and x:1",
		"subjet:\"x\"", // typo immediately followed by quote
	}
	for _, q := range bad {
		err := validateQuery(q)
		if err == nil {
			t.Errorf("validateQuery(%q) = nil, want an error", q)
			continue
		}
		if !strings.Contains(err.Error(), "prefix") {
			t.Errorf("validateQuery(%q) error %q should explain the unknown prefix", q, err)
		}
	}
}

func TestExtractTags(t *testing.T) {
	// "tag:foo" inside a quoted subject value is realistic mail content, not
	// hypothetical, and must not be read as a tag term — nor tag:"a b", whose
	// value is itself quoted.
	got := extractTags(`tag:work subject:"see tag:foo for details" tag:"a b"`)
	if len(got) != 1 || got[0] != "work" {
		t.Errorf("extractTags = %v, want [work]", got)
	}

	if got := extractTags("from:alice subject:invoice"); len(got) != 0 {
		t.Errorf("extractTags with no tag: term = %v, want none", got)
	}
}

func TestScopeQuery(t *testing.T) {
	got, err := scopeQuery("from:alice", "work")
	if err != nil {
		t.Fatal(err)
	}
	// Both maildirs, and the alternation parenthesised so the "and" binds to
	// all of it rather than to the first branch only.
	if got != `(from:alice) and (path:work/** or path:work-recent/**)` {
		t.Errorf("scopeQuery = %q", got)
	}

	got, err = scopeQuery("*", "work")
	if err != nil {
		t.Fatal(err)
	}
	if got != `(path:work/** or path:work-recent/**)` {
		t.Errorf("scopeQuery(*) = %q", got)
	}

	got, err = scopeQuery("from:alice", "")
	if err != nil {
		t.Fatal(err)
	}
	if got != "from:alice" {
		t.Errorf("unscoped query changed: %q", got)
	}

	if _, err := scopeQuery("from:alice", "work dir"); err == nil {
		t.Error("account names with spaces must be rejected")
	}
}

func TestNotmuchCountsAndScopes(t *testing.T) {
	_, _, config := newFixture(t, map[string][]string{
		"work/INBOX": {
			message("alice@example.com", "me@work", "invoice 42", "a1@example.com", "the invoice is attached"),
		},
		// What the recent channel pulls before the full channel has caught
		// up: work's mail, in work's other maildir.
		"work-recent/INBOX": {
			message("carol@example.com", "me@work", "fresh today", "c1@example.com", "just arrived"),
		},
		"personal/INBOX": {
			message("bob@example.com", "me@home", "dinner", "b1@example.com", "are you free"),
		},
	})
	n := newNotmuch(config)
	ctx := context.Background()

	total, err := n.count(ctx, "*")
	if err != nil {
		t.Fatal(err)
	}
	if total != 3 {
		t.Fatalf("count(*) = %d, want 3", total)
	}

	q, err := scopeQuery("*", "work")
	if err != nil {
		t.Fatal(err)
	}
	scoped, err := n.count(ctx, q)
	if err != nil {
		t.Fatal(err)
	}
	if scoped != 2 {
		t.Fatalf("count scoped to work = %d, want both maildirs' messages", scoped)
	}
}
