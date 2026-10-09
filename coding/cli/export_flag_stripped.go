//go:build pig_strip_export_html

package cli

import "github.com/MichaelKinsy/PiG/internal/pigstrip"

// pig additive (D92): this Piglet Binary compiled out HTML session export.
func init() { pigstrip.Strip(pigstrip.ListFeatures, pigstrip.ExportHTML) }

// runExportFlag reports that `--export` is unavailable in this build and exits 1.
func runExportFlag(string, string) {
	exitProcess(reportStrippedFeature("HTML export", pigstrip.ExportHTML, ""))
}
