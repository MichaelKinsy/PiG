package ai

import (
	"encoding/json"
	"fmt"
	"maps"
	"math/rand"
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/internal/pioracle"
)

type llamaRenderDoc struct {
	State     map[string]any      `json:"state"`
	Questions []llamaRenderEntry  `json:"questions"`
	Ask       string              `json:"ask"`
	Unused    map[string]struct{} `json:"-"`
}

type llamaRenderEntry struct {
	ID           string     `json:"id"`
	Type         string     `json:"type"`
	Instructions string     `json:"instructions"`
	Choices      [][]string `json:"choices"`
	Levels       []string   `json:"levels"`
	True         string     `json:"true"`
	False        string     `json:"false"`
}

type llamaRenderResult struct {
	Content string   `json:"content"`
	Labels  []string `json:"labels"`
	Keys    []string `json:"keys"`
	Error   string   `json:"error"`
}

// llama-cpp-classify.ts renderQuestion (renderState, renderTask, renderOverview, labels and keys) against pinned Pi over 1500 seeded requests: states of nested objects, arrays, numbers, escapes and
// integer-like keys; one to five questions of every type, named with integer-like keys; choice options of every count (two to eight are labeled, others throw), empty and non-empty descriptions,
// score levels of every count, bool criteria with either, both or neither meaning; unknown ids throw.
func TestLlamaCppRenderQuestionMatchesPi(t *testing.T) {
	docs := llamaRenderDocs()
	var want []llamaRenderResult
	pioracle.Run(t, `
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
}));`, docs, &want)
	failures := 0
	for i, doc := range docs {
		got := goLlamaRender(doc)
		if fmt.Sprint(got) != fmt.Sprint(want[i]) {
			if failures++; failures <= 3 {
				input, _ := json.Marshal(doc)
				at := 0
				for at < min(len(got.Content), len(want[i].Content)) && got.Content[at] == want[i].Content[at] {
					at++
				}
				t.Errorf("request %d differs from Pi\n doc %s\n Pig %q %v %v %q\n Pi  %q %v %v %q\n first content difference at byte %d", i, input, got.Content[at:min(len(got.Content), at+80)], got.Labels, got.Keys, got.Error, want[i].Content[at:min(len(want[i].Content), at+80)], want[i].Labels, want[i].Keys, want[i].Error, at)
			}
		}
	}
	if failures > 3 {
		t.Errorf("%d of %d requests differ from Pi", failures, len(docs))
	}
}

// TestLlamaCppRenderDocsDump prints the corpus for the Pi side of the providers-registry/07 parity scenario.
func TestLlamaCppRenderDocsDump(t *testing.T) {
	line, err := json.Marshal(llamaRenderDocs())
	if err != nil {
		t.Fatal(err)
	}
	fmt.Printf("llama-render-probes:%s\n", line)
}

// TestLlamaCppRenderDocsParity prints PiG's result of each request, one JSON line [content, labels, keys, error] per request, for the providers-registry/07 parity scenario.
func TestLlamaCppRenderDocsParity(t *testing.T) {
	for _, doc := range llamaRenderDocs() {
		got := goLlamaRender(doc)
		line, err := marshalJSONValue([]any{got.Content, got.Labels, got.Keys, got.Error})
		if err != nil {
			t.Fatal(err)
		}
		// JSON.stringify writes U+2028 and U+2029 literally; the scenario compares lines, so both sides escape them.
		fmt.Printf("llama-render-observation:%s\n", strings.NewReplacer("\u2028", `\u2028`, "\u2029", `\u2029`).Replace(string(line)))
	}
}

