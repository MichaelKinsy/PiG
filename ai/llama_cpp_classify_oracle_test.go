package ai

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"math/rand"
	"net/http"
	"net/http/httptest"
	"os/exec"
	"regexp"
	"slices"
	"strings"
	"sync"
	"testing"

	sdkjson "github.com/MichaelKinsy/PiG/extensions/sdk/json"
	"github.com/MichaelKinsy/PiG/internal/coding/pigversion"
	"github.com/MichaelKinsy/PiG/internal/jsstring"
)

// pi: packages/ai/src/api/llama-cpp-classify.ts

type llamaOracleQuestion struct {
	ID           string              `json:"id"`
	Type         string              `json:"type"`
	Instructions string              `json:"instructions"`
	Criteria     []llamaOracleChoice `json:"criteria"`
	Levels       []string            `json:"levels"`
	Yes          string              `json:"yes,omitempty"`
	No           string              `json:"no,omitempty"`
}

type llamaOracleChoice struct {
	Key         string `json:"key"`
	Description string `json:"description"`
}

type llamaOracleRank struct {
	Depth   int    `json:"depth"`
	Logprob string `json:"logprob"`
}

type llamaOracleTemplate struct {
	Status int    `json:"status"`
	Body   string `json:"body"`
}

type llamaOracleProbe struct {
	BaseSuffix  string                     `json:"baseSuffix"`
	APIKey      string                     `json:"apiKey"`
	Temperature *float64                   `json:"temperature"`
	Images      bool                       `json:"images"`
	WithImages  bool                       `json:"withImages"`
	State       string                     `json:"state"`
	Questions   []llamaOracleQuestion      `json:"questions"`
	Tokenizer   string                     `json:"tokenizer"`
	Template    llamaOracleTemplate        `json:"template"`
	Ranks       map[string]llamaOracleRank `json:"ranks"`
	Noise       []string                   `json:"noise"`
	Shape       string                     `json:"shape"`
}

type llamaOracleExchange struct {
	Path          string  `json:"path"`
	Authorization *string `json:"authorization"`
	ContentType   *string `json:"contentType"`
	Body          string  `json:"body"`
}

type llamaOracleResult struct {
	Requests     []llamaOracleExchange `json:"requests"`
	StopReason   string                `json:"stopReason"`
	ErrorMessage *string               `json:"errorMessage"`
	AnswersJSON  string                `json:"answersJson"`
}

var llamaOracleLabels = func() []string {
	labels := strings.Split("ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz0123456789", "")
	return append(labels, "Yes", "No")
}()

func llamaOracleWordID(word string) int {
	sum := 0
	for i, r := range []rune(word) {
		sum += int(r) * (i + 1)
	}
	return 1000 + sum%50000
}

var llamaOracleTokenPattern = regexp.MustCompile(`\n|[A-Za-z0-9]+|[^A-Za-z0-9\n]`)
var llamaOracleAlnum = regexp.MustCompile(`^[A-Za-z0-9]+$`)

// llamaOracleTokenize is the scripted tokenizer llama_cpp_classify.mjs also runs.
func llamaOracleTokenize(mode, content string) any {
	switch mode {
	case "bad":
		return "not a list"
	case "string":
		return []any{"7"}
	case "shared":
		if content == "\n" {
			return []int{10}
		}
		return []int{10, 55}
	case "chars":
		ids := []int{}
		for _, r := range content {
			ids = append(ids, int(r))
		}
		return ids
	}
	words := llamaOracleTokenPattern.FindAllString(content, -1)
	if mode == "merged" && len(words) > 1 && words[0] == "\n" {
		return []int{2000 + llamaOracleWordID(strings.Join(words[1:], ""))}
	}
	ids := []int{}
	for _, word := range words {
		switch {
		case word == "\n":
			ids = append(ids, 10)
		case llamaOracleAlnum.MatchString(word):
			ids = append(ids, llamaOracleWordID(word))
		default:
			ids = append(ids, int([]rune(word)[0]))
		}
	}
	if mode == "objects" {
		objects := make([]map[string]any, len(ids))
		for i, id := range ids {
			objects[i] = map[string]any{"id": id, "text": "t"}
		}
		return objects
	}
	return ids
}

