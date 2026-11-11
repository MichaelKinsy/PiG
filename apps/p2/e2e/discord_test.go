// Ports the ingress part of test/p2/discord.test.ts (Pi main c23b9ec1): a session's `DiscordIngress` under the fake
// provider (a batch with an attachment downloaded into the session's uploads, its turn's answer, a thread's subagent forked
// from the main conversation, the channel commands), the relay for real against it, and the owner's `Discord` view. The relay
// cases with fake sessions are plugins/discord/contract/relay_test.go.

package e2e_test

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/MichaelKinsy/PiG/apps/p2/hosts"
	"github.com/MichaelKinsy/PiG/apps/p2/p2test"
	chatcontract "github.com/MichaelKinsy/PiG/apps/p2/plugins/chat/contract"
	discordcontract "github.com/MichaelKinsy/PiG/apps/p2/plugins/discord/contract"
	filescontract "github.com/MichaelKinsy/PiG/apps/p2/plugins/files/contract"
	subagentscontract "github.com/MichaelKinsy/PiG/apps/p2/plugins/subagents/contract"
	p2runtime "github.com/MichaelKinsy/PiG/apps/p2/runtime"
	"github.com/MichaelKinsy/PiG/chord/delta"
	"github.com/MichaelKinsy/PiG/chord/next/host"

	_ "github.com/MichaelKinsy/PiG/apps/p2/plugins/discord"
	_ "github.com/MichaelKinsy/PiG/apps/p2/plugins/envs"
	_ "github.com/MichaelKinsy/PiG/apps/p2/plugins/files"
	_ "github.com/MichaelKinsy/PiG/apps/p2/plugins/send-file"
	_ "github.com/MichaelKinsy/PiG/apps/p2/plugins/status"
	_ "github.com/MichaelKinsy/PiG/apps/p2/plugins/subagents"
)

type discordRig struct {
	t       *testing.T
	f       *agentFixture
	cdnURL  string
	session string
}

// ingress is one call of the session's `DiscordIngress` as the server.
func (r *discordRig) ingress(method string, args delta.JsonValue) (delta.JsonValue, error) {
	return r.f.Fixture.Server.Host.Root(func(ctx host.Ctx) (any, error) {
		ref, err := ctx.Get(discordcontract.DiscordIngress, delta.JsonObjectOf("session", r.session), host.GetOptions{})
		if err != nil {
			return nil, err
		}
		return ref.Call(method, args, ctx)
	}, p2test.Background, host.RootOptions{})
}

func (r *discordRig) must(method string, args delta.JsonValue) *delta.JsonObject {
	r.t.Helper()
	value, err := r.ingress(method, args)
	if err != nil {
		r.t.Fatalf("%s: %v", method, err)
	}
	return object(value)
}

func noLock() *delta.JsonObject {
	return delta.JsonObjectOf("mcp", []any{}, "projectMcp", false, "instructions", false, "capabilities", []any{})
}

func (r *discordRig) input(requestID, text string, conversation float64, thread string) *delta.JsonObject {
	where := delta.JsonObjectOf("channel", "general", "guild", "Guild", "messages", float64(1))
	if thread != "" {
		where.Set("thread", thread)
	}
	in := delta.JsonObjectOf("requestId", requestID, "author", delta.JsonObjectOf("id", "u1", "name", "Ada"), "text", text,
		"context", where, "files", []any{}, "lock", nil)
	if conversation != 0 {
		in.Set("conversation", conversation)
	}
	return in
}

// ended is the turn of submission once it is no longer pending.
func (r *discordRig) ended(submission any) *delta.JsonObject {
	r.t.Helper()
	var turn *delta.JsonObject
	if err := p2test.Poll(func() (bool, error) {
		value, err := r.ingress("turn", delta.JsonObjectOf("submissionId", submission))
		if err != nil {
			return false, err
		}
		turn = object(value)
		return turn.Value("state") != "pending", nil
	}, 60*time.Second, "the turn to end"); err != nil {
		r.t.Fatal(err)
	}
	return turn
}

func (r *discordRig) command(name string) string {
	r.t.Helper()
	text, _ := r.must("command", delta.JsonObjectOf("command", name)).Value("text").(string)
	return text
}

