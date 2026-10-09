// Command durableperf runs the durable-bench workload (github.com/clavia-labs/durable-bench) on PiG's Go Durable over
// SQLite. A scripted faux model calls a lookup tool; historical turns repeat a one-tool, one-tool, zero-tool pattern;
// the measured turns are ten eight-tool-call turns on a copy of a seeded store.
//
//	durableperf seed   -dir DIR [-sizes 50,250,1000,3500]
//	durableperf sample -fixture FILE -turns N [-cpuprofile F] [-memprofile F] [-blockprofile F] [-mutexprofile F]
//	durableperf run    -dir DIR [-sizes ...] [-samples 3] [-out results.jsonl]
package main

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"runtime/pprof"
	"runtime/trace"
	"slices"
	"strconv"
	"strings"
	"time"

	_ "modernc.org/sqlite"

	"github.com/MichaelKinsy/PiG/ai"
	"github.com/MichaelKinsy/PiG/durable"
	"github.com/MichaelKinsy/PiG/durable/harness"
	"github.com/MichaelKinsy/PiG/durable/storage/sqlite"
	sqlitenode "github.com/MichaelKinsy/PiG/durable/storage/sqlite/node"
)

const (
	measuredTurns = 10
	measuredTools = 8
	seedBatch     = 50
	systemText    = "You are a benchmark agent. Call lookup as instructed, then answer briefly."
)

var (
	cycle        = [...]int{1, 1, 0}
	defaultSizes = []int{50, 250, 1000, 3500}
	toolsPattern = regexp.MustCompile(`tools=(\d+)`)
	background   = context.Background()
)

type turn struct{ id, text string }

func newTurn(id string, tools int) turn {
	return turn{id: id, text: fmt.Sprintf("turn %s tools=%d", id, tools)}
}

func history(from, to int) []turn {
	turns := make([]turn, 0, to-from)
	for i := from; i < to; i++ {
		turns = append(turns, newTurn(fmt.Sprintf("h%d", i), cycle[i%len(cycle)]))
	}
	return turns
}

func payload(n int) string {
	head := fmt.Sprintf("record %d: ", n)
	size := 256
	if n%97 == 0 {
		size = 8192
	}
	return head + strings.Repeat("x", size-len(head))
}

// message is the durable-bench plan.ts Message: the scripted model sees only role, text and tool-call numbers.
type message struct {
	role  string
	text  string
	calls []int
}

// next is plan.ts next(): call lookup(total+1) until the planned count for the last user turn is done.
func next(context []message) (call int, answer string) {
	last := -1
	for i, c := range slices.Backward(context) {
		if c.role == "user" {
			last = i
			break
		}
	}
	planned := 0
	if last >= 0 {
		if match := toolsPattern.FindStringSubmatch(context[last].text); match != nil {
			planned, _ = strconv.Atoi(match[1])
		}
	}
	total, done := 0, 0
	for i, m := range context {
		if m.role == "tool" {
			total++
			if i > last {
				done++
			}
		}
	}
	if done < planned {
		return total + 1, ""
	}
	return 0, fmt.Sprintf("done after %d lookups", done)
}

// fingerprint is plan.ts fingerprint(): the first eight bytes of SHA-256 over JSON [[role, text, calls]...].
func fingerprint(context []message) string {
	rows := make([][3]any, len(context))
	for i, m := range context {
		calls := m.calls
		if calls == nil {
			calls = []int{}
		}
		rows[i] = [3]any{m.role, m.text, calls}
	}
	encoded, _ := json.Marshal(rows)
	sum := sha256.Sum256(encoded)
	return hex.EncodeToString(sum[:8])
}

