// Deterministic faux provider extension for upstream pi.
// Used by the parity runner to drive tree/session scenarios without live LLM
// calls. Must match the behavior of ai/test_faux.go in pig.
//
// Install: pi --no-extensions -e <this file> --model test-faux/faux-1

// Dual-namespace resolution: the extension runs inside pi's process, so
// module resolution depends on whether pi is @earendil-works or @mariozechner.
import { existsSync, readFileSync, realpathSync } from "fs";
import { dirname, join } from "path";
import { pathToFileURL } from "url";

const PI_PACKAGE_NAMES = ["@earendil-works/pi-coding-agent", "@mariozechner/pi-coding-agent"];

const findPiPackageRoot = (): string => {
  let dir = dirname(realpathSync(process.argv[1] ?? process.execPath));
  while (true) {
    const packageJSON = join(dir, "package.json");
    if (existsSync(packageJSON)) {
      try {
        const pkg = JSON.parse(readFileSync(packageJSON, "utf8"));
        if (PI_PACKAGE_NAMES.includes(pkg?.name)) return dir;
      } catch {}
    }
    const parent = dirname(dir);
    if (parent === dir) throw new Error("could not locate pi-coding-agent package root");
    dir = parent;
  }
};

const piRoot = findPiPackageRoot();
const piAIRoot = (() => {
  const earendil = join(piRoot, "node_modules/@earendil-works/pi-ai");
  if (existsSync(earendil)) return earendil;
  return join(piRoot, "node_modules/@mariozechner/pi-ai");
})();

const { createAssistantMessageEventStream } = await import(
  pathToFileURL(join(piAIRoot, "dist/utils/event-stream.js")).href
);

const retryState = new Set<string>();
const toolCallCounters = new Map<string, bigint>();

// Reserve each batch without yielding. Counters survive turns and compaction; resumed history seeds a session on its first tool request.
function reserveToolCallIDs(sessionId: string, messages: any[], count: number): bigint {
  let last = toolCallCounters.get(sessionId);
  if (last === undefined) {
    last = 0n;
    const seed = (id: string) => {
      if (/^call_test_faux_\d+$/.test(id)) {
        const n = BigInt(id.slice("call_test_faux_".length));
        if (n > last!) last = n;
      }
    };
    for (const message of messages) {
      if (message.role === "assistant") {
        for (const block of message.content ?? []) {
          if (block.type === "toolCall") seed(block.id);
        }
      } else if (message.role === "toolResult") seed(message.toolCallId);
    }
  }
  toolCallCounters.set(sessionId, last + BigInt(count));
  return last + 1n;
}

function contentToText(content: any): string {
  if (typeof content === "string") return content;
  if (!Array.isArray(content)) return String(content ?? "");
  const parts: string[] = [];
  for (const block of content) {
    if (!block) continue;
    if (block.type === "text") parts.push(block.text ?? "");
    else if (block.type === "image") parts.push(block.mimeType ?? "image");
    else parts.push(String(block.text ?? block.content ?? block.type ?? ""));
  }
  return parts.join("\n");
}

function messageText(message: any): string {
  const c = message?.content;
  if (typeof c === "string") return c;
  if (!Array.isArray(c)) return String(c ?? "");
  const parts: string[] = [];
  for (const block of c) {
    if (!block) continue;
    if (block.type === "text") parts.push(block.text ?? "");
    else if (block.type === "tool_result") parts.push(contentToText(block.content));
    else if (block.type === "thinking") parts.push(block.thinking ?? "");
    else if (block.type === "toolCall") parts.push(block.name ?? "");
  }
  return parts.join("\n");
}

// Canned responses for compaction / branch summarization.
// Must match ai/test_faux.go constants identically.
const testFauxSummaryResponse = `## Goal
The user explored Go programming language features and error handling patterns.

## Constraints & Preferences
- (none)

## Progress
### Done
- [x] Discussed Go language features (goroutines, channels, GC, interfaces)
- [x] Covered error handling patterns (sentinel, wrapping, Is/As)

### In Progress
- [ ] (none)

### Blocked
- (none)

## Key Decisions
- (none)

## Next Steps
1. Continue exploring Go topics as needed

## Critical Context
- (none)`;

