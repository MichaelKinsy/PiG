package contract

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strconv"
	"strings"
	"sync"
	"time"
	"unicode"
	"unicode/utf16"

	p2runtime "github.com/MichaelKinsy/PiG/apps/p2/runtime"
)

// Ports apps/p2/plugins/discord/relay.ts: what happens between a Discord channel and its linked session (server). A mention
// by someone allowed to wake Pi, seen by the link's bot, delivers the messages since the link's cursor (a thread's: to its
// forked subagent) as one input; a pending reply is posted once its turn ended. Plain logic over ports, so tests run it
// with fake bots and sessions.

// Message is a Discord message as the relay reads it.
type Message struct {
	ID          string
	ChannelID   string
	Author      Author
	Roles       []string
	Text        string
	MentionsBot bool
	Attachments []Attachment
}

// Author is who wrote a message.
type Author struct {
	ID   string
	Name string
	Bot  bool
}

// Attachment is a file of a message.
type Attachment struct {
	Name string
	URL  string
	Size float64
}

// ThreadInfo marks a channel as a thread: its parent channel, when it was created, and a snowflake of its creation (its ID):
// the parent's messages up to it came before the thread.
type ThreadInfo struct {
	ParentID  string
	CreatedAt float64
	CreatedID string
}

// Channel is a channel or a thread of one.
type Channel struct {
	ID            string
	Name          string
	GuildID       string
	GuildName     string
	Thread        *ThreadInfo
	LastMessageID *string
}

// File is a file to attach to a reply.
type File struct {
	Name string
	Data []byte
}

// Port is what the relay needs of Discord.
type Port interface {
	BotID() string
	Channel(ctx context.Context, id string) (Channel, error)
	// Messages is the first `limit` messages after `after` (oldest first; "0": from the channel's start).
	Messages(ctx context.Context, channelID, after string, limit int) ([]Message, error)
	// Reply posts text (at most 2000 characters; may be empty with files) with files attached (at most 10, together within
	// the upload limit), as a reply to messageID when it is not empty.
	Reply(ctx context.Context, channelID, messageID, text string, files []File) error
	// UploadLimit is the bytes one message's attachments may have in channelID (its server's limit).
	UploadLimit(ctx context.Context, channelID string) (float64, error)
	Typing(ctx context.Context, channelID string) error
}

// OfferedFile is a file the agent offered in a turn.
type OfferedFile struct {
	ID   string
	Name string
	Size float64
}

// TurnState is a submission's turn: State is "pending", "answered", "stopped" or "unanswered".
type TurnState struct {
	State  string
	Text   string
	Reason string
	Files  []OfferedFile
}

// DownloadFile is a Discord attachment to download into the session's uploads.
type DownloadFile struct {
	URL  string
	Name string
	Size float64
	Key  string
	Ref  string
}

// Delivery is a batch handed to a session.
type Delivery struct {
	Conversation *int
	RequestID    string
	Author       NamedID
	Text         string
	Context      DeliveryContext
	Files        []DownloadFile
	Lock         *p2runtime.SessionLock
}

// NamedID is an ID with its display name.
type NamedID struct{ ID, Name string }

// DeliveryContext says where a batch comes from.
type DeliveryContext struct {
	Channel  string
	Guild    string
	Messages int
	Thread   *string
}

// Delivered is where a batch went.
type Delivered struct {
	Conversation int
	SubmissionID float64
}

// Sessions is what the relay needs of a session (its worker's `DiscordIngress`, the catalog).
type Sessions interface {
	// Usable is why the session cannot take input (it is gone, archived or not in a sandbox); empty when it can.
	Usable(ctx context.Context, session string) (string, error)
	Lock(session string) *p2runtime.SessionLock
	Deliver(ctx context.Context, session string, input Delivery) (Delivered, error)
	Turn(ctx context.Context, session string, submissionID float64) (TurnState, error)
	Thread(ctx context.Context, session, name string, before float64) (int, error)
	// Read is `length` bytes (at most 4 MiB) of offered file id from offset.
	Read(ctx context.Context, session, id string, offset, length int) ([]byte, error)
	// Command runs channel command on conversation (nil: the main one): the reply's text.
	Command(ctx context.Context, session string, conversation *int, command string) (string, error)
}

