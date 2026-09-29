package main

import (
	"bytes"
	"fmt"
	"path/filepath"
	"sort"
	"strings"
	"unicode"

	"github.com/BurntSushi/toml"

	"github.com/MichaelKinsy/PiG/coding"
)

const (
	matrixPath     = "docs/extension-sdk-surface.md"
	mapPath        = "test/parity/sdk-surface.toml"
	exceptionsPath = "test/parity/sdk-surface-exceptions.toml"
)

// sdks are the matrix columns, in order.
var sdks = []string{"node", "go", "rust", "python"}

var sdkTitle = map[string]string{"node": "Node runtime", "go": "Go", "rust": "Rust", "python": "Python"}

const (
	statusImplemented = "implemented"
	statusPartial     = "stand-in/partial"
	statusMissing     = "missing"
)

// rowSpec is one entry of test/parity/sdk-surface.toml.
type rowSpec struct {
	Key string `toml:"key"`
	// Symbols per SDK. "a|b" lists alternatives; "-" means no realization
	// exists by design (the cell is missing unless an exception covers it).
	Node   string `toml:"node"`
	Go     string `toml:"go"`
	Rust   string `toml:"rust"`
	Python string `toml:"python"`
	// Stand-in reasons. Partial applies to every SDK whose own reason is empty.
	Partial       string `toml:"partial"`
	NodePartial   string `toml:"node_partial"`
	GoPartial     string `toml:"go_partial"`
	RustPartial   string `toml:"rust_partial"`
	PythonPartial string `toml:"python_partial"`
	// Wire names the host decoding a value crosses: "extension.Type.jsonName"
	// or "subprocess.Type.jsonName" (a JSON field of that host Go type),
	// "literal:name" (a string or JSON field name the host, its interactive UI or
	// its RPC/print modes read), or "none".
	Wire string `toml:"wire"`
}

func (s rowSpec) symbols(sdk string) string {
	switch sdk {
	case "node":
		return s.Node
	case "go":
		return s.Go
	case "rust":
		return s.Rust
	}
	return s.Python
}

func (s rowSpec) partial(sdk string) string {
	own := map[string]string{"node": s.NodePartial, "go": s.GoPartial, "rust": s.RustPartial, "python": s.PythonPartial}[sdk]
	if own != "" {
		return own
	}
	return s.Partial
}

type mapFile struct {
	Rows []rowSpec `toml:"row"`
}

type exceptionSpec struct {
	Key string `toml:"key"`
	// Prefix covers every surface whose key starts with it, for a whole
	// module or class another change owns.
	Prefix string   `toml:"prefix"`
	SDKs   []string `toml:"sdks"`
	Reason string   `toml:"reason"`
}

type exceptionsFile struct {
	Exceptions []exceptionSpec `toml:"exception"`
}

func (e exceptionSpec) covers(key string) bool {
	if e.Prefix != "" {
		return strings.HasPrefix(key, e.Prefix)
	}
	return key == e.Key
}

func (e exceptionSpec) name() string {
	if e.Prefix != "" {
		return e.Prefix + "*"
	}
	return e.Key
}

// cell is one runtime's realization of one surface.
type cell struct {
	Status string
	Symbol string
	Note   string // stand-in reason, or why a found symbol still does not reach Pi
}

type matrixRow struct {
	surfaceRow
	Cells     map[string]cell
	Exception map[string]string // sdk → reason, for missing cells a reviewed exception covers
}

type matrix struct {
	rows       []matrixRow
	exceptions []exceptionSpec
	unused     []string // map keys naming no surface
}

// sdkSymbols holds every runtime's detected symbols and the host's decoding
// types.
type sdkSymbols struct {
	bySDK     map[string]symbolSet
	extension *goPackage // coding/extension: event and option wire types, API
	host      *goPackage // coding/extension/host/subprocess: wire payloads
	// consumers are the host packages that read values the subprocess host
	// passes through opaquely (the interactive UI and the RPC/print modes).
	consumers []*goPackage
}

