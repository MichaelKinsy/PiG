//go:build !pig_strip_mermaid

package codingagent

// Ports packages/coding-agent/src/modes/interactive/components/mermaid.ts (Marked definition ownership).

import (
	"strings"

	"github.com/yuin/goldmark/ast"
	gmparser "github.com/yuin/goldmark/parser"
	gmtext "github.com/yuin/goldmark/text"

	"github.com/MichaelKinsy/PiG/tui/widthx"
)

// A Marked definition owns its entire multiline title before contained HTML or fences are parsed. Post-parse masking cannot recover the blocks those contained openers would otherwise swallow.
type markedDefinitionBlock struct {
	ast.BaseBlock
	end int
}

var kindMarkedDefinition = ast.NewNodeKind("MarkedDefinition")

func (*markedDefinitionBlock) Kind() ast.NodeKind { return kindMarkedDefinition }
func (*markedDefinitionBlock) IsRaw() bool        { return true }
func (b *markedDefinitionBlock) Dump(source []byte, level int) {
	ast.DumpHelper(b, source, level, nil, nil)
}

type markedDefinitionParser struct{}

func (*markedDefinitionParser) Trigger() []byte                                 { return []byte{'['} }
func (*markedDefinitionParser) CanInterruptParagraph() bool                     { return false }
func (*markedDefinitionParser) CanAcceptIndentedLine() bool                     { return false }
func (*markedDefinitionParser) Close(ast.Node, gmtext.Reader, gmparser.Context) {}

func (*markedDefinitionParser) Open(parent ast.Node, reader gmtext.Reader, _ gmparser.Context) (ast.Node, gmparser.State) {
	if parent.Kind() != ast.KindDocument {
		return nil, gmparser.NoChildren
	}
	_, segment := reader.PeekLine()
	source := string(reader.Source()[segment.Start:])
	match := mermaidReferenceDefinition.FindStringSubmatchIndex(source)
	if match == nil || strings.TrimFunc(source[match[2]:match[3]], widthx.IsJSSpace) == "" {
		return nil, gmparser.NoChildren
	}
	return &markedDefinitionBlock{end: segment.Start + match[1]}, gmparser.NoChildren
}

func (*markedDefinitionParser) Continue(node ast.Node, reader gmtext.Reader, _ gmparser.Context) gmparser.State {
	_, segment := reader.PeekLine()
	if segment.Start >= node.(*markedDefinitionBlock).end {
		return gmparser.Close
	}
	reader.AdvanceToEOL()
	return gmparser.Continue | gmparser.NoChildren
}
