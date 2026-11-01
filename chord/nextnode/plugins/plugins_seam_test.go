package plugins

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"
	"weak"

	"github.com/MichaelKinsy/PiG/chord"
	"github.com/MichaelKinsy/PiG/chord/delta"
	"github.com/MichaelKinsy/PiG/chord/next/contracts"
	"github.com/MichaelKinsy/PiG/chord/next/host"
	"github.com/MichaelKinsy/PiG/chord/next/state"
)

// The reference Builder and Evaluators of the tests: the seams of plugins_build.go and plugins_evaluate.go in their simplest
// form. Upstream builds with esbuild and evaluates with `vm.Script`, which Go cannot (AGENTS.md: no embedded JavaScript
// runtime); these read a facet file written in the small language below instead of TypeScript, and the evaluator makes the
// facet that text means, so the component tests keep upstream's plugin layouts, edits and expectations.
//
// A facet file is lines; `import <path>` lines name a file of the plugin (`./label.ts`) or of another discovered plugin by
// name (`@test/contracts/contract.ts`, a copy), and the first other line is the facet:
//
//	item <label>               serves `test.<plugin directory>` with the member `label` constant
//	provider <n>               serves `test.provider` version n with the member `n` constant
//	consumer <n>               serves `test.consumer` whose `label` is "v<n> " plus the `n` of `test.provider` version n
//	gc <label>                 serves `test.gc` with `label`, its closure holding the evaluation's big state (probe.made)
//	failed-gc <bad>            serves `test.failed-gc`; its setup throws "bad setup" when bad is `true`
//	empty                      a facet that declares nothing
//	throws <message>           evaluation throws message
//	setup-throws <message>     the facet's setup returns message
//	syntax error ...           the build fails
//
// Anything else builds and fails its evaluation: its default export is not a facet.

// chordWatchable is a JSON watchable of an implementation's state.
type chordWatchable = chord.Watchable[delta.JsonValue]

func sha256Hex(bytes []byte) string {
	sum := sha256.Sum256(bytes)
	return hex.EncodeToString(sum[:])
}

// hasSyntaxError reports whether a file of the language has a `syntax error` line.
func hasSyntaxError(text string) bool {
	return slices.ContainsFunc(strings.Split(text, "\n"), func(line string) bool { return strings.HasPrefix(line, "syntax error") })
}

// referenceBuilder builds a facet file as a script of its own text and the files it imports, digested and hashed as
// `buildFacet` does (sha256, hex), relative to the plugins so the digest is the same on every checkout.
type referenceBuilder struct {
	mu sync.Mutex
	// built counts the builds, by "plugin\x00kind".
	built map[string]int
}

var _ Builder = (*referenceBuilder)(nil)

// builds is how many times plugin's facet of kind was built.
func (b *referenceBuilder) builds(plugin, kind string) int {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.built[plugin+"\x00"+kind]
}

// Build implements Builder.
func (b *referenceBuilder) Build(plugin Plugin, kind string, options BuildOptions, ctx context.Context) (BuiltFacet, error) {
	b.mu.Lock()
	if b.built == nil {
		b.built = map[string]int{}
	}
	b.built[plugin.Name+"\x00"+kind]++
	b.mu.Unlock()
	failed := func(errors ...string) (BuiltFacet, error) {
		return BuiltFacet{Kind: BuiltFacetFailed, Plugin: plugin.Name, Facet: kind, Errors: errors}, nil
	}
	entry := plugin.Facets[kind]
	text, err := os.ReadFile(entry)
	if err != nil {
		return BuiltFacet{}, err
	}
	if hasSyntaxError(string(text)) {
		return failed(fmt.Sprintf("%s:1:1: Expected %q", filepath.Base(entry), "}"))
	}
	source := strings.Builder{}
	source.Write(text)
	inputs := map[string]string{entry: sha256Hex(text)}
	copied := []string{}
	var parts []string
	for line := range strings.SplitSeq(string(text), "\n") {
		spec, ok := strings.CutPrefix(line, "import ")
		if !ok {
			continue
		}
		var file, name string
		if strings.HasPrefix(spec, ".") {
			file, name = filepath.Join(plugin.Dir, spec), spec
		} else {
			index := slices.IndexFunc(options.Plugins, func(each Plugin) bool { return strings.HasPrefix(spec, each.Name+"/") })
			if index < 0 {
				return failed(filepath.Base(entry) + `: "` + spec + `": neither a host package, in the dependencies of plugin "` + plugin.Name + `" nor a discovered plugin`)
			}
			other := options.Plugins[index]
			file, name = filepath.Join(other.Dir, strings.TrimPrefix(spec, other.Name+"/")), spec
			copied = append(copied, file)
		}
		content, err := os.ReadFile(file)
		if err != nil {
			return failed(filepath.Base(entry) + `: "` + spec + `": ` + err.Error())
		}
		if hasSyntaxError(string(content)) {
			return failed(fmt.Sprintf("%s:1:1: Expected %q", name, "}"))
		}
		inputs[file] = sha256Hex(content)
		parts = append(parts, "\n--- "+name+"\n"+string(content))
	}
	slices.Sort(parts)
	for _, part := range parts {
		source.WriteString(part)
	}
	files := make([]string, 0, len(inputs))
	for file := range inputs {
		files = append(files, file)
	}
	slices.Sort(files)
	slices.Sort(copied)
	built := BuiltFacet{Kind: BuiltFacetScript, Plugin: plugin.Name, Facet: kind, Source: source.String(), Digest: sha256Hex([]byte(source.String())), Copied: copied, Assets: []Asset{}}
	built.Inputs = make([]BuildInput, len(files))
	for i, file := range files {
		built.Inputs[i] = BuildInput{File: file, Hash: inputs[file]}
	}
	return built, nil
}