// Wake is who may wake Pi in a channel.
type Wake struct {
	Anyone bool     `json:"anyone"`
	Users  []string `json:"users"`
	Roles  []string `json:"roles"`
}

// ThreadBinding is a Discord thread of a linked channel: its subagent conversation and its own cursor.
type ThreadBinding struct {
	Conversation int     `json:"conversation"`
	Name         string  `json:"name"`
	Cursor       *string `json:"cursor"`
}

// Binding is one channel linked to one session, through one bot.
type Binding struct {
	BotID       string                    `json:"botId"`
	ChannelID   string                    `json:"channelId"`
	ChannelName string                    `json:"channelName"`
	GuildID     string                    `json:"guildId"`
	GuildName   string                    `json:"guildName"`
	Session     string                    `json:"session"`
	Wake        Wake                      `json:"wake"`
	Cursor      *string                   `json:"cursor"`
	Threads     map[string]*ThreadBinding `json:"threads"`
	CreatedAt   float64                   `json:"createdAt"`
}

// Pending is a delivered batch waiting for its turn's answer, to go back as a reply to the message that woke Pi.
type Pending struct {
	BotID          string  `json:"botId"`
	Session        string  `json:"session"`
	ChannelID      string  `json:"channelId"`
	ReplyChannelID string  `json:"replyChannelId"`
	MessageID      string  `json:"messageId"`
	SubmissionID   float64 `json:"submissionId"`
	Since          float64 `json:"since"`
}

// State is the relay's persisted state: bindings by channel, pending replies by the Discord message that woke Pi.
type State struct {
	Bindings map[string]*Binding `json:"bindings"`
	Pending  map[string]*Pending `json:"pending"`
}

// StatePort is the relay's state.
type StatePort interface {
	Value() State
	Change(ctx context.Context, update func(draft *State)) error
}

const (
	// Page is the messages one history request asks for (Discord's maximum).
	Page = 100
	// BatchCap is the messages one batch puts into the input at most: the newest; the header counts the earlier ones.
	BatchCap = 200
	// MessageLimit is Discord's limit for one message's text.
	MessageLimit = 2000
	// MaxAttachment: attachments up to this size are downloaded into the session; larger ones are listed as links.
	MaxAttachment = 25 * 1024 * 1024
	maxFiles      = 20
	// PendingTimeout: a pending reply older than this is dropped with a notice (ms).
	PendingTimeout = 6 * 60 * 60 * 1000
	readChunk      = 4 * 1024 * 1024
	// MaxAttachmentsPerMessage is the attachments one Discord message carries at most.
	MaxAttachmentsPerMessage = 10
)

func now() float64 { return float64(time.Now().UnixMilli()) }

// snowflake reads a Discord ID as the number it is (Pi's BigInt).
func snowflake(id string) uint64 {
	n, _ := strconv.ParseUint(id, 10, 64)
	return n
}

// Relay is the relay between Discord and sessions.
type Relay struct {
	discord  func(bot string) Port
	sessions Sessions
	state    StatePort
	mu       sync.Mutex
	// checking: pending replies being checked (two checks at once would post one reply twice).
	checking map[string]bool
	// lanes: each channel's and thread's delivery under way, the next one chained after it: two mentions in quick
	// succession would otherwise both read from the same cursor and deliver overlapping batches.
	lanes map[string]chan struct{}
	// delivering: channels and threads with deliveries under way (how many), and their bots.
	delivering map[string]*activeDelivery
}

type activeDelivery struct {
	bot   string
	count int
}

// NewRelay is the relay; discord gives bot `bot` while it is connected (nil otherwise).
func NewRelay(discord func(bot string) Port, sessions Sessions, state StatePort) *Relay {
	return &Relay{
		discord: discord, sessions: sessions, state: state,
		checking: map[string]bool{}, lanes: map[string]chan struct{}{}, delivering: map[string]*activeDelivery{},
	}
}

var errNotConnected = errors.New("The Discord bot is not connected")

func (r *Relay) port(bot string) (Port, error) {
	port := r.discord(bot)
	if port == nil {
		return nil, errNotConnected
	}
	return port, nil
}

func errText(err error) string { return err.Error() }

