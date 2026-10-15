package packagecontent

import (
	"context"
	"fmt"
	"os/exec"
	"strconv"
	"strings"
	"testing"
	"time"
)

// jsMaxArrayLength is V8's limit, not a value any Pi or dependency file spells, so this test measures it in the installed Node: Array.prototype.push of a dense array throws "RangeError: Invalid array length" once the array holds jsMaxArrayLength elements. brace-expansion pushes each sequence item, so braceExpand must accept a sequence of exactly that many items and fail with V8's message for one more. The probe holds about 3.4 GB in Node for about 6 s; -short skips it.
func TestJSMaxArrayLengthMatchesNode(t *testing.T) {
	if testing.Short() {
		t.Skip("the Node probe allocates about 3.4 GB")
	}
	node, err := exec.LookPath("node")
	if err != nil {
		t.Skip("node is not installed")
	}
	// node --eval starts no child process; the context kills it if the test ends first.
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Minute)
	defer cancel()
	cmd := exec.CommandContext(ctx, node, "--max-old-space-size=6144", "--eval", `
const a = [];
try {
	for (let i = 0; i <= 2 ** 32; i++) a.push(0);
	console.log("none");
} catch (e) {
	console.log(a.length + " " + e.constructor.name + ": " + e.message);
}`)
	output, err := cmd.Output()
	if err != nil {
		t.Fatalf("node probe: %v", err)
	}
	length, message, ok := strings.Cut(strings.TrimSpace(string(output)), " ")
	limit, convErr := strconv.Atoi(length)
	if !ok || convErr != nil || message != "RangeError: "+errInvalidArrayLength.Error() {
		t.Fatalf("node probe printed %q, want <length> RangeError: %s", output, errInvalidArrayLength)
	}
	if limit != jsMaxArrayLength {
		t.Fatalf("V8 push throws at length %d, jsMaxArrayLength is %d", limit, jsMaxArrayLength)
	}
	if got, err := braceExpand(fmt.Sprintf("x{1..%d}", limit)); err != nil || len(got) != braceExpansionMax {
		t.Errorf("braceExpand of %d items: %d expansions, %v; want %d", limit, len(got), err, braceExpansionMax)
	}
	if _, err := braceExpand(fmt.Sprintf("x{1..%d}", limit+1)); err == nil || err.Error() != errInvalidArrayLength.Error() {
		t.Errorf("braceExpand of %d items fails with %v, want %s", limit+1, err, errInvalidArrayLength)
	}
}
