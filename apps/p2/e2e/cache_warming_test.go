// Ports the worker case of test/p2/cache-warming.test.ts (Pi c9552c7d): a worker keeps the main conversation's cache warm
// after its run and counts the refreshes' spend. The fake provider is given a short cache lifetime; the warmers' own
// behavior is plugins/cache-warming/warmer_test.go.

package e2e_test

import (
	"bufio"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/MichaelKinsy/PiG/apps/p2/hosts"
	"github.com/MichaelKinsy/PiG/apps/p2/p2test"
	chatcontract "github.com/MichaelKinsy/PiG/apps/p2/plugins/chat/contract"
	sessionscontract "github.com/MichaelKinsy/PiG/apps/p2/plugins/sessions/contract"
	"github.com/MichaelKinsy/PiG/chord/delta"

	_ "github.com/MichaelKinsy/PiG/apps/p2/plugins/cache-warming"
)

func TestAWorkerKeepsTheMainConversationsCacheWarmAfterItsRunAndCountsTheRefreshesSpend(t *testing.T) {
	p2test.NoLeakedGoroutines(t)
	scratch := t.TempDir()
	requestLog := filepath.Join(scratch, "requests.jsonl")
	if err := os.WriteFile(requestLog, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	fixture := p2test.StartFixture(t, hosts.ServerOptions{WorkerEnv: map[string]string{"P2_FAKE_REQUESTS": requestLog}})
	// The fake model as a cached, expensive one, before the session's worker reads models.json: an 11-second lifetime
	// refreshes after one second, and every refresh pays off even for its small prompts.
	const models = `{"providers":{"faux":{"modelOverrides":{"faux-1":{"cost":{"input":4000,"output":20000,"cacheRead":200,"cacheWrite":5000},"promptCache":{"short":11}}}}}}`
	if err := os.WriteFile(filepath.Join(fixture.DataDir, "models.json"), []byte(models), 0o600); err != nil {
		t.Fatal(err)
	}
	owner := fixture.Browser()
	sessions, err := p2test.HoldIn(owner, sessionscontract.Sessions, delta.NewJsonObject(0), "owner")
	if err != nil {
		t.Fatal(err)
	}
	created, err := sessions.Call("create", delta.JsonObjectOf("cwd", fixture.Workspace), p2test.Background)
	if err != nil {
		t.Fatal(err)
	}
	session := object(created).Value("id").(string)
	chat, err := p2test.HoldIn(owner, chatcontract.Chat, delta.JsonObjectOf("session", session, "conversation", float64(1)), "participant")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := chat.Call("send", delta.JsonObjectOf("text", "hello", "requestId", "m1"), p2test.Background); err != nil {
		t.Fatal(err)
	}
	entries := func(view delta.JsonValue) int {
		list, _ := object(view).Value("entries").([]any)
		return len(list)
	}
	// tokens is the conversation's usage in total tokens (`pi.usage`).
	tokens := func(view delta.JsonValue) float64 {
		var total float64
		models := object(object(object(object(view).Value("docs")).Value("pi.usage")).Value("models"))
		if models == nil {
			return 0
		}
		for _, key := range models.Keys() {
			total += object(models.Value(key)).Value("totalTokens").(float64)
		}
		return total
	}
	answered, err := p2test.Until(chat.Member("view"), func(view delta.JsonValue) bool { return entries(view) > 1 && !running(view) }, 30*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	requests := func() []string {
		file, err := os.Open(requestLog)
		if err != nil {
			return nil
		}
		defer func() { _ = file.Close() }()
		var lines []string
		scanner := bufio.NewScanner(file)
		scanner.Buffer(nil, 1<<26)
		for scanner.Scan() {
			if scanner.Text() != "" {
				lines = append(lines, scanner.Text())
			}
		}
		return lines
	}
	// Idle now: the refreshes repeat the request exactly, and their spend is the conversation's.
	var last delta.JsonValue
	if _, err := p2test.Until(chat.Member("view"), func(view delta.JsonValue) bool {
		last = view
		return tokens(view) > tokens(answered) && len(requests()) >= 3
	}, 20*time.Second); err != nil {
		t.Fatalf("tokens %v after the answer (%v), requests: %d", tokens(last), tokens(answered), len(requests()))
	}
	sent := requests()
	for _, refresh := range sent[1:] {
		if refresh != sent[0] {
			t.Fatalf("a refresh differs from the request:\n%s\n%s", sent[0], refresh)
		}
	}
}
