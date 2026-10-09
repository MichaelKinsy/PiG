package cli

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"
)

// Pi createAgentSessionServices registers extension providers and then awaits refresh({ allowNetwork: false }) (agent-session-services.ts:170-182) before main.ts resolves the startup model and answers --list-models. A native Provider object's registration runs no auth check (model-runtime.ts:744-750), so only that awaited refresh makes it available. Pi 0.87.1 lists it and selects it as the RPC startup model.
func TestStartupRefreshMakesExtensionNativeProviderAvailable(t *testing.T) {
	binary := buildPigBinaryForSignalTest(t)
	fixture, err := filepath.Abs(filepath.Join("testdata", "startup-native-provider.mjs"))
	if err != nil {
		t.Fatal(err)
	}
	home := t.TempDir()
	env := []string{"HOME=" + home, "PIG_HOME=" + filepath.Join(home, "pig"), "PIG_CODING_AGENT_DIR=" + filepath.Join(home, "agent"), "PIG_OFFLINE=1", "FORCE_COLOR=0"}

	cmd := exec.CommandContext(t.Context(), binary, "--no-extensions", "-e", fixture, "--list-models", "native-startup")
	cmd.Dir = home
	cmd.Env = append(os.Environ(), env...)
	var stdout, stderr bytes.Buffer
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	if err := cmd.Run(); err != nil {
		t.Fatalf("list models: %v stdout=%q stderr=%q", err, stdout.String(), stderr.String())
	}
	const listing = "provider             model                 context  max-out  thinking  images\n" +
		"native-startup-prov  native-startup-model  4K       100      no        no    \n"
	if stdout.String() != listing || stderr.Len() != 0 {
		t.Errorf("list stdout=%q stderr=%q, want Pi's listing %q", stdout.String(), stderr.String(), listing)
	}

	p := startRPCProcessAt(t, t.TempDir(), env, "--no-extensions", "-e", fixture, "--no-session")
	p.send(`{"id":"state","type":"get_state"}`)
	p.await("native startup model state", func(record rpcRecord) bool {
		if record["type"] != "response" || record["id"] != "state" {
			return false
		}
		data, _ := record["data"].(map[string]any)
		model, _ := data["model"].(map[string]any)
		if model["provider"] != "native-startup-prov" || model["id"] != "native-startup-model" {
			t.Errorf("startup model = %v, want native-startup-prov/native-startup-model", model)
		}
		return true
	})
	p.closeAndWait("after native startup model state")
}

// Pi's registerProvider starts an unawaited refresh (model-runtime.ts:744-750) that lands on a later event-loop turn with no further model-runtime call, so RPC get_available_models, a synchronous snapshot read (rpc-mode.ts:473), lists a native Provider that an extension registered after startup. The registering host call ends the caller's turn; PiG starts the queued refresh there instead of waiting for an unrelated awaited call. The wait is bounded only to fail a refresh that never lands.
func TestPostStartupNativeProviderRegistrationBecomesAvailableWithoutAnotherCall(t *testing.T) {
	fixture, err := filepath.Abs(filepath.Join("testdata", "post-startup-native-provider.mjs"))
	if err != nil {
		t.Fatal(err)
	}
	home := t.TempDir()
	env := []string{"HOME=" + home, "PIG_HOME=" + filepath.Join(home, "pig"), "PIG_CODING_AGENT_DIR=" + filepath.Join(home, "agent"), "PIG_OFFLINE=1", "FORCE_COLOR=0"}
	p := startRPCProcessAt(t, t.TempDir(), env, "--no-extensions", "-e", fixture, "--no-session")
	listed := func(id string) bool {
		p.send(`{"id":"` + id + `","type":"get_available_models"}`)
		found := false
		p.await("get_available_models "+id, func(record rpcRecord) bool {
			if record["type"] != "response" || record["id"] != id {
				return false
			}
			data, _ := record["data"].(map[string]any)
			models, _ := data["models"].([]any)
			for _, value := range models {
				if model, _ := value.(map[string]any); model["provider"] == "post-startup-prov" && model["id"] == "post-startup-model" {
					found = true
				}
			}
			return true
		})
		return found
	}
	if listed("before") {
		t.Fatal("the fixture provider is listed before its command registers it")
	}
	p.send(`{"id":"register","type":"prompt","message":"/register_native"}`)
	p.await("register_native prompt response", func(record rpcRecord) bool {
		return record["type"] == "response" && record["id"] == "register"
	})
	deadline := time.Now().Add(p.budget)
	for attempt := 0; !listed("after-" + strconv.Itoa(attempt)); attempt++ {
		if time.Now().After(deadline) {
			t.Fatalf("the registered native Provider never became available\n%s", p.stderr.String())
		}
		time.Sleep(20 * time.Millisecond)
	}
	p.closeAndWait("after post-startup native Provider")
}

