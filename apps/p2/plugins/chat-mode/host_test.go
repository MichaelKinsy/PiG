package chatmode_test

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/MichaelKinsy/PiG/ai"
	"github.com/MichaelKinsy/PiG/apps/p2/p2test"
	"github.com/MichaelKinsy/PiG/apps/p2/plugins/chat"
	chatmode "github.com/MichaelKinsy/PiG/apps/p2/plugins/chat-mode"
	"github.com/MichaelKinsy/PiG/apps/p2/plugins/chat-mode/contract"
	chatcontract "github.com/MichaelKinsy/PiG/apps/p2/plugins/chat/contract"
	filescontract "github.com/MichaelKinsy/PiG/apps/p2/plugins/files/contract"
	"github.com/MichaelKinsy/PiG/apps/p2/plugins/models"
	modelscontract "github.com/MichaelKinsy/PiG/apps/p2/plugins/models/contract"
	statuscontract "github.com/MichaelKinsy/PiG/apps/p2/plugins/status/contract"
	p2runtime "github.com/MichaelKinsy/PiG/apps/p2/runtime"
	"github.com/MichaelKinsy/PiG/chord/delta"
	"github.com/MichaelKinsy/PiG/chord/next/data"
	"github.com/MichaelKinsy/PiG/chord/next/host"
	"github.com/MichaelKinsy/PiG/durable"
	durableenv "github.com/MichaelKinsy/PiG/durable/env"
	"github.com/MichaelKinsy/PiG/durable/env/node"
	"github.com/MichaelKinsy/PiG/durable/harness"
	sqlitenode "github.com/MichaelKinsy/PiG/durable/storage/sqlite/node"
)

// A bare session host for chat-mode tests (test/p2/chat-mode-host.ts): the chat worker facet and the facet under test, over a
// real Durable harness on a SQLite file with the fake provider. No worker process or HTTP.

var (
	ownerPrincipal = p2runtime.OwnerPrincipal{Access: p2runtime.AccessLocal}
	alicePrincipal = p2runtime.GuestPrincipal{ID: "alice", Name: "Alice", Home: "s1", Sessions: map[string]p2runtime.GuestMode{"s1": p2runtime.GuestFull}, Revision: 1}
	bobPrincipal   = p2runtime.GuestPrincipal{ID: "bob", Name: "Bob", Home: "s1", Sessions: map[string]p2runtime.GuestMode{"s1": p2runtime.GuestView}, Revision: 1}
	where          = delta.JsonObjectOf("session", "s1", "conversation", float64(1))
)

// localEnvironments is the one local directory every conversation works in.
type localEnvironments struct {
	dir string
	env durableenv.ExecutionEnv
}

func (e localEnvironments) Session() p2runtime.Location { return p2runtime.Location{Cwd: e.dir} }
func (e localEnvironments) Locate(context.Context, durable.ConversationId, durable.DocumentReader) (p2runtime.Location, error) {
	return p2runtime.Location{Cwd: e.dir}, nil
}
func (e localEnvironments) At(context.Context, p2runtime.Location) (durableenv.ExecutionEnv, error) {
	return e.env, nil
}
func (e localEnvironments) Sandbox(context.Context, *string) (string, error) { return "", nil }

type modeHost struct {
	*p2test.TestHost
	durable harness.Harness
	dir     string
	closed  bool
}

// close stops the host and its harness.
func (m *modeHost) close() {
	if m.closed {
		return
	}
	m.closed = true
	m.TestHost.Close()
	_ = m.durable.Close(p2test.Background)
}

