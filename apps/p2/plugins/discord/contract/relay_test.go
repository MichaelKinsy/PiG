package contract

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"reflect"
	"slices"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	p2runtime "github.com/MichaelKinsy/PiG/apps/p2/runtime"
)

// Ports the relay part of test/p2/discord.test.ts (Pi main c23b9ec1): the relay between channels and sessions with a fake
// Discord and a fake session.

const botID = "999"

var serial = 100

func message(text string, patch ...func(*Message)) Message {
	serial++
	m := Message{ID: strconv.Itoa(serial), ChannelID: "c1", Author: Author{ID: "u1", Name: "Ada"}, Text: text, MentionsBot: strings.Contains(text, "<@"+botID+">")}
	for _, each := range patch {
		each(&m)
	}
	return m
}

func by(id, name string) func(*Message) { return func(m *Message) { m.Author = Author{ID: id, Name: name} } }
func roles(r ...string) func(*Message)   { return func(m *Message) { m.Roles = r } }

type post struct {
	Channel, ReplyTo, Text string
	Files                  []string
}

type fakeDiscord struct {
	mu       sync.Mutex
	channels map[string]*Channel
	history  map[string][]Message
	posted   []post
	uploaded map[string][]byte
	limit    float64
	typed    []string
	requests []string
	events   []string
}

func newFakeDiscord() *fakeDiscord {
	return &fakeDiscord{channels: map[string]*Channel{}, history: map[string][]Message{}, uploaded: map[string][]byte{}, limit: 10 * 1024 * 1024}
}

func (d *fakeDiscord) add(channel Channel) {
	d.channels[channel.ID] = &channel
	d.history[channel.ID] = nil
}

func (d *fakeDiscord) say(channel string, entry Message) Message {
	d.mu.Lock()
	defer d.mu.Unlock()
	entry.ChannelID = channel
	d.history[channel] = append(d.history[channel], entry)
	return entry
}

func (d *fakeDiscord) BotID() string { return botID }
func (d *fakeDiscord) Channel(_ context.Context, id string) (Channel, error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	found, ok := d.channels[id]
	if !ok {
		return Channel{}, fmt.Errorf("No channel %s", id)
	}
	return *found, nil
}
func (d *fakeDiscord) Messages(_ context.Context, channel, after string, limit int) ([]Message, error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.requests = append(d.requests, after)
	d.events = append(d.events, "messages:"+channel)
	var out []Message
	for _, entry := range d.history[channel] {
		if snowflake(entry.ID) > snowflake(after) && len(out) < min(limit, 100) {
			out = append(out, entry)
		}
	}
	return out, nil
}
func (d *fakeDiscord) Reply(_ context.Context, channel, replyTo, text string, files []File) error {
	d.mu.Lock()
	defer d.mu.Unlock()
	if len([]rune(text)) > 2000 || (text == "" && len(files) == 0) || len(files) > 10 {
		panic("a message Discord would refuse")
	}
	total := 0
	var names []string
	for _, file := range files {
		d.uploaded[file.Name] = file.Data
		total += len(file.Data)
		names = append(names, file.Name)
	}
	if float64(total) > d.limit {
		panic("over the upload limit")
	}
	d.posted = append(d.posted, post{channel, replyTo, text, names})
	return nil
}
func (d *fakeDiscord) UploadLimit(context.Context, string) (float64, error) { return d.limit, nil }
func (d *fakeDiscord) Typing(_ context.Context, channel string) error {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.typed = append(d.typed, channel)
	d.events = append(d.events, "typing:"+channel)
	return nil
}
func (d *fakeDiscord) typedCount() int { d.mu.Lock(); defer d.mu.Unlock(); return len(d.typed) }
func (d *fakeDiscord) posts() []post   { d.mu.Lock(); defer d.mu.Unlock(); return slices.Clone(d.posted) }

type command struct {
	Conversation *int
	Command      string
}

type fakeSessions struct {
	mu        sync.Mutex
	refusal   string
	delivered []Delivery
	turns     map[int]TurnState
	threads   []string
	before    []float64
	bytes     map[string][]byte
	slow      time.Duration
	running   map[int]bool
	commands  []command
	failing   error
}

func newFakeSessions() *fakeSessions {
	return &fakeSessions{turns: map[int]TurnState{}, bytes: map[string][]byte{}, running: map[int]bool{}}
}

