package codingagent

import (
	"bytes"
	"strings"

	"github.com/yuin/goldmark/ast"
	gmparser "github.com/yuin/goldmark/parser"
	gmtext "github.com/yuin/goldmark/text"
)

// Ports packages/coding-agent/src/modes/interactive/components/mermaid.ts (Marked fenced-code token boundaries).
// Marked accepts mixed fence suffixes after the required matching run and permits only spaces after the closing fence. Use that same rule for containment and replacement spans.
type markedCodeBlock struct {
	ast.FencedCodeBlock
	start, bodyStart, bodyEnd, end int
	fence                          byte
	length                         int
	indent                         int
}

type markedFenceParser struct{}

func (*markedFenceParser) Trigger() []byte                                 { return []byte{'`', '~'} }
func (*markedFenceParser) CanInterruptParagraph() bool                     { return true }
func (*markedFenceParser) CanAcceptIndentedLine() bool                     { return false }
func (*markedFenceParser) Close(ast.Node, gmtext.Reader, gmparser.Context) {}

func (*markedFenceParser) Open(_ ast.Node, reader gmtext.Reader, pc gmparser.Context) (ast.Node, gmparser.State) {
	line, segment := reader.PeekLine()
	pos := pc.BlockOffset()
	if pos < 0 || pos >= len(line) || (line[pos] != '`' && line[pos] != '~') {
		return nil, gmparser.NoChildren
	}
	end := pos
	for end < len(line) && line[end] == line[pos] {
		end++
	}
	if end-pos < 3 {
		return nil, gmparser.NoChildren
	}
	infoEnd := len(line)
	if infoEnd > 0 && line[infoEnd-1] == '\n' {
		infoEnd--
	}
	if line[pos] == '`' && bytes.IndexByte(line[end:infoEnd], '`') >= 0 {
		return nil, gmparser.NoChildren
	}
	var info *ast.Text
	if end < infoEnd {
		info = ast.NewTextSegment(gmtext.NewSegment(segment.Start-segment.Padding+end, segment.Start-segment.Padding+infoEnd))
	}
	return &markedCodeBlock{
		FencedCodeBlock: *ast.NewFencedCodeBlock(info),
		start:           segment.Start, bodyStart: segment.Stop, bodyEnd: segment.Stop, end: segment.Stop,
		fence: line[pos], length: end - pos, indent: pos,
	}, gmparser.NoChildren
}

func (*markedFenceParser) Continue(node ast.Node, reader gmtext.Reader, _ gmparser.Context) gmparser.State {
	code := node.(*markedCodeBlock)
	line, segment := reader.PeekLine()
	value := strings.TrimSuffix(string(line), "\n")
	if isMermaidFenceClose(value, code.length, code.fence) {
		code.bodyEnd = segment.Start
		code.end = segment.Stop
		if len(line) > 0 && line[len(line)-1] == '\n' {
			code.end--
		}
		// The lexer appends a single space-token newline to the preceding code token; longer whitespace tokens remain independent raw source.
		if rest := reader.Source()[code.end:]; len(rest) > 0 && rest[0] == '\n' {
			if match := markedBlockSpace.Find(rest); len(match) == 1 {
				code.end++
			}
		}
		reader.AdvanceToEOL()
		return gmparser.Close
	}
	code.bodyEnd, code.end = segment.Stop, segment.Stop
	code.Lines().Append(segment)
	reader.AdvanceToEOL()
	return gmparser.Continue | gmparser.NoChildren
}
