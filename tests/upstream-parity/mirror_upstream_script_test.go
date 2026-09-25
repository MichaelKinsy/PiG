package parity

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestMirrorUpstreamWritesREADMEWithoutCommandSubstitution(t *testing.T) {
	_, thisFile, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("runtime.Caller failed")
	}
	root := filepath.Join(filepath.Dir(thisFile), "..", "..")
	data, err := os.ReadFile(filepath.Join(root, "automation", "gen", "mirror-upstream.sh"))
	if err != nil {
		t.Fatal(err)
	}
	script := string(data)
	if strings.Contains(script, "cat > \"$READMEPATH\" <<EOF") {
		t.Fatal("README heredoc permits Markdown backticks to execute as shell substitutions")
	}
	if !strings.Contains(script, "cat > \"$READMEPATH\" <<'EOF'") {
		t.Fatal("README heredoc is not literal")
	}
}
