package main

import (
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
