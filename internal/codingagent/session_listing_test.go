// SPDX-License-Identifier: MIT

package codingagent

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/ai"
)

func TestSessionListingCancellationAndProgress(t *testing.T) {
	// .upstream/v0.99.1/packages/coding-agent/test/session-manager/file-operations.test.ts:341
	t.Run("rejects a cancelled session listing", func(t *testing.T) {
		dir := t.TempDir()
		a, b := t.TempDir(), t.TempDir()
		listingPersistedSession(t, dir, a, "from A")
		listingPersistedSession(t, dir, b, "from B")
		manager := NewSessionManagerWithDir(a, dir)
		ctx, cancel := context.WithCancel(t.Context())
		defer cancel()
		callbacks := 0
		_, err := manager.ListAllSessions(SessionListOptions{Context: ctx, OnProgress: func(_, _ int, partial []SessionInfo) {
			callbacks++
			if partial != nil {
				cancel()
			}
		}})
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("listing error=%v", err)
		}
		if callbacks != 1 {
			t.Fatalf("callbacks=%d after cancellation", callbacks)
		}
		fmt.Printf("SESSION_LIST cancelled=%t callbacks=%d\n", errors.Is(err, context.Canceled), callbacks)
		for _, list := range []func(SessionListOptions) ([]SessionInfo, error){func(o SessionListOptions) ([]SessionInfo, error) { return manager.ListSessions(o) }, func(o SessionListOptions) ([]SessionInfo, error) { return manager.ListCurrentSessions(o) }, func(o SessionListOptions) ([]SessionInfo, error) { return manager.ListAllSessions(o) }} {
			if _, err := list(SessionListOptions{Context: ctx}); !errors.Is(err, context.Canceled) {
				t.Fatalf("pre-cancelled listing=%v", err)
			}
		}
	})
	t.Run("publishes bounded ordered immutable snapshots", func(t *testing.T) {
		dir := t.TempDir()
		cwd := t.TempDir()
		const files = 21
		for i := range files {
			writeSessionInfoFixture(t, dir, fmt.Sprintf("%02d", i), cwd)
		}
		manager := NewSessionManagerWithDir(cwd, dir)
		calls := 0
		var first []SessionInfo
		var firstCopy []SessionInfo
		var published []int
		infos, err := manager.ListSessions(SessionListOptions{Context: t.Context(), OnProgress: func(loaded, total int, partial []SessionInfo) {
			calls++
			if loaded != calls || total != files {
				t.Errorf("progress=%d/%d call=%d", loaded, total, calls)
			}
			if partial != nil {
				published = append(published, loaded)
				if !slices.IsSortedFunc(partial, compareSessionModifiedDesc) {
					t.Error("unsorted snapshot")
				}
				if first == nil {
					first = partial
					firstCopy = slices.Clone(partial)
				}
			}
		}})
		if err != nil {
			t.Fatal(err)
		}
		if calls != files || len(infos) != files || !slices.Equal(published, []int{1, 10, 20, files}) {
			t.Fatalf("calls=%d files=%d publications=%v", calls, len(infos), published)
		}
		if !slices.Equal(first, firstCopy) {
			t.Fatal("published snapshot mutated")
		}
	})
	t.Run("distinguishes absent progress from a published empty snapshot", func(t *testing.T) {
		dir := t.TempDir()
		for i := range 2 {
			if err := os.WriteFile(filepath.Join(dir, fmt.Sprintf("%d.jsonl", i)), []byte("invalid\n"), 0o600); err != nil {
				t.Fatal(err)
			}
		}
		calls := 0
		manager := NewSessionManagerWithDir(t.TempDir(), dir)
		_, err := manager.ListSessions(SessionListOptions{OnProgress: func(_, _ int, partial []SessionInfo) {
			calls++
			if partial == nil || len(partial) != 0 {
				t.Errorf("snapshot=%v", partial)
			}
		}})
		if err != nil || calls != 2 {
			t.Fatalf("calls=%d err=%v", calls, err)
		}
	})
	t.Run("filters current-directory progress as well as final results", func(t *testing.T) {
		dir := t.TempDir()
		a, b := t.TempDir(), t.TempDir()
		writeSessionInfoFixture(t, dir, "a", a)
		writeSessionInfoFixture(t, dir, "b", b)
		manager := NewSessionManagerWithDir(a, dir)
		infos, err := manager.ListCurrentSessions(SessionListOptions{Context: t.Context(), OnProgress: func(_, _ int, partial []SessionInfo) {
			for _, info := range partial {
				if info.CWD != a {
					t.Errorf("foreign cwd in progress: %s", info.CWD)
				}
			}
		}})
		if err != nil || len(infos) != 1 || infos[0].ID != "a" {
			t.Fatalf("infos=%v err=%v", infos, err)
		}
	})
}

func listingPersistedSession(t *testing.T, dir, cwd, label string) string {
	t.Helper()
	id, err := GenerateSessionID()
	if err != nil {
		t.Fatal(err)
	}
	s, err := NewSessionManagerWithDir(cwd, dir).Create(id, "")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.AppendMessage(mkUserMsg(label)); err != nil {
		t.Fatal(err)
	}
	message := mkAssistantMsg("reply to " + label)
	message.Assistant.API, message.Assistant.Provider, message.Assistant.ModelID = "anthropic-messages", "anthropic", "test"
	message.Assistant.Usage = &ai.Usage{Input: 1, Output: 1, TotalTokens: 2}
	message.Assistant.StopReason = ai.StopReasonStop
	if _, err := s.AppendMessage(message); err != nil {
		t.Fatal(err)
	}
	return s.Path()
}

type cancelSessionReader struct {
	reader io.Reader
	cancel context.CancelFunc
	reads  int
}

func (r *cancelSessionReader) Read(buffer []byte) (int, error) {
	r.reads++
	n, err := r.reader.Read(buffer)
	if r.reads == 2 {
		r.cancel()
	}
	return n, err
}

func TestSessionLineReaderCancelsWithinAnOversizedRecord(t *testing.T) {
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	reader := &cancelSessionReader{reader: strings.NewReader(strings.Repeat("x", 1024*1024)), cancel: cancel}
	called := false
	err := forEachJSONLLineContext(ctx, reader, func([]byte) error { called = true; return nil })
	if !errors.Is(err, context.Canceled) || reader.reads != 2 || called {
		t.Fatalf("err=%v reads=%d callback=%v", err, reader.reads, called)
	}
}