// Pi loads every extension factory before it flushes their provider registrations and awaits refresh({ allowNetwork: false }) (agent-session-services.ts:158-182), so a native Provider's auth check never runs while a later extension is still loading. Pi 0.87.1 writes "sibling factory done" and then only "check" lines with these fixtures; the sibling factory waits up to 3 seconds for a check, so any check during loading comes first.
func TestStartupNativeProviderCallbacksWaitForEveryExtensionFactory(t *testing.T) {
	t.Parallel()
	binary := buildPigBinaryForSignalTest(t)
	var fixtures []string
	for _, name := range []string{"startup-order-native-provider.mjs", "startup-order-sibling.mjs"} {
		fixture, err := filepath.Abs(filepath.Join("testdata", name))
		if err != nil {
			t.Fatal(err)
		}
		fixtures = append(fixtures, "-e", fixture)
	}
	home := t.TempDir()
	orderLog := filepath.Join(home, "order.log")
	cmd := exec.CommandContext(t.Context(), binary, append(append([]string{"--no-extensions"}, fixtures...), "--list-models", "order-prov")...)
	cmd.Dir = home
	cmd.Env = append(os.Environ(), "HOME="+home, "PIG_HOME="+filepath.Join(home, "pig"), "PIG_CODING_AGENT_DIR="+filepath.Join(home, "agent"), "PIG_OFFLINE=1", "FORCE_COLOR=0", "ORDER_LOG="+orderLog)
	var stdout, stderr bytes.Buffer
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	if err := cmd.Run(); err != nil {
		t.Fatalf("list models: %v stdout=%q stderr=%q", err, stdout.String(), stderr.String())
	}
	const listing = "provider    model        context  max-out  thinking  images\n" +
		"order-prov  order-model  4K       100      no        no    \n"
	if stdout.String() != listing {
		t.Errorf("list stdout=%q stderr=%q, want Pi's listing %q", stdout.String(), stderr.String(), listing)
	}
	data, err := os.ReadFile(orderLog)
	if err != nil {
		t.Fatal(err)
	}
	lines := strings.Split(strings.TrimSuffix(string(data), "\n"), "\n")
	if len(lines) < 2 || lines[0] != "sibling factory done" || slices.ContainsFunc(lines[1:], func(line string) bool { return line != "check" }) {
		t.Fatalf("order log = %q, want Pi's sibling factory line followed only by auth checks", lines)
	}
}

// Pi's get_available_models answers the model runtime's availability snapshot and nothing else (rpc-mode.ts:490-493). With no credential, no configured provider and no extension provider the snapshot is empty, so Pi answers []: the no-model placeholder is not a model.
func TestRPCGetAvailableModelsIsTheSnapshotAloneWhenNothingIsAvailable(t *testing.T) {
	home := t.TempDir()
	env := []string{"HOME=" + home, "PIG_HOME=" + filepath.Join(home, "pig"), "PIG_CODING_AGENT_DIR=" + filepath.Join(home, "agent"), "PIG_OFFLINE=1", "FORCE_COLOR=0"}
	for _, entry := range os.Environ() {
		name, _, _ := strings.Cut(entry, "=")
		if strings.HasSuffix(name, "_API_KEY") || strings.HasSuffix(name, "_TOKEN") || strings.HasSuffix(name, "_KEY") || name == "ANTHROPIC_FEDERATION_RULE_ID" {
			env = append(env, name+"=")
		}
	}
	p := startRPCProcessAt(t, t.TempDir(), env, "--no-extensions", "--no-session")
	p.send(`{"id":"models","type":"get_available_models"}`)
	var models []any
	p.await("get_available_models", func(record rpcRecord) bool {
		if record["type"] != "response" || record["id"] != "models" {
			return false
		}
		data, _ := record["data"].(map[string]any)
		models, _ = data["models"].([]any)
		return true
	})
	if len(models) != 0 {
		t.Fatalf("get_available_models = %v, want [] as Pi's empty snapshot", models)
	}
	p.closeAndWait("after get_available_models")
}
