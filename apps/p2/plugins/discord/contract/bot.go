package contract

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/bwmarrin/discordgo"
)

// Ports apps/p2/plugins/discord/bot.ts: one Discord bot (server, part of the fleet): a gateway client logged in with one of
// the owner's tokens (discord.js 14.28.1 there, discordgo here), its status (identity and servers, live), and the messages
// it receives handed to its owner; behind `Port`, what the relay uses.

// invitePermissions are the bot's permissions in the invite: view channels, send (also in threads), embed links, attach
// files, history.
const invitePermissions = (1 << 10) | (1 << 11) | (1 << 14) | (1 << 15) | (1 << 16) | (1 << 38)

// retryAfter is how long a bot that could not reach Discord waits before it tries again (a refused token or intent never
// does).
const retryAfter = 60 * time.Second

// readyTimeout is how long a login waits for Discord to get ready.
// The application flags that say the Message Content intent is on (verified or not).
const (
	gatewayMessageContent        = 1 << 18
	gatewayMessageContentLimited = 1 << 19
)

const readyTimeout = 30 * time.Second

// BotEvent is what a bot hands its fleet: a message it received, or that it (re)connected and may have missed some.
type BotEvent struct {
	Message *Message
	Ready   bool
}

// GuildInfo is a Discord server the bot is in.
type GuildInfo struct{ ID, Name string }

// BotStatus is a bot's connection as the owner sees it: State is "connecting", "error" or "connected".
type BotStatus struct {
	State         string
	Error         string
	User          string
	ApplicationID string
	AvatarURL     string
	InviteURL     string
	Guilds        []GuildInfo
}

// ServerDetails is a Discord server's text channels and roles.
type ServerDetails struct {
	Channels []NamedChannel
	Roles    []NamedChannel
}

// NamedChannel is an ID with a name.
type NamedChannel struct{ ID, Name string }

// Bot is one Discord bot.
type Bot struct {
	mu         sync.Mutex
	session    *discordgo.Session
	ready      bool
	status     BotStatus
	listeners  map[int]func()
	nextToken  int
	onEvent    func(BotEvent)
	generation int
	retry      *time.Timer
}

// NewBot is a bot that hands what it receives (never its own messages) and each (re)connection to onEvent.
func NewBot(onEvent func(BotEvent)) *Bot {
	return &Bot{status: BotStatus{State: "connecting"}, listeners: map[int]func(){}, onEvent: onEvent}
}

// Status is the bot's connection.
func (b *Bot) Status() BotStatus {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.status
}

// Subscribe calls listener at each change of the status.
func (b *Bot) Subscribe(listener func()) (stop func()) {
	b.mu.Lock()
	id := b.nextToken
	b.nextToken++
	b.listeners[id] = listener
	b.mu.Unlock()
	return func() {
		b.mu.Lock()
		delete(b.listeners, id)
		b.mu.Unlock()
	}
}

func (b *Bot) set(status BotStatus) {
	b.mu.Lock()
	b.status = status
	listeners := make([]func(), 0, len(b.listeners))
	for _, listener := range b.listeners {
		listeners = append(listeners, listener)
	}
	b.mu.Unlock()
	for _, listener := range listeners {
		listener()
	}
}

// Connected is whether the bot's client is ready.
func (b *Bot) Connected() bool {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.session != nil && b.ready
}

