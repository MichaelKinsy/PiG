package coding

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/internal/testenv"
)

// semverCore parses the major, minor and patch numbers of a release version, ignoring a +build suffix.
func semverCore(t *testing.T, version string) [3]int {
	t.Helper()
	core, _, _ := strings.Cut(version, "+")
	parts := strings.Split(core, ".")
	var numbers [3]int
	if len(parts) != 3 {
		t.Fatalf("version %q is not major.minor.patch", version)
	}
	for i, part := range parts {
		n, err := strconv.Atoi(part)
		if err != nil || n < 0 {
			t.Fatalf("version %q is not major.minor.patch", version)
		}
		numbers[i] = n
	}
	return numbers
}

// UpstreamReviewedVersion is a hand-kept constant that make reads (UPSTREAM_REVIEWED_VERSION in the Makefile) to name the ledgers
// the pin's leap covers. It may not name a release newer than the pin, and every ledger the leap reads must exist: the delta from
// it to the pin, its interface and CLI inventories, its behavior-input mapping, and the pin's upstream-sync manifest.
func TestUpstreamReviewedVersionIsNotAfterThePinAndItsLedgersExist(t *testing.T) {
	reviewed := semverCore(t, UpstreamReviewedVersion)
	pin := semverCore(t, UpstreamPackageVersion)
	for i := range reviewed {
		if reviewed[i] < pin[i] {
			break
		}
		if reviewed[i] > pin[i] {
			t.Fatalf("UpstreamReviewedVersion %s is later than the pin %s", UpstreamReviewedVersion, UpstreamPackageVersion)
		}
	}
	interfaces := filepath.Join(testenv.ModuleRoot(t), "test", "parity", "interfaces")
	for _, name := range []string{
		"upstream-v" + UpstreamReviewedVersion + ".json",
		"cli-v" + UpstreamReviewedVersion + ".json",
		"behavior-input-mapping-v" + UpstreamReviewedVersion + ".json",
		"delta-v" + UpstreamReviewedVersion + "-v" + UpstreamVersion + ".json",
		filepath.Join("..", "upstream-sync", "v"+UpstreamVersion+".toml"),
	} {
		if _, err := os.Stat(filepath.Join(interfaces, name)); err != nil {
			t.Errorf("UpstreamReviewedVersion %s names a ledger that is missing: %v", UpstreamReviewedVersion, err)
		}
	}
	data, err := os.ReadFile(filepath.Join(interfaces, "delta-v"+UpstreamReviewedVersion+"-v"+UpstreamVersion+".json"))
	if err != nil {
		return
	}
	var delta struct{ From, To string }
	if err := json.Unmarshal(data, &delta); err != nil {
		t.Fatal(err)
	}
	if delta.From != UpstreamReviewedVersion || delta.To != UpstreamVersion {
		t.Errorf("the delta ledger covers %s to %s, want %s to %s", delta.From, delta.To, UpstreamReviewedVersion, UpstreamVersion)
	}
}
