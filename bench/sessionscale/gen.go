// SPDX-License-Identifier: MIT

// Package sessionscale writes synthetic long-lived Pi session files and the agent directory that resumes them, so the memory and per-prompt cost of a resumed session can be measured on Pi and PiG with the same bytes.
//
// The default shape is the one reported for a five-day session (owner, 2026-10-10): 299 MB, 46,100 entries, of which 25,171 tool results (130 MB) and 20,138 assistant messages (166 MB), 700 user messages, 63 compactions, 17 leaves and a longest line of 1.06 MB. Only those shape statistics are used; the content is pseudo-random prose, code and base64 from a fixed seed, so a shape and seed always produce the same bytes.
//
// Tokens are estimated as Pi's compaction.ts estimateTokens does: a quarter of the characters of message text, thinking, tool-call names and JSON arguments, and tool-result text, rounded up. Signatures and tool-result details are on disk but not in the estimate.
package sessionscale

import (
	"bufio"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"maps"
	"math"
	"math/rand/v2"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"
)

// Owner shape statistics (2026-10-10 report). Only counts and sizes; no content.
const (
	OwnerBytes           = 299 << 20
	OwnerEntries         = 46100
	OwnerToolResults     = 25171
	OwnerToolResultBytes = 130 << 20
	OwnerAssistants      = 20138
	OwnerAssistantBytes  = 166 << 20
	OwnerUsers           = 700
	OwnerCompactions     = 63
	OwnerLeaves          = 17
	OwnerMaxLine         = 1_060_000
)

// Real-session profile statistics (handoffs/P1-real-session.md, 2026-10-11): the owner's session as profiled for open and /tree. Only counts; no content.
const (
	P1Bytes       = 314_175_691
	P1Users       = 706
	P1Assistants  = 20_305
	P1ToolResults = 25_339
	P1Systems     = 2
	P1Compactions = 63
	// P1CompactionBytes is the size of the profiled compaction records.
	P1CompactionBytes = 13 << 20
	P1ModelChanges    = 19
	P1CustomMessages  = 3
	P1ThinkingLevels  = 2
	P1BranchPoints    = 15
	// P1TreeRows is the default-filter /tree row count; the assistant messages that are not rows carry no text.
	P1TreeRows = 28_442
	// P1MissEvery is one prompt-cache miss per this many assistant responses.
	P1MissEvery = 40
)

// ProfileP1 selects the real-session profile.
const ProfileP1 = "p1"

// Provider and Model name the scripted model the agent directory declares and the assistant messages carry.
const (
	Provider = "sessionscale"
	Model    = "scripted"
	// ModelB is the second model a ProfileP1 session switches to; its cache-read price differs from Model's.
	ModelB = "scripted-b"
)

// Shape selects a fixture.
type Shape struct {
	// Bytes is the file size to reach. Generation stops at the first turn boundary at or above it, then writes the active tail.
	Bytes int64
	// Compactions and Leaves are the counts for the whole file. Zero scales the owner's counts by Bytes.
	Compactions int
	Leaves      int
	// ContextTokens is the estimated size of the active context the file resumes to: the last compaction's summary, its kept messages and everything after it. Zero means 150,000.
	ContextTokens int
	// KeepRecentTokens is how much each compaction keeps (Pi's compaction.keepRecentTokens default, 20,000, when zero).
	KeepRecentTokens int
	// Window is the scripted model's context window. Zero means the smallest of 272,000 and ContextTokens*100/85 rounded up to a multiple of 1,000 that is at least ContextTokens + 32,768.
	Window int
	// Seed selects the content.
	Seed uint64
	// Cwd is the header's working directory.
	Cwd string
	// Profile "" is the 2026-10-10 report's shape. ProfileP1 is the real-session profile: its entry mix, model changes, priced
	// prompt caching with misses, system and custom messages, and assistant messages without text that the /tree default filter hides.
	Profile string
}

