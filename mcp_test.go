package main

import (
	"strings"
	"testing"
)

func TestRenderWrapsContent(t *testing.T) {
	text, truncated, next := render("hello", 0, 100)
	if !strings.HasPrefix(text, untrustedOpen) || !strings.HasSuffix(text, untrustedClose) {
		t.Fatalf("render did not wrap its content: %q", text)
	}
	if !strings.Contains(text, "hello") {
		t.Error("render dropped the content")
	}
	if truncated || next != 0 {
		t.Errorf("short content should not be truncated, got truncated=%v next=%d", truncated, next)
	}
}

func TestRenderPaginates(t *testing.T) {
	body := strings.Repeat("x", 250)

	text, truncated, next := render(body, 0, 100)
	if !truncated {
		t.Fatal("want truncated=true")
	}
	if next != 100 {
		t.Fatalf("next = %d, want 100", next)
	}
	if strings.Count(text, "x") != 100 {
		t.Fatalf("got %d bytes of content, want 100", strings.Count(text, "x"))
	}

	text, truncated, next = render(body, 200, 100)
	if truncated {
		t.Error("the final page should not be marked truncated")
	}
	if next != 0 {
		t.Errorf("next = %d on the final page, want 0", next)
	}
	if strings.Count(text, "x") != 50 {
		t.Errorf("final page has %d bytes, want 50", strings.Count(text, "x"))
	}

	_, _, _ = render(body, 9999, 100)
}

func TestRenderHandlesOffsetPastEnd(t *testing.T) {
	text, truncated, next := render("short", 500, 100)
	if truncated || next != 0 {
		t.Errorf("offset past the end: truncated=%v next=%d", truncated, next)
	}
	if strings.Contains(text, "short") {
		t.Error("offset past the end should yield no content")
	}
}
