// Package clienttest holds the TUI client tests that mount the plugins' surfaces together (Pi's test/p2/tui-*.test.ts over test/p2/tui-fixture.ts).
package clienttest

import (
	"context"
	"errors"
	"regexp"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/MichaelKinsy/PiG/apps/p2/p2test"
	chatcontract "github.com/MichaelKinsy/PiG/apps/p2/plugins/chat/contract"
	"github.com/MichaelKinsy/PiG/apps/p2/plugins/sessions/contract"
	p2runtime "github.com/MichaelKinsy/PiG/apps/p2/runtime"
	"github.com/MichaelKinsy/PiG/apps/p2/tui/components"
	tuiruntime "github.com/MichaelKinsy/PiG/apps/p2/tui/runtime"
	"github.com/MichaelKinsy/PiG/apps/p2/tui/tuitest"
	"github.com/MichaelKinsy/PiG/chord/delta"
	"github.com/MichaelKinsy/PiG/durable"
)

const (
	enter = "\r"
	esc   = "\x1b"
	tab   = "\t"
	down  = "\x1b[B"
	up    = "\x1b[A"
	ctrlR = "\x12"
	ctrlA = "\x01"
	ctrlK = "\x0b"
	ctrlN = "\x0e"
	altP  = "\x1bp"
)

// fixtureSession is test/p2/tui-fixture.ts SESSION.
func fixtureSession() contract.Session {
	return contract.Session{
		ID: "s", Cwd: "/work", Title: "Fixture", CreatedAt: 1, UpdatedAt: 1,
		Model: durable.ModelRef{Provider: "faux", ModelId: "faux-1"}, ThinkingLevel: "off", Main: 1,
	}
}

func typed(f *tuitest.Fixture, text string) {
	for _, char := range text {
		f.Send(string(char))
	}
}

// pollScreen waits for the screen to satisfy cond.
func pollScreen(t testing.TB, f *tuitest.Fixture, cond func(screen string) bool) {
	t.Helper()
	last := ""
	deadline := time.Now().Add(5 * time.Second)
	for {
		last = f.Screen()
		if cond(last) {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("poll: the screen never satisfied the condition; last screen:\n%s", last)
		}
		time.Sleep(5 * time.Millisecond)
	}
}

func pollContains(t testing.TB, f *tuitest.Fixture, text string) {
	t.Helper()
	pollScreen(t, f, func(screen string) bool { return strings.Contains(screen, text) })
}

func matches(pattern string) func(string) bool {
	re := regexp.MustCompile(pattern)
	return re.MatchString
}

// gate is a call the test settles: call waits until pass or refuse(message).
type gate struct {
	mu   sync.Mutex
	open chan error
}

func newGate() *gate { return &gate{open: make(chan error, 1)} }

func (g *gate) current() chan error {
	g.mu.Lock()
	defer g.mu.Unlock()
	return g.open
}

func (g *gate) call() error {
	open := g.current()
	err := <-open
	open <- err
	return err
}
func (g *gate) pass() {
	select {
	case g.current() <- nil:
	default:
	}
}
func (g *gate) refuse(message string) {
	select {
	case g.current() <- errors.New(message):
	default:
	}
}
func (g *gate) reset() {
	g.mu.Lock()
	g.open = make(chan error, 1)
	g.mu.Unlock()
}

func rowsOf(sessions ...contract.Session) []any {
	rows := make([]any, len(sessions))
	for i, session := range sessions {
		rows[i] = session.JSON()
	}
	return rows
}

// patchRows replaces every row (a copy) by patch of it.
func patchRows(cell *p2test.Cell, patch func(row *delta.JsonObject)) {
	_ = cell.Change(func(current any) any {
		rows, _ := current.([]any)
		out := make([]any, len(rows))
		for i, each := range rows {
			row := each.(*delta.JsonObject).Clone()
			patch(row)
			out[i] = row
		}
		return out
	})
}

func noticeWith(f *tuitest.Fixture, kind components.NoticeKind, text string) func() bool {
	return func() bool {
		found := false
		f.Do(func() {
			for _, notice := range f.Runtime.Notices {
				if notice.Kind == kind && strings.Contains(notice.Text, text) {
					found = true
				}
			}
		})
		return found
	}
}

var _ = p2runtime.Host
var _ = context.Background

func containsText(screen, text string) bool { return strings.Contains(screen, text) }

func metaJSON() *delta.JsonObject {
	return delta.JsonObjectOf("provider", "faux", "modelId", "faux-1", "name", "Faux", "contextWindow", float64(1000), "reasoning", false, "thinkingLevels", []any{"off"})
}

func emptyView() *delta.JsonObject {
	return delta.JsonObjectOf("conversation", delta.JsonObjectOf("id", float64(1)), "entries", []any{}, "docs", delta.JsonObjectOf())
}

func unusedMethod(delta.JsonValue, context.Context) (delta.JsonValue, error) {
	return nil, errors.New("not in this test")
}

// chatWorker is the session worker's Chat of the location and project tests: an empty conversation, nothing sent.
func chatWorker() p2test.Stub {
	return p2test.Stub{Contract: chatcontract.Chat, Factory: func(_ *delta.JsonObject, _ p2runtime.Principal, self p2test.StubSelf) p2test.StubMembers {
		methods := map[string]p2test.StubMethod{
			"entries": func(delta.JsonValue, context.Context) (delta.JsonValue, error) {
				return delta.JsonObjectOf("items", []any{}, "more", false), nil
			},
		}
		for _, name := range []string{"send", "stop", "dequeue", "configure", "compact", "abortCompaction", "reset", "shell", "abortShell", "dequeueOwn"} {
			methods[name] = unusedMethod
		}
		return p2test.StubMembers{
			Variant: "participant",
			State:   map[string]any{"view": emptyView(), "model": metaJSON(), "liveShell": nil, "stats": nil},
			Methods: methods,
		}
	}}
}

func roleOf(role string) tuiruntime.TuiRole {
	if role == "guest" {
		return tuiruntime.RoleGuest
	}
	return tuiruntime.RoleOwner
}
