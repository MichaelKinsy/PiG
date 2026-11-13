package hljs

import (
	"bytes"
	"compress/gzip"
	"encoding/json"
	"io"
	"maps"
	"slices"
	"testing"

	"github.com/MichaelKinsy/PiG/internal/zstdblob"
)

// callbackReadsPastTheMatch classifies every language-definition callback by whether it reads the input past the match. patternReach sees only patterns, so a language whose modes call such a callback must have an unbounded pattern too, or a check of its own (the angle guard of the JSX tag callback), or a resume point could precede text that changes the callback's decision.
var callbackReadsPastTheMatch = map[string]bool{
	"endSameAsBegin.begin":         false, // group 1 of the match
	"endSameAsBegin.end":           false, // group 1 of the match
	"shebang.begin":                false, // the match index
	"javascript.isTrulyOpeningTag": true,  // hasClosingTag searches to the end of the input
	"mathematica.systemSymbol":     false, // the lexeme
}

// TestCallbacksReadingPastTheMatchHaveNoResumePoints: every callback is classified, and every language whose grammar reaches a callback that reads past the match has an unbounded reach or the guard that checks the text for that callback, so HighlightResumable offers it no resume point the callback could decide differently at.
func TestCallbacksReadingPastTheMatchHaveNoResumePoints(t *testing.T) {
	if got, want := slices.Sorted(maps.Keys(callbacks)), slices.Sorted(maps.Keys(callbackReadsPastTheMatch)); !slices.Equal(got, want) {
		t.Fatalf("callbacks %v, classified %v: classify every callback", got, want)
	}
	// The full snapshot's nodes continue the eager snapshot's numbering (its firstNode), and its grammars refer to the eager nodes.
	type grammarNode struct {
		Props [][2]json.RawMessage `json:"p"`
	}
	var nodes []grammarNode
	var registrations [][2]json.RawMessage
	eagerReader, err := gzip.NewReader(bytes.NewReader(eagerGrammars))
	if err != nil {
		t.Fatal(err)
	}
	eagerJSON, err := io.ReadAll(eagerReader)
	if err != nil {
		t.Fatal(err)
	}
	allJSON, err := zstdblob.Decode(allGrammars)
	if err != nil {
		t.Fatal(err)
	}
	for _, data := range [][]byte{eagerJSON, allJSON} {
		var document struct {
			FirstNode     int                  `json:"firstNode"`
			Nodes         []grammarNode        `json:"nodes"`
			Registrations [][2]json.RawMessage `json:"registrations"`
		}
		if err := json.Unmarshal(data, &document); err != nil {
			t.Fatal(err)
		}
		if document.FirstNode != len(nodes) {
			t.Fatalf("a snapshot starts at node %d after %d nodes", document.FirstNode, len(nodes))
		}
		nodes = append(nodes, document.Nodes...)
		registrations = append(registrations, document.Registrations...)
	}
	// reads reports whether a node, or a node it refers to, names a callback that reads past the match; seen holds the nodes already visited from the current registration.
	var seen map[int]bool
	var reads func(node int) bool
	var valueReads func(raw json.RawMessage) bool
	valueReads = func(raw json.RawMessage) bool {
		var value struct {
			Ref *int              `json:"ref"`
			Fn  string            `json:"fn"`
			Arr []json.RawMessage `json:"arr"`
		}
		if json.Unmarshal(raw, &value) != nil {
			return false
		}
		if value.Fn != "" && callbackReadsPastTheMatch[value.Fn] {
			return true
		}
		if value.Ref != nil && reads(*value.Ref) {
			return true
		}
		return slices.ContainsFunc(value.Arr, valueReads)
	}
	reads = func(node int) bool {
		if seen[node] {
			return false
		}
		seen[node] = true
		return slices.ContainsFunc(nodes[node].Props, func(prop [2]json.RawMessage) bool { return valueReads(prop[1]) })
	}
	r := NewRegistry()
	r.LoadAllLanguages()
	var found []string
	for _, registration := range registrations {
		var name string
		var root int
		if json.Unmarshal(registration[0], &name) != nil || json.Unmarshal(registration[1], &root) != nil {
			t.Fatalf("registration %s", registration)
		}
		seen = map[int]bool{}
		if !reads(root) || slices.Contains(found, name) {
			continue
		}
		found = append(found, name)
		if reach := languageReach(compileLanguage(r.getLanguage(name))); !reach.unbounded && reach.guards&guardAngle == 0 {
			t.Errorf("%s calls a callback that reads past the match but its patterns are bounded and guard nothing, so it would offer resume points", name)
		}
	}
	if !slices.Contains(found, "javascript") || !slices.Contains(found, "typescript") {
		t.Fatalf("languages calling a callback that reads past the match: %v; want javascript and typescript among them", found)
	}
	t.Logf("languages calling a callback that reads past the match: %v", found)
}
