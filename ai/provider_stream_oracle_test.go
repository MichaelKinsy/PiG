package ai

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"math/rand"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"reflect"
	"regexp"
	"strconv"
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/internal/coding/pigversion"
)

// providerOracleTransport, when set, is the transport both providers are asked to use (the Codex provider tries a WebSocket first otherwise); PI_ORACLE_TRANSPORT carries it to Pi.
var providerOracleTransport Transport

func providerOracleStreamOptions() StreamOptions {
	return StreamOptions{MaxRetries: new(0), Transport: providerOracleTransport}
}

type providerOracleCase struct {
	Chunks     []string `json:"chunks"`
	Status     int      `json:"status"`
	Drop       bool     `json:"drop"`
	StatusText string   `json:"statusText"`
}

var generatedFunctionCallID = regexp.MustCompile(`"(\w*)_\d{13}_(\d+)"`)

// providerStreamOracle runs Pi's provider (the installed pi-ai dist, over a replaced global fetch) and Pig's provider over the same response bodies, served by a real HTTP server, and compares the event types and the final message. pi names the Pi api file, its lazy export, the model api and the model provider; newProvider builds Pig's provider against a base URL. d120 compares a tool call id that Pig generated for a call streamed without one (D120) as the empty id Pi keeps.
func providerStreamOracle(t *testing.T, pi [4]string, d120 bool, bodies [][][]byte, newProvider func(baseURL string) Provider) {
	t.Helper()
	// Every body also runs with its integers written as floats (12.0; jsjson.NormalizeIntegers) with truncated UTF-8 sequences (one U+FFFD per maximal subpart, as TextDecoder) with NUL, solidus, backspace, form-feed and \\u escapes inside strings, with SSE id/retry/comment/unknown fields, with a duplicated type member (the last wins), and with trailing text after the JSON. PIG_ORACLE_KEY_CASE=all|type|number selects other variants instead (member-name case differs from Pi: open question).
	modes := []string{"float", "utf8", "escapes", "sse", "dup", "garbage"}
	if mode := os.Getenv("PIG_ORACLE_KEY_CASE"); mode != "" {
		modes = []string{mode}
	}
	base := bodies[:len(bodies):len(bodies)]
	for _, mode := range modes {
		bodies = append(bodies, keyCaseVariants(base, mode)...)
	}
	providerResponseOracle(t, pi, d120, bodies, nil, false, newProvider)
}

var jsonIntPattern = regexp.MustCompile(`:[0-9]+[,}\]]`)

var jsonKeyPattern = regexp.MustCompile(`"([A-Za-z_]+)"\s*:`)

