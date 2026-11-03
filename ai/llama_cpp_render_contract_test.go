package ai

import (
	"fmt"
	"math"
	"strconv"
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/internal/pioracle"
)

// llamaRenderOracleBody runs Pi's renderQuestion over each doc and emits {content, labels, keys} or {error}.
const llamaRenderOracleBody = `
const mod = await load("pi-ai/api/llama-cpp-classify.js");
emit(input.map((doc) => {
  const questions = {};
  for (const q of doc.questions) {
    if (q.type === "choice") questions[q.id] = { type: "choice", instructions: q.instructions, criteria: Object.fromEntries(q.choices) };
    else if (q.type === "score") questions[q.id] = { type: "score", instructions: q.instructions, criteria: q.levels };
    else questions[q.id] = { type: "bool", instructions: q.instructions, criteria: { true: q.true, false: q.false } };
  }
  try {
    const r = mod.renderQuestion({ state: doc.state, questions }, doc.ask);
    return { content: r.content, labels: r.labels, keys: r.keys };
  } catch (error) {
    return { error: error.message };
  }
}));`

// llamaBoundaryDocs are the renderQuestion boundaries the seeded corpus of TestLlamaCppRenderQuestionMatchesPi does not reach: every option count around the
// label tables (choice labels run A-Z, a-z, 0-9; a score has ten digit labels; both throw outside 2 and the table length), array-index detection at the
// edges of the index range for state, question and option keys, requests of one to four questions asked in turn, and every Yes/No meaning shape.
func llamaBoundaryDocs() []llamaRenderDoc {
	boolQ := func(id, instructions, yes, no string) llamaRenderEntry {
		return llamaRenderEntry{ID: id, Type: "bool", Instructions: instructions, True: yes, False: no}
	}
	choiceQ := func(id string, count int, key func(int) string, description func(int) string) llamaRenderEntry {
		entry := llamaRenderEntry{ID: id, Type: "choice", Instructions: "Which?", Choices: [][]string{}}
		for i := range count {
			entry.Choices = append(entry.Choices, []string{key(i), description(i)})
		}
		return entry
	}
	scoreQ := func(id string, levels int) llamaRenderEntry {
		entry := llamaRenderEntry{ID: id, Type: "score", Instructions: "How much?", Levels: make([]string, levels)}
		for i := range entry.Levels {
			entry.Levels[i] = "level " + strconv.Itoa(i)
		}
		return entry
	}
	byIndex := func(i int) string { return "k" + strconv.Itoa(i) }
	described := func(i int) string { return "desc " + strconv.Itoa(i) }
	var docs []llamaRenderDoc
	askEach := func(state map[string]any, entries ...llamaRenderEntry) {
		for _, entry := range entries {
			docs = append(docs, llamaRenderDoc{State: state, Questions: entries, Ask: entry.ID})
		}
	}
	empty := map[string]any{}
	for _, count := range []int{0, 1, 2, 3, 8, 9, 10, 25, 26, 27, 51, 52, 53, 60, 61, 62, 63, 64, 100} {
		askEach(empty, choiceQ("c", count, byIndex, described))
	}
	for _, levels := range []int{0, 1, 2, 3, 9, 10, 11, 12} {
		askEach(empty, scoreQ("s", levels))
	}
	// Options of a choice keep their descriptions only when non-empty, and an option key that is an array index sorts ahead of the others in the overview and
	// the labeled question alike.
	for _, key := range []func(int) string{
		func(i int) string { return []string{"10", "9", "b", "0", "a"}[i] },
		func(i int) string { return []string{"4294967296", "4294967295", "01", "4294967294", "-1"}[i] },
		func(i int) string { return []string{"1.5", "1e3", "", " ", "x"}[i] },
	} {
		for _, description := range []func(int) string{
			described,
			func(int) string { return "" },
			func(i int) string { return []string{"", "0", " ", "false", "multi\nline"}[i] },
		} {
			askEach(empty, choiceQ("c", 5, key, description))
		}
	}
	// Question ids and their order: array-index ids ahead of the others, each group in insertion order.
	askEach(map[string]any{"title": "t"},
		boolQ("b", "is b?", "yes b", "no b"), scoreQ("a", 3), choiceQ("2", 2, byIndex, described), boolQ("1", "is 1?", "", ""))
	askEach(empty,
		boolQ("01", "padded", "", ""), boolQ("4294967295", "first non-index", "", ""), boolQ("-1", "negative", "", ""), boolQ("4294967294", "last index", "", ""), boolQ("0", "zero", "", ""))
	askEach(empty, boolQ("only", "alone", "y", "n"))
	askEach(empty, boolQ("x", "one", "", ""), boolQ("y", "two", "", ""))
	// Yes/No meanings: neither, either, both, and values JavaScript treats as truthy strings.
	for _, meaning := range [][2]string{{"", ""}, {"yes", ""}, {"", "no"}, {"yes", "no"}, {"0", "0"}, {" ", " "}, {"false", "null"}, {"a\nb", "c\nd"}} {
		askEach(empty, boolQ("q", "Is it so?", meaning[0], meaning[1]))
	}
	// An unknown id throws, also in a request that holds other questions and one whose ids are all array indexes.
	docs = append(docs,
		llamaRenderDoc{State: empty, Questions: []llamaRenderEntry{boolQ("a", "", "", "")}, Ask: "b"},
		llamaRenderDoc{State: empty, Questions: []llamaRenderEntry{boolQ("1", "", "", "")}, Ask: "2"},
		llamaRenderDoc{State: empty, Questions: []llamaRenderEntry{boolQ("a", "", "", "")}, Ask: ""})
	// States: JSON.stringify(state, null, 1) over empty containers, nesting, number spellings, control characters and key order.
	states := []map[string]any{
		{},
		{"a": map[string]any{}, "b": []any{}, "c": []any{[]any{}, map[string]any{}}},
		{"b": 1, "a": 2, "10": 3, "2": 4, "01": 5, "4294967294": 6, "4294967295": 7, "-1": 8, "": 9},
		{"n": []any{0.1 + 0.2, 1e21, 1e-7, 123456789012345680000.0, 5e-324, math.MaxFloat64, math.Copysign(0, -1), 0.000001, 1e20, -1e-7, 100, 1.5}},
		{"s": []any{"tab\t", "bell\u0007", "del\u007f", "nul\u0000", "quote\"", "back\\", "sep\u2028\u2029", "😀", "é", "\u001f", "</state>"}},
		{"deep": map[string]any{"a": map[string]any{"b": map[string]any{"c": []any{1, []any{2, []any{3, nil, true, false}}}}}}},
		{"null": nil, "t": true, "f": false, "list": []any{nil, nil}},
	}
	for _, state := range states {
		askEach(state, boolQ("q", "ok?", "", ""))
	}
	return docs
}