const testFauxBranchSummaryResponse = `## Goal
The user was exploring a conversation branch.

## Constraints & Preferences
- (none)

## Progress
### Done
- [x] Explored branch topic

### In Progress
- [ ] (none)

### Blocked
- (none)

## Key Decisions
- (none)

## Next Steps
1. Return to main branch

## Critical Context
- (none)`;

const testFauxTurnPrefixSummaryResponse = `## Original Request
The user asked a complex question requiring multi-step analysis.

## Early Progress
- Started analyzing the request

## Context for Suffix
- Analysis is ongoing`;

function messageImages(message: any): any[] {
  const c = message?.content;
  if (!Array.isArray(c)) return [];
  const out: any[] = [];
  for (const block of c) {
    if (!block) continue;
    if (block.type === "image") out.push(block);
    else if (block.type === "tool_result" && Array.isArray(block.images)) out.push(...block.images);
  }
  return out;
}

// isTurnPrefixRequest matches the pinned Pi's split-turn prefix summarization
// request (compaction.ts TURN_PREFIX_SUMMARIZATION_PROMPT). Only Pi loads this
// provider; PiG's side is ai/test_faux.go.
function isTurnPrefixRequest(lastText: string): boolean {
  return lastText.includes("Create a concise checkpoint of the user's request and the progress shown above");
}

