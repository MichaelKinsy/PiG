//go:build parity

package journeys

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/MichaelKinsy/PiG/internal/coding/pigversion"
	"github.com/MichaelKinsy/PiG/internal/pioracle"
	"github.com/MichaelKinsy/PiG/internal/testenv"
	"github.com/MichaelKinsy/PiG/test/journeys/harness"
)

var (
	cfg      *harness.Config
	registry []*harness.Journey

	timingMu sync.Mutex
	timings  []journeyTiming
)

type journeyTiming struct {
	Journey   string   `json:"journey"`
	Seed      int64    `json:"seed"`
	PigSec    float64  `json:"pigSeconds"`
	PiSec     float64  `json:"piSeconds"`
	TotalSec  float64  `json:"totalSeconds"`
	PeakMemMB int64    `json:"pigPeakMemMB"`
	Failed    bool     `json:"failed"`
	Requests  int      `json:"providerRequests"`
	Frames    int      `json:"frames"`
	Touches   []string `json:"touches"`
}

// moduleRoot is the repository root, where changed files are listed from.
var moduleRoot string

func register(j *harness.Journey) { registry = append(registry, j) }

func TestMain(m *testing.M) {
	code := func() int {
		root, err := testenv.ModuleRootPath()
		if err != nil {
			fmt.Fprintln(os.Stderr, "module root:", err)
			return 1
		}
		moduleRoot = root
		cfg = &harness.Config{Scale: 1}
		if s := os.Getenv("JOURNEY_TIMEOUT_SCALE"); s != "" {
			cfg.Scale, _ = strconv.ParseFloat(s, 64)
		}
		cfg.ArtifactDir = os.Getenv("JOURNEY_ARTIFACTS")
		if cfg.ArtifactDir == "" {
			cfg.ArtifactDir = filepath.Join(root, "tmp", "journeys")
		}
		cfg.CoverDir = os.Getenv("JOURNEY_COVERDIR")
		variant := harness.Variant(os.Getenv("JOURNEY_PIG_VARIANT"))
		if variant == "" {
			variant = harness.Instrumented
		}
		if bin := os.Getenv("JOURNEY_PIG_BIN"); bin != "" {
			cfg.PigBin = bin
			cfg.Instrumented = variant == harness.Instrumented
		} else {
			dir, err := os.MkdirTemp("", "pigj-bin-")
			if err != nil {
				fmt.Fprintln(os.Stderr, err)
				return 1
			}
			defer func() { _ = os.RemoveAll(dir) }()
			cfg.PigBin = filepath.Join(dir, "pig")
			d, err := harness.BuildPig(root, cfg.PigBin, variant)
			if err != nil {
				fmt.Fprintln(os.Stderr, "build pig:", err)
				return 1
			}
			fmt.Fprintf(os.Stderr, "journeys: built %s pig in %s\n", variant, d.Round(time.Second))
			cfg.Instrumented = variant == harness.Instrumented
		}
		pi, err := resolvePi()
		if err != nil {
			fmt.Fprintln(os.Stderr, err)
			return 1
		}
		cfg.PiBin = pi
		code := m.Run()
		writeTimings(root)
		if os.Getenv("JOURNEY_STRICT_NORMALIZERS") != "" && os.Getenv("JOURNEY_ONLY") == "" && code == 0 {
			if stale := harness.StaleNormalizers(registry); len(stale) > 0 {
				fmt.Fprintf(os.Stderr, "stale journey normalizers (the difference no longer occurs; delete them):\n  %s\n", strings.Join(stale, "\n  "))
				return 1
			}
		}
		return code
	}()
	os.Exit(code)
}

func resolvePi() (string, error) {
	for _, env := range []string{"JOURNEY_PI_BIN", "PIG_PARITY_PI_BIN"} {
		if bin := os.Getenv(env); bin != "" {
			return checkPi(bin)
		}
	}
	// The comparator is Pi built from source at the pinned commit (`make parity-deps`), linked into extensions/sdk-ts; a commit pin has
	// no published release, so mise's install of the package version is used only on the explicit published opt-in of a release-tag pin.
	if dir, err := os.Getwd(); err == nil {
		for ; ; dir = filepath.Dir(dir) {
			if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
				if bin := filepath.Join(dir, "extensions", "sdk-ts", "node_modules", ".bin", "pi"); fileExists(bin) {
					return checkPi(bin)
				}
				break
			}
			if dir == filepath.Dir(dir) {
				break
			}
		}
	}
	if publishedPiAllowed() {
		//portlint:allow testisolation the real home is read only to find mise's installed Pi binary; each product runs in a sandbox with its own HOME (harness/sandbox.go)
		home, _ := os.UserHomeDir()
		for _, dir := range []string{"npm-earendil-works-pi-coding-agent", "npm-mariozechner-pi-coding-agent"} {
			bin := filepath.Join(home, ".local", "share", "mise", "installs", dir, pigversion.UpstreamPackageVersion, "bin", "pi")
			if fileExists(bin) {
				return checkPi(bin)
			}
		}
	}
	return "", fmt.Errorf("Pi %s is not built: run `make parity-deps` (builds Pi from source at pinned commit %s), or set JOURNEY_PI_BIN", pigversion.UpstreamVersion, pigversion.UpstreamCommit)
}

// publishedPiAllowed reports the run's opt-in to a published Pi, which only a release-tag pin has.
func publishedPiAllowed() bool {
	return os.Getenv("PIG_PARITY_PI_PUBLISHED") == "1" && pigversion.UpstreamVersion == pigversion.UpstreamPackageVersion
}

func fileExists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}

