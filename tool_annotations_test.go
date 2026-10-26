package main

import (
	"context"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

func TestToolAnnotationsDescribeSideEffects(t *testing.T) {
	for _, publicURL := range []string{"", "https://mail.example.com"} {
		name := "stdio"
		if publicURL != "" {
			name = "http"
		}
		t.Run(name, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			srv := newServer(&Config{}, nil, t.TempDir())
			srv.publicURL = publicURL
			m := mcp.NewServer(&mcp.Implementation{Name: "test", Version: "1"}, nil)
			srv.registerTools(m)
			ct, st := mcp.NewInMemoryTransports()
			serverSession, err := m.Connect(ctx, st, nil)
			if err != nil {
				t.Fatal(err)
			}
			defer serverSession.Close()
			c := mcp.NewClient(&mcp.Implementation{Name: "test", Version: "1"}, nil)
			session, err := c.Connect(ctx, ct, nil)
			if err != nil {
				t.Fatal(err)
			}
			defer session.Close()
			tools, err := session.ListTools(ctx, nil)
			if err != nil {
				t.Fatal(err)
			}
			if len(tools.Tools) != 11 {
				t.Fatalf("got %d tools, want 11", len(tools.Tools))
			}
			for _, tool := range tools.Tools {
				a := tool.Annotations
				if a == nil {
					t.Errorf("tool %s has no side-effect annotations", tool.Name)
					continue
				}
				writes := tool.Name == "refresh" || (tool.Name == "attachment" && publicURL == "")
				if a.ReadOnlyHint == writes {
					t.Errorf("tool %s readOnlyHint = %v, want %v", tool.Name, a.ReadOnlyHint, !writes)
				}
				// Nothing here destroys mail: refresh only pulls, and local
				// attachment export evicts only its own earlier exports.
				if a.DestructiveHint == nil || *a.DestructiveHint {
					t.Errorf("tool %s must explicitly advertise destructiveHint = false", tool.Name)
				}
				openWorld := tool.Name == "refresh" // Sync contacts the IMAP provider.
				if a.OpenWorldHint == nil || *a.OpenWorldHint != openWorld {
					t.Errorf("tool %s must explicitly advertise openWorldHint = %v", tool.Name, openWorld)
				}
			}
		})
	}
}
