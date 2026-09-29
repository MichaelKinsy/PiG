package experimental

import (
	"context"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"strings"
	"sync"
	"testing"
	"testing/synctest"
)

func TestPortWave08ServerProfile(t *testing.T) {
	const firstID = "00000000-0000-4000-8000-000000000001"
	const secondID = "00000000-0000-4000-8000-000000000002"
	type result struct {
		profile     *ServerProfile
		err         error
		requestedID string
	}
	// Every profile is released exactly once, as in Pi: a test releases explicitly where Pi does, and cleanup releases only the profiles a failed test left held.
	var mu sync.Mutex
	released := make(map[*ServerProfile]bool)
	release := func(t *testing.T, p *ServerProfile) {
		t.Helper()
		mu.Lock()
		done := released[p]
		released[p] = true
		mu.Unlock()
		if done {
			t.Fatalf("profile %s released twice", p.ServerID)
		}
		if err := p.Release(); err != nil {
			t.Fatal(err)
		}
	}
	hold := func(t *testing.T, p *ServerProfile) {
		t.Helper()
		t.Cleanup(func() {
			mu.Lock()
			done := released[p]
			mu.Unlock()
			if !done {
				release(t, p)
			}
		})
	}
	acquire := func(t *testing.T, dir string, id *string) *ServerProfile {
		t.Helper()
		p, err := AcquireServerProfile(context.Background(), dir, id)
		if err != nil {
			t.Fatal(err)
		}
		hold(t, p)
		return p
	}
	// upstream: packages/coding-agent/test/experimental-server-profile.test.ts:23
	t.Run("serializes launchers and preserves the server identity", func(t *testing.T) {
		synctest.Test(t, func(t *testing.T) {
			dir := t.TempDir()
			first := acquire(t, dir, nil)
			secondCh := make(chan result, 1)
			go func() {
				p, err := AcquireServerProfile(context.Background(), dir, nil)
				secondCh <- result{profile: p, err: err}
			}()
			synctest.Wait()
			select {
			case r := <-secondCh:
				if r.profile != nil {
					release(t, r.profile)
				}
				t.Fatalf("second launcher acquired before release: %+v", r)
			default:
			}
			release(t, first)
			second := <-secondCh
			if second.err != nil {
				t.Fatal(second.err)
			}
			hold(t, second.profile)
			if second.profile.ServerID != first.ServerID {
				t.Fatalf("server ID = %q, want %q", second.profile.ServerID, first.ServerID)
			}
			data, err := os.ReadFile(filepath.Join(dir, "default-server-id"))
			if err != nil {
				t.Fatal(err)
			}
			if got := strings.TrimSpace(string(data)); got != first.ServerID {
				t.Fatalf("persisted ID = %q, want %q", got, first.ServerID)
			}
			release(t, second.profile)
		})
	})
	// upstream: packages/coding-agent/test/experimental-server-profile.test.ts:43
	t.Run("does not serialize different server IDs in one directory", func(t *testing.T) {
		dir := t.TempDir()
		results := make(chan result, 2)
		for _, id := range []string{firstID, secondID} {
			go func() {
				p, err := AcquireServerProfile(context.Background(), dir, &id)
				results <- result{profile: p, err: err, requestedID: id}
			}()
		}
		profiles := make(map[string]*ServerProfile)
		for range 2 {
			r := <-results
			if r.err != nil {
				t.Fatal(r.err)
			}
			hold(t, r.profile)
			if r.profile.ServerID != r.requestedID {
				t.Errorf("server ID = %q, want request ID %q", r.profile.ServerID, r.requestedID)
			}
			profiles[r.profile.ServerID] = r.profile
		}
		if profiles[firstID] == nil || profiles[secondID] == nil {
			t.Fatalf("acquired identities = %v, want %q and %q", profiles, firstID, secondID)
		}
		entries, err := os.ReadDir(dir)
		if err != nil {
			t.Fatal(err)
		}
		names := make([]string, 0, len(entries))
		for _, entry := range entries {
			names = append(names, entry.Name())
		}
		sort.Strings(names)
		want := []string{"launcher-" + firstID + ".lock", "launcher-" + secondID + ".lock"}
		if !reflect.DeepEqual(names, want) {
			t.Fatalf("directory = %v, want %v", names, want)
		}
		for _, p := range profiles {
			release(t, p)
		}
	})
	// upstream: packages/coding-agent/test/experimental-server-profile.test.ts:58
	t.Run("does not share the default identity across server directories", func(t *testing.T) {
		dirs := []string{t.TempDir(), t.TempDir()}
		results := make(chan result, 2)
		for _, dir := range dirs {
			go func() {
				p, err := AcquireServerProfile(context.Background(), dir, nil)
				results <- result{profile: p, err: err}
			}()
		}
		var profiles []*ServerProfile
		for range dirs {
			r := <-results
			if r.err != nil {
				t.Fatal(r.err)
			}
			hold(t, r.profile)
			profiles = append(profiles, r.profile)
		}
		if profiles[0].ServerID == profiles[1].ServerID {
			t.Fatalf("different directories share ID %q", profiles[0].ServerID)
		}
		for _, p := range profiles {
			release(t, p)
		}
	})
	// upstream: packages/coding-agent/test/experimental-server-profile.test.ts:70
	t.Run("rejects a corrupt default identity", func(t *testing.T) {
		dir := t.TempDir()
		if err := os.WriteFile(filepath.Join(dir, "default-server-id"), []byte("invalid\n"), 0o600); err != nil {
			t.Fatal(err)
		}
		_, err := AcquireServerProfile(context.Background(), dir, nil)
		if err == nil || !strings.Contains(err.Error(), "Invalid default experimental server identity") {
			t.Fatalf("error = %v, want invalid default identity", err)
		}
	})
	// upstream: packages/coding-agent/test/experimental-server-profile.test.ts:77
	t.Run("rejects an invalid explicit server ID", func(t *testing.T) {
		_, err := AcquireServerProfile(context.Background(), t.TempDir(), new("invalid"))
		if err == nil || !strings.Contains(err.Error(), "Invalid experimental server ID") {
			t.Fatalf("error = %v, want invalid explicit ID", err)
		}
	})
}
