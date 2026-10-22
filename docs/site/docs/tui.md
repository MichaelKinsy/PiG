# Terminal UI

PiG provides a Go-native terminal interface that follows the observable behavior of Pi 1.4.0. The interactive application includes the startup header, transcript, editor, overlays, and footer.

This page describes the PiG user and extension boundary. It does not expose PiG's internal TUI packages as a stable application SDK.

## Start the TUI

Run `pig` without print or RPC mode:

```bash
pig
```

The TUI uses the current working directory, settings, model selection, trusted Resources, and active Piglet.

## Worked example: a first session on screen

These screens were captured from PiG 0.5.0+1.2.0 in a 100-column, 30-line terminal, with the default `tuiMode` (fullscreen) in a folder holding `notes.txt` and `README.md`. The model is a scripted test server, so the model's sentence is illustrative. Everything else is what PiG drew. Trailing spaces are trimmed.

**1. Start.** `cd` to the folder and run `pig`. With a plain terminal (for example `NO_COLOR` set, or a terminal that does not report colour support) the header is the `PiG.` mark:

```text
 PiG. v0.5.0+1.2.0
      escape interrupt · ctrl+c/ctrl+d clear/exit · / commands · ! bash · ctrl+o more
 Press ctrl+o to show full startup help and loaded resources.

 PiG can explain its own features and look up its docs. Ask it how to use or extend PiG.

────────────────────────────────────────────────────────────────────────────────────────────────────
                                                                         ← the editor, empty
────────────────────────────────────────────────────────────────────────────────────────────────────
~/project
0.0%/128k (auto)                                                                              mock-1
```

