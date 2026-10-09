package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"maps"
	"os"
	"path/filepath"
	"slices"
	"strings"
)

// The ledger status is derived: a row that is not a gap is `ported`, a row that is a gap is `pending`. Only numbered divergences and
// designed-out decisions are set by hand and are kept as they are.

const derivedPrefix = "Derived by the interface gap detector"

// reasonHeld is the gap reason of a row that holds.json keeps pending: the shape rules pass, but a reviewed behaviour difference from Pi
// is still open. heldPrefix starts the rationale the ledger carries for it.
const (
	reasonHeld = "held"
	heldPrefix = "Held pending by test/parity/interface-closure/autobind/holds.json: "
)

// reasonReviewedGap is the gap reason of a row no rule decides that review found Go does not carry (reviewed-gaps.json): it needs
// porting, and its reason names the shard and the Pi source.
const reasonReviewedGap = "reviewed-gap"

// applyReviewedGaps gives every reviewed undecided row the reason reviewed-gap and its reviewed reason. An entry without a reason fails.
// An entry for a row that is not undecided (gone, or decided by a rule) is stale: a review cannot outlive the question it answered. The
// stale IDs are returned together, sorted, so the caller reports or prunes them in one run.
func applyReviewedGaps(ds []*decision, gaps map[string]string) (stale []string, err error) {
	byID := make(map[string]*decision, len(ds))
	for _, d := range ds {
		byID[d.ID] = d
	}
	for _, id := range slices.Sorted(maps.Keys(gaps)) {
		d := byID[id]
		switch {
		case strings.TrimSpace(gaps[id]) == "":
			return nil, fmt.Errorf("%s: %s has no reason", reviewedGapsFile, id)
		case d == nil || d.Reason != reasonUndecided:
			stale = append(stale, id)
		default:
			d.Reason, d.Detail = reasonReviewedGap, gaps[id]
		}
	}
	return stale, nil
}

// pruneReviewedGaps rewrites the reviewed-gaps file without the stale IDs.
func pruneReviewedGaps(path string, gaps map[string]string, stale []string) error {
	for _, id := range stale {
		delete(gaps, id)
	}
	return writeIndentedJSON(path, gaps, " ")
}

// pruneReviewedTypes rewrites the reviewed-types file without the stale IDs.
func pruneReviewedTypes(path string, reviewed map[string]reviewedType, stale []string) error {
	for _, id := range stale {
		delete(reviewed, id)
	}
	return writeIndentedJSON(path, reviewed, "  ")
}

// writeIndentedJSON writes value as the review files are formatted: sorted keys, one indent unit, no HTML escaping, a final newline.
func writeIndentedJSON(path string, value any, indent string) error {
	var out bytes.Buffer
	encoder := json.NewEncoder(&out)
	encoder.SetEscapeHTML(false)
	encoder.SetIndent("", indent)
	if err := encoder.Encode(value); err != nil {
		return err
	}
	return os.WriteFile(path, out.Bytes(), 0o644)
}

// applyHolds records the reason of every held ID and turns a held row the rules pass into a gap with reason held; a row the rules
// already report keeps its own reason. A hold for an ID the ledger does not have fails, so a hold cannot outlive its row unnoticed.
func applyHolds(ds []*decision, holds map[string]string) error {
	byID := make(map[string]*decision, len(ds))
	for _, d := range ds {
		byID[d.ID] = d
	}
	for _, id := range slices.Sorted(maps.Keys(holds)) {
		d := byID[id]
		if d == nil {
			return fmt.Errorf("%s: no interface ID %s", holdsFile, id)
		}
		if strings.TrimSpace(holds[id]) == "" {
			return fmt.Errorf("%s: %s has no reason", holdsFile, id)
		}
		d.Held = holds[id]
		if !d.Gap {
			d.Gap, d.Reason, d.Detail = true, reasonHeld, holds[id]
			d.Sym, d.Info, d.Evidence = nil, nil, ""
		}
		// A held row keeps every row above it open, as any other gap below a declaration does (foldChildren).
		for parent := id; strings.Contains(parent, "::"); {
			parent = parent[:strings.LastIndex(parent, "::")]
			if p := byID[parent]; p != nil && !p.Gap && p.Evidence != ruleDesignedOut {
				p.Gap, p.Reason, p.Detail = true, reasonChild, "a row below it is held, for example "+id+" ("+reasonHeld+")"
				p.Sym, p.Info, p.Evidence = nil, nil, ""
			}
		}
	}
	return nil
}