func (s *fakeSessions) Read(_ context.Context, _, id string, offset, length int) ([]byte, error) {
	data := s.bytes[id]
	return data[min(offset, len(data)):min(offset+length, len(data))], nil
}
func (s *fakeSessions) Usable(context.Context, string) (string, error) { return s.refusal, nil }
func (s *fakeSessions) Lock(string) *p2runtime.SessionLock {
	return &p2runtime.SessionLock{MCP: []string{}, Capabilities: []p2runtime.LockCapability{}}
}
func (s *fakeSessions) Deliver(_ context.Context, _ string, input Delivery) (Delivered, error) {
	time.Sleep(s.slow)
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.failing != nil {
		return Delivered{}, s.failing
	}
	s.delivered = append(s.delivered, input)
	conversation := 1
	if input.Conversation != nil {
		conversation = *input.Conversation
	}
	s.running[conversation] = true
	id := len(s.delivered)
	s.turns[id] = TurnState{State: "pending"}
	return Delivered{Conversation: conversation, SubmissionID: float64(id)}, nil
}
func (s *fakeSessions) Turn(_ context.Context, _ string, id float64) (TurnState, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if turn, ok := s.turns[int(id)]; ok {
		return turn, nil
	}
	return TurnState{State: "unanswered", Reason: "gone"}, nil
}
func (s *fakeSessions) Command(_ context.Context, _ string, conversation *int, name string) (string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.commands = append(s.commands, command{conversation, name})
	target := 1
	if conversation != nil {
		target = *conversation
	}
	if name != "stop" {
		return fmt.Sprintf("%s of %d", name, target), nil
	}
	if !s.running[target] {
		return "Nothing is running.", nil
	}
	delete(s.running, target)
	for index, delivered := range s.delivered {
		current := 1
		if delivered.Conversation != nil {
			current = *delivered.Conversation
		}
		if current == target && s.turns[index+1].State == "pending" {
			s.turns[index+1] = TurnState{State: "stopped"}
		}
	}
	return "Stopped.", nil
}
func (s *fakeSessions) Thread(_ context.Context, _ string, name string, before float64) (int, error) {
	time.Sleep(s.slow)
	s.mu.Lock()
	defer s.mu.Unlock()
	s.threads = append(s.threads, name)
	s.before = append(s.before, before)
	return 40 + len(s.threads), nil
}
func (s *fakeSessions) setTurn(id int, turn TurnState) { s.mu.Lock(); s.turns[id] = turn; s.mu.Unlock() }
func (s *fakeSessions) delivery(i int) Delivery        { s.mu.Lock(); defer s.mu.Unlock(); return s.delivered[i] }
func (s *fakeSessions) count() int                     { s.mu.Lock(); defer s.mu.Unlock(); return len(s.delivered) }

type fakeState struct {
	mu    sync.Mutex
	state State
}

func (f *fakeState) Value() State { f.mu.Lock(); defer f.mu.Unlock(); return f.state }
func (f *fakeState) Change(_ context.Context, update func(*State)) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	update(&f.state)
	return nil
}

func sp(s string) *string { return &s }

func binding(patch ...func(*Binding)) *Binding {
	b := &Binding{
		BotID: "bot-a", ChannelID: "c1", ChannelName: "general", GuildID: "g1", GuildName: "Guild", Session: "s1",
		Wake: Wake{Users: []string{"u1"}, Roles: []string{"r1"}}, Threads: map[string]*ThreadBinding{}, CreatedAt: 1,
	}
	for _, each := range patch {
		each(b)
	}
	return b
}

type rig struct {
	discord  *fakeDiscord
	sessions *fakeSessions
	relay    *Relay
	offline  map[string]bool
	state    *fakeState
	mu       sync.Mutex
}

func setup(t *testing.T, initial *Binding) *rig {
	t.Helper()
	if initial == nil {
		initial = binding()
	}
	r := &rig{discord: newFakeDiscord(), sessions: newFakeSessions(), offline: map[string]bool{}}
	r.discord.add(Channel{ID: "c1", Name: "general", GuildID: "g1", GuildName: "Guild"})
	r.discord.add(Channel{ID: "t1", Name: "Release plan", GuildID: "g1", GuildName: "Guild", Thread: &ThreadInfo{ParentID: "c1", CreatedAt: 5000, CreatedID: "0"}})
	r.state = &fakeState{state: State{Bindings: map[string]*Binding{initial.ChannelID: initial}, Pending: map[string]*Pending{}}}
	r.relay = NewRelay(func(bot string) Port {
		r.mu.Lock()
		defer r.mu.Unlock()
		if r.offline[bot] {
			return nil
		}
		return r.discord
	}, r.sessions, r.state)
	return r
}

func (r *rig) setOffline(bot string, off bool) { r.mu.Lock(); r.offline[bot] = off; r.mu.Unlock() }

var ctx = context.Background()

func (r *rig) on(bot string, m Message) {
	if err := r.relay.OnMessage(ctx, bot, m); err != nil {
		panic(err)
	}
}

