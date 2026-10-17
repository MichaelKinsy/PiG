package ai

// pi: packages/ai/src/providers/radius-config.ts

import (
	"bytes"
	"encoding/json"
	"math"
	"math/rand"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	sdkjson "github.com/MichaelKinsy/PiG/extensions/sdk/json"
	"github.com/MichaelKinsy/PiG/internal/testenv"
)

type radiusProbe struct {
	Op      string  `json:"op"`
	Value   string  `json:"value"`
	Gateway *string `json:"gateway"`
}

type radiusConfigAnswer struct {
	OK      bool                         `json:"ok"`
	BaseURL string                       `json:"baseUrl"`
	IDs     []string                     `json:"ids"`
	Models  []map[string]json.RawMessage `json:"models"`
	Bound   [][]string                   `json:"bound"`
}

// radiusModelMembers are the members of a gateway model Pig carries. Pi's member is read by its exact key; a number keeps its integer part
// clamped to the int range (the radius oracle writes Infinity as a string), a non-string input entry is skipped, and any other member Pig
// cannot represent is its zero value.
type radiusModelMembers struct {
	Name             string
	Reasoning        bool
	Input            []string
	ContextWindow    int
	MaxTokens        int
	Cost             ModelCost
	Headers          map[string]string
	ThinkingLevelMap ThinkingLevelMap
}

func pigRadiusModelMembers(model RadiusGatewayModel) radiusModelMembers {
	return radiusModelMembers{model.Name, model.Reasoning, model.Input, model.ContextWindow, model.MaxTokens, model.Cost, model.Headers, model.ThinkingLevelMap}
}

func piRadiusModelMembers(model map[string]json.RawMessage) radiusModelMembers {
	integer := func(raw json.RawMessage) int {
		var number float64
		var text string
		if json.Unmarshal(raw, &text) == nil {
			number = map[string]float64{"Infinity": math.Inf(1), "-Infinity": math.Inf(-1)}[text]
		} else if json.Unmarshal(raw, &number) != nil {
			return 0
		}
		if number >= math.MaxInt {
			return math.MaxInt
		}
		if number <= math.MinInt {
			return math.MinInt
		}
		return int(math.Trunc(number))
	}
	var members radiusModelMembers
	_ = json.Unmarshal(model["name"], &members.Name)
	_ = json.Unmarshal(model["reasoning"], &members.Reasoning)
	var input []any
	_ = json.Unmarshal(model["input"], &input)
	members.Input = []string{}
	for _, entry := range input {
		if text, ok := entry.(string); ok {
			members.Input = append(members.Input, text)
		}
	}
	members.ContextWindow, members.MaxTokens = integer(model["contextWindow"]), integer(model["maxTokens"])
	if json.Unmarshal(model["cost"], &members.Cost) != nil {
		members.Cost = ModelCost{}
	}
	if model["headers"] != nil && json.Unmarshal(model["headers"], &members.Headers) != nil {
		members.Headers = nil
	}
	if model["thinkingLevelMap"] != nil && json.Unmarshal(model["thinkingLevelMap"], &members.ThinkingLevelMap) != nil {
		members.ThinkingLevelMap = nil
	}
	return members
}