// Meta describes a written fixture.
type Meta struct {
	Shape            Shape            `json:"shape"`
	Bytes            int64            `json:"bytes"`
	Entries          int              `json:"entries"`
	ByType           map[string]int   `json:"byType"`
	BytesByType      map[string]int64 `json:"bytesByType"`
	MaxLine          int              `json:"maxLine"`
	Compactions      int              `json:"compactions"`
	Leaves           int              `json:"leaves"`
	PathEntries      int              `json:"pathEntries"`
	ContextEntries   int              `json:"contextEntries"`
	ContextTokens    int              `json:"contextTokens"`
	ContextBytes     int64            `json:"contextBytes"`
	Window           int              `json:"window"`
	SHA256           string           `json:"sha256"`
	Session          string           `json:"session,omitempty"`
	AgentDir         string           `json:"agentDir,omitempty"`
	LeafID           string           `json:"leafId"`
	LastCompactionID string           `json:"lastCompactionId"`
}

// Normalize fills the zero fields of a shape.
func (s Shape) Normalize() Shape {
	if s.Bytes <= 0 {
		s.Bytes = OwnerBytes
		if s.Profile == ProfileP1 {
			s.Bytes = P1Bytes
		}
	}
	if s.Profile == ProfileP1 && s.Leaves == 0 {
		s.Leaves = max(1, int(math.Round((P1BranchPoints+1)*float64(s.Bytes)/P1Bytes)))
	}
	scale := float64(s.Bytes) / OwnerBytes
	if s.Compactions == 0 {
		s.Compactions = max(1, int(math.Round(OwnerCompactions*scale)))
	}
	if s.Leaves == 0 {
		s.Leaves = max(1, int(math.Round(OwnerLeaves*scale)))
	}
	if s.ContextTokens == 0 {
		s.ContextTokens = 150_000
	}
	if s.KeepRecentTokens == 0 {
		s.KeepRecentTokens = 20_000
	}
	if s.Window == 0 {
		w := (s.ContextTokens*100/85 + 999) / 1000 * 1000
		w = max(w, s.ContextTokens+32_768)
		if s.ContextTokens <= 200_000 {
			w = max(w, 272_000)
		}
		s.Window = w
	}
	if s.Cwd == "" {
		s.Cwd = "/sessionscale"
	}
	return s
}

// Write creates dir/session.jsonl, dir/agent/models.json, dir/agent/settings.json, dir/meta.json and dir/work. baseURL is the scripted model's OpenAI-compatible endpoint.
func Write(dir string, shape Shape, baseURL string) (Meta, error) {
	shape = shape.Normalize()
	work, err := filepath.Abs(filepath.Join(dir, "work"))
	if err != nil {
		return Meta{}, err
	}
	if shape.Cwd == "/sessionscale" {
		shape.Cwd = work
	}
	agentDir := filepath.Join(dir, "agent")
	for _, d := range []string{agentDir, work} {
		if err := os.MkdirAll(d, 0o700); err != nil {
			return Meta{}, err
		}
	}
	if err := writeAgentDir(agentDir, shape.Window, baseURL, shape.Profile == ProfileP1); err != nil {
		return Meta{}, err
	}
	path := filepath.Join(dir, "session.jsonl")
	f, err := os.Create(path)
	if err != nil {
		return Meta{}, err
	}
	meta, err := Generate(f, shape)
	if cerr := f.Close(); err == nil {
		err = cerr
	}
	if err != nil {
		return Meta{}, err
	}
	meta.Session, meta.AgentDir = path, agentDir
	//portlint:allow jsonescape benchmark tool output, not Pi-facing
	data, err := json.MarshalIndent(meta, "", "  ")
	if err != nil {
		return Meta{}, err
	}
	return meta, os.WriteFile(filepath.Join(dir, "meta.json"), append(data, '\n'), 0o600)
}

// WriteAgentDir writes models.json declaring the scripted model at baseURL with the window, and settings.json selecting it.
func WriteAgentDir(agentDir string, window int, baseURL string) error {
	return writeAgentDir(agentDir, window, baseURL, false)
}

