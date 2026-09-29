// Ports packages/coding-agent/src/utils/frontmatter.ts.
package frontmatter

import (
	"fmt"
	"slices"
	"strings"
	"unicode/utf16"

	"github.com/MichaelKinsy/PiG/internal/lazyregexp"

	"go.yaml.in/yaml/v3"
)

// compactMappingError locates the scalar that an illegal same-line mapping would replace. The YAML decoder does not expose scanner marks on errors, so decoding the prefix ending before the offending colon recovers the scalar's source position without interpreting quotes, tags, or mapping keys ourselves.
func compactMappingError(source string, err error) error {
	if err.Error() != "yaml: mapping values are not allowed in this context" && !strings.HasSuffix(err.Error(), ": mapping values are not allowed in this context") {
		return positionedYAMLError(err)
	}
	for end := strings.LastIndexByte(source, ':'); end >= 0; end = strings.LastIndexByte(source[:end], ':') {
		node := scalarAtEnd(source[:end])
		if node == nil || node.Style != 0 || node.Tag != "!!str" || node.Value == "" {
			continue
		}
		lines := strings.Split(source, "\n")
		if node.Line != strings.Count(source[:end], "\n")+1 {
			continue
		}
		line := lines[node.Line-1]
		// yaml.Node columns count code points; Pi's YAML diagnostics count UTF-16 units.
		column := 1
		for _, r := range []rune(line)[:node.Column-1] {
			column += utf16.RuneLen(r)
		}
		return fmt.Errorf("Nested mappings are not allowed in compact mappings at line %d, column %d:\n\n%s\n%s^\n", node.Line, column, line, strings.Repeat(" ", column-1))
	}
	return positionedYAMLError(err)
}

var yamlLineError = lazyregexp.New(`^yaml: line ([0-9]+): (.*)$`)

type yamlDiagnosticError struct {
	message string
	cause   error
}

func (e *yamlDiagnosticError) Error() string { return e.message }
func (e *yamlDiagnosticError) Unwrap() error { return e.cause }

func positionedYAMLError(err error) error {
	if match := yamlLineError.FindStringSubmatch(err.Error()); match != nil {
		return &yamlDiagnosticError{message: match[2] + " at line " + match[1], cause: err}
	}
	return err
}

func scalarAtEnd(source string) *yaml.Node {
	var doc yaml.Node
	if err := yaml.Unmarshal([]byte(source), &doc); err != nil {
		return nil
	}
	return lastScalar(&doc)
}

func lastScalar(node *yaml.Node) *yaml.Node {
	for _, v := range slices.Backward(node.Content) {
		if last := lastScalar(v); last != nil {
			return last
		}
	}
	if node.Kind == yaml.ScalarNode {
		return node
	}
	return nil
}
