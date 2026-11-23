package tools

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"strings"
	"testing"
)

// referenceGrepEvent is the event parser before it stopped allocating a struct and strings per line: encoding/json into rg's schema.
func referenceGrepEvent(line string) (isMatch bool, path string, lineNumber int, lineText string, ok bool) {
	type rgText struct {
		Text  *string `json:"text"`
		Bytes *string `json:"bytes"`
	}
	decode := func(t *rgText) string {
		if t == nil {
			return ""
		}
		if t.Text != nil {
			return *t.Text
		}
		if t.Bytes != nil {
			if data, err := base64.StdEncoding.DecodeString(*t.Bytes); err == nil {
				return decodeText(data)
			}
		}
		return ""
	}
	var event struct {
		Type string `json:"type"`
		Data struct {
			Path       *rgText  `json:"path"`
			LineNumber *float64 `json:"line_number"`
			Lines      *rgText  `json:"lines"`
		} `json:"data"`
	}
	if err := json.Unmarshal([]byte(line), &event); err != nil {
		return false, "", 0, "", false
	}
	isMatch = event.Type == "match"
	if !isMatch && event.Type != "context" {
		return false, "", 0, "", false
	}
	d := event.Data
	path = decode(d.Path)
	if path == "" || d.LineNumber == nil {
		return false, "", 0, "", false
	}
	return isMatch, path, int(*d.LineNumber), decode(d.Lines), true
}

func checkGrepEvent(t *testing.T, parser *grepEventParser, line string) {
	t.Helper()
	wantMatch, wantPath, wantNumber, wantText, wantOK := referenceGrepEvent(line)
	got, ok := parser.parse([]byte(line))
	if ok != wantOK {
		t.Fatalf("%q: parsed = %v, reference %v", line, ok, wantOK)
	}
	if !ok {
		return
	}
	if got.isMatch != wantMatch || string(got.path) != wantPath || got.lineNumber != wantNumber || string(got.lineText) != wantText {
		t.Fatalf("%q:\n got  %v %q %d %q\n want %v %q %d %q", line, got.isMatch, got.path, got.lineNumber, got.lineText, wantMatch, wantPath, wantNumber, wantText)
	}
}

var grepEventLines = []string{
	`{"type":"match","data":{"path":{"text":"./big.txt"},"lines":{"text":"line 0 of the file\n"},"line_number":1,"absolute_offset":0,"submatches":[{"match":{"text":"line"},"start":0,"end":4}]}}`,
	`{"type":"context","data":{"path":{"text":"a/b.txt"},"lines":{"text":"tab\there \"quoted\" back\\slash \u00e9 \ud83d\ude00 \ud83d lone\r\n"},"line_number":42,"absolute_offset":7,"submatches":[]}}`,
	`{"type":"match","data":{"path":{"bytes":"Zm9vLnR4dA=="},"lines":{"bytes":"YWJj/w=="},"line_number":3,"absolute_offset":0,"submatches":[]}}`,
	`{"type":"begin","data":{"path":{"text":"a.txt"}}}`,
	`{"type":"end","data":{"path":{"text":"a.txt"},"binary_offset":null,"stats":{"elapsed":{"secs":0,"nanos":1,"human":"0.000001s"},"searches":1}}}`,
	`{"type":"summary","data":{"elapsed_total":{"human":"0.01s","nanos":1,"secs":0},"stats":{"bytes_printed":1}}}`,
	`{"type":"match","data":{"path":{"text":""},"lines":{"text":"x"},"line_number":1}}`,
	`{"type":"match","data":{"path":{"text":"a"},"lines":{"text":"x"}}}`,
	`{"type":"match","data":{"path":{"text":"a"},"lines":{"text":"x"},"line_number":null}}`,
	`{"type":"match","data":{"path":{"text":"a"},"lines":{"text":"x"},"line_number":2.9}}`,
	`{"type":"match","data":{"path":{"text":"a"},"lines":{"text":"x"},"line_number":1e2}}`,
	`{"type":"match","data":{"path":{"text":"a"},"lines":{"text":"x"},"line_number":"7"}}`,
	`{"type":"match","data":{"path":{"text":"a"},"lines":null,"line_number":7}}`,
	`{"type":"match","data":{"path":{"text":null,"bytes":"YQ=="},"lines":{"text":"x"},"line_number":7}}`,
	`  {"type" : "context" , "data" : { "path" : { "text" : "a" } , "lines" : { "text" : "x" } , "line_number" : 1 } }  `,
	`{"type":"match","data":{"path":{"text":"a"},"lines":{"text":"x"},"line_number":1}} trailing`,
	`{"type":"match","data":{"path":{"text":"a"},"lines":{"text":"x"},"line_number":1}`,
	`{"type":"match","data":{"path":{"text":"a"},"lines":{"text":"x`,
	`{"type":"match","data":{"path":{"text":"a\x"},"lines":{"text":"x"},"line_number":1}}`,
	`{"type":"match","data":{"path":{"text":"a"},"lines":{"text":"x"},"line_number":01}}`,
	`{"type":"mat\u0063h","data":{"path":{"text":"a"},"lines":{"text":"x"},"line_number":1}}`,
	`{"type":"match","type":"context","data":{"path":{"text":"a"},"lines":{"text":"x"},"line_number":1}}`,
	`{"type":null,"data":{}}`,
	`{null:1}`,
	`{"type":"match","data":{"path":{"text":"a"},"lines":{"text":"x"},"line_number":1,"extra":[1,{"k":[true,false,null,"s",-1.5e3]},[]],"more":{}}}`,
	`{"type":"match","data":{"path":{"text":"a"},"lines":{"text":"x"},"line_number":1,"extra":[1,]}}`,
	`{"type":"match","data":{"path":{"text":"a"},"lines":{"text":"x"},"line_number":1,"extra":{"a":1,}}}`,
	`{"t\u0079pe":"match","d\u0061ta":{"p\u0061th":{"t\u0065xt":"a"},"line_number":1}}`,
	`{"type":7,"data":{}}`,
	`{"type":"match","data":[]}`,
	`[]`,
	`null`,
	``,
	`{`,
}

