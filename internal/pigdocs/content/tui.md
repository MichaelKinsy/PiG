# Terminal UI

PiG provides a Go-native terminal interface that follows the observable behavior of upstream Pi. The interactive application includes the startup header, transcript, editor, overlays, and footer.

This page describes the PiG user and extension boundary. It does not expose PiG's internal TUI packages as a stable application SDK.

## Start the TUI

Run `pig` without print or RPC mode:

```bash
pig
```

The TUI uses the current working directory, settings, model selection, trusted Resources, and active Piglet.

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
- Press Escape to cancel the active dialog, compaction, Bash command, or model turn.
- Press Ctrl+C to clear the editor. Press it again within 500 ms to exit.
- Press Ctrl+D on an empty editor to exit.

See [Keybindings](/docs/latest/keybindings) for the complete binding list.

## Modes

PiG supports regular and fullscreen terminal modes. Configure `tuiMode` in settings or use the corresponding interactive setting.

The TUI responds to terminal resize. It calculates width in terminal cells and preserves Unicode and ANSI rendering boundaries.

## Extension UI

Extensions can contribute:

- notifications;
- status entries;
- widgets;
- headers and typed login identity;
- message and entry renderers;
- focused custom components;
- terminal input handlers;
- theme changes.

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

## Timer-driven components

Go, Rust, Python, and Node custom components can request a new frame after timer-owned state changes. PiG coalesces requests, limits timer frames to the 16 ms TUI interval, and preserves immediate input frames.

PiG Standard uses this public contract for PiG Runner. The Runner is an ordinary fused Go extension. Stock PiG has no Runner-specific callback or launcher.

## Login and header identity

The startup header shows PiG's pig head where Pi shows its logo (D2): the active sprite's 16-by-14 pixel pig, 16 cells by 7 lines, with the version beside its first line and the key hints, the `Press` line, the blank line and the onboarding line beside the next ones, as Pi lays its first lines beside its 2-line logo. Lines beside the head wrap in the cells right of it; the rest of the header wraps at the full width. The compact header is 2 lines taller than Pi's. The head needs truecolor, a terminal that aligns half blocks and 31 columns; otherwise, in 256-color terminals and narrow panes, a one-line bold `PiG.` mark takes the 4 cells of Pi's logo and the hints keep Pi's layout. Apple Terminal gets Pi's own Apple Terminal layout with the `PiG.` mark in place of Pi's wordmark. `quietStartup` hides the header as in Pi, and non-interactive modes have none.

In fullscreen mode (`tuiMode: fullscreen`), clicking the pig head in the header (or the `PiG.` mark) plays PiG's version of Pi's logo animation (D87): the screen dissolves into braille dust, the pig flies to the center, grows and spins, then turns into a big side-view pig in the sprite's colors that runs in place, and Escape or Ctrl+C sends it back to the header (press again to skip). Pi plays the same animation with its own logo. `/arminsayshi`, and its PiG alias `/pigsayhi`, draws a pig head labeled `pigsayhi` with Pi's Armin effects (D87).

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

The package `@earendil-works/pi-tui` is the TypeScript TUI package for upstream Pi. It is not PiG's Go API.

Use the [upstream TUI documentation](https://pi.dev/docs/latest/tui) when you are writing an in-process TypeScript extension for Pi itself.

## Related documentation

- [Using PiG](/docs/latest/usage)
- [Extensions](/docs/latest/extensions)
- [Keybindings](/docs/latest/keybindings)
- [Terminal setup](/docs/latest/terminal-setup)
