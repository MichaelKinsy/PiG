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
	"reflect"
	"strings"
	"testing"

	sdkjson "github.com/MichaelKinsy/PiG/extensions/sdk/json"
	"github.com/MichaelKinsy/PiG/internal/coding/pigversion"
	"github.com/MichaelKinsy/PiG/internal/jsstring"
)

// pi: packages/ai/src/api/openai-decisions.ts

type decisionsOracleProbe struct {
	BaseSuffix  string                `json:"baseSuffix"`
	APIKey      string                `json:"apiKey"`
	State       string                `json:"state"`
	Questions   []llamaOracleQuestion `json:"questions"`
	Images      int                   `json:"images"`
	Cost        json.RawMessage       `json:"cost"`
	Status      int                   `json:"status"`
	ContentType string                `json:"contentType"`
	Body        string                `json:"body"`
}

type decisionsOracleResult struct {
	Requests     []llamaOracleExchange `json:"requests"`
	StopReason   string                `json:"stopReason"`
	ErrorMessage *string               `json:"errorMessage"`
	AnswersJSON  string                `json:"answersJson"`
	Usage        map[string]any        `json:"usage"`
}

// TestOpenAIDecisionsMatchesPi runs the pinned pi-ai openai-decisions classifier and PiG's ClassifyOpenAIDecisions against the same scripted server for random
// states, questions, image counts, model prices (with prompt-length tiers), base URLs and responses (well-formed, malformed, refused, missing and extra
// answers, odd usage objects, HTTP errors). Fractional and over-int32 token counts are left out: Usage holds integers, where Pi keeps the JavaScript number (the
// same representation gap as the other providers' token counts). The request the server saw, the stop reason, the error message, the answers and the priced usage must agree.
func TestOpenAIDecisionsMatchesPi(t *testing.T) {
	rng := rand.New(rand.NewSource(11))
	pick := func(items ...string) string { return items[rng.Intn(len(items))] }
	number := func() string {
		return pick("0", "1", "0.5", "0.25", "-1", "7", "1e-7", "0.1", "1e21", `"0.5"`, "null", "true", "-0", "1e999", "[]")
	}
	answerFor := func(q llamaOracleQuestion) string {
		name, _ := json.Marshal(q.ID)
		switch q.Type {
		case "choice":
			var entries []string
			for _, c := range q.Criteria {
				value, _ := json.Marshal(c.Key)
				entries = append(entries, fmt.Sprintf(`{"value":%s,"probability":%s}`, value, number()))
			}
			choice := `""`
			if len(q.Criteria) > 0 {
				choice, _ = func() (string, error) { b, err := json.Marshal(q.Criteria[0].Key); return string(b), err }()
			}
			return fmt.Sprintf(`{"name":%s,"type":%s,"choice":%s,"probabilities":[%s],"confidence":%s}`, name, pick(`"choice"`, `"choice"`, `"choice"`, `"score"`), pick(choice, choice, `5`, `null`), strings.Join(entries, ","), number())
		case "score":
			return fmt.Sprintf(`{"name":%s,"type":%s,"score":%s,"confidence":%s}`, name, pick(`"score"`, `"score"`, `"score"`, `"choice"`), number(), number())
		default:
			return fmt.Sprintf(`{"name":%s,"type":%s,"probability":%s}`, name, pick(`"predicate"`, `"predicate"`, `"predicate"`, `"bool"`), number())
		}
	}
	var probes []decisionsOracleProbe
	for range 700 {
		p := decisionsOracleProbe{
			BaseSuffix: pick("/v1", "/v1", "/v1/", "", "//", "/a/b", "/v1?x=1", "/v1#frag", "/ä/ö", "/a b"),
			APIKey:     pick("k", "k", "sk-test", ""),
			State:      drawClassifierState(rng), Questions: drawClassifierQuestions(rng), Status: 200, ContentType: "application/json",
			Cost: json.RawMessage(pick(`{"input":0,"output":0,"cacheRead":0,"cacheWrite":0}`, `{"input":2.5,"output":10,"cacheRead":0.25,"cacheWrite":3}`, `{"input":0.15,"output":0.6,"cacheRead":0,"cacheWrite":0,"tiers":[{"inputTokensAbove":100,"input":1,"output":2,"cacheRead":0.5,"cacheWrite":1.5}]}`, `{"input":1e-7,"output":3.3,"cacheRead":0,"cacheWrite":0}`)),
		}
		p.Images = []int{0, 0, 0, 0, 1, 2, 3, 128, 129}[rng.Intn(9)]
		var answers []string
		for _, q := range p.Questions {
			answers = append(answers, answerFor(q))
		}
		switch rng.Intn(14) {
		case 0:
			if len(answers) > 0 {
				answers = answers[1:]
			}
		case 1:
			answers = append(answers, pick(`5`, `null`, `[]`, `{"name":5}`, `{"name":"extra","type":"choice"}`, `"text"`))
		case 2:
			if len(answers) > 0 {
				name, _ := json.Marshal(p.Questions[rng.Intn(len(p.Questions))].ID)
				answers = append(answers, fmt.Sprintf(`{"name":%s,"type":"refusal"}`, name))
			}
		case 3:
			answers = append([]string{pick(`5`, `null`, `{"name":null}`)}, answers...)
		}
		body := `{"answers":[` + strings.Join(answers, ",") + `]`
		switch rng.Intn(10) {
		case 0, 1, 2:
			body += `,"usage":` + pick(`{"input_tokens":120,"output_tokens":40}`, `{"input_tokens":50}`, `{"output_tokens":7}`, `{"input_tokens":-4,"output_tokens":2}`, `{"input_tokens":"9","output_tokens":null}`, `{}`, `[]`, `5`, `null`, `{"input_tokens":100000,"output_tokens":100000}`)
		}
		body += "}"
		switch rng.Intn(14) {
		case 0:
			p.Body = pick(`{"answers":5}`, `{"answers":null}`, `{}`, `[]`, `null`, `"text"`, `5`, `{"answers":{}}`)
		case 1:
			p.Status, p.Body = []int{400, 401, 404, 504}[rng.Intn(4)], pick(`{"error":{"message":"bad request","type":"invalid"}}`, `plain text`, ``, `<html>gateway</html>`, `{"message":"nope"}`, `{"error":"x"}`)
		case 2:
			p.Body = pick(`{`, ``, "\ufeff{}", `{"answers":[`)
		default:
			p.Body = body
		}
		probes = append(probes, p)
	}
	input, err := json.Marshal(probes)
	if err != nil {
		t.Fatal(err)
	}
	cmd := exec.CommandContext(t.Context(), "node", "testdata/openai_decisions.mjs", pigversion.UpstreamPackageVersion)
	cmd.Stdin = bytes.NewReader(input)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	output, err := cmd.Output()
	if err != nil {
		t.Fatalf("Pi oracle: %v\n%s", err, &stderr)
	}
	var want []decisionsOracleResult
	if err := json.Unmarshal(output, &want); err != nil {
		t.Fatal(err)
	}
	failures := 0
	for i, probe := range probes {
		var seen []llamaOracleExchange
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			raw, _ := io.ReadAll(r.Body)
			exchange := llamaOracleExchange{Path: r.URL.RequestURI(), Body: string(raw)}
			if v := r.Header.Get("Authorization"); v != "" {
				exchange.Authorization = &v
			}
			if v := r.Header.Get("Content-Type"); v != "" {
				exchange.ContentType = &v
			}
			seen = append(seen, exchange)
			w.Header().Set("Content-Type", probe.ContentType)
			w.WriteHeader(probe.Status)
			_, _ = io.WriteString(w, probe.Body)
		}))
		var cost ModelCost
		if err := json.Unmarshal(probe.Cost, &cost); err != nil {
			t.Fatal(err)
		}
		model := ClassifierModel{ID: fmt.Sprintf("m%d", i), Name: "M", API: ClassifierAPIOpenAIDecisions, Provider: "p", BaseURL: server.URL + probe.BaseSuffix, Input: []string{"text", "image"}, Cost: cost, ContextWindow: 1000}
		var state JsonObject
		if err := sdkjson.Unmarshal([]byte(probe.State), &state); err != nil {
			t.Fatal(err)
		}
		request := ClassifierContext{State: state, Questions: classifierQuestionsOf(probe.Questions)}
		for n := range probe.Images {
			mime := "image/jpeg"
			if n%2 == 1 {
				mime = "image/png"
			}
			request.Images = append(request.Images, ImageContent{Data: fmt.Sprintf("AAAA%d", n), MimeType: mime})
		}
		result := ClassifyOpenAIDecisions(t.Context(), model, request, ClassifierOptions{APIKey: probe.APIKey})
		closeClassifierIdleConnections(t)
		server.Close()
		answers, err := jsstring.MarshalJSON(result.Answers)
		if err != nil {
			t.Fatal(err)
		}
		var problems []string
		if string(result.StopReason) != want[i].StopReason {
			problems = append(problems, fmt.Sprintf("stopReason PiG %q Pi %q", result.StopReason, want[i].StopReason))
		}
		wantMessage := ""
		if want[i].ErrorMessage != nil {
			wantMessage = *want[i].ErrorMessage
		}
		if result.ErrorMessage != wantMessage {
			problems = append(problems, fmt.Sprintf("errorMessage\n   PiG %q\n   Pi  %q", result.ErrorMessage, wantMessage))
		}
		if string(answers) != want[i].AnswersJSON {
			problems = append(problems, fmt.Sprintf("answers\n   PiG %s\n   Pi  %s", answers, want[i].AnswersJSON))
		}
		var gotUsage map[string]any
		if result.Usage != nil {
			encoded, _ := json.Marshal(result.Usage)
			_ = json.Unmarshal(encoded, &gotUsage)
		}
		if !reflect.DeepEqual(gotUsage, want[i].Usage) {
			problems = append(problems, fmt.Sprintf("usage\n   PiG %v\n   Pi  %v", gotUsage, want[i].Usage))
		}
		if len(seen) != 0 || len(want[i].Requests) != 0 {
			if !reflect.DeepEqual(seen, want[i].Requests) {
				problems = append(problems, fmt.Sprintf("requests\n   PiG %.500s\n   Pi  %.500s", decisionsOracleJSON(seen), decisionsOracleJSON(want[i].Requests)))
			}
		}
		if len(problems) > 0 {
			if failures++; failures <= 8 {
				label, _ := json.Marshal(struct {
					Base, Body string
					Images     int
					Cost       json.RawMessage
				}{probe.BaseSuffix, probe.Body, probe.Images, probe.Cost})
				t.Errorf("probe %d %.600s:\n  %s", i, label, strings.Join(problems, "\n  "))
			}
		}
	}
	if failures > 8 {
		t.Errorf("%d of %d probes differ from Pi", failures, len(probes))
	}
}

func decisionsOracleJSON(v any) string {
	encoded, _ := json.Marshal(v)
	return string(encoded)
}
