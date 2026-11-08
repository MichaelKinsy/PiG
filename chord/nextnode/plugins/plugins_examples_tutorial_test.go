package plugins

// pi: packages/chord/test/examples/61-tutorial/plugins/lists/worker.test.ts
// pi: packages/chord/test/examples/61-tutorial/plugins/traces/clock.test.ts

import (
	"context"
	"encoding/json"
	"errors"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/MichaelKinsy/PiG/chord/delta"
	"github.com/MichaelKinsy/PiG/chord/next/contracts"
	"github.com/MichaelKinsy/PiG/chord/next/data"
	"github.com/MichaelKinsy/PiG/chord/next/host"
	"github.com/MichaelKinsy/PiG/chord/next/surfaces"
)

// tutorialModules writes the tutorial app's API v1 (`@todo/app/v1`) as src/api/v1/index.ts has it for a test: Chord's surface
// re-exported (the part plugins use), and the app's host kind, `List`. `defineFacet` is Chord's: the app's only adds types.
func tutorialModules(t *testing.T, cache string) HostPackages {
	t.Helper()
	tree, err := extractNode(cache)
	if err != nil {
		t.Fatal(err)
	}
	src := "file://" + filepath.ToSlash(filepath.Join(tree, "pi", "chord", "src")) + "/index.ts"
	dir := t.TempDir()
	writeFiles(t, dir, map[string]string{
		"v1.ts": `import { hostKind, str } from "` + src + `";` + "\n" +
			`export * from "` + src + `";` + "\n" +
			`export const List = hostKind("list", { id: str() });` + "\n",
	})
	return HostPackages{"@todo/app/v1": filepath.Join(dir, "v1.ts")}
}

// tutorialItem is a todo, `Item` of the lists plugin's contract.ts.
var tutorialItem = contracts.Obj(
	contracts.Prop("id", contracts.Int()), contracts.Prop("text", contracts.Str()), contracts.Prop("done", contracts.Bool()),
	contracts.Prop("by", contracts.Str()), contracts.Prop("at", contracts.Num()),
)

// tutorialTodos is `Todos` of the lists plugin's contract.ts, declared again with Go's builders (contracts are data: the same
// name, version and schema bind), served on a list's worker and addressed to it.
func tutorialTodos(list *contracts.HostKind) *contracts.Contract {
	notice := contracts.Union([]contracts.Schema{surfaces.MessageSchema, contracts.Nul()})
	state := []contracts.Property{contracts.Prop("items", contracts.Arr(tutorialItem)), contracts.Prop("notice", notice)}
	add := contracts.Meth("add", contracts.NewMethod(contracts.Str()))
	tick := contracts.Meth("tick", contracts.NewMethod(contracts.Obj(contracts.Prop("id", contracts.Int()), contracts.Prop("done", contracts.Bool()))))
	return mustContract(contracts.Remote("todo.todos", contracts.RemoteDeclaration{
		Version: 1,
		Params:  contracts.Params{contracts.Prop("list", contracts.Str())},
		HostAddress: func(params *delta.JsonObject) (contracts.Address, error) {
			return list.Address(delta.JsonObjectOf("id", params.Value("list"))), nil
		},
		Variants: []contracts.VariantEntry{
			contracts.Var("member", contracts.VariantDeclaration{State: state, Methods: []contracts.MethodEntry{add, tick}}),
			contracts.Var("owner", contracts.VariantDeclaration{State: state, Methods: []contracts.MethodEntry{add, tick, contracts.Meth("clear", contracts.NewMethod())}}),
		},
	}))
}

// tutorialList is a tested lists plugin on the worker of the list "groceries", with a Go host that routes the list kind to its
// link, as the app's server does: code runs on that host as a principal of its choice (as `t.root(fn, context, { principal })`
// does upstream) and reaches the plugin's objects over the link, which stamps the principal.
type tutorialList struct {
	tested *TestedPlugin
	server host.Host
	todos  *contracts.Contract
}

