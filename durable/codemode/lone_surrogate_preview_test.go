package codemode

import (
	"bytes"
	"os/exec"
	"strings"
	"testing"

	sdkjson "github.com/MichaelKinsy/PiG/extensions/sdk/json"
	"github.com/MichaelKinsy/PiG/internal/jsstring"
)

// codemode.ts cut(): a preview of the arguments is cut to 200 UTF-16 units, and a cut inside a surrogate pair leaves a lone
// surrogate that JSON.stringify writes as the escape `\ud83d`. The details of the result keep it from the call to the
// stored result and to what a reader gets, so a UI, a session file or JSON output sees what Pi's would. The oracle is Node's
// own `JSON.stringify(text.slice(0, 197) + "...")` of the compact arguments.
func TestAPreviewCutInsideASurrogatePairKeepsItsEscapeLikePi(t *testing.T) {
	chat := codemodeChat(t)
	// `{"text":"a` is 10 units, so 197 units end inside the 94th emoji.
	text := "a" + strings.Repeat("😀", 100)
	run := runScript(t, `await tools.echo({ text: "`+text+`" });`, chat)
	if len(run.details.Calls) != 1 {
		t.Fatalf("calls %+v", run.details.Calls)
	}
	got, err := jsstring.MarshalJSON(run.details.Calls[0].Args)
	if err != nil {
		t.Fatal(err)
	}
	compact, err := sdkjson.MarshalStringify(map[string]string{"text": text})
	if err != nil {
		t.Fatal(err)
	}
	cmd := exec.CommandContext(t.Context(), "node", "-e", `process.stdout.write(JSON.stringify(process.argv[1].slice(0, 197) + "..."))`, string(compact))
	want, err := cmd.Output()
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(want), `\ud83d`) {
		t.Fatalf("the case does not cut inside a pair: %s", want)
	}
	if !bytes.Equal(got, want) {
		t.Errorf("the preview in the details is %s, Pi's is %s", got, want)
	}
}
