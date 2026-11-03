package tui

import "testing"

// markdownKnownDifferenceProbes are minimal documents on which PiG still renders other rows than pi-tui. Each is the smallest document the random-corpus sweep produced for a shape of marked's block lexer that the
// line-based block loop does not reproduce; markdownRandomKnownDifferences lists the documents of the random corpus they stand for. A difference that is fixed is removed from both.
var markdownKnownDifferenceProbes = []string{
	"-\t *\n  d",
	">>a\n>\t\n|",
	">>*\n>b\n$",
	">>>\n>\t\n]",
	">>]\n>\t\n$",
	">*\nb\n\t>*",
	">-\n>\tb \n]",
	"a\n* \n-\n\\[",
	"- a\n$$\n\t*",
	"~\n\\[\na\n-|-",
	">* *  \n  =",
	">* >  \n  `",
	">b\nb\nz\n-|-",
	">* *\n>_ \ns",
	">1. *  \n\te",
	">b\nt\ns||\n-|",
	">`\n    `\n>\t",
	"-     a\n  -",
	">-\n>   z \n$",
	">-\n>z\n    ;",
	">a\n    b\n>\t",
	">*\t>\t\n    a",
	"z\n\\[\nd||\n-|",
	"+\n  >\t\n   \n+",
	">z\n    b\n>\n>",
	">a\n    b\n>  ",
	"b\n    |\n-\n$$",
	"b\n    _\n-\n\\[",
	">\\[\n>- \\\n$\n=",
	">>a\n>  \n🙂",
	"**\n    |\n-\n\\[",
	"**\n    `\n-\n$$",
	"-\n  >  \n   \n-",
	"><v>\n>)\n    >",
	"*\n  d||\n-|-\n  *",
	"- [ ] z\n      =",
	">a\n    ```\n>b\na",
	"+ `\n\n      `\n  *",
	">- b\n$\n>     *\n]",
	">   - )\n***x***\n=",
}

// markdownRandomKnownDifferences are the indexes of the documents of markdownRandomDocs(20260101, 12000) that differ from pi-tui in markdownRandomProbes.
var markdownRandomKnownDifferences = []int{
	81, 693, 918, 1229, 1395, 1625, 1647, 1755, 1770, 2052, 2574, 2703, 2941, 3002, 3099, 3131,
	3153, 3192, 3323, 3510, 3611, 3780, 3918, 3943, 4124, 4173, 4545, 4665, 5623, 5853, 6178, 6593,
	6885, 6981, 7155, 7299, 7365, 7448, 7476, 7783, 7809, 7936, 8122, 8193, 8425, 8631, 8787, 8835,
	8945, 9108, 9207, 9228, 9232, 9284, 9429, 9491, 9681, 9841, 10153, 10382, 10419, 10662, 10782, 10827,
	11008, 11017, 11804, 11850,
}

// TestMarkdownKnownDifferencesStillDiffer requires every probe of markdownKnownDifferenceProbes to differ from pi-tui at one of the widths of the random corpus, padded or not, so that fixing a difference makes
// the test fail until the probe leaves the list.
func TestMarkdownKnownDifferencesStillDiffer(t *testing.T) {
	var probes []markdownContractProbe
	for _, doc := range markdownKnownDifferenceProbes {
		for padding := range 2 {
			probes = append(probes, markdownContractProbe{Text: doc, PaddingX: padding, Theme: "tagged", Ops: markdownContractRenderOps(5, 11, 24, 80)})
		}
	}
	differences, _ := markdownContractDifferences(t, probes)
	for i, doc := range markdownKnownDifferenceProbes {
		_, plain := differences[2*i]
		_, padded := differences[2*i+1]
		if !plain && !padded {
			t.Errorf("%q matches Pi now: remove it from markdownKnownDifferenceProbes", doc)
		}
	}
}
