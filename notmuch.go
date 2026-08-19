package main

import (
	"fmt"
	"strings"
)

// notmuchPrefixes are the query prefixes notmuch understands. Anything else is
// rejected before the index sees it: an unknown prefix matches nothing, and an
// empty result is indistinguishable from an empty mailbox to the caller.
var notmuchPrefixes = map[string]bool{
	"from": true, "to": true, "subject": true, "attachment": true,
	"mimetype": true, "tag": true, "is": true, "id": true, "mid": true,
	"thread": true, "path": true, "folder": true, "date": true,
	"lastmod": true, "query": true, "property": true, "body": true,
}

// validateQuery rejects unknown prefixes. Text inside double quotes is skipped,
// so subject:"re: lunch" does not read "re" as a prefix.
func validateQuery(q string) error {
	inQuotes := false
	word := strings.Builder{}
	flush := func() error {
		w := word.String()
		word.Reset()
		i := strings.Index(w, ":")
		if i <= 0 {
			return nil
		}
		p := strings.ToLower(w[:i])
		if strings.ContainsAny(p, `"'()`) {
			return nil
		}
		if !notmuchPrefixes[p] {
			return fmt.Errorf("unknown query prefix %q; valid prefixes are from, to, subject, tag, is, id, thread, path, folder, date, attachment, mimetype, body, property, lastmod", p)
		}
		return nil
	}
	for _, r := range q {
		switch {
		case r == '"':
			inQuotes = !inQuotes
			word.Reset()
		case inQuotes:
			// skip
		case r == ' ' || r == '(' || r == ')':
			if err := flush(); err != nil {
				return err
			}
		default:
			word.WriteRune(r)
		}
	}
	return flush()
}

// scopeQuery validates q and, when account is non-empty, restricts it to that
// account's directory under the maildir root.
func scopeQuery(q, account string) (string, error) {
	if err := validateQuery(q); err != nil {
		return "", err
	}
	if account == "" {
		return q, nil
	}
	if strings.ContainsAny(account, `/\ "'`) {
		return "", fmt.Errorf("account %q: names cannot contain spaces, quotes or slashes", account)
	}
	if q == "" || q == "*" {
		return fmt.Sprintf("path:%s/**", account), nil
	}
	return fmt.Sprintf("(%s) and path:%s/**", q, account), nil
}