// Connect connects with token, replacing a connection: its status once Discord accepted or refused it. A failure to reach
// Discord is tried again every minute (the status says why meanwhile).
func (b *Bot) Connect(token string) BotStatus {
	b.mu.Lock()
	b.generation++
	generation := b.generation
	if b.retry != nil {
		b.retry.Stop()
	}
	previous := b.session
	b.session, b.ready = nil, false
	b.mu.Unlock()
	if previous != nil {
		_ = previous.Close()
	}
	b.set(BotStatus{State: "connecting"})
	session, err := discordgo.New("Bot " + token)
	if err != nil {
		b.set(BotStatus{State: "error", Error: "Could not reach Discord: " + err.Error()})
		return b.Status()
	}
	session.Identify.Intents = discordgo.IntentsGuilds | discordgo.IntentsGuildMessages | discordgo.IntentMessageContent
	session.ShouldReconnectOnError = true
	readied := make(chan struct{})
	var once sync.Once
	session.AddHandler(func(s *discordgo.Session, _ *discordgo.Ready) {
		once.Do(func() { close(readied) })
		b.mu.Lock()
		current := generation == b.generation && b.session == s
		b.mu.Unlock()
		// A new gateway session after the first: messages may have been missed.
		if current {
			b.onEvent(BotEvent{Ready: true})
		}
	})
	session.AddHandler(func(s *discordgo.Session, m *discordgo.MessageCreate) {
		b.mu.Lock()
		current := generation == b.generation
		b.mu.Unlock()
		if !current || s.State == nil || s.State.User == nil || m.Author == nil || m.Author.ID == s.State.User.ID {
			return
		}
		b.onEvent(BotEvent{Message: ptr(messageOf(s, m.Message, s.State.User.ID))})
	})
	refresh := func(s *discordgo.Session, _ any) {
		b.mu.Lock()
		current := generation == b.generation && b.session == s && b.ready
		b.mu.Unlock()
		if current {
			if status, err := connectedStatus(s); err == nil {
				b.set(status)
			}
		}
	}
	session.AddHandler(func(s *discordgo.Session, e *discordgo.GuildCreate) { refresh(s, e) })
	session.AddHandler(func(s *discordgo.Session, e *discordgo.GuildDelete) { refresh(s, e) })
	status, failure := b.login(session, readied)
	if failure != nil {
		_ = session.Close()
		b.mu.Lock()
		stale := generation != b.generation
		b.mu.Unlock()
		if stale {
			return b.Status()
		}
		reason := failure.Error()
		message := "Could not reach Discord: " + reason
		var denied *refusalError
		if errors.As(failure, &denied) {
			message = denied.text
		} else {
			b.mu.Lock()
			b.retry = time.AfterFunc(retryAfter, func() { b.Connect(token) })
			b.mu.Unlock()
		}
		b.set(BotStatus{State: "error", Error: message})
		return b.Status()
	}
	b.mu.Lock()
	if generation != b.generation {
		b.mu.Unlock()
		_ = session.Close()
		return b.Status()
	}
	b.session, b.ready = session, true
	b.mu.Unlock()
	b.set(status)
	b.onEvent(BotEvent{Ready: true})
	return b.Status()
}

type refusalError struct{ text string }

func (e *refusalError) Error() string { return e.text }

// refusalOf is why Discord refused a login, in the owner's words, from discordgo's log line; empty for other lines.
func refusalOf(text string) string {
	if strings.Contains(text, "4014") || strings.Contains(text, "isallowed intent") {
		return "Discord refused the Message Content intent: turn on \"Message Content Intent\" under Bot in the Discord Developer Portal, then connect again."
	}
	if strings.Contains(text, "4004") || strings.Contains(text, "Authentication failed") {
		return "Discord refused the token."
	}
	return ""
}

// login opens the gateway and waits until Discord is ready; the status it then has.
func (b *Bot) login(session *discordgo.Session, readied <-chan struct{}) (BotStatus, error) {
	// A token the REST API refuses never gets to the gateway.
	if _, err := session.User("@me"); err != nil {
		var rest *discordgo.RESTError
		if errors.As(err, &rest) && rest.Response != nil && rest.Response.StatusCode == 401 {
			return BotStatus{}, &refusalError{"Discord refused the token."}
		}
		return BotStatus{}, err
	}
	// The gateway closes a session whose application lacks the Message Content intent (4014) without a word discordgo gives
	// back: the application's flags say so beforehand.
	application, err := session.Application("@me")
	if err != nil {
		return BotStatus{}, err
	}
	if application.Flags&(gatewayMessageContent|gatewayMessageContentLimited) == 0 {
		return BotStatus{}, &refusalError{refusalOf("disallowed intent")}
	}
	if err := session.Open(); err != nil {
		if text := refusalOf(err.Error()); text != "" {
			return BotStatus{}, &refusalError{text}
		}
		return BotStatus{}, err
	}
	select {
	case <-readied:
	case <-time.After(readyTimeout):
		return BotStatus{}, errors.New("Discord did not get ready")
	}
	return connectedStatus(session)
}

func connectedStatus(session *discordgo.Session) (BotStatus, error) {
	application, err := session.Application("@me")
	if err != nil {
		return BotStatus{}, err
	}
	user := session.State.User
	status := BotStatus{
		State: "connected", User: tagOf(user), ApplicationID: application.ID,
		InviteURL: "https://discord.com/oauth2/authorize?client_id=" + application.ID + "&scope=bot&permissions=" + strconv.FormatInt(invitePermissions, 10),
		Guilds:    []GuildInfo{},
	}
	if user.Avatar != "" {
		status.AvatarURL = user.AvatarURL("64")
	}
	session.State.RLock()
	for _, guild := range session.State.Guilds {
		status.Guilds = append(status.Guilds, GuildInfo{guild.ID, guild.Name})
	}
	session.State.RUnlock()
	return status, nil
}

