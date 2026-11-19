package cli

import (
	"bytes"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/coding"
	"github.com/MichaelKinsy/PiG/internal/coding/pigversion"
	"github.com/MichaelKinsy/PiG/internal/testbudget"
)

func newModelRegistryTestSession(t *testing.T, services *coding.AgentSessionServices) *coding.Session {
	t.Helper()
	model, err := coding.BuildModel("openai/gpt-5.4", services)
	if err != nil {
		t.Fatal(err)
	}
	runtime, err := coding.NewRuntime(coding.RuntimeOptions{Services: services})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = runtime.Close() })
	session, err := runtime.New(coding.SessionStartOptions{Model: model, NoSession: true})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = session.Close() })
	return session
}

func TestModelOperationsThroughPrintJSONAndRPCEntrypoints(t *testing.T) {
	t.Parallel()
	binary := buildPigBinaryForSignalTest(t)
	fixture, err := filepath.Abs(filepath.Join("testdata", "model-operations-entrypoint.mjs"))
	if err != nil {
		t.Fatal(err)
	}
	for _, mode := range []string{"print", "json", "rpc"} {
		t.Run(mode, func(t *testing.T) {
			home := t.TempDir()
			agentDir := filepath.Join(home, "agent")
			if err := os.MkdirAll(agentDir, 0o755); err != nil {
				t.Fatal(err)
			}
			config := `{"providers":{"test-faux":{"baseUrl":"http://localhost:0","api":"test-faux","authHeader":false,"models":[{"id":"faux-1","name":"Test Faux","api":"test-faux","input":["text"],"cost":{"input":0,"output":0,"cacheRead":0,"cacheWrite":0},"contextWindow":128000,"maxTokens":4096}]}}}`
			if err := os.WriteFile(filepath.Join(agentDir, "models.json"), []byte(config), 0o600); err != nil {
				t.Fatal(err)
			}
			marker := filepath.Join(home, "model-operations.marker")
			args := []string{"--model", "test-faux/faux-1", "--no-session", "-e", fixture}
			switch mode {
			case "print":
				args = append(args, "--print", "What is 20+22?")
			case "json":
				args = append(args, "--mode", "json", "--print", "What is 20+22?")
			case "rpc":
				args = append(args, "--mode", "rpc")
			}
			ctx := testbudget.Context(t)
			command := exec.CommandContext(ctx, binary, args...)
			command.Env = append(os.Environ(), "PIG_HOME="+home, "PIG_TEST_FAUX=1", "PIG_TEST_FAUX_SCENARIO=parity-basic", "PIG_MODEL_OPERATIONS_MARKER="+marker, "PIG_QUIET_STARTUP=1")
			if mode == "rpc" {
				command.Stdin = strings.NewReader("{\"id\":\"prompt\",\"type\":\"prompt\",\"message\":\"What is 20+22?\"}\n")
			}
			var output bytes.Buffer
			command.Stdout = &output
			command.Stderr = &output
			if err := command.Run(); err != nil {
				t.Fatalf("%s entrypoint: %v\n%s", mode, err, output.String())
			}
			observed, err := os.ReadFile(marker)
			if err != nil {
				t.Fatalf("%s marker: %v\n%s", mode, err, output.String())
			}
			if string(observed) != mode {
				t.Fatalf("%s extension mode = %q", mode, observed)
			}
		})
	}
}

// piModelRegistryObjects runs a model registry program through the installed Pi 1.2.0's ModelRuntime, the runtime behind an
// extension's ctx.modelRegistry (testdata/model-registry-oracle.mjs), and returns, for each find step, JSON.stringify of each
// model object Pi holds: what an extension reads from ctx.modelRegistry.find after the same steps. A model Pi does not have is nil.
func piModelRegistryObjects(t *testing.T, program ...map[string]any) []map[string]*string {
	t.Helper()
	root, err := filepath.EvalSymlinks("../../extensions/sdk-ts/node_modules/@earendil-works/pi-coding-agent")
	if err != nil {
		t.Fatal(err)
	}
	var manifest struct {
		Version string `json:"version"`
	}
	raw, err := os.ReadFile(filepath.Join(root, "package.json"))
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(raw, &manifest); err != nil || manifest.Version != pigversion.UpstreamPackageVersion {
		t.Fatalf("installed Pi %q (%v), want %s", manifest.Version, err, pigversion.UpstreamPackageVersion)
	}
	encoded, err := json.Marshal(program)
	if err != nil {
		t.Fatal(err)
	}
	home := t.TempDir()
	command := exec.CommandContext(testbudget.Context(t), "node", filepath.Join("testdata", "model-registry-oracle.mjs"), root, string(encoded))
	command.Env = append(os.Environ(), "HOME="+home, "PI_OFFLINE=1", "PI_CODING_AGENT_DIR="+filepath.Join(home, "agent"))
	var stderr bytes.Buffer
	command.Stderr = &stderr
	output, err := command.Output()
	if err != nil {
		t.Fatalf("Pi model registry oracle: %v\n%s", err, stderr.String())
	}
	var finds []map[string]*string
	if err := json.Unmarshal(output, &finds); err != nil {
		t.Fatalf("Pi model registry oracle output %q: %v", output, err)
	}
	return finds
}

// modelRegistryCheck is the model_registry_refresh argument (testdata/model-registry-refresh.mjs): the model objects Pi holds for
// provider ("" is registry-refresh) and, when auth is not nil, the auth every present model resolves.
func modelRegistryCheck(t *testing.T, provider string, objects map[string]*string, auth any) string {
	t.Helper()
	check := map[string]any{"objects": objects}
	if provider != "" {
		check["provider"] = provider
	}
	if auth != nil {
		check["auth"] = auth
	}
	data, err := json.Marshal(check)
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}
