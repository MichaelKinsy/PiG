// Package activity is the chat's turn model, as the web UI shows it (apps/p2/web/upstream/src/web/activity/model.ts at the
// default detail 2): what the agent did between a person's message and its answer. The transcript's items (the p2 TUI's shared
// model) become chat items: each run of responses and calls is its thinking and calls on ONE activity row (a wheel on the web,
// a summary row here) between the agent's texts, the turn's last text is its answer. Pure data, no MyGo.
package activity

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/MichaelKinsy/PiG/ai"
	"github.com/MichaelKinsy/PiG/apps/p2/tui/jsvalue"
	"github.com/MichaelKinsy/PiG/apps/p2/tui/transcript"
)

// StepStatus is a call's state for its row: running, done or error (an interrupted call is an error).
type StepStatus string

// The step states.
const (
	StepRunning StepStatus = "running"
	StepDone    StepStatus = "done"
	StepError   StepStatus = "error"
)

// Step is one tool call of an activity.
type Step struct {
	ID     string
	Tool   transcript.ToolView
	Status StepStatus
}

// EntryKind is what an entry of an activity is.
type EntryKind string

// The entry kinds: a call, or the agent's thinking.
const (
	EntryCall    EntryKind = "call"
	EntryThought EntryKind = "thought"
)

// Entry is one thing on an activity, in order: a call (Step) or a thought (ID, Text, Live).
type Entry struct {
	Kind EntryKind
	Step *Step
	ID   string
	Text string
	Live bool
}

// Item is a chat item.
type Item interface{ ItemKey() string }

// Passed is a transcript item the chat shows as it is: a person's message, a marker, a failure, an unloaded gap.
type Passed struct{ transcript.Item }

// Activity is a run of thinking and calls as one row. Working: the agent is still at it.
type Activity struct {
	Key     string
	Steps   []Step
	Entries []Entry
	Working bool
	// StartedAt is when the run began (its first response's time) and EndedAt, once done, when it ended, in ms; 0 when unknown.
	StartedAt, EndedAt int64
}

// Text is the agent's text between its calls, as prose.
type Text struct {
	Key  string
	Text string
}

// Answer is a turn's last text, with the response it ends (for its stop, error or length notice).
type Answer struct {
	Key       string
	Text      string
	Message   *ai.AssistantMessage
	Streaming bool
}

// ItemKey names the item.
func (i Activity) ItemKey() string { return i.Key }

// ItemKey names the item.
func (i Text) ItemKey() string { return i.Key }

// ItemKey names the item.
func (i Answer) ItemKey() string { return i.Key }

func stepOf(tool transcript.ToolView) Step {
	st := StepRunning
	switch tool.Status {
	case transcript.ToolError, transcript.ToolInterrupted:
		st = StepError
	case transcript.ToolDone:
		st = StepDone
	}
	return Step{ID: tool.ID, Tool: tool, Status: st}
}

// part is a run's piece before the wheels are made: thinking, a text, or a run of calls.
type part struct {
	kind  string // "thought", "text", "calls"
	key   string
	text  string
	live  bool
	steps []Step
	from  []*transcript.AssistantItem
	ents  []Entry
	entry *int
}