// kv is one key of an ordered JSON object.
type kv struct {
	key   string
	value json.RawMessage
}

// orderedRow is a mapping row that keeps its key order, so that syncing an unchanged row rewrites it byte for byte.
type orderedRow []kv

func (r orderedRow) MarshalJSON() ([]byte, error) {
	var b bytes.Buffer
	b.WriteByte('{')
	for i, p := range r {
		if i > 0 {
			b.WriteByte(',')
		}
		k, _ := json.Marshal(p.key)
		b.Write(k)
		b.WriteByte(':')
		b.Write(p.value)
	}
	b.WriteByte('}')
	return b.Bytes(), nil
}

func (r *orderedRow) UnmarshalJSON(data []byte) error {
	dec := json.NewDecoder(bytes.NewReader(data))
	if _, err := dec.Token(); err != nil {
		return err
	}
	*r = nil
	for dec.More() {
		tok, err := dec.Token()
		if err != nil {
			return err
		}
		var v json.RawMessage
		if err := dec.Decode(&v); err != nil {
			return err
		}
		*r = append(*r, kv{key: tok.(string), value: v})
	}
	return nil
}

func (r orderedRow) get(key string) string {
	for _, p := range r {
		if p.key == key {
			var s string
			_ = json.Unmarshal(p.value, &s)
			return s
		}
	}
	return ""
}

func jsonValue(v any) json.RawMessage {
	var b bytes.Buffer
	enc := json.NewEncoder(&b)
	enc.SetEscapeHTML(false)
	if err := enc.Encode(v); err != nil {
		panic(err) // strings and string slices always encode
	}
	return bytes.TrimSpace(b.Bytes())
}

// mappingDoc is the mapping file with its rows in order.
type mappingDoc struct {
	UpstreamVersion string       `json:"upstreamVersion"`
	Mappings        []orderedRow `json:"mappings"`
}

func encodeMapping(doc *mappingDoc) ([]byte, error) {
	var b bytes.Buffer
	enc := json.NewEncoder(&b)
	enc.SetEscapeHTML(false)
	enc.SetIndent("", "  ")
	if err := enc.Encode(doc); err != nil {
		return nil, err
	}
	return b.Bytes(), nil
}

// withBehaviorContracts keeps the state-transition contracts a hand row cites: they live in behavior-contracts.toml and no rule derives them.
func withBehaviorContracts(row orderedRow, contracts []string) orderedRow {
	if len(row.list("behaviorContracts")) > 0 {
		return row
	}
	return append(row, kv{"behaviorContracts", jsonValue(contracts)})
}

func pendingRow(id, hash string) orderedRow {
	return orderedRow{{"id", jsonValue(id)}, {"disposition", jsonValue("pending")}, {"upstreamShapeHash", jsonValue(hash)}}
}

// needsExtensionAPILayers reports an ExtensionAPI.on overload: it needs the eleven extension API layers, which no rule derives, so the
// row stays pending by hand and its owner cannot be derived as ported.
func needsExtensionAPILayers(id string) bool {
	return strings.Contains(id, "#ExtensionAPI::property:on::call:")
}

