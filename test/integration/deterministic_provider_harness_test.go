//go:build integration

package integration

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

const deterministicScenario = "parity-basic"
const deterministicModel = "test-faux/faux-1"

// deterministicGopiConfig launches pig against the test-only built-in
// provider hook. This is a deterministic parity harness, not a live-model test.
func deterministicGopiConfig(t *testing.T) systemConfig {
	t.Helper()
	pigHome := t.TempDir()
	// cli/args.ts --approve selects trust for this run without touching user state.
	return systemConfig{
		name:   "pig",
		bin:    buildBinary(t),
		args:   []string{"--model", deterministicModel, "--no-extensions", "--approve", "--offline"},
		env:    []string{"PIG_HOME=" + pigHome, "PIG_QUIET_STARTUP=1", "PIG_TEST_FAUX=1", "PIG_TEST_FAUX_SCENARIO=" + deterministicScenario},
		prompt: interactiveReadyMarker,
	}
}

// deterministicUpstreamConfig launches upstream pi with a temp extension that
// registers a deterministic custom provider named test-faux. Upstream remains
// the source of truth: parity tests run against this provider first and only
// then assert pig behavior.
func deterministicUpstreamConfig(t *testing.T) systemConfig {
	t.Helper()
	piBin := upstreamPiBin(t)
	extPath := writeDeterministicUpstreamProviderExt(t)
	return systemConfig{
		name:   "upstream",
		bin:    piBin,
		args:   []string{"--no-extensions", "-e", extPath, "--model", deterministicModel, "--approve", "--offline"},
		env:    []string{"PI_TEST_FAUX_SCENARIO=" + deterministicScenario},
		prompt: "pi v",
	}
}

func writeDeterministicUpstreamProviderExt(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, "test-faux-provider.ts")
	content := `import { createAssistantMessageEventStream } from "@mariozechner/pi-ai";

function messageText(message) {
  const c = message?.content;
  if (typeof c === "string") return c;
  if (!Array.isArray(c)) return String(c ?? "");
  const parts = [];
  for (const block of c) {
    if (!block) continue;
    if (block.type === "text") parts.push(block.text ?? "");
    else if (block.type === "tool_result") parts.push(block.content ?? "");
    else if (block.type === "thinking") parts.push(block.thinking ?? "");
    else if (block.type === "toolCall") parts.push(block.name ?? "");
  }
  return parts.join("\n");
}

function classify(messages) {
  const last = messages[messages.length - 1];
  const lastText = messageText(last);
  const historyText = messages.map(messageText).join("\n\n");

  if (lastText.includes("42") && (last?.role === "tool" || last?.role === "toolResult" || historyText.includes("expr 20 + 22"))) {
    return { kind: "text", text: "42" };
  }
  if (lastText.includes("What is 20+22?")) {
    return { kind: "text", text: "42" };
  }
  if (lastText.includes("Remember this exact code:")) {
    return { kind: "text", text: "Alpha" };
  }
  if (lastText.includes("What was the code I told you to remember?")) {
    if (historyText.includes("letters A L P H A") && historyText.includes("digits 7 7 4 9")) {
      return { kind: "text", text: "ALPHA-7749" };
    }
    return { kind: "error", text: "test-faux: missing memory context for code recall" };
  }
  if (lastText.includes("Run: expr 20 + 22")) {
    return { kind: "tool", toolName: "bash", toolArgs: { command: "expr 20 + 22" } };
  }
  if (lastText.includes("markdown parity fixture")) {
    return { kind: "text", text: "# Fixture Heading\n\n> quoted line\ncontinued quote\n> - quoted bullet\n\n1. first item\n   1. nested ordered\n   - nested bullet\n\n| Name | Value |\n| --- | ---: |\n| alpha | 10 |\n| beta | \u0060wrapped code sample\u0060 |\n\nRead [docs](https://example.com/docs)" };
  }
  if (lastText.trim().startsWith("/help")) {
    return { kind: "text", text: "Tell me what you want me to do and I will use the appropriate tools." };
  }
  return { kind: "error", text: "test-faux: unhandled request " + JSON.stringify(lastText) };
}

function streamTestFaux(model, context, options) {
  const stream = createAssistantMessageEventStream();
  const output = {
    role: "assistant",
    content: [],
    api: "test-faux",
    provider: "test-faux",
    model: model.id,
    usage: {
      input: 0,
      output: 0,
      cacheRead: 0,
      cacheWrite: 0,
      totalTokens: 0,
      cost: { input: 0, output: 0, cacheRead: 0, cacheWrite: 0, total: 0 },
    },
    stopReason: "stop",
    timestamp: Date.now(),
  };

  const messages = context?.messages ?? [];
  const plan = classify(messages);

  queueMicrotask(() => {
    stream.push({ type: "start", partial: output });
    if (plan.kind === "text") {
      output.content.push({ type: "text", text: plan.text });
      stream.push({ type: "text_start", contentIndex: 0, partial: output });
      stream.push({ type: "text_delta", contentIndex: 0, delta: plan.text, partial: output });
      stream.push({ type: "text_end", contentIndex: 0, content: plan.text, partial: output });
      stream.push({ type: "done", reason: "stop", message: output });
      stream.end();
      return;
    }
    if (plan.kind === "tool") {
      const toolCall = { type: "toolCall", id: "test-faux-tool-1", name: plan.toolName, arguments: plan.toolArgs };
      output.content.push(toolCall);
      const json = JSON.stringify(plan.toolArgs);
      stream.push({ type: "toolcall_start", contentIndex: 0, partial: output });
      stream.push({ type: "toolcall_delta", contentIndex: 0, delta: json, partial: output });
      stream.push({ type: "toolcall_end", contentIndex: 0, toolCall, partial: output });
      output.stopReason = "toolUse";
      stream.push({ type: "done", reason: "toolUse", message: output });
      stream.end();
      return;
    }
    output.stopReason = "error";
    output.errorMessage = plan.text;
    stream.push({ type: "error", reason: "error", error: output });
    stream.end();
  });

  return stream;
}

export default function (pi) {
  pi.registerProvider("test-faux", {
    baseUrl: "http://localhost:0",
    apiKey: "TEST_FAUX_UNUSED",
    api: "test-faux",
    models: [{
      id: "faux-1",
      name: "Test Faux",
      reasoning: false,
      input: ["text"],
      cost: { input: 0, output: 0, cacheRead: 0, cacheWrite: 0 },
      contextWindow: 128000,
      maxTokens: 4096,
    }],
    streamSimple: streamTestFaux,
  });
}
`
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("write deterministic upstream provider extension: %v", err)
	}
	if !strings.Contains(content, "registerProvider(\"test-faux\"") {
		t.Fatalf("generated upstream provider extension missing registerProvider")
	}
	return path
}