func llamaOracleLabelIDs(mode string) map[string]int {
	ids := map[string]int{}
	for _, label := range llamaOracleLabels {
		var last any
		switch tokens := llamaOracleTokenize(mode, "\n"+label).(type) {
		case []int:
			if len(tokens) > 0 {
				last = tokens[len(tokens)-1]
			}
		case []map[string]any:
			if len(tokens) > 0 {
				last = tokens[len(tokens)-1]["id"]
			}
		}
		if id, ok := last.(int); ok {
			ids[label] = id
		}
	}
	return ids
}

// serveLlamaOracle is the scripted llama-server llama_cpp_classify.mjs also is; it records every request it gets.
func serveLlamaOracle(t *testing.T, probe llamaOracleProbe, record func(llamaOracleExchange)) *httptest.Server {
	t.Helper()
	ids := llamaOracleLabelIDs(probe.Tokenizer)
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		raw, _ := io.ReadAll(r.Body)
		exchange := llamaOracleExchange{Path: r.URL.Path, Body: string(raw)}
		if v := r.Header.Get("Authorization"); v != "" {
			exchange.Authorization = &v
		}
		if v := r.Header.Get("Content-Type"); v != "" {
			exchange.ContentType = &v
		}
		record(exchange)
		var body struct {
			Content string `json:"content"`
			NProbs  int    `json:"n_probs"`
		}
		_ = json.Unmarshal(raw, &body)
		w.Header().Set("Content-Type", "application/json")
		reply := func(status int, text string) {
			w.WriteHeader(status)
			_, _ = io.WriteString(w, text)
		}
		switch r.URL.Path {
		case "/tokenize":
			encoded, _ := json.Marshal(map[string]any{"tokens": llamaOracleTokenize(probe.Tokenizer, body.Content)})
			reply(200, string(encoded))
			return
		case "/apply-template":
			reply(probe.Template.Status, probe.Template.Body)
			return
		}
		switch probe.Shape {
		case "array":
			reply(200, "[]")
			return
		case "noProbs":
			reply(200, "{}")
			return
		case "emptyList":
			reply(200, `{"completion_probabilities":[]}`)
			return
		case "noTop":
			reply(200, `{"completion_probabilities":[{"id":1}]}`)
			return
		}
		var top []string
		for _, label := range llamaOracleLabels {
			if rank, ok := probe.Ranks[label]; ok && body.NProbs >= rank.Depth {
				id, has := ids[label]
				idText := "null"
				if has {
					idText = fmt.Sprint(id)
				}
				top = append(top, fmt.Sprintf(`{"id":%s,"token":%q,"logprob":%s}`, idText, label, rank.Logprob))
			}
		}
		top = append(top, probe.Noise...)
		reply(200, `{"completion_probabilities":[{"top_logprobs":[`+strings.Join(top, ",")+`]}]}`)
	}))
}