func (r *rig) check(sessions ...string) {
	var only map[string]bool
	if len(sessions) > 0 {
		only = map[string]bool{}
		for _, s := range sessions {
			only[s] = true
		}
	}
	if err := r.relay.CheckPending(ctx, only); err != nil {
		panic(err)
	}
}

func lines(d Delivery) []string { return strings.Split(d.Text, "\n") }

func eq(t *testing.T, what string, got, want any) {
	t.Helper()
	if !reflect.DeepEqual(got, want) {
		t.Errorf("%s = %#v, want %#v", what, got, want)
	}
}

func ptrTo(s *string) string {
	if s == nil {
		return "<nil>"
	}
	return *s
}

func TestDeliversTheMessagesSinceTheCursorWhenSomeoneAllowedMentionsPi(t *testing.T) {
	r := setup(t, nil)
	r.discord.say("c1", message("before the bot"))
	r.on("bot-a", r.discord.say("c1", message("<@"+botID+"> hi", by("u2", "Eve"))))
	eq(t, "delivered by a stranger", r.sessions.count(), 0)
	r.on("bot-a", r.discord.say("c1", message("<@"+botID+"> I am a bot", func(m *Message) { m.Author = Author{"u1", "Bot", true} })))
	eq(t, "delivered by a bot", r.sessions.count(), 0)
	byRole := r.discord.say("c1", message("<@"+botID+"> what do you think?", by("u3", "Bo"), roles("r1")))
	r.on("bot-a", byRole)
	eq(t, "delivered", r.sessions.count(), 1)
	input := r.sessions.delivery(0)
	eq(t, "requestId", input.RequestID, "discord:"+byRole.ID)
	eq(t, "author", input.Author, NamedID{"u3", "Bo"})
	eq(t, "text", lines(input), []string{
		"[Discord #general (Guild): 3 messages since the last delivery]", "Ada: before the bot", "Eve: @Pi hi", "Bo: @Pi what do you think?",
	})
	eq(t, "cursor", ptrTo(r.state.Value().Bindings["c1"].Cursor), byRole.ID)
	eq(t, "submission", r.state.Value().Pending[byRole.ID].SubmissionID, float64(1))
	eq(t, "typed", r.discord.typed, []string{"c1"})
	next := r.discord.say("c1", message("<@"+botID+"> and now?"))
	r.on("bot-a", next)
	eq(t, "next batch", lines(r.sessions.delivery(1))[1:], []string{"Ada: @Pi and now?"})
}

func TestPostsTheAnswerOnceItsTurnEndedAsAReplySplitAt2000Characters(t *testing.T) {
	r := setup(t, nil)
	trigger := r.discord.say("c1", message("<@"+botID+"> long answer please"))
	r.on("bot-a", trigger)
	r.check()
	eq(t, "posted while pending", len(r.discord.posts()), 0)
	answer := strings.Repeat("word ", 500) + "\n" + strings.Repeat("more ", 300)
	r.sessions.setTurn(1, TurnState{State: "answered", Text: answer})
	var wait sync.WaitGroup
	wait.Add(2)
	go func() { defer wait.Done(); r.check() }()
	go func() { defer wait.Done(); r.check("s1") }()
	wait.Wait()
	posts := r.discord.posts()
	if len(posts) < 2 {
		t.Fatalf("not split: %d", len(posts))
	}
	eq(t, "first replies to", posts[0].ReplyTo, trigger.ID)
	var joined []string
	for index, each := range posts {
		if index > 0 && each.ReplyTo != "" {
			t.Errorf("part %d replies", index)
		}
		joined = append(joined, each.Text)
	}
	eq(t, "text", strings.Join(strings.Fields(strings.Join(joined, " ")), " "), strings.Join(strings.Fields(answer), " "))
	eq(t, "pending", len(r.state.Value().Pending), 0)
	count := len(r.discord.posts())
	r.check()
	eq(t, "posted again", len(r.discord.posts()), count)
}

func TestSaysWhyWhenATurnEndsWithoutAnAnswerOrNeverEnds(t *testing.T) {
	r := setup(t, nil)
	r.on("bot-a", r.discord.say("c1", message("<@"+botID+"> one")))
	r.sessions.setTurn(1, TurnState{State: "unanswered", Reason: "The model failed."})
	r.check()
	posts := r.discord.posts()
	eq(t, "text", posts[len(posts)-1].Text, "(No answer: The model failed.)")
	trigger := r.discord.say("c1", message("<@"+botID+"> two"))
	r.on("bot-a", trigger)
	r.state.Value().Pending[trigger.ID].Since -= PendingTimeout + 1
	r.check()
	posts = r.discord.posts()
	if !strings.Contains(posts[len(posts)-1].Text, "did not answer in time") {
		t.Errorf("text = %q", posts[len(posts)-1].Text)
	}
}

