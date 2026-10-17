package harness

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"slices"
	"strings"
	"sync"
	"time"
	"unicode/utf8"
)

// Message is one entry of an OpenAI chat-completions request as the provider saw it.
type Message struct {
	Role       string
	Text       string
	ImageURLs  []string
	ToolCalls  []WireCall
	ToolCallID string
}

// WireCall is one assistant tool call in a request transcript.
type WireCall struct {
	ID, Name, Arguments string
}

// Request is one provider request. N counts from 1 per provider.
type Request struct {
	N        int
	Raw      []byte
	Messages []Message
	Tools    []string
	Model    string
}

// LastUser returns the text of the last user message, or "".
func (r *Request) LastUser() string {
	for _, v := range slices.Backward(r.Messages) {
		if v.Role == "user" {
			return v.Text
		}
	}
	return ""
}

// Last returns the newest message.
func (r *Request) Last() Message {
	if len(r.Messages) == 0 {
		return Message{}
	}
	return r.Messages[len(r.Messages)-1]
}

// ToolResults returns the tool messages after the last assistant message.
func (r *Request) ToolResults() []Message {
	var out []Message
	for i := len(r.Messages) - 1; i >= 0 && r.Messages[i].Role == "tool"; i-- {
		out = append([]Message{r.Messages[i]}, out...)
	}
	return out
}

// Has reports whether any message text contains substr.
func (r *Request) Has(substr string) bool {
	for _, m := range r.Messages {
		if strings.Contains(m.Text, substr) {
			return true
		}
	}
	return false
}

// Call is one tool call a scripted reply makes.
type Call struct {
	Name string
	Args any
}

// Part is one streamed piece of a reply.
type Part struct {
	kind partKind
	text string
	call Call
	gate string
}

type partKind int

const (
	partText partKind = iota
	partReasoning
	partCall
	partGate
)

// Usage is the token usage a reply reports.
type Usage struct{ Prompt, Completion int }

// Reply is a scripted provider response. A reply with Status != 0 is an HTTP error.
type Reply struct {
	Status int
	Body   string
	Parts  []Part
	Usage  *Usage
	// Length makes the reply finish with finish_reason "length".
	Length bool
}

// Say streams text.
func Say(text string) Reply { return Reply{Parts: []Part{{kind: partText, text: text}}} }

// Think streams reasoning text (reasoning_content).
func Think(text string) Reply { return Reply{Parts: []Part{{kind: partReasoning, text: text}}} }

// Use calls tools; several calls in one reply are parallel tool calls.
func Use(calls ...Call) Reply {
	r := Reply{}
	for _, c := range calls {
		r.Parts = append(r.Parts, Part{kind: partCall, call: c})
	}
	return r
}

// Fail answers with an HTTP error.
func Fail(status int, body string) Reply { return Reply{Status: status, Body: body} }

// Hold is a stream gate: everything before it is flushed, then the stream blocks until Release(name) or the client disconnects.
func Hold(name string) Reply { return Reply{Parts: []Part{{kind: partGate, gate: name}}} }

// Then appends the parts of next to r.
func (r Reply) Then(next Reply) Reply {
	r.Parts = append(append([]Part(nil), r.Parts...), next.Parts...)
	if next.Usage != nil {
		r.Usage = next.Usage
	}
	return r
}

// Script maps a request to its reply. It must be a pure function of the request so that PiG and Pi receive the same answers.
type Script func(r *Request) Reply

// EventKind labels a provider log entry.
type EventKind string

// Provider log entry kinds.
const (
	EvRequest    EventKind = "request"
	EvGate       EventKind = "gate"
	EvRelease    EventKind = "release"
	EvDisconnect EventKind = "disconnect"
	EvDone       EventKind = "done"
)

// Event is one entry of the provider's event log.
type Event struct {
	At   time.Time
	Kind EventKind
	N    int
	Name string
}

// Provider is a scripted OpenAI-compatible endpoint.
type Provider struct {
	srv    *http.Server
	ln     net.Listener
	script Script
	// OnRequest runs for every request before it is answered, so a checker sees the exact wire transcript.
	OnRequest func(*Request) error

	mu       sync.Mutex
	reqs     []*Request
	events   []Event
	gates    map[string]*gate
	notify   chan struct{}
	failures []string
	injected []Reply
}

type gate struct {
	reached chan struct{}
	release chan struct{}
	once    sync.Once
	rel     sync.Once
}

// NewProvider starts a scripted provider on a loopback port. Close it with Close.
func NewProvider(script Script) (*Provider, error) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return nil, err
	}
	p := &Provider{ln: ln, script: script, gates: map[string]*gate{}, notify: make(chan struct{})}
	mux := http.NewServeMux()
	mux.HandleFunc("/", p.handle)
	p.srv = &http.Server{Handler: mux, ReadHeaderTimeout: 30 * time.Second}
	go func() { _ = p.srv.Serve(ln) }()
	return p, nil
}

