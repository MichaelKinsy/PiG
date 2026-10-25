package agent

import (
	"bytes"
	"math/rand/v2"
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/internal/jsonstringify"
)

// shapeCandidate is one member a generated message may hold: its name and the JSON texts its value may take.
type shapeCandidate struct {
	name     string
	values   []string
	optional bool
}

var (
	shapeStrings  = []string{`"a"`, `""`, `"<&>"`, "\"\u2028\u2029\"", `"é😀"`, `"q\"x\\y"`, `"l\nb\tc\r"`, `"\u0001"`, `"\/"`, `null`, `5`}
	shapeIntegers = []string{`0`, `1`, `-5`, `1767225601000`, `1.5`, `1e3`, `100.0`, `"7"`, `null`, `9007199254740993`}
	shapeNumbers  = []string{`0`, `0.1`, `1.5e-7`, `0.000123`, `100`, `3.25`, `-0`, `1e21`, `0.30000000000000004`, `null`}
	shapeBools    = []string{`true`, `false`, `null`, `0`}
	shapeAnys     = []string{`{"b":1,"a":[{"z":1,"y":"<"}],"c":null}`, `{}`, `[]`, `"s"`, `null`, `{"x":{"b":2.5,"a":"\u2028"}}`, `[1,{"d":0,"c":1}]`, `{"1":0,"a":1}`, `{"a":1,"a":2}`}
)

// shapeNoise is the chance, one in shapeNoise, of each perturbation; a message drawn with a large value stays close to what Pi writes, so that messages with many members are accepted often enough to test.
var shapeNoise = 8

func shapeObject(r *rand.Rand, members []shapeCandidate) string {
	var parts []string
	for _, member := range members {
		if member.optional && r.IntN(2) == 0 {
			continue
		}
		if r.IntN(5*shapeNoise) == 0 {
			continue
		}
		parts = append(parts, `"`+member.name+`":`+member.values[r.IntN(len(member.values))])
	}
	switch r.IntN(shapeNoise) {
	case 0:
		r.Shuffle(len(parts), func(i, j int) { parts[i], parts[j] = parts[j], parts[i] })
	case 1, 2:
		if len(parts) > 1 {
			i := r.IntN(len(parts) - 1)
			parts[i], parts[i+1] = parts[i+1], parts[i]
		}
	}
	if r.IntN(shapeNoise+2) == 0 {
		parts = append(parts, `"extra":1`)
	}
	return "{" + strings.Join(parts, ",") + "}"
}

func shapeValues(r *rand.Rand, values []string) []string {
	// Most members take a value Pi writes; the others may take any value of the list.
	if r.IntN(shapeNoise) == 0 {
		return values
	}
	return values[:1]
}