// referenceEvaluator evaluates a script to the facet registered for its filename (ScriptFilename): what a runtime that
// evaluated the script would have produced. Anything else is the evaluator's error, as a default export that is no facet is.
type referenceEvaluator struct {
	facets map[string]host.Facet
}

var _ Evaluator = referenceEvaluator{}

// EvaluateFacet implements Evaluator.
func (e referenceEvaluator) EvaluateFacet(built BuiltFacet, options EvaluateOptions) (host.Facet, error) {
	where := fmt.Sprintf("plugin %q (%s)", options.Plugin.Name, built.Facet)
	if built.Kind != BuiltFacetScript {
		return host.Facet{}, fmt.Errorf("%s: only a built script can be evaluated", where)
	}
	facet, ok := e.facets[ScriptFilename(built, options.Plugin)]
	if !ok || facet == (host.Facet{}) {
		return host.Facet{}, fmt.Errorf("%s: its default export is not a facet (defineFacet)", where)
	}
	return facet, nil
}

// probe is what the evaluated facets of the `gc` and `failed-gc` lines report to a test: the state each evaluation made,
// held weakly by the test (upstream's `@test/probe` module).
type probe struct {
	mu   sync.Mutex
	made []func() bool
}

// record keeps value weakly: alive reports whether it is still reachable.
func record[T any](p *probe, value *T) {
	weakValue := weak.Make(value)
	p.mu.Lock()
	defer p.mu.Unlock()
	p.made = append(p.made, func() bool { return weakValue.Value() != nil })
}

// alive reports, for each state made so far in order, whether it is still reachable.
func (p *probe) alive() []func() bool {
	p.mu.Lock()
	defer p.mu.Unlock()
	return append([]func() bool{}, p.made...)
}

// dslEvaluator evaluates the facet files of the language above.
type dslEvaluator struct {
	probe *probe
	// prefix is the namespace of the contracts the facets serve ("test" by default).
	prefix string
	// evaluations counts the evaluations of `clock` facets.
	evaluations *atomic.Int64
}

// named is the contract name of name in the evaluator's namespace.
func (e dslEvaluator) named(name string) string { return e.namespace() + "." + name }

// namespace is the prefix of the contracts' names.
func (e dslEvaluator) namespace() string {
	if e.prefix == "" {
		return "test"
	}
	return e.prefix
}

var _ Evaluator = dslEvaluator{}

// itemContract is `local(name, { version, params: {}, members: { label: str() } })`.
func itemContract(name string, version int) *contracts.Contract {
	contract, err := contracts.Local(name, contracts.LocalDeclaration{
		Version: float64(version), Params: contracts.Params{},
		Members: []contracts.LocalEntry{contracts.Mem("label", contracts.Str())},
	})
	if err != nil {
		panic(err)
	}
	return contract
}

// numberContract is `local(name, { version, params: {}, members: { n: int() } })`.
func numberContract(name string, version int) *contracts.Contract {
	contract, err := contracts.Local(name, contracts.LocalDeclaration{
		Version: float64(version), Params: contracts.Params{},
		Members: []contracts.LocalEntry{contracts.Mem("n", contracts.Int())},
	})
	if err != nil {
		panic(err)
	}
	return contract
}

