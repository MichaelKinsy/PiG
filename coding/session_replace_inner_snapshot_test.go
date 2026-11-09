package coding

import (
	"encoding/json"
	"fmt"
	"sync"
	"testing"

	"github.com/MichaelKinsy/PiG/agent"
	"github.com/MichaelKinsy/PiG/ai"
	icodingagent "github.com/MichaelKinsy/PiG/internal/codingagent"
)

// ReplaceInner stores noSession under the Session's mutex, and prepareNewSession (/new) reads it on the goroutine that runs the command: -race fails this test when the flag is a plain field.
func TestSessionNewSessionReadsTheNoSessionFlagWhileReplaceInnerSwapsIt(t *testing.T) {
	session, err := NewSession(warmingServices(t, "off"), SessionOptions{Model: warmingModel(&warmingProvider{}), NoSession: true, SkipBuiltinTools: true})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = session.Close() })
	cwd := session.CWD()
	const replacements = 50
	var readers sync.WaitGroup
	for range 4 {
		readers.Go(func() {
			for range replacements {
				next, err := session.prepareNewSession("")
				if err != nil {
					t.Error(err)
					return
				}
				if next.Path() != "" {
					t.Errorf("a Session started with NoSession got the file-backed log %q", next.Path())
					return
				}
			}
		})
	}
	for i := range replacements {
		session.ReplaceInner(icodingagent.NewSession(fmt.Sprintf("replacement-%d", i), cwd))
	}
	readers.Wait()
}

// A request reads the router state from the log the Session owns, and the entry it stores for the state the router returns belongs to that same log: a replacement that lands while the router runs does not move the entry to the log it installed.
func TestRouterStateIsStoredOnTheLogTheRequestReadItFrom(t *testing.T) {
	var suite *virtualSuite
	var original, replacement *icodingagent.Session
	route := func(request ModelRouteRequest, find func(string) *ai.Model) (ModelRoute, error) {
		replacement = icodingagent.NewSession("replacement", suite.session.CWD())
		suite.session.ReplaceInner(replacement)
		route, err := defaultVirtualRoute(request, find)
		route.State = json.RawMessage(`{"turns":1}`)
		return route, err
	}
	suite = newVirtualSuite(t, route, "", nil)
	original = suite.session.Inner()
	user := agent.AgentMessage{User: &agent.UserMessage{Role: agent.RoleUser, Content: ai.UserContentBlocks{ai.TextContent{Text: "hello"}}, Timestamp: 1}}

	if _, err := suite.session.routeRequest(t.Context(), suite.session.Model(), ai.ThinkingHigh, []agent.AgentMessage{user}, nil); err != nil {
		t.Fatal(err)
	}

	stored := func(log *icodingagent.Session) int {
		count := 0
		for _, entry := range log.GetBranch() {
			if entry.Base().Type == "custom" {
				count++
			}
		}
		return count
	}
	if got := stored(original); got != 1 {
		t.Errorf("the log the request read the state from holds %d custom entries, want the router state", got)
	}
	if got := stored(replacement); got != 0 {
		t.Errorf("the log installed while the router ran holds %d custom entries, want none", got)
	}
}