// derivedRow builds the ported row for a decision that is not a gap.
func derivedRow(l *ledger, d *decision, hash string) (orderedRow, bool) {
	if d.Sym == nil || d.Info == nil {
		return nil, false
	}
	if needsExtensionAPILayers(d.ID) {
		return nil, false
	}
	target := d.Sym.Target()
	production, layer := []string{"call:" + d.Info.Call}, "complete"
	switch {
	case d.Info.Call != "":
	case d.Sym.PublicAPI():
		production, layer = []string{"public-api:" + target}, publicAPITestedStatus
	default:
		production, layer = []string{"tested:" + target}, testExercisedStatus
	}
	evidence := d.Info.Tests
	if len(evidence) == 0 {
		evidence = []string{"reach:" + d.Info.Call}
	}
	layers := map[string]string{"shape": "complete", "production": layer, "behavior": "complete"}
	if d.Layers != nil {
		layers = maps.Clone(d.Layers)
		layers["production"] = layer
	}
	rule := d.Sym.Tier
	if rule == "" {
		rule = ruleExact
	}
	how := "an asserting Go test"
	if len(d.Info.Tests) == 0 {
		how = "a function reachable from cmd/pig"
	}
	rationale := fmt.Sprintf("%s: %s is Go %s (name rule %s), every shape rule holds, and it is exercised by %s. The row is re-derived on every run of make interface-gaps-update.", derivedPrefix, shortName(d.ID), target, rule, how)
	row := orderedRow{
		{"id", jsonValue(d.ID)}, {"disposition", jsonValue("ported")}, {"upstreamShapeHash", jsonValue(hash)}, {"rationale", jsonValue(rationale)},
		{"pigTargets", jsonValue(append([]string{target}, d.Targets...))},
		{"layers", jsonValue(layers)},
		{"production", jsonValue(production)}, {"evidence", jsonValue(evidence)},
	}
	if e := l.byID[d.ID]; e != nil && (strings.Contains(e.RawShape, "Promise<") || strings.Contains(e.RawShape, "AbortSignal")) {
		row = append(row, kv{"asyncContract", jsonValue(asyncContract)})
	}
	return row, true
}

const (
	publicAPITestedStatus = "public-api-tested"
	testExercisedStatus   = "test-exercised"
)

// syncStats counts what a sync changed.
type syncStats struct {
	Derived, Reopened, Kept, Underivable int
	Decisions                            decisionStats
}

// withContracts carries the behavior contracts a row cites onto its replacement: the detector reads them (rule B1 closes a handleInput
// row only when one is cited) and the validator requires them on a handleInput row that claims behavior complete.
func withContracts(next, prev orderedRow) orderedRow {
	if contracts := prev.list("behaviorContracts"); len(contracts) > 0 && len(next.list("behaviorContracts")) == 0 {
		return append(next, kv{"behaviorContracts", jsonValue(contracts)})
	}
	return next
}

// syncMapping derives every status from the decisions. A numbered divergence and a designed-out decision stay as they are; a hand-written
// ported row the rules confirm stays as it is; every other row becomes the derived ported row or a pending row.
func syncMapping(l *ledger, doc *mappingDoc, decisions map[string]*decision) syncStats {
	var st syncStats
	contracts := make(map[int][]string)
	for i, row := range doc.Mappings {
		if c := row.list("behaviorContracts"); len(c) > 0 {
			contracts[i] = c
		}
	}
	for i, row := range doc.Mappings {
		id, disp, hash := row.get("id"), row.get("disposition"), row.get("upstreamShapeHash")
		if disp == "divergence" || disp == "designed-out" {
			st.Kept++
			continue
		}
		d := decisions[id]
		if d == nil || d.Gap {
			if disp != "pending" {
				st.Reopened++
			}
			doc.Mappings[i] = pendingRow(id, hash)
			if d != nil && d.Held != "" {
				doc.Mappings[i] = append(doc.Mappings[i], kv{"rationale", jsonValue(heldPrefix + d.Held)})
			}
			doc.Mappings[i] = withContracts(doc.Mappings[i], row)
			continue
		}
		if disp == "ported" && !strings.HasPrefix(row.get("rationale"), derivedPrefix) && l.referencesResolve(row) {
			st.Kept++
			continue
		}
		if d.DesignedOut != "" {
			doc.Mappings[i] = orderedRow{
				{"id", jsonValue(id)}, {"disposition", jsonValue("designed-out")}, {"upstreamShapeHash", jsonValue(hash)},
				{"rationale", jsonValue(d.DesignedOut)}, {"pigTargets", jsonValue([]string{d.Candidate})},
			}
			st.Derived++
			continue
		}
		derived, ok := derivedRow(l, d, hash)
		if !ok {
			st.Underivable++
			doc.Mappings[i] = pendingRow(id, hash)
			continue
		}
		doc.Mappings[i] = withContracts(derived, row)
		st.Derived++
	}
	for i, c := range contracts {
		doc.Mappings[i] = withBehaviorContracts(doc.Mappings[i], c)
	}
	inheritCallContracts(doc)
	return st
}

