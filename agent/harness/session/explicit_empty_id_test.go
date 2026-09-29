package session_test

import (
	"context"
	"testing"

	"github.com/MichaelKinsy/PiG/agent/harness/session"
)

// upstream: packages/agent/src/harness/session/jsonl/repo.ts:154,212 and memory.ts:348,405
// `options.id ?? uuidv7(createdAt)` keeps an explicitly empty ID and generates one only when the ID is omitted.
func TestRepositoriesKeepAnExplicitlyEmptyCreateAndForkID(t *testing.T) {
	type repository interface {
		Create(context.Context, session.SessionCreateOptions) (session.Session, error)
		Fork(context.Context, session.SessionMetadata, session.ForkOptions) (session.Session, error)
	}
	for _, kind := range []struct {
		name string
		open func(t *testing.T) repository
	}{
		{"memory", func(t *testing.T) repository {
			repo := session.NewMemorySessionRepo(&session.MemorySessionRepoOptions{Now: fixedNow})
			t.Cleanup(func() { _ = repo.Close(background) })
			return repo
		}},
		{"jsonl", func(t *testing.T) repository {
			repo := session.NewJsonlSessionRepo(session.JsonlSessionRepoOptions{FileSystem: jsonlOptions(t).FileSystem, SessionsRoot: "sessions", Now: fixedNow})
			t.Cleanup(func() { _ = repo.Close(background) })
			return repo
		}},
	} {
		t.Run(kind.name, func(t *testing.T) {
			repo := kind.open(t)
			omitted, err := repo.Create(background, session.SessionCreateOptions{Cwd: "/workspace"})
			mustNoErr(t, err)
			if omitted.Metadata().ID == "" {
				t.Fatal("omitted ID must be generated")
			}
			empty, err := repo.Create(background, session.SessionCreateOptions{Cwd: "/workspace", HasID: true})
			mustNoErr(t, err)
			if got := empty.Metadata().ID; got != "" {
				t.Fatalf("explicit empty create ID became %q", got)
			}
			if _, err := repo.Create(background, session.SessionCreateOptions{Cwd: "/workspace", HasID: true}); err == nil {
				t.Fatal("a second Session with the explicit empty ID must collide")
			}
			generatedFork, err := repo.Fork(background, omitted.Metadata(), session.ForkOptions{Scope: session.ForkScopeTree})
			mustNoErr(t, err)
			if generatedFork.Metadata().ID == "" {
				t.Fatal("omitted fork ID must be generated")
			}
			// A separate repository keeps the empty fork ID from colliding with the empty created ID.
			forkRepo := kind.open(t)
			source, err := forkRepo.Create(background, session.SessionCreateOptions{Cwd: "/workspace"})
			mustNoErr(t, err)
			emptyFork, err := forkRepo.Fork(background, source.Metadata(), session.ForkOptions{Scope: session.ForkScopeTree, HasID: true})
			mustNoErr(t, err)
			if got := emptyFork.Metadata().ID; got != "" {
				t.Fatalf("explicit empty fork ID became %q", got)
			}
		})
	}
}