// writeAgentDir is WriteAgentDir; priced declares ModelB as well and gives both models the prices p1Prices names, so a resumed session reprices its cache misses with them.
func writeAgentDir(agentDir string, window int, baseURL string, priced bool) error {
	if baseURL == "" {
		baseURL = "http://127.0.0.1:9/v1"
	}
	model := func(id string, cost map[string]float64) map[string]any {
		return map[string]any{
			"id": id, "name": "Session scale scripted model " + id, "reasoning": false, "input": []string{"text"},
			"contextWindow": window, "maxTokens": 4096, "cost": cost,
		}
	}
	declared := []any{model(Model, map[string]float64{"input": 0, "output": 0, "cacheRead": 0, "cacheWrite": 0})}
	if priced {
		declared = []any{model(Model, p1Prices[Model]), model(ModelB, p1Prices[ModelB])}
	}
	models := map[string]any{"providers": map[string]any{Provider: map[string]any{
		"baseUrl": baseURL,
		"api":     "openai-completions",
		"apiKey":  "sessionscale-key",
		"models":  declared,
	}}}
	settings := map[string]any{"defaultProvider": Provider, "defaultModel": Model, "quietStartup": true}
	for name, value := range map[string]any{"models.json": models, "settings.json": settings} {
		//portlint:allow jsonescape benchmark tool output, not Pi-facing
		data, err := json.MarshalIndent(value, "", "  ")
		if err != nil {
			return err
		}
		if err := os.WriteFile(filepath.Join(agentDir, name), append(data, '\n'), 0o600); err != nil {
			return err
		}
	}
	return nil
}

// p1Prices are the ProfileP1 models' prices in dollars per million tokens, in models.json's cost shape.
var p1Prices = map[string]map[string]float64{
	Model:  {"input": 3, "output": 15, "cacheRead": 0.3, "cacheWrite": 3.75},
	ModelB: {"input": 5, "output": 25, "cacheRead": 0.5, "cacheWrite": 6.25},
}

// Generate writes the session file for shape to w.
func Generate(w io.Writer, shape Shape) (Meta, error) {
	shape = shape.Normalize()
	hash := sha256.New()
	g := &generator{
		shape:  shape,
		out:    bufio.NewWriterSize(io.MultiWriter(w, hash), 1<<20),
		rng:    rand.New(rand.NewPCG(shape.Seed, 0x7365737373636c65)),
		clock:  time.Date(2026, 10, 4, 12, 0, 0, 0, time.UTC),
		ids:    map[string]struct{}{},
		byType: map[string]int{}, bytesByType: map[string]int64{},
	}
	g.run()
	if g.err == nil {
		g.err = g.out.Flush()
	}
	meta := Meta{
		Shape: shape, Bytes: g.bytes, Entries: g.entries, ByType: g.byType, BytesByType: g.bytesByType, MaxLine: g.maxLine,
		Compactions: g.compactions, Leaves: g.leaves + 1, PathEntries: len(g.path), Window: shape.Window,
		SHA256: hex.EncodeToString(hash.Sum(nil)), LeafID: g.parent, LastCompactionID: g.lastCompaction,
	}
	meta.ContextEntries, meta.ContextTokens, meta.ContextBytes = g.context()
	return meta, g.err
}

type pathEntry struct {
	id       string
	kind     string // user, assistant, toolResult, compaction, branch_summary, other
	tokens   int
	bytes    int
	keptFrom int // for a compaction: the path index of its first kept entry
}

type generator struct {
	shape  Shape
	out    *bufio.Writer
	rng    *rand.Rand
	clock  time.Time
	ids    map[string]struct{}
	parent string
	path   []pathEntry

	entries, compactions, leaves, maxLine int
	bytes                                 int64
	byType                                map[string]int
	bytesByType                           map[string]int64
	lastCompaction                        string
	maxPlaced                             bool
	turn                                  int
	tail                                  bool
	err                                   error

	// ProfileP1 state: the current model, the previous response's prompt tokens, and the counts still to place.
	model                         string
	prevPrompt                    int
	assistants                    int
	modelChanges, customs, system int
}