func TestGrepEventParserMatchesEncodingJSON(t *testing.T) {
	var parser grepEventParser
	for _, line := range grepEventLines {
		checkGrepEvent(t, &parser, line)
	}
	// The parser reuses its buffers: a parse after another one must not see the earlier event.
	checkGrepEvent(t, &parser, grepEventLines[1])
	checkGrepEvent(t, &parser, grepEventLines[0])
}

func TestGrepEventParserHandlesLongLinesAndDeepNesting(t *testing.T) {
	var parser grepEventParser
	long := fmt.Sprintf(`{"type":"match","data":{"path":{"text":"a"},"lines":{"text":%q},"line_number":1}}`, strings.Repeat("x", 1<<20))
	checkGrepEvent(t, &parser, long)
	for _, depth := range []int{200, 2000} {
		deep := `{"type":"match","data":{"path":{"text":"a"},"lines":{"text":"x"},"line_number":1,"extra":` + strings.Repeat(`{"a":[`, depth) + `1` + strings.Repeat(`]}`, depth) + `}}`
		checkGrepEvent(t, &parser, deep)
	}
	// Past encoding/json's nesting limit the parser still reads the event, in memory proportional to the text.
	deeper := `{"type":"match","data":{"path":{"text":"a"},"lines":{"text":"x"},"line_number":1,"extra":` + strings.Repeat("[", 100_000) + strings.Repeat("]", 100_000) + `}}`
	if event, ok := parser.parse([]byte(deeper)); !ok || event.lineNumber != 1 {
		t.Fatalf("deeply nested value: %+v, %v", event, ok)
	}
	unbalanced := `{"type":"match","data":{"path":{"text":"a"},"lines":{"text":"x"},"line_number":1,"extra":[[[1]]}}`
	if _, ok := parser.parse([]byte(unbalanced)); ok {
		t.Fatal("an unbalanced value parsed")
	}
}

func TestGrepEventParserAllocatesNothingPerEvent(t *testing.T) {
	var parser grepEventParser
	line := []byte(grepEventLines[0])
	escaped := []byte(grepEventLines[1])
	if allocs := testing.AllocsPerRun(100, func() {
		if _, ok := parser.parse(line); !ok {
			t.Fatal("event not parsed")
		}
		if _, ok := parser.parse(escaped); !ok {
			t.Fatal("event not parsed")
		}
	}); allocs != 0 {
		t.Fatalf("parsing events allocated %v times per run", allocs)
	}
}

