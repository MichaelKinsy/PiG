package codingagent

import (
	"fmt"
	"math/rand/v2"
	"path/filepath"
	"reflect"
	"sync"
	"testing"

	"github.com/MichaelKinsy/PiG/agent"
	"github.com/MichaelKinsy/PiG/ai"
)

// freshProjectionView is the view projection of the current leaf built from the branch, without the Session's kept projection.
func freshProjectionView(s *Session) SessionProjection {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if s.leafID == nil {
		return SessionProjection{ThinkingLevel: "off"}
	}
	return buildSessionProjection(s.contextPathToLocked(*s.leafID), s.messageFor, false)
}

func sameProjection(t *testing.T, what string, got, want SessionProjection) {
	t.Helper()
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("%s: kept projection differs from a new one:\n got %d entries %d messages thinking %q model %+v\nwant %d entries %d messages thinking %q model %+v",
			what, len(got.Entries), len(got.Messages), got.ThinkingLevel, got.Model, len(want.Entries), len(want.Messages), want.ThinkingLevel, want.Model)
	}
}

// The projection a Session keeps for its leaf and extends with each append equals a new projection of the branch after every step of a
// random history: messages, tool results, model and thinking changes, custom entries and messages, labels, context edits, compactions
// (with and without a kept entry), branch moves and branch summaries. Reads are skipped at random, so one read extends by several entries.
func TestProjectionViewEqualsANewProjection(t *testing.T) {
	for seed := range uint64(12) {
		extended, rebuilt := 0, 0
		randomStoreSessionWith(t, seed, 300, func(s *Session, rng *rand.Rand) {
			if rng.IntN(3) == 0 {
				return
			}
			before := s.projView
			sameProjection(t, "view", s.BuildSessionProjectionView(), freshProjectionView(s))
			if before != nil && s.projView == before {
				extended++
			} else {
				rebuilt++
			}
			sameProjection(t, "copy", s.BuildSessionProjection(), freshProjectionView(s))
		})
		if extended < rebuilt {
			t.Fatalf("seed %d: the kept projection was extended %d times and built %d times; the comparison would not exercise extension", seed, extended, rebuilt)
		}
	}
}

// projectionSession is a file-backed session with a short conversation whose projection the Session keeps.
func projectionSession(t *testing.T) (*Session, []string) {
	t.Helper()
	dir := t.TempDir()
	session, err := NewSessionManagerWithDir(dir, filepath.Join(dir, "sessions")).Create("", "")
	if err != nil {
		t.Fatal(err)
	}
	var ids []string
	for i := range 4 {
		user, err := session.AppendMessage(mkUserMsg("question " + string(rune('a'+i))))
		if err != nil {
			t.Fatal(err)
		}
		answer, err := session.AppendMessage(mkAssistantMsg("answer " + string(rune('a'+i))))
		if err != nil {
			t.Fatal(err)
		}
		ids = append(ids, user, answer)
	}
	_ = session.BuildSessionProjectionView()
	if session.projView == nil {
		t.Fatal("the session keeps no projection")
	}
	return session, ids
}

// appendTurns appends n user and assistant messages.
func appendTurns(t *testing.T, s *Session, n int) {
	t.Helper()
	for i := range n {
		if _, err := s.AppendMessage(mkUserMsg(fmt.Sprintf("later question %d", i))); err != nil {
			t.Fatal(err)
		}
		if _, err := s.AppendMessage(mkAssistantMsg(fmt.Sprintf("later answer %d", i))); err != nil {
			t.Fatal(err)
		}
	}
}

// An appended turn extends the kept projection instead of projecting the branch again.
func TestProjectionViewExtendsWithAnAppendedTurn(t *testing.T) {
	session, _ := projectionSession(t)
	kept := session.projView
	if _, err := session.AppendMessage(mkUserMsg("next")); err != nil {
		t.Fatal(err)
	}
	if _, err := session.AppendMessage(mkAssistantMsg("next answer")); err != nil {
		t.Fatal(err)
	}
	view := session.BuildSessionProjectionView()
	sameProjection(t, "turn", view, freshProjectionView(session))
	if session.projView != kept {
		t.Fatal("an appended turn projected the branch again")
	}
	// A caller's append to a view it holds cannot write into the entries the kept projection added after it.
	if _, err := session.AppendMessage(mkUserMsg("after")); err != nil {
		t.Fatal(err)
	}
	_ = session.BuildSessionProjectionView()
	_ = append(view.Messages, agent.AgentMessage{User: &agent.UserMessage{Role: "user", Content: ai.UserText("written by a caller")}})
	_ = append(view.Entries, ProjectedSessionEntry{})
	sameProjection(t, "after a caller's append", session.BuildSessionProjectionView(), freshProjectionView(session))
}

