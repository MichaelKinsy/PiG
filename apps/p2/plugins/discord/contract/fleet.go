package contract

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/MichaelKinsy/PiG/internal/chord/chordjson"
)

// Ports apps/p2/plugins/discord/fleet.ts: the owner's Discord bots (server, a native of `DiscordStore`): their tokens in
// `<data>/discord.json` (0600), one connected `Bot` each, their views, and what they all receive in one queue (oldest
// first) for the store's routine.

// FirstBot is the ID the one bot of the first release (`{ "token": … }`) gets; its links name it too.
const FirstBot = "bot1"

// BotView is one configured bot: its ID and its connection.
type BotView struct {
	ID     string
	Status BotStatus
}

// Received is what a bot of the fleet received (a message, or its (re)connection), and which bot.
type Received struct {
	Bot   string
	Event BotEvent
}

type botEntry struct {
	Token   string  `json:"token"`
	AddedAt float64 `json:"addedAt"`
}

type botsFile struct {
	Bots map[string]botEntry `json:"bots"`
}

// Fleet is the owner's bots.
type Fleet struct {
	path      string
	mu        sync.Mutex
	file      botsFile
	bots      map[string]*Bot
	listeners map[int]func()
	next      int
	queue     []Received
	wake      chan struct{}
}

// OpenFleet is the fleet of dataDir's discord.json, each bot connecting in the background.
func OpenFleet(dataDir string) (*Fleet, error) {
	path := filepath.Join(dataDir, "discord.json")
	text, err := os.ReadFile(path)
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return nil, err
	}
	file, migrated := parseBots(string(text), err == nil)
	fleet := &Fleet{path: path, file: file, bots: map[string]*Bot{}, listeners: map[int]func(){}, wake: make(chan struct{})}
	if migrated {
		if err := fleet.save(); err != nil {
			return nil, err
		}
	}
	for id, entry := range file.Bots {
		bot := fleet.start(id)
		//portlint:allow golifetime Connect returns once the bot is connected or closed (Pi's un-awaited connect)
		go bot.Connect(entry.Token)
	}
	return fleet, nil
}

// Subscribe calls listener at each change of the views.
func (f *Fleet) Subscribe(listener func()) (stop func()) {
	f.mu.Lock()
	id := f.next
	f.next++
	f.listeners[id] = listener
	f.mu.Unlock()
	return func() {
		f.mu.Lock()
		delete(f.listeners, id)
		f.mu.Unlock()
	}
}

func (f *Fleet) changed() {
	f.mu.Lock()
	listeners := make([]func(), 0, len(f.listeners))
	for _, listener := range f.listeners {
		listeners = append(listeners, listener)
	}
	f.mu.Unlock()
	for _, listener := range listeners {
		listener()
	}
}

// Views is every bot with its status, oldest first.
func (f *Fleet) Views() []BotView {
	f.mu.Lock()
	ids := make([]string, 0, len(f.file.Bots))
	for id := range f.file.Bots {
		ids = append(ids, id)
	}
	slices.SortStableFunc(ids, func(a, b string) int {
		x, y := f.file.Bots[a].AddedAt, f.file.Bots[b].AddedAt
		switch {
		case x < y:
			return -1
		case x > y:
			return 1
		}
		return strings.Compare(a, b)
	})
	bots := make([]*Bot, len(ids))
	for i, id := range ids {
		bots[i] = f.bots[id]
	}
	f.mu.Unlock()
	views := make([]BotView, len(ids))
	for i, id := range ids {
		status := BotStatus{State: "connecting"}
		if bots[i] != nil {
			status = bots[i].Status()
		}
		views[i] = BotView{ID: id, Status: status}
	}
	return views
}

// Connected is bot id while it is connected (nil otherwise).
func (f *Fleet) Connected(id string) *Bot {
	f.mu.Lock()
	bot := f.bots[id]
	f.mu.Unlock()
	if bot != nil && bot.Connected() {
		return bot
	}
	return nil
}

func randomID() string {
	var raw [4]byte
	_, _ = rand.Read(raw[:])
	return "b" + hex.EncodeToString(raw[:])
}

// Add connects a new bot with token and keeps it once Discord accepted it: its ID.
func (f *Fleet) Add(token string) (string, error) {
	id := randomID()
	bot := f.candidate(id)
	status := bot.Connect(token)
	if status.State != "connected" {
		bot.Close()
		return "", refusedError(status)
	}
	for _, view := range f.Views() {
		if view.Status.State == "connected" && view.Status.ApplicationID == status.ApplicationID {
			bot.Close()
			return "", fmt.Errorf("%s is added already", status.User)
		}
	}
	f.adopt(id, bot)
	f.mu.Lock()
	f.file.Bots[id] = botEntry{Token: token, AddedAt: float64(time.Now().UnixMilli())}
	f.mu.Unlock()
	if err := f.save(); err != nil {
		return "", err
	}
	f.changed()
	return id, nil
}

func refusedError(status BotStatus) error {
	if status.State == "error" {
		return errors.New(status.Error)
	}
	return errors.New("Discord did not answer")
}

