package session_test

import (
	"encoding/json"
	"maps"
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/agent/harness/session"
)

// The numeric versions are the upstream-owned persisted schema, not Pi's release
// version. Keep the expected bytes independent of the Go constants being tested.
func TestHarnessJSONLHeaderRepositoryRoundTrip(t *testing.T) {
	t.Parallel()
	ctx := t.Context()
	fs := jsonlOptions(t).FileSystem
	options := session.JsonlSessionRepoOptions{FileSystem: fs, SessionsRoot: "sessions", Now: func() int64 { return 1234 }}
	repo := session.NewJsonlSessionRepo(options)
	t.Cleanup(func() { mustNoErr(t, repo.Close(ctx)) })
	opened, err := repo.Create(ctx, session.SessionCreateOptions{ID: "schema", Cwd: "/workspace", ParentSessionID: "parent"})
	mustNoErr(t, err)
	t.Cleanup(func() { mustNoErr(t, opened.Close(ctx)) })
	metadata := opened.Metadata()
	mustNoErr(t, opened.Close(ctx))
	mustNoErr(t, repo.Close(ctx))

	// The repository stores the cwd resolved as an absolute path, as upstream
	// repo.ts does with fileSystem.absolutePath: "/workspace" on POSIX,
	// "C:\workspace" on Windows.
	cwd, err := fs.AbsolutePath(ctx, "/workspace")
	mustNoErr(t, err)
	persisted := readFile(t, fs, metadata.Path)
	if !strings.HasSuffix(persisted, "\n") || strings.Count(persisted, "\n") != 1 {
		t.Fatalf("expected one terminated header, got %q", persisted)
	}
	var header map[string]any
	mustNoErr(t, json.Unmarshal([]byte(persisted), &header))
	assertJSON(t, header, map[string]any{"v": 4, "kind": "header", "id": "schema", "storageVersion": 1, "createdAt": 1234, "cwd": cwd, "parentSessionId": "parent"})

	fresh := session.NewJsonlSessionRepo(options)
	t.Cleanup(func() { mustNoErr(t, fresh.Close(ctx)) })
	listed, err := fresh.List(ctx, nil)
	mustNoErr(t, err)
	if len(listed) != 1 {
		t.Fatalf("discovered metadata = %+v", listed)
	}
	if listed[0].StorageVersion != 1 || listed[0].CreatedAt != 1234 || listed[0].ID != "schema" || listed[0].ParentSessionID != "parent" || listed[0].Cwd != cwd {
		t.Fatalf("reopened header metadata = %+v", listed[0])
	}
	reopened, err := fresh.Open(ctx, listed[0])
	mustNoErr(t, err)
	mustNoErr(t, reopened.Close(ctx))
	if got := readFile(t, fs, metadata.Path); got != persisted {
		t.Fatalf("read-only reopen rewrote header: %q -> %q", persisted, got)
	}
}

func TestHarnessJSONLInvalidHeaderRepositoryOpen(t *testing.T) {
	t.Parallel()
	valid := map[string]any{"v": 4, "kind": "header", "id": "schema", "storageVersion": 1, "createdAt": 1234, "cwd": "/workspace"}
	for _, tc := range []struct {
		name, key string
		value     any
	}{
		{"wrong-format", "v", 5},
		{"wrong-kind", "kind", "entry"},
		{"null-id", "id", nil},
		{"nonstring-cwd", "cwd", 7},
		{"future-storage", "storageVersion", 2},
		{"zero-storage", "storageVersion", 0},
		{"fractional-storage", "storageVersion", 1.5},
		{"negative-created", "createdAt", -1},
		{"zero-next-seq", "nextSeq", 0},
		{"null-parent", "parentSessionId", nil},
		{"nonstring-legacy-parent", "legacyParentSessionPath", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ctx := t.Context()
			options := jsonlOptions(t)
			fields := maps.Clone(valid)
			fields[tc.key] = tc.value
			// A torn transaction must not be repaired before validating its header.
			content := string(mustJSON(t, fields)) + "\n{torn"
			mustNoErr(t, options.FileSystem.WriteFile(ctx, options.Path, []byte(content)))
			repo := session.NewJsonlSessionRepo(session.JsonlSessionRepoOptions{FileSystem: options.FileSystem, SessionsRoot: "sessions"})
			t.Cleanup(func() { mustNoErr(t, repo.Close(ctx)) })
			opened, err := repo.Open(ctx, session.SessionMetadata{ID: "schema", Cwd: "/workspace", Path: options.Path})
			if err == nil {
				mustNoErr(t, opened.Close(ctx))
				t.Fatalf("repository accepted invalid header %s", content)
			}
			if got := readFile(t, options.FileSystem, options.Path); got != content {
				t.Fatalf("invalid header was rewritten: %q", got)
			}
		})
	}
}
