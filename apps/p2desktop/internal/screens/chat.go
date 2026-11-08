package screens

import (
	"context"
	"encoding/json"
	"fmt"
	"sync"

	chatcontract "github.com/MichaelKinsy/PiG/apps/p2/plugins/chat/contract"
	statuscontract "github.com/MichaelKinsy/PiG/apps/p2/plugins/status/contract"
	"github.com/MichaelKinsy/PiG/apps/p2/tui/transcript"
	"github.com/MichaelKinsy/PiG/apps/p2desktop/internal/surfacehost"
	"github.com/MichaelKinsy/PiG/chord/delta"
	"github.com/MichaelKinsy/PiG/chord/next/host"
	"github.com/MichaelKinsy/PiG/chord/next/tui"
	"github.com/MichaelKinsy/PiG/durable"
	"github.com/MichaelKinsy/PiG/durable/harness"
	"github.com/MichaelKinsy/PiG/internal/chord/chordjson"
)

// Chat is one conversation of a session as the chat screen shows it: the transcript (the Chat contract's `view`, a window over
// the newest entries, joined by harness.ConversationTranscript with the older entries the reader scrolled back to and built
// into display items by the p2 TUI's shared model, apps/p2/tui/transcript), the model and the context numbers, and what the
// composer does (send, stop). The view is a function of what these return.
type Chat struct {
	scope   *tui.TuiScope
	ref     *host.Reference
	ctx     context.Context
	session string
	// conversation is the one this screen holds.
	conversation int

	viewRaw  func() (delta.JsonValue, bool)
	modelRaw func() (delta.JsonValue, bool)
	statsRaw func() (delta.JsonValue, bool)
	// clientsRaw and usageRaw are the session's metrics (p2.session-metrics): the viewers and the spend.
	clientsRaw func() (delta.JsonValue, bool)
	usageRaw   func() (delta.JsonValue, bool)

	mu      sync.Mutex
	changed func()
	// joined holds every entry got from the view or fetched; built is the display transcript of the last view value.
	joined   *harness.ConversationTranscript
	seen     *delta.JsonObject
	built    transcript.Transcript
	have     bool
	err      error
	earlier  bool // a load of older entries runs
	sendSeq  int
	lastSent string
}

// OpenChat holds the conversation of session on scope: its view, model and stats. changed is called when any of them
// changes or a load finishes (the window's redraw).
func OpenChat(h *surfacehost.Host, scope *tui.TuiScope, session string, conversation int, changed func()) (*Chat, error) {
	ref, err := scope.Hold(chatcontract.Chat, delta.JsonObjectOf("session", session, "conversation", float64(conversation)))
	if err != nil {
		return nil, err
	}
	c := &Chat{scope: scope, ref: ref, ctx: h.Context(), session: session, conversation: conversation, changed: changed}
	c.viewRaw = tui.WatchMember(scope, ref.Member("view"))
	c.modelRaw = tui.WatchMember(scope, ref.Member("model"))
	c.statsRaw = tui.WatchMember(scope, ref.Member("stats"))
	metrics, err := scope.Hold(statuscontract.SessionMetrics, delta.JsonObjectOf("session", session, "viewer", "p2-desktop"))
	if err != nil {
		return nil, err
	}
	c.clientsRaw = tui.WatchMember(scope, metrics.Member("clients"))
	c.usageRaw = tui.WatchMember(scope, metrics.Member("usage"))
	joined, err := harness.NewConversationTranscript(c.loadEntries, nil)
	if err != nil {
		return nil, err
	}
	c.joined = joined
	return c, nil
}

// Conversation is the conversation shown: the session's main one.
func (c *Chat) Conversation() int { return c.conversation }

// Session is the session's ID.
func (c *Chat) Session() string { return c.session }

// loadEntries is the harness.EntryLoader: a page of entries through `Chat.entries`.
func (c *Chat) loadEntries(ctx context.Context, query durable.EntryQuery, limit int) (durable.Page[durable.EntryRecord, durable.Cursor], error) {
	args := delta.JsonObjectOf("limit", float64(limit))
	if query.MinEntryId != nil {
		args.Set("minEntryId", float64(*query.MinEntryId))
	}
	if query.MaxEntryId != nil {
		args.Set("maxEntryId", float64(*query.MaxEntryId))
	}
	if query.Order != nil {
		args.Set("order", string(*query.Order))
	}
	var page durable.Page[durable.EntryRecord, durable.Cursor]
	result, err := c.ref.Call("entries", args, ctx)
	if err != nil {
		return page, err
	}
	data, err := chordjson.Marshal(result)
	if err != nil {
		return page, err
	}
	var decoded struct {
		Items []durable.EntryRecord `json:"items"`
		More  bool                  `json:"more"`
	}
	if err := json.Unmarshal(data, &decoded); err != nil {
		return page, fmt.Errorf("chat entries: %w", err)
	}
	page.Items = decoded.Items
	if decoded.More {
		page.Next = &durable.Cursor{}
	}
	return page, nil
}