// TestLlamaCppClassifyMatchesPi runs the pinned pi-ai llama-cpp-classify and PiG's ClassifyLlamaCpp against the same scripted llama-server for random
// states, questions, tokenizers, templates and log-probabilities (including labels missing from the first readouts, underflow, and malformed answers).
// The stop reason, the error message, the answers and the requests the server saw must agree.
func TestLlamaCppClassifyMatchesPi(t *testing.T) {
	rng := rand.New(rand.NewSource(7))
	pick := func(items ...string) string { return items[rng.Intn(len(items))] }
	logprobs := []string{"-0.3", "-1.5", "-0.05", "-3", "-12.25", "0", "-0", "-1e30", "-745", "-1e-300", "-100", "-7.5"}
	var probes []llamaOracleProbe
	for range 600 {
		p := llamaOracleProbe{BaseSuffix: pick("", "/", "/v1", "/v1/", "/v1//"), APIKey: pick("", "k", "local"), Tokenizer: pick("words", "words", "words", "words", "merged", "chars", "objects", "bad", "string", "shared"),
			State: drawClassifierState(rng), Ranks: map[string]llamaOracleRank{}, Shape: pick("ok", "ok", "ok", "ok", "ok", "ok", "ok", "ok", "array", "noProbs", "emptyList", "noTop")}
		if rng.Intn(3) != 0 {
			temperature := []float64{0.5, 1, 2, 0.01, 100, 0, -1, 0.7}[rng.Intn(8)]
			p.Temperature = &temperature
		}
		p.Images = rng.Intn(2) == 0
		p.WithImages = rng.Intn(25) == 0
		p.Questions = drawClassifierQuestions(rng)
		for _, label := range llamaOracleLabels {
			depth := []int{0, 0, 0, 0, 256, 4096, 32768, 1 << 30}[rng.Intn(8)]
			p.Ranks[label] = llamaOracleRank{Depth: depth, Logprob: logprobs[rng.Intn(len(logprobs))]}
		}
		for range rng.Intn(3) {
			p.Noise = append(p.Noise, pick(`{"id":"x","logprob":-1}`, `5`, `null`, `{"id":1,"logprob":"a"}`, `{"id":7}`, `{"id":1065,"logprob":-9}`, `[]`, `{"id":1066,"logprob":0}`))
		}
		// When the tokenizer fails too, which of the two concurrent failures Promise.all reports is a matter of timing (Pi: the template's single round trip beats the label lookups' several), so the template is sound then.
		failingTokenizer := slices.Contains([]string{"bad", "string", "shared", "chars"}, p.Tokenizer)
		kind := rng.Intn(12)
		if failingTokenizer && kind < 3 {
			kind = 3
		}
		switch kind {
		case 0:
			p.Template = llamaOracleTemplate{Status: 200, Body: `{}`}
		case 1:
			p.Template = llamaOracleTemplate{Status: 200, Body: `{"prompt":5}`}
		case 2:
			p.Template = llamaOracleTemplate{Status: 400, Body: pick(`{"error":{"message":"bad template"}}`, `plain text`, ``)}
		default:
			prompt, _ := json.Marshal(pick("PROMPT", "PROMPT<think>", "<|im_start|>assistant\n", "é😀", "a<think>b", ""))
			p.Template = llamaOracleTemplate{Status: 200, Body: `{"prompt":` + string(prompt) + `}`}
		}
		probes = append(probes, p)
	}
	input, err := json.Marshal(probes)
	if err != nil {
		t.Fatal(err)
	}
	cmd := exec.CommandContext(t.Context(), "node", "testdata/llama_cpp_classify.mjs", pigversion.UpstreamPackageVersion)
	cmd.Stdin = bytes.NewReader(input)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	output, err := cmd.Output()
	if err != nil {
		t.Fatalf("Pi oracle: %v\n%s", err, &stderr)
	}
	var want []llamaOracleResult
	if err := json.Unmarshal(output, &want); err != nil {
		t.Fatal(err)
	}
	canonical := func(e llamaOracleExchange) string {
		var body any
		_ = json.Unmarshal([]byte(e.Body), &body)
		encoded, _ := json.Marshal(body)
		auth, ctype := "", ""
		if e.Authorization != nil {
			auth = *e.Authorization
		}
		if e.ContentType != nil {
			ctype = *e.ContentType
		}
		return e.Path + "|" + auth + "|" + ctype + "|" + string(encoded)
	}
	canonicalAll := func(exchanges []llamaOracleExchange) (sorted, completions []string) {
		for _, e := range exchanges {
			c := canonical(e)
			sorted = append(sorted, c)
			if e.Path == "/completion" {
				completions = append(completions, c)
			}
		}
		slices.Sort(sorted)
		return sorted, completions
	}
	failures, keyOrder := 0, 0
	for i, probe := range probes {
		var mu sync.Mutex
		var seen []llamaOracleExchange
		server := serveLlamaOracle(t, probe, func(e llamaOracleExchange) {
			mu.Lock()
			seen = append(seen, e)
			mu.Unlock()
		})
		model := ClassifierModel{ID: fmt.Sprintf("m%d", i), Name: "M", API: ClassifierAPILlamaCppClassify, Provider: "p", BaseURL: server.URL + probe.BaseSuffix, Input: []string{"text"}, ContextWindow: 1000}
		if probe.Images {
			model.Input = []string{"text", "image"}
		}
		var state JsonObject
		if err := sdkjson.Unmarshal([]byte(probe.State), &state); err != nil {
			t.Fatal(err)
		}
		request := ClassifierContext{State: state}
		if probe.WithImages {
			request.Images = []ImageContent{{Data: "AAAA", MimeType: "image/png"}}
		}
		request.Questions = classifierQuestionsOf(probe.Questions)
		options := ClassifierOptions{APIKey: probe.APIKey, Temperature: probe.Temperature}
		result := ClassifyLlamaCpp(t.Context(), model, request, options)
		server.Close()
		answers, err := jsstring.MarshalJSON(result.Answers)
		if err != nil {
			t.Fatal(err)
		}
		gotMessage := ""
		if result.ErrorMessage != "" {
			gotMessage = result.ErrorMessage
		}
		wantMessage := ""
		if want[i].ErrorMessage != nil {
			wantMessage = *want[i].ErrorMessage
		}
		gotSorted, gotCompletions := canonicalAll(seen)
		wantSorted, wantCompletions := canonicalAll(want[i].Requests)
		var problems []string
		if string(result.StopReason) != want[i].StopReason {
			problems = append(problems, fmt.Sprintf("stopReason PiG %q Pi %q", result.StopReason, want[i].StopReason))
		}
		if gotMessage != wantMessage {
			problems = append(problems, fmt.Sprintf("errorMessage\n   PiG %q\n   Pi  %q", gotMessage, wantMessage))
		}
		if string(answers) != want[i].AnswersJSON {
			problems = append(problems, fmt.Sprintf("answers\n   PiG %s\n   Pi  %s", answers, want[i].AnswersJSON))
		}
		if !slices.Equal(gotSorted, wantSorted) {
			onlyPiG, onlyPi := slices.Clone(gotSorted), slices.Clone(wantSorted)
			for _, item := range gotSorted {
				if at := slices.Index(onlyPi, item); at >= 0 {
					onlyPi = slices.Delete(onlyPi, at, at+1)
				}
			}
			for _, item := range wantSorted {
				if at := slices.Index(onlyPiG, item); at >= 0 {
					onlyPiG = slices.Delete(onlyPiG, at, at+1)
				}
			}
			problems = append(problems, fmt.Sprintf("requests (%d PiG, %d Pi)\n   only PiG %.600q\n   only Pi  %.600q", len(gotSorted), len(wantSorted), onlyPiG, onlyPi))
		}
		if !slices.Equal(gotCompletions, wantCompletions) {
			problems = append(problems, fmt.Sprintf("completion order\n   PiG %q\n   Pi  %q", gotCompletions, wantCompletions))
		}
		if len(problems) == 0 {
			sentBodies := func(exchanges []llamaOracleExchange) []string {
				var bodies []string
				for _, e := range exchanges {
					bodies = append(bodies, e.Path+e.Body)
				}
				slices.Sort(bodies)
				return bodies
			}
			if !slices.Equal(sentBodies(seen), sentBodies(want[i].Requests)) {
				keyOrder++
			}
		}
		if len(problems) > 0 {
			if failures++; failures <= 8 {
				label, _ := json.Marshal(probe)
				t.Errorf("probe %d %.700s:\n  %s", i, label, strings.Join(problems, "\n  "))
			}
		}
	}
	if failures > 8 {
		t.Errorf("%d of %d probes differ from Pi", failures, len(probes))
	}
	if keyOrder > 0 {
		t.Errorf("%d of %d probes sent bodies whose bytes differ from Pi's (member order) though their parsed values agree", keyOrder, len(probes))
	}
}