func partsOf(run []transcript.Item, live bool, after string, byKey map[string]*transcript.AssistantItem) []part {
	var parts []part
	calls := func(key string, from *transcript.AssistantItem) *part {
		if n := len(parts); n > 0 && parts[n-1].kind == "calls" {
			last := &parts[n-1]
			if from != nil && (len(last.from) == 0 || last.from[len(last.from)-1] != from) {
				last.from = append(last.from, from)
			}
			return last
		}
		k := key
		if len(parts) == 0 {
			k = "activity:" + after
		}
		made := part{kind: "calls", key: k}
		if from != nil {
			made.from = []*transcript.AssistantItem{from}
		}
		parts = append(parts, made)
		return &parts[len(parts)-1]
	}
	for _, raw := range run {
		switch item := raw.(type) {
		case transcript.ToolItem:
			c := calls("activity:"+item.Key, nil)
			c.steps = append(c.steps, stepOf(item.Tool))
		case transcript.AssistantItem:
			if item.Message == nil {
				continue
			}
			tools := make(map[string]transcript.ToolView, len(item.Tools))
			for _, t := range item.Tools {
				tools[t.ID] = t
			}
			last := len(item.Message.Content) - 1
			for i, block := range item.Message.Content {
				key := fmt.Sprint(item.Key, ":", i)
				switch b := block.(type) {
				case ai.ThinkingContent:
					if strings.TrimSpace(b.Thinking) == "" {
						continue
					}
					thinking := item.Streaming && live && i == last
					if n := len(parts); n > 0 && parts[n-1].kind == "thought" {
						parts[n-1].text += "\n\n" + b.Thinking
						parts[n-1].live = thinking
					} else {
						parts = append(parts, part{kind: "thought", key: "thought:" + key, text: b.Thinking, live: thinking, entry: item.EntryID})
					}
				case ai.TextContent:
					if strings.TrimSpace(b.Text) != "" {
						parts = append(parts, part{kind: "text", key: "text:" + key, text: b.Text, entry: item.EntryID})
					}
				case ai.ToolCall:
					tool, ok := tools[b.ID]
					if !ok {
						tool = transcript.ToolView{ID: b.ID, Name: b.Name, Status: transcript.ToolPending}
					}
					if tool.Args == nil {
						tool.Args = argsOf(b)
					}
					c := calls("activity:"+key, byKey[item.Key])
					c.steps = append(c.steps, stepOf(tool))
				}
			}
		}
	}
	return parts
}

// wheels merges thinking into the runs of calls around it: each stretch between the texts is one activity.
func wheels(parts []part, responses []*transcript.AssistantItem, after string) []part {
	var merged []part
	for _, p := range parts {
		if p.kind == "text" {
			merged = append(merged, p)
			continue
		}
		if n := len(merged); n == 0 || merged[n-1].kind != "calls" {
			key := "activity:" + after
			if n > 0 {
				if p.kind == "calls" {
					key = p.key
				} else {
					key = "activity:" + p.key
				}
			}
			merged = append(merged, part{kind: "calls", key: key})
		}
		w := &merged[len(merged)-1]
		from := p.from
		if p.kind != "calls" {
			from = nil
			for _, r := range responses {
				if r.EntryID != nil && p.entry != nil && *r.EntryID == *p.entry {
					from = append(from, r)
				}
			}
		}
		for _, r := range from {
			dup := false
			for _, e := range w.from {
				dup = dup || e == r
			}
			if !dup {
				w.from = append(w.from, r)
			}
		}
		if p.kind == "calls" {
			w.steps = append(w.steps, p.steps...)
			for i := range p.steps {
				w.ents = append(w.ents, Entry{Kind: EntryCall, Step: &p.steps[i]})
			}
		} else {
			w.ents = append(w.ents, Entry{Kind: EntryThought, ID: p.key, Text: p.text, Live: p.live})
		}
	}
	index := func(r *transcript.AssistantItem) int {
		for i, x := range responses {
			if x == r {
				return i
			}
		}
		return -1
	}
	for i := range merged {
		f := merged[i].from
		for a := 1; a < len(f); a++ {
			for b := a; b > 0 && index(f[b-1]) > index(f[b]); b-- {
				f[b-1], f[b] = f[b], f[b-1]
			}
		}
	}
	return merged
}

func activityOf(p part, responses []*transcript.AssistantItem, working bool) Activity {
	a := Activity{Key: p.key, Steps: p.steps, Entries: p.ents, Working: working}
	if len(p.from) == 0 || p.from[0].Message == nil {
		return a
	}
	a.StartedAt = p.from[0].Message.Timestamp
	last := p.from[len(p.from)-1]
	if working || last.Message == nil {
		return a
	}
	var next *transcript.AssistantItem
	for i, r := range responses {
		if r == last && i+1 < len(responses) {
			next = responses[i+1]
		}
	}
	switch {
	case next != nil && next.Message != nil:
		a.EndedAt = next.Message.Timestamp
	default:
		end := last.Message.Timestamp
		for _, t := range last.Tools {
			if t.DurationMs != nil {
				end += int64(*t.DurationMs)
			}
		}
		a.EndedAt = end
	}
	return a
}

