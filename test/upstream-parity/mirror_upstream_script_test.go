package parity

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/internal/testenv"
)

func TestMirrorUpstreamWritesREADMEWithoutCommandSubstitution(t *testing.T) {
	root := testenv.ModuleRoot(t)
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