// inheritCallContracts gives a handleInput `::call:N` row the contracts of its property row: the call is the property's one signature, the
// state machine is the same, and the validator requires a contract on every handleInput row that claims behavior complete.
func inheritCallContracts(doc *mappingDoc) {
	byID := make(map[string]orderedRow, len(doc.Mappings))
	for _, row := range doc.Mappings {
		byID[row.get("id")] = row
	}
	for i, row := range doc.Mappings {
		property, _, found := strings.Cut(row.get("id"), "::call:")
		if !found || !strings.HasSuffix(property, "::property:handleInput") || row.get("disposition") != "ported" || len(row.list("behaviorContracts")) > 0 {
			continue
		}
		if parent, ok := byID[property]; ok {
			doc.Mappings[i] = withBehaviorContracts(row, parent.list("behaviorContracts"))
		}
	}
}

func readMapping(path string) (*mappingDoc, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var doc mappingDoc
	if err := json.Unmarshal(data, &doc); err != nil {
		return nil, fmt.Errorf("%s: %w", path, err)
	}
	return &doc, nil
}

// shortName is the upstream declaration name of an ID without its package and entrypoint.
func shortName(id string) string {
	_, tail, _ := strings.Cut(id, "#")
	return tail
}

// asyncContract is the contract text for a derived row whose shape has a Promise or an AbortSignal.
const asyncContract = "An awaited Promise is a blocking Go call that returns the value or error; AbortSignal is the context.Context argument; no detached goroutine."

// list returns a string-array field of a row.
func (r orderedRow) list(key string) []string {
	for _, p := range r {
		if p.key == key {
			var out []string
			_ = json.Unmarshal(p.value, &out)
			return out
		}
	}
	return nil
}

// referencesResolve reports whether every Go target and production reference of a hand-written row still names a Go declaration. A hand
// row whose declaration was renamed or removed is derived again instead of being kept.
func (l *ledger) referencesResolve(row orderedRow) bool {
	for _, ref := range row.list("production") {
		if file, _, _ := strings.Cut(strings.TrimPrefix(strings.TrimPrefix(strings.TrimPrefix(ref, "call:"), "tested:"), "public-api:"), "#"); filepath.Base(file) == "parity_harness.go" {
			return false // a parity-harness probe runs only under PIG_PARITY_HARNESS=1 and is not a production caller
		}
	}
	refs := append(row.list("pigTargets"), row.list("production")...)
	for _, ref := range refs {
		ref = strings.TrimPrefix(strings.TrimPrefix(strings.TrimPrefix(ref, "call:"), "tested:"), "public-api:")
		file, frag, ok := strings.Cut(ref, "#")
		if !ok || !strings.HasSuffix(file, ".go") {
			continue
		}
		if !l.declares(file, frag) {
			return false
		}
	}
	return true
}

// declares reports whether the Go file declares the type, function, value or Owner.Member the fragment names.
func (l *ledger) declares(file, frag string) bool {
	if l.root == "" {
		return true
	}
	f, err := parser.ParseFile(token.NewFileSet(), filepath.Join(l.root, file), nil, 0)
	if err != nil {
		return false
	}
	owner, name, hasOwner := strings.Cut(frag, ".")
	if !hasOwner {
		name, owner = owner, ""
	}
	found := false
	ast.Inspect(f, func(n ast.Node) bool {
		switch d := n.(type) {
		case *ast.FuncDecl:
			if hasOwner {
				found = found || d.Name.Name == name && recvOf(d.Recv) == owner
			} else {
				found = found || d.Recv == nil && d.Name.Name == name
			}
		case *ast.TypeSpec:
			if !hasOwner {
				found = found || d.Name.Name == name
			} else if d.Name.Name == owner {
				found = found || memberDeclared(d.Type, name)
			}
		case *ast.ValueSpec:
			if !hasOwner {
				for _, id := range d.Names {
					found = found || id.Name == name
				}
			}
		}
		return !found
	})
	return found
}

func recvOf(recv *ast.FieldList) string {
	if recv == nil || len(recv.List) == 0 {
		return ""
	}
	return receiverName(recv.List[0].Type)
}

func memberDeclared(expr ast.Expr, name string) bool {
	var fields *ast.FieldList
	switch v := expr.(type) {
	case *ast.StructType:
		fields = v.Fields
	case *ast.InterfaceType:
		fields = v.Methods
	}
	if fields == nil {
		return false
	}
	for _, f := range fields.List {
		for _, id := range f.Names {
			if id.Name == name {
				return true
			}
		}
	}
	return false
}
