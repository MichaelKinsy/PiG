package main

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/BurntSushi/toml"

	"github.com/MichaelKinsy/PiG/test/parity/interface-closure/autobind/rules"
)

// param, callShape and typeParam are the function rule family's types (rules/functions.go).
type (
	param     = rules.Param
	callShape = rules.Call
	typeParam = rules.TypeParam
)

// propShape is one upstream property of an interface or class.
type propShape struct {
	Name     string      `json:"name"`
	Optional bool        `json:"optional"`
	Readonly bool        `json:"readonly"`
	Type     string      `json:"type"`
	Calls    []callShape `json:"calls"`
}

// shape is the union of the shapes the inventory records. Which fields are set depends on the entry kind.
type shape struct {
	// declaration shapes
	TypeParameters []typeParam `json:"typeParameters"`
	Type           string      `json:"type"`
	AliasTo        string      `json:"aliasTarget"`
	Calls          []callShape `json:"calls"`
	Constructs     []callShape `json:"constructs"`
	Properties     []propShape `json:"properties"`
	// property shape
	Name     string `json:"name"`
	Optional bool   `json:"optional"`
	Readonly bool   `json:"readonly"`
	// call-overload and construct-overload shape
	Parameters []param `json:"parameters"`
	Returns    string  `json:"returns"`
}

// upstreamEntry is one interface ID of the upstream inventory.
type upstreamEntry struct {
	ID        string
	ParentID  string
	Role      string
	Name      string
	Kind      string
	Shape     shape
	ShapeHash string
	// RawShape is the shape exactly as recorded; close.py tests it for Promise and AbortSignal.
	RawShape string
	// SourcePath is the file the inventory records for the declaration: a member a class inherits from the TypeScript library (Error.stack) is
	// declared under node_modules/typescript/lib.
	SourcePath string
	// SourceLine is the line of the declaration in SourcePath.
	SourceLine int
}

// UnmarshalJSON keeps the raw shape next to the decoded one.
func (e *upstreamEntry) UnmarshalJSON(data []byte) error {
	var wire struct {
		ID        string          `json:"id"`
		ParentID  string          `json:"parentId"`
		Role      string          `json:"role"`
		Name      string          `json:"name"`
		Kind      string          `json:"kind"`
		Shape     json.RawMessage `json:"shape"`
		ShapeHash string          `json:"shapeHash"`
		Source    struct {
			Path string `json:"path"`
			Line int    `json:"line"`
		} `json:"source"`
	}
	if err := json.Unmarshal(data, &wire); err != nil {
		return err
	}
	*e = upstreamEntry{ID: wire.ID, ParentID: wire.ParentID, Role: wire.Role, Name: wire.Name, Kind: wire.Kind, ShapeHash: wire.ShapeHash, RawShape: string(wire.Shape), SourcePath: wire.Source.Path, SourceLine: wire.Source.Line}
	return json.Unmarshal(wire.Shape, &e.Shape)
}

// mappingRow is the part of a mapping row the tool reads.
type mappingRow struct {
	ID          string   `json:"id"`
	Disposition string   `json:"disposition"`
	Targets     []string `json:"pigTargets"`
	Production  []string `json:"production"`
	Evidence    []string `json:"evidence"`
	Rationale   string   `json:"rationale"`
	// BehaviorContracts are the state-transition contracts a hand row cites.
	BehaviorContracts []string `json:"behaviorContracts"`
}

// ledger is the upstream inventory with the current mapping.
type ledger struct {
	entries   []*upstreamEntry
	byID      map[string]*upstreamEntry
	children  map[string][]*upstreamEntry // parent ID -> direct property, call and construct rows
	mapping   map[string]*mappingRow
	root      string              // repository root, for the checks that read Go files
	contracts map[string]string   // behavior contract statuses, read once from test/parity/behavior-contracts.toml
	cli       map[string]*cliFlag // command-line inventory rows by ID
}

// handLedger replaces the committed mapping when set, so the trust check can run on the ledger as it was closed by hand.
var handLedger string

