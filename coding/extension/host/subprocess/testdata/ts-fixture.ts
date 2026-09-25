import type { ExtensionAPI } from "@earendil-works/pi-coding-agent";
import { buildSessionContext } from "@earendil-works/pi-coding-agent";
import { complete } from "@earendil-works/pi-ai";
import { Type } from "typebox";

export default function (pi: ExtensionAPI) {
  pi.registerTool({
    name: "echo_ts",
    description: "Echo from TS shim",
    parameters: Type.Object({
      text: Type.String({ description: "Text to echo" }),
    }),
    async execute(_toolCallId, params, _signal, _onUpdate, ctx) {
      const execResult = await pi.exec(process.execPath, ["-e", "process.stdout.write('exec-ts')"]);
      const missingExec = await pi.exec("pig-test-missing-program", []);
      const found = ctx.modelRegistry.find("anthropic", "claude-3-5-haiku");
      const auth = found ? await ctx.modelRegistry.getApiKeyAndHeaders(found) : { ok: false };
      const completion = found
        ? await complete(
            found,
            {
              systemPrompt: "Answer with exactly OK.",
              messages: [{ role: "user", content: [{ type: "text", text: "Say OK" }] }],
            },
            auth,
          )
        : { stopReason: "error", content: [] };
      const built = buildSessionContext(ctx.sessionManager.getEntries(), ctx.sessionManager.getLeafId());
      return {
        content: [{ type: "text", text: `echo-ts: ${params.text}` }],
        details: {
          source: "ts",
          activeTools: pi.getActiveTools(),
          allTools: pi.getAllTools(),
          commands: pi.getCommands(),
          thinkingLevel: pi.getThinkingLevel(),
          isIdle: ctx.isIdle(),
          hasPendingMessages: ctx.hasPendingMessages(),
          systemPrompt: ctx.getSystemPrompt(),
          contextUsage: ctx.getContextUsage(),
          sessionId: ctx.sessionManager.getSessionId(),
          sessionName: ctx.sessionManager.getSessionName(),
          sessionFile: ctx.sessionManager.getSessionFile(),
          leafId: ctx.sessionManager.getLeafId(),
          branchCount: ctx.sessionManager.getBranch().length,
          entryCount: ctx.sessionManager.getEntries().length,
          builtContextMessages: built.messages.length,
          modelId: ctx.model?.id,
          modelApi: ctx.model?.api,
          foundModelId: found?.id,
          authOk: Boolean(auth?.ok),
          execStdout: execResult.stdout,
          execCode: execResult.code,
          missingExec,
          completionStopReason: completion.stopReason,
          completionText: (completion.content || [])
            .filter((c: any) => c.type === "text")
            .map((c: any) => c.text)
            .join(" "),
        },
      };
    },
  });

  pi.registerCommand("ping_ts", {
    description: "Notify from TS shim",
    async handler(_args, ctx) {
      ctx.ui.notify("pong-ts", "info");
    },
  });

  // Mirrors upstream examples/extensions/doom-overlay: an overlay-mode
  // ui.custom() whose component height follows the width it is rendered at.
  pi.registerCommand("doom_overlay_ts", {
    description: "Open a doom-overlay-shaped TS overlay",
    async handler(_args, ctx) {
      await ctx.ui.custom<undefined>((_tui, _theme, _kb, done) => ({
        dispose: () => {},
        invalidate: () => {},
        render: (width: number): string[] => {
          const height = Math.max(10, Math.floor(width / 3.2));
          const lines: string[] = [];
          for (let i = 0; i < height; i++) lines.push("#".repeat(width));
          lines.push(`HUD w=${width}`.padEnd(width).slice(0, width));
          return lines;
        },
        handleInput(data: string): void {
          if (data === "q") done(undefined);
        },
      }), {
        overlay: true,
        overlayOptions: { width: "75%", maxHeight: "95%", anchor: "center", margin: { top: 1 } },
      });
    },
  });

  pi.registerCommand("custom_ts", {
    description: "Open a custom TS overlay",
    async handler(_args, ctx) {
      const result = await ctx.ui.custom<string | null>((_tui, theme, _kb, done) => {
        let buffer = "";
        // The factory builds a component object with render/handleInput.
        // Render emits the live buffer; handleInput collects bytes until
        // \r (Enter) which closes the overlay with the buffer, or \u001b
        // (Esc) which closes with null.
        const component = {
          dispose: () => {},
          invalidate: () => {},
          render: (_width: number): string[] => {
            const label = theme && typeof theme.bold === "function" ? theme.bold("custom>") : "custom>";
            return [`${label} ${buffer}`];
          },
          handleInput(data: string): void {
            if (data === "\r" || data === "\n") {
              done(buffer || "(empty)");
              return;
            }
            if (data === "\u001b") {
              done(null);
              return;
            }
            if (data === "\u007f") {
              buffer = buffer.slice(0, -1);
              this.invalidate();
              return;
            }
            if (data && data.length === 1 && data >= " ") {
              buffer += data;
              this.invalidate();
            }
          },
        };
        return component;
      }, { overlayOptions: { title: "custom-ts", widthFraction: 0.5, heightFraction: 0.3 } });
      ctx.ui.notify(`custom-ts-result:${result === null ? "(null)" : result}`, "info");
    },
  });

  pi.on("session_start", async (_event, ctx) => {
    ctx.ui.setStatus("ts-fixture", "ready");
    ctx.ui.setHeader((_tui, theme) => ({
      dispose: () => {},
      invalidate() {},
      render(width: number): string[] {
        return [theme.dim(`ts-header`.padEnd(Math.max(1, width)).slice(0, width))];
      },
    }));
    ctx.ui.setFooter((_tui, theme, _footerData) => ({
      dispose: () => {},
      invalidate() {},
      render(width: number): string[] {
        return [theme.dim(`ts-footer`.padEnd(Math.max(1, width)).slice(0, width))];
      },
    }));
    // Exercise setEditorComponent decoration mode so we have a parity
    // regression: capture border color, mode label, and history.
    const mod: any = await import("@earendil-works/pi-coding-agent");
    const CustomEditor = mod.CustomEditor;
    ctx.ui.setEditorComponent((tui: any, theme: any, kb: any) => {
      const editor = new (CustomEditor as any)(tui, theme, kb);
      editor.modeLabelProvider = () => "ts-mode";
      editor.modeLabelColor = (text: string) => `\u001b[35m${text}\u001b[0m`;
      editor.borderColor = (text: string) => `\u001b[31m${text}\u001b[0m`;
      editor.lockBorderColor();
      editor.addToHistory("history-1");
      editor.addToHistory("history-2");
      return editor;
    });
    // Register an autocomplete provider that fires on the `#issue` token.
    // Mirrors the upstream github-issue-autocomplete example shape so we
    // exercise the chain-with-base-provider semantic.
    ctx.ui.addAutocompleteProvider((current: any) => ({
      async getSuggestions(lines: string[], cursorLine: number, cursorCol: number, options: any) {
        const line = lines[cursorLine] ?? "";
        const before = line.slice(0, cursorCol);
        const m = before.match(/(?:^|\s)#([^\s]*)$/);
        if (!m) {
          return current.getSuggestions(lines, cursorLine, cursorCol, options);
        }
        const token = m[1];
        return {
          items: [
            { value: `#1`, label: `#1`, description: `ts-issue-1 ${token}` },
            { value: `#2`, label: `#2`, description: `ts-issue-2 ${token}` },
          ],
          prefix: `#${token}`,
        };
      },
      applyCompletion(lines: string[], cursorLine: number, cursorCol: number, item: any, prefix: string) {
        return current.applyCompletion(lines, cursorLine, cursorCol, item, prefix);
      },
    }));
  });
}