// URL is the base URL a model's baseUrl points at.
func (p *Provider) URL() string { return "http://" + p.ln.Addr().String() + "/v1" }

// Close stops the server and releases every open gate.
func (p *Provider) Close() {
	p.mu.Lock()
	for _, g := range p.gates {
		g.rel.Do(func() { close(g.release) })
	}
	p.mu.Unlock()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	_ = p.srv.Shutdown(ctx)
	_ = p.srv.Close()
}

func (p *Provider) gateFor(name string) *gate {
	p.mu.Lock()
	defer p.mu.Unlock()
	g := p.gates[name]
	if g == nil {
		g = &gate{reached: make(chan struct{}), release: make(chan struct{})}
		p.gates[name] = g
	}
	return g
}

func (p *Provider) log(kind EventKind, n int, name string) {
	p.mu.Lock()
	p.events = append(p.events, Event{At: time.Now(), Kind: kind, N: n, Name: name})
	old := p.notify
	p.notify = make(chan struct{})
	p.mu.Unlock()
	close(old)
}

// Changed returns a channel closed on the next provider event; take it before checking state to avoid a lost wakeup.
func (p *Provider) Changed() <-chan struct{} {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.notify
}

// Requests returns the requests received so far.
func (p *Provider) Requests() []*Request {
	p.mu.Lock()
	defer p.mu.Unlock()
	return append([]*Request(nil), p.reqs...)
}

// Events returns the provider's event log.
func (p *Provider) Events() []Event {
	p.mu.Lock()
	defer p.mu.Unlock()
	return append([]Event(nil), p.events...)
}

// Count returns how many events of kind were logged.
func (p *Provider) Count(kind EventKind) int {
	n := 0
	for _, e := range p.Events() {
		if e.Kind == kind {
			n++
		}
	}
	return n
}

// Failures returns checker failures recorded for requests.
func (p *Provider) Failures() []string {
	p.mu.Lock()
	defer p.mu.Unlock()
	return append([]string(nil), p.failures...)
}

// Reached returns a channel closed once a stream reached the named gate.
func (p *Provider) Reached(name string) <-chan struct{} { return p.gateFor(name).reached }

// FailNext makes the next n requests fail with the given status and body before the script is consulted. The requests are still logged, so they count toward Idle and the request comparison.
func (p *Provider) FailNext(n, status int, body string) {
	p.mu.Lock()
	defer p.mu.Unlock()
	for range n {
		p.injected = append(p.injected, Reply{Status: status, Body: body})
	}
}

// Release opens the named gate; it may be called before the stream reaches it.
func (p *Provider) Release(name string) {
	g := p.gateFor(name)
	g.rel.Do(func() { close(g.release) })
	p.log(EvRelease, 0, name)
}

func parseRequest(n int, raw []byte) (*Request, error) {
	var body struct {
		Model    string `json:"model"`
		Messages []struct {
			Role       string          `json:"role"`
			Content    json.RawMessage `json:"content"`
			ToolCallID string          `json:"tool_call_id"`
			ToolCalls  []struct {
				ID       string `json:"id"`
				Function struct {
					Name      string `json:"name"`
					Arguments string `json:"arguments"`
				} `json:"function"`
			} `json:"tool_calls"`
		} `json:"messages"`
		Tools []struct {
			Function struct {
				Name string `json:"name"`
			} `json:"function"`
		} `json:"tools"`
	}
	if err := json.Unmarshal(raw, &body); err != nil {
		return nil, err
	}
	r := &Request{N: n, Raw: raw, Model: body.Model}
	for _, t := range body.Tools {
		r.Tools = append(r.Tools, t.Function.Name)
	}
	for _, m := range body.Messages {
		msg := Message{Role: m.Role, ToolCallID: m.ToolCallID}
		var s string
		if json.Unmarshal(m.Content, &s) == nil {
			msg.Text = s
		} else {
			var blocks []struct {
				Type     string `json:"type"`
				Text     string `json:"text"`
				ImageURL struct {
					URL string `json:"url"`
				} `json:"image_url"`
			}
			if json.Unmarshal(m.Content, &blocks) == nil {
				var b strings.Builder
				for _, bl := range blocks {
					b.WriteString(bl.Text)
					if bl.Type == "image_url" {
						msg.ImageURLs = append(msg.ImageURLs, bl.ImageURL.URL)
					}
				}
				msg.Text = b.String()
			}
		}
		for _, c := range m.ToolCalls {
			msg.ToolCalls = append(msg.ToolCalls, WireCall{ID: c.ID, Name: c.Function.Name, Arguments: c.Function.Arguments})
		}
		r.Messages = append(r.Messages, msg)
	}
	return r, nil
}

