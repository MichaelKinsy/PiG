// SPDX-License-Identifier: MIT

// Package longsession writes a long Pi session fixture: a version 3 session file whose active context holds a chosen number of tokens, the agent directory that declares a scripted model with a chosen context window, and a metadata record. Pi, PiG and any harness that reads Pi session files open the same file, so memory measured with a loaded session compares like with like.
//
// Tokens are counted as Pi's compaction.ts estimateTokens counts them: a quarter of the characters of each message's text, tool-call name and JSON arguments, and tool-result text, rounded up. All text is ASCII, so characters equal UTF-16 code units. Each assistant message reports usage whose input is the context before it, so the footer and the context gauge read the size the file holds.
//
// A turn is a user prompt, an assistant message with a read tool call, the tool result (ResultBytes of source-like text), and an assistant answer. Content is pseudo-random from a fixed seed, so a size and seed always produce the same bytes.
package longsession

import (
	"bufio"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"hash"
	"io"
	"math/rand/v2"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// Provider and Model name the scripted model the fixture's agent directory declares and its assistant messages carry.
const (
	Provider = "longsession"
	Model    = "scripted"
)

// Options selects a fixture.
type Options struct {
	// Tokens is the context size to reach, by Pi's estimate. Generation stops at the first turn boundary at or above it.
	Tokens int
	// Window is the model's declared context window in tokens.
	Window int
	// ResultBytes is the size of each tool result. Zero means 16384.
	ResultBytes int
	// Seed selects the content. Sizes with the same seed share their leading turns.
	Seed uint64
	// Cwd is the session's working directory. Write uses dir/work and creates it; Generate uses /longsession when empty.
	Cwd string
	// BaseURL is the scripted model's OpenAI-compatible endpoint written to models.json. Empty means http://127.0.0.1:9/v1, which refuses connections.
	BaseURL string
}

// Meta describes a written fixture.
type Meta struct {
	Tokens      int    `json:"tokens"`
	Target      int    `json:"target"`
	Window      int    `json:"window"`
	Turns       int    `json:"turns"`
	Entries     int    `json:"entries"`
	Bytes       int64  `json:"bytes"`
	ResultBytes int    `json:"resultBytes"`
	Seed        uint64 `json:"seed"`
	SHA256      string `json:"sha256"`
	// ContentSHA256 hashes the entries without the header line, whose working directory differs per fixture directory: it identifies the conversation across machines.
	ContentSHA256 string `json:"contentSha256"`
	Cwd           string `json:"cwd"`
	Session       string `json:"session"`
	AgentDir      string `json:"agentDir"`
	Provider      string `json:"provider"`
	Model         string `json:"model"`
}

// Write creates dir/session.jsonl, dir/agent/models.json, dir/agent/settings.json, dir/meta.json and the working directory dir/work.
func Write(dir string, opts Options) (Meta, error) {
	if opts.Tokens <= 0 || opts.Window <= 0 {
		return Meta{}, fmt.Errorf("longsession: tokens and window must be positive (tokens %d, window %d)", opts.Tokens, opts.Window)
	}
	if opts.ResultBytes == 0 {
		opts.ResultBytes = 16384
	}
	if opts.BaseURL == "" {
		opts.BaseURL = "http://127.0.0.1:9/v1"
	}
	agentDir := filepath.Join(dir, "agent")
	if opts.Cwd == "" {
		abs, err := filepath.Abs(filepath.Join(dir, "work"))
		if err != nil {
			return Meta{}, err
		}
		opts.Cwd = abs
	}
	for _, d := range []string{agentDir, opts.Cwd} {
		if err := os.MkdirAll(d, 0o700); err != nil {
			return Meta{}, err
		}
	}
	if err := WriteAgentDir(agentDir, opts.Window, opts.BaseURL); err != nil {
		return Meta{}, err
	}
	sessionPath := filepath.Join(dir, "session.jsonl")
	f, err := os.Create(sessionPath)
	if err != nil {
		return Meta{}, err
	}
	hash := sha256.New()
	counter := &countingWriter{w: io.MultiWriter(f, hash)}
	meta, err := Generate(counter, opts)
	if closeErr := f.Close(); err == nil {
		err = closeErr
	}
	if err != nil {
		return Meta{}, err
	}
	meta.Bytes = counter.n
	meta.SHA256 = hex.EncodeToString(hash.Sum(nil))
	meta.Session, meta.AgentDir = sessionPath, agentDir
	data, err := json.MarshalIndent(meta, "", "  ")
	if err != nil {
		return Meta{}, err
	}
	return meta, os.WriteFile(filepath.Join(dir, "meta.json"), append(data, '\n'), 0o600)
}

// Stage copies the fixture's session file to dir/session.jsonl and returns that path. A harness appends every prompt it answers to the session it opened, so each measurement opens its own copy and the fixture keeps the bytes meta.json describes. Stage fails when the fixture no longer matches its recorded SHA256.
func (m Meta) Stage(dir string) (string, error) {
	src, err := os.Open(m.Session)
	if err != nil {
		return "", err
	}
	defer func() { _ = src.Close() }()
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return "", err
	}
	path := filepath.Join(dir, "session.jsonl")
	dst, err := os.Create(path)
	if err != nil {
		return "", err
	}
	hash := sha256.New()
	n, err := io.Copy(io.MultiWriter(dst, hash), src)
	if closeErr := dst.Close(); err == nil {
		err = closeErr
	}
	if err != nil {
		return "", err
	}
	if sum := hex.EncodeToString(hash.Sum(nil)); n != m.Bytes || sum != m.SHA256 {
		return "", fmt.Errorf("longsession: %s has %d bytes with sha256 %s, but meta.json records %d bytes with sha256 %s", m.Session, n, sum, m.Bytes, m.SHA256)
	}
	return path, nil
}

