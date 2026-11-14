//go:build !windows

package ai

import (
	"bufio"
	"context"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"
)

// A refresh that outlasts the sync lock's staleness window (10 s) has its lock taken by any process that opens the store: its holder must still persist the credential it obtained, which may have spent a single-use refresh token. Pi persists it (its compromise flag is raised only by a 15 s heartbeat); PiG discarded it with 'Credential store modify failed ... auth.json.lock'.
func TestRefreshedCredentialSurvivesALockTakenMeanwhile(t *testing.T) {
	if role := os.Getenv("AUTH_TEST_ROLE"); role == "holder" {
		store, err := NewAuthStorage(os.Getenv("AUTH_TEST_PATH"))
		if err != nil {
			t.Fatal(err)
		}
		input := bufio.NewReader(os.Stdin)
		_, err = store.Modify(context.Background(), "openai-codex", func(current *Credential) (*Credential, error) {
			_, _ = os.Stdout.WriteString("READY\n") // the network call of the refresh is in flight
			if _, err := input.ReadString('\n'); err != nil {
				return nil, err
			}
			next := *current
			next.Access, next.Refresh = "access-rotated", "refresh-rotated"
			return &next, nil
		})
		if err != nil {
			_, _ = os.Stderr.WriteString("holder: the refreshed credential was not persisted: " + err.Error() + "\n")
			os.Exit(1)
		}
		return
	}
	scratch := t.TempDir()
	path := filepath.Join(scratch, "agent", "auth.json")
	seedCredentialFixture(t, path)

	holder := credentialChild(t, scratch, path, "holder")
	stdin, err := holder.StdinPipe()
	if err != nil {
		t.Fatal(err)
	}
	stdout, err := holder.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	var output strings.Builder
	holder.Stderr = &output
	if err := holder.Start(); err != nil {
		t.Fatal(err)
	}
	if line, err := bufio.NewReader(stdout).ReadString('\n'); err != nil || line != "READY\n" {
		t.Fatalf("holder did not reach its refresh: %q, %v\n%s", line, err, output.String())
	}
	// The refresh has run for 11 s without the holder's 15 s heartbeat having touched the lock: another process then opens a store, whose sync acquire (stale after 10 s) takes the lock.
	lockDir := path + ".lock"
	if resolved, err := filepath.EvalSymlinks(path); err == nil {
		lockDir = resolved + ".lock"
	}
	old := time.Now().Add(-11 * time.Second)
	if err := os.Chtimes(lockDir, old, old); err != nil {
		t.Fatal(err)
	}
	if got := credentialFixtureEntries(t, path); !slices.Equal(got, credentialFixtureProviders) {
		t.Fatalf("second process lists %v, want %v", got, credentialFixtureProviders)
	}
	if _, err := stdin.Write([]byte("go\n")); err != nil {
		t.Fatal(err)
	}
	if err := holder.Wait(); err != nil {
		t.Fatalf("holder process: %v\n%s", err, output.String())
	}
	store, err := NewAuthStorage(path)
	if err != nil {
		t.Fatal(err)
	}
	stored, ok, err := store.GetRaw("openai-codex")
	if err != nil || !ok || stored.Access != "access-rotated" || stored.Refresh != "refresh-rotated" {
		t.Fatalf("stored openai-codex = %+v (%v, %v), want the rotated credential", stored, ok, err)
	}
	if got := credentialFixtureEntries(t, path); !slices.Equal(got, credentialFixtureProviders) {
		t.Fatalf("providers = %v, want %v", got, credentialFixtureProviders)
	}
}