func loadLedger(root, version string) (*ledger, error) {
	var err error
	base := filepath.Join(root, "test/parity/interfaces")
	var inv struct {
		Interfaces []*upstreamEntry `json:"interfaces"`
	}
	if err := readJSON(filepath.Join(base, "upstream-v"+version+".json"), &inv); err != nil {
		return nil, err
	}
	var m struct {
		Mappings []*mappingRow `json:"mappings"`
	}
	mappingPath := filepath.Join(base, "mapping-v"+version+".json")
	if handLedger != "" {
		mappingPath = handLedger
	}
	if err := readJSON(mappingPath, &m); err != nil {
		return nil, err
	}
	l := &ledger{entries: inv.Interfaces, byID: map[string]*upstreamEntry{}, children: map[string][]*upstreamEntry{}, mapping: map[string]*mappingRow{}, root: root}
	for _, e := range inv.Interfaces {
		l.byID[e.ID] = e
		if e.ParentID != "" {
			l.children[e.ParentID] = append(l.children[e.ParentID], e)
		}
	}
	var reviewed reviewedDecisions
	if handLedger == "" {
		if reviewed, err = readReviewedDecisions(filepath.Join(root, reviewedDecisionsFile)); err != nil {
			return nil, err
		}
	}
	for _, r := range m.Mappings {
		if _, ok := reviewed[r.ID]; handLedger == "" && !ok && isReviewedDisposition(r.Disposition) {
			// Only a reviewed decision stands as one. A designed-out row the rules derived is derived again on every run, so a rule that
			// stops deriving it cannot leave it behind as though a person had decided it.
			r.Disposition = "pending"
		}
		l.mapping[r.ID] = r
	}
	cli, err := loadCLI(root, version)
	if err != nil {
		return nil, err
	}
	l.cli = cli
	return l, nil
}

func readJSON(path string, into any) error {
	data, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	if err := json.Unmarshal(data, into); err != nil {
		return fmt.Errorf("%s: %w", path, err)
	}
	return nil
}

// idParts is a parsed interface ID: pkg:<Pkg>/<Entry>#<Name>[::property:<Member>][::call:<N>|::construct:<N>].
type idParts struct {
	Pkg, Entry, Name, Member, Call, Construct string
	IsCLI                                     bool
}

func parseID(id string) idParts {
	rest, ok := strings.CutPrefix(id, "pkg:")
	if !ok {
		return idParts{IsCLI: true, Name: id}
	}
	head, tail, _ := strings.Cut(rest, "#")
	pkg, entry, _ := strings.Cut(head, "/")
	p := idParts{Pkg: pkg, Entry: entry}
	segments := strings.Split(tail, "::")
	p.Name = segments[0]
	for _, s := range segments[1:] {
		switch {
		case strings.HasPrefix(s, "property:"):
			p.Member = strings.TrimPrefix(s, "property:")
		case strings.HasPrefix(s, "call:"):
			p.Call = strings.TrimPrefix(s, "call:")
		case strings.HasPrefix(s, "construct:"):
			p.Construct = strings.TrimPrefix(s, "construct:")
		}
	}
	return p
}

// topID returns the ID of the declaration that owns id.
func topID(id string) string {
	top, _, _ := strings.Cut(id, "::")
	return top
}

// isUnit reports whether id is closed on its own row: call overloads close with their owner.
func isUnit(id string) bool {
	return !strings.Contains(id, "::call:")
}

// properties returns the direct property rows of a declaration in inventory order.
func (l *ledger) properties(id string) []*upstreamEntry {
	var out []*upstreamEntry
	for _, c := range l.children[id] {
		if c.Role == "property" {
			out = append(out, c)
		}
	}
	return out
}

// callRows returns the call-overload rows of id.
func (l *ledger) callRows(id string) []*upstreamEntry {
	var out []*upstreamEntry
	for _, c := range l.children[id] {
		if c.Role == "call-overload" {
			out = append(out, c)
		}
	}
	return out
}

// contractStatuses maps each behavior contract ID to its status in test/parity/behavior-contracts.toml; a missing file has none.
func (l *ledger) contractStatuses() map[string]string {
	if l.contracts != nil {
		return l.contracts
	}
	var doc struct {
		Contract []struct {
			ID     string `toml:"id"`
			Status string `toml:"status"`
		} `toml:"contract"`
	}
	l.contracts = map[string]string{}
	if _, err := toml.DecodeFile(filepath.Join(l.root, "test/parity/behavior-contracts.toml"), &doc); err == nil {
		for _, c := range doc.Contract {
			l.contracts[c.ID] = c.Status
		}
	}
	return l.contracts
}