type deterministicPrintSystem struct {
	name         string
	bin          string
	args         []string
	env          []string
	cwd          string
	sessionsRoot string
}

func deterministicPrintGopi(t *testing.T) deterministicPrintSystem {
	t.Helper()
	pigHome := t.TempDir()
	return deterministicPrintSystem{
		name:         "pig",
		bin:          buildBinary(t),
		args:         []string{"--model", deterministicModel, "--no-extensions"},
		env:          []string{"PIG_HOME=" + pigHome, "PIG_CODING_AGENT_DIR=" + filepath.Join(pigHome, "agent"), "PIG_CODING_AGENT_SESSION_DIR=", "PIG_TEST_FAUX=1", "PIG_TEST_FAUX_SCENARIO=" + deterministicScenario},
		cwd:          t.TempDir(),
		sessionsRoot: filepath.Join(pigHome, "agent", "sessions"),
	}
}

func deterministicPrintUpstream(t *testing.T) deterministicPrintSystem {
	t.Helper()
	home := t.TempDir()
	ext := writeDeterministicUpstreamProviderExt(t)
	return deterministicPrintSystem{
		name:         "upstream",
		bin:          upstreamPiBin(t),
		args:         []string{"--no-extensions", "-e", ext, "--model", deterministicModel},
		env:          []string{"HOME=" + home, "PI_CODING_AGENT_DIR=" + filepath.Join(home, ".pi", "agent"), "PI_CODING_AGENT_SESSION_DIR=", "PI_TEST_FAUX_SCENARIO=" + deterministicScenario},
		cwd:          t.TempDir(),
		sessionsRoot: filepath.Join(home, ".pi", "agent", "sessions"),
	}
}

func runDeterministicPrint(t *testing.T, sys deterministicPrintSystem, extraArgs ...string) string {
	t.Helper()
	cmd := exec.Command(sys.bin, append(sys.args, extraArgs...)...)
	cmd.Env = mergeEnv(defaultEnv(), sys.env)
	cmd.Dir = sys.cwd
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	out, err := cmd.Output()
	if err != nil {
		t.Fatalf("%s print run failed: %v\nstdout: %s\nstderr: %s", sys.name, err, out, stderr.String())
	}
	return strings.TrimSpace(string(out))
}

func mergeEnv(base, overrides []string) []string {
	m := map[string]string{}
	for _, kv := range base {
		if k, v, ok := strings.Cut(kv, "="); ok {
			m[k] = v
		}
	}
	for _, kv := range overrides {
		if k, v, ok := strings.Cut(kv, "="); ok {
			m[k] = v
		}
	}
	out := make([]string, 0, len(m))
	for k, v := range m {
		out = append(out, k+"="+v)
	}
	return out
}
