# Additive features

These PiG-only features have no Pi equivalent. This page lists the ones that matter when you extend or modify PiG: extensions and SDKs, Piglets and Binaries, and diagnostic commands. 20 more additive records cover performance, crash handling, security, and platform details; they do not change how you author. Search this page with `grep -n -i <word>`. [Divergences](divergences.md) lists the places where PiG behaves differently from Pi.

| ID | Title | Summary |
|---|---|---|
| D18 | Piglet management and build dispatch | PiG manages, validates, runs, and builds Piglets with generic commands, and Pi has no Piglets. |
| D19 | Multi-language subprocess SDK bridges | PiG ships extension SDKs for Go, Rust, Python, and Lisp that use the same wire protocol and the same features. |
| D22 | Stock documentation materialization | PiG carries its own manual, and the pig docs command writes it to the config directory so no source checkout is needed. |
| D23 | Per-tool source metadata and piglet-scoped active-tool Context API | Extensions can list all tools with their source, read and change the active tools, and read flag values. |
| D28 | Extension validation report surface (pig install --validate-only --json) | The pig install --validate-only command loads and starts an extension without installing it and prints a machine-readable report. |
| D31 | Fused in-process extension runtime for Piglet Binaries | A Piglet Binary can run a Go extension inside the host process instead of in a separate process. |
| D40 | Extension-contributed authentication targets | The pig login --list command shows every sign-in method, including the methods that extensions add. |
| D41 | Additive typed status and Piglet inventory | The pig package list, pig status, and pig piglet list commands can print Packages, Resources, and Piglets as JSON. |
| D52 | Extension terminal-geometry context (ctx.width / ctx.height) | Extensions can read the terminal width and height and learn when either one changes. |
| D60 | Typed native login extension API | An extension can replace the header with a native login screen by calling ui.setLogin. |
| D91 | Piglet frontend members draw the interactive mode | A Piglet Binary can include a Go frontend member that draws the interactive screen. |
| D92 | Piglet strip lists disable built-ins and compile them out of a Piglet Binary | A Piglet can strip built-in tools, commands, extensions, APIs, and features, and a Piglet Binary compiles out what the Piglet strips. |
| D107 | Extension component kit | An extension can describe its screen as a tree of standard UI components that the host renders. |
| D109 | Extension SDK upgrade | When a Go extension fails to build because the SDK changed, PiG says that it was written for an older SDK and shows each changed API. |
| D170 | Piglet tree, lineage and provenance diagnostics | The pig piglet tree and pig piglet lineage commands show how installed Piglets extend each other and where each one comes from. |
| D194 | Piglet resolution installs npm and Git Packages with lifecycle scripts disabled unless the command opts in | Resolving a Piglet installs npm and Git Packages with lifecycle scripts turned off unless the command allows them. |
| D201 | The SDK resource loader takes the runtime its extensions register against | The SDK resource loader accepts a runtime, so the extensions it loads register against the runtime that you supply. |
| D202 | A Piglet Binary accepts overlay Piglets unless it is built closed (build.closed) | A Piglet Binary accepts a Piglet that you choose at launch as an overlay, as stock PiG does, unless you build it with build.closed. |
| D209 | A session exports its active Piglet to its tool processes (PIG_ACTIVE_PIGLET_SOURCE) | A running session sets PIG_ACTIVE_PIGLET_SOURCE for its tools, so pig status, pig diagnose, and pig piglet show can name the active Piglet. |
| D211 | A Piglet can withhold a project's context files until the project is trusted (contextFiles: trusted-only) | A Piglet with contextFiles: trusted-only loads no AGENTS.md or CLAUDE.md of a project that is not trusted, and says which files it skipped. |
