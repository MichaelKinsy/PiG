package ai

import (
	"bytes"
	"encoding/json"
	"sort"
	"testing"

	"github.com/MichaelKinsy/PiG/internal/jsstring"
)

// The rule this census guards is the read side of marshalerCensus: encoding/json reads the escape of a lone UTF-16 surrogate (`\ud83d`) as
// U+FFFD, where JSON.parse reads the unit. A provider's stream, a session line, models.json and an extension's payload are JSON from outside
// PiG, and a string read from one goes on into a session line, an RPC line or the next request, where Pi writes the unit. So an UnmarshalJSON
// method whose strings take that way reads through jsstring.UnmarshalKeepingSurrogates (or the sdk decoder), and its row proves it with a
// document. The census is of receivers only: the decodes that are plain calls are classed in the audit's record, not here.

// readCensusRow classes one receiver of UnmarshalJSON. Exactly one of probe, table, other and open is set.
type readCensusRow struct {
	// probe decodes a document that holds the escape units times, through encoding/json's Unmarshal, which calls the method, and returns what
	// was read: written by the stringify encoder it holds the escape units times and no U+FFFD.
	probe func() (any, error)
	units int
	// table names the rows that compare what the receiver read with Pi's bytes.
	table string
	// other says why a lone unit cannot reach the receiver, or why reading it as U+FFFD changes no byte Pi writes.
	other string
	// open says where the receiver's strings come from when no row proves yet that the unit is kept: a read of the source only.
	open string
}

const (
	readCensusNumber     = "a number or an index: no string"
	readCensusCredential = "a credential or model record of a file PiG writes, or an answer of a login flow: tokens and identifiers, not text Pi writes again"
	readCensusSettings   = "a value of the client or model configuration that is compared with the values PiG knows, not written again"
	readCensusClassifier = "a classifier question, context or answer (ai/classifier*.go): no row proves the unit is kept"
	readCensusStream     = "TestStreamDeltasKeepSurrogatesAsPi"
)

func readCensusAssistant() (any, error) {
	var message AssistantMessage
	err := json.Unmarshal([]byte(`{"role":"assistant","content":[{"type":"thinking","thinking":"p\ud83dq"},{"type":"text","text":"p\ud83dq"},`+
		`{"type":"toolCall","id":"call_1","name":"p\ud83dq","arguments":{"k\ud83d":"p\ud83dq"}}],"api":"openai-completions","provider":"p","model":"m",`+
		`"usage":{"input":0,"output":0,"cacheRead":0,"cacheWrite":0,"totalTokens":0,"cost":{"input":0,"output":0,"cacheRead":0,"cacheWrite":0,"total":0}},"stopReason":"toolUse","timestamp":1}`), &message)
	return message, err
}