func (r *discordRig) view(conversation float64, wanted func(delta.JsonValue) bool) delta.JsonValue {
	r.t.Helper()
	chat := r.f.hold(conversation)
	view, err := p2test.Until(chat.Member("view"), wanted, 60*time.Second)
	if err != nil {
		r.t.Fatal(err)
	}
	return view
}

func fileNames(files any) []string {
	var names []string
	for _, each := range list(files) {
		names = append(names, object(each).Value("name").(string))
	}
	return names
}

// fakeDiscord is a Discord with channels and what the bot posted.
type fakeDiscord struct {
	mu       sync.Mutex
	posted   []string
	replyTo  []string
	files    [][]string
	uploaded map[string][]byte
	history  []discordcontract.Message
}

func (d *fakeDiscord) BotID() string { return "999" }
func (d *fakeDiscord) Channel(_ context.Context, id string) (discordcontract.Channel, error) {
	return discordcontract.Channel{ID: id, Name: "general", GuildID: "g1", GuildName: "Guild"}, nil
}
func (d *fakeDiscord) Messages(_ context.Context, _, after string, limit int) ([]discordcontract.Message, error) {
	var out []discordcontract.Message
	for _, each := range d.history {
		if each.ID > after && len(out) < limit {
			out = append(out, each)
		}
	}
	return out, nil
}
func (d *fakeDiscord) Reply(_ context.Context, _, replyTo, text string, files []discordcontract.File) error {
	d.mu.Lock()
	defer d.mu.Unlock()
	var names []string
	for _, file := range files {
		d.uploaded[file.Name] = file.Data
		names = append(names, file.Name)
	}
	d.posted, d.replyTo, d.files = append(d.posted, text), append(d.replyTo, replyTo), append(d.files, names)
	return nil
}
func (d *fakeDiscord) UploadLimit(_ context.Context, _ string) (float64, error) { return 10 << 20, nil }
func (d *fakeDiscord) Typing(_ context.Context, _ string) error                 { return nil }

// ingressSessions is the relay's session port over the session's real worker; the batch's text is the fake model's
// `send-file` prompt (it acts on exact prompts, not on a batch).
type ingressSessions struct{ r *discordRig }

func (s ingressSessions) Usable(context.Context, string) (string, error) { return "", nil }
func (s ingressSessions) Lock(string) *p2runtime.SessionLock             { return nil }
func (s ingressSessions) Deliver(_ context.Context, _ string, input discordcontract.Delivery) (discordcontract.Delivered, error) {
	out := s.r.must("deliver", s.r.input(input.RequestID, "send-file", 0, ""))
	return discordcontract.Delivered{Conversation: int(out.Value("conversation").(float64)), SubmissionID: out.Value("submissionId").(float64)}, nil
}
func (s ingressSessions) Turn(_ context.Context, _ string, id float64) (discordcontract.TurnState, error) {
	turn := s.r.must("turn", delta.JsonObjectOf("submissionId", id))
	out := discordcontract.TurnState{State: turn.Value("state").(string), Files: []discordcontract.OfferedFile{}}
	out.Text, _ = turn.Value("text").(string)
	out.Answer, _ = turn.Value("answer").(float64)
	for _, each := range list(turn.Value("files")) {
		file := object(each)
		out.Files = append(out.Files, discordcontract.OfferedFile{ID: file.Value("id").(string), Name: file.Value("name").(string), Size: file.Value("size").(float64)})
	}
	return out, nil
}
func (s ingressSessions) Thread(context.Context, string, string, float64) (int, error) { return 0, nil }
func (s ingressSessions) Read(_ context.Context, _, id string, offset, length int) ([]byte, error) {
	value, err := s.r.ingress("read", delta.JsonObjectOf("id", id, "offset", float64(offset), "length", float64(length)))
	if err != nil {
		return nil, err
	}
	return value.([]byte), nil
}
func (s ingressSessions) Command(context.Context, string, *int, string) (string, error) {
	return "", nil
}

type memoryState struct {
	mu    sync.Mutex
	state discordcontract.State
}

func (m *memoryState) Value() discordcontract.State {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := discordcontract.State{Bindings: map[string]*discordcontract.Binding{}, Pending: map[string]*discordcontract.Pending{}}
	for key, value := range m.state.Bindings {
		copy := *value
		out.Bindings[key] = &copy
	}
	for key, value := range m.state.Pending {
		copy := *value
		out.Pending[key] = &copy
	}
	return out
}
func (m *memoryState) Change(_ context.Context, update func(*discordcontract.State)) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	update(&m.state)
	return nil
}

