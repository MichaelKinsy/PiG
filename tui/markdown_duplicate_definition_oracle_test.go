package tui

import "testing"

// marked drops a link definition whose label is already defined without pushing a token, so a heading, rule, fence, quote or LaTeX block followed only by such definitions is the last token and adds no spacing row of its own.
// Pi source: marked 18 Lexer.blockTokens (`this.tokens.links[r.tag] || (this.tokens.links[r.tag] = ..., t.push(r))`) and packages/tui/src/components/markdown.ts renderToken (spacing after a block depends on the next token).
func TestMarkdownDuplicateDefinitionAddsNoTokenMatchesPi(t *testing.T) {
	compareMarkdownWithPi(t, []string{
		"[a]: x\n# H\n[a]: y", "[a]: x\n---\n[a]: y", "[a]: x\n```\n```\n[a]: y", "[a]: x\n```\n```\n[a]: y\n\nq", "[a]: x\n\n$$\nx\n$$\n[a]: y", "[a]: x\n\n$$\nx\n$$\n[a]: y\nq",
		"[a]: x\n# H\n[a]: y\n[a]: z\nq", "[a]: x\n# H\n[a]: y\n[b]: z\nq", "[a]: x\n# H\n[a]: y\n\n", "[a]: x\n---\n[a]: y\n---\n[a]: z", "[ref]: http://x.y\n``` js\n```\n[ref]: http://x.y",
		// A reference before its definition makes the document lex twice, the second time with every definition already known: only a label an earlier part of that pass defined is a repeat.
		"\\(a\\) and [b]\n```\n```\n[r]: x", "[b]\n```\n```\n[r]: x\n[r]: y", "[b]\n# H\n[b]: y\n[b]: z", "[b]\n---\n[r]: x",
		"[a]: x\n> ```\n[a]: y", "[a]: x\n> ```\n> c\n> ```\n[a]: y\n[a]: z", "[a]: x\n> ```\n> c\n> ```\n[a]: y\nq", "[a]: x\n> ```\n> c\n> ```\n[b]: y", "[a]: x\n> ```\n> c\n> ```\n[a]: y\n\n",
		"[a]: x\n\n[a]: y\n\nq", "[a]: x\n\n> q\n\n[a]: y", "[a]: x\n\n```\nc\n```\n\n[a]: y",
	})
}
