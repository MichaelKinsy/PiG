package coding

import (
	"encoding/json"
	"reflect"
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/ai"
	"github.com/MichaelKinsy/PiG/coding/extension"
)

// sameModelObjectByTree is the comparison sameModelObject made before it decoded into a concrete type: both texts decoded into a tree of interface
// values and compared with reflect.DeepEqual. The tests below hold the function to it row by row.
func sameModelObjectByTree(left, right []byte) bool {
	var a, b any
	if json.Unmarshal(left, &a) != nil || json.Unmarshal(right, &b) != nil {
		return false
	}
	return reflect.DeepEqual(a, b)
}

// modelObjectShapes are model objects of several provider shapes, as Pi's composer writes them (provider-composer.ts:230-248) with the members each
// API adds.
var modelObjectShapes = []struct{ name, object string }{
	{"anthropic-messages", `{"id":"claude-shape","name":"Claude Shape","api":"anthropic-messages","provider":"anthropic","baseUrl":"https://api.anthropic.com","reasoning":true,"input":["text","image"],"cost":{"input":3,"output":15,"cacheRead":0.3,"cacheWrite":3.75},"contextWindow":200000,"maxTokens":64000}`},
	{"openai-completions", `{"id":"gpt-shape","name":"GPT Shape","api":"openai-completions","provider":"acme","baseUrl":"https://acme.invalid/v1","reasoning":false,"input":["text"],"cost":{"input":0,"output":0,"cacheRead":0,"cacheWrite":0},"contextWindow":128000,"maxTokens":16384,"compat":{"maxTokensField":"max_tokens","supportsDeveloperRole":false}}`},
	{"openai-responses", `{"id":"o-shape","name":"O Shape","api":"openai-responses","provider":"openai","baseUrl":"https://api.openai.com/v1","reasoning":true,"input":["text","image"],"cost":{"input":1.25,"output":10,"cacheRead":0.125,"cacheWrite":0},"contextWindow":400000,"maxTokens":128000}`},
	{"google-generative-ai", `{"id":"gemini-shape","name":"Gemini Shape","api":"google-generative-ai","provider":"google","baseUrl":"https://generativelanguage.googleapis.com/v1beta","reasoning":true,"input":["text","image"],"cost":{"input":0.3,"output":2.5,"cacheRead":0.075,"cacheWrite":0},"contextWindow":1048576,"maxTokens":65536}`},
	{"mistral-conversations", `{"id":"mistral-shape","name":"Mistral Shape","api":"mistral-conversations","provider":"mistral","baseUrl":"https://api.mistral.ai","reasoning":false,"input":["text"],"cost":{"input":2,"output":6,"cacheRead":0,"cacheWrite":0},"contextWindow":131072,"maxTokens":8192,"headers":{"x-shape":"1"}}`},
	{"bedrock-converse-stream", `{"id":"bedrock.shape-v1:0","name":"Bedrock Shape","api":"bedrock-converse-stream","provider":"amazon-bedrock","baseUrl":"https://bedrock-runtime.us-east-1.amazonaws.com","reasoning":true,"input":["text","image"],"cost":{"input":3,"output":15,"cacheRead":0.3,"cacheWrite":3.75},"contextWindow":200000,"maxTokens":8192}`},
}

// withMember returns object with one more member at its end.
func withMember(object, member string) string {
	return strings.TrimSuffix(object, "}") + "," + member + "}"
}

// reversedMembers returns object with its top-level members in the opposite order.
func reversedMembers(t *testing.T, object string) string {
	t.Helper()
	decoder := json.NewDecoder(strings.NewReader(object))
	if _, err := decoder.Token(); err != nil {
		t.Fatal(err)
	}
	var members []string
	for decoder.More() {
		name, err := decoder.Token()
		if err != nil {
			t.Fatal(err)
		}
		var value json.RawMessage
		if err := decoder.Decode(&value); err != nil {
			t.Fatal(err)
		}
		quoted, err := json.Marshal(name)
		if err != nil {
			t.Fatal(err)
		}
		members = append([]string{string(quoted) + ":" + string(value)}, members...)
	}
	return "{" + strings.Join(members, ",") + "}"
}