func (p *Provider) handle(w http.ResponseWriter, hr *http.Request) {
	if hr.Method != http.MethodPost || !strings.HasSuffix(hr.URL.Path, "/chat/completions") {
		http.NotFound(w, hr)
		return
	}
	raw, err := io.ReadAll(hr.Body)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	p.mu.Lock()
	n := len(p.reqs) + 1
	p.mu.Unlock()
	req, err := parseRequest(n, raw)
	if err != nil {
		http.Error(w, "unparseable request: "+err.Error(), http.StatusBadRequest)
		return
	}
	p.mu.Lock()
	p.reqs = append(p.reqs, req)
	p.mu.Unlock()
	if p.OnRequest != nil {
		if err := p.OnRequest(req); err != nil {
			p.mu.Lock()
			p.failures = append(p.failures, fmt.Sprintf("request %d: %v", n, err))
			p.mu.Unlock()
		}
	}
	p.log(EvRequest, n, "")
	var reply Reply
	p.mu.Lock()
	injected := len(p.injected) > 0
	if injected {
		reply, p.injected = p.injected[0], p.injected[1:]
	}
	p.mu.Unlock()
	if !injected {
		reply = p.script(req)
	}
	if reply.Status != 0 {
		w.Header().Set("content-type", "application/json")
		w.WriteHeader(reply.Status)
		_, _ = io.WriteString(w, reply.Body)
		p.log(EvDone, n, "")
		return
	}
	w.Header().Set("content-type", "text/event-stream")
	w.Header().Set("cache-control", "no-cache")
	w.WriteHeader(http.StatusOK)
	fl, _ := w.(http.Flusher)
	emit := func(data any) {
		var b bytes.Buffer
		if s, ok := data.(string); ok {
			fmt.Fprintf(&b, "data: %s\n\n", s)
		} else {
			enc, _ := json.Marshal(data)
			fmt.Fprintf(&b, "data: %s\n\n", enc)
		}
		_, _ = w.Write(b.Bytes())
		if fl != nil {
			fl.Flush()
		}
	}
	chunk := func(delta map[string]any, finish any) map[string]any {
		return map[string]any{"id": fmt.Sprintf("chatcmpl-j%d", n), "object": "chat.completion.chunk", "created": 1700000000, "model": "strict", "choices": []any{map[string]any{"index": 0, "delta": delta, "finish_reason": finish}}}
	}
	emit(chunk(map[string]any{"role": "assistant", "content": ""}, nil))
	calls := 0
	for _, part := range reply.Parts {
		switch part.kind {
		case partText:
			for _, piece := range pieces(part.text, 24) {
				emit(chunk(map[string]any{"content": piece}, nil))
			}
		case partReasoning:
			for _, piece := range pieces(part.text, 24) {
				emit(chunk(map[string]any{"reasoning_content": piece}, nil))
			}
		case partCall:
			args := part.call.Args
			var enc string
			if s, ok := args.(string); ok {
				enc = s
			} else {
				b, _ := json.Marshal(args)
				enc = string(b)
			}
			id := fmt.Sprintf("call_j%d_%d", n, calls)
			emit(chunk(map[string]any{"tool_calls": []any{map[string]any{"index": calls, "id": id, "type": "function", "function": map[string]any{"name": part.call.Name, "arguments": ""}}}}, nil))
			for _, piece := range pieces(enc, 16) {
				emit(chunk(map[string]any{"tool_calls": []any{map[string]any{"index": calls, "function": map[string]any{"arguments": piece}}}}, nil))
			}
			calls++
		case partGate:
			g := p.gateFor(part.gate)
			g.once.Do(func() { close(g.reached) })
			p.log(EvGate, n, part.gate)
			select {
			case <-g.release:
			case <-hr.Context().Done():
				p.log(EvDisconnect, n, part.gate)
				return
			}
		}
	}
	finish := "stop"
	if calls > 0 {
		finish = "tool_calls"
	}
	if reply.Length {
		finish = "length"
	}
	last := chunk(map[string]any{}, finish)
	usage := reply.Usage
	if usage == nil {
		usage = &Usage{Prompt: 10 + len(raw)/64, Completion: 3}
	}
	last["usage"] = map[string]int{"prompt_tokens": usage.Prompt, "completion_tokens": usage.Completion, "total_tokens": usage.Prompt + usage.Completion}
	emit(last)
	emit("[DONE]")
	select {
	case <-hr.Context().Done():
		p.log(EvDisconnect, n, "")
	default:
		p.log(EvDone, n, "")
	}
}

// pieces splits s into chunks of at most size runes.
func pieces(s string, size int) []string {
	var out []string
	for len(s) > 0 {
		n := 0
		cut := 0
		for cut < len(s) && n < size {
			_, w := utf8.DecodeRuneInString(s[cut:])
			cut += w
			n++
		}
		out = append(out, s[:cut])
		s = s[cut:]
	}
	if len(out) == 0 {
		out = []string{""}
	}
	return out
}

// WithUsage sets the token usage the reply reports, which drives the footer's context percentage and Pi's auto-compaction threshold.
func (r Reply) WithUsage(prompt, completion int) Reply {
	r.Usage = &Usage{Prompt: prompt, Completion: completion}
	return r
}