// llama-cpp-classify.ts renderQuestion (renderState, renderTask, renderOverview, the label tables and the thrown option-count messages) against pinned Pi
// over llamaBoundaryDocs: the option counts at the edges of the label tables, array-index key ordering at the edges of the index range, one-to-four-question
// requests asked in turn, every Yes/No meaning shape, unknown ids and state spellings.
func TestLlamaCppRenderBoundariesMatchPi(t *testing.T) {
	docs := llamaBoundaryDocs()
	var want []llamaRenderResult
	pioracle.Run(t, llamaRenderOracleBody, docs, &want)
	failures, thrown, labeled, longest := 0, 0, 0, 0
	for i, doc := range docs {
		got := goLlamaRender(doc)
		if fmt.Sprint(got) != fmt.Sprint(want[i]) {
			if failures++; failures <= 3 {
				t.Errorf("doc %d (ask %q, %d questions) differs from Pi\n Pig %q %v %v %q\n Pi  %q %v %v %q", i, doc.Ask, len(doc.Questions), got.Content, got.Labels, got.Keys, got.Error, want[i].Content, want[i].Labels, want[i].Keys, want[i].Error)
			}
		}
		// The denominators come from Pi's results: a corpus that never reached a branch would pass it vacuously.
		if want[i].Error != "" {
			thrown++
		}
		if len(want[i].Labels) > 0 {
			labeled++
		}
		longest = max(longest, len(want[i].Labels))
	}
	if failures > 3 {
		t.Errorf("%d of %d docs differ from Pi", failures, len(docs))
	}
	if thrown < 12 || labeled < 50 || longest != 62 {
		t.Errorf("Pi threw for %d and labeled %d of %d docs, longest label table %d; want at least 12, 50 and 62", thrown, labeled, len(docs), longest)
	}
	var messages []string
	for i := range docs {
		if want[i].Error != "" {
			messages = append(messages, want[i].Error)
		}
	}
	for _, fragment := range []string{"A choice question needs 2 to 62 options, got 63", "A score question needs 2 to 10 levels, got 11", "Unknown question: "} {
		found := false
		for _, message := range messages {
			found = found || strings.HasPrefix(message, fragment)
		}
		if !found {
			t.Errorf("Pi threw no %q among %d errors", fragment, len(messages))
		}
	}
}

// TestLlamaCppRenderBoundaryDocsDump prints the boundary docs for the Pi side of the providers-registry/09 parity scenario.
func TestLlamaCppRenderBoundaryDocsDump(t *testing.T) {
	line, err := marshalJSONValue(llamaBoundaryDocs())
	if err != nil {
		t.Fatal(err)
	}
	fmt.Printf("llama-boundary-probes:%s\n", line)
}

// TestLlamaCppRenderBoundaryDocsParity prints PiG's result of each boundary doc, one JSON line [content, labels, keys, error] per doc, for the providers-registry/09 parity scenario.
func TestLlamaCppRenderBoundaryDocsParity(t *testing.T) {
	for _, doc := range llamaBoundaryDocs() {
		got := goLlamaRender(doc)
		line, err := marshalJSONValue([]any{got.Content, got.Labels, got.Keys, got.Error})
		if err != nil {
			t.Fatal(err)
		}
		// JSON.stringify writes U+2028 and U+2029 literally; the scenario compares lines, so both sides escape them.
		fmt.Printf("llama-boundary-observation:%s\n", strings.NewReplacer("\u2028", `\u2028`, "\u2029", `\u2029`).Replace(string(line)))
	}
}
