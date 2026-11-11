// Package sessiongen writes deterministic session files in Pi's JSONL format at a chosen size in tokens, so the long-session gates and
// benchmarks open a million-token session without a fixture checked in.
package sessiongen

import (
	"bufio"
	"fmt"
	"io"
	"math/rand/v2"
	"strconv"
	"time"
)

// CharsPerToken is how many characters of message text one token stands for, as the context estimate counts them (compaction.ts estimateTokens).
const CharsPerToken = 4

// Options selects the size and shape of a generated session.
type Options struct {
	// Tokens is the session's size: generation stops at the first turn that takes the estimated tokens of the message text to Tokens.
	Tokens int
	// CWD is the header's working directory; it defaults to /work/project.
	CWD string
	// Seed makes the content deterministic.
	Seed uint64
	// CompactEvery is the number of turns between compaction entries; zero writes none.
	CompactEvery int
	// ForkEvery is the number of turns between abandoned side branches; zero writes none.
	ForkEvery int
}

// Result describes the file Write produced.
type Result struct {
	// Bytes and Lines are the size of the file and its number of lines, the header included.
	Bytes int64
	Lines int
	// Turns is the number of user prompts on the active branch's turns.
	Turns int
	// LeafID is the id of the last entry of the active branch.
	LeafID string
}

type generator struct {
	w       *bufio.Writer
	rng     *rand.Rand
	line    []byte
	written int64
	lines   int
	chars   int
	clock   int64
	last    string
	ids     []string
	seen    map[string]struct{}
}

const words = "alpha beta gamma delta epsilon zeta eta theta iota kappa lambda mu nu xi omicron pi rho sigma tau upsilon phi chi psi omega func return error nil string"

var wordList = splitWords(words)

// writerBuffer is the size of the generator's output buffer.
const writerBuffer = 64 << 10 // pig divergence (D112): a test-data generator's buffer size, not an upstream value.

func splitWords(s string) []string {
	var out []string
	start := 0
	for i := 0; i <= len(s); i++ {
		if i == len(s) || s[i] == ' ' {
			out = append(out, s[start:i])
			start = i + 1
		}
	}
	return out
}

// Write generates a session to w.
func Write(w io.Writer, opts Options) (Result, error) {
	if opts.CWD == "" {
		opts.CWD = "/work/project"
	}
	g := &generator{w: bufio.NewWriterSize(w, writerBuffer), rng: rand.New(rand.NewPCG(opts.Seed, 0x9e3779b97f4a7c15)), clock: 1_760_000_000_000, seen: map[string]struct{}{}}
	g.begin()
	g.str(`{"type":"session","version":3,"id":"0198f3a0-0000-7000-8000-000000000001","timestamp":"`)
	g.line = append(g.line, g.iso()...)
	g.str(`","cwd":`)
	g.quote(opts.CWD)
	g.str("}")
	if err := g.end(); err != nil {
		return Result{}, err
	}
	turns := 0
	for g.chars < opts.Tokens*CharsPerToken {
		turns++
		if err := g.turn(turns); err != nil {
			return Result{}, err
		}
		if opts.ForkEvery > 0 && turns%opts.ForkEvery == 0 {
			if err := g.sideBranch(); err != nil {
				return Result{}, err
			}
		}
		if opts.CompactEvery > 0 && turns%opts.CompactEvery == 0 {
			if err := g.compact(); err != nil {
				return Result{}, err
			}
		}
	}
	if err := g.w.Flush(); err != nil {
		return Result{}, err
	}
	return Result{Bytes: g.written, Lines: g.lines, Turns: turns, LeafID: g.last}, nil
}

func (g *generator) begin() { g.line = g.line[:0] }

func (g *generator) end() error {
	g.line = append(g.line, '\n')
	n, err := g.w.Write(g.line)
	g.written += int64(n)
	g.lines++
	return err
}

func (g *generator) str(s string) { g.line = append(g.line, s...) }

func (g *generator) quote(s string) { g.line = strconv.AppendQuote(g.line, s) }

func (g *generator) iso() string {
	g.clock += int64(1 + g.rng.IntN(5000))
	return time.UnixMilli(g.clock).UTC().Format("2006-01-02T15:04:05.000Z")
}

// newID returns an id no earlier entry has: a repeated id would make a parent cycle.
func (g *generator) newID() string {
	for {
		id := fmt.Sprintf("%08x", g.rng.Uint32())
		if _, taken := g.seen[id]; !taken {
			g.seen[id] = struct{}{}
			return id
		}
	}
}

// text returns about n characters of words and counts them toward the session's size.
func (g *generator) text(n int) string {
	buf := make([]byte, 0, n+16)
	for len(buf) < n {
		buf = append(buf, wordList[g.rng.IntN(len(wordList))]...)
		buf = append(buf, ' ')
	}
	g.chars += len(buf)
	return string(buf)
}