// checkPi accepts only the Pi built from the pinned commit (its build stamp names the commit), or on the published opt-in a Pi that
// reports the pinned package version.
func checkPi(bin string) (string, error) {
	if publishedPiAllowed() {
		out, err := exec.Command(bin, "--version").CombinedOutput()
		if err != nil {
			return "", fmt.Errorf("%s --version: %w", bin, err)
		}
		if got := strings.TrimSpace(string(out)); got != pigversion.UpstreamPackageVersion {
			return "", fmt.Errorf("%s is Pi %s, journeys require %s", bin, got, pigversion.UpstreamPackageVersion)
		}
		return bin, nil
	}
	if err := pioracle.CheckBin(bin); err != nil {
		return "", err
	}
	return bin, nil
}

func writeTimings(root string) {
	timingMu.Lock()
	defer timingMu.Unlock()
	if len(timings) == 0 {
		return
	}
	path := os.Getenv("JOURNEY_TIMING_OUT")
	if path == "" {
		path = filepath.Join(root, "tmp", "journeys", "timing.json")
	}
	_ = os.MkdirAll(filepath.Dir(path), 0o755)
	slices.SortFunc(timings, func(a, b journeyTiming) int { return strings.Compare(a.Journey, b.Journey) })
	b, _ := json.MarshalIndent(timings, "", " ")
	_ = os.WriteFile(path, b, 0o644)
}

func seedFor(j *harness.Journey) int64 {
	switch s := os.Getenv("JOURNEY_SEED"); s {
	case "":
		return 1
	case "random":
		return time.Now().UnixNano() % 1_000_000_000
	default:
		n, err := strconv.ParseInt(s, 10, 64)
		if err != nil {
			panic("JOURNEY_SEED must be a number or \"random\": " + s)
		}
		return n
	}
}

func parallelism() int {
	if s := os.Getenv("JOURNEY_PARALLEL"); s != "" {
		if n, err := strconv.Atoi(s); err == nil && n > 0 {
			return n
		}
	}
	return 2
}

func shrinkBudget() int {
	if s := os.Getenv("JOURNEY_SHRINK_BUDGET"); s != "" {
		n, _ := strconv.Atoi(s)
		return n
	}
	return 10
}

var slots chan struct{}
var slotsOnce sync.Once

func TestJourneys(t *testing.T) {
	slotsOnce.Do(func() { slots = make(chan struct{}, parallelism()) })
	only := os.Getenv("JOURNEY_ONLY")
	var selected []string
	if base := os.Getenv("JOURNEY_CHANGED_BASE"); base != "" && only == "" {
		changed, err := harness.ChangedFiles(moduleRoot, base)
		if err != nil {
			t.Fatalf("select changed journeys: %v", err)
		}
		selected = harness.Select(registry, changed)
		t.Logf("changed since %s selects %d of %d journeys: %v", base, len(selected), len(registry), selected)
		if len(selected) == 0 {
			return
		}
	}
	for _, j := range registry {
		if only != "" && !slices.Contains(strings.Split(only, ","), j.Name) {
			continue
		}
		if selected != nil && !slices.Contains(selected, j.Name) {
			continue
		}
		t.Run(j.Name, func(t *testing.T) {
			t.Parallel()
			slots <- struct{}{}
			defer func() { <-slots }()
			runJourney(t, j)
		})
	}
}

func newRoot(t *testing.T) string {
	t.Helper()
	dir, err := os.MkdirTemp("", "pj-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if !t.Failed() || os.Getenv("JOURNEY_KEEP") == "" {
			_ = os.RemoveAll(dir)
		}
	})
	return dir
}

func runJourney(t *testing.T, j *harness.Journey) {
	seed := seedFor(j)
	plan := harness.MakePlan(j, seed)
	o, err := harness.RunJourney(cfg, j, plan, newRoot(t))
	if err != nil {
		t.Fatal(err)
	}
	record(j, o)
	if !o.Failed() {
		return
	}
	shrunk := plan
	runs := 0
	if budget := shrinkBudget(); budget > 0 && len(plan.Pool) > 1 {
		shrunk, runs = harness.Shrink(cfg, j, o, func() string { return newRoot(t) }, budget)
	}
	var b strings.Builder
	fmt.Fprintf(&b, "journey %s failed (%s)\n", j.Name, strings.Join(o.Kinds(), ", "))
	for _, f := range o.Failures {
		fmt.Fprintf(&b, "\n--- %s\n%s\n", f.Kind, f.Msg)
	}
	fmt.Fprintf(&b, "\nplan:    %s\nshrunk:  %s (after %d extra runs)\nreplay:  JOURNEY_SEED=%d JOURNEY_ONLY=%s go test -tags parity ./test/journeys -run TestJourneys\nevidence: %s\n",
		plan, shrunk, runs, seed, j.Name, filepath.Join(cfg.ArtifactDir, fmt.Sprintf("%s-seed%d", j.Name, seed)))
	t.Fatal(b.String())
}

func record(j *harness.Journey, o *harness.Outcome) {
	tm := journeyTiming{Journey: j.Name, Seed: o.Plan.Seed, TotalSec: o.Elapsed.Seconds(), Failed: o.Failed(), Touches: j.Touches}
	tm.PigSec = o.Pig.Elapsed.Seconds()
	tm.PeakMemMB = o.Pig.PeakMem() >> 20
	tm.Requests = len(o.Pig.P.Requests())
	tm.Frames = len(o.Pig.Snaps)
	if o.Pi != nil {
		tm.PiSec = o.Pi.Elapsed.Seconds()
	}
	timingMu.Lock()
	timings = append(timings, tm)
	timingMu.Unlock()
}
