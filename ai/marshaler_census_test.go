package ai

import (
	"bytes"
	"encoding/json"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"slices"
	"sort"
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/internal/jsstring"
)

// The rule this census guards: a Go string holds a lone UTF-16 surrogate as three bytes that encoding/json writes as three U+FFFD, where
// JSON.stringify writes the unit's escape. The tree's own JSON.stringify encoder (jsstring.MarshalJSON) owns such text: it reaches every string
// by reflection, and for a value with a MarshalJSON method it copies what the method returns. So a marshaler method is right only when its bytes
// come from jsstring.MarshalKeepingSurrogates (or from the stringify appender), never from bare encoding/json. The request encoder's first
// encode calls MarshalJSONTo and may write U+FFFD: that is what sends the body to the second encode, which calls MarshalJSON; a MarshalJSONTo
// can therefore not put U+FFFD on the wire, and a MarshalJSON on bare encoding/json can.
//
// An encoder option (a string rule in the json/v2 options) was measured and rejected: it does not reach map keys, named string types, token
// writers or nested MarshalJSON, and it costs a well-formed encode +5 allocations and +42 % time.

// loneSurrogateProbe is a text with U+D83D alone, in the three-byte form a Go string holds; loneSurrogateProbeEscape is how JSON.stringify writes that unit.
const (
	loneSurrogateProbe       = "p\xed\xa0\xbdq"
	loneSurrogateProbeEscape = `\ud83d`
)

// censusRow classes one receiver of MarshalJSON or MarshalJSONTo. Exactly one of probe, table and other is set.
type censusRow struct {
	// probe builds a value of the receiver whose free text holds the probe: its MarshalJSON, the request encoder and the stringify encoder must
	// each write the escape and no U+FFFD.
	probe func() any
	// sameUnderEncodingJSON: json.Marshal of the probe (which calls MarshalJSONTo) equals MarshalJSON, as the request encoder's first encode relies on.
	sameUnderEncodingJSON bool
	// table names the rows of the wire tables that compare a body holding the receiver with Pi's bytes; the receiver's MarshalJSON must still
	// not write through bare encoding/json.
	table string
	// other says what the receiver is when it is no part of a provider request body.
	other string
}

func censusProbeSchema() map[string]any {
	return map[string]any{"type": "object", "properties": map[string]any{loneSurrogateProbe: map[string]any{"type": "string", "description": loneSurrogateProbe}}}
}

const (
	censusWireTable     = "TestToolLoneSurrogatesReachTheWireAsPi"
	censusAuthFile      = "a credential or model record of a file on disk, not a provider request"
	censusAuthPrompt    = "a prompt or event of a login flow, not a provider request"
	censusClassifier    = "a classifier question, context or answer (ai/classifier*.go); no row of the provider tables holds one"
	censusStreamEvent   = "an event of the assistant stream: written into RPC lines by the stringify encoder, not into a provider request"
	censusSessionRecord = "a message as a session line and an RPC line hold it; TestCalledToolNameKeepsALoneSurrogateOfTheStream and parity scenario tools/34 write one"
	censusSystemOne     = "a System One request or asset (ai/system_one.go); no row of the provider tables holds one"
	censusArguments     = "the arguments of a tool call as a message holds them; a provider request writes them through its own converter (the history rows of TestToolLoneSurrogatesReachTheWireAsPi)"
	censusSettings      = "a value of the model or client configuration, not text of a user, a model, a tool or an extension"
)