func TestRefusesWithANoticeWhenTheSessionCannotTakeInputTheCursorStays(t *testing.T) {
	r := setup(t, nil)
	r.sessions.refusal = "its p2 session does not work in a sandbox."
	r.on("bot-a", r.discord.say("c1", message("<@"+botID+"> hello")))
	eq(t, "delivered", r.sessions.count(), 0)
	if !strings.Contains(r.discord.posts()[0].Text, "not listening here right now: its p2 session does not work in a sandbox") {
		t.Errorf("notice = %q", r.discord.posts()[0].Text)
	}
	eq(t, "cursor", r.state.Value().Bindings["c1"].Cursor, (*string)(nil))
}

func TestGivesAThreadItsOwnSubagentAtTheFirstMentionWithItsOwnCursor(t *testing.T) {
	r := setup(t, binding(func(b *Binding) { b.Cursor = sp("1") }))
	r.discord.say("t1", message("thread opener"))
	first := r.discord.say("t1", message("<@"+botID+"> plan it"))
	r.on("bot-a", first)
	eq(t, "threads", r.sessions.threads, []string{"Release plan"})
	eq(t, "before", r.sessions.before, []float64{5000})
	input := r.sessions.delivery(0)
	eq(t, "conversation", *input.Conversation, 41)
	eq(t, "thread", *input.Context.Thread, "Release plan")
	eq(t, "text", lines(input), []string{
		`[Discord thread "Release plan" in #general (Guild), its first delivery: 2 messages of the thread]`, "Ada: thread opener", "Ada: @Pi plan it",
	})
	known := r.state.Value().Bindings["c1"].Threads["t1"]
	eq(t, "thread binding", *known, ThreadBinding{Conversation: 41, Name: "Release plan", Cursor: sp(first.ID)})
	eq(t, "channel cursor", ptrTo(r.state.Value().Bindings["c1"].Cursor), "1")
	r.on("bot-a", r.discord.say("t1", message("<@"+botID+"> next step")))
	eq(t, "subagents", len(r.sessions.threads), 1)
	eq(t, "conversation", *r.sessions.delivery(1).Conversation, 41)
	r.sessions.setTurn(1, TurnState{State: "answered", Text: "Plan."})
	r.check()
	eq(t, "reply", r.discord.posts()[0], post{"t1", first.ID, "Plan.", nil})
}

func TestListensOnlyThroughTheLinksOwnBotAReplyWaitsWhileItsBotIsNotConnected(t *testing.T) {
	r := setup(t, nil)
	r.on("bot-b", r.discord.say("c1", message("<@"+botID+"> hello")))
	eq(t, "another bot's mention", r.sessions.count(), 0)
	trigger := r.discord.say("c1", message("<@"+botID+"> hello again"))
	r.on("bot-a", trigger)
	eq(t, "bot", r.state.Value().Pending[trigger.ID].BotID, "bot-a")
	r.sessions.setTurn(1, TurnState{State: "answered", Text: "Hi."})
	r.setOffline("bot-a", true)
	r.check()
	eq(t, "posted", len(r.discord.posts()), 0)
	if r.state.Value().Pending[trigger.ID] == nil {
		t.Fatal("not kept for later")
	}
	r.setOffline("bot-a", false)
	r.check()
	eq(t, "posts", r.discord.posts(), []post{{"c1", trigger.ID, "Hi.", nil}})
}

func TestDeliversOneBatchAtATimePerChannelAndThread(t *testing.T) {
	r := setup(t, binding(func(b *Binding) { b.Cursor = sp("1") }))
	r.sessions.slow = 20 * time.Millisecond
	r.discord.say("c1", message("one"))
	first := r.discord.say("c1", message("<@"+botID+"> first"))
	r.discord.say("c1", message("two"))
	second := r.discord.say("c1", message("<@"+botID+"> second"))
	a := r.discord.say("t1", message("<@"+botID+"> a"))
	b := r.discord.say("t1", message("<@"+botID+"> b"))
	r.sessions.running = map[int]bool{}
	var wait sync.WaitGroup
	// The mentions arrive in order: the lane's order is the order they reached the relay.
	for _, m := range []Message{first, second, a, b} {
		wait.Add(1)
		go func() { defer wait.Done(); r.on("bot-a", m) }()
		time.Sleep(5 * time.Millisecond)
	}
	wait.Wait()
	batches := func(thread bool) [][]string {
		var out [][]string
		for i := range r.sessions.delivered {
			if d := r.sessions.delivery(i); (d.Context.Thread != nil) == thread {
				out = append(out, lines(d)[1:])
			}
		}
		return out
	}
	eq(t, "channel batches", batches(false), [][]string{{"Ada: one", "Ada: @Pi first"}, {"Ada: two", "Ada: @Pi second"}})
	eq(t, "thread batches", batches(true), [][]string{{"Ada: @Pi a"}, {"Ada: @Pi b"}})
	eq(t, "subagents", len(r.sessions.threads), 1)
	state := r.state.Value()
	eq(t, "cursor", ptrTo(state.Bindings["c1"].Cursor), second.ID)
	eq(t, "thread cursor", ptrTo(state.Bindings["c1"].Threads["t1"].Cursor), b.ID)
	var keys []string
	for key := range state.Pending {
		keys = append(keys, key)
	}
	want := []string{first.ID, second.ID, a.ID, b.ID}
	slices.Sort(keys)
	slices.Sort(want)
	eq(t, "pending", keys, want)
}