// MayWake is whether the author of a message may wake Pi in its binding.
func MayWake(wake Wake, author string, roles []string) bool {
	if wake.Anyone || contains(wake.Users, author) {
		return true
	}
	for _, role := range roles {
		if contains(wake.Roles, role) {
			return true
		}
	}
	return false
}

func contains(list []string, value string) bool {
	for _, each := range list {
		if each == value {
			return true
		}
	}
	return false
}

// OnMessage handles a message bot `bot` received: a mention of it by someone allowed to wake Pi, in a channel linked
// through it (or a thread of one), delivers.
func (r *Relay) OnMessage(ctx context.Context, bot string, message Message) error {
	if message.Author.Bot || !message.MentionsBot {
		return nil
	}
	port, err := r.port(bot)
	if err != nil {
		return err
	}
	bindings := r.state.Value().Bindings
	binding := bindings[message.ChannelID]
	var thread *Channel
	if binding == nil {
		channel, err := port.Channel(ctx, message.ChannelID)
		if err != nil {
			return err
		}
		if channel.Thread == nil {
			return nil
		}
		binding = bindings[channel.Thread.ParentID]
		if binding == nil {
			return nil
		}
		thread = &channel
	}
	// Another bot in the channel may be mentioned too: only the link's own bot listens.
	if binding.BotID != bot || !MayWake(binding.Wake, message.Author.ID, message.Roles) {
		return nil
	}
	// A command runs now, never behind the delivery under way (a stop must reach the running turn).
	if command := CommandOf(message.Text, port.BotID()); command != "" {
		return r.command(ctx, *binding, thread, message, command)
	}
	return r.Deliver(ctx, binding.ChannelID, message, thread)
}

// command runs channel command of message on the channel's main conversation or the thread's subagent.
func (r *Relay) command(ctx context.Context, binding Binding, thread *Channel, message Message, command string) error {
	port, err := r.port(binding.BotID)
	if err != nil {
		return err
	}
	where := binding.ChannelID
	if thread != nil {
		where = thread.ID
	}
	refusal, err := r.sessions.Usable(ctx, binding.Session)
	if err != nil {
		return err
	}
	if refusal != "" {
		return port.Reply(ctx, where, message.ID, "Pi is not listening here right now: "+refusal, nil)
	}
	var conversation *int
	if thread != nil {
		if known := binding.Threads[thread.ID]; known != nil {
			id := known.Conversation
			conversation = &id
		}
		if conversation == nil {
			text := "This thread has no agent yet: mention the bot to start one."
			if command == "stop" {
				text = "Nothing is running."
			}
			return port.Reply(ctx, where, message.ID, text, nil)
		}
	}
	text, err := r.sessions.Command(ctx, binding.Session, conversation, command)
	if err != nil {
		text = "Could not reach Pi: " + errText(err)
	}
	return port.Reply(ctx, where, message.ID, text, nil)
}

// isCommand is whether message is a channel command of binding (never delivered to the agent, nor caught up on).
func isCommand(binding *Binding, message Message, botID string) bool {
	return message.MentionsBot && MayWake(binding.Wake, message.Author.ID, message.Roles) && CommandOf(message.Text, botID) != ""
}

// Deliver delivers the batch ending at trigger to the binding's session (a thread's subagent for a message in a thread),
// then advances the cursor and records the pending reply. A session that cannot take it gets a notice instead. One delivery
// at a time per channel or thread: a later one starts from the cursor the earlier one left.
func (r *Relay) Deliver(ctx context.Context, channelID string, trigger Message, thread *Channel) error {
	target := channelID
	if thread != nil {
		target = thread.ID
	}
	// "Typing" at once, before the wait for an earlier delivery and the history's pages.
	stopTyping := r.typingWhile(ctx, channelID, target, trigger)
	defer stopTyping()
	done := make(chan struct{})
	r.mu.Lock()
	previous := r.lanes[target]
	r.lanes[target] = done
	r.mu.Unlock()
	defer func() {
		r.mu.Lock()
		if r.lanes[target] == done {
			delete(r.lanes, target)
		}
		r.mu.Unlock()
		close(done)
	}()
	if previous != nil {
		select {
		case <-previous:
		case <-ctx.Done():
			return ctx.Err()
		}
	}
	return r.deliver(ctx, channelID, trigger, thread)
}

