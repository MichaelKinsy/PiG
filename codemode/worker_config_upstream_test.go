package codemode_test

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/codemode"
)

// Equivalence port of packages/coding-agent/test/codemode-worker-config.test.ts (Pi 1.0.0, #10204).
// resolveCodemodeWorkerSpecifier picks the codemode worker for each Pi distribution: a Bun compiled executable, the
// bundled Node module, or an unbundled package. A wrong choice breaks codemode in that distribution only. PiG runs
// QuickJS on wazero in process, so its concern is the module instead of a worker: every PiG distribution (go
// install or go test from source, the release archive binary, and the binary the @pi-in-go/pig npm launcher starts)
// loads the same checksum-pinned module embedded in the executable, independent of the working directory and of any
// file on disk.

const (
	codemodeProbeEnv     = "PIG_CODEMODE_DISTRIBUTION_PROBE"
	quickJSWasmSHA256    = "d4c9375f2b1ca4dc95f72c8aa2982a7a9951ac8011490d79c6582df732b4bbd9"
	codemodeProbePrefix  = "CODEMODE-PROBE "
	codemodeWorkerConfig = "packages/coding-agent/test/codemode-worker-config.test.ts"
)

type codemodeProbeReport struct {
	CWD    string          `json:"cwd"`
	SHA256 string          `json:"sha256"`
	OK     bool            `json:"ok"`
	Value  json.RawMessage `json:"value"`
	Error  string          `json:"error,omitempty"`
}

// TestCodemodeDistributionProbe runs one script with the production default options in this process. It does
// nothing unless TestCodemodeWorkerConfigUpstream started it.
func TestCodemodeDistributionProbe(t *testing.T) {
	if os.Getenv(codemodeProbeEnv) == "" {
		t.Skip("probe child only")
	}
	fmt.Println(codemodeProbePrefix + string(codemodeProbe(t)))
}

func codemodeProbe(t *testing.T) []byte {
	t.Helper()
	cwd, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	sum := sha256.Sum256(codemode.QuickJSWasm())
	report := codemodeProbeReport{CWD: cwd, SHA256: hex.EncodeToString(sum[:])}
	// The built-in codemode tool passes no Wasm loader (coding/extension/builtin/codemode/execute.go), which selects
	// the embedded module.
	sandbox, err := codemode.NewSandbox(codemode.SandboxOptions{TimeoutMs: 10_000})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = sandbox.Close() }()
	result, err := sandbox.Execute(t.Context(), "return [6 * 7, typeof store]", codemode.ExecuteOptions{})
	if err != nil {
		t.Fatal(err)
	}
	report.OK, report.Value = result.OK, result.Value
	if result.Error != nil {
		report.Error = result.Error.Message
	}
	encoded, err := json.Marshal(report)
	if err != nil {
		t.Fatal(err)
	}
	return encoded
}

// runCodemodeDistribution copies the test executable to binary, starts it in cwd with an environment that names no
// source tree, and returns its report.
func runCodemodeDistribution(t *testing.T, binary, cwd string) codemodeProbeReport {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(binary), 0o755); err != nil {
		t.Fatal(err)
	}
	copyExecutable(t, os.Args[0], binary)
	home := t.TempDir()
	cmd := exec.Command(binary, "-test.run=^TestCodemodeDistributionProbe$", "-test.count=1")
	cmd.Dir = cwd
	cmd.Env = []string{codemodeProbeEnv + "=1", "HOME=" + home, "USERPROFILE=" + home, "TMPDIR=" + home, "TEMP=" + home, "TMP=" + home, "PATH=" + filepath.Dir(binary)}
	if root := os.Getenv("SYSTEMROOT"); root != "" {
		cmd.Env = append(cmd.Env, "SYSTEMROOT="+root)
	}
	output, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("%s: %v\n%s", binary, err, output)
	}
	for line := range strings.SplitSeq(string(output), "\n") {
		if encoded, ok := strings.CutPrefix(strings.TrimRight(line, "\r"), codemodeProbePrefix); ok {
			var report codemodeProbeReport
			if err := json.Unmarshal([]byte(encoded), &report); err != nil {
				t.Fatal(err)
			}
			return report
		}
	}
	t.Fatalf("%s printed no report:\n%s", binary, output)
	return codemodeProbeReport{}
}

func copyExecutable(t *testing.T, from, to string) {
	t.Helper()
	source, err := os.Open(from)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = source.Close() }()
	target, err := os.OpenFile(to, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o755)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := io.Copy(target, source); err != nil {
		t.Fatal(err)
	}
	if err := target.Close(); err != nil {
		t.Fatal(err)
	}
}

func wantEmbeddedModule(t *testing.T, report codemodeProbeReport, cwd string) {
	t.Helper()
	if report.SHA256 != quickJSWasmSHA256 {
		t.Errorf("embedded quickjs.wasm sha256 = %s, want %s", report.SHA256, quickJSWasmSHA256)
	}
	if cwd != "" {
		want, _ := filepath.EvalSymlinks(cwd)
		got, _ := filepath.EvalSymlinks(report.CWD)
		if got != want {
			t.Errorf("probe ran in %s, want %s", report.CWD, cwd)
		}
	}
	if !report.OK || string(report.Value) != `[42,"function"]` {
		t.Fatalf("execution = ok %v value %s error %q, want [42,\"function\"] (store exists only after the embedded prelude ran)", report.OK, report.Value, report.Error)
	}
}