func loadSymbols(root string, probe *probeResult) (*sdkSymbols, error) {
	goSDK, err := parseGoPackage(filepath.Join(root, "extensions/sdk"))
	if err != nil {
		return nil, fmt.Errorf("go sdk: %w", err)
	}
	rs, err := parseRust(filepath.Join(root, "extensions/sdk-rs/src"))
	if err != nil {
		return nil, fmt.Errorf("rust sdk: %w", err)
	}
	py, err := parsePython(filepath.Join(root, "extensions/sdk-py/pig_sdk"))
	if err != nil {
		return nil, fmt.Errorf("python sdk: %w", err)
	}
	// The Node runtime's objects come from the runtime probe; the properties
	// it reads from an extension's definitions (tool, command, flag objects)
	// come from its source.
	node, err := parseNodeReads([]string{filepath.Join(root, "coding/extension/host/subprocess/runtime-node/runtime.mjs")})
	if err != nil {
		return nil, fmt.Errorf("node runtime: %w", err)
	}
	for _, sym := range probe.Symbols {
		node[sym] = true
	}
	ext, err := parseGoPackage(filepath.Join(root, "coding/extension"))
	if err != nil {
		return nil, fmt.Errorf("coding/extension: %w", err)
	}
	host, err := parseGoPackage(filepath.Join(root, "coding/extension/host/subprocess"))
	if err != nil {
		return nil, fmt.Errorf("subprocess host: %w", err)
	}
	var consumers []*goPackage
	for _, dir := range []string{"internal/codingagent", "cmd/pig"} {
		pkg, err := parseGoPackage(filepath.Join(root, dir))
		if err != nil {
			return nil, fmt.Errorf("%s: %w", dir, err)
		}
		consumers = append(consumers, pkg)
	}
	return &sdkSymbols{
		bySDK:     map[string]symbolSet{"node": node, "go": goSDK.symbols, "rust": rs, "python": py},
		extension: ext,
		host:      host,
		consumers: consumers,
	}, nil
}

func buildMatrix(root string) (*matrix, error) {
	mirror := filepath.Join(root, ".upstream", "v"+coding.UpstreamVersion)
	up, err := loadUpstream(mirror)
	if err != nil {
		return nil, err
	}
	surface, err := buildSurface(up)
	if err != nil {
		return nil, err
	}
	packages, err := packageExports(root)
	if err != nil {
		return nil, err
	}
	probe, err := runProbe(root, packages)
	if err != nil {
		return nil, err
	}
	syms, err := loadSymbols(root, probe)
	if err != nil {
		return nil, err
	}
	var mf mapFile
	if _, err := toml.DecodeFile(filepath.Join(root, mapPath), &mf); err != nil {
		return nil, err
	}
	var ef exceptionsFile
	if _, err := toml.DecodeFile(filepath.Join(root, exceptionsPath), &ef); err != nil {
		return nil, err
	}
	specs := map[string]rowSpec{}
	for _, s := range mf.Rows {
		if _, dup := specs[s.Key]; dup {
			return nil, fmt.Errorf("%s: duplicate key %q", mapPath, s.Key)
		}
		specs[s.Key] = s
	}
	m := &matrix{exceptions: ef.Exceptions}
	known := map[string]bool{}
	for _, r := range surface {
		if known[r.Key] {
			return nil, fmt.Errorf("upstream surface key %q is not unique", r.Key)
		}
		known[r.Key] = true
		spec := specs[r.Key]
		mr := matrixRow{surfaceRow: r, Cells: map[string]cell{}, Exception: map[string]string{}}
		for _, sdk := range sdks {
			mr.Cells[sdk] = classify(r, spec, sdk, syms)
		}
		m.rows = append(m.rows, mr)
	}
	for _, s := range mf.Rows {
		if !known[s.Key] {
			m.unused = append(m.unused, s.Key)
		}
	}
	m.rows = append(m.rows, packageRows(packages, probe)...)
	for i := range m.rows {
		for _, e := range ef.Exceptions {
			if !e.covers(m.rows[i].Key) {
				continue
			}
			for _, sdk := range e.SDKs {
				if c, ok := m.rows[i].Cells[sdk]; ok && c.Status == statusMissing {
					m.rows[i].Exception[sdk] = e.Reason
				}
			}
		}
	}
	return m, nil
}