(The `← the editor` label is added here for the page. PiG draws nothing there.) The two lines under the second rule are the footer (`~/project` stands for your folder's path): the working directory, then context use (`0.0%` of a `128k` window, `auto` for automatic compaction) on the left and the model on the right. In a terminal that reports 24-bit colour (`COLORTERM=truecolor`) the same screen starts with the pig head of the active sprite at the left, the version and key hints beside it:

```text
    ▄▄▄▄  ▄▄▄▄    v0.5.0+1.2.0
   ▄▀▀▀▀▄▄▀▀▀▀▄   escape interrupt · ctrl+c/ctrl+d clear/exit · / commands · ! bash · ctrl+o more
  ▀▀▀▀▀▀▀▀▀▀▀▀▀▀  Press ctrl+o to show full startup help and loaded resources.
  ▀▀▀▀▀▀▀▀▀▀▀▀▀▀
  ▀▀▀▀▀▀▀▀▀▀▀▀▀▀  PiG can explain its own features and look up its docs. Ask it how to use or
  ▀▀▀▀▀▀▀▀▀▀▀▀▀▀  extend PiG.
    ▀▀▀▀▀▀▀▀▀▀
```

This is the header the [Login and header identity](#login-and-header-identity) section describes (D2). The pig is drawn in colour; the text capture shows its shape only.

**2. Complete a command.** Type a slash and the letters `mo`, the start of `/model`. A list opens under the editor, with the best match marked `→`:

```text
/mo
────────────────────────────────────────────────────────────────────────────────────────────────────
→ model          <provider/model> — Select model (opens selector UI)
  scoped-models  Enable/disable models for Ctrl+P cycling
  import         Import and resume a session from a JSONL file
```

Press Tab to complete the marked entry. Clear the line with Ctrl+C (the first press only clears; see step 6).

**3. Complete a file.** Type `see @not`. The list shows the matching project files:

```text
see @not
────────────────────────────────────────────────────────────────────────────────────────────────────
→ notes.txt                       notes.txt
```

Tab replaces `@not` with `@notes.txt` in the editor. Checked by sending it: the request the model server received held the text `see @notes.txt` and nothing else. PiG does not attach the file's contents. The `@` completion only saves typing a path, and the model reads the file itself with its `read` tool.

**4. Send a prompt that makes the model run a command.** Type `Run: echo out; exit 3` and press Enter. The transcript, in order, is your message, the command the model chose (`$ echo out; exit 3`), its output (`out`), `Command exited with code 3` and how long it took, then the model's answer:

```text
 Run: echo out; exit 3

 $ echo out; exit 3

 out

 Command exited with code 3

 Took 0.0s

 The command printed out and exited with code 3.
```

The footer now shows token use: `↑200 ↓40 0.1%/128k (auto)`, where `↑` is tokens sent and `↓` tokens received over the session so far (the test server reports 100 in and 20 out per request, and this run made two requests).

**5. Run a command yourself with `!`.** `!ls` runs `ls` in the folder, shows the output in the transcript, and adds both to what the model sees on your next message. `!!echo private` runs the command and shows it, but keeps it out of the model's context. Checked by sending `hi` afterwards and reading the request the model server received: it held `Ran \`ls\`` with the listing, the listing's `notes.txt`, and your `hi`. The word `private` was not in the request. Output on screen:

```text
 $ ls

 README.md
 notes.txt

 $ echo private

 private
```

**6. Show everything, then leave.** Ctrl+O toggles expanded tool output and prints `Tool output: expanded` (press it again to collapse). It also lists the full startup help (`alt+enter to queue follow-up`, `alt+up to edit all queued messages`, `drop files to attach` and the rest). Ctrl+C on text in the editor clears it. Ctrl+C twice within half a second, or Ctrl+D on an empty editor, exits `pig`. Both were checked: the process ended and the terminal returned to the shell.

## Main areas

| Area | Purpose |
|---|---|
| Header | Show startup identity and operational information. |
| Transcript | Show user messages, assistant output, tools, results, notices, and extension content. |
| Editor | Accept prompts, commands, paths, and file references. |
| Footer | Show working directory, model, context, usage, and status. |
| Overlay | Own focus for selectors, settings, tree navigation, or extension UI. |

## Core interaction

- Type `/` to open command completion.
- Type `@` to search for project files.
- Press Tab to complete paths and commands.
- Press Enter to submit.
- Press Shift+Enter or Ctrl+J to insert a newline.
- Press Escape to cancel the active dialog, compaction, Bash command, or model turn. An extension tool that does not stop on its abort signal within 3 seconds is abandoned and fails with `Operation aborted`. Pi waits for such a tool without a limit (D111).
- Press Ctrl+C to clear the editor. Press it again within 500 ms to exit.
- Press Ctrl+D on an empty editor to exit.

See [Keybindings](/docs/latest/keybindings) for the complete binding list.

## Modes

PiG supports fullscreen and regular terminal modes. Fullscreen is the default. Set `tuiMode` to `regular`, choose **TUI mode** in `/settings`, or pass `--tui-mode regular` to keep the terminal's normal scrollback.

The TUI responds to terminal resize. It calculates width in terminal cells and preserves Unicode and ANSI rendering boundaries. Markdown horizontal rules use at most 80 content columns, including in expanded compaction summaries. Changing output padding retains user-message Markdown transforms and their pending work.

A single character wider than the editor's wrap width, such as a tab in an editor two cells wide, stays on one row and overflows it. Pi 1.4.0 exhausts its stack on this input (D159).

## Error notices

Provider errors appear in the assistant response without a second `Provider request failed` status. After a non-retryable assistant error, PiG shows a `/bug` hint once during the interactive run. Aborted or cancelled requests do not trigger the hint. See [Slash commands](/docs/latest/slash-commands) for the `/bug` report workflow and its D62 delivery difference.

If an update or another `pig` replaced or removed the `pig` executable while a session runs, or another `pig` pruned an extension cell or runtime file the session uses, PiG shows a warning once when an error is shown, in place of the `/bug` hint: `The pig installation this session runs from was removed or replaced. Features that load code on demand can fail until restart.` (`The pig extension files this session runs from were removed or replaced.` for a pruned extension file), followed by the `pig --session <id>` command that continues the session. PiG checks only when an error appears, never on a timer, and does not read a version from the replaced file. This is a PiG adaptation of Pi 1.4.0's install-change warning (D95).

Markdown wraps long unbroken text without truncating it. Link detection scans each word once so long text does not trigger repeated searches through the same suffix.

## Extension UI

Extensions can contribute:

- notifications;
- status entries;
- widgets;
- headers and typed login identity;
- message and entry renderers;
- focused custom components;
- terminal input handlers;
- mouse handlers for focused custom components;
- theme changes.

An extension's terminal input listener runs after PiG has read the input, not as a synchronous member of the input chain as in Pi. PiG's interactive driver owns terminal input (D105).

Source extensions send bounded serializable state through the extension host. PiG keeps socket and JSON work off the TUI render loop and renders from local cached state.

## Focused custom components

A focused component owns keyboard input until it completes or is cancelled.

For subprocess extensions:

1. the extension builds and renders the component locally;
2. PiG waits for exclusive terminal focus without blocking the TUI loop;
3. PiG opens a native focus-owning shell;
4. the extension sends replaceable line snapshots;
5. PiG sends ordered input to the extension;
6. the host rejects stale-width, out-of-order, or late frames;
7. close, cancellation, reload, disconnect, or shutdown stops the worker;
8. the SDK detaches invalidation before it disposes the component.

PiG suppresses identical frames. The host render path reads only the latest completed local frame.

In fullscreen mode, a component opened with `ctx.ui.custom` receives mouse events as in Pi: clicks, presses, drags, the wheel and hover, with coordinates local to the component. Go (`sdk.MouseHandler`), Rust and Python (`handle_mouse`) and Node (Pi's `handleMouse`) extensions all receive them. Select and settings lists in component-kit views respond to clicks and the wheel. A wheel event that no component handles scrolls the transcript, also while an overlay has focus, as in Pi 1.4.0. Pi 1.1.0 passed it to the focused overlay as raw input. An overlay that scrolls with the wheel must handle mouse events.

A custom session entry renders through its registered entry renderer. Ctrl+O re-runs the renderer with the new expansion state. A renderer that fails shows a `[type] renderer failed: ...` box in place of the entry.

## Timer-driven components

Go, Rust, Python, and Node custom components can request a new frame after timer-owned state changes. PiG coalesces requests, limits timer frames to the 16 ms TUI interval, and preserves immediate input frames.

PiG Standard uses this public contract for PiG Runner. The Runner is an ordinary fused Go extension. Stock PiG has no Runner-specific callback or launcher.

## Login and header identity

The startup header shows PiG's pig head where Pi shows its logo (D2): the active sprite's 16-by-14 pixel pig, 16 cells by 7 lines, with the version beside its first line and the key hints, the `Press` line, the blank line and the onboarding line beside the next ones, as Pi lays its first lines beside its 2-line logo. Lines beside the head wrap in the cells right of it; the rest of the header wraps at the full width. The compact header is 2 lines taller than Pi's. The head needs truecolor, a terminal that aligns half blocks and 31 columns; otherwise, in 256-color terminals and narrow panes, a one-line bold `PiG.` mark takes the 4 cells of Pi's logo and the hints keep Pi's layout. Apple Terminal gets Pi's own Apple Terminal layout with the `PiG.` mark in place of Pi's wordmark. `quietStartup` hides the header as in Pi, and non-interactive modes have none.

In fullscreen mode (`tuiMode: fullscreen`), clicking the pig head in the header (or the `PiG.` mark) plays PiG's version of Pi's logo animation (D87): the screen dissolves into braille dust, the pig flies to the center, grows and spins, then turns into a big side-view pig in the sprite's colors that runs in place, and Escape or Ctrl+C sends it back to the header (press again to skip). Pi plays the same animation with its own logo. `/arminsayshi`, and its PiG alias `/pigsayhi`, plays Pi's fullscreen 3D egg with the sprite's pig head in place of Armin: the pig grows out of the center, spins and slides its blocks around as a puzzle (D87). Outside fullscreen mode it draws a pig head labeled `pigsayhi` with Pi's Armin effects (D87).

`/sprite` chooses the pig from fifteen built-in sprites: `pig-default` (the default), the color variants `pink`, `green`, `mint`, `sandy`, `grey`, `blush`, `lavender` and `cloud`, and the characters `pigrogu`, `darth-vader`, `kratos`, `piglet`, `spider-ham` and `sheriff`. The color sprites are the standard pig recolored; the characters are their original pixel art. `/sprite list` lists them, `/sprite set <id>` chooses one, `/sprite` alone opens a picker, and `/sprite preview [id]` shows a sprite's full art, the `PiG.` wordmark and the pig, with its name and tagline, in an overlay that any key closes. The choice is saved in `$PIG_HOME/state/pig-standard/login.json`, where the Pigpen games read it. The command comes from the built-in `pig-login` extension, so `--no-extensions` and `-builtin:pig-login` leave it out. The saved sprite still shows in the header.

An extension or Piglet adds sprites with `ctx.ui.registerSprite({ id, name, tagline, mascot, palette })` (D2): `mascot` is the 16-by-14 pig the header draws and `/sprite preview` shows beside the wordmark, each row a string of palette symbols, `.` transparent, and `palette` maps each symbol to a `#RRGGBB` color. The id is a lowercase slug of at most 32 characters and must not be a built-in id. A registered sprite appears after the built-in ones in `/sprite list` and the picker and is saved like any other. When the extension is not loaded, the header draws `pig-default` and the saved choice stays. Pi has no sprites.

An extension replaces the header with `ctx.ui.setHeader` as in Pi, and `setHeader(undefined)` and `/reload` restore the PiG header. An extension or Piglet can also replace it with its own native login through `ctx.ui.setLogin`, a validated login definition drawn in the same header slot. PiG Standard's `piglogin` extension uses that contract with its own artwork and `/sprite` command, and replaces the built-in one.

Preview one login extension without starting a model Session:

```bash
pig extension preview-login ./my-login-extension
```

## Themes

Use `/settings` to select the active theme. PiG can load additional theme Resources from trusted user, project, Package, and Piglet sources.

See [Themes](/docs/latest/themes).

## Rendering failures

A renderer generation has a bounded inactivity contract. If it stalls, PiG cancels that generation and keeps the last valid frame.

A failed extension must not perform blocking IPC on the TUI render loop. Connection failure closes state owned by that extension and leaves unrelated UI available where possible.

## Accessibility and terminal behavior

PiG supports keyboard operation for its interactive surfaces. Theme and extension authors must preserve readable contrast, focus indication, terminal-cell width, and reduced-motion expectations.

Use a supported terminal and verify behavior at narrow and wide widths. See [Terminal setup](/docs/latest/terminal-setup).

## Upstream distinction

The package `@earendil-works/pi-tui` is the TypeScript TUI package for Pi. It is not PiG's Go API.

Use [Pi's TUI documentation](https://github.com/earendil-works/pi/blob/v1.2.0/packages/coding-agent/docs/tui.md) when you are writing an in-process TypeScript extension for Pi itself.

A Piglet can name a Go frontend member that draws the interactive mode in place of the ANSI renderer (D91). Stock PiG has no frontend member. See "Frontend members" in [Piglet Binaries](/docs/latest/piglet-binaries).

## Related documentation

- [Using PiG](/docs/latest/usage)
- [Extensions](/docs/latest/extensions)
- [Keybindings](/docs/latest/keybindings)
- [Terminal setup](/docs/latest/terminal-setup)