func tagOf(user *discordgo.User) string {
	if user.Discriminator == "" || user.Discriminator == "0" {
		return user.Username
	}
	return user.Username + "#" + user.Discriminator
}

func ptr[T any](v T) *T { return &v }

var errNoClient = errors.New("The Discord bot is not connected")

func (b *Bot) client() (*discordgo.Session, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.session == nil || !b.ready {
		return nil, errNoClient
	}
	return b.session, nil
}

// BotID is the bot's own user ID.
func (b *Bot) BotID() string {
	session, err := b.client()
	if err != nil {
		return ""
	}
	return session.State.User.ID
}

func isThread(kind discordgo.ChannelType) bool {
	return kind == discordgo.ChannelTypeGuildNewsThread || kind == discordgo.ChannelTypeGuildPublicThread || kind == discordgo.ChannelTypeGuildPrivateThread
}

func isText(kind discordgo.ChannelType) bool {
	return kind == discordgo.ChannelTypeGuildText || kind == discordgo.ChannelTypeGuildNews || isThread(kind) || kind == discordgo.ChannelTypeGuildVoice
}

// createdAt is the time a snowflake was made (ms).
func createdAt(id string) float64 { return float64(snowflake(id)>>22 + 1420070400000) }

func (b *Bot) guildName(session *discordgo.Session, id string) (string, error) {
	if guild, err := session.State.Guild(id); err == nil && guild.Name != "" {
		return guild.Name, nil
	}
	guild, err := session.Guild(id)
	if err != nil {
		return "", err
	}
	return guild.Name, nil
}

// Channel is channel id: a guild's text channel or a thread of one.
func (b *Bot) Channel(_ context.Context, id string) (Channel, error) {
	session, err := b.client()
	if err != nil {
		return Channel{}, err
	}
	channel, err := session.Channel(id)
	if err != nil || channel == nil || channel.GuildID == "" || !isText(channel.Type) {
		return Channel{}, fmt.Errorf("No text channel %s", id)
	}
	name, err := b.guildName(session, channel.GuildID)
	if err != nil {
		return Channel{}, err
	}
	out := Channel{ID: channel.ID, Name: channel.Name, GuildID: channel.GuildID, GuildName: name}
	if channel.LastMessageID != "" {
		out.LastMessageID = &channel.LastMessageID
	}
	if isThread(channel.Type) {
		out.Thread = &ThreadInfo{ParentID: channel.ParentID, CreatedAt: createdAt(channel.ID), CreatedID: channel.ID}
	}
	return out, nil
}

// Messages is the first limit messages after `after`, oldest first.
func (b *Bot) Messages(_ context.Context, channelID, after string, limit int) ([]Message, error) {
	session, err := b.client()
	if err != nil {
		return nil, err
	}
	found, err := session.ChannelMessages(channelID, limit, "", after, "")
	if err != nil {
		return nil, err
	}
	botID := session.State.User.ID
	slices.SortFunc(found, func(a, c *discordgo.Message) int {
		x, y := snowflake(a.ID), snowflake(c.ID)
		switch {
		case x < y:
			return -1
		case x > y:
			return 1
		}
		return 0
	})
	out := make([]Message, len(found))
	for i, message := range found {
		out[i] = messageOf(session, message, botID)
	}
	return out, nil
}

// Reply posts text with files as a reply to messageID (when not empty); nobody is mentioned.
func (b *Bot) Reply(_ context.Context, channelID, messageID, text string, files []File) error {
	session, err := b.client()
	if err != nil {
		return err
	}
	send := &discordgo.MessageSend{Content: text, AllowedMentions: &discordgo.MessageAllowedMentions{Parse: []discordgo.AllowedMentionType{}}}
	for _, file := range files {
		send.Files = append(send.Files, &discordgo.File{Name: file.Name, Reader: bytes.NewReader(file.Data)})
	}
	if messageID != "" {
		missing := false
		send.Reference = &discordgo.MessageReference{MessageID: messageID, ChannelID: channelID, FailIfNotExists: &missing}
	}
	_, err = session.ChannelMessageSendComplex(channelID, send)
	return err
}