func (g *generator) run() {
	g.line("session", map[string]any{"type": "session", "version": 3, "id": fmt.Sprintf("019a0000-5ca1-7000-8000-%012x", g.shape.Seed), "timestamp": g.stamp(), "cwd": g.shape.Cwd})
	g.model = Model
	g.entry("model_change", map[string]any{"provider": Provider, "modelId": Model}, "other", 0)
	g.entry("thinking_level_change", map[string]any{"thinkingLevel": "medium"}, "other", 0)
	p1 := g.shape.Profile == ProfileP1
	if p1 {
		g.modelChanges = 1
		for g.system < P1Systems {
			g.system++
			text := fmt.Sprintf("System notice %d: %s", g.system, g.prose(400))
			g.message("system", map[string]any{"role": "system", "content": text}, est(len(text)))
		}
	}

	// The history before the active tail fills Bytes less the tail's expected size; compactions and branches are spread evenly over it.
	tailBytes := int64(g.shape.ContextTokens) * 4 * 5 / 2
	historyBytes := max(g.shape.Bytes-tailBytes, g.shape.Bytes/2)
	// The last compaction opens the active tail, so the history holds the others.
	compactionEvery := historyBytes / int64(g.shape.Compactions)
	branchEvery := historyBytes / int64(g.shape.Leaves)
	nextCompaction, nextBranch := compactionEvery, branchEvery
	labels := 0
	for g.err == nil && g.bytes < historyBytes {
		if g.compactions < g.shape.Compactions-1 && g.bytes >= nextCompaction {
			g.compact()
			nextCompaction += compactionEvery
		}
		if g.leaves < g.shape.Leaves-1 && g.bytes >= nextBranch && g.branch() {
			nextBranch += branchEvery
		}
		if p1 {
			g.p1Events(historyBytes)
		}
		g.userTurn()
		if g.turn == 3 {
			g.entry("session_info", map[string]any{"name": "session scale fixture"}, "other", 0)
		}
		if labels < 3 && g.turn%97 == 50 {
			target := g.path[g.rng.IntN(len(g.path))].id
			g.entry("label", map[string]any{"targetId": target, "label": fmt.Sprintf("checkpoint %d", labels+1)}, "other", 0)
			labels++
		}
	}
	if p1 {
		g.p1Events(0)
	}
	for g.compactions < g.shape.Compactions && g.err == nil {
		before := g.compactions
		g.compact()
		if g.compactions == before {
			g.userTurn()
		}
	}
	// The active tail: turns until the active context reaches ContextTokens; the last turn ends early with its final answer.
	g.tail = true
	for g.err == nil {
		_, tokens, _ := g.context()
		if tokens >= g.shape.ContextTokens {
			break
		}
		g.userTurn()
	}
}

// p1Events places ProfileP1's model changes, custom messages and second thinking-level change evenly over historyBytes; zero places what is left.
func (g *generator) p1Events(historyBytes int64) {
	due := func(placed, total int) bool {
		return historyBytes == 0 || g.bytes >= historyBytes*int64(placed)/int64(total)
	}
	for g.modelChanges < P1ModelChanges && due(g.modelChanges, P1ModelChanges) && g.err == nil {
		g.modelChanges++
		g.model = Model
		if g.modelChanges%2 == 0 {
			g.model = ModelB
		}
		g.entry("model_change", map[string]any{"provider": Provider, "modelId": g.model}, "other", 0)
		// A new model starts with a cold cache.
		g.prevPrompt = 0
	}
	for g.customs < P1CustomMessages && due(g.customs+1, P1CustomMessages+1) && g.err == nil {
		g.customs++
		text := fmt.Sprintf("Extension note %d: %s", g.customs, g.prose(300))
		g.entry("custom_message", map[string]any{"customType": "sessionscale-note", "content": text, "display": true}, "custom", est(len(text)))
		if g.customs == 2 {
			g.entry("thinking_level_change", map[string]any{"thinkingLevel": "high"}, "other", 0)
		}
	}
}

