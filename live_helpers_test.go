//go:build live

package main

import (
	"encoding/json"
	"net/http"
	"testing"
)

func decodeJSONBody(t *testing.T, resp *http.Response, v any) {
	t.Helper()
	defer resp.Body.Close()
	if err := json.NewDecoder(resp.Body).Decode(v); err != nil {
		t.Fatalf("decoding response: %v", err)
	}
}