func goLlamaRender(doc llamaRenderDoc) llamaRenderResult {
	var questions ClassifierQuestions
	for _, q := range doc.Questions {
		var question ClassifierQuestion
		switch q.Type {
		case "choice":
			choice := ClassifierChoiceQuestion{Instructions: q.Instructions}
			for _, pair := range q.Choices {
				choice.Criteria = append(choice.Criteria, ClassifierChoiceCriterion{Key: pair[0], Description: pair[1]})
			}
			question = choice
		case "score":
			question = ClassifierScoreQuestion{Instructions: q.Instructions, Criteria: q.Levels}
		default:
			question = ClassifierBoolQuestion{Instructions: q.Instructions, Criteria: ClassifierBoolCriteria{True: q.True, False: q.False}}
		}
		questions = append(questions, ClassifierQuestionEntry{ID: q.ID, Question: question})
	}
	state := JsonObject{}
	maps.Copy(state, doc.State)
	rendered, err := LlamaCppRenderQuestion(ClassifierContext{State: state, Questions: questions}, doc.Ask)
	if err != nil {
		return llamaRenderResult{Error: err.Error()}
	}
	return llamaRenderResult{Content: rendered.Content, Labels: rendered.Labels, Keys: rendered.Keys}
}

func llamaRenderDocs() []llamaRenderDoc {
	rng := rand.New(rand.NewSource(20261013))
	pick := func(items ...string) string { return items[rng.Intn(len(items))] }
	text := func() string {
		return pick("", "Is it urgent?", "multi\nline", "quote \" and \\ slash", "日本語 😀", "tab\t", "</state>", "State:", "é\u2028x", "{\"a\":1}", "  spaced  ", strings.Repeat("long ", 20))
	}
	var value func(depth int) any
	value = func(depth int) any {
		switch n := rng.Intn(9); {
		case n == 0:
			return float64(rng.Intn(2000) - 1000)
		case n == 1:
			return []float64{0.1, 1.5, 1e21, 1e-7, 123456789.125, 100, -0.5, 2e308 / 1e300}[rng.Intn(8)]
		case n == 2:
			return rng.Intn(2) == 0
		case n == 3:
			return nil
		case n <= 5 || depth >= 3:
			return text()
		case n == 6:
			items := make([]any, rng.Intn(4))
			for i := range items {
				items[i] = value(depth + 1)
			}
			return items
		default:
			object := map[string]any{}
			for range rng.Intn(4) {
				object[pick("a", "b", "name", "10", "2", "1", "é", "", "z z", "k\"q")] = value(depth + 1)
			}
			return object
		}
	}
	var docs []llamaRenderDoc
	for range 1500 {
		state := map[string]any{}
		for range rng.Intn(5) {
			state[pick("title", "body", "n", "items", "7", "3", "ünï", "")] = value(0)
		}
		var entries []llamaRenderEntry
		used := map[string]bool{}
		for range 1 + rng.Intn(5) {
			id := pick("team", "urgent", "severity", "5", "12", "a", "b", "é", "z")
			if used[id] {
				continue
			}
			used[id] = true
			entry := llamaRenderEntry{ID: id, Instructions: text()}
			switch rng.Intn(3) {
			case 0:
				entry.Type = "choice"
				count := rng.Intn(11)
				seen := map[string]bool{}
				for range count {
					key := pick("billing", "technical", "sales", "3", "10", "x", "é", "", "k:v")
					if !seen[key] {
						seen[key] = true
						entry.Choices = append(entry.Choices, []string{key, pick("", "", "desc", "multi\nline", "日本")})
					}
				}
				if entry.Choices == nil {
					entry.Choices = [][]string{}
				}
			case 1:
				entry.Type = "score"
				entry.Levels = make([]string, rng.Intn(13))
				for i := range entry.Levels {
					entry.Levels[i] = pick("low", "", "high", "mid level", "日本")
				}
			default:
				entry.Type = "bool"
				entry.True, entry.False = pick("", "yes!", "ok"), pick("", "no!", "nope")
			}
			entries = append(entries, entry)
		}
		ask := entries[rng.Intn(len(entries))].ID
		if rng.Intn(25) == 0 {
			ask = "missing"
		}
		docs = append(docs, llamaRenderDoc{State: state, Questions: entries, Ask: ask})
	}
	return docs
}