// marshalerCensus classes every receiver of MarshalJSON or MarshalJSONTo in ai, ai/providers/bedrock and agent. A receiver that is not here fails
// the census by name: its author classes it, and a receiver a provider request can hold gets a probe.
// The probes of the Mistral receivers are in marshaler_census_mistral_test.go, which a build that strips mistral-conversations does not have; its
// twin marshaler_census_mistral_stripped_test.go classes the same receivers there, as the census parses every source file whatever its build tags.
var marshalerCensus = map[string]censusRow{
	"ai.anthTool": {probe: func() any {
		return anthTool{Name: loneSurrogateProbe, Description: loneSurrogateProbe, InputSchema: censusProbeSchema()}
	}, sameUnderEncodingJSON: true},
	"ai.oaiToolFunction": {probe: func() any {
		return oaiToolFunction{Name: loneSurrogateProbe, Description: loneSurrogateProbe, Parameters: censusProbeSchema()}
	}, sameUnderEncodingJSON: true},
	"ai.ResponsesTool": {probe: func() any {
		return ResponsesTool{Type: "function", Name: loneSurrogateProbe, Description: loneSurrogateProbe, Parameters: censusProbeSchema()}
	}, sameUnderEncodingJSON: true},
	"ai.ResponsesInputItem": {probe: func() any {
		return ResponsesInputItem{Type: "function_call", CallID: "call_1", Name: loneSurrogateProbe, Arguments: json.RawMessage(`"{}"`)}
	}},
	"ai.anthContentBlock": {probe: func() any {
		return anthContentBlock{Type: "tool_use", ID: "toolu_1", Name: loneSurrogateProbe, Input: map[string]any{"k": loneSurrogateProbe}}
	}},
	"ai.oaiMessage": {probe: func() any {
		return oaiMessage{Role: "tool", Content: "r", ToolCallID: "call_1", ToolName: loneSurrogateProbe}
	}},
	"ai.geminiFunctionDecl": {probe: func() any {
		return geminiFunctionDecl{{Key: "name", Value: loneSurrogateProbe}, {Key: "description", Value: loneSurrogateProbe}}
	}},
	"ai.ToolSchema": {probe: func() any {
		return ToolSchema{Name: loneSurrogateProbe, Description: loneSurrogateProbe, Parameters: censusProbeSchema()}
	}},
	"ai.compatObject": {probe: func() any {
		return compatObject{value: map[string]any{"sort": loneSurrogateProbe, "order": []any{loneSurrogateProbe}, loneSurrogateProbe: map[string]any{"k": loneSurrogateProbe}},
			order: schemaObjectOrder{"/openRouterRouting": {"sort", "order", loneSurrogateProbe}}, path: "/openRouterRouting"}
	}},
	// A compat is copied through its JSON form (a model record, an extension's registration), so its marshaler writes the unit its reader keeps.
	"ai.OpenAICompat": {probe: func() any {
		return OpenAICompat{OpenRouterRouting: OpenRouterRouting{"sort": loneSurrogateProbe}, Extra: map[string]json.RawMessage{loneSurrogateProbe: json.RawMessage(`1`)}}
	}},
	"ai.ToolCall":    {probe: func() any { return ToolCall{ID: "call_1", Name: loneSurrogateProbe} }, sameUnderEncodingJSON: true},
	"ai.TextContent": {probe: func() any { return TextContent{Text: loneSurrogateProbe} }, sameUnderEncodingJSON: true},

	"ai.respRequest":              {table: censusWireTable + " (openai-responses and azure-openai-responses rows)"},
	"ai.geminiWireObject":         {table: censusWireTable + " (google-generative-ai rows)"},
	"ai.encodedArguments":         {other: censusArguments},
	"ai.orderedArguments":         {other: censusArguments},
	"ai.ContentBlocks":            {other: censusSessionRecord},
	"ai.jsonSchemaToolParameters": {other: "the parameters of a constrained-sampling tool; no row of the provider tables declares one"},

	"ai.AssistantMessage":     {other: censusSessionRecord},
	"ai.ToolResultMessage":    {other: censusSessionRecord},
	"ai.UserMessage":          {other: censusSessionRecord},
	"ai.SystemMessage":        {other: censusSessionRecord},
	"ai.NestedToolCallRecord": {other: censusSessionRecord},
	"ai.ImageContent":         {other: censusSessionRecord},
	"ai.ThinkingContent":      {other: censusSessionRecord},
	"ai.Usage":                {other: "token counts and costs: numbers"},
	"ai.OrderedSections":      {other: "the named sections of a system message, as a session line holds them"},

	"ai.StartEvent": {other: censusStreamEvent}, "ai.DoneEvent": {other: censusStreamEvent}, "ai.ErrorEvent": {other: censusStreamEvent},
	"ai.TextStartEvent": {other: censusStreamEvent}, "ai.TextDeltaEvent": {other: censusStreamEvent}, "ai.TextEndEvent": {other: censusStreamEvent},
	"ai.ThinkingStartEvent": {other: censusStreamEvent}, "ai.ThinkingDeltaEvent": {other: censusStreamEvent}, "ai.ThinkingEndEvent": {other: censusStreamEvent},
	"ai.ToolCallStartEvent": {other: censusStreamEvent}, "ai.ToolCallDeltaEvent": {other: censusStreamEvent}, "ai.ToolCallEndEvent": {other: censusStreamEvent},
	"ai.AssistantMessageDiagnostic": {other: "a diagnostic of an assistant message, as a session line holds it"},

	"ai.authStorageData": {other: censusAuthFile}, "ai.Credential": {other: censusAuthFile}, "ai.OAuthCredentials": {other: censusAuthFile}, "ai.ModelsStoreEntry": {other: censusAuthFile},
	"ai.AuthDeviceCodeEvent": {other: censusAuthPrompt}, "ai.AuthInfoEvent": {other: censusAuthPrompt}, "ai.AuthManualCodePrompt": {other: censusAuthPrompt},
	"ai.AuthProgressEvent": {other: censusAuthPrompt}, "ai.AuthSecretPrompt": {other: censusAuthPrompt}, "ai.AuthSelectPrompt": {other: censusAuthPrompt},
	"ai.AuthTextPrompt": {other: censusAuthPrompt}, "ai.AuthURLEvent": {other: censusAuthPrompt},

	"ai.ClassifierAnswers": {other: censusClassifier}, "ai.ClassifierBoolAnswer": {other: censusClassifier}, "ai.ClassifierBoolQuestion": {other: censusClassifier},
	"ai.ClassifierChoiceAnswer": {other: censusClassifier}, "ai.ClassifierChoiceQuestion": {other: censusClassifier}, "ai.ClassifierContext": {other: censusClassifier},
	"ai.ClassifierQuestions": {other: censusClassifier}, "ai.ClassifierResult": {other: censusClassifier}, "ai.ClassifierScoreAnswer": {other: censusClassifier},
	"ai.ClassifierScoreQuestion": {other: censusClassifier}, "ai.AssistantImages": {other: censusClassifier}, "ai.ImagesContext": {other: censusClassifier},

	"ai.SystemOneImageAsset": {other: censusSystemOne}, "ai.SystemOneImageAssets": {other: censusSystemOne}, "ai.SystemOneWireQuestions": {other: censusSystemOne},
	"ai.SystemOneWireRequest": {other: censusSystemOne},

	"ai.ClientIdentity": {other: censusSettings}, "ai.UserAgentMode": {other: censusSettings}, "ai.DeferredOption": {other: censusSettings},
	"ai.ThinkingBudgets": {other: censusSettings},

	"agent.AgentMessage":               {other: censusSessionRecord},
	"agent.AssistantMessage":           {other: censusSessionRecord},
	"agent.ToolResultMessage":          {other: censusSessionRecord},
	"agent.ProxyAssistantMessageEvent": {other: "an event of the proxy stream between two PiG processes, not a provider request"},
}