// classify decides one cell from the map entry, the default naming rule and
// the detected symbols.
func classify(r surfaceRow, spec rowSpec, sdk string, syms *sdkSymbols) cell {
	set := syms.bySDK[sdk]
	candidates := defaultSymbols(r, sdk)
	if s := spec.symbols(sdk); s != "" {
		candidates = strings.Split(s, "|")
	}
	found := ""
	for _, c := range candidates {
		c = strings.TrimSpace(c)
		if c != "" && c != "-" && set.has(c) {
			found = c
			break
		}
	}
	if found == "" {
		return cell{Status: statusMissing}
	}
	if r.Kind == kindEvent && !hostEmits(r.Event, syms.extension) {
		return cell{Status: statusMissing, Symbol: found, Note: "the host never emits this event"}
	}
	if ok, what := wireCarries(r, spec, syms); !ok {
		return cell{Status: statusMissing, Symbol: found, Note: "the host drops " + what}
	}
	if reason := spec.partial(sdk); reason != "" {
		return cell{Status: statusPartial, Symbol: found, Note: reason}
	}
	return cell{Status: statusImplemented, Symbol: found}
}

// hostEmits reports whether the host dispatches an event: the in-process
// extension API, which the subprocess host bridges, has its On<Event> method.
func hostEmits(event string, ext *goPackage) bool {
	want := "api.on" + strings.ReplaceAll(event, "_", "")
	for sym := range ext.symbols {
		if strings.ToLower(sym) == want {
			return true
		}
	}
	return false
}

// wireTypeNames maps an upstream payload or result type to the host Go type
// that carries it on the subprocess wire, where the names differ.
var wireTypeNames = map[string]string{
	"ToolCallEvent":                "CustomToolCallEvent",
	"ToolResultEvent":              "CustomToolResultEvent",
	"TurnEndEventResult":           "BoundaryResult",
	"AgentBeforeSettleEventResult": "BoundaryResult",
	"ProjectTrustEventResult":      "ProjectTrustEventResult",
}

// wireCarries checks that the host's decoding type carries the row's field.
func wireCarries(r surfaceRow, spec rowSpec, syms *sdkSymbols) (bool, string) {
	wire := spec.Wire
	if wire == "" {
		switch r.Kind {
		case kindPayload, kindResult:
			if r.Name == "" {
				return true, ""
			}
			typ := r.WireType
			if t, ok := wireTypeNames[typ]; ok {
				typ = t
			}
			wire = "extension." + typ + "." + r.Name
		default:
			return true, ""
		}
	}
	if wire == "none" {
		return true, ""
	}
	if lit, ok := strings.CutPrefix(wire, "literal:"); ok {
		if syms.host.literals[lit] || syms.extension.literals[lit] {
			return true, ""
		}
		for _, pkg := range syms.consumers {
			if pkg.literals[lit] {
				return true, ""
			}
		}
		return false, "`" + lit + "`"
	}
	parts := strings.SplitN(wire, ".", 3)
	if len(parts) != 3 {
		return false, "unparseable wire " + wire
	}
	pkg := syms.extension
	if parts[0] == "subprocess" {
		pkg = syms.host
	}
	fields := pkg.jsonFields(parts[1])
	if fields[parts[2]] {
		return true, ""
	}
	// encoding/json matches field names case-insensitively.
	for f := range fields {
		if strings.EqualFold(f, parts[2]) {
			return true, ""
		}
	}
	return false, fmt.Sprintf("`%s` (%s.%s)", parts[2], parts[0], parts[1])
}

