package mcpext

import (
	"bytes"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"math/rand"
	"reflect"
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/internal/pioracle"
	"github.com/MichaelKinsy/PiG/mcp"
)

// createMcpToolName and convertMcpResult against Pi's tools.js: text, image, audio, resource-link, text and binary embedded resources (mime types with parameters and case, URIs with and
// without extensions), long multi-byte text past the 20 KiB limit, empty content with structured content, error results with and without text, a saver that fails, and tool names
// of every length with taken-name callbacks.
func TestConvertMcpResultMatchesPi(t *testing.T) {
	rng := rand.New(rand.NewSource(21))
	pick := func(items ...string) string { return items[rng.Intn(len(items))] }
	long := func() string {
		unit := pick("abc\n", "日本語テキスト\n", "emoji 😀 line\n", "x", "line with spaces and tabs\t\n")
		return strings.Repeat(unit, 1+rng.Intn(9000))
	}
	text := func() string {
		if rng.Intn(6) == 0 {
			return long()
		}
		return pick("", "hello", "multi\nline", "日本語", "😀", "  padded  ", strings.Repeat("a", 20*1024), strings.Repeat("a", 20*1024+1), strings.Repeat("é", 10*1024+1))
	}
	b64 := func() string {
		raw := make([]byte, rng.Intn(3000))
		rng.Read(raw)
		s := base64.StdEncoding.EncodeToString(raw)
		switch rng.Intn(4) {
		case 0:
			return strings.TrimRight(s, "=")
		case 1:
			return strings.NewReplacer("+", "-", "/", "_").Replace(s)
		}
		return s
	}
	mimes := []string{"text/plain", "TEXT/Markdown; charset=utf-8", "application/json", "application/ld+json", "application/atom+xml", "application/xml", "image/png", "IMAGE/JPEG", "application/pdf", "application/octet-stream", " text/html ", ""}
	uris := []string{"file:///a/b.png", "file:///a/b", "https://x.test/data.JSON?x=1", "mcp://srv/res.tar.gz", "not a url.txt", "file:///a/b.verylongext", "file:///a/b.", "x:y.z9", "file:///dir.d/file", "a b.c"}
	block := func() map[string]any {
		switch rng.Intn(6) {
		case 0:
			return map[string]any{"type": "text", "text": text()}
		case 1:
			return map[string]any{"type": "image", "data": b64(), "mimeType": pick("image/png", "image/jpeg")}
		case 2:
			return map[string]any{"type": "audio", "data": b64(), "mimeType": "audio/wav"}
		case 3:
			b := map[string]any{"type": "resource_link", "uri": pick(uris...), "name": pick("n", "name with space", "日本")}
			if rng.Intn(2) == 0 {
				b["title"] = pick("Title", "", "T \"q\"")
			}
			if rng.Intn(2) == 0 {
				b["description"] = pick("desc", "", "multi\nline")
			}
			if rng.Intn(2) == 0 {
				b["mimeType"] = pick(mimes...)
			}
			if rng.Intn(2) == 0 {
				b["size"] = []float64{0, 1, 1023, 1024, 1536, 10240, 5 * 1024 * 1024, 3.5e9, 1e15}[rng.Intn(9)]
			}
			return b
		case 4:
			r := map[string]any{"uri": pick(uris...), "text": text()}
			if rng.Intn(2) == 0 {
				r["mimeType"] = pick(mimes...)
			}
			return map[string]any{"type": "resource", "resource": r}
		default:
			r := map[string]any{"uri": pick(uris...), "blob": b64()}
			if rng.Intn(3) != 0 {
				r["mimeType"] = pick(mimes...)
			}
			return map[string]any{"type": "resource", "resource": r}
		}
	}
	type probe struct {
		Server, Tool string
		Result       map[string]any
		Readable     bool
		FailSave     bool
	}
	var probes []probe
	for range 1500 {
		result := map[string]any{}
		var content []any
		if rng.Intn(7) != 0 {
			for range 1 + rng.Intn(4) {
				content = append(content, block())
			}
		}
		if content == nil {
			content = []any{}
		}
		result["content"] = content
		if rng.Intn(3) == 0 {
			result["structuredContent"] = pick(`{"a":1,"b":[1,2,{"c":"d"}]}`, `{"text":"hi"}`, `[1,2,3]`, `"str"`, `{}`)
			var sc any
			_ = json.Unmarshal([]byte(result["structuredContent"].(string)), &sc)
			result["structuredContent"] = sc
		}
		switch rng.Intn(5) {
		case 0:
			result["isError"] = true
		case 1:
			result["isError"] = false
		}
		if rng.Intn(3) == 0 {
			result["_meta"] = map[string]any{"k": "v"}
		}
		probes = append(probes, probe{Server: pick("srv", "my server", "日本", "a.b"), Tool: pick("tool", "list-things", "x y"), Result: result, Readable: rng.Intn(2) == 0, FailSave: rng.Intn(6) == 0})
	}
	type nameProbe struct {
		Server, Tool string
		Taken        string
		TakeSuffix   bool
	}
	var names []nameProbe
	for range 600 {
		server := pick("srv", "github", "a.b-c", "日本語", "😀", strings.Repeat("s", 30), strings.Repeat("s", 80), "")
		tool := pick("list", "list-issues", "x y", strings.Repeat("t", 40), strings.Repeat("t", 70), "é", "")
		names = append(names, nameProbe{Server: server, Tool: tool, Taken: pick("", "mcp__srv__list"), TakeSuffix: rng.Intn(2) == 0})
	}
	type packed struct {
		Content    []map[string]any `json:"content"`
		Details    map[string]any   `json:"details"`
		Structured string           `json:"structured"`
		IsError    bool             `json:"isError"`
		Err        string           `json:"err"`
	}
	var want struct {
		Results []packed `json:"results"`
		Names   []string `json:"names"`
	}
	jsProbes := make([]map[string]any, len(probes))
	for i, p := range probes {
		jsProbes[i] = map[string]any{"server": p.Server, "tool": p.Tool, "result": p.Result, "readable": p.Readable, "failSave": p.FailSave}
	}
	jsNames := make([]map[string]any, len(names))
	for i, n := range names {
		jsNames[i] = map[string]any{"server": n.Server, "tool": n.Tool, "taken": n.Taken, "takeSuffix": n.TakeSuffix}
	}
	input := map[string]any{"probes": jsProbes, "names": jsNames}
	pioracle.Run(t, `
const m = await load("pi-coding-agent/extensions/mcp/tools.js");
const { createHash } = await import("node:crypto");
const saver = (fail) => async (data, ext) => {
	if (fail) throw new Error("disk full");
	return "/saved/" + createHash("sha256").update(data).digest("hex").slice(0, 8) + ext;
};
const results = [];
for (const p of input.probes) {
	try {
		const r = await m.convertMcpResult(p.server, p.tool, p.result, { saveOutput: saver(p.failSave), readableResources: p.readable });
		results.push({ content: r.content, details: r.details, structured: JSON.stringify(r.structuredContent), isError: r.isError === true, err: "" });
	} catch (e) { results.push({ content: [], details: {}, structured: "", isError: false, err: String(e && e.message) }); }
}
const names = input.names.map((n) => m.createMcpToolName(n.server, n.tool, (name) => (n.taken !== "" && name === n.taken) || (n.takeSuffix && name.endsWith("_x"))));
emit({ results, names });
`, input, &want)
	bad := 0
	report := func(format string, args ...any) {
		if bad++; bad <= 6 {
			t.Errorf(format, args...)
		}
	}
	for i, p := range probes {
		raw, _ := json.Marshal(p.Result)
		var result mcp.CallToolResult
		if err := json.Unmarshal(raw, &result); err != nil {
			t.Fatal(err)
		}
		result.Raw = raw // the client keeps the result object as the server sent it
		fail := p.FailSave
		got, err := ConvertMcpResult(p.Server, p.Tool, &result, ConvertMcpResultOptions{ReadableResources: p.Readable, SaveOutput: func(data []byte, ext string) (string, error) {
			if fail {
				return "", errors.New("disk full")
			}
			sum := sha256.Sum256(data)
			return "/saved/" + hex.EncodeToString(sum[:])[:8] + ext, nil
		}})
		w := want.Results[i]
		if err != nil {
			report("probe %d: error %v, Pi err %q", i, err, w.Err)
			continue
		}
		var gotContent []map[string]any
		contentRaw, _ := json.Marshal(got.Content)
		_ = json.Unmarshal(contentRaw, &gotContent)
		var gotDetails map[string]any
		detailsRaw, _ := json.Marshal(got.Details)
		_ = json.Unmarshal(detailsRaw, &gotDetails)
		var compact bytes.Buffer
		_ = json.Compact(&compact, got.StructuredContent)
		if gotContent == nil {
			gotContent = []map[string]any{}
		}
		if !reflect.DeepEqual(gotContent, w.Content) || !reflect.DeepEqual(gotDetails, w.Details) || compact.String() != w.Structured || got.IsError != w.IsError {
			wantContent, _ := json.Marshal(w.Content)
			gotSorted, _ := json.Marshal(gotContent)
			report("probe %d (%s/%s readable=%v failSave=%v):\n content: %s\n details: got %s want %v\n structured: %s\n isError got %v want %v", i, p.Server, p.Tool, p.Readable, p.FailSave, firstDiff(string(gotSorted), string(wantContent)), detailsRaw, w.Details, firstDiff(compact.String(), w.Structured), got.IsError, w.IsError)
		}
	}
	for i, n := range names {
		got := CreateMcpToolName(n.Server, n.Tool, func(name string) bool {
			return (n.Taken != "" && name == n.Taken) || (n.TakeSuffix && strings.HasSuffix(name, "_x"))
		})
		if got != want.Names[i] {
			report("name %+v: got %q, Pi %q", n, got, want.Names[i])
		}
	}
	if bad > 6 {
		t.Errorf("%d mismatches", bad)
	}
}

func firstDiff(got, want string) string {
	g, w := []rune(got), []rune(want)
	for i := 0; i < min(len(g), len(w)); i++ {
		if g[i] != w[i] {
			lo := max(0, i-60)
			return fmt.Sprintf("at rune %d: got %q want %q", i, string(g[lo:min(len(g), i+60)]), string(w[lo:min(len(w), i+60)]))
		}
	}
	return fmt.Sprintf("lengths %d vs %d", len(g), len(w))
}