// WriteAgentDir writes models.json, declaring the scripted model with the window at baseURL, and settings.json, selecting it, into agentDir.
func WriteAgentDir(agentDir string, window int, baseURL string) error {
	models := map[string]any{"providers": map[string]any{Provider: map[string]any{
		"baseUrl": baseURL,
		"api":     "openai-completions",
		"apiKey":  "longsession-key",
		"models": []any{map[string]any{
			"id": Model, "name": "Long session scripted model", "reasoning": false, "input": []string{"text"},
			"contextWindow": window, "maxTokens": 4096,
			"cost": map[string]float64{"input": 0, "output": 0, "cacheRead": 0, "cacheWrite": 0},
		}},
	}}}
	settings := map[string]any{"defaultProvider": Provider, "defaultModel": Model}
	for name, value := range map[string]any{"models.json": models, "settings.json": settings} {
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

// Generate writes the session file to w and returns its metadata without the byte count, hash or paths.
func Generate(w io.Writer, opts Options) (Meta, error) {
	if opts.ResultBytes == 0 {
		opts.ResultBytes = 16384
	}
	if opts.Cwd == "" {
		opts.Cwd = "/longsession"
	}
	g := &generator{
		out:   bufio.NewWriterSize(w, 1<<16),
		hash:  sha256.New(),
		rng:   rand.New(rand.NewPCG(opts.Seed, 0x6c6f6e6773657373)),
		clock: time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC),
	}
	g.line(object{{"type", "session"}, {"version", 3}, {"id", fmt.Sprintf("longsession-%d-%d", opts.Tokens, opts.Seed)}, {"timestamp", g.stamp()}, {"cwd", opts.Cwd}})
	meta := Meta{Cwd: opts.Cwd, Target: opts.Tokens, Window: opts.Window, ResultBytes: opts.ResultBytes, Seed: opts.Seed, Provider: Provider, Model: Model}
	for g.tokens < opts.Tokens && g.err == nil {
		g.turn(meta.Turns, opts.ResultBytes)
		meta.Turns++
	}
	if g.err == nil {
		g.err = g.out.Flush()
	}
	meta.Tokens, meta.Entries = g.tokens, g.entries
	meta.ContentSHA256 = hex.EncodeToString(g.hash.Sum(nil))
	return meta, g.err
}

type generator struct {
	out     *bufio.Writer
	hash    hash.Hash
	header  bool
	rng     *rand.Rand
	clock   time.Time
	parent  any
	tokens  int
	entries int
	err     error
}

func (g *generator) stamp() string {
	g.clock = g.clock.Add(time.Second)
	return g.clock.Format("2006-01-02T15:04:05.000Z")
}

func (g *generator) line(v any) {
	if g.err != nil {
		return
	}
	data, err := json.Marshal(v)
	if err != nil {
		g.err = err
		return
	}
	data = append(data, '\n')
	if _, err := g.out.Write(data); err != nil {
		g.err = err
	}
	if g.header {
		g.hash.Write(data)
	}
	g.header = true
}

// message appends one message entry whose estimate is chars/4 rounded up.
func (g *generator) message(id string, message object, chars int) {
	stamp := g.stamp()
	message = append(message, field{"timestamp", g.clock.UnixMilli()})
	g.line(object{{"type", "message"}, {"id", id}, {"parentId", g.parent}, {"timestamp", stamp}, {"message", message}})
	g.parent = id
	g.entries++
	g.tokens += (chars + 3) / 4
}

func (g *generator) assistant(content []any, stop string) object {
	return object{
		{"role", "assistant"}, {"content", content}, {"api", "openai-completions"}, {"provider", Provider}, {"model", Model},
		{"usage", object{
			{"input", g.tokens}, {"output", 0}, {"cacheRead", 0}, {"cacheWrite", 0}, {"totalTokens", g.tokens},
			{"cost", object{{"input", 0}, {"output", 0}, {"cacheRead", 0}, {"cacheWrite", 0}, {"total", 0}}},
		}},
		{"stopReason", stop},
	}
}

func (g *generator) turn(n, resultBytes int) {
	prompt := fmt.Sprintf("Turn %d: read the next module and explain what it changes. %s", n, g.prose(160))
	g.message(fmt.Sprintf("u%d", n), object{{"role", "user"}, {"content", []any{text(prompt)}}}, len(prompt))

	path := fmt.Sprintf("src/module_%04d.go", n)
	args, _ := json.Marshal(map[string]any{"path": path})
	intro := fmt.Sprintf("I will read %s. %s", path, g.prose(120))
	call := object{{"type", "toolCall"}, {"id", fmt.Sprintf("call-%d", n)}, {"name", "read"}, {"arguments", object{{"path", path}}}}
	g.message(fmt.Sprintf("a%d", n), g.assistant([]any{text(intro), call}, "toolUse"), len(intro)+len("read")+len(args))

	result := g.source(resultBytes)
	g.message(fmt.Sprintf("r%d", n), object{
		{"role", "toolResult"}, {"toolCallId", fmt.Sprintf("call-%d", n)}, {"toolName", "read"},
		{"content", []any{text(result)}}, {"isError", false},
	}, len(result))

	answer := fmt.Sprintf("%s changes the request path. %s", path, g.prose(1400))
	g.message(fmt.Sprintf("b%d", n), g.assistant([]any{text(answer)}, "stop"), len(answer))
}

func text(value string) object { return object{{"type", "text"}, {"text", value}} }

// object is a JSON object written in its members' order, as JSON.stringify writes the object literals Pi builds; a map would sort the keys.
type object []field

type field struct {
	key   string
	value any
}

func (o object) MarshalJSON() ([]byte, error) {
	out := []byte{'{'}
	for i, member := range o {
		if i > 0 {
			out = append(out, ',')
		}
		key, err := json.Marshal(member.key)
		if err != nil {
			return nil, err
		}
		value, err := json.Marshal(member.value)
		if err != nil {
			return nil, err
		}
		out = append(append(append(out, key...), ':'), value...)
	}
	return append(out, '}'), nil
}

var words = strings.Fields(`the request handler session buffer reads writes returns context window token model provider stream event
message tool result value error cache index entry state turn render line width cursor offset frame update queue retry
budget limit bound slice map key field parser encoder decoder writer reader channel lock owner caller callee branch`)

func (g *generator) prose(n int) string {
	var b strings.Builder
	for b.Len() < n {
		if b.Len() > 0 {
			b.WriteByte(' ')
		}
		b.WriteString(words[g.rng.IntN(len(words))])
	}
	b.WriteByte('.')
	return b.String()
}

func (g *generator) source(n int) string {
	var b strings.Builder
	b.Grow(n + 128)
	for line := 1; b.Len() < n; line++ {
		indent := strings.Repeat("\t", g.rng.IntN(3))
		a, c := words[g.rng.IntN(len(words))], words[g.rng.IntN(len(words))]
		fmt.Fprintf(&b, "%s%s := %s(%sValue, %d) // %s %s\n", indent, a, c, a, g.rng.IntN(100000), words[g.rng.IntN(len(words))], c)
	}
	return b.String()[:n]
}

type countingWriter struct {
	w io.Writer
	n int64
}

func (c *countingWriter) Write(p []byte) (int, error) {
	n, err := c.w.Write(p)
	c.n += int64(n)
	return n, err
}