func toMessages(messages []ai.Message) []message {
	out := make([]message, 0, len(messages))
	for _, m := range messages {
		switch m := m.(type) {
		case ai.UserMessage:
			out = append(out, message{role: "user", text: userText(m)})
		case ai.AssistantMessage:
			var text strings.Builder
			var calls []int
			for _, block := range m.Content {
				switch block := block.(type) {
				case ai.TextContent:
					text.WriteString(block.Text)
				case ai.ToolCall:
					n, _ := block.Arguments["n"].(float64)
					calls = append(calls, int(n))
				}
			}
			out = append(out, message{role: "assistant", text: text.String(), calls: calls})
		case ai.ToolResultMessage:
			var text strings.Builder
			for _, block := range m.Content {
				if block, ok := block.(ai.TextContent); ok {
					text.WriteString(block.Text)
				}
			}
			out = append(out, message{role: "tool", text: text.String()})
		}
	}
	return out
}

func userText(m ai.UserMessage) string {
	switch content := m.Content.(type) {
	case ai.UserText:
		return string(content)
	case ai.UserContentBlocks:
		var text strings.Builder
		for _, block := range content {
			if block, ok := block.(ai.TextContent); ok {
				text.WriteString(block.Text)
			}
		}
		return text.String()
	}
	return ""
}

var lookup = &durable.ToolRegistration{
	ToolSchema: ai.ToolSchema{
		Name:        "lookup",
		Description: "Look up record number n",
		Parameters: map[string]any{
			"type":       "object",
			"properties": map[string]any{"n": map[string]any{"type": "number"}},
			"required":   []any{"n"},
		},
	},
	Execute: func(_ context.Context, args any, _ durable.ToolExecutionApi) (durable.ToolExecutionResult, error) {
		n, _ := args.(map[string]any)["n"].(float64)
		tl("tool exec")
		return durable.ToolExecutionResult{Content: []ai.ToolResultMessageContent{ai.TextContent{Text: payload(int(n))}}}, nil
	},
}

// bench is one open Harness over a store with the scripted model.
type bench struct {
	opened harness.Harness
	root   harness.Conversation
	last   ai.TranscriptContext
}

// Scripted-model variants. "walk" is durable-bench's model as written: it rebuilds its view of the whole transcript on
// every call. "incremental" reads only the messages added since its previous call.
const (
	modelWalk        = "walk"
	modelIncremental = "incremental"
)

var modelVariant = modelWalk

// Providers. "faux" is pi-ai's faux provider, as durable-bench uses it: besides the scripted answer it serializes the
// whole transcript to estimate usage on every call. "scripted" answers and nothing else.
const (
	providerFaux     = "faux"
	providerScripted = "scripted"
)

var providerKind = providerFaux

// scriptedProvider is a chat provider whose answer comes from respond.
type scriptedProvider struct {
	model   *ai.Model
	respond ai.FauxResponseFactory
}

func (p *scriptedProvider) ID() string   { return "faux" }
func (p *scriptedProvider) Close() error { return nil }
func (p *scriptedProvider) Stream(_ context.Context, transcript ai.TranscriptContext, _ ai.StreamOptions) (*ai.AssistantMessageEventStream, error) {
	response, err := p.respond(transcript, ai.StreamOptions{}, nil, p.model)
	if err != nil {
		return nil, err
	}
	message := &ai.AssistantMessage{API: p.model.ProviderMeta.API, Provider: "faux", Model: p.model.ID, Content: []ai.AssistantContentBlock{}, StopReason: ai.StopReasonStop, Timestamp: time.Now().UnixMilli()}
	message.Content = append(message.Content, response.Content...)
	if response.StopReason != "" {
		message.StopReason = response.StopReason
	}
	stream := ai.NewAssistantMessageEventStream()
	if err := stream.Push(ai.DoneEvent{Reason: message.StopReason, Message: message}); err != nil {
		return nil, err
	}
	return stream, nil
}

