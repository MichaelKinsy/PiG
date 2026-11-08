### Added

- `apps/p2/plugins/terminals` ports Pi's terminals plugin: the detached terminal host (`p2 terminals-host`, one per data directory, owning the programs, a headless libghostty terminal for each and the pi-env connections), its framed protocol, the owner's `Terminals` view with its one-use `/terminal` byte channel, the agent's `AgentTerminals` calls and the tmux-style `terminal` tool, plain-text output, tmux keys and commands, closing a session's terminals on archive, and the vendored web facet files. `hosts.RegisterSubcommand` lets a plugin be a program of the p2 executable. <!-- feat: p2-terminals -->
- The p2 executable links the workspace, locations, ports, projects, search and file-index plugins, and its web bundle holds the workspace and ports facets: the terminal panel's default place is the workspace's `workspace-terminal` slot, so the terminals' browser tests (clipboard, reconnect) run in headless Chromium instead of skipping. <!-- feat: p2-terminals -->

### Fixed

- The p2 server's owner notices go by the error's class, as Pi's `isOwnerNotice`: a hold its factory refused with `forbidden` (the terminals plugin's `agent terminals are for session workers`, `terminals are owner-only`) is never one, whatever its message says. The Go filter matched the word `forbidden` in the printed text, which these messages lack, so a refused hold reached the owner's notices (`Server: {activation_failed p2.agent-terminals …}`). <!-- fix: b0a9e4fce90e07a98f47a97b05b918aced409dfb -->