// TestSameModelObjectJSONEqualsTheTreeComparison holds the concrete decode to the comparison it replaced over every provider shape and over the
// forms in which two texts of one value differ: member order, a number written as an integer or a float, an escape, a repeated name; and the forms
// in which two values differ: an unknown extra member, null against absent, an array's order, a type.
func TestSameModelObjectJSONEqualsTheTreeComparison(t *testing.T) {
	type row struct {
		name, left, right string
		want              bool
	}
	var rows []row
	for i, shape := range modelObjectShapes {
		object := shape.object
		other := modelObjectShapes[(i+1)%len(modelObjectShapes)].object
		window := object[strings.Index(object, `"contextWindow":`)+len(`"contextWindow":`) : strings.Index(object, `,"maxTokens"`)]
		rows = append(rows,
			row{shape.name + "/itself", object, object, true},
			row{shape.name + "/members reversed", object, reversedMembers(t, object), true},
			row{shape.name + "/another shape", object, other, false},
			row{shape.name + "/unknown extra member on one side", object, withMember(object, `"x-vendor":{"tier":"gold","limits":[1,2]}`), false},
			row{shape.name + "/the same unknown extra member", withMember(object, `"x-vendor":{"tier":"gold","limits":[1,2]}`), withMember(reversedMembers(t, object), `"x-vendor":{"limits":[1,2],"tier":"gold"}`), true},
			row{shape.name + "/unknown extra members that differ", withMember(object, `"x-vendor":{"limits":[1,2]}`), withMember(object, `"x-vendor":{"limits":[2,1]}`), false},
			row{shape.name + "/integer against float", object, strings.Replace(object, `"contextWindow":`+window, `"contextWindow":`+window+`.0`, 1), true},
			row{shape.name + "/integer against exponent", object, strings.Replace(object, `"contextWindow":`+window, `"contextWindow":`+window+`e0`, 1), true},
			row{shape.name + "/another number", object, strings.Replace(object, `"contextWindow":`+window, `"contextWindow":`+window+`1`, 1), false},
			row{shape.name + "/number against its string", object, strings.Replace(object, `"contextWindow":`+window, `"contextWindow":"`+window+`"`, 1), false},
			row{shape.name + "/null against absent", object, withMember(object, `"compatOverride":null`), false},
			row{shape.name + "/null against null", withMember(object, `"compatOverride":null`), withMember(object, `"compatOverride":null`), true},
			row{shape.name + "/null against an empty object", withMember(object, `"compatOverride":null`), withMember(object, `"compatOverride":{}`), false},
			row{shape.name + "/boolean flipped", object, strings.Replace(strings.Replace(strings.Replace(object, `"reasoning":true`, `"reasoning":0`, 1), `"reasoning":false`, `"reasoning":true`, 1), `"reasoning":0`, `"reasoning":false`, 1), false},
			row{shape.name + "/escaped name", object, strings.Replace(object, `"name":"`, `"name":"\u0020`, 1), false},
			row{shape.name + "/escape of the same text", strings.Replace(object, `"name":"`, `"name":" `, 1), strings.Replace(object, `"name":"`, `"name":"\u0020`, 1), true},
			row{shape.name + "/repeated name keeps the last", withMember(object, `"id":"last"`), strings.Replace(object, object[strings.Index(object, `"id":`):strings.Index(object, `,"name"`)], `"id":"last"`, 1), true},
			row{shape.name + "/number float64 cannot hold", withMember(object, `"limit":1e999`), withMember(object, `"limit":1e999`), false},
			row{shape.name + "/such a number under a repeated name", withMember(withMember(object, `"limit":1e999`), `"limit":1`), withMember(object, `"limit":1`), false},
			row{shape.name + "/not JSON", object, strings.TrimSuffix(object, "}"), false},
			row{shape.name + "/text after the object", object, object + "x", false},
		)
	}
	rows = append(rows,
		row{"input order", `{"input":["text","image"]}`, `{"input":["image","text"]}`, false},
		row{"input length", `{"input":["text"]}`, `{"input":["text","image"]}`, false},
		row{"nested member order", `{"cost":{"input":1,"output":2}}`, `{"cost":{"output":2,"input":1}}`, true},
		row{"objects in an array", `{"routes":[{"a":1,"b":2},{"c":null}]}`, `{"routes":[{"b":2,"a":1},{"c":null}]}`, true},
		row{"empty array against null", `{"input":[]}`, `{"input":null}`, false},
		row{"empty array against empty object", `{"input":[]}`, `{"input":{}}`, false},
		row{"empty arrays", `{"input":[]}`, `{"input":[ ]}`, true},
		row{"empty objects", `{"compat":{}}`, `{ "compat" : { } }`, true},
		row{"negative zero", `{"cost":-0}`, `{"cost":0}`, true},
		row{"fraction", `{"cost":0.1}`, `{"cost":1e-1}`, true},
		row{"beyond 2^53", `{"limit":9007199254740993}`, `{"limit":9007199254740992}`, true},
		row{"true against false", `{"reasoning":true}`, `{"reasoning":false}`, false},
		row{"true against its string", `{"reasoning":true}`, `{"reasoning":"true"}`, false},
		row{"escaped member name", `{"a\u0062":1}`, `{"ab":1}`, true},
		row{"lone surrogates", `{"name":"\ud800"}`, `{"name":"\udfff"}`, true},
		row{"null against null", `null`, `null`, true},
		row{"null against an object", `null`, `{}`, false},
		row{"empty text", ``, `{}`, false},
		row{"two empty texts", ``, ``, false},
	)
	for _, c := range rows {
		t.Run(c.name, func(t *testing.T) {
			for _, sides := range [][2]string{{c.left, c.right}, {c.right, c.left}} {
				got := sameModelObjectJSON(json.RawMessage(sides[0]), json.RawMessage(sides[1]))
				before := sameModelObjectByTree([]byte(sides[0]), []byte(sides[1]))
				if got != c.want || before != c.want {
					t.Errorf("same = %t, the tree comparison %t, want %t\nleft  %s\nright %s", got, before, c.want, sides[0], sides[1])
				}
			}
		})
	}
}