// typingWhile shows "typing" in target while a delivery of trigger is under way (none for one delivered already): its end.
func (r *Relay) typingWhile(ctx context.Context, channelID, target string, trigger Message) func() {
	binding := r.state.Value().Bindings[channelID]
	if binding == nil {
		return func() {}
	}
	cursor := binding.Cursor
	if target != channelID {
		cursor = nil
		if known := binding.Threads[target]; known != nil {
			cursor = known.Cursor
		}
	}
	if cursor != nil && *cursor != "" && snowflake(trigger.ID) <= snowflake(*cursor) {
		return func() {}
	}
	r.mu.Lock()
	active := r.delivering[target]
	if active == nil {
		active = &activeDelivery{bot: binding.BotID}
	}
	active.count++
	r.delivering[target] = active
	r.mu.Unlock()
	if port := r.discord(binding.BotID); port != nil {
		_ = port.Typing(ctx, target)
	}
	return func() {
		r.mu.Lock()
		defer r.mu.Unlock()
		active.count--
		if active.count == 0 && r.delivering[target] == active {
			delete(r.delivering, target)
		}
	}
}

func (r *Relay) deliver(ctx context.Context, channelID string, trigger Message, thread *Channel) error {
	binding := r.state.Value().Bindings[channelID]
	if binding == nil {
		return nil
	}
	port, err := r.port(binding.BotID)
	if err != nil {
		return err
	}
	where := channelID
	if thread != nil {
		where = thread.ID
	}
	refusal, err := r.sessions.Usable(ctx, binding.Session)
	if err != nil {
		return err
	}
	if refusal != "" {
		return port.Reply(ctx, where, trigger.ID, "Pi is not listening here right now: "+refusal, nil)
	}
	var conversation *int
	cursor := binding.Cursor
	if thread != nil {
		known := binding.Threads[thread.ID]
		if known == nil {
			createdAt := now()
			if thread.Thread != nil {
				createdAt = thread.Thread.CreatedAt
			}
			created, err := r.sessions.Thread(ctx, binding.Session, thread.Name, createdAt)
			if err != nil {
				return err
			}
			fresh := &ThreadBinding{Conversation: created, Name: thread.Name}
			if err := r.state.Change(ctx, func(draft *State) {
				if current := draft.Bindings[channelID]; current != nil {
					if current.Threads == nil {
						current.Threads = map[string]*ThreadBinding{}
					}
					current.Threads[thread.ID] = fresh
				}
			}); err != nil {
				return err
			}
			known = fresh
		}
		id := known.Conversation
		conversation = &id
		cursor = known.Cursor
	}
	// Delivered already (a catch-up, then the same mention replayed by a resumed gateway).
	if cursor != nil && snowflake(trigger.ID) <= snowflake(*cursor) {
		return nil
	}
	// Everything since the cursor, page by page; only the newest BatchCap go into the input. A thread's first batch starts
	// with what its channel said after the channel's cursor until the thread was made: its subagent is forked from before
	// that and never saw it (the channel's cursor stays: the main agent gets it too).
	window := newBatchWindow(BatchCap)
	before := ""
	if thread != nil && thread.Thread != nil && cursor == nil {
		before = thread.Thread.CreatedID
	}
	if before != "" {
		if err := history(ctx, port, channelID, binding.Cursor, before, func(message Message) {
			if !message.Author.Bot && !isCommand(binding, message, port.BotID()) {
				window.add(partChannel, message)
			}
		}); err != nil {
			return err
		}
	}
	if err := history(ctx, port, where, cursor, trigger.ID, func(message Message) {
		if !message.Author.Bot && !isCommand(binding, message, port.BotID()) {
			window.add(partHere, message)
		}
	}); err != nil {
		return err
	}
	if !window.has(trigger.ID) {
		window.add(partHere, trigger)
	}
	plural := func(count int) string {
		if count == 1 {
			return "1 message"
		}
		return strconv.Itoa(count) + " messages"
	}
	left := ""
	if window.omitted() > 0 {
		left = fmt.Sprintf("; the %d earliest are left out, the newest %d follow", window.omitted(), window.kept())
	}
	channelName := "#" + binding.ChannelName
	var header string
	switch {
	case thread == nil:
		header = fmt.Sprintf("[Discord %s (%s): %s since the last delivery%s]", channelName, binding.GuildName, plural(window.total()), left)
	case before == "":
		header = fmt.Sprintf("[Discord thread \"%s\" in %s (%s): %s since the last delivery%s]", thread.Name, channelName, binding.GuildName, plural(window.total()), left)
	default:
		earlier := ""
		if window.count(partChannel) > 0 {
			earlier = fmt.Sprintf("%s of %s before the thread, then ", plural(window.count(partChannel)), channelName)
		}
		header = fmt.Sprintf("[Discord thread \"%s\" in %s (%s), its first delivery: %s%s of the thread%s]", thread.Name, channelName, binding.GuildName, earlier, plural(window.count(partHere)), left)
	}
	titles := map[part]*string{}
	channelTitle := "[" + channelName + ", before the thread]"
	titles[partChannel] = &channelTitle
	if before != "" && window.count(partChannel) > 0 {
		here := "[The thread]"
		titles[partHere] = &here
	}
	text, files := BatchOf(window.sections(titles), port.BotID(), header)
	input := Delivery{
		Conversation: conversation,
		RequestID:    "discord:" + trigger.ID,
		Author:       NamedID{trigger.Author.ID, trigger.Author.Name},
		Text:         text,
		Context:      DeliveryContext{Channel: binding.ChannelName, Guild: binding.GuildName, Messages: window.total()},
		Files:        files,
		Lock:         r.sessions.Lock(binding.Session),
	}
	if thread != nil {
		name := thread.Name
		input.Context.Thread = &name
	}
	delivered, err := r.sessions.Deliver(ctx, binding.Session, input)
	if err != nil {
		return port.Reply(ctx, where, trigger.ID, "Could not reach Pi: "+errText(err), nil)
	}
	botID, session := binding.BotID, binding.Session
	return r.state.Change(ctx, func(draft *State) {
		current := draft.Bindings[channelID]
		if current == nil {
			return
		}
		id := trigger.ID
		if thread != nil {
			if known := current.Threads[thread.ID]; known != nil {
				known.Cursor = &id
			}
		} else {
			current.Cursor = &id
		}
		if draft.Pending == nil {
			draft.Pending = map[string]*Pending{}
		}
		draft.Pending[trigger.ID] = &Pending{
			BotID: botID, Session: session, ChannelID: channelID, ReplyChannelID: where, MessageID: trigger.ID,
			SubmissionID: delivered.SubmissionID, Since: now(),
		}
	})
}