// entry writes one entry line, whose members after the base members are body's output, and returns its id.
func (g *generator) entry(parent, entryType string, body func()) (string, error) {
	id := g.newID()
	g.begin()
	g.str(`{"type":"` + entryType + `","id":"` + id + `","parentId":`)
	if parent == "" {
		g.str("null")
	} else {
		g.str(`"` + parent + `"`)
	}
	g.str(`,"timestamp":"`)
	g.line = append(g.line, g.iso()...)
	g.str(`",`)
	body()
	g.str("}")
	g.ids = append(g.ids, id)
	return id, g.end()
}

func (g *generator) textBlock(text string) {
	g.str(`{"type":"text","text":`)
	g.quote(text)
	g.str("}")
}

func (g *generator) usage(input, output int) {
	g.str(`"usage":{"input":` + strconv.Itoa(input) + `,"output":` + strconv.Itoa(output) + `,"cacheRead":` + strconv.Itoa(input*3/4) +
		`,"cacheWrite":0,"totalTokens":` + strconv.Itoa(input+output+input*3/4) + `,"cost":{"input":0.001,"output":0.002,"cacheRead":0.0003,"cacheWrite":0,"total":0.0033}}`)
}

func (g *generator) user(parent, text string) (string, error) {
	return g.entry(parent, "message", func() {
		g.str(`"message":{"role":"user","content":[`)
		g.textBlock(text)
		g.str(`],"timestamp":` + strconv.FormatInt(g.clock, 10) + "}")
	})
}

func (g *generator) assistant(parent string, content func(), stop string) (string, error) {
	return g.entry(parent, "message", func() {
		g.str(`"message":{"role":"assistant","content":[`)
		content()
		g.str(`],"api":"anthropic-messages","provider":"anthropic","model":"claude-sonnet-4-5",`)
		g.usage(2000+g.rng.IntN(60000), 100+g.rng.IntN(900))
		g.str(`,"stopReason":"` + stop + `","timestamp":` + strconv.FormatInt(g.clock, 10) + "}")
	})
}

// toolOutputSize is skewed as real tool output is: most outputs are a few kilobytes, a few are very large.
func (g *generator) toolOutputSize() int {
	switch r := g.rng.IntN(100); {
	case r < 70:
		return 500 + g.rng.IntN(4000)
	case r < 95:
		return 8000 + g.rng.IntN(40000)
	default:
		return 100000 + g.rng.IntN(200000)
	}
}

func (g *generator) turn(n int) error {
	id, err := g.user(g.last, "Turn "+strconv.Itoa(n)+": "+g.text(200+g.rng.IntN(600)))
	if err != nil {
		return err
	}
	for range 1 + g.rng.IntN(4) {
		call := fmt.Sprintf("toolu_%016x", g.rng.Uint64())
		id, err = g.assistant(id, func() {
			g.textBlock(g.text(100 + g.rng.IntN(300)))
			g.str(`,{"type":"toolCall","id":"` + call + `","name":"bash","arguments":{"command":`)
			g.quote("grep -rn " + g.text(20))
			g.str("}}")
		}, "toolUse")
		if err != nil {
			return err
		}
		output := g.text(g.toolOutputSize())
		id, err = g.entry(id, "message", func() {
			g.str(`"message":{"role":"toolResult","toolCallId":"` + call + `","toolName":"bash","content":[`)
			g.textBlock(output)
			g.str(`],"isError":false,"timestamp":` + strconv.FormatInt(g.clock, 10) + "}")
		})
		if err != nil {
			return err
		}
	}
	g.last, err = g.assistant(id, func() { g.textBlock(g.text(400 + g.rng.IntN(1200))) }, "stop")
	return err
}

// sideBranch writes a short abandoned subtree off an earlier entry and leaves the active leaf as it is.
func (g *generator) sideBranch() error {
	if len(g.ids) < 20 {
		return nil
	}
	from := g.ids[len(g.ids)-10-g.rng.IntN(len(g.ids)/2)]
	id, err := g.user(from, "side: "+g.text(120))
	if err != nil {
		return err
	}
	_, err = g.assistant(id, func() { g.textBlock(g.text(300)) }, "stop")
	return err
}

// compact writes a compaction of the active branch that keeps its last few entries.
func (g *generator) compact() error {
	kept := g.ids[len(g.ids)-8]
	summary := g.text(3000)
	var err error
	g.last, err = g.entry(g.last, "compaction", func() {
		g.str(`"summary":`)
		g.quote(summary)
		g.str(`,"firstKeptEntryId":"` + kept + `","tokensBefore":150000,"fromHook":false,`)
		g.usage(60000, 1500)
	})
	return err
}
