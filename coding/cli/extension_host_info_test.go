package cli

import (
	"testing"

	"github.com/MichaelKinsy/PiG/coding"
)

func TestExtensionHostInfoNamesPigsConfigurationTree(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("PIG_CODING_AGENT_DIR", dir)
	t.Setenv("PIG_USE_PI_DIRS", "")
	info := extensionHostInfo()
	if info.AgentDir != dir || info.ConfigDirName != ".pig" || info.Version != coding.UpstreamPackageVersion {
		t.Fatalf("host info = %+v, want agent dir %q, .pig and Pi %s", info, dir, coding.UpstreamPackageVersion)
	}
}

func TestExtensionHostInfoSharingPisTreeUsesPisNames(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("PIG_USE_PI_DIRS", "1")
	t.Setenv("PI_CODING_AGENT_DIR", dir)
	t.Setenv("PIG_CODING_AGENT_DIR", t.TempDir())
	info := extensionHostInfo()
	if info.AgentDir != dir || info.ConfigDirName != ".pi" {
		t.Fatalf("host info = %+v, want Pi's agent dir %q and .pi", info, dir)
	}
}