func newScriptedModels(respond ai.FauxResponseFactory) (*ai.Models, *ai.Model) {
	model := &ai.Model{
		ID: "scripted-1", DisplayName: "Scripted", Input: []string{"text"},
		Capabilities: ai.ModelCapabilities{ContextWindow: 1e9, MaxOutputTokens: 16384, SupportsToolUse: true},
		ProviderMeta: ai.ProviderMetadata{ProviderID: "faux", API: "faux", BaseURL: "http://localhost:0"},
	}
	owner := &scriptedProvider{model: model, respond: respond}
	model.Provider = owner
	stream := func(ctx context.Context, model *ai.Model, transcript ai.TranscriptContext, options ai.StreamOptions) (*ai.AssistantMessageEventStream, error) {
		return owner.Stream(ctx, transcript, options)
	}
	provider := ai.CreateProvider(ai.CreateProviderOptions{
		ID:     "faux",
		Auth:   ai.ProviderAuth{APIKey: &ai.APIKeyAuth{Name: "Scripted", Resolve: func(context.Context, ai.APIKeyAuthInput) (*ai.AuthResult, error) { return &ai.AuthResult{}, nil }}},
		Models: ai.AnyModels([]*ai.Model{model}),
		API:    &ai.ProviderStreams{Stream: stream, StreamSimple: stream},
	})
	models := ai.CreateModels()
	models.SetProvider(provider)
	return models, model
}

// progress is the incremental model's state: what it has counted of the transcript so far.
type progress struct{ read, total, done, planned int }

func (p *progress) update(transcript ai.TranscriptContext) {
	if transcript.Len() < p.read {
		*p = progress{}
	}
	for ; p.read < transcript.Len(); p.read++ {
		switch message := transcript.At(p.read).(type) {
		case ai.UserMessage:
			p.planned, p.done = 0, 0
			if match := toolsPattern.FindStringSubmatch(userText(message)); match != nil {
				p.planned, _ = strconv.Atoi(match[1])
			}
		case ai.ToolResultMessage:
			p.total++
			p.done++
		}
	}
}

func (p *progress) next() (call int, answer string) {
	if p.done < p.planned {
		return p.total + 1, ""
	}
	return 0, fmt.Sprintf("done after %d lookups", p.done)
}

// seen is the transcript of the model's last call as the benchmark's fingerprint reads it.
func (b *bench) seen() []message {
	messages := make([]ai.Message, b.last.Len())
	for index := range messages {
		messages[index] = b.last.At(index)
	}
	return toMessages(messages)
}

