//go:build !pig_strip_export_html

package cli

import (
	"fmt"

	"github.com/MichaelKinsy/PiG/internal/codingagent"
	"github.com/MichaelKinsy/PiG/internal/pigstrip"
)

// runExportFlag is `pig --export <session.jsonl> [output.html]`: it writes
// the session file as HTML and exits. Mirrors upstream main.ts:459-466.
//
// This file and its internal/codingagent/export import are the boundary a
// Piglet Binary compiles out with the pig_strip_export_html tag.
func runExportFlag(sessionFile, outputPath string) {
	// pig additive (D92): a Piglet that strips export-html fails as its Binary does.
	if pigstrip.Has(pigstrip.ListFeatures, pigstrip.ExportHTML) {
		exitProcess(reportStrippedFeature("HTML export", pigstrip.ExportHTML, ""))
	}
	result, err := codingagent.ExportFileToHTML(sessionFile, outputPath)
	if err != nil {
		printCLIError("%v", err)
		exitProcess(1)
	}
	fmt.Printf("Exported to: %s\n", result) // upstream: main.ts console.log(`Exported to: ${result}`)
	exitProcess(0)
}