// marshalerMethod is a MarshalJSON or MarshalJSONTo method found in the source.
type marshalerMethod struct {
	name string
	body *ast.BlockStmt
	at   string
}

// marshalerReceivers are the receivers of MarshalJSON and MarshalJSONTo.
func marshalerReceivers(t *testing.T) map[string][]marshalerMethod {
	return censusReceivers(t, "MarshalJSON", "MarshalJSONTo")
}

// censusReceivers parses the non-test files of ai, ai/providers/bedrock and agent and returns the methods of each receiver that are named in methods.
func censusReceivers(t *testing.T, methods ...string) map[string][]marshalerMethod {
	t.Helper()
	receivers := map[string][]marshalerMethod{}
	files := token.NewFileSet()
	for _, directory := range []struct{ path, name string }{{".", "ai"}, {"providers/bedrock", "bedrock"}, {"../agent", "agent"}} {
		entries, err := os.ReadDir(directory.path)
		if err != nil {
			t.Fatal(err)
		}
		for _, entry := range entries {
			if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".go") || strings.HasSuffix(entry.Name(), "_test.go") {
				continue
			}
			file, err := parser.ParseFile(files, filepath.Join(directory.path, entry.Name()), nil, parser.SkipObjectResolution)
			if err != nil {
				t.Fatal(err)
			}
			for _, declaration := range file.Decls {
				method, ok := declaration.(*ast.FuncDecl)
				if !ok || method.Recv == nil || len(method.Recv.List) != 1 || !slices.Contains(methods, method.Name.Name) {
					continue
				}
				receiver := method.Recv.List[0].Type
				for {
					switch inner := receiver.(type) {
					case *ast.StarExpr:
						receiver = inner.X
						continue
					case *ast.IndexExpr:
						receiver = inner.X
						continue
					case *ast.IndexListExpr:
						receiver = inner.X
						continue
					}
					break
				}
				name, ok := receiver.(*ast.Ident)
				if !ok {
					t.Fatalf("%s: a receiver the census cannot name", files.Position(method.Pos()))
				}
				key := directory.name + "." + name.Name
				receivers[key] = append(receivers[key], marshalerMethod{name: method.Name.Name, body: method.Body, at: files.Position(method.Pos()).String()})
			}
		}
	}
	return receivers
}