// UploadLimit is the bytes one message's attachments may have in channelID: Discord's per-message limit by the server's
// boost level.
func (b *Bot) UploadLimit(_ context.Context, channelID string) (float64, error) {
	session, err := b.client()
	if err != nil {
		return 0, err
	}
	channel, err := session.Channel(channelID)
	if err != nil {
		return 0, err
	}
	tier := discordgo.PremiumTierNone
	if channel.GuildID != "" {
		if guild, err := session.State.Guild(channel.GuildID); err == nil {
			tier = guild.PremiumTier
		} else if guild, err := session.Guild(channel.GuildID); err == nil {
			tier = guild.PremiumTier
		}
	}
	megabytes := 10
	switch tier {
	case discordgo.PremiumTier3:
		megabytes = 100
	case discordgo.PremiumTier2:
		megabytes = 50
	}
	return float64(megabytes * 1024 * 1024), nil
}

// Typing shows "Pi is typing" in channelID; it does not wait for Discord.
func (b *Bot) Typing(_ context.Context, channelID string) error {
	session, err := b.client()
	if err != nil {
		return err
	}
	go func() { _ = session.ChannelTyping(channelID) }()
	return nil
}

// Server is server guildID's text channels and roles (without @everyone), by name.
func (b *Bot) Server(guildID string) (ServerDetails, error) {
	session, err := b.client()
	if err != nil {
		return ServerDetails{}, err
	}
	channels, err := session.GuildChannels(guildID)
	if err != nil {
		return ServerDetails{}, err
	}
	roles, err := session.GuildRoles(guildID)
	if err != nil {
		return ServerDetails{}, err
	}
	details := ServerDetails{Channels: []NamedChannel{}, Roles: []NamedChannel{}}
	for _, channel := range channels {
		if channel.Type == discordgo.ChannelTypeGuildText || channel.Type == discordgo.ChannelTypeGuildNews {
			details.Channels = append(details.Channels, NamedChannel{channel.ID, channel.Name})
		}
	}
	for _, role := range roles {
		if role.ID != guildID && !role.Managed {
			details.Roles = append(details.Roles, NamedChannel{role.ID, role.Name})
		}
	}
	byName := func(a, c NamedChannel) int { return strings.Compare(strings.ToLower(a.Name), strings.ToLower(c.Name)) }
	slices.SortFunc(details.Channels, byName)
	slices.SortFunc(details.Roles, byName)
	return details, nil
}

// Close disconnects the bot and cancels its retry.
func (b *Bot) Close() {
	b.mu.Lock()
	b.generation++
	if b.retry != nil {
		b.retry.Stop()
	}
	session := b.session
	b.session, b.ready = nil, false
	b.mu.Unlock()
	if session != nil {
		_ = session.Close()
	}
}

// messageOf is a Discord message as the relay reads it. A mention of the bot's own role (Discord offers it beside the bot's
// user when typing `@name`) counts as a mention of the bot, written as one.
func messageOf(session *discordgo.Session, message *discordgo.Message, botID string) Message {
	role := ""
	if message.GuildID != "" && session.State != nil {
		if guild, err := session.State.Guild(message.GuildID); err == nil {
			if me, err := session.State.Member(message.GuildID, botID); err == nil {
				for _, each := range guild.Roles {
					if each.Managed && slices.Contains(me.Roles, each.ID) {
						role = each.ID
					}
				}
			}
		}
	}
	byRole := role != "" && slices.Contains(message.MentionRoles, role)
	name := message.Author.Username
	if message.Author.GlobalName != "" {
		name = message.Author.GlobalName
	}
	roles := []string{}
	if message.Member != nil {
		if message.Member.Nick != "" {
			name = message.Member.Nick
		}
		roles = append(roles, message.Member.Roles...)
	}
	text := message.Content
	if byRole {
		text = strings.ReplaceAll(text, "<@&"+role+">", "<@"+botID+">")
	}
	mentioned := byRole
	for _, user := range message.Mentions {
		if user.ID == botID {
			mentioned = true
		}
	}
	out := Message{
		ID: message.ID, ChannelID: message.ChannelID,
		Author: Author{ID: message.Author.ID, Name: name, Bot: message.Author.Bot},
		Roles:  roles, Text: text, MentionsBot: botID != "" && mentioned,
	}
	for _, attachment := range message.Attachments {
		out.Attachments = append(out.Attachments, Attachment{Name: attachment.Filename, URL: attachment.URL, Size: float64(attachment.Size)})
	}
	return out
}