func TestAThreadsFirstBatchTheChannelsUndeliveredMessagesUpToTheThreadThenTheThreads(t *testing.T) {
	r := setup(t, binding(func(b *Binding) { b.Cursor = sp("1") }))
	r.discord.say("c1", message("before the thread"))
	starter := r.discord.say("c1", message("shall we plan the release?", by("u3", "Bo")))
	r.discord.channels["t1"].Thread.CreatedID = starter.ID
	r.discord.say("c1", message("unrelated, after the thread"))
	r.discord.say("t1", message("in the thread"))
	first := r.discord.say("t1", message("<@"+botID+"> plan it"))
	r.on("bot-a", first)
	eq(t, "first batch", lines(r.sessions.delivery(0)), []string{
		`[Discord thread "Release plan" in #general (Guild), its first delivery: 2 messages of #general before the thread, then 2 messages of the thread]`,
		"[#general, before the thread]", "Ada: before the thread", "Bo: shall we plan the release?", "[The thread]", "Ada: in the thread", "Ada: @Pi plan it",
	})
	eq(t, "messages", r.sessions.delivery(0).Context.Messages, 4)
	eq(t, "channel cursor", ptrTo(r.state.Value().Bindings["c1"].Cursor), "1")
	eq(t, "thread cursor", ptrTo(r.state.Value().Bindings["c1"].Threads["t1"].Cursor), first.ID)
	r.discord.say("t1", message("more detail"))
	r.on("bot-a", r.discord.say("t1", message("<@"+botID+"> and now?")))
	eq(t, "second batch", lines(r.sessions.delivery(1)), []string{
		`[Discord thread "Release plan" in #general (Guild): 2 messages since the last delivery]`, "Ada: more detail", "Ada: @Pi and now?",
	})
	r.on("bot-a", r.discord.say("c1", message("<@"+botID+"> back here")))
	eq(t, "main batch", lines(r.sessions.delivery(2))[1:], []string{
		"Ada: before the thread", "Bo: shall we plan the release?", "Ada: unrelated, after the thread", "Ada: @Pi back here",
	})
}

func TestStopWhileATurnRunsAnsweredAtOnceEvenBehindADelivery(t *testing.T) {
	r := setup(t, binding(func(b *Binding) { b.Cursor = sp("1") }))
	first := r.discord.say("c1", message("<@"+botID+"> long task"))
	r.on("bot-a", first)
	r.sessions.slow = 50 * time.Millisecond
	second := r.discord.say("c1", message("<@"+botID+"> and another"))
	stop := r.discord.say("c1", message("<@"+botID+"> /STOP", by("u3", "Bo"), roles("r1")))
	done := make(chan struct{})
	go func() { r.on("bot-a", second); close(done) }()
	time.Sleep(10 * time.Millisecond)
	r.on("bot-a", stop)
	eq(t, "before the delivery", r.discord.posts(), []post{{"c1", stop.ID, "Stopped.", nil}})
	eq(t, "delivered", r.sessions.count(), 1)
	<-done
	eq(t, "commands", r.sessions.commands, []command{{nil, "stop"}})
	eq(t, "turn", r.sessions.turns[1].State, "stopped")
	r.check("s1")
	var keys []string
	for key := range r.state.Value().Pending {
		keys = append(keys, key)
	}
	eq(t, "pending", keys, []string{second.ID})
	eq(t, "posted", len(r.discord.posts()), 1)
	r.on("bot-a", r.discord.say("c1", message("<@"+botID+"> next")))
	eq(t, "next", lines(r.sessions.delivery(r.sessions.count() - 1))[1:], []string{"Ada: @Pi next"})
	eq(t, "second", lines(r.sessions.delivery(1))[1:], []string{"Ada: @Pi and another"})
}

