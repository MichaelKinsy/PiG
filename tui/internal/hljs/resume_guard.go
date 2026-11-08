package hljs

import "strings"

// guardSet is the text checks that stand in for the patterns of a language that read without bound across lines. A pattern
// that reads without bound makes every resume point unsafe, because an attempt that started before the point may have read
// to the end of the code, and more text after that end decides it differently (HighlightResumable). javascript and
// typescript have three such patterns and one callback that reads past the match; each reads without bound only when the
// text holds a construct a check can see, so a point is kept only where the check shows no attempt at or before it can have
// read to the end.
// pig additive (D112): see hlCacheMax in the tui package. The checks only decide which resume points a run offers; the HTML of a resumed run is the one of a whole run.
type guardSet uint8

const (
	// guardParens: a pattern of the form `(` [^()]* `)` followed by `=>` or `{` (an arrow function's parameters, a
	// method's). An attempt at a parenthesis ends at the parenthesis that closes it, or fails at a fourth nested level, and
	// at the end of the code when none closes it. A point is kept where every parenthesis at or before it is closed.
	guardParens guardSet = 1 << iota
	// guardTrivia: the object key pattern `[{,\n]\s*(?=(comment\s*)*ident\s*:)`. Its look-ahead reads a chain of comments and
	// white space, and a comment may be a block comment that is not closed yet or one more line comment, without bound. An
	// attempt at or before the point reads past it only through white space and comments, so a point is kept where the text
	// from it reaches a character that is none of those, through block comments the pattern surely closes at the first `*/`.
	guardTrivia
	// guardAngle: the JSX tag begin `<name` and its callback, which accepts a tag that ends the code and searches the rest of
	// the code for a closing tag. A point is kept where no `<name` at or before it is followed by `>` or the end of the code.
	guardAngle
)

// guardedSources are the patterns of highlight.js 10.7.3's javascript and typescript that patternReach finds to read without bound, each with the check that bounds it. A language whose unbounded patterns are all listed has no unbounded reach, and keeps the points that pass its checks. TestGuardedSourcesArePinned fails when a grammar update adds, removes or changes one, so each is reviewed again.
var guardedSources = map[string]guardSet{
	`[{,\n]\s*(?=(((\/\/.*$)|(\/\*(\*[^/]|[^*])*\*\/))\s*)*[A-Za-z$_][0-9A-Za-z$_]*\s*:)`: guardTrivia,
	`(\([^()]*(\([^()]*(\([^()]*\)[^()]*)*\)[^()]*)*\)|[a-zA-Z_]\w*)\s*=>`:                guardParens,
	`[a-zA-Z_]\w*\([^()]*(\([^()]*(\([^()]*\)[^()]*)*\)[^()]*)*\)\s*\{`:                   guardParens,
}

// xmlTagBegin is the begin pattern of XML_TAG in javascript and typescript, the one whose callback reads past the match.
const xmlTagBegin = `<[A-Za-z0-9\\._:-]+`

// guardTokens is how many white-space-terminated tokens the text after a point holds, at least. The patterns of these
// languages that read across white space continue with a few characters (`=>`, `{`, a name and a colon), so an attempt
// that crosses a point through white space reads at most a few tokens past it. A point with more tokens after it than any
// attempt reads cannot have an attempt that reached the end of the code.
const guardTokens = 6

// guardTriviaBudget bounds the bytes the trivia check reads for one call, which a long run of comments after the last
// points would otherwise read for each of them. Points that are not examined within it are dropped, as safe.
// pig additive (D112): see hlCacheMax in the tui package. The bound is a Go CPU choice; a dropped point only offers fewer resume points, and the HTML of a run is the same.
const guardTriviaBudget = 1 << 16