// context returns the entries, estimated tokens and bytes of the active context: the last compaction on the path, its kept entries and everything after it.
func (g *generator) context() (entries, tokens int, bytes int64) {
	start := 0
	for i, v := range slices.Backward(g.path) {
		if v.kind == "compaction" {
			start = v.keptFrom
			tokens += v.tokens
			bytes += int64(v.bytes)
			entries++
			for j := start; j < i; j++ {
				tokens += g.path[j].tokens
				bytes += int64(g.path[j].bytes)
				entries++
			}
			start = i + 1
			break
		}
	}
	for _, e := range g.path[start:] {
		tokens += e.tokens
		bytes += int64(e.bytes)
		entries++
	}
	return entries, tokens, bytes
}

func (g *generator) stamp() string {
	g.clock = g.clock.Add(time.Duration(800+g.rng.IntN(9000)) * time.Millisecond)
	return g.clock.Format("2006-01-02T15:04:05.000Z")
}

func (g *generator) newID() string {
	for {
		var b [4]byte
		binary := g.rng.Uint32()
		b[0], b[1], b[2], b[3] = byte(binary>>24), byte(binary>>16), byte(binary>>8), byte(binary)
		id := hex.EncodeToString(b[:])
		if _, dup := g.ids[id]; !dup {
			g.ids[id] = struct{}{}
			return id
		}
	}
}

func (g *generator) line(kind string, v map[string]any) int {
	if g.err != nil {
		return 0
	}
	//portlint:allow mapkeyorder synthetic session fixture lines for a Go-only bench; session readers look members up by name
	data, err := json.Marshal(v)
	if err != nil {
		g.err = err
		return 0
	}
	data = append(data, '\n')
	if _, err := g.out.Write(data); err != nil {
		g.err = err
		return 0
	}
	g.bytes += int64(len(data))
	g.maxLine = max(g.maxLine, len(data)-1)
	g.byType[kind]++
	g.bytesByType[kind] += int64(len(data))
	return len(data)
}

// entry appends a non-message entry as a child of the leaf.
func (g *generator) entry(typ string, fields map[string]any, kind string, tokens int) string {
	id := g.newID()
	v := map[string]any{"type": typ, "id": id, "parentId": nullable(g.parent), "timestamp": g.stamp()}
	maps.Copy(v, fields)
	n := g.line(typ, v)
	g.entries++
	if typ != "label" && typ != "session_info" {
		g.path = append(g.path, pathEntry{id: id, kind: kind, tokens: tokens, bytes: n})
		g.parent = id
	} else {
		g.path = append(g.path, pathEntry{id: id, kind: "other", bytes: n})
		g.parent = id
	}
	return id
}

func nullable(id string) any {
	if id == "" {
		return nil
	}
	return id
}

func (g *generator) message(role string, message map[string]any, tokens int) string {
	id := g.newID()
	stamp := g.stamp()
	message["timestamp"] = g.clock.UnixMilli()
	n := g.line(role, map[string]any{"type": "message", "id": id, "parentId": nullable(g.parent), "timestamp": stamp, "message": message})
	g.entries++
	g.path = append(g.path, pathEntry{id: id, kind: role, tokens: tokens, bytes: n})
	g.parent = id
	return id
}

// lognormal draws a size whose mean is mean, with a heavy right tail, capped at limit.
func (g *generator) lognormal(mean float64, sigma float64, limit int) int {
	mu := math.Log(mean) - sigma*sigma/2
	v := math.Exp(mu + sigma*g.rng.NormFloat64()) //portlint:allow fusedmuladd synthetic benchmark sizes from Go's own RNG: no Pi counterpart
	return max(16, min(int(v), limit))
}