// radiusProbes cover gateway URL spellings (scheme case and look-alike letters, repeated and mixed trailing slashes, whitespace, empty), HTTP bodies around the
// 512-unit truncation with surrogate pairs and the whitespace JavaScript trims and Go does not (and the reverse), and gateway configs whose fields have each JSON type.
func radiusProbes() []radiusProbe {
	random := rand.New(rand.NewSource(20260940))
	pieces := []string{"https://", "HTTP://", "hTtPs://", "http:/", "ftp://", "httpſ://", "HTTPſ://", "http\u212a://", "ſ", "\u212a", "radius.example", "localhost:8788", "/", "//", "///", "\t", " ", "\u00a0", "\ufeff", "\u0085", "\u2028", "\n", "é", "日本", "😀", "", "?q=1", "#h", "/path/"}
	var probes []radiusProbe
	for range 1500 {
		var value string
		for range 1 + random.Intn(5) {
			value += pieces[random.Intn(len(pieces))]
		}
		probes = append(probes, radiusProbe{Op: "norm", Value: value})
	}
	for range 600 {
		pad := []string{"", " ", "\t\n", "\ufeff", "\u0085", "\u00a0\u2028", "\u200b", "\u180e"}
		body := pad[random.Intn(len(pad))] + strings.Repeat([]string{"a", "é", "😀", "日"}[random.Intn(4)], 500+random.Intn(25)) + pad[random.Intn(len(pad))]
		if random.Intn(3) == 0 {
			body = pad[random.Intn(len(pad))] + []string{"", "short", "x"}[random.Intn(3)] + pad[random.Intn(len(pad))]
		}
		probes = append(probes, radiusProbe{Op: "trunc", Value: body})
	}
	field := func(good string, bad ...string) string {
		if random.Intn(4) == 0 {
			return bad[random.Intn(len(bad))]
		}
		return good
	}
	model := func() string {
		parts := []string{
			`"id":` + field(`"m`+string(rune('a'+random.Intn(26)))+`"`, `1`, `null`, `[]`, `""`),
			`"name":` + field(`"N"`, `1`, `null`, `{}`),
			`"reasoning":` + field(`true`, `"true"`, `1`, `null`, `false`),
			`"input":` + field(`["text"]`, `"text"`, `{}`, `null`, `[]`, `["audio",1]`, `["text","image"]`),
			`"cost":` + field(`{"input":1,"output":2,"cacheRead":0,"cacheWrite":0}`, `null`, `[]`, `"x"`, `{}`, `{"input":"x"}`, `{"input":null}`, `{"tiers":3}`, `1`),
			`"contextWindow":` + field(`1000`, `"1"`, `null`, `1.5`, `1e30`, `-1`, `0`, `1e3`, `3e9`, `3000000000`, `1e400`, `1000.0`),
			`"maxTokens":` + field(`100`, `"1"`, `null`, `2.5`, `1e40`, `-7`, `-2.5`, `-1e400`, `9007199254740993`),
		}
		// encoding/json matches a struct field case-insensitively; JavaScript reads the exact key.
		if random.Intn(4) == 0 {
			parts = append(parts, []string{`"ID":"zz"`, `"Name":"Q"`, `"contextwindow":5`, `"MaxTokens":9`, `"INPUT":["image"]`, `"Reasoning":"x"`, `"COST":{"input":7}`}[random.Intn(7)])
		}
		if random.Intn(3) == 0 {
			parts = append(parts, `"thinkingLevelMap":`+[]string{`{}`, `null`, `{"high":"x"}`, `[]`, `3`, `{"high":null}`}[random.Intn(6)])
		}
		if random.Intn(4) == 0 {
			parts = append(parts, `"headers":`+[]string{`{"A":"b"}`, `{"A":1}`, `null`, `[]`}[random.Intn(4)])
		}
		for i := range parts {
			j := random.Intn(i + 1)
			parts[i], parts[j] = parts[j], parts[i]
		}
		return "{" + strings.Join(parts, ",") + "}"
	}
	for range 1500 {
		var models []string
		for range random.Intn(4) {
			if random.Intn(8) == 0 {
				models = append(models, []string{`null`, `1`, `"x"`, `[]`, `{}`}[random.Intn(5)])
			} else {
				models = append(models, model())
			}
		}
		config := `{"baseUrl":` + field(`"https://r.example/v1"`, `1`, `null`, `[]`, `""`) + `,"models":` + field("["+strings.Join(models, ",")+"]", `{}`, `null`, `"x"`, `1`)
		if random.Intn(6) == 0 {
			config += []string{`,"Models":5`, `,"MODELS":[]`, `,"BaseURL":"https://other.example"`, `,"baseurl":1`}[random.Intn(4)]
		}
		config += `}`
		switch random.Intn(12) {
		case 0:
			config = []string{`null`, `[]`, `"x"`, `1`, `true`, `{}`}[random.Intn(6)]
		case 1:
			probes = append(probes, radiusProbe{Op: "config"})
			continue
		}
		probes = append(probes, radiusProbe{Op: "config", Gateway: &config})
	}
	return probes
}