// Each change that replaces what earlier entries contribute, or moves the leaf, projects the branch again: compaction, a move of the leaf
// (/tree), a fork into a new session file (/fork), a context edit, and model and thinking-level changes, which set the projection's settings.
// Turns appended after the change make the new branch longer than the kept projection, so a kept projection that missed the change would
// be extended instead of built again.
func TestProjectionViewFollowsBranchChanges(t *testing.T) {
	for _, tc := range []struct {
		name   string
		change func(t *testing.T, s *Session, ids []string)
		check  func(t *testing.T, view SessionProjection, ids []string)
	}{
		{"compaction", func(t *testing.T, s *Session, ids []string) {
			if _, err := s.AppendCompaction("summary", ids[4], 1000, nil, false, nil); err != nil {
				t.Fatal(err)
			}
			appendTurns(t, s, 3)
		}, func(t *testing.T, view SessionProjection, ids []string) {
			if role, _ := view.Messages[0].Custom["role"].(string); role != agent.RoleCompactionSummary || len(view.Messages) != 11 {
				t.Fatalf("messages after compaction: %d, first %+v", len(view.Messages), view.Messages[0])
			}
		}},
		{"leaf move", func(t *testing.T, s *Session, ids []string) {
			if err := s.Branch(ids[3]); err != nil {
				t.Fatal(err)
			}
			appendTurns(t, s, 3)
		}, func(t *testing.T, view SessionProjection, ids []string) {
			if len(view.Messages) != 10 {
				t.Fatalf("%d messages after moving the leaf to the second answer and three turns, want 10", len(view.Messages))
			}
		}},
		{"fork", func(t *testing.T, s *Session, ids []string) {
			if _, err := s.CreateBranchedSession(ids[5]); err != nil {
				t.Fatal(err)
			}
			appendTurns(t, s, 2)
		}, func(t *testing.T, view SessionProjection, ids []string) {
			if len(view.Messages) != 10 {
				t.Fatalf("%d messages after forking at the third answer and two turns, want 10", len(view.Messages))
			}
		}},
		{"context edit", func(t *testing.T, s *Session, ids []string) {
			if _, err := s.AppendContextEdit(ids[2], &ContextEditReplacement{Content: []byte(`"edited"`)}); err != nil {
				t.Fatal(err)
			}
		}, func(t *testing.T, view SessionProjection, ids []string) {
			if blocks := view.Messages[2].ContentBlocks(); len(blocks) != 1 || blocks[0] != ai.ContentBlock(ai.TextContent{Text: "edited"}) {
				t.Fatalf("edited message holds %+v", blocks)
			}
		}},
		{"model change", func(t *testing.T, s *Session, ids []string) {
			if _, err := s.AppendModelChange("other-provider", "other-model"); err != nil {
				t.Fatal(err)
			}
		}, func(t *testing.T, view SessionProjection, ids []string) {
			if view.Model == nil || view.Model.Provider != "other-provider" || view.Model.ModelID != "other-model" {
				t.Fatalf("model %+v after a model change", view.Model)
			}
		}},
		{"thinking level change", func(t *testing.T, s *Session, ids []string) {
			if _, err := s.AppendThinkingLevelChange("high"); err != nil {
				t.Fatal(err)
			}
		}, func(t *testing.T, view SessionProjection, ids []string) {
			if view.ThinkingLevel != "high" {
				t.Fatalf("thinking level %q after a change to high", view.ThinkingLevel)
			}
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			session, ids := projectionSession(t)
			tc.change(t, session, ids)
			view := session.BuildSessionProjectionView()
			sameProjection(t, tc.name, view, freshProjectionView(session))
			tc.check(t, view, ids)
		})
	}
}

// Concurrent reads of the view while turns are appended each see a projection equal to some state of the branch, and the race detector
// sees the kept projection extended only under its lock.
func TestProjectionViewConcurrentReadsAndAppends(t *testing.T) {
	session, _ := projectionSession(t)
	done := make(chan struct{})
	var wg sync.WaitGroup
	for range 4 {
		wg.Go(func() {
			for {
				select {
				case <-done:
					return
				default:
				}
				view := session.BuildSessionProjectionView()
				if len(view.Messages) == 0 || len(view.Entries) != len(view.Messages) {
					t.Errorf("view of %d entries holds %d messages", len(view.Entries), len(view.Messages))
					return
				}
			}
		})
	}
	appendTurns(t, session, 20)
	close(done)
	wg.Wait()
	sameProjection(t, "after concurrent reads", session.BuildSessionProjectionView(), freshProjectionView(session))
}