// keyCaseVariants returns a copy of every body with one JSON member name recased everywhere it occurs (ASCII, so chunk boundaries stay put). JavaScript reads members by exact name; Go's encoding/json matches struct fields case-insensitively, so a `TYPE` or `Delta` member must stay unread by Pig as by Pi.
func keyCaseVariants(bodies [][][]byte, mode string) [][][]byte {
	random := rand.New(rand.NewSource(20261116))
	variants := make([][][]byte, 0, len(bodies))
	for _, chunks := range bodies {
		whole := bytes.Join(chunks, nil)
		var names []string
		for _, match := range jsonKeyPattern.FindAllSubmatch(whole, -1) {
			names = append(names, string(match[1]))
		}
		variant := make([][]byte, len(chunks))
		if mode == "extra" || mode == "dup" {
			// extra: an unknown member holding a number past 2^53 (selectable only: Pi's Object.assign copies an unknown member of a pi-messages toolcall_end onto the block, Pig's typed block does not); dup: a duplicated member, the last one wins on both sides.
			for i, chunk := range chunks {
				if mode == "extra" {
					chunk = bytes.ReplaceAll(chunk, []byte("{\""), []byte(`{"zz":123456789012345678901234567890,"`))
				}
				variant[i] = chunk
				if mode == "dup" {
					variant[i] = bytes.ReplaceAll(chunk, []byte(`"type":"`), []byte(`"type":"junk","type":"`))
				}
			}
			variants = append(variants, variant)
			continue
		}
		if mode == "huge" {
			// One text value of 300 KB (past common 64 KB line and 1 MB buffer limits when repeated across events).
			big := bytes.Repeat([]byte("x"), 300_000)
			if random.Intn(40) != 0 { // the payload goes to Node as one string: keep it small
				copy(variant, chunks)
				variants = append(variants, variant)
				continue
			}
			for i, chunk := range chunks {
				chunk = bytes.ReplaceAll(chunk, []byte(`"hi"`), append(append([]byte(`"`), big...), '"'))
				chunk = bytes.ReplaceAll(chunk, []byte(`"hello"`), append(append([]byte(`"`), big...), '"'))
				variant[i] = bytes.ReplaceAll(chunk, []byte(`"plain"`), append(append([]byte(`"`), big...), '"'))
			}
			variants = append(variants, variant)
			continue
		}
		if mode == "garbage" {
			// Trailing text after the JSON of a data line. (A non-object top-level value such as `data: 5` makes Pi's pi-messages provider yield an event without a type; Pig skips it. Not mirrored: no backend sends it.)
			for i, chunk := range chunks {
				variant[i] = bytes.ReplaceAll(chunk, []byte("}\n\n"), []byte("} x\n\n"))
			}
			variants = append(variants, variant)
			continue
		}
		if mode == "sse" {
			// Fields and comments a server may add to an event: id, retry, a comment line, an unknown field.
			for i, chunk := range chunks {
				chunk = bytes.ReplaceAll(chunk, []byte("data: {"), []byte("id: 7\nretry: 100\n: note\nfoo: bar\ndata: {"))
				variant[i] = bytes.ReplaceAll(chunk, []byte("data:{"), []byte(": note\nid:\ndata:{"))
			}
			variants = append(variants, variant)
			continue
		}
		if mode == "escapes" {
			// Control and other escapes inside strings: NUL, a solidus, a backspace, a form feed.
			for i, chunk := range chunks {
				chunk = bytes.ReplaceAll(chunk, []byte(`\n`), []byte(`\u0000\/\b\f`))
				variant[i] = bytes.ReplaceAll(chunk, []byte(`"hi"`), []byte(`"\u0068\u0069"`))
			}
			variants = append(variants, variant)
			continue
		}
		if mode == "utf8" {
			// Truncated UTF-8 sequences: the WHATWG decoder (fetch) answers one U+FFFD per maximal subpart.
			for i, chunk := range chunks {
				chunk = bytes.ReplaceAll(chunk, []byte("𝄞"), []byte{0xf0, 0x9d, 0x84})
				variant[i] = bytes.ReplaceAll(chunk, []byte("日"), []byte{0xe6, 0x97})
			}
			if len(variant) > 0 && random.Intn(2) == 0 {
				variant[0] = append([]byte("\ufeff"), variant[0]...)
			}
			variants = append(variants, variant)
			continue
		}
		if mode == "number" || mode == "float" {
			// Integers written as JSON numbers that JavaScript reads the same: 12.0, 1.2e1, 12E0, 120e-1.
			form := random.Intn(4)
			if mode == "float" {
				form = 0
			}
			for i, chunk := range chunks {
				variant[i] = jsonIntPattern.ReplaceAllFunc(chunk, func(match []byte) []byte {
					digits := match[1 : len(match)-1]
					forms := []string{string(digits) + ".0", "", string(digits) + "E0", string(digits) + "0e-1"}
					if len(digits) == 2 {
						forms[1] = string(digits[:1]) + "." + string(digits[1:]) + "e1"
					}
					if forms[form] == "" {
						return match
					}
					return []byte(":" + forms[form] + string(match[len(match)-1:]))
				})
			}
			variants = append(variants, variant)
			continue
		}
		if len(names) == 0 {
			copy(variant, chunks)
			variants = append(variants, variant)
			continue
		}
		name := names[random.Intn(len(names))]
		if mode == "type" {
			name = "type"
		}
		recased := strings.ToUpper(name)
		if random.Intn(2) == 0 {
			recased = strings.ToUpper(name[:1]) + name[1:]
		}
		if recased == name {
			recased = strings.ToUpper(name)
		}
		for i, chunk := range chunks {
			variant[i] = bytes.ReplaceAll(chunk, []byte(`"`+name+`"`), []byte(`"`+recased+`"`))
		}
		variants = append(variants, variant)
	}
	return variants
}

