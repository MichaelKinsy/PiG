// SPDX-License-Identifier: MIT

//go:build unix

package upgrade

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// A listing cancelled after it printed part of its output is an error, not a
// short package list that would type check without its dependencies.
func TestListPackagesCancelledMidOutputIsAnError(t *testing.T) {
	dir := t.TempDir()
	ready := filepath.Join(dir, "ready")
	script := filepath.Join(dir, "go")
	body := "#!/bin/sh\nprintf '{\"ImportPath\":\"example.com/x\",\"Dir\":\"" + dir + "\",\"GoFiles\":[\"x.go\"]}\\n'\n: > '" + ready + "'\nexec sleep 30\n"
	if err := os.WriteFile(script, []byte(body), 0o700); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	go func() {
		for {
			if _, err := os.Stat(ready); err == nil {
				cancel()
				return
			}
			select {
			case <-ctx.Done():
				return
			case <-time.After(5 * time.Millisecond):
			}
		}
	}()
	listed, _, _, err := listPackages(ctx, LoadOptions{Dir: dir, Roots: []string{dir}, Command: script})
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("err = %v, listed = %+v", err, listed)
	}
}