// radius-config.ts runs in Node against the same probes: normalizeRadiusGatewayUrl, truncateHttpBody (cut from the source) and getRadiusCredentialConfig
// with getRadiusModels (which models, which of their members and which base URL a credential's cached gateway config yields) must match Pi's.
func TestRadiusConfigMatchesPiOnSeededProbes(t *testing.T) {
	probes := radiusProbes()
	payload, err := json.Marshal(probes)
	if err != nil {
		t.Fatal(err)
	}
	source := filepath.Join(testenv.ModuleRoot(t), ".upstream", "current", "packages", "ai", "src", "providers", "radius-config.ts")
	cmd := exec.CommandContext(t.Context(), "node", "--disable-warning=ExperimentalWarning", "testdata/radius_config.mjs", source)
	cmd.Stdin = bytes.NewReader(payload)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	output, err := cmd.Output()
	if err != nil {
		t.Fatalf("Pi oracle: %v\n%s", err, &stderr)
	}
	var expected []json.RawMessage
	if err := json.Unmarshal(output, &expected); err != nil {
		t.Fatal(err)
	}
	if len(expected) != len(probes) {
		t.Fatalf("Pi answered %d of %d probes", len(expected), len(probes))
	}
	accepted, differing := 0, 0
	for i, probe := range probes {
		var got, want any
		switch probe.Op {
		case "norm":
			got = NormalizeRadiusGatewayURL(probe.Value)
			var s string
			_ = json.Unmarshal(expected[i], &s)
			want = s
		case "trunc":
			got = truncateHTTPBody(probe.Value)
			// A cut through a surrogate pair leaves Pi's text with a lone high surrogate; the WTF-8 decoder keeps it as PiG's strings carry it.
			var s string
			_ = sdkjson.Unmarshal(expected[i], &s)
			want = s
		default:
			var credential *Credential
			if probe.Gateway == nil {
				credential = &Credential{}
			} else {
				credential = &Credential{GatewayConfig: json.RawMessage(*probe.Gateway)}
			}
			answer := radiusConfigAnswer{}
			var members []radiusModelMembers
			if config, ok := GetRadiusCredentialConfig(credential); ok {
				answer = radiusConfigAnswer{OK: true, BaseURL: config.BaseURL, IDs: []string{}, Bound: [][]string{}}
				for _, model := range config.Models {
					answer.IDs = append(answer.IDs, model.ID)
					members = append(members, pigRadiusModelMembers(model))
				}
				for _, model := range GetRadiusModels("radius", credential) {
					answer.Bound = append(answer.Bound, []string{model.ID, model.ProviderID(), model.ProviderMeta.BaseURL, string(model.ProviderMeta.API)})
				}
				accepted++
			}
			var piAnswer radiusConfigAnswer
			if err := json.Unmarshal(expected[i], &piAnswer); err != nil {
				t.Fatal(err)
			}
			var piMembers []radiusModelMembers
			for _, model := range piAnswer.Models {
				piMembers = append(piMembers, piRadiusModelMembers(model))
			}
			piAnswer.Models = nil
			got, want = []any{answer, members}, []any{piAnswer, piMembers}
		}
		if !reflect.DeepEqual(got, want) {
			if differing++; differing <= 10 {
				gateway := ""
				if probe.Gateway != nil {
					gateway = *probe.Gateway
				}
				t.Errorf("%s %q %s: Pig %+v, Pi %+v", probe.Op, probe.Value, gateway, got, want)
			}
		}
	}
	if differing > 10 {
		t.Errorf("%d of %d probes differ from Pi", differing, len(probes))
	}
	if accepted < 200 {
		t.Errorf("config probes are one-sided: %d accepted", accepted)
	}
}