// TestCodemodeEmbedsItsOnlyModuleSource proves the module has one source: production codemode code embeds the
// asset and never reads a file, and the built-in codemode tool does not choose another module.
func TestCodemodeEmbedsItsOnlyModuleSource(t *testing.T) {
	files, err := filepath.Glob("*.go")
	if err != nil {
		t.Fatal(err)
	}
	fileAccess := regexp.MustCompile(`\bos\.(Open|OpenFile|ReadFile|ReadDir|Stat|Lstat)\(|\bioutil\.|\bfs\.ReadFile\(`)
	embedded := false
	for _, file := range files {
		if strings.HasSuffix(file, "_test.go") {
			continue
		}
		data, err := os.ReadFile(file)
		if err != nil {
			t.Fatal(err)
		}
		if fileAccess.Match(data) {
			t.Errorf("%s reads the file system: %s", file, fileAccess.Find(data))
		}
		embedded = embedded || strings.Contains(string(data), "//go:embed assets/quickjs.wasm\nvar quickJSWasm []byte")
	}
	if !embedded {
		t.Error("codemode does not embed assets/quickjs.wasm")
	}
	tool, err := os.ReadFile(filepath.Join("..", "coding", "extension", "builtin", "codemode", "execute.go"))
	if err != nil {
		t.Fatal(err)
	}
	options := regexp.MustCompile(`(?s)sandbox\.NewSandbox\(sandbox\.SandboxOptions\{(.*?)\n\t\}\)`).FindSubmatch(tool)
	if options == nil {
		t.Fatal("built-in codemode tool does not construct a sandbox")
	}
	if strings.Contains(string(options[1]), "Wasm:") {
		t.Errorf("built-in codemode tool selects a module other than the embedded one:%s", options[1])
	}
}

func TestCodemodeWorkerConfigUpstream(t *testing.T) {
	cases := upstreamCases(t, codemodeWorkerConfig)
	if len(cases) != 3 {
		t.Fatalf("upstream denominator has %d cases, want the 3 this port maps", len(cases))
	}
	exe := ""
	if runtime.GOOS == "windows" {
		exe = ".exe"
	}
	byLine := map[int]func(t *testing.T){
		// uses a relative source entrypoint in Bun binaries: Pi's standalone executable. PiG's counterpart is the
		// release archive binary, started from an unrelated working directory with no source tree reachable.
		6: func(t *testing.T) {
			root := t.TempDir()
			cwd := t.TempDir()
			wantEmbeddedModule(t, runCodemodeDistribution(t, filepath.Join(root, "pig-release", "pig"+exe), cwd), cwd)
		},
		// uses the emitted worker beside the bundled Node module: Pi's npm bundle. PiG's counterpart is the
		// platform binary inside the @pi-in-go/pig-<os>-<cpu> package that the npm launcher starts.
		12: func(t *testing.T) {
			root := t.TempDir()
			cwd := t.TempDir()
			// pack_npm.py package_name and TARGETS use Node's process.platform and process.arch names.
			platform := map[string]string{"windows": "win32"}[runtime.GOOS]
			if platform == "" {
				platform = runtime.GOOS
			}
			cpu := map[string]string{"amd64": "x64"}[runtime.GOARCH]
			if cpu == "" {
				cpu = runtime.GOARCH
			}
			pkg := filepath.Join(root, "node_modules", "@pi-in-go", "pig-"+platform+"-"+cpu)
			wantEmbeddedModule(t, runCodemodeDistribution(t, filepath.Join(pkg, "pig"+exe), cwd), cwd)
		},
		// uses the pi-codemode worker when unbundled: Pi run from source. PiG's counterpart is go install or go test
		// from the module, here this process after it leaves the source directory.
		18: func(t *testing.T) {
			cwd := t.TempDir()
			t.Chdir(cwd)
			var report codemodeProbeReport
			if err := json.Unmarshal(codemodeProbe(t), &report); err != nil {
				t.Fatal(err)
			}
			wantEmbeddedModule(t, report, cwd)
		},
	}
	for _, tc := range cases {
		run, ok := byLine[tc.Line]
		if !ok {
			t.Fatalf("unmapped upstream case %s at line %d", tc.ID, tc.Line)
		}
		t.Run(tc.ID, func(t *testing.T) {
			t.Logf(".upstream/current/%s:%d", codemodeWorkerConfig, tc.Line)
			run(t)
		})
	}
}

type upstreamCase struct {
	ID   string `json:"id"`
	Line int    `json:"line"`
}

// upstreamCases reads the compiler-derived case inventory of one pinned upstream test file.
func upstreamCases(t *testing.T, path string) []upstreamCase {
	t.Helper()
	versionSource, err := os.ReadFile(filepath.Join("..", "internal", "coding", "pigversion", "pigversion.go"))
	if err != nil {
		t.Fatal(err)
	}
	match := regexp.MustCompile(`const UpstreamVersion = "([^"]+)"`).FindSubmatch(versionSource)
	if match == nil {
		t.Fatal("pigversion.go has no UpstreamVersion")
	}
	raw, err := os.ReadFile(filepath.Join("..", "test", "parity", "interfaces", "upstream-tests-v"+string(match[1])+".json"))
	if err != nil {
		t.Fatal(err)
	}
	var inventory struct {
		Files []struct {
			Path  string         `json:"path"`
			Cases []upstreamCase `json:"cases"`
		} `json:"files"`
	}
	if err := json.Unmarshal(raw, &inventory); err != nil {
		t.Fatal(err)
	}
	for _, file := range inventory.Files {
		if file.Path == path {
			return file.Cases
		}
	}
	t.Fatalf("missing upstream case inventory for %s", path)
	return nil
}
