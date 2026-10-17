package tui

import "testing"

// A backtick fence indented n spaces removes n units from each code line that has at least that much leading whitespace (marked's indentCodeCompensation; a tilde fence does not), and the closing fence is the opener's own run, then any run of '~' and '`', then spaces only.
func TestMarkdownFenceIndentAndCloseMatchPi(t *testing.T) {
	var docs []string
	for _, open := range []string{"```", "  ```", "   ```", " ```x", "~~~", "  ~~~", "   ~~~ js", "````", "  ````"} {
		for _, body := range []string{"c", "  c", "   c", "    c", " c", "\tc", "\u00a0c", "  \u00a0c", " \n  c\n   c", "c\n\n  d"} {
			docs = append(docs, open+"\n"+body+"\n```", open+"\n"+body+"\n"+open+"\n")
		}
	}
	for _, closing := range []string{"```", "``` ", "```\t", "```\u00a0", "``` x", "````", "```~", "```~~`", "``~", "~~~", "  ```", "   ```", "    ```", "```  \u00a0", "``` ``` "} {
		docs = append(docs, "```\nc\n"+closing+"\nd", "```\nc\n"+closing, "````\nc\n"+closing+"\nd", "~~~\nc\n"+closing+"\nd", "~~~~\nc\n"+closing+"\nd", "```\nc\n"+closing+"\n")
	}
	// An unclosed fence ends at the text's end; a last line that is a shorter run of the fence is no code line when the opener is not indented.
	docs = append(docs, "````\nc\n```", " ````\nc\n```", "  ````\nc\n```", "   ````\nc\n```", "  ````\nc\n  ```", "  ````\nc\n```\n", "  ````\nc\n``", "  ````\nc\n`", "  ````\nc\n```x", "  ~~~~\nc\n~~~", " ~~~~\nc\n~~~", "~~~~\nc\n~~~", "  `````\nc\n````", "  ```\nc\n``", "```\nc\n``", "````\nc\n  ```")
	compareMarkdownWithPi(t, docs)
}

// markdown.ts trimPartialClosingFences drops a partial closing fence only from the last token of the text and, through it, the last item of a list or the last token of a quote; a code token ends a quote that a lazy line would continue (Tokenizer.blockquote: `lastToken?.type === 'code'`).
// Pi source: packages/tui/src/components/markdown.ts, marked 18 Tokenizer.blockquote.
func TestMarkdownPartialClosingFenceInContainersMatchesPi(t *testing.T) {
	var docs []string
	for _, pre := range []string{"- ", "1. ", "> ", "- > ", "> - ", "  - ", "- a\n- "} {
		cont := "  "
		switch pre {
		case "> ":
			cont = "> "
		case "> - ":
			cont = ">   "
		case "- > ":
			cont = "  > "
		case "1. ":
			cont = "   "
		}
		for _, open := range []string{"```", "  ```", "~~~", "````"} {
			if pre == "> - " && open == "  ```" {
				continue
			}
			for _, closer := range []string{"`", "``", "```", ""} {
				d := pre + open + "\n" + cont + "c"
				if closer != "" {
					d += "\n" + cont + closer
				}
				for _, tail := range []string{"", "\n", "\n\n", "\n" + cont + "after", "\nafter", "\n- b", "\n> q"} {
					docs = append(docs, d+tail)
				}
			}
		}
	}
	// A line indented four spaces inside a quoted paragraph continues the paragraph, so the quote does not end in code and a lazy line still joins it; after a blank quoted line the same indent is indented code, which a lazy line cannot continue.
	docs = append(docs, "> p\n>     q\nlazy", "> p\n>\n>     code\nlazy", "> - a\n>     b\nlazy", ">     code\nlazy")
	compareMarkdownWithPi(t, docs)
}

// A blank line inside a list item's fence ends the item when a line under the content indent does not follow it (Tokenizer.ts list: `if (blankLine) break`), and the blank line before a nested list is a space token that renders an empty row.
// Pi source: packages/tui/src/components/markdown.ts case "space", marked 18 Tokenizer.list.
func TestMarkdownListItemBlankLinesMatchPi(t *testing.T) {
	docs := []string{
		"- a\n\n  - n", "- a\n\n     - n", "1. a\n\n    - n", "- a\n\n  b\n\n    - n", "- a\n  b\n\n    - n", "- a\n    - n", "- a\n\n\n  - n", "- a\n\n\n\n  - n", "- a\n\n  - n\n\n  - m", "- a\n \n  - n", "- a\n- b\n\n  - n",
	}
	for _, pre := range []string{"- ", "1. ", "- a\n- "} {
		cont := "  "
		if pre == "1. " {
			cont = "   "
		}
		for _, open := range []string{"```", "~~~"} {
			for _, mid := range []string{"", "\n" + cont + "c"} {
				for _, closer := range []string{"", "\n" + cont + open} {
					for _, gap := range []string{"\n", "\n\n", "\n \n"} {
						for _, tail := range []string{"after", cont + "after", "- b", "> q", cont + "```"} {
							docs = append(docs, pre+open+mid+closer+gap+tail, pre+open+mid+closer+gap+tail+"\nlazy")
						}
					}
				}
			}
		}
	}
	compareMarkdownWithPi(t, docs)
}
