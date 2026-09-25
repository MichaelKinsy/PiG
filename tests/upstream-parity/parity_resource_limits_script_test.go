package parity

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/internal/testenv"
)

func TestParityResourcePigletsBoundConcurrentPigPiPairs(t *testing.T) {
	root := pigRepoRoot(t)
	script := filepath.Join(root, "automation", "ci", "parity-resource-limits.sh")
	want := map[string]string{
		"low":    "2 process=2,process-ext=1,tmux=2,tmux-ext=1,ht=1,rpc=1",
		"medium": "3 process=3,process-ext=1,tmux=3,tmux-ext=1,ht=2,rpc=2",
		"high":   "4 process=4,process-ext=1,tmux=4,tmux-ext=1,ht=2,rpc=3",
	}
	for piglet, expected := range want {
		t.Run(piglet, func(t *testing.T) {
			cmd := exec.Command(testenv.Bash(t), script)
			cmd.Env = append(os.Environ(), "PIG_PARITY_PROFILE="+piglet)
			output, err := cmd.CombinedOutput()
			if err != nil {
				t.Fatalf("piglet failed: %v\n%s", err, output)
			}
			if got := strings.TrimSpace(string(output)); got != expected {
				t.Fatalf("piglet = %q, want %q", got, expected)
			}
		})
	}
}