function classify(messages: any[]) {
  const last = messages[messages.length - 1];
  const lastText = messageText(last);
  const lastImages = messageImages(last);
  const historyText = messages.map(messageText).join("\n\n");
  let currentUserText = "";
  for (let i = messages.length - 1; i >= 0; i--) {
    if (messages[i]?.role === "user") {
      currentUserText = messageText(messages[i]);
      break;
    }
  }

  // Compaction summarization: triggered by /compact or auto-compaction.
  if (lastText.includes("Create a structured context checkpoint summary") ||
      lastText.includes("NEW conversation messages to incorporate into the existing summary")) {
    return { kind: "text", text: testFauxSummaryResponse };
  }
  // Branch summarization: triggered by tree navigation with summarize=true.
  if (lastText.includes("Create a structured summary of this conversation branch")) {
    return { kind: "text", text: testFauxBranchSummaryResponse };
  }
  // Split-turn prefix summarization.
  if (isTurnPrefixRequest(lastText)) {
    return { kind: "text", text: testFauxTurnPrefixSummaryResponse };
  }

  if (lastText.includes('<file name="') && lastText.includes('CLI_FILE_PAYLOAD') && lastText.includes('prompt-after-file')) {
    return { kind: "text", text: "cli-file-ok" };
  }
  if (lastText.includes('</file>') && lastText.includes('describe sample image')) {
    if (lastImages.length === 1 && lastImages[0].mimeType === 'image/png') {
      return { kind: "text", text: "cli-image-mime-ok" };
    }
    return { kind: "error", text: `test-faux: expected one image/png attachment, got ${JSON.stringify(lastImages)}` };
  }
  if (lastText.includes('original 4000x2000, displayed at 2000x1000') && lastText.includes('describe resized image')) {
    if (lastImages.length === 1 && lastImages[0].mimeType === 'image/png') {
      return { kind: "text", text: "cli-image-resize-ok" };
    }
    return { kind: "error", text: `test-faux: expected resized image/png attachment, got ${JSON.stringify(lastImages)}` };
  }
  if (lastText.includes('<file name="') && lastText.includes('INLINE_FILE_PAYLOAD') && lastText.includes('inspect inline file')) {
    return { kind: "text", text: "inline-file-ok" };
  }
  if (lastText.includes("PT_ARGS:first|two words")) {
    return { kind: "text", text: "prompt-template-ok" };
  }
  if (lastText.includes('<skill name="sample-skill"') && lastText.includes("extra words")) {
    return { kind: "text", text: "skill-command-ok" };
  }
  // Deterministic Porter Profile activation probe. This response is used only
  // by the local headless smoke and does not stand in for closure evidence.
  if (lastText.includes('PORTER_HEADLESS_VERIFY') && lastText.includes('/skill:pig-porter')) {
    return { kind: "text", text: "pig-porter-headless-ok" };
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
    return { kind: "tool", toolCalls: [{ toolName: "bash", toolArgs: { command: "expr 20 + 22" } }] };
  }
  // Read tool parity.
  if (lastText.includes("Run: read parity-read-target.txt")) {
    return { kind: "tool", toolCalls: [{ toolName: "read", toolArgs: { path: "parity-read-target.txt" } }] };
  }
  // Write tool parity.
  if (lastText.includes("Run: write parity-write-output.txt")) {
    return { kind: "tool", toolCalls: [{ toolName: "write", toolArgs: { path: "parity-write-output.txt", content: "hello from faux\nline two\n" } }] };
  }
  // Same-file write+edit batch parity.
  if (lastText.includes("Run: write then edit parity-batched-target.txt")) {
    return {
      kind: "tool",
      toolCalls: [
        { toolName: "write", toolArgs: { path: "parity-batched-target.txt", content: "REPLACE_ME\n" } },
        {
          toolName: "edit",
          toolArgs: {
            path: "parity-batched-target.txt",
            edits: [{ oldText: "REPLACE_ME", newText: "DONE" }],
          },
        },
      ],
    };
  }
  // Extension tool bridge parity.
  if (lastText.includes("Run: extension echo hello")) {
    return { kind: "tool", toolCalls: [{ toolName: "echo_bridge", toolArgs: { text: "hello" } }] };
  }
  if (lastText.includes("Run: extension details")) {
    return {
      kind: "tool",
      toolCalls: [{
        toolName: "echo_bridge",
        toolArgs: { text: "DETAILS_ALPHA_BRAVO_CHARLIE_DELTA_ECHO_FOXTROT_GOLF_HOTEL_INDIA_JULIET_KILO_LIMA_TAIL" },
      }],
    };
  }
  if (lastText.includes("Run: extension UI dialogs")) {
    return { kind: "tool", toolCalls: [{ toolName: "ui_dialog_probe", toolArgs: {} }] };
  }
  // Extension tool_call blocker parity.
  if (lastText.includes("Run: bash BLOCK_ME")) {
    return { kind: "tool", toolCalls: [{ toolName: "bash", toolArgs: { command: "echo BLOCK_ME" } }] };
  }
  // Extension tool_result modifier parity.
  if (lastText.includes("Run: bash modified")) {
    return { kind: "tool", toolCalls: [{ toolName: "bash", toolArgs: { command: "echo parity-base" } }] };
  }
  // Slow parallel tools keep both live cards visible long enough for the
  // no-input rendering probe to observe independent streamed output.
  if (lastText.includes("Run: tui live parallel tools")) {
    return {
      kind: "tool",
      toolCalls: [
        { toolName: "read", toolArgs: { path: ".pig-live-parallel-a" } },
        { toolName: "read", toolArgs: { path: ".pig-live-parallel-b" } },
      ],
    };
  }
  // Parallel tool call parity: two tools dispatched simultaneously.
  if (lastText.includes("Run: parallel reads")) {
    return {
      kind: "tool",
      toolCalls: [
        { toolName: "read", toolArgs: { path: "parity-read-target.txt" } },
        { toolName: "bash", toolArgs: { command: "echo parallel-ok", timeout: 10 } },
      ],
    };
  }
  // Grep tool parity.
  if (lastText.includes("Run: grep hello")) {
    return { kind: "tool", toolCalls: [{ toolName: "grep", toolArgs: { pattern: "hello", path: "parity-read-target.txt" } }] };
  }
  // Find tool parity.
  if (lastText.includes("Run: find txt files")) {
    return { kind: "tool", toolCalls: [{ toolName: "find", toolArgs: { pattern: "*.txt", path: "." } }] };
  }
  // Ls tool parity.
  if (lastText.includes("Run: ls here")) {
    return { kind: "tool", toolCalls: [{ toolName: "ls", toolArgs: { path: "." } }] };
  }
  // Edit tool parity.
  if (lastText.includes("Run: edit parity-edit-target.txt")) {
    return { kind: "tool", toolCalls: [{ toolName: "edit", toolArgs: { path: "parity-edit-target.txt", oldText: "REPLACE_ME", newText: "REPLACED" } }] };
  }
  // Long-output bash tool parity (exercises visual-truncate.ts via bash.ts collapse hint).
  if (lastText.includes("Run: bash long output")) {
    return { kind: "tool", toolCalls: [{ toolName: "bash", toolArgs: { command: "seq 1 30" } }] };
  }
  // Slow-output tool used by the live main-screen parity probe.
  if (lastText.includes("Run: tui live tool")) {
    const command = lastText.includes("Run: tui live tool pause")
      ? "for i in $(seq 1 24); do printf 'LIVE-TOOL-%02d\\n' \"$i\"; if [ \"$i\" -eq 8 ]; then sleep 5; elif [ \"$i\" -eq 24 ]; then sleep 5; else sleep 0.04; fi; done"
      : "for i in $(seq 1 24); do printf 'LIVE-TOOL-%02d\\n' \"$i\"; sleep 0.04; done";
    return {
      kind: "tool",
      toolCalls: [{
        toolName: "bash",
        toolArgs: { command },
      }],
    };
  }
  // Tool result follow-ups: dispatch based on original user prompt.
  if (last?.role === "tool" || last?.role === "toolResult") {
    if (currentUserText.includes("Run: expr 20 + 22")) return { kind: "text", text: "42" };
    if (currentUserText.includes("Run: read parity-read-target.txt")) return { kind: "text", text: "done" };
    if (currentUserText.includes("Run: write parity-write-output.txt")) return { kind: "text", text: "wrote" };
    if (currentUserText.includes("Run: write then edit parity-batched-target.txt")) {
      if (historyText.includes("Successfully wrote") && historyText.includes("Successfully replaced 1 block(s) in parity-batched-target.txt.")) {
        return { kind: "text", text: "batched-done" };
      }
      return { kind: "error", text: "test-faux: batched mutation missing success markers" };
    }
    if (currentUserText.includes("Run: extension echo hello")) {
      if (historyText.includes("echo-bridge: hello")) {
        return { kind: "text", text: "echo-bridge: hello" };
      }
      return { kind: "error", text: "test-faux: extension tool result missing echo-bridge marker" };
    }
    if (currentUserText.includes("Run: extension details")) {
      if (historyText.includes("DETAILS_ALPHA_BRAVO_CHARLIE") && historyText.includes("KILO_LIMA_TAIL")) {
        return { kind: "text", text: "details-probe-done" };
      }
      return { kind: "error", text: "test-faux: extension details marker missing" };
    }
    if (currentUserText.includes("Run: extension UI dialogs")) {
      for (const marker of ["dialogs-ok:", "dialogs-cancelled:"]) {
        const index = historyText.indexOf(marker);
        if (index >= 0) return { kind: "text", text: historyText.slice(index) };
      }
      return { kind: "error", text: "test-faux: extension UI result missing dialog marker" };
    }
    if (currentUserText.includes("Run: bash BLOCK_ME")) {
      if (historyText.includes("parity-blocked")) {
        return { kind: "text", text: "blocked:parity-blocked" };
      }
      return { kind: "text", text: "not-blocked" };
    }
    if (currentUserText.includes("Run: bash modified")) {
      if (historyText.includes("[parity-modified]")) {
        return { kind: "text", text: "modified:parity-modified" };
      }
      return { kind: "text", text: "not-modified" };
    }
    if (currentUserText.includes("Run: grep hello")) return { kind: "text", text: "found" };
    if (currentUserText.includes("Run: find txt files")) return { kind: "text", text: "listed" };
    if (currentUserText.includes("Run: ls here")) return { kind: "text", text: "listed-ls" };
    if (currentUserText.includes("Run: edit parity-edit-target.txt")) return { kind: "text", text: "edited" };
    if (currentUserText.includes("Run: tui live parallel tools")) return { kind: "text", text: "LIVE-PARALLEL-DONE" };
    if (currentUserText.includes("Run: tui live tool")) return { kind: "text", text: "LIVE-TOOL-DONE" };
    if (currentUserText.includes("Run: bash long output")) return { kind: "text", text: "ran" };
    if (currentUserText.includes("Run: parallel reads")) return { kind: "text", text: "parallel-done" };
    if (currentUserText.includes("Run: bash control-chars")) return { kind: "text", text: "sanitized" };
    if (currentUserText.includes("Run: bash with invalid args")) return { kind: "text", text: "validation-handled" };
  }
  if (lastText.includes("markdown parity fixture")) {
    return {
      kind: "text",
      text: "# Fixture Heading\n\n> quoted line\ncontinued quote\n> - quoted bullet\n\n1. first item\n   1. nested ordered\n   - nested bullet\n\n| Name | Value |\n| --- | ---: |\n| alpha | 10 |\n| beta | `wrapped code sample` |\n\nRead [docs](https://example.com/docs)",
    };
  }
  // Over-window threshold parity: normal response with usage above the
  // configured context window threshold. This must compact without retrying.
  if (lastText.includes("Trigger: over-window response")) {
    return { kind: "text", text: "over-window-ok", usage: { input: 130000, output: 1000, cacheRead: 0, cacheWrite: 0, totalTokens: 131000 } };
  }
  // Overflow error parity: provider returns an overflow-pattern error.
  if (lastText.includes("Trigger: overflow error")) {
    return { kind: "error", text: "prompt is too long: 500000 tokens > 200000 maximum" };
  }
  if (lastText.includes("Send after overflow compaction")) {
    return { kind: "text", text: "queued-after-compaction-ok" };
  }
  // Sanitize parity: trigger bash with control-char output.
  if (lastText.includes("Run: bash control-chars")) {
    return { kind: "tool", toolCalls: [{ toolName: "bash", toolArgs: { command: "printf 'hello\x01\x02world'" } }] };
  }
  // Validation parity: tool call with invalid args (command should be string, not number).
  if (lastText.includes("Run: bash with invalid args")) {
    return { kind: "tool", toolCalls: [{ toolName: "bash", toolArgs: { command: 42 } }] };
  }
  if (lastText.trimStart().startsWith("/help")) {
    return { kind: "text", text: "Tell me what you want me to do and I will use the appropriate tools." };
  }
  // Mermaid rendering parity: return a multi-line ```mermaid block so the
  // assistant markdown pipeline runs the Mermaid transformer (mermaid.ts +
  // grok-mermaid render). The rendered box-drawing diagram is theme-independent
  // after ANSI normalization, so pig and pi must produce the same plain art.
  if (lastText.includes("show mermaid flowchart")) {
    return { kind: "text", text: "```mermaid\ngraph TD\n  A[Start] --> B[End]\n```" };
  }
  // Generic "reply with exactly: <word>": for tree parity scenarios.
  if (lastText.includes("reply with exactly:")) {
    const after = lastText.split("reply with exactly:")[1]?.trim();
    return { kind: "text", text: after || "done" };
  }
  return { kind: "error", text: `test-faux: unhandled request "${lastText}"` };
}

