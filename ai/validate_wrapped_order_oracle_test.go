package ai

import (
	"bytes"
	"encoding/json"
	"os/exec"
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/internal/testenv"
)

// TypeBox's Convert wraps a value that is not an array in one when the schema asks for an array: {"edits": {...}} becomes {"edits": [{...}]}. The wrapped object keeps the member order of the arguments' text in Pi (JSON.parse's enumeration order); the arguments Pig returns and the order of the
// errors it lists must follow it, not the byte-sorted order of an object whose position Pig no longer knows. The oracle comparison of TestValidateToolArgumentsMatchesPiOnSeededSchemas decodes both results, so it cannot see member order: this test compares text.
func TestValidateToolArgumentsKeepsTheSourceOrderOfWrappedObjectsLikePi(t *testing.T) {
	num := `{"~kind":"Integer","type":"integer"}`
	str := `{"~kind":"String","type":"string"}`
	object := func(properties, extra string) string {
		return `{"~kind":"Object","type":"object","properties":{` + properties + `}` + extra + `}`
	}
	array := func(items string) string { return `{"~kind":"Array","type":"array","items":` + items + `}` }
	root := func(properties string) string { return object(properties, "") }
	edit := object(`"oldText":`+str+`,"newText":`+str+`,"n":`+num+`,"inner":`+object(`"z":`+num+`,"a":`+num, "")+``, "")
	probes := []struct {
		Schema string `json:"schema"`
		Args   string `json:"args"`
	}{
		{root(`"edits":` + array(edit) + `,"path":` + str), `{"path":"a","edits":{"newText":"N","n":"5","oldText":"O"}}`},
		{root(`"edits":` + array(edit) + `,"path":` + str), `{"edits":{"newText":"N","oldText":"O","zz":1,"aa":2},"path":"a"}`},
		{root(`"edits":` + array(edit)), `{"edits":{"oldText":"O","inner":{"z":1,"a":2},"newText":"N"}}`},
		{root(`"m":` + array(array(object(`"b":`+str+`,"a":`+str, "")))), `{"m":{"b":"x","a":"y"}}`},
		{root(`"m":` + array(array(object(`"b":`+str+`,"a":`+str, "")))), `{"m":[{"b":"x","a":"y"},{"d":"p","c":"q"}]}`},
		{root(`"m":` + array(object(`"10":`+str+`,"2":`+str+`,"b":`+str+`,"a":`+str, ""))), `{"m":{"b":"1","10":"2","a":"3","2":"4"}}`},
		{root(`"m":` + array(object(`"0":`+object(`"z":`+num+`,"a":`+num, "")+`,"n":`+num, ""))), `{"m":{"n":1,"0":{"z":1,"a":2}}}`},
		{root(`"m":` + array(object("", `,"additionalProperties":`+num))), `{"m":{"z":"a","y":"b","x":"c","w":"d","v":"e","u":"f","t":"g","s":"h","r":"i","q":"j"}}`},
		{root(`"m":` + array(object(`"k":`+num, `,"additionalProperties":`+num))), `{"m":{"k":"x","z":"a","y":"b"}}`},
		{root(`"m":` + array(array(object("", `,"additionalProperties":`+num)))), `{"m":{"z":"a","y":"b","x":"c"}}`},
	}
	payload, err := json.Marshal(probes)
	if err != nil {
		t.Fatal(err)
	}
	cmd := exec.CommandContext(t.Context(), "node", "--disable-warning=ExperimentalWarning", "testdata/validate_tool_args.mjs", testenv.ModuleRoot(t), pinnedTypeboxVersion)
	cmd.Stdin = bytes.NewReader(payload)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	output, err := cmd.Output()
	if err != nil {
		t.Fatalf("Pi oracle (npm ci --ignore-scripts in internal/typebox/testdata/npm first): %v\n%s", err, &stderr)
	}
	var expected []validateResult
	if err := json.Unmarshal(output, &expected); err != nil {
		t.Fatal(err)
	}
	for i, probe := range probes {
		got, err := ValidateToolArgumentsJSON("probe", json.RawMessage(probe.Schema), json.RawMessage(probe.Args))
		have, want := string(got), strings.TrimSpace(string(expected[i].Ok))
		if err != nil {
			have = "ERR " + err.Error()
		}
		if expected[i].Err != nil {
			want = "ERR " + *expected[i].Err
		}
		if have != want {
			t.Errorf("probe %d: arguments %s\n  Pig %s\n  Pi  %s", i, probe.Args, have, want)
		}
	}
}