func TestDiscordIngressFakeProvider(t *testing.T) {
	cdn := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		response.Header().Set("Content-Type", "text/plain")
		fmt.Fprintf(response, "file at %s", request.URL.Path)
	}))
	defer cdn.Close()
	f := newAgentFixture(t, hosts.ServerOptions{})
	r := &discordRig{t: t, f: f, cdnURL: cdn.URL, session: f.Session}

	t.Run("places a batch as the Discord author's input with its attachment, and reads the turn's answer", func(t *testing.T) {
		in := r.input("discord:500", "[Discord #general (Guild): 1 message since the last delivery]\nAda: @Pi read this", 0, "")
		in.Set("files", []any{delta.JsonObjectOf("url", cdn.URL+"/notes.txt", "name", "notes.txt", "size", float64(20), "key", "discord:500:0", "ref", "500")})
		in.Set("lock", noLock())
		delivered := r.must("deliver", in)
		if delivered.Value("conversation") != float64(1) {
			t.Fatalf("conversation = %v", delivered.Value("conversation"))
		}
		if again := r.must("deliver", in); again.Value("submissionId") != delivered.Value("submissionId") {
			t.Fatalf("a retry was placed twice: %v %v", again, delivered)
		}
		turn := r.ended(delivered.Value("submissionId"))
		if text, _ := turn.Value("text").(string); turn.Value("state") != "answered" || !strings.HasPrefix(text, "Echo:") {
			t.Fatalf("turn = %v", turn)
		}
		view := r.view(1, func(view delta.JsonValue) bool { return batchEntry(view) != nil })
		entry := batchEntry(view)
		data := object(entry.Value("data"))
		if want := delta.JsonObjectOf("kind", "guest", "id", "discord:u1", "name", "Ada · Discord"); !jsonEqual(data.Value("author"), want) {
			t.Fatalf("author = %v", data.Value("author"))
		}
		if want := []any{delta.JsonObjectOf("id", "discord:500:0", "name", "notes.txt")}; !jsonEqual(data.Value("attachments"), want) {
			t.Fatalf("attachments = %v", data.Value("attachments"))
		}
		// `entry.model[0].content` is an array of parts, one of them the text with the `<attachments>` block.
		content := list(object(list(entry.Value("model"))[0]).Value("content"))
		if !slices.ContainsFunc(content, func(part any) bool {
			text, _ := object(part).Value("text").(string)
			return object(part).Value("type") == "text" && strings.Contains(text, "<attachments>")
		}) {
			t.Fatalf("the model message lacks the attachments: %s", jsonText(entry.Value("model")))
		}
		mirror, err := p2test.HoldIn(f.Owner, filescontract.FileSummaries, delta.NewJsonObject(0), "owner")
		if err != nil {
			t.Fatal(err)
		}
		spaces, err := mirror.Call("of", delta.JsonObjectOf("session", r.session), p2test.Background)
		if err != nil {
			t.Fatal(err)
		}
		newest := list(object(object(spaces).Value("uploads")).Value("newest"))
		if len(newest) == 0 || object(newest[0]).Value("source") != "discord" {
			t.Fatalf("uploads = %v", spaces)
		}
	})

	t.Run("a turn's answer lists the files its conversation offered during it, readable through the ingress", func(t *testing.T) {
		if err := os.WriteFile(filepath.Join(f.Fixture.Workspace, "created.txt"), []byte("made for Discord\n"), 0o600); err != nil {
			t.Fatal(err)
		}
		sent := r.must("deliver", r.input("discord:800", "send-file", 0, ""))
		turn := r.ended(sent.Value("submissionId"))
		if turn.Value("state") != "answered" || !slices.Equal(fileNames(turn.Value("files")), []string{"created.txt"}) {
			t.Fatalf("turn = %v", turn)
		}
		offered := object(list(turn.Value("files"))[0])
		if offered.Value("size") != float64(17) {
			t.Fatalf("size = %v", offered.Value("size"))
		}
		bytes, err := r.ingress("read", delta.JsonObjectOf("id", offered.Value("id"), "offset", float64(0), "length", float64(1024)))
		if err != nil || string(bytes.([]byte)) != "made for Discord\n" {
			t.Fatalf("read = %v %v", bytes, err)
		}
		// The next turn offers nothing: an earlier turn's files are not its.
		next := r.ended(r.must("deliver", r.input("discord:801", "nothing to send", 0, "")).Value("submissionId"))
		if next.Value("state") != "answered" || len(list(next.Value("files"))) != 0 {
			t.Fatalf("next = %v", next)
		}
		// A thread's subagent: its own turn's files only.
		thread := r.must("thread", delta.JsonObjectOf("name", "files", "before", float64(time.Now().UnixMilli())))
		inThread := r.ended(r.must("deliver", r.input("discord:802", "send-file", thread.Value("conversation").(float64), "")).Value("submissionId"))
		if inThread.Value("state") != "answered" || !slices.Equal(fileNames(inThread.Value("files")), []string{"created.txt"}) {
			t.Fatalf("thread turn = %v", inThread)
		}
		if object(list(inThread.Value("files"))[0]).Value("id") == offered.Value("id") {
			t.Fatal("the thread's file is the main turn's")
		}
	})

	t.Run("end to end: a mention, a turn that calls send_file, and the reply in Discord carries the file", func(t *testing.T) {
		if err := os.WriteFile(filepath.Join(f.Fixture.Workspace, "created.txt"), []byte("# Rules\nBe brief.\n"), 0o600); err != nil {
			t.Fatal(err)
		}
		discord := &fakeDiscord{uploaded: map[string][]byte{}}
		state := &memoryState{state: discordcontract.State{
			Bindings: map[string]*discordcontract.Binding{"c1": {
				BotID: "bot-a", ChannelID: "c1", ChannelName: "general", GuildID: "g1", GuildName: "Guild", Session: r.session,
				Wake: discordcontract.Wake{Users: []string{"u1"}}, Cursor: ptrOf("1"), Threads: map[string]*discordcontract.ThreadBinding{},
			}},
			Pending: map[string]*discordcontract.Pending{},
		}}
		relay := discordcontract.NewRelay(func(string) discordcontract.Port { return discord }, ingressSessions{r}, state)
		trigger := discordcontract.Message{ID: "9000", ChannelID: "c1", Author: discordcontract.Author{ID: "u1", Name: "Ada"}, Text: "<@999> send me the rules file", MentionsBot: true}
		discord.history = append(discord.history, trigger)
		if err := relay.OnMessage(p2test.Background, "bot-a", trigger); err != nil {
			t.Fatal(err)
		}
		if err := p2test.Poll(func() (bool, error) {
			if err := relay.CheckPending(p2test.Background, nil); err != nil {
				return false, err
			}
			discord.mu.Lock()
			defer discord.mu.Unlock()
			return len(discord.posted) > 0, nil
		}, 60*time.Second, "the reply"); err != nil {
			t.Fatal(err)
		}
		if discord.replyTo[0] != trigger.ID || !slices.Equal(discord.files[0], []string{"created.txt"}) || string(discord.uploaded["created.txt"]) != "# Rules\nBe brief.\n" {
			t.Fatalf("reply = %v %v %q", discord.replyTo, discord.files, discord.uploaded["created.txt"])
		}
		if len(state.Value().Pending) != 0 {
			t.Fatalf("pending = %v", state.Value().Pending)
		}
	})

	t.Run("the owner's Discord view: updateLink needs a linked session; pages of version 2 still bind", func(t *testing.T) {
		view, err := p2test.HoldIn(f.Owner, discordcontract.Discord, delta.NewJsonObject(0), "owner")
		if err != nil {
			t.Fatal(err)
		}
		_, err = view.Call("updateLink", delta.JsonObjectOf("session", r.session, "wake", delta.JsonObjectOf("anyone", true, "users", []any{}, "roles", []any{}), "lock", noLock()), p2test.Background)
		if err == nil || !strings.Contains(err.Error(), "not linked") {
			t.Fatalf("updateLink = %v", err)
		}
		old, err := p2test.HoldIn(f.Owner, discordcontract.DiscordV2, delta.NewJsonObject(0), "owner")
		if err != nil {
			t.Fatal(err)
		}
		bindings, err := p2test.Until(old.Member("bindings"), func(value delta.JsonValue) bool { _, ok := value.([]any); return ok }, 30*time.Second)
		if err != nil || len(list(bindings)) != 0 {
			t.Fatalf("bindings = %v %v", bindings, err)
		}
	})

	t.Run("tells a linked session's agents where their answer goes; unlinking takes it back", func(t *testing.T) {
		r.ended(r.must("deliver", r.input("discord:1000", "hello", 0, "")).Value("submissionId"))
		said := sectionsOf(r.view(1, func(view delta.JsonValue) bool { return sectionsOf(view)["discord"] != "" }))["discord"]
		for _, want := range []string{
			`^<discord>\nYour input comes from the Discord channel #general \(Guild\)`,
			`your final answer of each turn is posted there as the reply`,
			`files you offer with send_file during the turn attached`,
			`Nothing else is posted \(not intermediate text, thinking or tool output\)`,
			"`Ann \\(<@1234>\\): text`; write `<@1234>` to mention them",
		} {
			if !regexp.MustCompile(want).MatchString(said) {
				t.Fatalf("section lacks %q: %q", want, said)
			}
		}
		thread := r.must("thread", delta.JsonObjectOf("name", "Release", "before", float64(time.Now().UnixMilli())))
		conversation := thread.Value("conversation").(float64)
		r.ended(r.must("deliver", r.input("discord:1001", "hello", conversation, "Release")).Value("submissionId"))
		inThread := sectionsOf(r.view(conversation, func(view delta.JsonValue) bool {
			return strings.Contains(sectionsOf(view)["discord"], `the thread "Release"`)
		}))["discord"]
		if !strings.Contains(inThread, `Your input comes from the thread "Release" of the Discord channel #general`) {
			t.Fatalf("thread section = %q", inThread)
		}
		r.must("unlinked", nil)
		f.send("just me now")
		r.view(1, func(view delta.JsonValue) bool {
			return slices.Contains(textsOf(view), "Echo: just me now") && sectionsOf(view)["discord"] == ""
		})
	})

	t.Run("a batch while a turn runs steers it: both inputs end with the one answer", func(t *testing.T) {
		running := r.must("deliver", r.input("discord:1100", "slow-tool", 0, ""))
		if err := p2test.Poll(func() (bool, error) { return strings.Contains(r.command("status"), "Working: yes"), nil }, 60*time.Second, "the turn to run"); err != nil {
			t.Fatal(err)
		}
		steered := r.must("deliver", r.input("discord:1101", "and this too", 0, ""))
		one := r.ended(running.Value("submissionId"))
		two := r.ended(steered.Value("submissionId"))
		if one.Value("state") != "answered" || two.Value("state") != "answered" {
			t.Fatalf("turns = %v %v", one, two)
		}
		if one.Value("answer") != two.Value("answer") {
			t.Fatalf("one turn, one answer: %v %v", one.Value("answer"), two.Value("answer"))
		}
	})

	t.Run("stop aborts the running turn and withdraws the queued inputs, as the web's Stop", func(t *testing.T) {
		running := r.must("deliver", r.input("discord:700", "slow-tool", 0, ""))
		queued := r.must("deliver", r.input("discord:701", "queued behind", 0, ""))
		if err := p2test.Poll(func() (bool, error) { return strings.Contains(r.command("status"), "Working: yes"), nil }, 60*time.Second, "the turn to run"); err != nil {
			t.Fatal(err)
		}
		if status := r.command("status"); !regexp.MustCompile(`^Model: .+\nWorking: yes\nQueued: 1\nContext: `).MatchString(status) {
			t.Fatalf("status = %q", status)
		}
		if got := r.command("stop"); got != "Stopped; 1 queued message withdrawn." {
			t.Fatalf("stop = %q", got)
		}
		for _, each := range []*delta.JsonObject{running, queued} {
			if turn := r.ended(each.Value("submissionId")); turn.Value("state") != "stopped" {
				t.Fatalf("turn = %v", turn)
			}
		}
		if got := r.command("stop"); got != "Nothing is running." {
			t.Fatalf("second stop = %q", got)
		}
		if !regexp.MustCompile(`Working: no\nQueued: 0`).MatchString(r.command("status")) {
			t.Fatal("status after the stop")
		}
		if got := r.command("compact"); got != "Compacting the context." {
			t.Fatalf("compact = %q", got)
		}
	})

	t.Run("stop also aborts a running compaction (a task of its own), and says so", func(t *testing.T) {
		if err := p2test.Poll(func() (bool, error) { return !strings.Contains(r.command("status"), "Compacting: yes"), nil }, 60*time.Second, "the earlier compaction to end"); err != nil {
			t.Fatal(err)
		}
		for index := range 3 {
			turn := r.must("deliver", r.input(fmt.Sprintf("discord:90%d", index), fmt.Sprintf("part %d: %s", index, strings.Repeat("lorem ipsum dolor ", 700)), 0, ""))
			r.ended(turn.Value("submissionId"))
		}
		if got := r.command("compact"); got != "Compacting the context." {
			t.Fatalf("compact = %q", got)
		}
		if got := r.command("stop"); got != "Stopped compaction." {
			t.Fatalf("stop = %q", got)
		}
		if strings.Contains(r.command("status"), "Compacting") {
			t.Fatal("still compacting")
		}
		if got := r.command("stop"); got != "Nothing is running." {
			t.Fatalf("second stop = %q", got)
		}
	})

	t.Run("status reports the cost so far from the cost sheet's data, and the last turn's", func(t *testing.T) {
		r.ended(r.must("deliver", r.input("discord:950", "what does it cost", 0, "")).Value("submissionId"))
		sheet, err := p2test.HoldIn(f.Owner, chatcontract.ChatCosts, delta.JsonObjectOf("session", r.session, "conversation", float64(1)), "viewer")
		if err != nil {
			t.Fatal(err)
		}
		costs, err := sheet.Call("costs", nil, p2test.Background)
		if err != nil || len(list(costs)) == 0 {
			t.Fatalf("costs = %v %v", costs, err)
		}
		total := 0.0
		for _, each := range list(costs) {
			total += object(each).Value("cost").(float64)
		}
		places := 6
		if total >= 0.01 || total == 0 {
			places = 4
		}
		if text := r.command("status"); !strings.Contains(text, fmt.Sprintf("\nCost: $%.*f so far, last turn $", places, total)) {
			t.Fatalf("status = %q (total %v)", text, total)
		}
	})

	t.Run("forks a thread's subagent from the main conversation's last answer before the thread", func(t *testing.T) {
		thread := r.must("thread", delta.JsonObjectOf("name", "Release Plan!", "before", float64(time.Now().UnixMilli())))
		conversation := thread.Value("conversation").(float64)
		if conversation == 1 {
			t.Fatal("the thread works in the main conversation")
		}
		children, err := p2test.HoldIn(f.Owner, subagentscontract.Subagents, delta.NewJsonObject(0), "reader")
		if err != nil {
			t.Fatal(err)
		}
		named := func(name string) func(delta.JsonValue) bool {
			return func(value delta.JsonValue) bool {
				return slices.ContainsFunc(list(object(value).Value(r.session)), func(child any) bool { return object(child).Value("name") == name })
			}
		}
		if _, err := p2test.Until(children.Member("children"), named("discord-release-plan"), 60*time.Second); err != nil {
			t.Fatal(err)
		}
		forked := r.view(conversation, func(view delta.JsonValue) bool {
			return slices.ContainsFunc(textsOf(view), func(text string) bool { return strings.HasPrefix(text, "Echo:") })
		})
		if len(textsOf(forked)) < 1 {
			t.Fatal("the fork lacks the main conversation's answer")
		}
		delivered := r.must("deliver", r.input("discord:600", "Ada (<@u1>): @Pi in the thread", conversation, "Release Plan!"))
		if delivered.Value("conversation") != conversation {
			t.Fatalf("conversation = %v", delivered.Value("conversation"))
		}
		turn := r.ended(delivered.Value("submissionId"))
		if text, _ := turn.Value("text").(string); turn.Value("state") != "answered" || !strings.Contains(text, "in the thread") {
			t.Fatalf("turn = %v", turn)
		}
		again := r.must("thread", delta.JsonObjectOf("name", "Release Plan!", "before", float64(time.Now().UnixMilli())))
		if _, err := p2test.Until(children.Member("children"), named("discord-release-plan-2"), 60*time.Second); err != nil {
			t.Fatal(err)
		}
		if again.Value("conversation") == conversation {
			t.Fatal("the second thread shares the first's subagent")
		}
	})
}

// batchEntry is the first Discord batch entry of a conversation's view.
func batchEntry(view delta.JsonValue) *delta.JsonObject {
	for _, each := range list(object(view).Value("entries")) {
		if object(each).Value("kind") == discordcontract.DiscordBatch {
			return object(each)
		}
	}
	return nil
}

func ptrOf[T any](v T) *T { return &v }

var _ = subagentscontract.Subagents
