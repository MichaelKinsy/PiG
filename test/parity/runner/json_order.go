//go:build parity

package runner

import (
	"encoding/json/jsontext"
	"fmt"
	"slices"
	"strings"

	"github.com/MichaelKinsy/PiG/internal/jsonopts"
)

// compareJSONResultsOrdered is json_output_ordered_equal: compareJSONResults first (the same aliases, the same number, string and presence rules, the same raw-output
// choice), then the order in which every object writes its members. A value difference is reported before an order difference, and each error names which it is.
// Object member order is part of the bytes a program writes; json_output_equal keeps it out of the comparison, this form keeps it in.
func compareJSONResultsOrdered(g, p Result, rules []JSONAliasRule) error {
	if err := compareJSONResults(g, p, rules); err != nil {
		return fmt.Errorf("value difference: %w", err)
	}
	left, err := jsonMemberOrders(jsonResultText(g))
	if err != nil {
		return fmt.Errorf("pig: %w", err)
	}
	right, err := jsonMemberOrders(jsonResultText(p))
	if err != nil {
		return fmt.Errorf("pi: %w", err)
	}
	byPath := make(map[string][]string, len(right))
	for _, object := range right {
		byPath[object.path] = object.names
	}
	for _, object := range left {
		other, ok := byPath[object.path]
		if !ok {
			return fmt.Errorf("value difference: %s: object present in pig only", object.path)
		}
		// A member one side omits (an optional duration the alias rules allow to be absent) has no position to compare; the members both sides write must follow one order.
		pig := slices.DeleteFunc(slices.Clone(object.names), func(name string) bool { return !slices.Contains(other, name) })
		pi := slices.DeleteFunc(slices.Clone(other), func(name string) bool { return !slices.Contains(object.names, name) })
		if !slices.Equal(pig, pi) {
			return fmt.Errorf("order difference: %s: member order pig=%s pi=%s", object.path, jsonDiagnostic(pig), jsonDiagnostic(pi))
		}
	}
	return nil
}

// jsonObjectOrder is the member names of one object in the order they were written, at its JSON pointer path ("/<record>/<key>/<index>...").
type jsonObjectOrder struct {
	path  string
	names []string
}

// jsonMemberOrders lists every object of every JSONL record, parents before children, members in document order. It walks the tokens and builds no map, so
// no object member order is lost on the way. Member names are the comparison identities parseJSONRecords uses.
func jsonMemberOrders(output string) ([]jsonObjectOrder, error) {
	if output == "" {
		return nil, fmt.Errorf("empty JSONL output")
	}
	var objects []jsonObjectOrder
	for i, line := range strings.Split(strings.TrimSuffix(output, "\n"), "\n") {
		decoder := jsontext.NewDecoder(strings.NewReader(line), jsonopts.Parse)
		if err := walkJSONMemberOrders(decoder, line, fmt.Sprintf("/%d", i), &objects); err != nil {
			return nil, fmt.Errorf("JSONL record %d: %w", i+1, err)
		}
	}
	return objects, nil
}

func walkJSONMemberOrders(d *jsontext.Decoder, source, path string, objects *[]jsonObjectOrder) error {
	kind, _, err := readJSONToken(d, source)
	if err != nil {
		return err
	}
	switch kind {
	case '{':
		at := len(*objects)
		*objects = append(*objects, jsonObjectOrder{path: path})
		var names []string
		for d.PeekKind() != '}' {
			keyKind, key, err := readJSONToken(d, source)
			if err != nil {
				return err
			}
			name, ok := key.(string)
			if !ok || keyKind != '"' {
				return fmt.Errorf("object key is not a string")
			}
			names = append(names, name)
			if err := walkJSONMemberOrders(d, source, path+"/"+jsonPointerEscaper.Replace(name), objects); err != nil {
				return err
			}
		}
		(*objects)[at].names = names
		_, err := d.ReadToken()
		return err
	case '[':
		for index := 0; d.PeekKind() != ']'; index++ {
			if err := walkJSONMemberOrders(d, source, fmt.Sprintf("%s/%d", path, index), objects); err != nil {
				return err
			}
		}
		_, err := d.ReadToken()
		return err
	}
	return nil
}
