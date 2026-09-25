//go:build parity

package runner

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
)

// realAuthSourcePath returns the auth.json the operator has explicitly opted
// into using for parity scenarios that need real provider credentials, or ""
// if no such opt-in is in effect.
//
// The parity runner never reads ~/.pig/agent/auth.json or ~/.pi/agent/auth.json
// implicitly: requires-auth scenarios would otherwise silently see the
// operator's real credentials on any machine where they happen to be logged
// in, and copy them into Pi's (or Pig's) parity agent directory. Set
// PIG_PARITY_REAL_AUTH to the exact auth.json path to use; an unset, missing,
// empty, or invalid file means no credentials are staged anywhere and
// requires-auth scenarios skip (see credentialsAvailable in runner_test.go).
func realAuthSourcePath() string {
	p := os.Getenv("PIG_PARITY_REAL_AUTH")
	if p == "" {
		return ""
	}
	data, err := os.ReadFile(p)
	if err != nil {
		return ""
	}
	var providers map[string]any
	if json.Unmarshal(data, &providers) != nil || len(providers) == 0 {
		return ""
	}
	return p
}

// EnsureTestdataAuth copies the operator-opted-in auth.json (PIG_PARITY_REAL_AUTH)
// into an ephemeral scenario snapshot, but only if auth.json does not already
// exist there. Fixture roots remain credential-free and immutable.
//
// label is "pig" or "pi" and is used only to identify the destination in error
// messages; both binaries stage the same opted-in file, since the credential
// wire is shared and one oracle running authenticated while the other reports
// an empty model catalog would be a false parity failure.
//
// Returns the path written (or existing), or an error if the operator has not
// opted in or the named file cannot be read.
func EnsureTestdataAuth(agentDir, label string) (string, error) {
	dst := filepath.Join(agentDir, "auth.json")

	// Only copy if the file does not exist at all.
	if _, err := os.Stat(dst); err == nil {
		return dst, nil // already present; never overwrite
	} else if !errors.Is(err, os.ErrNotExist) {
		return "", err
	}

	src := realAuthSourcePath()
	if src == "" {
		return "", fmt.Errorf("ensureTestdataAuth(%s): PIG_PARITY_REAL_AUTH not set (or names a missing/empty/invalid "+
			"auth.json); requires-auth scenarios need an explicit opt-in naming a real auth.json, the runner never reads "+
			"~/.pig or ~/.pi credentials implicitly", label)
	}
	data, err := os.ReadFile(src)
	if err != nil {
		return "", fmt.Errorf("ensureTestdataAuth(%s): read PIG_PARITY_REAL_AUTH=%s: %w", label, src, err)
	}

	if err := os.MkdirAll(agentDir, 0o700); err != nil {
		return "", err
	}
	if err := os.WriteFile(dst, data, 0o600); err != nil {
		return "", err
	}
	return dst, nil
}