func open(path string) (*bench, error) {
	faux := ai.NewFauxProvider(ai.FauxConfig{
		Models:    []ai.FauxModelDefinition{{ID: "scripted-1", Name: "Scripted", ContextWindow: 1e9}},
		TokenSize: &ai.FauxTokenSize{Min: new(int(1e9)), Max: new(int(1e9))},
	})
	b := &bench{}
	var respond ai.FauxResponseFactory
	var counted progress
	respond = func(transcript ai.TranscriptContext, _ ai.StreamOptions, _ *ai.FauxProviderState, _ *ai.Model) (ai.AssistantMessage, error) {
		if providerKind == providerFaux {
			faux.AppendResponses([]ai.FauxResponseStep{ai.FauxFactoryStep(respond)})
		}
		tl("model factory")
		if transcript.Len() > 0 {
			if system, ok := transcript.At(0).(ai.SystemMessage); ok {
				if text, isText := system.Content.(ai.SystemText); isText && strings.Contains(string(text), "summarization") {
					return ai.FauxResponse{Content: []ai.FauxContentBlock{ai.FauxText("## Goal\nScripted summary.")}}.AssistantMessage(), nil
				}
			}
		}
		b.last = transcript
		var call int
		var answer string
		if modelVariant == modelIncremental {
			counted.update(transcript)
			call, answer = counted.next()
		} else {
			call, answer = next(toMessages(transcript.Messages()))
		}
		if call > 0 {
			return ai.FauxResponse{
				Content:    []ai.FauxContentBlock{ai.FauxToolCall("lookup", map[string]any{"n": float64(call)}, &ai.FauxToolCallOptions{ID: fmt.Sprintf("call-%d", call)})},
				StopReason: "toolUse",
			}.AssistantMessage(), nil
		}
		return ai.FauxResponse{Content: []ai.FauxContentBlock{ai.FauxText(answer)}}.AssistantMessage(), nil
	}
	faux.SetResponses([]ai.FauxResponseStep{ai.FauxFactoryStep(respond)})
	models := ai.CreateModels()
	model := faux.GetModel()
	if providerKind == providerScripted {
		models, model = newScriptedModels(respond)
	} else {
		models.SetProvider(faux.Provider())
	}
	registry := harness.CreateRegistry()
	preamble := systemText
	if err := registry.Install(&durable.Extension{
		Name:  "bench",
		Tools: []*durable.ToolRegistration{lookup},
		Sections: []*durable.PromptSection{harness.Section("preamble", func(context.Context, durable.PromptInput) (*string, error) {
			return &preamble, nil
		}, harness.SectionOptions{Tag: new(false)})},
	}); err != nil {
		return nil, err
	}
	rawdb, err := sqlitenode.OpenNodeSqliteDatabase(path, sqlitenode.NodeSqliteStorageOptions{})
	if err != nil {
		return nil, err
	}
	var wrapped sqlite.SqliteDatabase = countingDB{countingExec{rawdb}, rawdb}
	if os.Getenv("DBG") != "" {
		wrapped = dbgDB{dbgExec{wrapped}, wrapped}
	}
	if os.Getenv("CAPTURE") != "" {
		wrapped = captureDB{captureExec{wrapped}, wrapped}
	}
	store, err := sqlite.Open(wrapped)
	if err != nil {
		return nil, err
	}
	opened, err := harness.OpenHarness(background, store, harness.HarnessOptions{
		Models: models, Registry: registry,
		Settings: func() *harness.HarnessSettings {
			return &harness.HarnessSettings{Compaction: &harness.CompactionPolicyPatch{Enabled: new(false)}}
		},
	})
	if err != nil {
		return nil, err
	}
	root, err := opened.Root(background, &harness.RootOptions{
		Agent: &harness.AgentChange{Model: harness.SetTo(durable.ModelRef{Provider: model.Provider.ID(), ModelId: model.ID})},
	})
	if err != nil {
		return nil, err
	}
	b.opened, b.root = opened, root
	return b, nil
}

func (b *bench) turn(t turn) error {
	submission, err := b.root.Submit(background, durable.SubmissionDraft{
		Type: durable.SubmissionTypeInput, Content: ai.UserText(t.text), RequestId: new(t.id),
	})
	if err != nil {
		return err
	}
	settled, err := submission.Wait(background)
	if err != nil {
		return err
	}
	if settled.Status != durable.SubmissionDone {
		return fmt.Errorf("turn %s settled as %s", t.id, settled.Status)
	}
	return b.root.WaitForIdle(background)
}

// compact runs one manual compaction and waits for it.
func (b *bench) compact() error {
	id, err := b.root.Compact(background, nil)
	if err != nil {
		return err
	}
	_, err = b.opened.WaitForTask(background, id)
	return err
}

func (b *bench) close() error { return b.opened.Close(background) }

func tableCounts(path string) (map[string]int, error) {
	db, err := sql.Open("sqlite", path)
	if err != nil {
		return nil, err
	}
	defer func() { _ = db.Close() }()
	rows, err := db.Query("SELECT name FROM sqlite_master WHERE type = 'table' AND name NOT LIKE 'sqlite\\_%' ESCAPE '\\'")
	if err != nil {
		return nil, err
	}
	var names []string
	for rows.Next() {
		var name string
		if err := rows.Scan(&name); err != nil {
			return nil, err
		}
		names = append(names, name)
	}
	if err := rows.Close(); err != nil {
		return nil, err
	}
	counts := map[string]int{}
	for _, name := range names {
		var n int
		if err := db.QueryRow(fmt.Sprintf("SELECT COUNT(*) FROM %q", name)).Scan(&n); err != nil {
			return nil, err
		}
		counts[name] = n
	}
	return counts, nil
}