func TestStopWithNothingRunningSaysSoStatusAndCompactGoToTheMainConversation(t *testing.T) {
	r := setup(t, nil)
	for _, m := range []Message{
		message("<@" + botID + "> stop"), message("<@" + botID + ">   Status "), message("<@" + botID + "> compact"),
		message("<@"+botID+"> stop", by("u9", "Eve")),
	} {
		r.on("bot-a", r.discord.say("c1", m))
	}
	var texts []string
	for _, each := range r.discord.posts() {
		texts = append(texts, each.Text)
	}
	eq(t, "texts", texts, []string{"Nothing is running.", "status of 1", "compact of 1"})
	eq(t, "delivered", r.sessions.count(), 0)
	r.on("bot-a", r.discord.say("c1", message("<@"+botID+"> stop the build please")))
	eq(t, "delivered", r.sessions.count(), 1)
}

func TestACommandInAThreadReachesTheThreadsSubagentAThreadWithoutOneHasNothingToStop(t *testing.T) {
	r := setup(t, binding(func(b *Binding) {
		b.Cursor = sp("1")
		b.Threads["t1"] = &ThreadBinding{Conversation: 41, Name: "Release plan", Cursor: sp("1")}
	}))
	r.discord.add(Channel{ID: "t2", Name: "New", GuildID: "g1", GuildName: "Guild", Thread: &ThreadInfo{ParentID: "c1", CreatedAt: 6000, CreatedID: "0"}})
	r.on("bot-a", r.discord.say("t1", message("<@"+botID+"> do it")))
	stop := r.discord.say("t1", message("<@"+botID+"> stop"))
	r.on("bot-a", stop)
	forty := 41
	eq(t, "commands", r.sessions.commands, []command{{&forty, "stop"}})
	eq(t, "posts", r.discord.posts(), []post{{"t1", stop.ID, "Stopped.", nil}})
	eq(t, "turn", r.sessions.turns[1].State, "stopped")
	elsewhere := r.discord.say("t2", message("<@"+botID+"> stop"))
	r.on("bot-a", elsewhere)
	posts := r.discord.posts()
	eq(t, "last", posts[len(posts)-1], post{"t2", elsewhere.ID, "Nothing is running.", nil})
	eq(t, "forked", len(r.sessions.threads), 0)
}

func TestACommandMissedWhileDownIsNeitherRunNorACatchUpTrigger(t *testing.T) {
	r := setup(t, binding(func(b *Binding) { b.Cursor = sp("1") }))
	r.discord.say("c1", message("<@"+botID+"> stop"))
	if err := r.relay.CatchUp(ctx, "bot-a"); err != nil {
		t.Fatal(err)
	}
	eq(t, "delivered", r.sessions.count(), 0)
	eq(t, "commands", len(r.sessions.commands), 0)
}

func TestAttachesTheFilesATurnOfferedToItsReply(t *testing.T) {
	r := setup(t, nil)
	r.discord.limit = 1024
	file := func(id string, size int) OfferedFile {
		r.sessions.bytes[id] = bytes.Repeat([]byte{byte(len(id))}, size)
		return OfferedFile{ID: id, Name: id + ".bin", Size: float64(size)}
	}
	trigger := r.discord.say("c1", message("<@"+botID+"> build it"))
	r.on("bot-a", trigger)
	var small []OfferedFile
	for index := range 11 {
		small = append(small, file(fmt.Sprintf("f%d", index), 10))
	}
	pair := []OfferedFile{file("half-a", 600), file("half-b", 600)}
	files := append(append(slices.Clone(small), file("huge", 2048)), pair...)
	r.sessions.setTurn(1, TurnState{State: "answered", Text: "Built.", Files: files})
	r.check()
	var firstNames []string
	for _, each := range small[:10] {
		firstNames = append(firstNames, each.Name)
	}
	eq(t, "posts", r.discord.posts(), []post{
		{"c1", trigger.ID, "Built.\n\nToo large for Discord: huge.bin (0.0 MiB)", firstNames},
		{"c1", "", "", []string{"f10.bin", "half-a.bin"}},
		{"c1", "", "", []string{"half-b.bin"}},
	})
	eq(t, "uploaded", r.discord.uploaded["half-a.bin"], r.sessions.bytes["half-a"])
	if _, ok := r.discord.uploaded["huge.bin"]; ok {
		t.Error("the huge file was uploaded")
	}
}

func TestATurnWithFilesAndNoTextInAThreadPostsThemAsTheReplyThere(t *testing.T) {
	r := setup(t, binding(func(b *Binding) {
		b.Cursor = sp("1")
		b.Threads["t1"] = &ThreadBinding{Conversation: 41, Name: "Release plan", Cursor: sp("1")}
	}))
	trigger := r.discord.say("t1", message("<@"+botID+"> send the log"))
	r.on("bot-a", trigger)
	r.sessions.bytes["log"] = []byte("log line\n")
	r.sessions.setTurn(1, TurnState{State: "answered", Files: []OfferedFile{{ID: "log", Name: "build.log", Size: 9}}})
	r.check()
	eq(t, "posts", r.discord.posts(), []post{{"t1", trigger.ID, "", []string{"build.log"}}})
	eq(t, "uploaded", string(r.discord.uploaded["build.log"]), "log line\n")
}