// newModeHost opens the host on directory dir (a previous host's reopens the same session).
func newModeHost(t *testing.T, dir string) *modeHost {
	t.Helper()
	fakeModels := models.CreateFakeModels(ai.CreateModelsOptions{})
	settingsOf := func() *harness.HarnessSettings { return &harness.HarnessSettings{} }
	storage, err := sqlitenode.OpenNodeSqliteStorage(filepath.Join(dir, "session.sqlite"), sqlitenode.NodeSqliteStorageOptions{})
	if err != nil {
		t.Fatal(err)
	}
	opened, err := harness.OpenHarness(p2test.Background, storage, harness.HarnessOptions{Models: fakeModels, Registry: harness.CreateRegistry(), Settings: settingsOf})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := opened.Root(p2test.Background, &harness.RootOptions{Agent: &harness.AgentChange{
		Model: harness.SetTo(durable.ModelRef{Provider: "faux", ModelId: "faux-1"}), Cwd: harness.SetTo(dir),
	}}); err != nil {
		t.Fatal(err)
	}
	opened.Resume()
	fixture := &modeHost{durable: opened, dir: dir}
	address := p2runtime.SessionKind.Address(delta.JsonObjectOf("id", "s1"))
	parts := &p2runtime.HarnessParts{
		Durable: func() (harness.Harness, error) { return opened, nil },
		Environments: func() (p2runtime.ExecutionEnvironments, error) {
			return localEnvironments{dir: dir, env: node.NewNodeExecutionEnv(node.NodeExecutionEnvOptions{Cwd: dir})}, nil
		},
		Relocate: func(context.Context, p2runtime.Location, func()) error { return nil },
	}
	var created *p2test.TestHost
	worker := &p2runtime.WorkerParts{Liveness: p2runtime.NewLiveness(), Location: p2runtime.NewSessionLocation(p2runtime.Location{Cwd: dir})}
	configuration := host.DefineFacet(func(_ host.FacetHost, self host.FacetSelf) error {
		return p2runtime.HarnessConfig.Contribute(self, p2runtime.Entry("default", p2runtime.HarnessConfigEntry{
			Models: func() durable.Models { return fakeModels }, Settings: settingsOf,
		}))
	})
	facets := []host.Facet{p2test.WorkerFacet(parts, worker, func() host.Host { return created.Host }), configuration, chat.Facet(), chatmode.Worker}
	created = p2test.NewTestHost(t, p2test.TestHostOptions{
		Address: &address, Worker: true, Facets: facets,
		Stubs: []p2test.Stub{
			// Upload IDs resolve to files named after them, in a temporary directory that is never read (no images).
			{Contract: filescontract.Attachments, Factory: func(*delta.JsonObject, p2runtime.Principal, p2test.StubSelf) p2test.StubMembers {
				return p2test.StubMembers{Methods: map[string]p2test.StubMethod{"resolve": func(args delta.JsonValue, _ context.Context) (delta.JsonValue, error) {
					ids, _ := args.(*delta.JsonObject).Value("ids").([]any)
					files := make([]any, len(ids))
					for i, id := range ids {
						files[i] = delta.JsonObjectOf("id", id, "name", fmt.Sprint(id)+".txt", "path", filepath.Join(os.TempDir(), fmt.Sprint(id)+".txt"), "size", float64(1))
					}
					return files, nil
				}}}
			}},
			// No server here: the worker's status pushes go nowhere.
			{Contract: statuscontract.StatusReport, Factory: func(*delta.JsonObject, p2runtime.Principal, p2test.StubSelf) p2test.StubMembers {
				return p2test.StubMembers{Methods: map[string]p2test.StubMethod{"push": func(delta.JsonValue, context.Context) (delta.JsonValue, error) { return nil, nil }}}
			}},
			// No settings.json: the owner's entries carry the default name.
			{Contract: modelscontract.WorkerModels, Factory: func(_ *delta.JsonObject, _ p2runtime.Principal, self p2test.StubSelf) p2test.StubMembers {
				return p2test.StubMembers{State: map[string]any{"settings": self.Cell(delta.NewJsonObject(0))}}
			}},
		},
	})
	fixture.TestHost = created
	t.Cleanup(fixture.close)
	return fixture
}

func object(value any) *delta.JsonObject {
	object, _ := value.(*delta.JsonObject)
	return object
}