// CatchUp handles bot `bot` (re)connecting: what it missed. For each of its links and their threads, the newest mention
// since the cursor by someone allowed to wake Pi delivers, as if it had just arrived (one delivery each, the messages
// before it included).
func (r *Relay) CatchUp(ctx context.Context, bot string) error {
	port, err := r.port(bot)
	if err != nil {
		return err
	}
	type place struct {
		id     string
		cursor *string
		thread bool
	}
	for _, binding := range r.state.Value().Bindings {
		if binding.BotID != bot {
			continue
		}
		places := []place{{binding.ChannelID, binding.Cursor, false}}
		for id, known := range binding.Threads {
			places = append(places, place{id, known.Cursor, true})
		}
		for _, each := range places {
			if err := r.catchUpPlace(ctx, port, binding, each.id, each.cursor, each.thread); err != nil {
				// A channel or thread that went away (or a session that cannot take it) skips only itself.
				fmt.Fprintln(os.Stderr, "p2 discord: catching up failed:", errText(err))
			}
		}
	}
	return nil
}

func (r *Relay) catchUpPlace(ctx context.Context, port Port, binding *Binding, id string, cursor *string, isThread bool) error {
	var newest *Message
	// A missed command is stale: it neither runs nor triggers a delivery.
	if err := history(ctx, port, id, cursor, "", func(message Message) {
		if !message.Author.Bot && message.MentionsBot && MayWake(binding.Wake, message.Author.ID, message.Roles) && !isCommand(binding, message, port.BotID()) {
			copy := message
			newest = &copy
		}
	}); err != nil {
		return err
	}
	if newest == nil {
		return nil
	}
	var thread *Channel
	if isThread {
		channel, err := port.Channel(ctx, id)
		if err != nil {
			return err
		}
		thread = &channel
	}
	return r.Deliver(ctx, binding.ChannelID, *newest, thread)
}