func runItems(run []transcript.Item, working bool, after string) []Item {
	var responses []*transcript.AssistantItem
	byKey := map[string]*transcript.AssistantItem{}
	for _, it := range run {
		if a, ok := it.(transcript.AssistantItem); ok {
			a := a
			responses = append(responses, &a)
			byKey[a.Key] = &a
		}
	}
	parts := partsOf(run, working, after, byKey)
	var response *transcript.AssistantItem
	if len(responses) > 0 {
		response = responses[len(responses)-1]
	}
	var answer *part
	if n := len(parts); n > 0 && parts[n-1].kind == "text" {
		a := parts[n-1]
		answer = &a
		parts = parts[:n-1]
	}
	parts = wheels(parts, responses, after)
	var final *part
	if n := len(parts); n > 0 {
		final = &parts[n-1]
	}
	var items []Item
	for i := range parts {
		p := parts[i]
		if p.kind == "calls" {
			items = append(items, activityOf(p, responses, working && answer == nil && final != nil && i == len(parts)-1))
		} else {
			items = append(items, Text{Key: p.key, Text: p.text})
		}
	}
	if working && answer == nil && final == nil {
		items = append(items, Activity{Key: "activity:" + after, Working: true})
	}
	notice := response != nil && response.Message != nil && !response.Streaming &&
		(response.Message.StopReason == ai.StopReasonError || response.Message.StopReason == ai.StopReasonAborted || response.Message.StopReason == ai.StopReasonLength)
	if response != nil && (answer != nil || notice) {
		ans := Answer{Key: "answer:" + response.Key, Message: response.Message, Streaming: response.Streaming}
		if answer != nil {
			ans.Key, ans.Text = answer.key, answer.text
		}
		items = append(items, ans)
	}
	return items
}

// Items are the chat's items for a transcript: the responses between two people's messages (or markers and failures) become one
// run, shown as its thinking, texts and runs of calls in order, then its answer. working: the agent is at it, so the last run is live.
func Items(items []transcript.Item, working bool) []Item {
	var out []Item
	var run []transcript.Item
	after := "start"
	flush := func(last bool) {
		live := last && working
		if len(run) == 0 {
			if live {
				out = append(out, Activity{Key: "activity:" + after, Working: true})
			}
			return
		}
		out = append(out, runItems(run, live, after)...)
		run = nil
	}
	for _, it := range items {
		switch it.(type) {
		case transcript.AssistantItem, transcript.ToolItem:
			run = append(run, it)
			continue
		}
		flush(false)
		out = append(out, Passed{it})
		after = it.ItemKey()
	}
	flush(true)
	return out
}

// Elapsed is a duration as a reader says it: 12s, 1m 05s, 1h 02m.
func Elapsed(ms int64) string {
	seconds := ms / 1000
	if seconds < 0 {
		seconds = 0
	}
	switch {
	case seconds < 60:
		return fmt.Sprintf("%ds", seconds)
	case seconds < 3600:
		return fmt.Sprintf("%dm %02ds", seconds/60, seconds%60)
	}
	return fmt.Sprintf("%dh %02dm", seconds/3600, seconds/60%60)
}

// argsOf is the arguments a call's request carried, for a call whose result has not given its own.
func argsOf(call ai.ToolCall) *jsvalue.Object {
	text, err := json.Marshal(call.Arguments)
	if err != nil {
		return nil
	}
	value, err := jsvalue.Parse(text)
	if err != nil {
		return nil
	}
	object, _ := value.(*jsvalue.Object)
	return object
}
