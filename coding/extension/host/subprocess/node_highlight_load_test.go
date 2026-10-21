//go:build !pig_strip_node_extensions

package subprocess

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"slices"
	"testing"
)

// pig additive (D20): Pi loads every highlight.js grammar in one synchronous pass in a process that serves nothing else. A Node member serves the host's requests on the same thread under the renderer inactivity lease (D56), and that pass took more than the five seconds of the lease on a starved machine (125 ms on an idle one), so a Markdown transform, a render or a mouse event queued behind it was abandoned while the extension was healthy. The load must give the event loop back between grammar modules, and the languages must still become available together, as the one registration pass of Pi's lib/index.js makes them.
func TestNodeLoadsHighlightLanguagesWithoutHoldingTheEventLoop(t *testing.T) {
	root := filepath.Join(findModuleRoot(t), "coding", "extension", "host", "subprocess", "runtime-node")
	script := `import { registerHooks } from "node:module";
import { pathToFileURL } from "node:url";
const root = pathToFileURL(process.argv[1] + "/");
let modules = 0;
registerHooks({load(url, context, next) {
  if (/\/highlight\.js\/lib\/languages\//.test(url)) modules++;
  return next(url, context);
}});
const hljs = (await import(new URL("shims/highlight.js/lib/core.js", root))).default;
const { loadAllHighlightLanguages, supportsLanguage } = await import(new URL("shims/syntax-highlight.mjs", root));
if (process.argv[2] === "pi") {
  const pi = await import(new URL("shims/pi-dist/pi-coding-agent/utils/syntax-highlight.js", root));
  await pi.loadAllHighlightLanguages();
  console.log(JSON.stringify({ languages: hljs.listLanguages() }));
  process.exit(0);
}
supportsLanguage("javascript");
const eager = hljs.listLanguages().length;
const sizes = new Set();
let turns = 0, running = 0, longest = 0, done = false;
modules = 0;
const tick = () => {
  longest = Math.max(longest, modules - running);
  running = modules;
  sizes.add(hljs.listLanguages().length);
  turns++;
  if (!done) setImmediate(tick);
};
setImmediate(tick);
const unavailable = supportsLanguage("ada");
await loadAllHighlightLanguages();
done = true;
longest = Math.max(longest, modules - running);
console.log(JSON.stringify({ eager, sizes: [...sizes], turns, longest, modules, unavailable, available: supportsLanguage("ada"), languages: hljs.listLanguages() }));
`
	run := func(args ...string) (out struct {
		Eager       int      `json:"eager"`
		Sizes       []int    `json:"sizes"`
		Turns       int      `json:"turns"`
		Longest     int      `json:"longest"`
		Modules     int      `json:"modules"`
		Unavailable bool     `json:"unavailable"`
		Available   bool     `json:"available"`
		Languages   []string `json:"languages"`
	}) {
		t.Helper()
		cmd := exec.CommandContext(t.Context(), "node", append([]string{"--import", registerLoaderURL(t, root), "--input-type=module", "--eval", script, root}, args...)...)
		output, err := cmd.Output()
		if err != nil {
			t.Fatalf("highlight load %v: %v\n%s", args, err, output)
		}
		if err := json.Unmarshal(output, &out); err != nil {
			t.Fatalf("highlight load output: %v\n%s", err, output)
		}
		return out
	}
	pi := run("pi")
	got := run()
	if got.Unavailable || !got.Available {
		t.Fatalf("ada before the load = %v, after = %v; want false, true", got.Unavailable, got.Available)
	}
	// The registration is one pass: no event-loop turn sees some of the languages and not the rest.
	for _, size := range got.Sizes {
		if size != got.Eager && size != len(pi.Languages) {
			t.Fatalf("a turn saw %d languages; want %d before the load or %d after it", size, got.Eager, len(pi.Languages))
		}
	}
	if !slices.Equal(got.Languages, pi.Languages) {
		t.Fatalf("languages after the load differ from Pi's own load:\n got %v\nwant %v", got.Languages, pi.Languages)
	}
	if got.Modules < 150 {
		t.Fatalf("only %d grammar modules loaded under the hook; the load did not run", got.Modules)
	}
	// Pi's single pass loads every grammar without a turn between two of them.
	if got.Longest > 4 {
		t.Fatalf("event loop held: %d grammar modules loaded in one turn at most, %d turns for %d modules", got.Longest, got.Turns, got.Modules)
	}
}

// The yielding load requires each grammar itself, so its list must be the one lib/index.js registers, in its order: a grammar left out would be loaded inside the single pass again, and one added would register nothing.
func TestNodeHighlightLanguageListMatchesIndex(t *testing.T) {
	root := filepath.Join(findModuleRoot(t), "coding", "extension", "host", "subprocess", "runtime-node", "shims")
	names := func(file string, pattern string) []string {
		t.Helper()
		data, err := os.ReadFile(filepath.Join(root, file))
		if err != nil {
			t.Fatal(err)
		}
		var out []string
		for _, match := range regexp.MustCompile(pattern).FindAllSubmatch(data, -1) {
			out = append(out, string(match[1]))
		}
		return out
	}
	index := names("highlight.js/lib/index.js", `require\('\./languages/([^']+)'\)`)
	list := names("syntax-highlight.mjs", `import\("\./highlight\.js/lib/languages/([^"]+)\.js"\)`)
	if len(index) == 0 || !slices.Equal(index, list) {
		t.Fatalf("syntax-highlight.mjs lists %d grammars, lib/index.js registers %d:\n list %v\nindex %v", len(list), len(index), list, index)
	}
}