// CheckPending posts the replies of the turns that ended (sessions: only those, when not nil); answers split at 2000.
func (r *Relay) CheckPending(ctx context.Context, sessions map[string]bool) error {
	current := now()
	for _, pending := range r.state.Value().Pending {
		r.mu.Lock()
		skip := (sessions != nil && !sessions[pending.Session]) || r.checking[pending.MessageID]
		r.mu.Unlock()
		if skip {
			continue
		}
		// A bot that is not connected (or was removed) keeps its replies until it is, or they time out.
		port := r.discord(pending.BotID)
		if port == nil && current-pending.Since < PendingTimeout {
			continue
		}
		r.mu.Lock()
		if r.checking[pending.MessageID] {
			r.mu.Unlock()
			continue
		}
		r.checking[pending.MessageID] = true
		r.mu.Unlock()
		err := r.check(ctx, port, *pending, current)
		r.mu.Lock()
		delete(r.checking, pending.MessageID)
		r.mu.Unlock()
		if err != nil {
			return err
		}
	}
	return nil
}

func (r *Relay) check(ctx context.Context, port Port, pending Pending, current float64) error {
	turn, err := r.sessions.Turn(ctx, pending.Session, pending.SubmissionID)
	timedOut := TurnState{State: "unanswered", Reason: "the session did not answer in time.", Files: []OfferedFile{}}
	if err != nil {
		fmt.Fprintln(os.Stderr, "p2 discord: reading a turn failed:", errText(err))
		if current-pending.Since < PendingTimeout {
			return nil
		}
		turn = timedOut
	}
	if turn.State == "pending" {
		if current-pending.Since < PendingTimeout {
			return nil
		}
		turn = timedOut
	}
	// A stopped turn posts nothing: the stop command answered already.
	if turn.State != "stopped" {
		var err error
		if port == nil {
			err = errors.New("its bot is not connected")
		} else {
			err = r.reply(ctx, port, pending, turn)
		}
		if err != nil {
			// A channel that went away: the reply is dropped, as Discord has nowhere to show it.
			fmt.Fprintln(os.Stderr, "p2 discord: posting a reply failed:", errText(err))
		}
	}
	return r.state.Change(ctx, func(draft *State) { delete(draft.Pending, pending.MessageID) })
}

// reply posts a finished turn's reply: its text split at 2000 characters, the first part replying to the waking message,
// and the files the turn offered attached (at most 10 a message, together within the server's limit; larger ones named
// only), the first ones on the last text part.
func (r *Relay) reply(ctx context.Context, port Port, pending Pending, turn TurnState) error {
	text := "(No answer: " + turn.Reason + ")"
	if turn.State == "answered" {
		text = turn.Text
	}
	limit := float64(0)
	if len(turn.Files) > 0 {
		var err error
		if limit, err = port.UploadLimit(ctx, pending.ReplyChannelID); err != nil {
			return err
		}
	}
	var tooLarge, fits []OfferedFile
	for _, file := range turn.Files {
		if file.Size > limit {
			tooLarge = append(tooLarge, file)
		} else {
			fits = append(fits, file)
		}
	}
	if len(tooLarge) > 0 {
		names := make([]string, len(tooLarge))
		for i, file := range tooLarge {
			names[i] = fmt.Sprintf("%s (%s)", file.Name, megabytes(file.Size))
		}
		if text != "" {
			text += "\n\n"
		}
		text += "Too large for Discord: " + strings.Join(names, ", ")
	}
	type post struct {
		text  string
		files []OfferedFile
	}
	var posts []post
	for _, chunk := range ChunkText(text, MessageLimit) {
		posts = append(posts, post{text: chunk})
	}
	for index, group := range AttachmentGroups(fits, limit) {
		if index == 0 && len(posts) > 0 {
			posts[len(posts)-1].files = group
		} else {
			posts = append(posts, post{files: group})
		}
	}
	for index, each := range posts {
		var files []File
		for _, file := range each.files {
			data, err := r.readFile(ctx, pending.Session, file)
			if err != nil {
				fmt.Fprintf(os.Stderr, "p2 discord: reading %s failed: %s\n", file.Name, errText(err))
				continue
			}
			files = append(files, File{Name: file.Name, Data: data})
		}
		if each.text == "" && len(files) == 0 {
			continue
		}
		replyTo := ""
		if index == 0 {
			replyTo = pending.MessageID
		}
		if err := port.Reply(ctx, pending.ReplyChannelID, replyTo, each.text, files); err != nil {
			return err
		}
	}
	return nil
}

