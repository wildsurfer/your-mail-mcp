package main

import (
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"unicode"
)

// Account is one IMAP account to mirror. Everything except name, host, user
// and password has a default; see the design spec for why the mbsync knobs
// that used to live here (pipeline depth, SubFolders, AuthMechs) are pinned.
type Account struct {
	Name           string   `json:"name"`
	Host           string   `json:"host"`
	Port           int      `json:"port"`
	User           string   `json:"user"`
	Password       string   `json:"password"`
	TLS            string   `json:"tls"`
	Patterns       []string `json:"patterns"`
	ExcludeFolders []string `json:"exclude_folders"`
}

type Config struct {
	Accounts []Account `json:"accounts"`
}

// hasWhitespaceOrControl returns true if s contains any whitespace or control character.
func hasWhitespaceOrControl(s string) bool {
	for _, r := range s {
		if unicode.IsSpace(r) || unicode.IsControl(r) {
			return true
		}
	}
	return false
}

// loadConfig reads the accounts file, expands ${VAR} references against the
// environment so secrets never sit in the file, then validates and defaults.
func loadConfig(path string) (*Config, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("accounts file: %w", err)
	}
	var cfg Config
	if err := json.Unmarshal([]byte(os.ExpandEnv(string(raw))), &cfg); err != nil {
		return nil, fmt.Errorf("accounts file: %w", err)
	}
	if len(cfg.Accounts) == 0 {
		return nil, fmt.Errorf("accounts file: no accounts defined")
	}
	seen := map[string]bool{}
	for i := range cfg.Accounts {
		a := &cfg.Accounts[i]
		if a.Name == "" || strings.ContainsAny(a.Name, `/\ "'`) {
			return nil, fmt.Errorf("account %d: name must be non-empty and free of spaces, quotes and slashes", i)
		}
		if seen[a.Name] {
			return nil, fmt.Errorf("account %q: duplicate name", a.Name)
		}
		seen[a.Name] = true
		if a.Host == "" {
			return nil, fmt.Errorf("account %q: host is required", a.Name)
		}
		if hasWhitespaceOrControl(a.Host) {
			return nil, fmt.Errorf("account %q: host must not contain whitespace or control characters", a.Name)
		}
		if a.User == "" {
			return nil, fmt.Errorf("account %q: user is required", a.Name)
		}
		if hasWhitespaceOrControl(a.User) {
			return nil, fmt.Errorf("account %q: user must not contain whitespace or control characters", a.Name)
		}
		if a.Password == "" {
			return nil, fmt.Errorf("account %q: password is empty; is the referenced environment variable set?", a.Name)
		}
		switch a.TLS {
		case "":
			a.TLS = "imaps"
		case "imaps", "starttls", "none":
		default:
			return nil, fmt.Errorf("account %q: tls must be imaps, starttls or none", a.Name)
		}
		if a.Port == 0 {
			if a.TLS == "imaps" {
				a.Port = 993
			} else {
				a.Port = 143
			}
		}
		if len(a.Patterns) == 0 {
			a.Patterns = []string{"*"}
		}
	}
	return &cfg, nil
}

func main() {
	// Wired in Task 8; kept minimal so the package builds from Task 1 onward.
	fmt.Fprintln(os.Stderr, "your-mail-mcp: not yet wired")
	os.Exit(1)
}