func tutorialListOf(t *testing.T) *tutorialList {
	t.Helper()
	cache := t.TempDir()
	root := exCopy(t, "61-tutorial", "plugins")
	list, err := contracts.NewHostKind("list", contracts.Prop("id", contracts.Str()))
	if err != nil {
		t.Fatal(err)
	}
	address := list.Address(delta.JsonObjectOf("id", "groceries"))
	worker := delta.JsonObjectOf("kind", "worker", "list", "list|groceries")
	tested := tpPlugin(t, filepath.Join(root, "plugins", "lists"), TestPluginOptions{
		Kind: "worker", Kinds: []string{"server", "worker", "web", "tui"},
		Api:     &AppApi{Name: "@todo/app", Versions: []string{"v1"}},
		Modules: tutorialModules(t, cache), Address: &address, Principal: worker, Node: NodeOptions{CacheDir: cache},
	})
	server, err := host.CreateHost(host.HostOptions{Principal: serverPrincipal, OnError: func(host.HostError) {}})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = server.Close(background) })
	var resolved atomic.Bool
	if _, err := server.Route(list, host.RouteOptions{
		Authorize: func(data.Principal, contracts.Address) (bool, error) { return true, nil },
		Resolve: func(contracts.Address, context.Context) (host.ResolvedLink, error) {
			if !resolved.CompareAndSwap(false, true) {
				return host.ResolvedLink{}, errors.New("the worker's link was attached already")
			}
			return host.ResolvedLink{Link: tested.Host.Link(), Principal: worker, Trusted: true}, nil
		},
	}, background); err != nil {
		t.Fatal(err)
	}
	if _, err := server.Load(nil, background); err != nil {
		t.Fatal(err)
	}
	return &tutorialList{tested: tested, server: server, todos: tutorialTodos(list)}
}

// root runs fn as principal, with the list's `Todos` of "groceries" taken as that principal sees it. Root runs its callback on a
// goroutine of the host, where a failed `t.Fatal` would end that goroutine and never answer Root: fn runs on a goroutine of its
// own that the callback waits for, and a failure it recorded fails the test after Root returns.
func (l *tutorialList) root(t *testing.T, principal data.Principal, fn func(todos *host.Reference, ctx host.Ctx)) {
	t.Helper()
	if _, err := l.server.Root(func(ctx host.Ctx) (any, error) {
		todos, err := ctx.Get(l.todos, delta.JsonObjectOf("list", "groceries"), host.GetOptions{})
		if err != nil {
			return nil, err
		}
		done := make(chan struct{})
		//portlint:allow golifetime ends with fn, which the callback waits for
		go func() {
			defer close(done)
			fn(todos, ctx)
		}()
		<-done
		return nil, nil
	}, background, host.RootOptions{Principal: principal}); err != nil {
		t.Fatal(err)
	}
	if t.Failed() {
		t.FailNow()
	}
}

// tutorialWait reads the state member `name` of todos until `ok` holds of its JSON, as a change crosses the link, and returns it.
func tutorialWait(t *testing.T, todos *host.Reference, name string, ok func(json string) bool) string {
	t.Helper()
	var last string
	deadline := time.Now().Add(30 * time.Second)
	for {
		value, err := todos.Member(name).Value()
		if err == nil {
			last = tpJSON(t, value)
			if ok(last) {
				return last
			}
		}
		if time.Now().After(deadline) {
			t.Fatalf("%s = %s (error %v)", name, last, err)
		}
		time.Sleep(10 * time.Millisecond)
	}
}

// tutorialItems are the items' `text (by)` or texts, from the state's JSON.
func tutorialItems(t *testing.T, items string, show func(text, by string) string) []string {
	t.Helper()
	var rows []struct{ Text, By string }
	if err := json.Unmarshal([]byte(items), &rows); err != nil {
		t.Fatal(err)
	}
	shown := []string{}
	for _, row := range rows {
		shown = append(shown, show(row.Text, row.By))
	}
	return shown
}

// Ports lists/worker.test.ts "the lists plugin on a list's worker": the plugin built as production builds it, evaluated with the
// app's API module and loaded on a host of its own (an in-memory store) at a list's address; each case runs code as a principal
// of its choice: a user gets the `member` variant, the owner `owner`.
func TestTutorialListsPluginOnAListsWorker(t *testing.T) {
	ada := delta.JsonObjectOf("kind", "user", "id", "u1", "name", "ada")
	owner := delta.JsonObjectOf("kind", "owner")

	t.Run("adds a todo as the principal that called", func(t *testing.T) {
		l := tutorialListOf(t)
		l.root(t, ada, func(todos *host.Reference, ctx host.Ctx) {
			if variant, _, err := todos.Variant(); err != nil || variant != "member" {
				t.Fatalf("variant = %q, %v; want member", variant, err)
			}
			if _, err := todos.Call("add", "coffee", ctx); err != nil {
				t.Fatal(err)
			}
			items := tutorialWait(t, todos, "items", func(items string) bool { return strings.Contains(items, "coffee") })
			shown := tutorialItems(t, items, func(text, by string) string { return text + " (" + by + ")" })
			if want := []string{"coffee (ada)"}; !reflect.DeepEqual(shown, want) {
				t.Fatalf("items = %v, want %v", shown, want)
			}
		})
	})

	t.Run("refuses a todo without text with invalid/args", func(t *testing.T) {
		l := tutorialListOf(t)
		l.root(t, ada, func(todos *host.Reference, ctx host.Ctx) {
			_, err := todos.Call("add", "", ctx)
			chordErr, ok := data.IsChordErrorCode(err, data.CodeInvalid)
			if !ok || chordErr.Reason != data.ReasonArgs {
				t.Fatalf("refused = %v, want invalid/args", err)
			}
		})
	})

	t.Run("lets the owner clear the done todos", func(t *testing.T) {
		l := tutorialListOf(t)
		l.root(t, owner, func(todos *host.Reference, ctx host.Ctx) {
			if variant, _, err := todos.Variant(); err != nil || variant != "owner" {
				t.Fatalf("the owner got %q, %v", variant, err)
			}
			for _, text := range []string{"milk", "eggs"} {
				if _, err := todos.Call("add", text, ctx); err != nil {
					t.Fatal(err)
				}
			}
			if _, err := todos.Call("tick", delta.JsonObjectOf("id", float64(1), "done", true), ctx); err != nil {
				t.Fatal(err)
			}
			if _, err := todos.Call("clear", nil, ctx); err != nil {
				t.Fatal(err)
			}
			tutorialWait(t, todos, "items", func(items string) bool {
				return reflect.DeepEqual(tutorialItems(t, items, func(text, _ string) string { return text }), []string{"eggs"})
			})
			notice := tutorialWait(t, todos, "notice", func(notice string) bool { return notice != "null" })
			if want := `{"namespace":"todo.lists","key":"cleared","params":["owner"]}`; notice != want {
				t.Fatalf("notice = %s, want %s", notice, want)
			}
		})
		unserved, err := l.tested.Unserved(background)
		if err != nil || len(unserved) != 0 {
			t.Fatalf("unserved = %v, %v; want none", unserved, err)
		}
	})
}

