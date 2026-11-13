package ai

import "strings"

// d2ProductNameTexts maps the fragments of Pi's messages that name the product to PiG's (D2, "Product name in messages",
// docs/parity/divergences/D2.md). A test that states Pi's text compares with PiG's through d2Text, so the one difference
// between the two is this table.
var d2ProductNameTexts = [][2]string{
	{"in another pi session or by the Codex CLI", "in another pig session or by the Codex CLI"},
}

// d2Text is Pi's text with each mapped fragment replaced by PiG's. A text that holds no mapped fragment is a mistake in the
// test: it panics.
func d2Text(piText string) string {
	pigText := piText
	for _, pair := range d2ProductNameTexts {
		pigText = strings.ReplaceAll(pigText, pair[0], pair[1])
	}
	if pigText == piText {
		panic("d2Text: no mapped product-name fragment in " + piText)
	}
	return pigText
}