// SetToken connects bot id with a new token of the same Discord application, kept once Discord accepted it.
func (f *Fleet) SetToken(id, token string) error {
	f.mu.Lock()
	current := f.bots[id]
	entry, known := f.file.Bots[id]
	f.mu.Unlock()
	if current == nil || !known {
		return errors.New("No such bot")
	}
	bot := f.candidate(id)
	status := bot.Connect(token)
	if status.State != "connected" {
		bot.Close()
		return refusedError(status)
	}
	if was := current.Status(); was.State == "connected" && was.ApplicationID != status.ApplicationID {
		bot.Close()
		return fmt.Errorf("This token is for %s, another bot: add it as a new bot", status.User)
	}
	current.Close()
	f.adopt(id, bot)
	f.mu.Lock()
	f.file.Bots[id] = botEntry{Token: token, AddedAt: entry.AddedAt}
	f.mu.Unlock()
	if err := f.save(); err != nil {
		return err
	}
	f.changed()
	return nil
}

// Remove disconnects and forgets bot id.
func (f *Fleet) Remove(id string) error {
	f.mu.Lock()
	bot := f.bots[id]
	delete(f.bots, id)
	delete(f.file.Bots, id)
	f.mu.Unlock()
	if bot != nil {
		bot.Close()
	}
	err := f.save()
	f.changed()
	return err
}

// Next is the next received event, or false when ctx ends.
func (f *Fleet) Next(ctx context.Context) (Received, bool) {
	for {
		f.mu.Lock()
		if len(f.queue) > 0 {
			received := f.queue[0]
			f.queue = f.queue[1:]
			f.mu.Unlock()
			return received, true
		}
		wake := f.wake
		f.mu.Unlock()
		select {
		case <-wake:
		case <-ctx.Done():
			return Received{}, false
		}
	}
}

// Close disconnects every bot.
func (f *Fleet) Close() {
	f.mu.Lock()
	bots := make([]*Bot, 0, len(f.bots))
	for _, bot := range f.bots {
		bots = append(bots, bot)
	}
	f.bots = map[string]*Bot{}
	f.mu.Unlock()
	for _, bot := range bots {
		bot.Close()
	}
}

// candidate is a bot id that is not one of the fleet's yet (a token being tried); it delivers messages once adopted.
func (f *Fleet) candidate(id string) *Bot {
	var bot *Bot
	bot = NewBot(func(event BotEvent) {
		f.mu.Lock()
		own := f.bots[id] == bot
		f.mu.Unlock()
		if own {
			f.push(Received{Bot: id, Event: event})
		}
	})
	return bot
}

// adopt makes bot the fleet's bot id; one connected already (a new token) catches up at once.
func (f *Fleet) adopt(id string, bot *Bot) {
	f.mu.Lock()
	f.bots[id] = bot
	f.mu.Unlock()
	bot.Subscribe(f.changed)
	if bot.Connected() {
		f.push(Received{Bot: id, Event: BotEvent{Ready: true}})
	}
}

func (f *Fleet) push(received Received) {
	f.mu.Lock()
	f.queue = append(f.queue, received)
	wake := f.wake
	f.wake = make(chan struct{})
	f.mu.Unlock()
	close(wake)
}

func (f *Fleet) start(id string) *Bot {
	bot := f.candidate(id)
	f.adopt(id, bot)
	return bot
}

func (f *Fleet) save() error {
	f.mu.Lock()
	compact, err := chordjson.Marshal(f.file)
	f.mu.Unlock()
	if err != nil {
		return err
	}
	// JSON.stringify(file, null, "\t"): no HTML escaping, then the tab indentation.
	var indented bytes.Buffer
	if err := json.Indent(&indented, compact, "", "\t"); err != nil {
		return err
	}
	data := indented.Bytes()
	temporary := fmt.Sprintf("%s.%d.tmp", f.path, os.Getpid())
	if err := os.WriteFile(temporary, append(data, '\n'), 0o600); err != nil {
		return err
	}
	if err := os.Chmod(temporary, 0o600); err != nil {
		return err
	}
	return os.Rename(temporary, f.path)
}

// parseBots reads discord.json (a trust boundary: the owner may edit it): the bots, and whether it was the first release's
// `{ "token": … }` (that bot becomes FirstBot). Unreadable: no bots, said in the log.
func parseBots(text string, exists bool) (botsFile, bool) {
	none := botsFile{Bots: map[string]botEntry{}}
	if !exists || strings.TrimSpace(text) == "" {
		return none, false
	}
	var value any
	if err := json.Unmarshal([]byte(text), &value); err != nil {
		fmt.Fprintln(os.Stderr, "p2 discord: discord.json is not valid JSON; no bots run")
		return none, false
	}
	object, ok := value.(map[string]any)
	if !ok {
		return none, false
	}
	if token, ok := object["token"].(string); ok && token != "" {
		return botsFile{Bots: map[string]botEntry{FirstBot: {Token: token}}}, true
	}
	listed, _ := object["bots"].(map[string]any)
	for id, each := range listed {
		entry, _ := each.(map[string]any)
		token, _ := entry["token"].(string)
		if token == "" {
			continue
		}
		added, _ := entry["addedAt"].(float64)
		if math.IsNaN(added) {
			added = 0
		}
		none.Bots[id] = botEntry{Token: token, AddedAt: added}
	}
	return none, false
}