func fileSize(path string) int64 {
	info, err := os.Stat(path)
	if err != nil {
		return 0
	}
	return info.Size()
}

func parseSizes(text string) ([]int, error) {
	if text == "" {
		return defaultSizes, nil
	}
	var sizes []int
	for part := range strings.SplitSeq(text, ",") {
		n, err := strconv.Atoi(strings.TrimSpace(part))
		if err != nil {
			return nil, err
		}
		sizes = append(sizes, n)
	}
	return sizes, nil
}

// fixturePrefix names the stores run reads: "pi" (seeded by durable/interop/bench.mjs) or "go". The two seeds hold the
// same rows, so either runtime opens either.
var fixturePrefix = "go"

func fixturePath(dir string, turns int) string {
	return filepath.Join(dir, fmt.Sprintf("%s-%d.sqlite", fixturePrefix, turns))
}

// seed grows one store in batches of seedBatch turns, reopening it for each batch as durable-bench's seed does, and
// copies it out at every size.
func seed(args []string) error {
	flags := flag.NewFlagSet("seed", flag.ExitOnError)
	dir := flags.String("dir", "fixtures", "fixture directory")
	sizesText := flags.String("sizes", "", "ascending turn counts (default 50,250,1000,3500)")
	_ = flags.Parse(args)
	sizes, err := parseSizes(*sizesText)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(*dir, 0o777); err != nil {
		return err
	}
	work := filepath.Join(*dir, "go-work.sqlite")
	for _, suffix := range []string{"", "-wal", "-shm"} {
		_ = os.Remove(work + suffix)
	}
	done := 0
	digest := ""
	for _, size := range sizes {
		started := time.Now()
		for done < size {
			end := min(done+seedBatch, size)
			b, err := open(work)
			if err != nil {
				return err
			}
			for _, t := range history(done, end) {
				if err := b.turn(t); err != nil {
					return err
				}
			}
			digest = fingerprint(b.seen())
			if err := b.close(); err != nil {
				return err
			}
			done = end
			fmt.Fprintf(os.Stderr, "\rgo %d/%d", done, size)
		}
		if err := copyFile(work, fixturePath(*dir, size)); err != nil {
			return err
		}
		counts, err := tableCounts(work)
		if err != nil {
			return err
		}
		meta := map[string]any{"target": "go", "turns": size, "fingerprint": digest, "seedMs": time.Since(started).Milliseconds(), "bytes": fileSize(work), "tables": counts}
		//portlint:allow mapkeyorder a bench progress line read by people and JSON parsers; no Pi output to match
		encoded, _ := json.Marshal(meta)
		fmt.Fprintf(os.Stderr, "\r%s\n", encoded)
		if err := os.WriteFile(fixturePath(*dir, size)+".json", encoded, 0o666); err != nil {
			return err
		}
	}
	for _, suffix := range []string{"", "-wal", "-shm"} {
		_ = os.Remove(work + suffix)
	}
	return nil
}

func copyFile(from, to string) error {
	in, err := os.Open(from)
	if err != nil {
		return err
	}
	defer func() { _ = in.Close() }()
	out, err := os.Create(to)
	if err != nil {
		return err
	}
	if _, err := io.Copy(out, in); err != nil {
		_ = out.Close()
		return err
	}
	return out.Close()
}