func shapeMessage(r *rand.Rand) string {
	str := func() []string { return shapeValues(r, shapeStrings) }
	integer := func() []string { return shapeValues(r, shapeIntegers) }
	cost := shapeObject(r, []shapeCandidate{{"input", shapeValues(r, shapeNumbers), false}, {"output", shapeValues(r, shapeNumbers), false}, {"cacheRead", shapeValues(r, shapeNumbers), false}, {"cacheWrite", shapeValues(r, shapeNumbers), false}, {"total", shapeValues(r, shapeNumbers), false}})
	usage := shapeObject(r, []shapeCandidate{{"input", integer(), false}, {"output", integer(), false}, {"cacheRead", integer(), false}, {"cacheWrite", integer(), false}, {"cacheWrite1h", integer(), true}, {"reasoning", integer(), true}, {"totalTokens", integer(), false}, {"cost", []string{cost}, false}})
	block := func(kinds ...string) string {
		switch kinds[r.IntN(len(kinds))] {
		case "text":
			return shapeObject(r, []shapeCandidate{{"type", []string{`"text"`}, false}, {"text", str(), false}, {"textSignature", str(), true}, {"index", []string{`0`}, true}})
		case "image":
			return shapeObject(r, []shapeCandidate{{"type", []string{`"image"`}, false}, {"data", str(), false}, {"mimeType", str(), false}})
		case "thinking":
			return shapeObject(r, []shapeCandidate{{"type", []string{`"thinking"`}, false}, {"thinking", str(), false}, {"thinkingSignature", str(), true}, {"redacted", shapeValues(r, shapeBools), true}})
		default:
			return shapeObject(r, []shapeCandidate{{"type", []string{`"toolCall"`}, false}, {"id", str(), false}, {"name", str(), false}, {"arguments", shapeValues(r, shapeAnys), false}, {"thoughtSignature", str(), true}, {"namespace", str(), true}})
		}
	}
	content := func(kinds ...string) []string {
		var blocks []string
		for range r.IntN(3) {
			blocks = append(blocks, block(kinds...))
		}
		values := []string{"[" + strings.Join(blocks, ",") + "]"}
		if r.IntN(8) == 0 {
			values = append(values, `"plain"`, `null`)
		}
		return values
	}
	switch r.IntN(6) {
	case 5:
		return shapeObject(r, []shapeCandidate{{"a", shapeValues(r, shapeAnys), true}, {"content", content("text", "image"), true}, {"details", shapeValues(r, shapeAnys), true}, {"n", integer(), false}, {"role", []string{`"extensionRole"`}, false}, {"timestamp", integer(), false}})
	case 0:
		return shapeObject(r, []shapeCandidate{{"role", []string{`"user"`}, false}, {"content", content("text", "image"), false}, {"timestamp", integer(), false}})
	case 1:
		// The two member orders AgentMessage.MarshalJSON writes (see assistantShape and assistantErrorShape).
		errorBefore := r.IntN(3) == 0
		members := []shapeCandidate{{"role", []string{`"assistant"`}, false}, {"content", content("text", "thinking", "toolCall"), false}, {"api", str(), false}, {"provider", str(), false}, {"model", str(), false}}
		members = append(members, shapeCandidate{"usage", []string{usage, `null`}[:1+r.IntN(2)], false}, shapeCandidate{"stopReason", str(), false})
		if errorBefore {
			members = append(members, shapeCandidate{"errorMessage", str(), false})
		}
		members = append(members, shapeCandidate{"timestamp", integer(), false})
		if !errorBefore {
			members = append(members, shapeCandidate{"providerThinkingLevel", str(), true})
		}
		members = append(members, shapeCandidate{"responseId", str(), true}, shapeCandidate{"responseModel", str(), true})
		if errorBefore {
			members = append(members, shapeCandidate{"providerThinkingLevel", str(), true}, shapeCandidate{"rawStopReason", str(), true}, shapeCandidate{"endTurn", shapeValues(r, shapeBools), true})
		} else {
			members = append(members, shapeCandidate{"endTurn", shapeValues(r, shapeBools), true}, shapeCandidate{"rawStopReason", str(), true}, shapeCandidate{"errorMessage", str(), true})
		}
		members = append(members, shapeCandidate{"durationMs", integer(), true}, shapeCandidate{"thinkingLevel", str(), true})
		return shapeObject(r, members)
	case 2:
		return shapeObject(r, []shapeCandidate{{"role", []string{`"toolResult"`}, false}, {"toolCallId", str(), false}, {"toolName", str(), false}, {"content", content("text", "image"), false}, {"details", shapeValues(r, shapeAnys), true}, {"usage", []string{usage}, true}, {"isError", shapeValues(r, shapeBools), false}, {"durationMs", integer(), true}, {"timestamp", integer(), false}})
	case 3:
		return shapeObject(r, []shapeCandidate{{"role", []string{`"custom"`}, false}, {"customType", str(), false}, {"content", append(content("text", "image"), `"s"`), false}, {"display", shapeValues(r, shapeBools), false}, {"details", shapeValues(r, shapeAnys), true}, {"timestamp", integer(), false}, {"a", shapeValues(r, shapeAnys), true}, {"zz", shapeValues(r, shapeAnys), true}})
	default:
		return shapeObject(r, []shapeCandidate{{"role", []string{`"bashExecution"`}, false}, {"command", str(), false}, {"output", str(), false}, {"exitCode", integer(), true}, {"cancelled", shapeValues(r, shapeBools), false}, {"truncated", shapeValues(r, shapeBools), false}, {"fullOutputPath", str(), true}, {"timestamp", integer(), false}, {"excludeFromContext", shapeValues(r, shapeBools), true}, {"b", shapeValues(r, shapeAnys), true}})
	}
}

// The member scan may accept a message only when its decoded fields encode to its text once canonicalized, as every writer canonicalizes; accepting any other text would let a message lose the object it was decoded from.
func TestMemberScanAcceptsOnlyTextTheFieldsEncodeTo(t *testing.T) {
	r := rand.New(rand.NewPCG(1, 2))
	accepted := map[string]int{}
	for range 100000 {
		shapeNoise = []int{5, 30}[r.IntN(2)]
		data := []byte(shapeMessage(r))
		var message AgentMessage
		if _, err := message.decode(data); err != nil || !jsonstringify.IsCanonical(data) || !message.encodesAs(data) {
			continue
		}
		encoded, err := message.MarshalJSON()
		if err == nil {
			encoded, err = jsonstringify.Canonicalize(encoded)
		}
		if err != nil || !bytes.Equal(encoded, data) {
			t.Fatalf("scan accepted %s\nfields encode to %s (err %v)", data, encoded, err)
		}
		accepted[message.Role()]++
	}
	t.Logf("accepted per role: %v", accepted)
	for _, role := range []string{RoleUser, RoleAssistant, RoleToolResult, RoleCustom, "bashExecution", "extensionRole"} {
		if accepted[role] < 100 {
			t.Errorf("role %s: scan accepted %d generated messages; the property is not exercised", role, accepted[role])
		}
	}
}

