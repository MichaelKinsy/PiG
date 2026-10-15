package codemode

import (
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/internal/testenv"
)

// Pi packages/codemode/src/runtime/inline.ts:39 DEFAULT_INTERRUPT_BUDGET = 100_000 and :42 INLINE_MAX_STACK_SIZE = 256 * 1024: the
// interrupt polls an in-thread execution may use, and the stack guard of a VM that runs on the caller's stack. When the
// current upstream mirror is checked out, the values are also read from Pi's source.
func TestInlineConstantsArePis(t *testing.T) {
	if DefaultInterruptBudget != 100_000 || inlineMaxStackSize != 256*1024 {
		t.Fatalf("DefaultInterruptBudget = %d, inlineMaxStackSize = %d; Pi inline.ts: 100000 and 262144", DefaultInterruptBudget, inlineMaxStackSize)
	}
	source, err := os.ReadFile(filepath.Join(testenv.ModuleRoot(t), ".upstream", "current", "packages", "codemode", "src", "runtime", "inline.ts"))
	if err != nil {
		t.Logf("no upstream mirror: %v", err)
		return
	}
	read := func(name string) int {
		m := regexp.MustCompile(`export const ` + name + ` = ([0-9_]+)(?: \* ([0-9_]+))?;`).FindSubmatch(source)
		if m == nil {
			t.Fatalf("Pi inline.ts declares no %s", name)
		}
		value, err := strconv.Atoi(strings.ReplaceAll(string(m[1]), "_", ""))
		if err != nil {
			t.Fatal(err)
		}
		if len(m[2]) > 0 {
			factor, err := strconv.Atoi(strings.ReplaceAll(string(m[2]), "_", ""))
			if err != nil {
				t.Fatal(err)
			}
			value *= factor
		}
		return value
	}
	if got := read("DEFAULT_INTERRUPT_BUDGET"); got != DefaultInterruptBudget {
		t.Fatalf("Pi DEFAULT_INTERRUPT_BUDGET %d, Go %d", got, DefaultInterruptBudget)
	}
	if got := read("INLINE_MAX_STACK_SIZE"); got != inlineMaxStackSize {
		t.Fatalf("Pi INLINE_MAX_STACK_SIZE %d, Go %d", got, inlineMaxStackSize)
	}
}