function streamTestFaux(model: any, context: any, options: any) {
  const stream = createAssistantMessageEventStream();
  const output: any = {
    role: "assistant",
    content: [],
    api: "test-faux",
    provider: "test-faux",
    model: model.id,
    usage: {
      input: 0, output: 0, cacheRead: 0, cacheWrite: 0, totalTokens: 0,
      cost: { input: 0, output: 0, cacheRead: 0, cacheWrite: 0, total: 0 },
    },
    stopReason: "stop",
    timestamp: Date.now(),
  };

  const messages = context?.messages ?? [];
  const last = messages[messages.length - 1];
  const lastText = messageText(last);
  if (lastText.includes("TUI_LIVE_STREAM")) {
    queueMicrotask(async () => {
      output.content.push({ type: "text", text: "" });
      stream.push({ type: "start", partial: output });
      stream.push({ type: "text_start", contentIndex: 0, partial: output });
      for (let i = 1; i <= 24; i++) {
        const delta = `LIVE-STREAM-${String(i).padStart(2, "0")}\n`;
        output.content[0].text += delta;
        stream.push({ type: "text_delta", contentIndex: 0, delta, partial: output });
        if (i === 8 && lastText.includes("TUI_LIVE_STREAM_PAUSE")) {
          await new Promise((resolve) => setTimeout(resolve, 1000));
          continue;
        }
        await new Promise((resolve) => setTimeout(resolve, 40));
      }
      stream.push({ type: "text_end", contentIndex: 0, content: output.content[0].text, partial: output });
      stream.push({ type: "done", reason: "stop", message: output });
      stream.end();
    });
    return stream;
  }
  if (lastText.includes("Trigger: retryable provider error")) {
    const key = "retryable-provider-error";
    if (!retryState.has(key)) {
      retryState.add(key);
      queueMicrotask(() => {
        output.stopReason = "error";
        output.errorMessage = "please retry your request";
        stream.push({ type: "start", partial: output });
        stream.push({ type: "error", reason: "error", error: output });
        stream.end();
      });
      return stream;
    }
    queueMicrotask(() => {
      output.content.push({ type: "text", text: "retry-ok" });
      stream.push({ type: "start", partial: output });
      stream.push({ type: "text_start", contentIndex: 0, partial: output });
      stream.push({ type: "text_delta", contentIndex: 0, delta: "retry-ok", partial: output });
      stream.push({ type: "text_end", contentIndex: 0, content: "retry-ok", partial: output });
      stream.push({ type: "done", reason: "stop", message: output });
      stream.end();
    });
    return stream;
  }
  const plan = classify(messages);
  const usage = (plan as any).usage;
  if (usage) {
    output.usage = {
      input: usage.input ?? 0,
      output: usage.output ?? 0,
      cacheRead: usage.cacheRead ?? 0,
      cacheWrite: usage.cacheWrite ?? 0,
      totalTokens: usage.totalTokens ?? ((usage.input ?? 0) + (usage.output ?? 0) + (usage.cacheRead ?? 0) + (usage.cacheWrite ?? 0)),
      cost: { input: 0, output: 0, cacheRead: 0, cacheWrite: 0, total: 0 },
    };
  }

  const emitPlan = (emitStart = true) => {
    if (emitStart) stream.push({ type: "start", partial: output });
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
      const firstID = reserveToolCallIDs(options?.sessionId ?? "", messages, (plan as any).toolCalls.length);
      for (const [index, call] of (plan as any).toolCalls.entries()) {
        const toolCall = { type: "toolCall", id: `call_test_faux_${firstID + BigInt(index)}`, name: call.toolName, arguments: call.toolArgs };
        output.content.push(toolCall);
        const json = JSON.stringify(call.toolArgs);
        stream.push({ type: "toolcall_start", contentIndex: index, partial: output });
        stream.push({ type: "toolcall_delta", contentIndex: index, delta: json, partial: output });
        stream.push({ type: "toolcall_end", contentIndex: index, toolCall, partial: output });
      }
      output.stopReason = "toolUse";
      stream.push({ type: "done", reason: "toolUse", message: output });
      stream.end();
      return;
    }
    output.stopReason = "error";
    output.errorMessage = (plan as any).text;
    stream.push({ type: "error", reason: "error", error: output });
    stream.end();
  };
  const slowCompaction =
    (process.env.TEST_FAUX_SLOW_COMPACTION === "1" &&
      (isTurnPrefixRequest(lastText) ||
        lastText.includes("Create a structured context checkpoint summary") ||
        lastText.includes("NEW conversation messages to incorporate into the existing summary"))) ||
    (process.env.TUI_LIVE_PROBE === "1" &&
      lastText.includes("Create a structured context checkpoint summary")) ||
    (lastText.includes("Create a structured context checkpoint summary") &&
      messages.map(messageText).join("\n").includes("Trigger: overflow error with queued message"));
  if (slowCompaction) {
    stream.push({ type: "start", partial: output });
    setTimeout(() => {
      if (options?.signal?.aborted) {
        output.stopReason = "error";
        output.errorMessage = "This operation was aborted";
        stream.push({ type: "error", reason: "error", error: output });
        stream.end();
        return;
      }
      emitPlan(false);
    }, 1000);
  } else queueMicrotask(emitPlan);

  return stream;
}

export default function (pi: any) {
  pi.registerProvider("test-faux", {
    baseUrl: "http://localhost:0",
    apiKey: "unused",
    api: "test-faux",
    models: [{
      id: "faux-1",
      name: "Test Faux",
      api: "test-faux",
      provider: "test-faux",
      baseUrl: "http://localhost:0",
      reasoning: false,
      input: ["text", "image"],
      cost: { input: 0, output: 0, cacheRead: 0, cacheWrite: 0 },
      contextWindow: 128000,
      maxTokens: 4096,
    }],
    streamSimple: streamTestFaux,
  });
}