// textContract is `local(name, { version: 1, params: {}, members: { text: str() } })`.
func textContract(name string) *contracts.Contract {
	contract, err := contracts.Local(name, contracts.LocalDeclaration{
		Version: 1, Params: contracts.Params{},
		Members: []contracts.LocalEntry{contracts.Mem("text", contracts.Str())},
	})
	if err != nil {
		panic(err)
	}
	return contract
}

// clockContract is `local(name, { version: 1, params: {}, members: { evaluatedAt: str() } })`.
func clockContract(name string) *contracts.Contract {
	contract, err := contracts.Local(name, contracts.LocalDeclaration{
		Version: 1, Params: contracts.Params{},
		Members: []contracts.LocalEntry{contracts.Mem("evaluatedAt", contracts.Str())},
	})
	if err != nil {
		panic(err)
	}
	return contract
}

func labelled(label string) contracts.Implementation {
	return contracts.Implementation{Members: contracts.VariantImplementation{State: map[string]chordWatchable{"label": host.Expose(state.NewConstant(label))}}}
}

// EvaluateFacet implements Evaluator.
func (e dslEvaluator) EvaluateFacet(built BuiltFacet, options EvaluateOptions) (host.Facet, error) {
	where := fmt.Sprintf("plugin %q (%s)", options.Plugin.Name, built.Facet)
	if built.Kind != BuiltFacetScript {
		return host.Facet{}, fmt.Errorf("%s: only a built script can be evaluated", where)
	}
	entry, _, _ := strings.Cut(built.Source, "\n--- ")
	var line string
	for each := range strings.SplitSeq(entry, "\n") {
		if each != "" && !strings.HasPrefix(each, "import ") {
			line = each
			break
		}
	}
	word, rest, _ := strings.Cut(line, " ")
	dir := filepath.Base(options.Plugin.Dir)
	switch word {
	case "item":
		return host.DefineFacet(func(h host.FacetHost, _ host.FacetSelf) error {
			return h.Serve(itemContract(e.named(dir), 1), func(*delta.JsonObject, host.Self) (contracts.Implementation, error) {
				return labelled(rest), nil
			}, host.ServeOptions{})
		}), nil
	case "provider":
		version, _ := strconv.Atoi(rest)
		return host.DefineFacet(func(h host.FacetHost, _ host.FacetSelf) error {
			return h.Serve(numberContract(e.named("provider"), version), func(*delta.JsonObject, host.Self) (contracts.Implementation, error) {
				return contracts.Implementation{Members: contracts.VariantImplementation{State: map[string]chordWatchable{"n": host.Expose(state.NewConstant(float64(version)))}}}, nil
			}, host.ServeOptions{})
		}), nil
	case "consumer":
		version, _ := strconv.Atoi(rest)
		return host.DefineFacet(func(h host.FacetHost, _ host.FacetSelf) error {
			return h.Serve(itemContract(e.named("consumer"), 1), func(_ *delta.JsonObject, self host.Self) (contracts.Implementation, error) {
				provider := self.Get(numberContract(e.named("provider"), version), delta.NewJsonObject(0))
				label := self.Derive([]any{provider.Member("n")}, func(values []any) (delta.JsonValue, error) {
					return fmt.Sprintf("v%d %v", version, values[0]), nil
				})
				return contracts.Implementation{Members: contracts.VariantImplementation{State: map[string]chordWatchable{"label": label}}}, nil
			}, host.ServeOptions{})
		}), nil
	case "gc":
		type bigState struct {
			label string
			big   []string
		}
		made := &bigState{label: rest, big: make([]string, 10_000)}
		for i := range made.big {
			made.big[i] = rest
		}
		record(e.probe, made)
		return host.DefineFacet(func(h host.FacetHost, _ host.FacetSelf) error {
			return h.Serve(itemContract(e.named("gc"), 1), func(*delta.JsonObject, host.Self) (contracts.Implementation, error) {
				return labelled(made.label), nil
			}, host.ServeOptions{})
		}), nil
	case "failed-gc":
		type bigState struct {
			bad bool
			big []string
		}
		made := &bigState{bad: rest == "true", big: make([]string, 10_000)}
		for i := range made.big {
			made.big[i] = "payload"
		}
		record(e.probe, made)
		return host.DefineFacet(func(h host.FacetHost, _ host.FacetSelf) error {
			if made.bad {
				return errors.New("bad setup")
			}
			return h.Serve(numberContract(e.named("failed-gc"), 1), func(*delta.JsonObject, host.Self) (contracts.Implementation, error) {
				return contracts.Implementation{Members: contracts.VariantImplementation{State: map[string]chordWatchable{"n": host.Expose(state.NewConstant(float64(len(made.big))))}}}, nil
			}, host.ServeOptions{})
		}), nil
	case "index-server":
		label := importedLabel(built.Source)
		return host.DefineFacet(func(h host.FacetHost, _ host.FacetSelf) error {
			return h.Serve(itemContract(e.named("index"), 1), func(*delta.JsonObject, host.Self) (contracts.Implementation, error) {
				return labelled(label), nil
			}, host.ServeOptions{})
		}), nil
	case "notes-worker":
		label := importedLabel(built.Source)
		return host.DefineFacet(func(h host.FacetHost, _ host.FacetSelf) error {
			return h.Serve(notesContractOf(e.namespace()), notesFactory(label), host.ServeOptions{})
		}), nil
	case "text":
		// `text <contract> <text>`: serves the local contract with its member `text` constant; `{label}` is the label of
		// `./label.ts`.
		name, text, _ := strings.Cut(rest, " ")
		text = strings.ReplaceAll(text, "{label}", importedLabel(built.Source))
		return host.DefineFacet(func(h host.FacetHost, _ host.FacetSelf) error {
			return h.Serve(textContract(name), func(*delta.JsonObject, host.Self) (contracts.Implementation, error) {
				return contracts.Implementation{Members: contracts.VariantImplementation{State: map[string]chordWatchable{"text": host.Expose(state.NewConstant(text))}}}, nil
			}, host.ServeOptions{})
		}), nil
	case "clock":
		// Serves `<prefix>.clock` with the number of this evaluation: when the plugin's script was evaluated.
		evaluatedAt := fmt.Sprintf("evaluation %d", e.evaluations.Add(1))
		return host.DefineFacet(func(h host.FacetHost, _ host.FacetSelf) error {
			return h.Serve(clockContract(e.named("clock")), func(*delta.JsonObject, host.Self) (contracts.Implementation, error) {
				return contracts.Implementation{Members: contracts.VariantImplementation{State: map[string]chordWatchable{"evaluatedAt": host.Expose(state.NewConstant(evaluatedAt))}}}, nil
			}, host.ServeOptions{})
		}), nil
	case "local", "local-throws":
		// `local <contract> <member> <str|int> <value>`: serves the local contract with one constant member; local-throws
		// has the factory fail with value.
		name, member, kind, value := fields4(rest)
		var constantValue delta.JsonValue = value
		if kind == "int" {
			number, _ := strconv.Atoi(value)
			constantValue = float64(number)
		}
		return host.DefineFacet(func(h host.FacetHost, _ host.FacetSelf) error {
			return h.Serve(oneMemberContract(name, 1, member, kind), func(*delta.JsonObject, host.Self) (contracts.Implementation, error) {
				if word == "local-throws" {
					return contracts.Implementation{}, errors.New(value)
				}
				return contracts.Implementation{Members: contracts.VariantImplementation{State: map[string]chordWatchable{member: host.Expose(state.NewConstant(constantValue))}}}, nil
			}, host.ServeOptions{})
		}), nil
	case "forecast":
		// A consumer of `<prefix>.weather`: `text` is "<celsius> degrees".
		return host.DefineFacet(func(h host.FacetHost, _ host.FacetSelf) error {
			return h.Serve(textContract(e.named("forecast")), func(_ *delta.JsonObject, self host.Self) (contracts.Implementation, error) {
				weather := self.Get(oneMemberContract(e.named("weather"), 1, "celsius", "int"), delta.NewJsonObject(0))
				text := self.Derive([]any{weather.Member("celsius")}, func(values []any) (delta.JsonValue, error) {
					return fmt.Sprintf("%v degrees", values[0]), nil
				})
				return contracts.Implementation{Members: contracts.VariantImplementation{State: map[string]chordWatchable{"text": text}}}, nil
			}, host.ServeOptions{})
		}), nil
	case "price-server":
		// `price-server <cents>`: serves `<prefix>.price` at the version of the plugin's `./contract.ts` (`version <n>`).
		version := importedVersion(built.Source, "./contract.ts")
		cents, _ := strconv.Atoi(rest)
		return host.DefineFacet(func(h host.FacetHost, _ host.FacetSelf) error {
			return h.Serve(oneMemberContract(e.named("price"), version, "cents", "int"), func(*delta.JsonObject, host.Self) (contracts.Implementation, error) {
				return contracts.Implementation{Members: contracts.VariantImplementation{State: map[string]chordWatchable{"cents": host.Expose(state.NewConstant(float64(cents)))}}}, nil
			}, host.ServeOptions{})
		}), nil
	case "summary":
		// A consumer of `<prefix>.price` at the version its copy of `@<prefix>/prices/contract.ts` declares.
		version := importedVersion(built.Source, "@"+e.namespace()+"/prices/contract.ts")
		return host.DefineFacet(func(h host.FacetHost, _ host.FacetSelf) error {
			return h.Serve(textContract(e.named("summary")), func(_ *delta.JsonObject, self host.Self) (contracts.Implementation, error) {
				price := self.Get(oneMemberContract(e.named("price"), version, "cents", "int"), delta.NewJsonObject(0))
				text := self.Derive([]any{price.Member("cents")}, func(values []any) (delta.JsonValue, error) {
					return fmt.Sprintf("price v%d: %v cents", version, values[0]), nil
				})
				return contracts.Implementation{Members: contracts.VariantImplementation{State: map[string]chordWatchable{"text": text}}}, nil
			}, host.ServeOptions{})
		}), nil
	case "cache":
		// `cache <generation>`: `Cache` opens a store per generation, which takes 200 ms (a native); `Status` reads the cache of
		// the plugin's generation.
		generation := rest
		cache, err := contracts.Local(e.named("cache"), contracts.LocalDeclaration{
			Version: 1, Params: contracts.Params{contracts.Prop("generation", contracts.Str())},
			Members: []contracts.LocalEntry{contracts.Mem("generation", contracts.Str())},
		})
		if err != nil {
			return host.Facet{}, err
		}
		return host.DefineFacet(func(h host.FacetHost, _ host.FacetSelf) error {
			if err := h.Serve(cache, func(params *delta.JsonObject, self host.Self) (contracts.Implementation, error) {
				self.Open(func(ctx context.Context) (any, error) {
					select {
					case <-time.After(200 * time.Millisecond):
					case <-ctx.Done():
						return nil, context.Cause(ctx)
					}
					return struct{}{}, nil
				}, func(any, host.NativeCloseReason, context.Context) error { return nil })
				return contracts.Implementation{Members: contracts.VariantImplementation{State: map[string]chordWatchable{"generation": host.Expose(state.NewConstant(params.Value("generation")))}}}, nil
			}, host.ServeOptions{}); err != nil {
				return err
			}
			return h.Serve(textContract(e.named("status")), func(_ *delta.JsonObject, self host.Self) (contracts.Implementation, error) {
				held := self.Get(cache, delta.JsonObjectOf("generation", generation))
				text := self.Derive([]any{held.Member("generation")}, func(values []any) (delta.JsonValue, error) {
					return fmt.Sprintf("cache %v", values[0]), nil
				})
				return contracts.Implementation{Members: contracts.VariantImplementation{State: map[string]chordWatchable{"text": text}}}, nil
			}, host.ServeOptions{})
		}), nil
	case "empty":
		return host.DefineFacet(func(host.FacetHost, host.FacetSelf) error { return nil }), nil
	case "throws":
		return host.Facet{}, errors.New(rest)
	case "setup-throws":
		return host.DefineFacet(func(host.FacetHost, host.FacetSelf) error { return errors.New(rest) }), nil
	}
	return host.Facet{}, fmt.Errorf("%s: its default export is not a facet (defineFacet)", where)
}

