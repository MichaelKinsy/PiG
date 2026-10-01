# fix-992-tool-renderer-examples

Port of `packages/coding-agent/test/tool-renderer-examples.test.ts` (Pi 0.99.2, tagged hot-path, area tui) into `internal/codingagent/tool_renderer_examples_upstream_test.go`.

## Probe of real Pi 0.99.2 first

`make parity-deps` installed Pi 0.99.2. A Node script (not committed) runs the three upstream assertions with Pi's own `DefaultResourceLoader`, `createAgentSession` and `ToolExecutionComponent` against the shipped examples:

- the system prompts with `built-in-tool-renderer.ts` and `minimal-mode.ts` equal the baseline;
- the edit definition from `minimal-mode.ts` has `renderShell: "default"`, and its 40-column card equals the card without `renderShell`: `["", <bg>"<blank>", <bg>" edit notes.txt …", <bg>"<blank>"]` (a boxed card);
- a `"self"` shell draws `["", "edit notes.txt …"]`, so the comparison discriminates.

`audit-992-codemode-mcp` had already measured the renderShell case byte-identical on real Pi and PiG (tmux). This lane additionally covers the in-process definition and card path.

## Port

- `TestToolRendererExamplesUpstream` has the two `it.each` system-prompt cases (`built-in tool renderer`, `minimal mode`, with Pi's tool lists) and `keeps minimal mode's edit tool in the default shell`, with Pi's input `{path: "notes.txt", oldText: "before", newText: "after"}` and width 40.
- It loads the vendored example (byte-identical to `.upstream/v0.99.2`, checked with `cmp`) through `subprocess.Host.Load`, builds a `coding.Session` with Pi's `getSessionState` options (no skills, prompt templates or context files; one shared cwd for the baseline and extension sessions; the tool list as the active tools), and renders through `InteractiveMode.applyToolPresentation`.
- `internal/codingagent/export_test.go` gains `RenderToolCard`, a test-only export of the existing `toolComponentRaw` fixture, because the test must live in `codingagent_test` (the `coding` package imports `internal/codingagent`).

## Red/green

No production code was missing, so there is no stub and no green commit. The first run failed only because the test harness used a different temp cwd for each session (Pi shares one); that was a port error in the test, fixed before the commit. The test is therefore not red-proven against production by a missing feature. Instead it is mutation-proven:

- forcing `RenderShell` to self for every host-built definition (`coding/extension/host/subprocess/host.go`) fails `keeps minimal mode's edit tool in the default shell`;
- appending text to the host-built `PromptSnippet` fails both system-prompt cases.

Both mutations were reverted.

## Deferred

Nothing. The known neighbouring defects (CM1 `prepareArguments` ordering, CM2 batch start order, CM3 argument order, CM4 edit diff context color) belong to other lanes. None changes this test's result.