// userTurn writes a user prompt, then rounds of an assistant tool call and its results, then a final assistant answer. The owner's mix is 28.8 assistant messages and 36 tool results per user message.
func (g *generator) userTurn() {
	g.turn++
	prompt := fmt.Sprintf("Task %d: %s", g.turn, g.prose(g.lognormal(600, 1.0, 20_000)))
	g.message("user", map[string]any{"role": "user", "content": []any{map[string]any{"type": "text", "text": prompt}}}, est(len(prompt)))
	rounds := max(1, int(g.rng.ExpFloat64()*float64(OwnerAssistants)/OwnerUsers))
	for r := 0; r < rounds && g.err == nil; r++ {
		if g.tail {
			if _, tokens, _ := g.context(); tokens >= g.shape.ContextTokens {
				break
			}
		}
		calls := 1
		if g.rng.Float64() < 0.2 {
			calls = 2 + g.rng.IntN(2)
		}
		g.assistantRound(calls)
	}
	g.assistantRound(0)
}

// assistantEnvelope is the mean size of an assistant line outside its content blocks (ids, timestamps, usage, api, provider and model) and of the JSON escapes inside them.
const assistantEnvelope = 600

var tools = []string{"read", "read", "read", "bash", "bash", "edit", "edit", "grep", "write", "find"}

// assistantRound writes one assistant message with calls tool calls and, for each, a tool result.
func (g *generator) assistantRound(calls int) {
	// Mean assistant line: OwnerAssistantBytes / OwnerAssistants (8.6 KB): a signature blob (45%), text (8%), and thinking and tool-call arguments (the rest, 45%).
	// Arguments are drawn first; thinking fills what the calls leave, so the line reaches the drawn size whatever tools were called.
	size := g.lognormal(float64(OwnerAssistantBytes)/OwnerAssistants-assistantEnvelope, 0.9, 400_000)
	sig := size * 45 / 100
	text := g.prose(size * 8 / 100)
	p1 := g.shape.Profile == ProfileP1
	// In ProfileP1 a tool-calling response carries text only as often as the profile's /tree rows require: the rows are every
	// entry but the assistant messages without text.
	if p1 && calls > 0 && g.rng.Float64() >= p1TextShare {
		text = ""
	}
	type call struct {
		id, name string
		args     map[string]any
	}
	var made []call
	argBytes := size * 30 / 100
	// chars counts what Pi's compaction.ts estimateTokens counts for an assistant message: text, thinking, and each call's name and JSON arguments.
	chars, argChars := len(text), 0
	for i := range calls {
		name := tools[g.rng.IntN(len(tools))]
		id := fmt.Sprintf("call_%s%d", g.blob(18)[:18], i)
		var args map[string]any
		switch name {
		case "edit":
			args = map[string]any{"path": g.pathName(), "edits": []any{map[string]any{"oldText": g.code(argBytes / 2), "newText": g.code(argBytes / 2)}}}
		case "write":
			args = map[string]any{"path": g.pathName(), "content": g.code(argBytes)}
		case "bash":
			args = map[string]any{"command": "go test ./" + g.pathName() + " -run " + g.word()}
		case "grep":
			args = map[string]any{"pattern": g.word(), "path": "."}
		case "find":
			args = map[string]any{"pattern": "*.go"}
		default:
			args = map[string]any{"path": g.pathName()}
		}
		//portlint:allow mapkeyorder only len(raw) is used, and member order cannot change the length
		raw, _ := json.Marshal(args)
		chars += len(name) + len(raw)
		argChars += len(raw)
		made = append(made, call{id, name, args})
	}
	thinking := g.prose(max(size*15/100, size*45/100-argChars))
	chars += len(thinking)
	tokens := est(chars)
	content := []any{map[string]any{"type": "thinking", "thinking": thinking, "thinkingSignature": g.blob(sig)}}
	if text != "" || !p1 {
		content = append(content, map[string]any{"type": "text", "text": text})
	}
	for _, c := range made {
		content = append(content, map[string]any{"type": "toolCall", "id": c.id, "name": c.name, "arguments": c.args})
	}
	_, ctx, _ := g.context()
	stop := "stop"
	if calls > 0 {
		stop = "toolUse"
	}
	model := Model
	usage := map[string]any{
		"input": ctx, "output": tokens, "cacheRead": 0, "cacheWrite": 0, "totalTokens": ctx + tokens,
		"cost": map[string]float64{"input": 0, "output": 0, "cacheRead": 0, "cacheWrite": 0, "total": 0},
	}
	if p1 {
		model = g.model
		usage = g.p1Usage(ctx, tokens)
		g.assistants++
	}
	g.message("assistant", map[string]any{
		"role": "assistant", "content": content, "api": "openai-completions", "provider": Provider, "model": model,
		"usage":      usage,
		"stopReason": stop,
	}, tokens)
	for _, c := range made {
		g.toolResult(c.id, c.name)
	}
}