// defaultSymbols is the naming rule for a row when the map names no symbol.
func defaultSymbols(r surfaceRow, sdk string) []string {
	n := r.Name
	P := goNames(n)
	s := snake(n)
	switch r.Kind {
	case kindEvent:
		switch sdk {
		case "node":
			return []string{"api.on"}
		case "go":
			return goNames("Event" + pascalWords(r.Event))
		default:
			return []string{"EVENT_" + strings.ToUpper(r.Event)}
		}
	case kindPayload, kindResult:
		return map[string][]string{"node": {"api.on"}, "go": {"EventFunc"}, "rust": {"EventHandler"}, "python": {"Extension.on_event"}}[sdk]
	}
	qualify := func(goTypes, rsTypes, pyTypes []string) []string {
		var out []string
		switch sdk {
		case "go":
			for _, t := range goTypes {
				for _, p := range P {
					out = append(out, t+"."+p)
				}
			}
		case "rust":
			for _, t := range rsTypes {
				sep := "::"
				if r.Kind == kindField {
					sep = "."
				}
				out = append(out, t+sep+s)
			}
		case "python":
			for _, t := range pyTypes {
				out = append(out, t+"."+s)
			}
		}
		return out
	}
	switch r.Root {
	case "pi":
		if sdk == "node" {
			return []string{"api." + n}
		}
		return qualify([]string{"Extension", "Context"}, []string{"Extension", "Context"}, []string{"Extension", "Context"})
	case "events":
		if sdk == "node" {
			return []string{"api.events." + n}
		}
		return qualify([]string{"EventBus"}, []string{"EventBus"}, []string{"EventBus"})
	case "ctx", "replacedCtx":
		if sdk == "node" {
			return []string{"RuntimeContext." + n}
		}
		return qualify([]string{"Context"}, []string{"Context"}, []string{"Context"})
	case "ui":
		if sdk == "node" {
			return []string{"RuntimeUI." + n}
		}
		return qualify([]string{"Context"}, []string{"Context"}, []string{"Context"})
	case "theme":
		if sdk == "node" {
			return []string{"ThemeShim." + n}
		}
		return qualify([]string{"Theme"}, []string{"Theme"}, []string{"Theme"})
	case "sessionManager":
		if sdk == "node" {
			return []string{"RuntimeSessionManager." + n}
		}
		return qualify([]string{"SessionManager", "Context"}, []string{"SessionManager", "Context"}, []string{"SessionManager", "Context"})
	case "modelRegistry":
		if sdk == "node" {
			return []string{"RuntimeModelRegistry." + n}
		}
		return qualify([]string{"ModelRegistry"}, []string{"ModelRegistry"}, []string{"ModelRegistry"})
	case "tool":
		if sdk == "node" {
			return []string{"read:tool." + n}
		}
		return qualify([]string{"ToolDefinition"}, []string{"ToolDefinition"}, []string{"ToolDefinition"})
	case "toolRender":
		return qualify([]string{"ToolRenderContext"}, []string{"ToolRenderContext"}, []string{"ToolRenderContext"})
	case "toolRenderOptions":
		return qualify([]string{"ToolRenderResultOptions"}, []string{"ToolRenderResultOptions"}, []string{"ToolRenderResultOptions"})
	case "command":
		if sdk == "node" {
			return []string{"read:cmd." + n}
		}
		return qualify([]string{"CommandOptions"}, []string{"CommandOptions"}, []string{"CommandOptions"})
	case "messageRenderOptions":
		return qualify([]string{"MessageRenderOptions"}, []string{"MessageRenderOptions"}, []string{"MessageRenderOptions"})
	case "entryRenderOptions":
		return qualify([]string{"EntryRenderOptions"}, []string{"EntryRenderOptions"}, []string{"EntryRenderOptions"})
	case "markdownContext":
		return qualify([]string{"MarkdownTransformContext"}, []string{"MarkdownTransformContext"}, []string{"MarkdownTransformContext"})
	}
	return nil
}

// goNames returns the Go spellings of a camelCase name: plain PascalCase and
// with Go's initialisms (Id → ID, Api → API, Url → URL, Ui → UI).
func goNames(n string) []string {
	if n == "" {
		return nil
	}
	p := strings.ToUpper(n[:1]) + n[1:]
	out := []string{p}
	fixed := p
	for _, pair := range [][2]string{{"Ids", "IDs"}, {"Id", "ID"}, {"Api", "API"}, {"Url", "URL"}, {"Ui", "UI"}, {"Json", "JSON"}, {"Ansi", "ANSI"}} {
		fixed = replaceWord(fixed, pair[0], pair[1])
	}
	if fixed != p {
		out = append(out, fixed)
	}
	return out
}