// providerResponseOracle is providerStreamOracle with an HTTP status per body (nil: every response is 200 event-stream; otherwise the body is JSON text with that status). drop ends every body with a broken connection (Pi: the body stream errors with undici's "terminated") instead of a clean end; Pig's message for that error is "terminated (cause: <Go error>)" (D144), and every other error text must equal Pi's.
func providerResponseOracle(t *testing.T, pi [4]string, d120 bool, bodies [][][]byte, statuses []int, drop bool, newProvider func(baseURL string) Provider) {
	t.Helper()
	cases := make([]providerOracleCase, len(bodies))
	for i, chunks := range bodies {
		cases[i].Chunks = []string{}
		cases[i].Status = http.StatusOK
		cases[i].Drop = drop
		if statuses != nil {
			cases[i].Status = statuses[i]
			// The reason phrase net/http writes, which fetch reports as statusText.
			if cases[i].StatusText = http.StatusText(statuses[i]); cases[i].StatusText == "" {
				cases[i].StatusText = fmt.Sprintf("status code %d", statuses[i])
			}
		}
		for _, chunk := range chunks {
			cases[i].Chunks = append(cases[i].Chunks, base64.StdEncoding.EncodeToString(chunk))
		}
	}
	payload, err := json.Marshal(cases)
	if err != nil {
		t.Fatal(err)
	}
	cmd := exec.CommandContext(t.Context(), "node", "--disable-warning=ExperimentalWarning", "testdata/provider_stream_oracle.mjs", pigversion.UpstreamPackageVersion, pi[0], pi[1], pi[2], pi[3])
	cmd.Stdin = bytes.NewReader(payload)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	output, err := cmd.Output()
	if err != nil {
		t.Fatalf("Pi oracle: %v\n%.2000s", err, &stderr)
	}
	var expected []struct {
		Events  []string `json:"events"`
		Message struct {
			Content      []map[string]any `json:"content"`
			StopReason   string           `json:"stopReason"`
			Usage        map[string]any   `json:"usage"`
			ErrorMessage *string          `json:"errorMessage"`
			ResponseID   *string          `json:"responseId"`
		} `json:"message"`
	}
	if err := json.Unmarshal(output, &expected); err != nil {
		t.Fatal(err)
	}
	canonical := func(value any) any {
		encoded, err := json.Marshal(value)
		if err != nil {
			t.Fatal(err)
		}
		if d120 {
			encoded = generatedFunctionCallID.ReplaceAll(encoded, []byte(`""`))
		}
		// A function call without a usable id gets `${name}_${Date.now()}_${++counter}` (google-shared.ts); the millisecond clock is not comparable.
		// The counter is process-wide, so it is compared as the offset from the first generated id of the message.
		first := -1
		encoded = generatedFunctionCallID.ReplaceAllFunc(encoded, func(match []byte) []byte {
			parts := generatedFunctionCallID.FindSubmatch(match)
			counter, _ := strconv.Atoi(string(parts[2]))
			if first < 0 {
				first = counter
			}
			return fmt.Appendf(nil, `"%s_<now>_+%d"`, parts[1], counter-first)
		})
		var out any
		_ = json.Unmarshal(encoded, &out)
		return out
	}
	user := NormalizeContext(Context{Messages: []Message{UserMessage{Content: UserContentBlocks{TextContent{Text: "hi"}}}}})
	// A real server, so the body is the observed one the provider's SDK pipeline reads.
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var index int
		_, _ = fmt.Sscanf(r.URL.Path, "/case/%d/", &index)
		status := http.StatusOK
		w.Header().Set("Content-Type", "text/event-stream")
		if statuses != nil {
			status = statuses[index]
			w.Header().Set("Content-Type", "application/json")
		}
		w.WriteHeader(status)
		for _, chunk := range bodies[index] {
			_, _ = w.Write(chunk)
			w.(http.Flusher).Flush()
		}
		if drop {
			conn, _, err := http.NewResponseController(w).Hijack()
			if err == nil {
				_ = conn.Close()
			}
		}
	}))
	defer server.Close()
	differing, blocks, errors := 0, 0, 0
	for i, chunks := range bodies {
		provider := newProvider(fmt.Sprintf("%s/case/%d", server.URL, i))
		// No wall-clock deadline: every body is finite and the server returns after writing it, so each stream ends; a stream that does not
		// is a hang the package -timeout reports with every goroutine's stack. A 10 s deadline here failed healthy cases on a loaded host.
		ctx := t.Context()
		stream, err := provider.Stream(ctx, user, providerOracleStreamOptions())
		var events []string
		var result *AssistantMessage
		if err != nil {
			// A provider that fails before it has a stream returns the error; the agent loop turns it into an error message, so it is compared as the failure event Pi's provider pushes.
			stream = NewAssistantMessageEventStream()
			failed := &AssistantMessage{StopReason: StopReasonError, ErrorMessage: err.Error()}
			_ = stream.Push(ErrorEvent{Reason: StopReasonError, Error: failed})
		}
		for event := range stream.Events(ctx) {
			events = append(events, string(event.EventType()))
		}
		result = stream.Result()
		want := expected[i]
		problem := ""
		gotContent := canonical(result.Content)
		wantContent := canonical(want.Message.Content)
		blocks += len(want.Message.Content)
		if want.Message.ErrorMessage != nil {
			errors++
		}
		switch {
		case !reflect.DeepEqual(events, want.Events):
			problem = fmt.Sprintf("events %v, want %v (stopReason %s, error %q, Stream() error %v)", events, want.Events, result.StopReason, result.ErrorMessage, err)
		case string(result.StopReason) != want.Message.StopReason:
			problem = fmt.Sprintf("stopReason %s, want %s", result.StopReason, want.Message.StopReason)
		case !reflect.DeepEqual(gotContent, wantContent) && (len(result.Content) != 0 || len(want.Message.Content) != 0):
			problem = fmt.Sprintf("content %v, want %v", gotContent, wantContent)
		case (result.ErrorMessage == "") != (want.Message.ErrorMessage == nil) || (want.Message.ErrorMessage != nil && !providerOracleErrorMatches(result.ErrorMessage, *want.Message.ErrorMessage, drop)):
			wantError := "<none>"
			if want.Message.ErrorMessage != nil {
				wantError = *want.Message.ErrorMessage
			}
			problem = fmt.Sprintf("error %q, want %q", result.ErrorMessage, wantError)
		case (result.ResponseID == "") != (want.Message.ResponseID == nil) || (want.Message.ResponseID != nil && result.ResponseID != *want.Message.ResponseID):
			problem = fmt.Sprintf("responseId %q, want %v", result.ResponseID, want.Message.ResponseID)
		default:
			usage := canonical(result.Usage).(map[string]any)
			for _, key := range []string{"input", "output", "cacheRead", "cacheWrite", "totalTokens"} {
				if usage[key] != want.Message.Usage[key] {
					problem = fmt.Sprintf("usage.%s %v, want %v", key, usage[key], want.Message.Usage[key])
				}
			}
			if want.Message.Usage["reasoning"] != nil && want.Message.Usage["reasoning"] != float64(0) && canonical(result.Usage).(map[string]any)["reasoning"] != want.Message.Usage["reasoning"] {
				problem = fmt.Sprintf("usage.reasoning %v, want %v", canonical(result.Usage).(map[string]any)["reasoning"], want.Message.Usage["reasoning"])
			}
		}
		if problem != "" {
			differing++
			if differing <= 5 {
				var raw []byte
				for _, c := range chunks {
					raw = append(raw, c...)
				}
				t.Errorf("case %d: %s\n  body %q", i, problem, raw)
			}
		}
	}
	t.Logf("%d bodies, %d content blocks, %d errors, %d differing", len(bodies), blocks, errors, differing)
	if differing > 0 {
		t.Fatalf("%d bodies differ from Pi", differing)
	}
}

// providerOracleErrorMatches reports whether Pig's error text is Pi's. For a dropped connection Pi's undici error is "terminated", which Pig reports as "terminated (cause: <Go error>)" (D144).
func providerOracleErrorMatches(got, want string, drop bool) bool {
	return got == want || (drop && want == "terminated" && strings.HasPrefix(got, "terminated (cause: "))
}