// p1TextShare is the share of tool-calling responses that carry text, so that about P1TreeRows of the profile's entries are /tree rows.
const p1TextShare = 0.08

// p1Usage is a priced response usage with prompt caching: the prompt the previous response sent is read from cache, the rest is written to it,
// except that every P1MissEvery-th response (and the first after a model change) reads nothing, a cache miss PiG prices with the model's cache-read price.
func (g *generator) p1Usage(prompt, output int) map[string]any {
	prices := p1Prices[g.model]
	read := min(g.prevPrompt, prompt)
	if g.assistants%P1MissEvery == P1MissEvery-1 {
		read = 0
	}
	write := prompt - read
	input := min(write, 2+g.rng.IntN(40))
	write -= input
	g.prevPrompt = prompt
	cost := map[string]float64{
		"input":      float64(input) * prices["input"] / 1e6,
		"output":     float64(output) * prices["output"] / 1e6,
		"cacheRead":  float64(read) * prices["cacheRead"] / 1e6,
		"cacheWrite": float64(write) * prices["cacheWrite"] / 1e6,
	}
	cost["total"] = cost["input"] + cost["output"] + cost["cacheRead"] + cost["cacheWrite"]
	return map[string]any{"input": input, "output": output, "cacheRead": read, "cacheWrite": write, "totalTokens": input + output + read + write, "cost": cost}
}

func (g *generator) toolResult(callID, name string) {
	// Mean tool-result line: OwnerToolResultBytes / OwnerToolResults (5.4 KB). One result in the history is the owner's longest line.
	size := g.lognormal(float64(OwnerToolResultBytes)/OwnerToolResults, 1.2, 600_000)
	if !g.maxPlaced && g.bytes > g.shape.Bytes/3 {
		size, name, g.maxPlaced = OwnerMaxLine*94/100, "bash", true
	}
	msg := map[string]any{"role": "toolResult", "toolCallId": callID, "toolName": name, "isError": g.rng.IntN(40) == 0}
	var text string
	switch name {
	case "edit":
		text = "Successfully replaced 1 block(s) in " + g.pathName() + "."
		msg["details"] = map[string]any{"diff": g.code(size), "firstChangedLine": 1 + g.rng.IntN(800)}
	case "write":
		text = fmt.Sprintf("Successfully wrote %d bytes to %s", size, g.pathName())
	case "bash":
		text = g.code(size)
		if size > 50_000 {
			msg["details"] = map[string]any{"truncation": map[string]any{"truncated": true, "truncatedBy": "bytes", "totalLines": size / 40, "totalBytes": size, "outputLines": 2000, "outputBytes": 51200}, "fullOutputPath": "/tmp/pi-bash-" + g.blob(8)[:8] + ".log"}
		}
	default:
		text = g.code(size)
	}
	msg["content"] = []any{map[string]any{"type": "text", "text": text}}
	g.message("toolResult", msg, est(len(text)))
}

