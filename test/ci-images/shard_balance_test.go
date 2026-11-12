package ciimages

import (
	"os/exec"
	"path/filepath"
	"testing"
)

// The Linux conformance shards are packed by the durations recorded in automation/ci/test-shard-timings.txt, and
// shard-balance.py fails when the recorded pins are not what those durations pack to or when a shard's predicted time leaves
// less than 40% of go test's 10-minute timeout. Its unit tests include that check on this repository's files.
func TestShardBalanceTool(t *testing.T) {
	command := exec.CommandContext(t.Context(), hostPython(), "-B", filepath.Join(repoRoot(t), "automation", "ci", "shard_balance_test.py"))
	if output, err := command.CombinedOutput(); err != nil {
		t.Fatalf("shard-balance tests: %v\n%s", err, output)
	}
}
