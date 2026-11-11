// SPDX-License-Identifier: MIT

package longsession

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io"
	"math/rand/v2"
	"time"
)

// TreeOptions shapes a session that exercises every entry type of a Pi v3 session file rather than one long conversation.
type TreeOptions struct {
	// Entries is the number of entries after the header.
	Entries int
	// Seed selects the shape and the text; one seed always produces the same file.
	Seed uint64
	// Cwd is the header's working directory.
	Cwd string
	// ForkOneIn makes one entry in this many branch from an earlier entry. Zero means 6, which gives a session with a leaf for every few entries; a large value gives a long conversation with a few branches.
	ForkOneIn int
}

// treeTexts holds text that JSON encoders treat differently: HTML-sensitive characters, line and paragraph separators, non-ASCII letters, astral emoji, control characters and backslashes.
var treeTexts = []string{
	"plain words",
	"<tag> & </tag>",
	"line\u2028separator\u2029paragraph",
	"caf\u00e9 na\u00efve \u4e2d\u6587",
	"emoji \U0001F600 \U0001F4A9",
	"tab\tnewline\nreturn\rbell\u0007",
	`back\slash "quoted"`,
	"",
}

// GenerateTree writes a session whose entries branch from earlier entries and include compactions, branch summaries, labels, custom entries, custom messages, session names, model and thinking-level changes, tool calls and results, extra members Pi does not read, and members written in an unusual order. Every entry's JSON is written by hand, so the file holds spellings JSON.stringify would not produce (escaped non-ASCII, exponent numbers, spaces).
func GenerateTree(w io.Writer, opts TreeOptions) error {
	rng := rand.New(rand.NewPCG(opts.Seed, opts.Seed^0x5eed))
	forkOneIn := opts.ForkOneIn
	if forkOneIn == 0 {
		forkOneIn = 6
	}
	out := bufio.NewWriter(w)
	clock := time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)
	cwd, _ := json.Marshal(opts.Cwd)
	if _, err := fmt.Fprintf(out, `{"type":"session","version":3,"id":"%08x-0000-4000-8000-000000000000","timestamp":"%s","cwd":%s}`+"\n", rng.Uint32(), clock.Format(time.RFC3339Nano), cwd); err != nil {
		return err
	}
	var ids []string
	leaf := "null"
	text := func() string {
		value, _ := json.Marshal(treeTexts[rng.IntN(len(treeTexts))] + fmt.Sprintf(" %d", rng.IntN(1000)))
		return string(value)
	}
	// emit writes one entry's line. The JSON arguments are spliced into text whose own quotes sit around them, so each line is written with
	// Fprintf rather than built as a string first: the values are whole JSON values (text() marshals), never the inside of a quoted string.
	emit := func(format string, args ...any) error {
		_, err := fmt.Fprintf(out, format+"\n", args...)
		return err
	}
	for n := range opts.Entries {
		id := fmt.Sprintf("%08x", rng.Uint32())
		parent := leaf
		if len(ids) > 2 && rng.IntN(forkOneIn) == 0 {
			parent = `"` + ids[rng.IntN(len(ids))] + `"`
		}
		clock = clock.Add(time.Duration(rng.IntN(5000)) * time.Millisecond)
		stamp := clock.Format("2006-01-02T15:04:05.000Z")
		millis := clock.UnixMilli()
		base := fmt.Sprintf(`"id":"%s","parentId":%s,"timestamp":"%s"`, id, parent, stamp)
		var err error
		switch kind := rng.IntN(16); {
		case kind < 4 || n == 0:
			err = emit(`{"type":"message",%s,"message":{"role":"user","content":[{"type":"text","text":%s}],"timestamp":%d}}`, base, text(), millis)
		case kind < 7:
			err = emit(`{"type":"message",%s,"message":{"role":"assistant","content":[{"type":"thinking","thinking":%s},{"type":"text","text":%s},{"type":"toolCall","id":"call_%s","name":"read","arguments":{"path":%s,"limit":1e2}}],"api":"openai-completions","provider":"longsession","model":"scripted","usage":{"input":%d,"output":7,"cacheRead":0,"cacheWrite":0,"totalTokens":%d,"cost":{"input":0.5,"output":0,"cacheRead":0,"cacheWrite":0,"total":0.5}},"stopReason":"toolUse","timestamp":%d}}`, base, text(), text(), id, text(), n*10, n*10+7, millis)
		case kind < 9:
			err = emit(`{"type":"message",%s,"message":{"role":"toolResult","toolCallId":"call_%s","toolName":"read","content":[{"type":"text","text":%s}],"details":{"extra":[1,2.50,-0]},"isError":%t,"timestamp":%d}}`, base, id, text(), rng.IntN(3) == 0, millis)
		case kind == 9 && len(ids) > 1:
			err = emit(`{"type":"compaction",%s,"summary":%s,"firstKeptEntryId":"%s","tokensBefore":%d,"details":{"readFiles":["a.go"],"modifiedFiles":[]}}`, base, text(), ids[rng.IntN(len(ids))], 1000+n)
		case kind == 10 && len(ids) > 0:
			err = emit(`{"type":"branch_summary",%s,"fromId":"%s","summary":%s}`, base, ids[rng.IntN(len(ids))], text())
		case kind == 11 && len(ids) > 0:
			label := text()
			if rng.IntN(3) == 0 {
				label = "null"
			}
			err = emit(`{"type":"label",%s,"targetId":"%s","label":%s}`, base, ids[rng.IntN(len(ids))], label)
		case kind == 12:
			err = emit(`{"type":"custom",%s,"customType":"probe","data":{"z":1,"a":[true,null],"s":%s}}`, base, text())
		case kind == 13:
			err = emit(`{"type":"custom_message",%s,"customType":"note","content":%s,"display":%t,"details":{"k":"v"}}`, base, text(), rng.IntN(2) == 0)
		case kind == 14:
			err = emit(`{ "timestamp" : "%s", "type":"session_info","id":"%s","parentId":%s,"name":%s, "unread":"\u00e9" }`, stamp, id, parent, text())
		default:
			if rng.IntN(2) == 0 {
				err = emit(`{"type":"model_change",%s,"provider":"longsession","modelId":"scripted-%d"}`, base, rng.IntN(3))
			} else {
				err = emit(`{"type":"thinking_level_change",%s,"thinkingLevel":"%s"}`, base, []string{"off", "low", "high"}[rng.IntN(3)])
			}
		}
		if err != nil {
			return err
		}
		ids = append(ids, id)
		leaf = `"` + id + `"`
	}
	return out.Flush()
}