// compact appends a compaction that keeps about KeepRecentTokens of the path, cut before a user or assistant message.
func (g *generator) compact() {
	start := 0
	for i, v := range slices.Backward(g.path) {
		if v.kind == "compaction" {
			start = i + 1
			break
		}
	}
	keep, kept := 0, len(g.path)
	for i := len(g.path) - 1; i > start; i-- {
		keep += g.path[i].tokens
		if (g.path[i].kind == "user" || g.path[i].kind == "assistant") && keep >= g.shape.KeepRecentTokens {
			kept = i
			break
		}
	}
	if kept == len(g.path) {
		return
	}
	_, before, _ := g.context()
	goal := g.lognormal(9000, 0.5, 40_000)
	if g.shape.Profile == ProfileP1 {
		// The profiled compactions hold P1CompactionBytes in all.
		goal = g.lognormal(P1CompactionBytes/P1Compactions-4000, 0.5, 1_000_000)
	}
	summary := "## Goal\n" + g.prose(goal) + "\n\n## Progress\n" + g.prose(4000)
	id := g.entry("compaction", map[string]any{"summary": summary, "firstKeptEntryId": g.path[kept].id, "tokensBefore": before}, "compaction", est(len(summary)))
	g.path[len(g.path)-1].keptFrom = kept
	g.lastCompaction = id
	g.compactions++
}

// branch abandons the current leaf: the leaf moves to the parent of a user message in the current segment, as /tree does, and half the time a branch summary records the abandoned branch.
func (g *generator) branch() bool {
	start := 0
	for i, v := range slices.Backward(g.path) {
		if v.kind == "compaction" {
			start = i + 1
			break
		}
	}
	var users []int
	for i := start; i < len(g.path); i++ {
		if g.path[i].kind == "user" && i > 0 {
			users = append(users, i)
		}
	}
	if len(users) < 2 {
		return false
	}
	target := users[len(users)/2+g.rng.IntN(len(users)-len(users)/2)]
	from := g.parent
	g.path = g.path[:target]
	g.parent = g.path[target-1].id
	g.leaves++
	if g.rng.IntN(2) == 0 {
		summary := "The user explored a branch that was abandoned. " + g.prose(g.lognormal(2500, 0.5, 20_000))
		g.entry("branch_summary", map[string]any{"fromId": from, "summary": summary}, "branch_summary", est(len(summary)))
	}
	return true
}

func est(chars int) int { return (chars + 3) / 4 }

var words = strings.Fields(`the request handler session buffer reads writes returns context window token model provider stream event
message tool result value error cache index entry state turn render line width cursor offset frame update queue retry
budget limit bound slice map key field parser encoder decoder writer reader channel lock owner caller callee branch`)

func (g *generator) word() string { return words[g.rng.IntN(len(words))] }

func (g *generator) pathName() string {
	return fmt.Sprintf("internal/%s/%s_%d.go", g.word(), g.word(), g.rng.IntN(500))
}

func (g *generator) prose(n int) string {
	var b strings.Builder
	b.Grow(n + 16)
	for b.Len() < n {
		if b.Len() > 0 {
			b.WriteByte(' ')
		}
		b.WriteString(g.word())
	}
	return b.String()
}

func (g *generator) code(n int) string {
	var b strings.Builder
	b.Grow(n + 128)
	for b.Len() < n {
		b.WriteString(strings.Repeat("\t", g.rng.IntN(3)))
		fmt.Fprintf(&b, "%s := %s.%s(%s, %d)\n", g.word(), g.word(), strings.ToUpper(g.word()[:1])+g.word(), g.word(), g.rng.IntN(1000))
	}
	return b.String()
}

// blob is n bytes of base64, like an encrypted reasoning signature.
func (g *generator) blob(n int) string {
	raw := make([]byte, n*3/4+3)
	for i := 0; i < len(raw); i += 8 {
		v := g.rng.Uint64()
		for j := 0; j < 8 && i+j < len(raw); j++ {
			raw[i+j] = byte(v >> (8 * j))
		}
	}
	s := base64.StdEncoding.EncodeToString(raw)
	return s[:min(n, len(s))]
}