// replaceWord replaces a camel-case word: from at a word boundary (followed
// by an upper-case letter or the end).
func replaceWord(s, from, to string) string {
	var b strings.Builder
	for i := 0; i < len(s); {
		if strings.HasPrefix(s[i:], from) {
			end := i + len(from)
			if end == len(s) || unicode.IsUpper(rune(s[end])) {
				b.WriteString(to)
				i = end
				continue
			}
		}
		b.WriteByte(s[i])
		i++
	}
	return b.String()
}

// snake converts camelCase to snake_case ("getLeafId" → "get_leaf_id",
// "isUsingOAuth" → "is_using_oauth").
func snake(n string) string {
	n = strings.ReplaceAll(n, "OAuth", "Oauth")
	var b strings.Builder
	rs := []rune(n)
	for i, r := range rs {
		if unicode.IsUpper(r) {
			prevLower := i > 0 && (unicode.IsLower(rs[i-1]) || unicode.IsDigit(rs[i-1]))
			nextLower := i > 0 && i+1 < len(rs) && unicode.IsLower(rs[i+1]) && unicode.IsUpper(rs[i-1])
			if prevLower || nextLower {
				b.WriteByte('_')
			}
			b.WriteRune(unicode.ToLower(r))
			continue
		}
		b.WriteRune(r)
	}
	return b.String()
}

// pascalWords converts snake_case to PascalCase ("session_start" →
// "SessionStart").
func pascalWords(s string) string {
	var b strings.Builder
	for w := range strings.SplitSeq(s, "_") {
		if w == "" {
			continue
		}
		b.WriteString(strings.ToUpper(w[:1]) + w[1:])
	}
	return b.String()
}

// ── Validation ──────────────────────────────────────────────────────────────

// validate fails on a missing cell no reviewed exception covers, and on map
// or exception entries that no longer name a live surface or missing cell.
func (m *matrix) validate() error {
	var problems []string
	for _, r := range m.rows {
		for _, sdk := range rowSDKs(r) {
			if r.Cells[sdk].Status == statusMissing && r.Exception[sdk] == "" {
				problems = append(problems, fmt.Sprintf("%s: %s is missing and %s lists no exception for it", r.Key, sdkTitle[sdk], exceptionsPath))
			}
		}
	}
	for _, k := range m.unused {
		problems = append(problems, fmt.Sprintf("%s names %q, which is not an upstream surface", mapPath, k))
	}
	for _, e := range m.exceptions {
		if strings.TrimSpace(e.Reason) == "" {
			problems = append(problems, fmt.Sprintf("%s: exception for %q has no reason", exceptionsPath, e.name()))
		}
		if (e.Key == "") == (e.Prefix == "") {
			problems = append(problems, fmt.Sprintf("%s: exception %q must set exactly one of key and prefix", exceptionsPath, e.name()))
			continue
		}
		matched := false
		for _, sdk := range e.SDKs {
			if _, ok := sdkTitle[sdk]; !ok {
				problems = append(problems, fmt.Sprintf("%s: exception for %q names unknown runtime %q", exceptionsPath, e.name(), sdk))
			}
		}
		for _, r := range m.rows {
			if !e.covers(r.Key) {
				continue
			}
			matched = true
			for _, sdk := range e.SDKs {
				c, ok := r.Cells[sdk]
				if !ok {
					continue
				}
				// A prefix covers a module or class, some of whose members
				// may exist; an exact key must name a missing cell.
				if e.Prefix == "" && c.Status != statusMissing {
					problems = append(problems, fmt.Sprintf("%s: exception for %q in %s is stale: that cell is %s", exceptionsPath, e.name(), sdkTitle[sdk], c.Status))
				}
			}
		}
		if !matched {
			problems = append(problems, fmt.Sprintf("%s names %q, which is not an upstream surface", exceptionsPath, e.name()))
			continue
		}
		if e.Prefix != "" && !m.prefixCoversMissing(e) {
			problems = append(problems, fmt.Sprintf("%s: exception for %q is stale: none of its cells is missing", exceptionsPath, e.name()))
		}
	}
	if len(problems) > 0 {
		sort.Strings(problems)
		return fmt.Errorf("%d problem(s):\n  %s", len(problems), strings.Join(problems, "\n  "))
	}
	return nil
}