// importedLabel is the label a `label <value>` line of an imported file (`import ./label.ts`) gives: upstream's
// `export const label = "<value>"`.
func importedLabel(source string) string {
	_, files, _ := strings.Cut(source, "\n--- ./label.ts\n")
	value, _ := strings.CutPrefix(strings.SplitN(files, "\n", 2)[0], "label ")
	return value
}

// importedVersion is the `version <n>` of an imported file `name` of a built source.
func importedVersion(source, name string) int {
	_, files, _ := strings.Cut(source, "\n--- "+name+"\n")
	text, _ := strings.CutPrefix(strings.SplitN(files, "\n", 2)[0], "version ")
	version, _ := strconv.Atoi(text)
	return version
}

// fields4 splits "<a> <b> <c> <rest>".
func fields4(text string) (a, b, c, rest string) {
	parts := strings.SplitN(text, " ", 4)
	for len(parts) < 4 {
		parts = append(parts, "")
	}
	return parts[0], parts[1], parts[2], parts[3]
}

// oneMemberContract is `local(name, { version, params: {}, members: { [member]: str() | int() } })`.
func oneMemberContract(name string, version int, member, kind string) *contracts.Contract {
	schema := contracts.Str()
	if kind == "int" {
		schema = contracts.Int()
	}
	contract, err := contracts.Local(name, contracts.LocalDeclaration{
		Version: float64(version), Params: contracts.Params{},
		Members: []contracts.LocalEntry{contracts.Mem(member, schema)},
	})
	if err != nil {
		panic(err)
	}
	return contract
}