// writesThroughEncodingJSON reports the first call of encoding/json's own writer in body: json.Marshal, json.MarshalIndent or json.NewEncoder.
func writesThroughEncodingJSON(body *ast.BlockStmt) string {
	found := ""
	ast.Inspect(body, func(node ast.Node) bool {
		call, ok := node.(*ast.CallExpr)
		if !ok || found != "" {
			return found == ""
		}
		if selector, ok := call.Fun.(*ast.SelectorExpr); ok {
			if pkg, ok := selector.X.(*ast.Ident); ok && pkg.Name == "json" && (selector.Sel.Name == "Marshal" || selector.Sel.Name == "MarshalIndent" || selector.Sel.Name == "NewEncoder") {
				found = "json." + selector.Sel.Name
			}
		}
		return true
	})
	return found
}

// Every MarshalJSON and MarshalJSONTo receiver is classed, and one a provider request can hold writes a lone UTF-16 surrogate as JSON.stringify
// does through each encoder that can reach it. A new receiver fails here by name until it is classed; a MarshalJSON put back on bare
// encoding/json fails twice, by its source and by the bytes of its probe.
func TestEveryMarshalerWritesALoneSurrogateAsItsEscape(t *testing.T) {
	receivers := marshalerReceivers(t)
	names := make([]string, 0, len(receivers))
	for name := range receivers {
		names = append(names, name)
	}
	sort.Strings(names)
	for name := range marshalerCensus {
		if _, ok := receivers[name]; !ok {
			t.Errorf("%s is in the census and has no MarshalJSON or MarshalJSONTo method any more: remove its row", name)
		}
	}
	holdsTheEscape := func(t *testing.T, what string, encoded []byte, err error) {
		t.Helper()
		if err != nil {
			t.Errorf("%s: %v", what, err)
			return
		}
		if bytes.Contains(encoded, []byte("\uFFFD")) || !bytes.Contains(encoded, []byte(loneSurrogateProbeEscape)) {
			t.Errorf("%s does not write the lone surrogate as %s: %s", what, loneSurrogateProbeEscape, encoded)
		}
	}
	for _, name := range names {
		t.Run(name, func(t *testing.T) {
			row, ok := marshalerCensus[name]
			if !ok {
				t.Fatalf("%s (%s) has a marshaler method and no row in marshalerCensus: class it, with a probe when a provider request can hold it", name, receivers[name][0].at)
			}
			if set := censusCount(row.probe != nil) + censusCount(row.table != "") + censusCount(row.other != ""); set != 1 {
				t.Fatalf("%s: exactly one of probe, table and other classes a receiver, %d are set", name, set)
			}
			if row.other != "" {
				return
			}
			hasTo := false
			for _, method := range receivers[name] {
				hasTo = hasTo || method.name == "MarshalJSONTo"
				if writer := writesThroughEncodingJSON(method.body); method.name == "MarshalJSON" && writer != "" {
					t.Errorf("%s: MarshalJSON writes through %s, which writes U+FFFD for a lone surrogate: take the bytes from jsstring.MarshalKeepingSurrogates", method.at, writer)
				}
			}
			if row.probe == nil {
				return
			}
			value := row.probe()
			marshaler, ok := value.(json.Marshaler)
			if !ok {
				t.Fatalf("%T has no MarshalJSON on its value", value)
			}
			own, err := marshaler.MarshalJSON()
			holdsTheEscape(t, "MarshalJSON", own, err)
			body, err := marshalRequestBody(value)
			holdsTheEscape(t, "the request encoder", body, err)
			line, err := jsstring.MarshalJSON(value)
			holdsTheEscape(t, "the stringify encoder", line, err)
			if row.sameUnderEncodingJSON {
				if !hasTo {
					t.Fatalf("%s is classed sameUnderEncodingJSON and has no MarshalJSONTo", name)
				}
				if plain, err := json.Marshal(value); err != nil || !bytes.Equal(plain, own) {
					t.Errorf("json.Marshal, which calls MarshalJSONTo, differs from MarshalJSON:\n got %s\nwant %s (%v)", plain, own, err)
				}
			}
		})
	}
}

func censusCount(set bool) int {
	if set {
		return 1
	}
	return 0
}