// result is one sample: the fields of durable-bench's results.jsonl line.
type result struct {
	Target      string         `json:"target"`
	Variant     string         `json:"variant"`
	Provider    string         `json:"provider"`
	Open        sqlCounts      `json:"openSql"`
	SQL         sqlCounts      `json:"sqlPerTenTurns"`
	Version     string         `json:"version"`
	Turns       int            `json:"turns"`
	Sample      int            `json:"sample"`
	Startup     float64        `json:"startup"`
	OpenMs      float64        `json:"open"`
	Turn        []float64      `json:"turn"`
	OpenCPU     float64        `json:"openCpu"`
	TurnCPU     []float64      `json:"turnCpu"`
	RSS         float64        `json:"rss"`
	PeakRSS     float64        `json:"peakRss"`
	CPU         float64        `json:"cpuSeconds"`
	Bytes       int64          `json:"bytes"`
	Tables      map[string]int `json:"tables"`
	Finger      string         `json:"fingerprint"`
	Goroutns    int            `json:"goroutines"`
	AllocMB     float64        `json:"allocMB"`
	Mallocs     uint64         `json:"mallocs"`
	GCs         uint32         `json:"gcs"`
	Procs       int            `json:"gomaxprocs"`
	Voluntary   int64          `json:"voluntarySwitches"`
	Involuntary int64          `json:"involuntarySwitches"`
	At          string         `json:"at"`
}

func millis(d time.Duration) float64 { return float64(d.Microseconds()) / 1000 }