func (m *matrix) prefixCoversMissing(e exceptionSpec) bool {
	for _, r := range m.rows {
		if !e.covers(r.Key) {
			continue
		}
		for _, sdk := range e.SDKs {
			if c, ok := r.Cells[sdk]; ok && c.Status == statusMissing {
				return true
			}
		}
	}
	return false
}

// ── Rendering ───────────────────────────────────────────────────────────────

func (m *matrix) counts() map[string]map[string]int {
	out := map[string]map[string]int{}
	for _, sdk := range sdks {
		out[sdk] = map[string]int{}
	}
	for _, r := range m.rows {
		if isPackageRow(r) {
			continue
		}
		for _, sdk := range sdks {
			c := r.Cells[sdk]
			out[sdk][c.Status]++
			if c.Status == statusMissing && r.Exception[sdk] != "" {
				out[sdk]["excepted"]++
			}
		}
	}
	return out
}

func (m *matrix) render() []byte {
	var b bytes.Buffer
	w := func(format string, a ...any) { fmt.Fprintf(&b, format, a...) }
	w("<!-- Code generated by go run ./test/parity/cmd/sdksurface; DO NOT EDIT. -->\n\n")
	w("# Extension SDK surface matrix\n\n")
	w("Every extension-facing surface of Pi %s's extension API, and how each PiG extension runtime realizes it. ", coding.UpstreamVersion)
	w("The rows come from Pi's `packages/coding-agent/src/core/extensions/types.ts` and the declarations it re-exports or exposes through its members ")
	w("(`EventBus`, `ExecOptions`/`ExecResult`, `CacheWarmingDecisionEvent`, `ReadonlySessionManager`, `ModelRegistry`, `Theme`, `AgentToolResult`), read from `.upstream/v%s`.\n\n", coding.UpstreamVersion)
	w("A cell's status comes from the runtime itself: the Go SDK's exported identifiers, the Rust SDK's `pub` items and the Python SDK's classes, members and parameters, read from their source, and the Node runtime's objects, read by instantiating the runtime in Node (`test/parity/cmd/sdksurface/probe.mjs`), plus the properties it reads from an extension's definitions. ")
	w("[`%s`](../%s) names the symbol where a language's naming differs from the default rule and says why a realization is a stand-in; a named symbol that does not exist is `missing`. ", mapPath, mapPath)
	w("Payload, result and option fields that cross the subprocess wire are also checked against the host's Go decoding type, so a field an SDK sends but the host drops is `missing`.\n\n")
	w("- `implemented`: the symbol and any checked wire fields exist; this is not behavioral proof. See `docs/extension-api-parity.md` and the conformance tests.\n")
	w("- `stand-in/partial`: the symbol exists; the note says what differs and why.\n")
	w("- `missing`: no symbol. Each missing cell is listed in [`%s`](../%s) with its reviewed reason, and `go test ./test/parity/cmd/sdksurface` fails on any other.\n\n", exceptionsPath, exceptionsPath)
	w("The package sections list every runtime export of the modules Pi serves to extensions (`pi-coding-agent`, `pi-tui`, `pi-ai`, `pi-ai/compat`, `pi-ai/providers/all`, `pi-agent-core`), from the compiler-derived inventory of Pi's `.d.ts` files (`test/parity/interfaces/upstream-v%s.json`), and every public instance member of each exported class represented by that inventory. Static factories and overload-specific reachability are outside this probe. ", coding.UpstreamVersion)
	w("Only the Node runtime imports these modules. The probe loads each module the runtime serves and classifies every value by where its code lives, using the V8 inspector's function locations:\n\n")
	w("- `Pi's own code`: the function originates in the vendored pinned modules or their dependencies; host import rewrites can still affect behavior (D73).\n")
	w("- `bridged`: PiG's implementation in the extension runtime, a line-for-line port or a host bridge, locked by the D73 tests (`TestNodeRuntimeShimsExportEveryPinnedPiValue`, `TestPiTuiComponentsMatchThePinnedPackage`, `TestPiAiUtilitiesMatchThePinnedPackage`, `TestPiThemeHelpersMatchThePinnedPackage`).\n")
	w("- `stand-in`: importable, and throws the quoted error when called or constructed (D73).\n")
	w("- `missing`: the module does not serve it.\n\n")
	w("Regenerate with `go run ./test/parity/cmd/sdksurface`; `make sdk-surface-drift` fails when this file is stale.\n\n")

	w("## Summary\n\n")
	api, pkg := 0, 0
	for _, r := range m.rows {
		if isPackageRow(r) {
			pkg++
		} else {
			api++
		}
	}
	w("%d extension API surfaces:\n\n", api)
	w("| Runtime | implemented | stand-in/partial | missing (all with a reviewed exception) |\n|---|---|---|---|\n")
	counts := m.counts()
	for _, sdk := range sdks {
		c := counts[sdk]
		w("| %s | %d | %d | %d |\n", sdkTitle[sdk], c[statusImplemented], c[statusPartial], c[statusMissing])
	}
	w("\n%d package exports and class members, Node runtime:\n\n", pkg)
	w("| Module | Pi's own code | bridged | stand-in | missing (all with a reviewed exception) |\n|---|---|---|---|---|\n")
	for _, p := range piPackages {
		c := map[string]int{}
		for _, r := range m.rows {
			if isPackageRow(r) && r.Group == "Package exports: "+p.module {
				c[r.Cells["node"].Status]++
			}
		}
		w("| `%s` | %d | %d | %d | %d |\n", p.module, c[statusVendored], c[statusBridged], c[statusStandIn], c[statusMissing])
	}
	w("\n")

	for i, r := range m.rows {
		if i == 0 || r.Group != m.rows[i-1].Group {
			w("## %s\n\n", r.Group)
			if isPackageRow(r) {
				w("| Export | Pi declaration | Node runtime |\n|---|---|---|\n")
			} else {
				w("| Surface | Pi declaration | Node runtime | Go | Rust | Python |\n|---|---|---|---|---|---|\n")
			}
		}
		w("| `%s` | `%s` |", escapeCell(r.Key), escapeCell(r.Decl))
		for _, sdk := range rowSDKs(r) {
			w(" %s |", renderCell(r.Cells[sdk], r.Exception[sdk] != ""))
		}
		w("\n")
		if i+1 == len(m.rows) || m.rows[i+1].Group != r.Group {
			w("\n")
		}
	}

	w("## Exceptions\n\n")
	if len(m.exceptions) == 0 {
		w("None.\n")
	} else {
		w("| Surface | Runtimes | Reason |\n|---|---|---|\n")
		for _, e := range m.exceptions {
			var names []string
			for _, sdk := range e.SDKs {
				names = append(names, sdkTitle[sdk])
			}
			w("| `%s` | %s | %s |\n", escapeCell(e.name()), strings.Join(names, ", "), escapeCell(e.Reason))
		}
	}
	return b.Bytes()
}

func renderCell(c cell, excepted bool) string {
	switch c.Status {
	case statusImplemented:
		return "implemented `" + escapeCell(c.Symbol) + "`"
	case statusPartial:
		return "stand-in/partial `" + escapeCell(c.Symbol) + "`: " + escapeCell(c.Note)
	case statusVendored, statusBridged:
		return c.Status
	case statusStandIn:
		return "stand-in: " + escapeCell(c.Note)
	}
	s := "missing"
	if c.Note != "" {
		s += " (`" + escapeCell(c.Symbol) + "`: " + escapeCell(c.Note) + ")"
	}
	if excepted {
		s += " [exception](#exceptions)"
	}
	return s
}

func escapeCell(s string) string {
	return strings.ReplaceAll(strings.ReplaceAll(s, "|", `\|`), "\n", " ")
}
