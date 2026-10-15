// SPDX-License-Identifier: MIT

package codingagent

import (
	"math/rand/v2"
	"strings"
	"testing"
	"time"

	"github.com/MichaelKinsy/PiG/extensions/sdk/json"
	"github.com/MichaelKinsy/PiG/internal/testenv"
)

// decodedToolCallCount is the count the accounting decode gives, the reference scanToolCallCount must equal when it decides.
func decodedToolCallCount(data []byte) (int, error) {
	var blocks []struct {
		Type string `json:"type"`
	}
	if err := json.Unmarshal(data, &blocks); err != nil {
		return 0, err
	}
	count := 0
	for _, block := range blocks {
		if block.Type == "toolCall" || block.Type == "tool_use" {
			count++
		}
	}
	return count, nil
}

func checkScanAgainstDecode(t *testing.T, data []byte) (decided bool) {
	t.Helper()
	if !json.Valid(data) || !strings.HasPrefix(strings.TrimLeft(string(data), " \t\r\n"), "[") {
		return false
	}
	data = []byte(strings.TrimSpace(string(data)))
	scanned, ok := scanToolCallCount(data)
	want, err := decodedToolCallCount(data)
	if ok && (err != nil || scanned != want) {
		t.Fatalf("scan of %s = %d, decode = %d (%v)", data, scanned, want, err)
	}
	return ok
}

func TestScanToolCallCountDecidesOrdinaryContentAsTheDecodeDoes(t *testing.T) {
	ordinary := []string{
		`[]`, ` [ ] `, `[{"type":"text","text":"a \"q\" {x} [y]"}]`,
		`[{"type":"thinking","thinking":"t","thinkingSignature":"s"},{"type":"toolCall","id":"c1","name":"read","arguments":{"path":"a","n":[1,2,{"type":"toolCall"}]}}]`,
		`[{"type":"tool_use"},{"type":"toolCall"},null,{"text":"x"},{}]`,
		`[{"type":"toolCall","type":"text"}]`, `[{"type":"text","type":"toolCall"}]`,
		`[ { "type" : "toolCall" , "arguments" : { } } , { "type" : "image" , "data" : "AAAA" } ]`,
		`[{"id":1.5e3,"ok":true,"no":false,"z":null,"type":"toolCall"}]`,
	}
	for _, data := range ordinary {
		if !checkScanAgainstDecode(t, []byte(data)) {
			t.Errorf("scan does not decide ordinary content %s", data)
		}
	}
	undecided := []string{`[{"Type":"toolCall"}]`, `[{"\u0074ype":"toolCall"}]`, `[{"type":"tool\u0043all"}]`, `[{"type":1}]`, `[{"type":null}]`, `[1]`, `["s"]`, `[[]]`}
	for _, data := range undecided {
		if checkScanAgainstDecode(t, []byte(data)) {
			t.Errorf("scan decides %s, which only the decode answers exactly", data)
		}
	}
}

func TestScanToolCallCountMatchesTheDecodeOnGeneratedArrays(t *testing.T) {
	keys := []string{`"type"`, `"Type"`, `"TYPE"`, `"\u0074ype"`, `"text"`, `"id"`, `"x\"y"`, `"typ"`, `"types"`}
	values := []string{`"toolCall"`, `"tool_use"`, `"text"`, `null`, `1`, `-0.5e-3`, `true`, `[1,{"type":"toolCall"}]`, `{"type":"toolCall","a":[]}`, `"to\"olCall"`, `"\u0074oolCall"`, `""`, `"tool_use "`}
	space := []string{"", " ", "\n", "\t "}
	rng := rand.New(rand.NewPCG(1, 2))
	pick := func(from []string) string { return from[rng.IntN(len(from))] }
	decided := 0
	for range 20000 {
		var b strings.Builder
		b.WriteString("[" + pick(space))
		for e := range rng.IntN(4) {
			if e > 0 {
				b.WriteString(pick(space) + "," + pick(space))
			}
			switch rng.IntN(8) {
			case 0:
				b.WriteString(pick([]string{"null", "1", `"s"`, "[]", "true"}))
			default:
				b.WriteString("{" + pick(space))
				for m := range rng.IntN(4) {
					if m > 0 {
						b.WriteString(pick(space) + "," + pick(space))
					}
					b.WriteString(pick(keys) + pick(space) + ":" + pick(space) + pick(values))
				}
				b.WriteString(pick(space) + "}")
			}
		}
		b.WriteString(pick(space) + "]")
		if checkScanAgainstDecode(t, []byte(b.String())) {
			decided++
		}
	}
	if decided < 2000 {
		t.Fatalf("scan decided only %d of 20000 generated arrays", decided)
	}
}

func FuzzScanToolCallCount(f *testing.F) {
	for _, seed := range []string{`[]`, `[{"type":"toolCall"}]`, `[{"type":"tool_use","x":{"y":["]"]}},null]`, `[{"Type":"toolCall"}]`} {
		f.Add([]byte(seed))
	}
	f.Fuzz(func(t *testing.T, data []byte) { checkScanAgainstDecode(t, data) })
}

// A string member dense with escapes is skipped in one pass: a rescan to the closing quote after each escape took minutes on a line of
// escaped JSON text, a common tool result.
func TestScanToolCallCountSkipsEscapedTextInLinearTime(t *testing.T) {
	escaped := strings.Repeat(`line\n`, 300_000) + `\"`
	data := []byte(`[{"type":"toolResult","text":"` + escaped + `"},{"type":"toolCall","arguments":{"a":"` + escaped + `"}}]`)
	start := time.Now()
	count, ok := scanToolCallCount(data)
	elapsed := time.Since(start)
	testenv.TimingGateFatal(t, elapsed > 2*time.Second, "scanning %d bytes took %v", len(data), elapsed)
	if want, err := decodedToolCallCount(data); !ok || err != nil || count != want {
		t.Fatalf("scan = %d, %v; decode = %d, %v", count, ok, want, err)
	}
}