func (d *fakeDiscord) snapshotEvents() []string { d.mu.Lock(); defer d.mu.Unlock(); return slices.Clone(d.events) }

func TestTypingAtOnceBeforeTheHistoryIsReadRefreshedThroughASlowDelivery(t *testing.T) {
	r := setup(t, binding(func(b *Binding) { b.Cursor = sp("1") }))
	r.sessions.slow = 120 * time.Millisecond
	trigger := r.discord.say("c1", message("<@"+botID+"> think hard"))
	done := make(chan struct{})
	go func() { r.on("bot-a", trigger); close(done) }()
	deadline := time.Now().Add(5 * time.Second)
	for len(r.discord.snapshotEvents()) == 0 && time.Now().Before(deadline) {
		time.Sleep(time.Millisecond)
	}
	eq(t, "events at once", r.discord.snapshotEvents()[:1], []string{"typing:c1"})
	time.Sleep(40 * time.Millisecond)
	eq(t, "still delivering", r.sessions.count(), 0)
	r.relay.Typing(ctx)
	eq(t, "events", r.discord.snapshotEvents(), []string{"typing:c1", "messages:c1", "typing:c1"})
	<-done
	r.relay.Typing(ctx)
	eq(t, "typed", r.discord.typedCount(), 3)
	r.sessions.setTurn(1, TurnState{State: "answered", Text: "Done."})
	r.check()
	eq(t, "pending", len(r.state.Value().Pending), 0)
	r.relay.Typing(ctx)
	eq(t, "typed after", r.discord.typedCount(), 3)
	eq(t, "posted", len(r.discord.posts()), 1)
}

func TestTypingEndsWithAStoppedTurnAndWithAFailedDeliveryACommandNeverTypes(t *testing.T) {
	r := setup(t, binding(func(b *Binding) { b.Cursor = sp("1") }))
	r.on("bot-a", r.discord.say("c1", message("<@"+botID+"> long task")))
	r.on("bot-a", r.discord.say("c1", message("<@"+botID+"> stop")))
	r.check()
	typed := r.discord.typedCount()
	r.relay.Typing(ctx)
	eq(t, "stopped turn's typing", r.discord.typedCount(), typed)
	r.sessions.failing = errors.New("worker down")
	r.on("bot-a", r.discord.say("c1", message("<@"+botID+"> again")))
	posts := r.discord.posts()
	if !strings.Contains(posts[len(posts)-1].Text, "Could not reach Pi: worker down") {
		t.Errorf("notice = %q", posts[len(posts)-1].Text)
	}
	after := r.discord.typedCount()
	r.relay.Typing(ctx)
	eq(t, "typing after the failure", r.discord.typedCount(), after)
	eq(t, "two deliveries typed", after, 2)
}

func TestPagesThroughEverythingSinceTheCursorUpToTheMention(t *testing.T) {
	r := setup(t, binding(func(b *Binding) { b.Cursor = sp("1") }))
	var said []Message
	for index := range 199 {
		said = append(said, r.discord.say("c1", message(fmt.Sprintf("line %d", index))))
	}
	trigger := r.discord.say("c1", message("<@"+botID+"> summarize"))
	r.discord.say("c1", message("after the mention"))
	r.on("bot-a", trigger)
	got := lines(r.sessions.delivery(0))
	eq(t, "header", got[0], "[Discord #general (Guild): 200 messages since the last delivery]")
	eq(t, "first", got[1], "Ada: line 0")
	eq(t, "past the first page", got[101], "Ada: "+said[100].Text)
	eq(t, "last", got[len(got)-1], "Ada: @Pi summarize")
	eq(t, "messages", r.sessions.delivery(0).Context.Messages, 200)
	eq(t, "requests", r.discord.requests, []string{"1", said[99].ID, trigger.ID})
	eq(t, "cursor", ptrTo(r.state.Value().Bindings["c1"].Cursor), trigger.ID)
}

func TestPutsOnlyTheNewest200IntoAHugeBatch(t *testing.T) {
	r := setup(t, binding(func(b *Binding) { b.Cursor = sp("1") }))
	for index := range 450 {
		r.discord.say("c1", message(fmt.Sprintf("line %d", index)))
	}
	r.on("bot-a", r.discord.say("c1", message("<@"+botID+"> what happened?")))
	got := lines(r.sessions.delivery(0))
	eq(t, "header", got[0], "[Discord #general (Guild): 451 messages since the last delivery; the 251 earliest are left out, the newest 200 follow]")
	eq(t, "lines", len(got), 201)
	eq(t, "first", got[1], "Ada: line 251")
	eq(t, "last", got[len(got)-1], "Ada: @Pi what happened?")
}