// Transcript is the display transcript now (false until the first view arrived).
func (c *Chat) Transcript() (transcript.Transcript, bool) {
	value, ok := c.viewRaw()
	if !ok {
		return transcript.Transcript{}, false
	}
	object, _ := value.(*delta.JsonObject)
	if object == nil {
		return transcript.Transcript{}, false
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.have && c.seen == object {
		return c.built, true
	}
	data, err := chordjson.Marshal(object)
	if err == nil {
		var view harness.ConversationView
		if err = json.Unmarshal(data, &view); err == nil {
			merged := c.joined.Merge(view)
			c.built = transcript.BuildTranscript(*merged, nil, nil)
			c.seen, c.have = object, true
		}
	}
	if err != nil {
		c.err = err
		return c.built, c.have
	}
	return c.built, true
}

// Rebuild makes the next Transcript call take what the joined transcript holds now (after older entries loaded).
func (c *Chat) rebuild(view *harness.TranscriptView) {
	c.mu.Lock()
	c.built = transcript.BuildTranscript(*view, nil, nil)
	c.have = true
	c.mu.Unlock()
}

// LoadEarlier fetches the page of entries before the oldest held, in the background; the transcript is rebuilt when it lands.
func (c *Chat) LoadEarlier() {
	c.mu.Lock()
	if c.earlier || !c.have || !c.built.Earlier {
		c.mu.Unlock()
		return
	}
	c.earlier = true
	c.mu.Unlock()
	go func() {
		view, err := c.joined.LoadEarlier(c.ctx)
		c.mu.Lock()
		c.earlier = false
		if err != nil {
			c.err = err
		}
		c.mu.Unlock()
		if err == nil && view != nil {
			c.rebuild(view)
		}
		c.changed()
	}()
}

// Model is the conversation's model metadata (nil: none).
func (c *Chat) Model() *chatcontract.ModelMeta {
	value, ok := c.modelRaw()
	object, _ := value.(*delta.JsonObject)
	if !ok || object == nil {
		return nil
	}
	num, _ := object.Value("contextWindow").(float64)
	reasoning, _ := object.Value("reasoning").(bool)
	meta := &chatcontract.ModelMeta{Provider: str(object.Value("provider")), ModelID: str(object.Value("modelId")), Name: str(object.Value("name")), ContextWindow: num, Reasoning: reasoning}
	if levels, ok := object.Value("thinkingLevels").([]any); ok {
		for _, level := range levels {
			meta.ThinkingLevels = append(meta.ThinkingLevels, str(level))
		}
	}
	return meta
}

// Stats are the active context's numbers: its estimated tokens and the latest response's usage line.
type Stats struct {
	ContextTokens float64
	Latest        map[string]float64
}

// CacheHit is the cache hit share of the latest response (the web's "chr"): cache reads over the whole prompt; ok is false without one.
func (s Stats) CacheHit() (rate float64, ok bool) {
	prompt := s.Latest["input"] + s.Latest["cacheRead"] + s.Latest["cacheWrite"]
	if prompt <= 0 {
		return 0, false
	}
	return s.Latest["cacheRead"] / prompt, true
}

// Stats reads the context numbers (the zero value until computed).
func (c *Chat) Stats() Stats {
	value, ok := c.statsRaw()
	object, _ := value.(*delta.JsonObject)
	if !ok || object == nil {
		return Stats{}
	}
	var s Stats
	s.ContextTokens, _ = object.Value("contextTokens").(float64)
	if usage, _ := object.Value("latestAssistantUsage").(*delta.JsonObject); usage != nil {
		s.Latest = map[string]float64{}
		for _, key := range usage.Keys() {
			if n, ok := usage.Value(key).(float64); ok {
				s.Latest[key] = n
			}
		}
	}
	return s
}

// Metrics are the session's viewers and spend (p2.session-metrics): Cost is the session's total cost in dollars, HasCost whether the
// session has any usage yet.
type Metrics struct {
	Clients float64
	Cost    float64
	HasCost bool
}

// Metrics reads the session's metrics (the zero value until they arrive).
func (c *Chat) Metrics() Metrics {
	var m Metrics
	if v, ok := c.clientsRaw(); ok {
		m.Clients, _ = v.(float64)
	}
	if v, ok := c.usageRaw(); ok {
		if usage, _ := v.(*delta.JsonObject); usage != nil {
			if cost, _ := usage.Value("cost").(*delta.JsonObject); cost != nil {
				m.Cost, m.HasCost = cost.Value("total").(float64)
			}
		}
	}
	return m
}

// Send sends text as the user's message (steering the run when one is going), with a fresh request ID.
func (c *Chat) Send(text string) error {
	c.mu.Lock()
	c.sendSeq++
	id := fmt.Sprintf("desktop-%s-%d", c.session, c.sendSeq)
	c.mu.Unlock()
	_, err := c.ref.Call("send", delta.JsonObjectOf("text", text, "requestId", id), c.ctx)
	c.fail(err)
	return err
}

// Stop aborts the current run.
func (c *Chat) Stop() error {
	_, err := c.ref.Call("stop", delta.NewJsonObject(0), c.ctx)
	c.fail(err)
	return err
}

// LastError is why the last call was refused (nil: none).
func (c *Chat) LastError() error { c.mu.Lock(); defer c.mu.Unlock(); return c.err }

// ClearError forgets the last error.
func (c *Chat) ClearError() { c.mu.Lock(); c.err = nil; c.mu.Unlock() }

func (c *Chat) fail(err error) {
	if err != nil {
		c.mu.Lock()
		c.err = err
		c.mu.Unlock()
	}
}