// Messages as Pi writes them take the scan, so decoding one encodes nothing and retains no text.
func TestMemberScanAcceptsMessagesPiWrites(t *testing.T) {
	for _, line := range []string{
		`{"role":"user","content":[{"type":"text","text":"hi <b> & \"q\""},{"type":"image","data":"aGk=","mimeType":"image/png"}],"timestamp":1767225601000}`,
		`{"role":"user","content":"plain","timestamp":1}`,
		`{"role":"assistant","content":[{"type":"thinking","thinking":"t","thinkingSignature":"s"},{"type":"text","text":"x"},{"type":"toolCall","id":"c1","name":"read","arguments":{"path":"a.go","opts":{"z":1,"a":[{"y":2,"b":0.5}]}}}],"api":"openai-completions","provider":"p","model":"m","usage":{"input":10,"output":2,"cacheRead":0,"cacheWrite":0,"totalTokens":12,"cost":{"input":0.0001,"output":0,"cacheRead":0,"cacheWrite":0,"total":0.0001}},"stopReason":"toolUse","timestamp":1767225602000,"responseId":"r","durationMs":5,"thinkingLevel":"high"}`,
		`{"role":"assistant","content":[],"api":"a","provider":"p","model":"m","usage":{"input":0,"output":0,"cacheRead":0,"cacheWrite":0,"totalTokens":0,"cost":{"input":0,"output":0,"cacheRead":0,"cacheWrite":0,"total":0}},"stopReason":"error","errorMessage":"boom","timestamp":3}`,
		`{"role":"assistant","content":[],"api":"a","provider":"p","model":"m","usage":{"input":0,"output":0,"cacheRead":0,"cacheWrite":0,"totalTokens":0,"cost":{"input":0,"output":0,"cacheRead":0,"cacheWrite":0,"total":0}},"stopReason":"aborted","timestamp":3,"errorMessage":"Request was aborted"}`,
		// openai-completions.ts:564-586 creates responseId, responseModel, rawStopReason and errorMessage in this order.
		`{"role":"assistant","content":[],"api":"openai-completions","provider":"p","model":"m","usage":{"input":0,"output":0,"cacheRead":0,"cacheWrite":0,"totalTokens":0,"cost":{"input":0,"output":0,"cacheRead":0,"cacheWrite":0,"total":0}},"stopReason":"error","timestamp":3,"responseId":"r","responseModel":"m-1","rawStopReason":"content_filter","errorMessage":"Provider finish_reason: content_filter"}`,
		// types.ts Usage.cost.totalFromProvider follows total: false from calculateCost, true from OpenRouter's reported cost.
		`{"role":"assistant","content":[],"api":"openai-completions","provider":"openrouter","model":"m","usage":{"input":1,"output":2,"cacheRead":0,"cacheWrite":0,"totalTokens":3,"cost":{"input":0,"output":0,"cacheRead":0,"cacheWrite":0,"total":0.004,"totalFromProvider":true}},"stopReason":"stop","timestamp":3}`,
		`{"role":"assistant","content":[],"api":"openai-completions","provider":"p","model":"m","usage":{"input":1,"output":2,"cacheRead":0,"cacheWrite":0,"totalTokens":3,"cost":{"input":0,"output":0,"cacheRead":0,"cacheWrite":0,"total":0,"totalFromProvider":false}},"stopReason":"stop","timestamp":3}`,
		`{"role":"toolResult","toolCallId":"c1","toolName":"read","content":[{"type":"text","text":"ok"}],"details":{"z":1,"a":[2]},"isError":false,"timestamp":4}`,
		`{"role":"bashExecution","command":"ls","output":"a\nb","exitCode":0,"cancelled":false,"truncated":false,"timestamp":5}`,
		`{"role":"custom","customType":"note","content":[{"type":"text","text":"n"}],"display":true,"details":{"b":1,"a":2},"timestamp":6}`,
	} {
		var message AgentMessage
		if _, err := message.decode([]byte(line)); err != nil {
			t.Fatal(err)
		}
		if !jsonstringify.IsCanonical([]byte(line)) || !message.encodesAs([]byte(line)) {
			t.Errorf("scan refused a message Pi writes: %s", line)
		}
	}
}