// guardPoints keeps the points of a run over code at which every check of g holds. points are in increasing Byte order. A
// point is where a run starts again, so the attempts it does not repeat are those that started at it or before it: at a
// point that follows a mode that ended without consuming text, the attempt of that mode at the point itself was made
// already, and its result stays.
func guardPoints(points []ResumePoint, code string, g guardSet) []ResumePoint {
	if g == 0 || len(points) == 0 {
		return points
	}
	valid := make([]bool, len(points))
	depth, scanned := 0, 0
	angleBad := false
	for i, p := range points {
		for ; scanned <= p.Byte && scanned < len(code); scanned++ {
			switch code[scanned] {
			case '(':
				depth++
			case ')':
				if depth > 0 {
					depth--
				}
			case '<':
				if g&guardAngle == 0 {
					break
				}
				end := scanned + 1
				for end < len(code) && isXMLNameByte(code[end]) {
					end++
				}
				if end == scanned+1 {
					break
				}
				// A name that ends the code, or is followed by `>`, is read past the match by the callback.
				if end >= len(code) || code[end] == '>' {
					angleBad = true
				}
			}
		}
		valid[i] = (g&guardParens == 0 || depth == 0) && (g&guardAngle == 0 || !angleBad)
	}
	if g&guardTrivia != 0 {
		budget := guardTriviaBudget
		for i := len(points) - 1; i >= 0; i-- {
			if !valid[i] {
				continue
			}
			if budget <= 0 {
				valid[i] = false
				continue
			}
			var read int
			valid[i], read = triviaTailLeavesRoom(code, points[i].Byte)
			budget -= read
		}
	}
	kept := points[:0]
	for i, p := range points {
		if valid[i] {
			kept = append(kept, p)
		}
	}
	return kept
}

// isXMLNameByte is a character of `[A-Za-z0-9\\._:-]`.
func isXMLNameByte(c byte) bool {
	return c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9' || c == '\\' || c == '.' || c == '_' || c == ':' || c == '-'
}

// isGuardSpace is a byte that may be white space to a `\s`: every byte of a multibyte character counts, which makes a check
// that reads past white space read further, and keeps it on the safe side.
func isGuardSpace(c byte) bool {
	return c == ' ' || c == '\t' || c == '\n' || c == '\r' || c == '\v' || c == '\f' || c >= 0x80
}

// triviaTailLeavesRoom reports whether the code from a point, past its white space, commas, braces and comments, holds
// guardTokens tokens that end in white space, and how many bytes it read. The commas and braces are the characters the
// object key pattern begins at, so an attempt at one of them reads what follows. A line comment reads to the end of its
// line. A block comment ends at the first `*/` that follows an odd run of stars (as the look-ahead's
// `(\*[^/]|[^*])*\*\/` reads it, which takes a pair of stars for one step); one closed after an even run is not known to end
// there, and a comment that does not end reaches the end of the code, so both leave no room.
func triviaTailLeavesRoom(code string, from int) (ok bool, read int) {
	i := from
	for i < len(code) {
		c := code[i]
		switch {
		case isGuardSpace(c) || c == ',' || c == '{':
			i++
		case c == '/' && i+1 < len(code) && code[i+1] == '/':
			end := strings.IndexByte(code[i:], '\n')
			if end < 0 {
				return false, len(code) - from
			}
			i += end
		case c == '/' && i+1 < len(code) && code[i+1] == '*':
			end := strings.Index(code[i+2:], "*/")
			if end < 0 {
				return false, len(code) - from
			}
			stars := 1
			for at := i + 2 + end - 1; at >= i+2 && code[at] == '*'; at-- {
				stars++
			}
			if stars%2 == 0 {
				return false, i + 2 + end - from
			}
			i += 2 + end + 2
		default:
			tokens, inToken := 0, false
			for ; i < len(code) && tokens < guardTokens; i++ {
				if isGuardSpace(code[i]) {
					if inToken {
						tokens++
					}
					inToken = false
				} else {
					inToken = true
				}
			}
			return tokens >= guardTokens, i - from
		}
	}
	return false, len(code) - from
}
