package ciimages

import (
	"os/exec"
	"path/filepath"
	"testing"
)

func TestDependencyRecipeContracts(t *testing.T) {
	for _, script := range []string{"npm_locked_test.py", "coverage_recipe_test.py"} {
		t.Run(script, func(t *testing.T) {
			// The coverage recipe test runs `make coverage`.
			if script == "coverage_recipe_test.py" {
				if _, err := exec.LookPath("make"); err != nil {
					t.Skip("make is not on PATH")
				}
			}
			command := exec.Command("python3", filepath.Join(repoRoot(t), "automation", "ci", script))
			if output, err := command.CombinedOutput(); err != nil {
				t.Fatalf("recipe tests: %v\n%s", err, output)
			}
		})
	}
}