// sample opens a copy of the fixture, measures the open and ten eight-tool turns, and prints one JSON result.
func sample(args []string) error {
	flags := flag.NewFlagSet("sample", flag.ExitOnError)
	fixture := flags.String("fixture", "", "seeded SQLite store")
	turns := flags.Int("turns", 0, "history turns in the fixture")
	index := flags.Int("sample", 0, "sample index")
	flags.StringVar(&modelVariant, "model", modelWalk, "scripted model: walk (as published) or incremental")
	flags.StringVar(&providerKind, "provider", providerFaux, "provider: faux (as published) or scripted")
	compact := flags.Bool("compact", false, "run one manual compaction before the measured turns, so the active context starts at a head marker")
	restartEach := flags.Bool("restart-each", false, "reopen the harness before every measured turn, so each measured turn is cold; the process stays warm")
	measured := flags.Int("measured", measuredTurns, "measured turns (fewer than ten is for profiling the cold turn)")
	cpuProfile := flags.String("cpuprofile", "", "write a CPU profile of open and the measured turns")
	memProfile := flags.String("memprofile", "", "write a heap profile after the measured turns")
	blockProfile := flags.String("blockprofile", "", "write a block profile")
	mutexProfile := flags.String("mutexprofile", "", "write a mutex profile")
	traceFile := flags.String("trace", "", "write an execution trace of open and the measured turns")
	_ = flags.Parse(args)
	dir, err := os.MkdirTemp("", "durableperf-")
	if err != nil {
		return err
	}
	defer func() { _ = os.RemoveAll(dir) }()
	work := filepath.Join(dir, "store.sqlite")
	if err := copyFile(*fixture, work); err != nil {
		return err
	}
	if *blockProfile != "" {
		runtime.SetBlockProfileRate(1)
	}
	if *mutexProfile != "" {
		runtime.SetMutexProfileFraction(1)
	}
	if *cpuProfile != "" {
		file, err := os.Create(*cpuProfile)
		if err != nil {
			return err
		}
		if err := pprof.StartCPUProfile(file); err != nil {
			return err
		}
		defer pprof.StopCPUProfile()
	}
	if *traceFile != "" {
		file, err := os.Create(*traceFile)
		if err != nil {
			return err
		}
		if err := trace.Start(file); err != nil {
			return err
		}
		defer trace.Stop()
	}
	cpuStart := cpuSeconds()
	voluntaryBefore, involuntaryBefore := switches()
	var memBefore runtime.MemStats
	runtime.ReadMemStats(&memBefore)
	started, openingCPU := time.Now(), cpuSeconds()
	b, err := open(work)
	if err != nil {
		return err
	}
	openedIn := time.Since(started)
	openCPU := 1000 * (cpuSeconds() - openingCPU)
	if *compact {
		if err := b.compact(); err != nil {
			return err
		}
		if err := b.close(); err != nil {
			return err
		}
		reopening, reopeningCPU := time.Now(), cpuSeconds()
		if b, err = open(work); err != nil {
			return err
		}
		openedIn = time.Since(reopening)
		openCPU = 1000 * (cpuSeconds() - reopeningCPU)
	}
	out := make([]float64, 0, *measured)
	turnCPU := make([]float64, 0, *measured)
	opening := counts
	resetCounts()
	for i := range *measured {
		if *restartEach && i > 0 {
			if err := b.close(); err != nil {
				return err
			}
		}
		started = time.Now()
		turnCPUStart := cpuSeconds()
		if *restartEach && i > 0 {
			reopened, err := open(work)
			if err != nil {
				return err
			}
			b = reopened
		}
		if i == 3 && os.Getenv("DBG") != "" {
			defer tlDump(started, 400)
		}
		if err := b.turn(newTurn(fmt.Sprintf("m%d", i), measuredTools)); err != nil {
			return err
		}
		out = append(out, millis(time.Since(started)))
		turnCPU = append(turnCPU, 1000*(cpuSeconds()-turnCPUStart))
	}
	cpu := cpuSeconds() - cpuStart
	sqlCount := counts
	if path := os.Getenv("CAPTURE"); path != "" {
		//portlint:allow nilslicenull a debug capture file of the bench; no Pi output to match
		encoded, _ := json.Marshal(captureWrites)
		_ = os.WriteFile(path, encoded, 0o666)
	}
	voluntaryAfter, involuntaryAfter := switches()
	var memAfter runtime.MemStats
	runtime.ReadMemStats(&memAfter)
	if os.Getenv("DBG") != "" {
		dbgDump()
	}
	current, peak := rssMB()
	finger := fingerprint(b.seen())
	if *memProfile != "" {
		runtime.GC()
		file, err := os.Create(*memProfile)
		if err != nil {
			return err
		}
		if err := pprof.Lookup("allocs").WriteTo(file, 0); err != nil {
			return err
		}
		_ = file.Close()
	}
	for name, path := range map[string]string{"block": *blockProfile, "mutex": *mutexProfile} {
		if path == "" {
			continue
		}
		file, err := os.Create(path)
		if err != nil {
			return err
		}
		if err := pprof.Lookup(name).WriteTo(file, 0); err != nil {
			return err
		}
		_ = file.Close()
	}
	goroutines := runtime.NumGoroutine()
	if err := b.close(); err != nil {
		return err
	}
	counts, err := tableCounts(work)
	if err != nil {
		return err
	}
	line := result{
		Target: "go", Variant: modelVariant, Provider: providerKind, Version: "pig", Turns: *turns, Sample: *index, OpenMs: millis(openedIn), Open: opening, SQL: sqlCount, Turn: out, OpenCPU: openCPU, TurnCPU: turnCPU,
		RSS: current, PeakRSS: peak, CPU: cpu, Bytes: fileSize(work), Tables: counts, Finger: finger,
		Goroutns: goroutines, AllocMB: float64(memAfter.TotalAlloc-memBefore.TotalAlloc) / 1e6,
		Mallocs: memAfter.Mallocs - memBefore.Mallocs, GCs: memAfter.NumGC - memBefore.NumGC, Procs: runtime.GOMAXPROCS(0),
		Voluntary: voluntaryAfter - voluntaryBefore, Involuntary: involuntaryAfter - involuntaryBefore, At: time.Now().UTC().Format(time.RFC3339),
	}
	//portlint:allow jsonescape a bench result line read by JSON parsers, which decode \u003c and < alike; no Pi output to match
	encoded, _ := json.Marshal(line)
	fmt.Println(string(encoded))
	return nil
}