func jsonText(t *testing.T, value any) string {
	t.Helper()
	var encoded bytes.Buffer
	encoder := json.NewEncoder(&encoded)
	encoder.SetEscapeHTML(false)
	if err := encoder.Encode(value); err != nil {
		t.Fatal(err)
	}
	return strings.TrimSuffix(encoded.String(), "\n")
}

func sameJSON(t *testing.T, value any, want string) bool {
	t.Helper()
	var got, expected any
	if err := json.Unmarshal([]byte(jsonText(t, value)), &got); err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal([]byte(want), &expected); err != nil {
		t.Fatal(err)
	}
	return fmt.Sprint(got) == fmt.Sprint(expected)
}

func until(t *testing.T, source p2test.JSONSource, predicate func(delta.JsonValue) bool) delta.JsonValue {
	t.Helper()
	value, err := p2test.Until(source, predicate, 30*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	return value
}

func chatOf(t *testing.T, h *modeHost, principal p2runtime.Principal, variant string, params ...*delta.JsonObject) *host.Reference {
	t.Helper()
	target := where
	if len(params) > 0 {
		target = params[0]
	}
	ref, err := p2test.HoldIn(h.Client(principal), chatcontract.Chat, target, variant)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(ref.Release)
	return ref
}

func modeOf(t *testing.T, h *modeHost, principal p2runtime.Principal, variant string, params ...*delta.JsonObject) *host.Reference {
	t.Helper()
	target := where
	if len(params) > 0 {
		target = params[0]
	}
	ref, err := p2test.HoldIn(h.Client(principal), contract.ChatMode, target, variant)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(ref.Release)
	return ref
}

func send(t *testing.T, ref *host.Reference, fields ...any) {
	t.Helper()
	if _, err := ref.Call("send", delta.JsonObjectOf(fields...), p2test.Background); err != nil {
		t.Fatalf("send: %v", err)
	}
}

func set(t *testing.T, mode *host.Reference, enabled bool) {
	t.Helper()
	if _, err := mode.Call("set", delta.JsonObjectOf("enabled", enabled), p2test.Background); err != nil {
		t.Fatal(err)
	}
	until(t, mode.Member("enabled"), func(value delta.JsonValue) bool { return value == enabled })
}

func entriesOfKind(view delta.JsonValue, kind string) []*delta.JsonObject {
	var out []*delta.JsonObject
	entries, _ := object(view).Value("entries").([]any)
	for _, entry := range entries {
		if object(entry).Value("kind") == kind {
			out = append(out, object(entry))
		}
	}
	return out
}

func chatEntries(view delta.JsonValue) []*delta.JsonObject {
	return entriesOfKind(view, "chat.message")
}

func answered(view delta.JsonValue) bool {
	entries, _ := object(view).Value("entries").([]any)
	for _, entry := range entries {
		model, _ := object(entry).Value("model").([]any)
		if len(model) > 0 && object(model[0]).Value("role") == "assistant" {
			return true
		}
	}
	return false
}

func live(view delta.JsonValue) bool {
	return view != nil && object(object(object(view).Value("docs")).Value("pi.live")).Value("run") != nil
}

func expectInvalid(t *testing.T, err error) {
	t.Helper()
	p2test.ExpectChord(t, err, data.CodeInvalid)
}

func TestKeepsChatAsDisplayOnlyEntriesAndGivesAMentionTheChatWithItsAuthors(t *testing.T) {
	h := newModeHost(t, t.TempDir())
	owner := chatOf(t, h, ownerPrincipal, "participant")
	mode := modeOf(t, h, ownerPrincipal, "owner")
	alice := chatOf(t, h, alicePrincipal, "participant")
	set(t, mode, true)

	send(t, owner, "text", "lunch first?", "requestId", "1", "attachments", []any{"menu"})
	send(t, alice, "text", "yes", "requestId", "2")
	chatted := until(t, owner.Member("view"), func(view delta.JsonValue) bool { return len(chatEntries(view)) == 2 })
	entries := chatEntries(chatted)
	want := []string{
		`{"author":{"kind":"owner","id":"owner","name":"Owner"},"text":"lunch first?","attachments":[{"id":"menu","name":"menu.txt"}]}`,
		`{"author":{"kind":"guest","id":"alice","name":"Alice"},"text":"yes","attachments":[]}`,
	}
	for i := range want {
		if !sameJSON(t, entries[i].Value("data"), want[i]) {
			t.Fatalf("chat entry %d: %s, want %s", i, jsonText(t, entries[i].Value("data")), want[i])
		}
	}
	if answered(chatted) {
		t.Fatal("a run started")
	}
	until(t, mode.Member("waiting"), func(value delta.JsonValue) bool { return value == float64(2) })

	send(t, alice, "text", "@pi which place?", "requestId", "3")
	view := until(t, owner.Member("view"), answered)
	// The mention is its own entry: data for people, the digest (after the speaker block) for the model.
	asked := entriesOfKind(view, "chat.mention")[0]
	wantData := fmt.Sprintf(`{"author":{"kind":"guest","id":"alice","name":"Alice"},"text":"@pi which place?","attachments":[],"included":[%v,%v]}`,
		entries[0].Value("id"), entries[1].Value("id"))
	if !sameJSON(t, asked.Value("data"), wantData) {
		t.Fatalf("mention data %s, want %s", jsonText(t, asked.Value("data")), wantData)
	}
	model, _ := asked.Value("model").([]any)
	text := jsonText(t, object(model[0]).Value("content"))
	for _, pattern := range []string{
		`Owner: lunch first\? \[attached: menu\.txt\]\\nAlice \(guest\): yes\\n</chat>`,
		`Alice \(guest\) mentioned you:\\n@pi which place\?`,
		`menu\.txt \(menu\.txt, 1 bytes\)`,
	} {
		if !regexp.MustCompile(pattern).MatchString(text) {
			t.Fatalf("%s does not match %s", text, pattern)
		}
	}
	until(t, mode.Member("waiting"), func(value delta.JsonValue) bool { return value == float64(0) })
	// An ordinary input with chat mode off is a p2.input entry with the same kind of data.
	set(t, mode, false)
	send(t, owner, "text", "plain", "requestId", "4", "attachments", []any{"notes"})
	plain := until(t, owner.Member("view"), func(view delta.JsonValue) bool { return len(entriesOfKind(view, "p2.input")) > 0 })
	if got := entriesOfKind(plain, "p2.input")[0].Value("data"); !sameJSON(t, got, `{"author":{"kind":"owner","id":"owner","name":"Owner"},"text":"plain","attachments":[{"id":"notes","name":"notes.txt"}]}`) {
		t.Fatalf("plain input data %s", jsonText(t, got))
	}
}

func TestPlacesChatAtOnceDuringARunAndHandsChatPlacedWhileAMentionWaitedToTheNextMention(t *testing.T) {
	h := newModeHost(t, t.TempDir())
	owner := chatOf(t, h, ownerPrincipal, "participant")
	mode := modeOf(t, h, ownerPrincipal, "owner")
	alice := chatOf(t, h, alicePrincipal, "participant")
	send(t, owner, "text", "slow run", "requestId", "run")
	until(t, owner.Member("view"), live)
	set(t, mode, true)
	// While Pi works, a mention waits in the queue; a chat message sent after it is placed at once.
	send(t, alice, "text", "@pi first", "requestId", "m1", "whenBusy", "followUp")
	send(t, owner, "text", "raced", "requestId", "c1")
	during := until(t, owner.Member("view"), func(view delta.JsonValue) bool { return len(chatEntries(view)) == 1 })
	if !live(during) {
		t.Fatal("the chat message did not wait for the run")
	}
	raced := chatEntries(during)[0]
	first := until(t, owner.Member("view"), func(view delta.JsonValue) bool { return len(entriesOfKind(view, "chat.mention")) == 1 && !live(view) })
	if included, _ := object(entriesOfKind(first, "chat.mention")[0].Value("data")).Value("included").([]any); len(included) != 0 {
		t.Fatalf("composed before the chat, but included %v", included)
	}
	send(t, alice, "text", "@pi second", "requestId", "m2")
	second := until(t, owner.Member("view"), func(view delta.JsonValue) bool { return len(entriesOfKind(view, "chat.mention")) == 2 })
	mention := entriesOfKind(second, "chat.mention")[1]
	if included, _ := object(mention.Value("data")).Value("included").([]any); len(included) != 1 || included[0] != raced.Value("id") {
		t.Fatalf("included %v, want [%v]", included, raced.Value("id"))
	}
	if model := jsonText(t, mention.Value("model")); !strings.Contains(model, "Owner: raced") {
		t.Fatalf("model %s", model)
	}
}

func TestKeepsTheSwitchInTheConversationAcrossAWorkerRestart(t *testing.T) {
	dir := t.TempDir()
	first := newModeHost(t, dir)
	mode := modeOf(t, first, ownerPrincipal, "owner")
	set(t, mode, true)
	mode.Release()
	first.close()

	second := newModeHost(t, dir)
	again := modeOf(t, second, ownerPrincipal, "owner")
	// The cell's first value comes from the document, before anyone can read it.
	until(t, again.Member("enabled"), func(value delta.JsonValue) bool { return value == true })
}

func TestLetsOnlyTheOwnerSwitchItAndReadOnlyGuestsCannotChat(t *testing.T) {
	h := newModeHost(t, t.TempDir())
	owner, err := p2test.HoldIn(h.Client(ownerPrincipal), contract.ChatMode, where, "owner")
	if err != nil {
		t.Fatal(err)
	}
	alice, err := p2test.HoldIn(h.Client(alicePrincipal), contract.ChatMode, where, "reader")
	if err != nil {
		t.Fatal(err)
	}
	bob, err := p2test.HoldIn(h.Client(bobPrincipal), contract.ChatMode, where, "reader")
	if err != nil {
		t.Fatal(err)
	}
	for _, ref := range []*host.Reference{owner, alice, bob} {
		defer ref.Release()
	}
	// A reader has no `set` and a viewer no `send`: called anyway, the provider refuses them.
	_, err = alice.Call("set", delta.JsonObjectOf("enabled", true), p2test.Background)
	expectInvalid(t, err)
	viewer := chatOf(t, h, bobPrincipal, "viewer")
	_, err = viewer.Call("send", delta.JsonObjectOf("text", "hi", "requestId", "b"), p2test.Background)
	expectInvalid(t, err)
	stranger := p2runtime.GuestPrincipal{ID: "carol", Name: "Alice", Home: "s2", Sessions: map[string]p2runtime.GuestMode{"s2": p2runtime.GuestFull}, Revision: 1}
	foreign, err := h.Client(stranger).Hold(p2test.Background, contract.ChatMode, where, host.GetOptions{})
	if err != nil {
		t.Fatal(err)
	}
	defer foreign.Release()
	p2test.ExpectChord(t, p2runtime.WhenLive(p2test.Background, foreign), data.CodeForbidden)
}

func TestShowsASwitchOrARenameOnceTheCallReturns(t *testing.T) {
	h := newModeHost(t, t.TempDir())
	setting, err := h.Host.Get(p2test.Background, contract.ChatModeSetting, delta.JsonObjectOf("conversation", float64(1)), host.GetOptions{})
	if err != nil {
		t.Fatal(err)
	}
	defer setting.Release()
	owner := modeOf(t, h, ownerPrincipal, "owner")
	read := func(member string) delta.JsonValue {
		value, err := until2(setting, member)
		if err != nil {
			t.Fatal(err)
		}
		return value
	}
	until(t, setting.Member("enabled"), func(value delta.JsonValue) bool { return value != nil })
	if read("enabled") != false {
		t.Fatalf("enabled %v", read("enabled"))
	}
	if _, err := owner.Call("set", delta.JsonObjectOf("enabled", true), p2test.Background); err != nil {
		t.Fatal(err)
	}
	if read("enabled") != true {
		t.Fatal("not committed and followed before the answer")
	}
	if _, err := owner.Call("rename", delta.JsonObjectOf("name", "Claude"), p2test.Background); err != nil {
		t.Fatal(err)
	}
	if read("name") != "Claude" {
		t.Fatalf("name %v", read("name"))
	}
}

func until2(ref *host.Reference, member string) (delta.JsonValue, error) {
	return ref.Member(member).Value()
}

func TestLetsOnlyTheOwnerRenameTheAgentAndMentionsThenUseTheNewName(t *testing.T) {
	h := newModeHost(t, t.TempDir())
	mode := modeOf(t, h, ownerPrincipal, "owner")
	reader := modeOf(t, h, alicePrincipal, "reader")
	alice := chatOf(t, h, alicePrincipal, "participant")
	until(t, mode.Member("name"), func(value delta.JsonValue) bool { return value == "Pi" })
	_, err := reader.Call("rename", delta.JsonObjectOf("name", "Mallory"), p2test.Background)
	expectInvalid(t, err)
	_, err = mode.Call("rename", delta.JsonObjectOf("name", "two words"), p2test.Background)
	expectInvalid(t, err)
	set(t, mode, true)
	if _, err := mode.Call("rename", delta.JsonObjectOf("name", "Claude"), p2test.Background); err != nil {
		t.Fatal(err)
	}
	until(t, reader.Member("name"), func(value delta.JsonValue) bool { return value == "Claude" })
	// @pi is ordinary chat now; @claude asks the agent.
	send(t, alice, "text", "@pi hello?", "requestId", "1")
	view := until(t, alice.Member("view"), func(view delta.JsonValue) bool { return len(chatEntries(view)) == 1 })
	if answered(view) {
		t.Fatal("a run for @pi")
	}
	send(t, alice, "text", "@claude which place?", "requestId", "2")
	view = until(t, alice.Member("view"), answered)
	if got := object(entriesOfKind(view, "chat.mention")[0].Value("data")).Value("text"); got != "@claude which place?" {
		t.Fatalf("asked %v", got)
	}
}

func TestChecksAReplysTargetAndDerivesWhatItQuotesFromThatEntry(t *testing.T) {
	h := newModeHost(t, t.TempDir())
	owner := chatOf(t, h, ownerPrincipal, "participant")
	alice := chatOf(t, h, alicePrincipal, "participant")
	set(t, modeOf(t, h, ownerPrincipal, "owner"), true)
	long := "should we split the tokenizer work from the parser work, or keep both in one change?"
	send(t, owner, "text", long, "requestId", "1")
	question := chatEntries(until(t, owner.Member("view"), func(view delta.JsonValue) bool { return len(chatEntries(view)) == 1 }))[0]

	// The client only names the entry; author and excerpt come from the entry itself.
	send(t, alice, "text", "split it", "requestId", "2", "replyTo", question.Value("id"))
	reply := chatEntries(until(t, owner.Member("view"), func(view delta.JsonValue) bool { return len(chatEntries(view)) == 2 }))[1]
	wantReply := fmt.Sprintf(`{"entryId":%v,"author":{"kind":"owner","id":"owner","name":"Owner"},"excerpt":"should we split the tokenizer work from the parser work, or…"}`, question.Value("id"))
	if got := object(reply.Value("data")).Value("replyTo"); !sameJSON(t, got, wantReply) {
		t.Fatalf("replyTo %s, want %s", jsonText(t, got), wantReply)
	}
	// A forged quote does not even pass the contract: `replyTo` is an entry ID.
	forged := delta.JsonObjectOf("entryId", question.Value("id"), "author", delta.JsonObjectOf("kind", "owner", "id", "owner", "name", "Owner"), "excerpt", "x")
	_, err := alice.Call("send", delta.JsonObjectOf("text", "forged", "requestId", "3", "replyTo", forged), p2test.Background)
	expectInvalid(t, err)
	_, err = alice.Call("send", delta.JsonObjectOf("text", "unknown", "requestId", "4", "replyTo", float64(9999)), p2test.Background)
	expectInvalid(t, err)

	// An entry of another conversation of the same session is not found from this one.
	created, err := h.durable.CreateConversation(p2test.Background, harness.ConversationCreateOptions{Ownership: durable.ConversationOwnership{Kind: durable.ConversationOwnerless}})
	if err != nil {
		t.Fatal(err)
	}
	elsewhere := delta.JsonObjectOf("session", "s1", "conversation", float64(created.Id()))
	set(t, modeOf(t, h, ownerPrincipal, "owner", elsewhere), true)
	side := chatOf(t, h, ownerPrincipal, "participant", elsewhere)
	send(t, side, "text", "in another conversation", "requestId", "5")
	foreign := chatEntries(until(t, side.Member("view"), func(view delta.JsonValue) bool { return len(chatEntries(view)) == 1 }))[0]
	_, err = alice.Call("send", delta.JsonObjectOf("text", "foreign", "requestId", "6", "replyTo", foreign.Value("id")), p2test.Background)
	expectInvalid(t, err)

	// Pi's answers can be replied to as well, and a mention can be a reply.
	send(t, alice, "text", "@pi which one is smaller?", "requestId", "7")
	view := until(t, owner.Member("view"), answered)
	var answer *delta.JsonObject
	entries, _ := object(view).Value("entries").([]any)
	for _, entry := range entries {
		model, _ := object(entry).Value("model").([]any)
		if len(model) > 0 && object(model[0]).Value("role") == "assistant" {
			answer = object(entry)
		}
	}
	send(t, owner, "text", "thanks", "requestId", "8", "replyTo", answer.Value("id"))
	thanks := chatEntries(until(t, owner.Member("view"), func(next delta.JsonValue) bool { return len(chatEntries(next)) == 3 }))[2]
	quoted := object(object(thanks.Value("data")).Value("replyTo"))
	if !sameJSON(t, quoted.Value("author"), `{"kind":"agent","id":"agent","name":"Pi"}`) || !strings.HasPrefix(quoted.Value("excerpt").(string), "Echo: ") {
		t.Fatalf("quote of an answer: %s", jsonText(t, quoted))
	}
	// Inputs are replyable from their data: a mention quotes what the person wrote, not the digest.
	input := entriesOfKind(view, "chat.mention")[0]
	send(t, owner, "text", "re", "requestId", "9", "replyTo", input.Value("id"))
	re := chatEntries(until(t, owner.Member("view"), func(next delta.JsonValue) bool { return len(chatEntries(next)) == 4 }))[3]
	wantRe := fmt.Sprintf(`{"entryId":%v,"author":{"kind":"guest","id":"alice","name":"Alice"},"excerpt":"@pi which one is smaller?"}`, input.Value("id"))
	if got := object(re.Value("data")).Value("replyTo"); !sameJSON(t, got, wantRe) {
		t.Fatalf("replyTo %s, want %s", jsonText(t, got), wantRe)
	}
	// A mention can be a reply to a chat message: the asking line says what it answers.
	send(t, owner, "text", "@pi and now?", "requestId", "10", "replyTo", reply.Value("id"))
	asked := until(t, owner.Member("view"), func(next delta.JsonValue) bool {
		entries, _ := object(next).Value("entries").([]any)
		return len(entries) > 0 && strings.Contains(jsonText(t, entries[len(entries)-1]), "replying to Alice")
	})
	entries, _ = object(asked).Value("entries").([]any)
	model := jsonText(t, object(entries[len(entries)-1]).Value("model"))
	if !regexp.MustCompile(`Owner \(replying to Alice: \\"split it\\"\) mentioned you:`).MatchString(model) {
		t.Fatalf("model %s", model)
	}
}