func TestCatchesUpAfterDowntimeOneBatchPerLinkAndThread(t *testing.T) {
	r := setup(t, binding(func(b *Binding) { b.Cursor = sp("1") }))
	delivered := r.discord.say("c1", message("<@"+botID+"> already answered"))
	link := r.state.Value().Bindings["c1"]
	link.Cursor = sp(delivered.ID)
	link.Threads["t1"] = &ThreadBinding{Conversation: 41, Name: "Release plan", Cursor: sp("1")}
	r.discord.say("c1", message("good morning"))
	r.discord.say("c1", message("<@"+botID+"> me too", by("u9", "Eve")))
	r.discord.say("c1", message("<@"+botID+"> first question"))
	r.discord.say("c1", message("some context for it", by("u3", "Bo")))
	newest := r.discord.say("c1", message("<@"+botID+"> second question"))
	r.discord.say("c1", message("chatter after"))
	inThread := r.discord.say("t1", message("<@"+botID+"> plan the release"))
	_ = r.relay.CatchUp(ctx, "bot-b")
	eq(t, "another bot's links", r.sessions.count(), 0)
	_ = r.relay.CatchUp(ctx, "bot-a")
	eq(t, "one delivery per channel and thread", r.sessions.count(), 2)
	channel := r.sessions.delivery(0)
	eq(t, "requestId", channel.RequestID, "discord:"+newest.ID)
	eq(t, "text", lines(channel), []string{
		"[Discord #general (Guild): 5 messages since the last delivery]", "Ada: good morning", "Eve: @Pi me too", "Ada: @Pi first question",
		"Bo: some context for it", "Ada: @Pi second question",
	})
	eq(t, "cursor", ptrTo(r.state.Value().Bindings["c1"].Cursor), newest.ID)
	eq(t, "thread conversation", *r.sessions.delivery(1).Conversation, 41)
	eq(t, "thread requestId", r.sessions.delivery(1).RequestID, "discord:"+inThread.ID)
	eq(t, "thread cursor", ptrTo(r.state.Value().Bindings["c1"].Threads["t1"].Cursor), inThread.ID)
	eq(t, "forked", len(r.sessions.threads), 0)
	_ = r.relay.CatchUp(ctx, "bot-a")
	r.on("bot-a", newest)
	eq(t, "replayed", r.sessions.count(), 2)
	r.sessions.setTurn(1, TurnState{State: "answered", Text: "Second answer."})
	r.sessions.setTurn(2, TurnState{State: "answered", Text: "Plan."})
	r.check()
	posts := r.discord.posts()
	slices.SortFunc(posts, func(a, b post) int { return strings.Compare(a.Channel, b.Channel) })
	eq(t, "posts", posts, []post{{"c1", newest.ID, "Second answer.", nil}, {"t1", inThread.ID, "Plan.", nil}})
}

func TestBatchOfDownloadsAttachmentsUpTo25MiBAndListsLargerOnesAsLinks(t *testing.T) {
	big := float64(26 * 1024 * 1024)
	m := message("see files", func(m *Message) {
		m.ID = "7"
		m.Attachments = []Attachment{{"a.png", "https://cdn/a.png", 10}, {"huge.zip", "https://cdn/huge.zip", big}}
	})
	text, files := BatchOf([]Section{{Messages: []Message{m}}}, botID, "[h]")
	eq(t, "files", files, []DownloadFile{{URL: "https://cdn/a.png", Name: "a.png", Size: 10, Key: "discord:7:0", Ref: "7"}})
	eq(t, "text", text, fmt.Sprintf("[h]\nAda: see files\n[attachment huge.zip (%d bytes): https://cdn/huge.zip]", int(big)))
	var many []Attachment
	for i := range 25 {
		many = append(many, Attachment{fmt.Sprintf("f%d", i), "u", 1})
	}
	_, files = BatchOf([]Section{{Messages: []Message{message("x", func(m *Message) { m.Attachments = many })}}}, botID, "h")
	eq(t, "at most 20", len(files), 20)
}

func TestChunkTextSplitsAtLineBreaksThenSpacesThenAnywhere(t *testing.T) {
	eq(t, "short", ChunkText("short", MessageLimit), []string{"short"})
	eq(t, "lines", ChunkText(strings.Repeat("a", 8)+"\n"+strings.Repeat("b", 8), 10), []string{strings.Repeat("a", 8), strings.Repeat("b", 8)})
	eq(t, "anywhere", ChunkText(strings.Repeat("x", 25), 10), []string{strings.Repeat("x", 10), strings.Repeat("x", 10), strings.Repeat("x", 5)})
}
