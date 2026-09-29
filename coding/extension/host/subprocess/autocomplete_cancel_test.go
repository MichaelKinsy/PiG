package subprocess

import (
	"fmt"
	"os/exec"
	"path/filepath"
	"testing"
)

// Pi's cancelled attachment queries return no suggestions. An editor replacing its provider must not turn that cancellation into an uncaught rejection, while real callback failures still reject.
func TestNodeAutocompleteProxyDistinguishesCancellationFromFailure(t *testing.T) {
	nodeCellRequireNode(t)
	path, err := filepath.Abs("runtime-node/autocomplete.mjs")
	if err != nil {
		t.Fatal(err)
	}
	script := fmt.Sprintf(`
import assert from "node:assert/strict";
import {pathToFileURL} from "node:url";
const {AutocompleteRuntime}=await import(pathToFileURL(%q));
let reject, requests=0, cancellations=0;
const runtime={call(){requests++;return new Promise((_,no)=>{reject=no})},fireAndForget(method){assert.equal(method,"ui.autocomplete.cancel");cancellations++;reject(new Error("context canceled"))}};
const provider=new AutocompleteRuntime(runtime).current({id:"current",triggerCharacters:[],hasFileTrigger:false});
const abort=new AbortController();
const pending=provider.getSuggestions(["@file"],0,5,{signal:abort.signal});
abort.abort();
assert.equal(await pending,null);
assert.equal(cancellations,1);
assert.equal(await provider.getSuggestions(["@file"],0,5,{signal:abort.signal}),null);
assert.equal(requests,1,"pre-cancelled query must not start work");
const failed=provider.getSuggestions(["@file"],0,5,{signal:new AbortController().signal});
reject(new Error("real failure"));
await assert.rejects(failed,/real failure/);
`, path)
	if out, err := exec.CommandContext(t.Context(), "node", "--input-type=module", "-e", script).CombinedOutput(); err != nil {
		t.Fatalf("autocomplete cancellation: %v\n%s", err, out)
	}
}