// readFile is offered file of session, whole.
func (r *Relay) readFile(ctx context.Context, session string, file OfferedFile) ([]byte, error) {
	var whole []byte
	for offset := 0; float64(offset) < file.Size; {
		part, err := r.sessions.Read(ctx, session, file.ID, offset, min(readChunk, int(file.Size)-offset))
		if err != nil {
			return nil, err
		}
		if len(part) == 0 {
			break
		}
		whole = append(whole, part...)
		offset += len(part)
	}
	return whole, nil
}

// Typing refreshes "Pi is typing" (Discord shows it about 10 s; call it every 8 s) where a delivery is under way or a reply
// is pending, queued or not: it ends with the delivery's failure, or when the reply is posted, stopped or failed.
func (r *Relay) Typing(ctx context.Context) {
	channels := map[string]string{}
	r.mu.Lock()
	for target, active := range r.delivering {
		channels[target] = active.bot
	}
	r.mu.Unlock()
	for _, pending := range r.state.Value().Pending {
		channels[pending.ReplyChannelID] = pending.BotID
	}
	for channel, bot := range channels {
		if port := r.discord(bot); port != nil {
			_ = port.Typing(ctx, channel)
		}
	}
}

// history calls each for the messages of channel after cursor (nil: from its start), oldest first, page by page; with
// until, up to and with that message.
func history(ctx context.Context, port Port, channel string, cursor *string, until string, each func(Message)) error {
	after := "0"
	if cursor != nil {
		after = *cursor
	}
	for {
		page, err := port.Messages(ctx, channel, after, Page)
		if err != nil {
			return err
		}
		for _, message := range page {
			if until != "" && snowflake(message.ID) > snowflake(until) {
				return nil
			}
			each(message)
		}
		if len(page) < Page {
			return nil
		}
		after = page[len(page)-1].ID
	}
}

// CommandOf is the channel command text is, if it is one: with the bot's mentions removed, exactly a command (any case, an
// optional leading slash); empty otherwise.
func CommandOf(text, botID string) string {
	rest := strings.ReplaceAll(text, "<@"+botID+">", "")
	rest = strings.ReplaceAll(rest, "<@!"+botID+">", "")
	rest = strings.TrimPrefix(strings.ToLower(trim(rest)), "/")
	for _, command := range Commands {
		if command == rest {
			return command
		}
	}
	return ""
}

// AttachmentGroups splits files (each within limit) in order into groups of at most 10 whose sizes add up to at most limit.
func AttachmentGroups(files []OfferedFile, limit float64) [][]OfferedFile {
	var groups [][]OfferedFile
	bytes := float64(0)
	for _, file := range files {
		if n := len(groups); n > 0 && len(groups[n-1]) < MaxAttachmentsPerMessage && bytes+file.Size <= limit {
			groups[n-1] = append(groups[n-1], file)
			bytes += file.Size
		} else {
			groups = append(groups, []OfferedFile{file})
			bytes = file.Size
		}
	}
	return groups
}

func megabytes(size float64) string { return fmt.Sprintf("%.1f MiB", size/(1024*1024)) }

type part int

const (
	partChannel part = iota
	partHere
)

type windowEntry struct {
	part    part
	message Message
}

// batchWindow is the newest cap messages of a batch in order, by part, and how many there were in all.
type batchWindow struct {
	cap     int
	entries []windowEntry
	counts  [2]int
}

func newBatchWindow(cap int) *batchWindow { return &batchWindow{cap: cap} }

