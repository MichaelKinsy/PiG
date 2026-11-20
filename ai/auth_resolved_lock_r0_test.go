//go:build unix

package ai

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/MichaelKinsy/PiG/internal/pilock"
)

// linkedStores makes ONE target file and two stores reached through two links in separate folders, simulating two processes sharing the same store. Fixture files only.
func linkedStores(t *testing.T, content string) (s1, s2 *AuthStorage, link1, link2, target string) {
	t.Helper()
	root := t.TempDir()
	target = filepath.Join(root, "owner", "auth.json")
	if err := os.MkdirAll(filepath.Dir(target), 0o700); err != nil {
		t.Fatal(err)
	}
	if content != "" {
		if err := os.WriteFile(target, []byte(content), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	for i, dst := range []*string{&link1, &link2} {
		dir := filepath.Join(root, "worker"+string(rune('A'+i)))
		if err := os.MkdirAll(dir, 0o700); err != nil {
			t.Fatal(err)
		}
		*dst = filepath.Join(dir, "auth.json")
		if err := os.Symlink(target, *dst); err != nil {
			t.Fatal(err)
		}
	}
	var err error
	if s1, err = NewAuthStorage(link1); err != nil {
		t.Fatal(err)
	}
	if s2, err = NewAuthStorage(link2); err != nil {
		t.Fatal(err)
	}
	return
}

// R0a: while store 1 holds its file lock, store 2 (another link, another folder, one target) cannot take the lock.
func TestR0aTwoLinksOneTargetExcludeEachOther(t *testing.T) {
	s1, s2, _, _, _ := linkedStores(t, `{}`)
	ran := false
	err := s1.withFileLock(t.Context(), false, func(*pilock.Lock) error {
		return s2.withFileLock(t.Context(), false, func(*pilock.Lock) error {
			ran = true
			return nil
		})
	})
	if ran || err == nil {
		t.Fatalf("store 2 took the lock while store 1 held it (ran=%v, err=%v); want ELOCKED", ran, err)
	}
	t.Logf("store 2 refused with: %v", err)
}

// R0b: store 1 modifies provider A, store 2 modifies provider B, started together; a barrier inside Modify makes them overlap whenever the locks do not exclude. Both records must be in the target. Repeated 20 times.
func TestR0bTwoLinksTwoProvidersBothSurvive(t *testing.T) {
	const runs = 20
	failed := 0
	for run := range runs {
		s1, s2, _, _, target := linkedStores(t, `{}`)
		arrived := make(chan struct{}, 2)
		release := make(chan struct{})
		barrier := func() {
			arrived <- struct{}{}
			select {
			case <-release:
			case <-time.After(300 * time.Millisecond):
			}
		}
		go func() { // opens the barrier once both are inside, or never (excluded stores)
			for range 2 {
				select {
				case <-arrived:
				case <-time.After(250 * time.Millisecond):
					return
				}
			}
			close(release)
		}()
		done := make(chan error, 2)
		for _, c := range []struct {
			s        *AuthStorage
			provider string
			key      string
		}{{s1, "provider-a", "fixture-refresh-A"}, {s2, "provider-b", "fixture-refresh-B"}} {
			go func() {
				_, err := c.s.Modify(context.Background(), c.provider, func(*Credential) (*Credential, error) {
					barrier()
					return &Credential{Type: CredentialAPIKey, Key: c.key}, nil
				})
				done <- err
			}()
		}
		for range 2 {
			if err := <-done; err != nil {
				t.Fatalf("run %d: Modify: %v", run, err)
			}
		}
		check, err := NewAuthStorage(target)
		if err != nil {
			t.Fatal(err)
		}
		a, _, _ := check.Get("provider-a")
		b, _, _ := check.Get("provider-b")
		if a.Key != "fixture-refresh-A" || b.Key != "fixture-refresh-B" {
			failed++
		}
	}
	t.Logf("R0b: %d of %d runs lost a record", failed, runs)
	if failed > 0 {
		t.Fatalf("R0b: %d of %d runs lost a record", failed, runs)
	}
}