// TestSameModelObjectOverProviderShapes compares models decoded from each shape's object, as BindModelObject compares the object an extension passed
// with the registry's model: a model is itself and its second decode, and is neither a model with one member changed nor the next shape's model. Each
// answer is also the tree comparison's over the two model objects.
func TestSameModelObjectOverProviderShapes(t *testing.T) {
	models := make([]*ai.Model, len(modelObjectShapes))
	for i, shape := range modelObjectShapes {
		models[i] = decodeModelObject(t, []byte(shape.object))
	}
	check := func(t *testing.T, left, right *ai.Model, want bool) {
		t.Helper()
		a, err := extension.ModelObject(left)
		if err != nil {
			t.Fatal(err)
		}
		b, err := extension.ModelObject(right)
		if err != nil {
			t.Fatal(err)
		}
		if got, before := sameModelObject(left, right), sameModelObjectByTree(a, b); got != want || before != want {
			t.Errorf("same = %t, the tree comparison %t, want %t\nleft  %s\nright %s", got, before, want, a, b)
		}
	}
	for i, shape := range modelObjectShapes {
		t.Run(shape.name, func(t *testing.T) {
			model := models[i]
			check(t, model, model, true)
			check(t, model, decodeModelObject(t, []byte(shape.object)), true)
			check(t, model, decodeModelObject(t, []byte(reversedMembers(t, shape.object))), true)
			check(t, model, editedModelObject(t, model, func(map[string]any) {}), true)
			check(t, model, editedModelObject(t, model, func(object map[string]any) { object["contextWindow"] = 4242 }), false)
			check(t, model, editedModelObject(t, model, func(object map[string]any) { object["name"] = "Edited" }), false)
			check(t, model, models[(i+1)%len(models)], false)
			check(t, models[(i+1)%len(models)], model, false)
		})
	}
	if sameModelObject(nil, models[0]) || sameModelObject(models[0], nil) || sameModelObject(nil, nil) {
		t.Error("a nil model is the same model object as another")
	}
}