func TestGrepEventParserAcceptsEscapedRGKeys(t *testing.T) {
	cases := []struct {
		name string
		line string
	}{
		{name: "type", line: `{"t\u0079pe":"match","data":{"path":{"text":"a"},"lines":{"text":"x"},"line_number":1}}`},
		{name: "data", line: `{"type":"match","d\u0061ta":{"path":{"text":"a"},"lines":{"text":"x"},"line_number":1}}`},
		{name: "path", line: `{"type":"match","data":{"p\u0061th":{"text":"a"},"lines":{"text":"x"},"line_number":1}}`},
		{name: "lines", line: `{"type":"match","data":{"path":{"text":"a"},"l\u0069nes":{"text":"x"},"line_number":1}}`},
		{name: "text", line: `{"type":"match","data":{"path":{"t\u0065xt":"a"},"lines":{"t\u0065xt":"x"},"line_number":1}}`},
		{name: "bytes", line: `{"type":"match","data":{"path":{"b\u0079tes":"YQ=="},"lines":{"b\u0079tes":"eA=="},"line_number":1}}`},
		{name: "line_number", line: `{"type":"match","data":{"path":{"text":"a"},"lines":{"text":"x"},"line_n\u0075mber":1}}`},
	}
	var parser grepEventParser
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			wantMatch, wantPath, wantNumber, wantText, wantOK := referenceGrepEvent(tc.line)
			got, gotOK := parser.parse([]byte(tc.line))
			t.Logf("%s: scanner=(ok=%v match=%v path=%q line=%d text=%q) reference=(ok=%v match=%v path=%q line=%d text=%q)",
				tc.name, gotOK, got.isMatch, got.path, got.lineNumber, got.lineText, wantOK, wantMatch, wantPath, wantNumber, wantText)
			checkGrepEvent(t, &parser, tc.line)
		})
	}
}

func FuzzGrepEventParserMatchesEncodingJSON(f *testing.F) {
	for _, line := range grepEventLines {
		f.Add(line)
	}
	f.Fuzz(func(t *testing.T, line string) {
		// rg writes valid UTF-8 in a text field; encoding/json would replace an invalid byte the parser keeps.
		if !bytes.Equal([]byte(strings.ToValidUTF8(line, "\uFFFD")), []byte(line)) {
			t.Skip()
		}
		// encoding/json matches a name without regard to case, as JSON.parse's property access never does: the scanner is exact, so a
		// name that only folds to one of rg's names has no reference.
		if grepEventLineHasFoldedKey(line) {
			t.Skip()
		}
		var parser grepEventParser
		checkGrepEvent(t, &parser, line)
	})
}

// grepEventLineHasFoldedKey walks the JSON object keys of line with encoding/json (so every key's JSON escapes are decoded before
// comparison) and reports whether any key at a path the scanner and reference inspect folds case-insensitively to one of rg's names
// without equalling it:
//   - depth 1 (root): "type", "data"
//   - depth 2 (under "data"): "path", "lines", "line_number"
//   - depth 3 (under "data"."path" or "data"."lines"): "text", "bytes"
func grepEventLineHasFoldedKey(line string) bool {
	dec := json.NewDecoder(strings.NewReader(line))
	type frame struct {
		delim  byte
		inData bool
		inText bool
		key    string
	}
	var stack []frame
	expectKey := false
	for {
		tok, err := dec.Token()
		if err != nil {
			return false
		}
		switch v := tok.(type) {
		case json.Delim:
			switch v {
			case '{':
				var f frame
				f.delim = '{'
				if len(stack) == 1 && stack[0].delim == '{' && stack[0].key == "data" {
					f.inData = true
				} else if len(stack) == 2 && stack[1].inData && (stack[1].key == "path" || stack[1].key == "lines") {
					f.inText = true
				}
				stack = append(stack, f)
				expectKey = true
			case '[':
				stack = append(stack, frame{delim: '['})
				expectKey = false
			case '}', ']':
				if len(stack) > 0 {
					stack = stack[:len(stack)-1]
				}
				expectKey = len(stack) > 0 && stack[len(stack)-1].delim == '{'
			}
		case string:
			if expectKey {
				top := &stack[len(stack)-1]
				top.key = v
				if len(stack) == 1 && grepEventKeyFolds(v, "type", "data") {
					return true
				}
				if top.inData && grepEventKeyFolds(v, "path", "lines", "line_number") {
					return true
				}
				if top.inText && grepEventKeyFolds(v, "text", "bytes") {
					return true
				}
				expectKey = false
			} else if len(stack) > 0 && stack[len(stack)-1].delim == '{' {
				expectKey = true
			}
		default:
			if len(stack) > 0 && stack[len(stack)-1].delim == '{' {
				expectKey = true
			}
		}
	}
}

func grepEventKeyFolds(key string, names ...string) bool {
	for _, name := range names {
		if key != name && strings.EqualFold(key, name) {
			return true
		}
	}
	return false
}