// unmarshalerCensus classes every receiver of UnmarshalJSON in ai, ai/providers/bedrock and agent. A receiver that is not here fails the
// census by name: its author classes it, and a receiver whose strings reach a session line, an RPC line or a request gets a probe.
var unmarshalerCensus = map[string]readCensusRow{
	"ai.AssistantMessage": {probe: readCensusAssistant, units: 5},
	"ai.ContentBlocks":    {probe: readCensusAssistant, units: 5},
	"ai.ToolCall":         {probe: readCensusAssistant, units: 5},
	"ai.ThinkingContent":  {probe: readCensusAssistant, units: 5},
	"ai.ToolResultMessage": {probe: func() (any, error) {
		var message ToolResultMessage
		err := json.Unmarshal([]byte(`{"role":"toolResult","toolCallId":"call_1","toolName":"p\ud83dq","content":[{"type":"text","text":"p\ud83dq"}],"details":{"d":"p\ud83dq"},"isError":false,"timestamp":1}`), &message)
		return message, err
	}, units: 3},
	"ai.ToolSchema": {probe: func() (any, error) {
		var schema ToolSchema
		err := json.Unmarshal([]byte(`{"name":"p\ud83dq","description":"p\ud83dq","parameters":{"type":"object","properties":{"k\ud83d":{"type":"string","description":"p\ud83dq"}}}}`), &schema)
		return schema, err
	}, units: 4},
	"ai.OpenAICompat": {probe: func() (any, error) {
		var compat OpenAICompat
		err := json.Unmarshal([]byte(`{"openRouterRouting":{"sort":"p\ud83dq","k\ud83d":["p\ud83dq"]},"x\ud83d":"p\ud83dq"}`), &compat)
		return compat, err
	}, units: 5},

	"ai.geminiFunctionCall": {table: readCensusStream + " (google-generative-ai rows: the name and the arguments of a called function)"},
	"ai.oaiChunkChoice":     {table: readCensusStream + " (openai-completions rows: choice delta content, reasoning and tool call arguments)"},
	"ai.anthStartInput":     {open: "the input of a tool_use block as content_block_start gives it (the anthropic stream); the rows of " + readCensusStream + " start a call with an empty input"},

	"ai.anthBlockIndex": {other: readCensusNumber}, "ai.piMessagesContentIndex": {other: readCensusNumber}, "ai.usageInt": {other: readCensusNumber},
	"ai.Usage": {other: "token counts and costs: numbers"}, "ai.OpenRouterRate": {other: "a price of the OpenRouter model list: a number or a numeric string"},
	"ai.ThinkingBudgets": {other: "token budgets: numbers"}, "agent.jsInt64": {other: readCensusNumber},
	"ai.mistralFinishReason": {other: "a finish reason of the Mistral stream: compared with the reasons the reader knows, and named in an error text otherwise"},

	"ai.Credential": {other: readCensusCredential}, "ai.rawCredential": {other: readCensusCredential}, "ai.OAuthCredentials": {other: readCensusCredential},
	"ai.AuthResult": {other: readCensusCredential}, "ai.ModelsStoreEntry": {other: readCensusCredential},
	"ai.UserAgentMode": {other: readCensusSettings}, "ai.ClientIdentity": {other: readCensusSettings}, "ai.DeferredOption": {other: readCensusSettings},

	"ai.NestedToolCallRecord":       {open: "a nested tool call of a tool result, as a session line holds it"},
	"ai.OrderedSections":            {open: "the named sections of a system message, as a session line holds them"},
	"ai.AssistantMessageDiagnostic": {open: "a diagnostic of an assistant message, as a session line holds it"},
	"ai.responsesTemplatePart":      {open: "a part of a prompt template of the Responses API"},
	"ai.responsesSlotKey":           {open: "the key of an output slot of the Responses stream"},
	"ai.ClassifierQuestions":        {open: readCensusClassifier}, "ai.ClassifierContext": {open: readCensusClassifier}, "ai.ClassifierAnswers": {open: readCensusClassifier},
	"ai.ClassifierResult": {open: readCensusClassifier}, "ai.ImagesContext": {open: readCensusClassifier}, "ai.AssistantImages": {open: readCensusClassifier},
	"agent.AgentMessage":               {open: "a message of the agent loop, as a session line and an RPC line hold it; parity scenario tools/35 reads a session Pi wrote through it"},
	"agent.ProxyAssistantMessageEvent": {open: "an event of the proxy stream (agent/proxy.go; pi: packages/agent/src/proxy.ts:186 JSON.parse)"},
}

// Every UnmarshalJSON receiver is classed, and one with a probe reads a lone UTF-16 surrogate as JSON.parse does. A new receiver fails here
// by name until it is classed; a probed receiver put back on bare encoding/json fails by the bytes of its probe.
func TestEveryUnmarshalerIsClassedAndReadsALoneSurrogate(t *testing.T) {
	receivers := censusReceivers(t, "UnmarshalJSON", "UnmarshalJSONFrom")
	names := make([]string, 0, len(receivers))
	for name := range receivers {
		names = append(names, name)
	}
	sort.Strings(names)
	for name := range unmarshalerCensus {
		if _, ok := receivers[name]; !ok {
			t.Errorf("%s is in the census and has no UnmarshalJSON method any more: remove its row", name)
		}
	}
	for _, name := range names {
		t.Run(name, func(t *testing.T) {
			row, ok := unmarshalerCensus[name]
			if !ok {
				t.Fatalf("%s (%s) has an UnmarshalJSON method and no row in unmarshalerCensus: class it, with a probe when its strings reach a session line, an RPC line or a request", name, receivers[name][0].at)
			}
			if set := censusCount(row.probe != nil) + censusCount(row.table != "") + censusCount(row.other != "") + censusCount(row.open != ""); set != 1 {
				t.Fatalf("%s: exactly one of probe, table, other and open classes a receiver, %d are set", name, set)
			}
			if row.probe == nil {
				return
			}
			value, err := row.probe()
			if err != nil {
				t.Fatal(err)
			}
			encoded, err := jsstring.MarshalJSON(value)
			if err != nil {
				t.Fatal(err)
			}
			if got := bytes.Count(encoded, []byte(loneSurrogateProbeEscape)); got != row.units || bytes.Contains(encoded, []byte("\uFFFD")) {
				t.Errorf("the value read holds the lone surrogate %d times, the document %d times: %s", got, row.units, encoded)
			}
		})
	}
}
