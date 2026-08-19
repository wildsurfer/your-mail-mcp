package main

import (
	"context"
	"strings"
	"testing"
)

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
		"fom:alice",         // typo, would silently match nothing
		"sender:alice",      // not a notmuch prefix
		"folder:INBOX and x:1",
		"subjet:\"x\"",      // typo immediately followed by quote
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
	if got != `(from:alice) and path:work/**` {
		t.Errorf("scopeQuery = %q", got)
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
	if total != 2 {
		t.Fatalf("count(*) = %d, want 2", total)
	}

	q, err := scopeQuery("*", "work")
	if err != nil {
		t.Fatal(err)
	}
	scoped, err := n.count(ctx, q)
	if err != nil {
		t.Fatal(err)
	}
	if scoped != 1 {
		t.Fatalf("count scoped to work = %d, want 1", scoped)
	}
}
