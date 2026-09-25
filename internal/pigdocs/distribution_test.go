package pigdocs

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestPigletDocsPreserveRemoteAddAndPublishedBinarySurface(t *testing.T) {
	for _, path := range []string{"content/piglets.md", "../../docs/site/docs/piglets.md"} {
		t.Run(path, func(t *testing.T) {
			data, err := os.ReadFile(filepath.FromSlash(path))
			if err != nil {
				t.Fatal(err)
			}
			text := string(data)
			for _, required := range []string{
				"pig piglet add npm:@acme/review@^1.0.0",
				"pig piglet add git:https://github.com/acme/review.git@v2.0.0",
				"<name>.origin.json", "`pig.piglet`", "`PIG_OFFLINE`", "`PI_OFFLINE`",
				"--to github --repo", "--yes",
			} {
				if !strings.Contains(text, required) {
					t.Errorf("missing shipped distribution guidance %q", required)
				}
			}
			if strings.Contains(text, "pig piglet publish <name> --to npm|github") {
				t.Error("GitHub publication is still listed as planned")
			}
		})
	}
}

func TestPigletDistributionSummariesDoNotCallShippedCommandsPlanned(t *testing.T) {
	for _, path := range []string{"../../docs/pig-piglet-spec.md", "../../docs/site/docs/packages.md"} {
		t.Run(path, func(t *testing.T) {
			data, err := os.ReadFile(filepath.FromSlash(path))
			if err != nil {
				t.Fatal(err)
			}
			text := string(data)
			for _, stale := range []string{
				"Remote npm/Git registration, publication, pull, Piglet-specific update, locked records, and Piglet Image production are planned",
				"The publish, pull, and Piglet-specific update commands are not available",
				"### Planned (not in this release): npm and Git Piglet registration",
				"pig piglet --help` does not list them yet",
			} {
				if strings.Contains(text, stale) {
					t.Errorf("stale distribution claim %q", stale)
				}
			}
		})
	}
}