// Ports traces/clock.test.ts "clamped": the viewer's clock correction, a pure function of the example's browser code
// (plugins/traces/clock.ts, unchanged), run on Node as the tutorial's web page runs it. One Node run answers every case's
// result as JSON; each case is Pi's, its inputs and expectation too, checked under Pi's title.
func TestTutorialTracesClockClamped(t *testing.T) {
	node, err := exec.LookPath("node")
	if err != nil {
		t.Fatal(err)
	}
	clock, err := filepath.Abs(filepath.Join("testdata", "examples", "61-tutorial", "plugins", "traces", "clock.ts"))
	if err != nil {
		t.Fatal(err)
	}
	// span and times are the test's helpers.
	script := `
import { clamped } from ` + pcJSON(t, fileURL(clock)) + `;
function span(id, host, startedMs, endedMs, parentId) {
	return { id, traceId: "t", ...(parentId === undefined ? {} : { parentId }), name: id, startedMs, endedMs,
		attributes: { "chord.host": host }, events: [], links: [], status: { status: "ok" } };
}
const times = (spans) => clamped(spans).map(({ id, startedMs, endedMs }) => [id, startedMs, endedMs]);
const late = [span("click", "browser", 5000, 5010), span("call", "browser", 5001, 5009, "click")];
late.push(span("relay", "server", 2002, 2008, "call"));
const early = [span("call", "browser", 100, 110), span("relay", "server", 1101, 1105, "call")];
const fitting = [span("call", "browser", 100, 110), span("relay", "server", 101, 109, "call")];
const alone = [span("click", "browser", 5000, 5010), span("other", "server", 1, 2)];
const longer = [span("call", "browser", 100, 102), span("relay", "server", 200, 210, "call")];
console.log(JSON.stringify({ late: times(late), early: times(early), fitting: clamped(fitting) === fitting,
	alone: clamped(alone) === alone, longer: times(longer)[0] }));
`
	cmd := exec.Command(node, "--experimental-strip-types", "--no-warnings", "--input-type=module", "--eval", script)
	cmd.Dir = t.TempDir()
	output, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("node: %v\n%s", err, output)
	}
	var got map[string]json.RawMessage
	if err := json.Unmarshal(output, &got); err != nil {
		t.Fatalf("node answered %q: %v", output, err)
	}
	check := func(t *testing.T, key, want string) {
		t.Helper()
		if string(got[key]) != want {
			t.Fatalf("%s = %s, want %s", key, got[key], want)
		}
	}
	t.Run("moves a page that runs late back, so its call contains the server's relay", func(t *testing.T) {
		check(t, "late", `[["click",2001,2011],["call",2002,2010],["relay",2002,2008]]`)
	})
	t.Run("moves a page that runs early forward by the least amount", func(t *testing.T) {
		check(t, "early", `[["call",1095,1105],["relay",1101,1105]]`)
	})
	t.Run("moves nothing when the server's spans fit, or when no server span is under a page span", func(t *testing.T) {
		check(t, "fitting", "true")
		check(t, "alone", "true")
	})
	t.Run("centres a server child longer than its page parent", func(t *testing.T) {
		check(t, "longer", `["call",204,206]`)
	})
}
