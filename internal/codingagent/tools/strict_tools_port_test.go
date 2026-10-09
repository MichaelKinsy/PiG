package tools

import (
	"os"
	"reflect"
	"testing"
)

func TestBuiltinStrictSamplingEnvironmentPort(t *testing.T) {
	// .upstream/v0.87.1/packages/coding-agent/test/builtin-tool-strict-mode.test.ts:18 (undefined, "0", "1").
	for _, value := range []string{"undefined", "0", "1"} {
		t.Run("prefers strict sampling with PI_EXPERIMENTAL="+value, func(t *testing.T) {
			t.Setenv("PI_EXPERIMENTAL", value)
			if value == "undefined" {
				if err := os.Unsetenv("PI_EXPERIMENTAL"); err != nil {
					t.Fatal(err)
				}
			}
			TestBuiltinToolsPreferStrictSampling(t)
			definitions := BuiltinToolSchemas()
			for i, tool := range CreateAllTools(t.TempDir(), nil) {
				if !reflect.DeepEqual(tool.Schema().ConstrainedSampling, definitions[i].ConstrainedSampling) {
					t.Fatalf("%s tool and definition strict metadata differ", tool.Name())
				}
			}
		})
	}
}