func median(values []float64) float64 {
	sorted := slices.Sorted(slices.Values(values))
	if len(sorted) == 0 {
		return 0
	}
	if len(sorted)%2 == 1 {
		return sorted[len(sorted)/2]
	}
	return (sorted[len(sorted)/2-1] + sorted[len(sorted)/2]) / 2
}

func p95(values []float64) float64 {
	sorted := slices.Sorted(slices.Values(values))
	if len(sorted) == 0 {
		return 0
	}
	return sorted[min(len(sorted)-1, int(float64(len(sorted))*0.95))]
}

// run spawns one fresh process per sample and prints durable-bench's table: cold is open plus the first turn, warm is
// the median of the other nine turns.
func run(args []string) error {
	flags := flag.NewFlagSet("run", flag.ExitOnError)
	dir := flags.String("dir", "fixtures", "fixture directory")
	sizesText := flags.String("sizes", "", "turn counts")
	samples := flags.Int("samples", 3, "samples per size")
	outPath := flags.String("out", "results.jsonl", "results file (appended)")
	flags.StringVar(&fixturePrefix, "prefix", "pi", "fixture prefix: pi or go")
	flags.StringVar(&modelVariant, "model", modelWalk, "scripted model: walk (as published) or incremental")
	flags.StringVar(&providerKind, "provider", providerFaux, "provider: faux (as published) or scripted")
	_ = flags.Parse(args)
	sizes, err := parseSizes(*sizesText)
	if err != nil {
		return err
	}
	self, err := os.Executable()
	if err != nil {
		return err
	}
	out, err := os.OpenFile(*outPath, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o666)
	if err != nil {
		return err
	}
	defer func() { _ = out.Close() }()
	fmt.Printf("%7s %9s %9s %9s %9s %9s %8s %8s\n", "turns", "cold ms", "warm ms", "warm p95", "open ms", "MB", "rss MB", "cpu s")
	for _, size := range sizes {
		var cold, warm, opens, rss, cpu, bytes []float64
		var all []float64
		for i := range *samples {
			command := exec.Command(self, "sample", "-model", modelVariant, "-provider", providerKind, "-fixture", fixturePath(*dir, size), "-turns", strconv.Itoa(size), "-sample", strconv.Itoa(i))
			command.Stderr = os.Stderr
			output, err := command.Output()
			if err != nil {
				return fmt.Errorf("sample %d/%d: %w", size, i, err)
			}
			if _, err := out.Write(output); err != nil {
				return err
			}
			var r result
			if err := json.Unmarshal(output, &r); err != nil {
				return errors.Join(err, errors.New(string(output)))
			}
			cold = append(cold, r.OpenMs+r.Turn[0])
			warm = append(warm, median(r.Turn[1:]))
			all = append(all, r.Turn[1:]...)
			opens = append(opens, r.OpenMs)
			rss = append(rss, r.PeakRSS)
			cpu = append(cpu, r.CPU)
			bytes = append(bytes, float64(r.Bytes)/1e6)
		}
		fmt.Printf("%7d %9.0f %9.0f %9.0f %9.0f %9.1f %8.0f %8.2f\n", size, median(cold), median(warm), p95(all), median(opens), median(bytes), median(rss), median(cpu))
	}
	return nil
}

func main() {
	if len(os.Args) < 2 {
		fmt.Fprintln(os.Stderr, "usage: durableperf seed|sample|run [flags]")
		os.Exit(2)
	}
	commands := map[string]func([]string) error{"seed": seed, "sample": sample, "run": run}
	command, ok := commands[os.Args[1]]
	if !ok {
		fmt.Fprintln(os.Stderr, "usage: durableperf seed|sample|run [flags]")
		os.Exit(2)
	}
	if err := command(os.Args[2:]); err != nil {
		fmt.Fprintln(os.Stderr, "durableperf:", err)
		os.Exit(1)
	}
}