func (w *batchWindow) add(p part, message Message) {
	w.counts[p]++
	w.entries = append(w.entries, windowEntry{p, message})
	if len(w.entries) > w.cap {
		w.entries = w.entries[1:]
	}
}

func (w *batchWindow) has(id string) bool {
	for _, entry := range w.entries {
		if entry.message.ID == id {
			return true
		}
	}
	return false
}

func (w *batchWindow) count(p part) int { return w.counts[p] }
func (w *batchWindow) total() int       { return w.counts[0] + w.counts[1] }
func (w *batchWindow) kept() int        { return len(w.entries) }
func (w *batchWindow) omitted() int     { return w.total() - w.kept() }

// Section is a run of messages of one part, with its title if any.
type Section struct {
	Title    *string
	Messages []Message
}

// sections are the kept messages in runs of one part, each titled as titles says (none: untitled).
func (w *batchWindow) sections(titles map[part]*string) []Section {
	var runs []struct {
		part     part
		messages []Message
	}
	for _, entry := range w.entries {
		if n := len(runs); n > 0 && runs[n-1].part == entry.part {
			runs[n-1].messages = append(runs[n-1].messages, entry.message)
		} else {
			runs = append(runs, struct {
				part     part
				messages []Message
			}{entry.part, []Message{entry.message}})
		}
	}
	out := make([]Section, len(runs))
	for i, run := range runs {
		out[i] = Section{Title: titles[run.part], Messages: run.messages}
	}
	return out
}

// BatchOf is a batch's text: under the header, each section's title (if any), then one `Name: text` line (or paragraph) per
// message, oldest first, the bot's mention written `@Pi`, and attachments too large to download listed as links. The
// attachments to download, keyed by message and position.
func BatchOf(sections []Section, botID, header string) (string, []DownloadFile) {
	files := []DownloadFile{}
	line := func(message Message) string {
		text := trim(strings.ReplaceAll(strings.ReplaceAll(message.Text, "<@"+botID+">", "@Pi"), "<@!"+botID+">", "@Pi"))
		parts := []string{}
		if text != "" {
			parts = append(parts, text)
		}
		for index, attachment := range message.Attachments {
			if attachment.Size <= MaxAttachment && len(files) < maxFiles {
				files = append(files, DownloadFile{
					URL: attachment.URL, Name: attachment.Name, Size: attachment.Size,
					Key: "discord:" + message.ID + ":" + strconv.Itoa(index), Ref: message.ID,
				})
			} else {
				parts = append(parts, fmt.Sprintf("[attachment %s (%s bytes): %s]", attachment.Name, jsNumber(attachment.Size), attachment.URL))
			}
		}
		return message.Author.Name + ": " + strings.Join(parts, "\n")
	}
	lines := []string{header}
	for _, section := range sections {
		if section.Title != nil {
			lines = append(lines, *section.Title)
		}
		for _, message := range section.Messages {
			lines = append(lines, line(message))
		}
	}
	return strings.Join(lines, "\n"), files
}

func jsNumber(n float64) string { return strconv.FormatFloat(n, 'f', -1, 64) }

func isSpace(r rune) bool { return unicode.IsSpace(r) || r == 0xFEFF }

func trim(text string) string { return strings.TrimFunc(text, isSpace) }

// ChunkText is text in pieces of at most limit UTF-16 units, split at a line break where possible, else at a space.
func ChunkText(text string, limit int) []string {
	var chunks []string
	rest := utf16.Encode([]rune(trim(text)))
	lastIndex := func(unit uint16, from int) int {
		for i := min(from, len(rest)-1); i >= 0; i-- {
			if rest[i] == unit {
				return i
			}
		}
		return -1
	}
	for len(rest) > limit {
		cut := lastIndex('\n', limit)
		if float64(cut) < float64(limit)/2 {
			cut = lastIndex(' ', limit)
		}
		if float64(cut) < float64(limit)/2 {
			cut = limit
		}
		chunks = append(chunks, strings.TrimRightFunc(string(utf16.Decode(rest[:cut])), isSpace))
		rest = utf16.Encode([]rune(strings.TrimLeftFunc(string(utf16.Decode(rest[cut:])), isSpace)))
	}
	if len(rest) > 0 {
		chunks = append(chunks, string(utf16.Decode(rest)))
	}
	return chunks
}
