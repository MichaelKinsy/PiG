### Added

- Lisp extensions can now use the `AgentSession` members of Pi that a child session from `ctx-create-agent-session` lacked: the reads (`agent-session-idle-p`, `-streaming-p`, `-compacting-p`, `-session-name`, `-steering-mode`, `-get-all-tools`, `-get-context-usage`, `-get-session-stats`, `-get-last-assistant-text` and the rest), the changes (`-set-model`, `-cycle-model`, `-set-thinking-level`, `-set-scoped-models`, `-set-active-tools-by-name`, `-set-steering-mode`, `-set-auto-compaction-enabled`, `-set-session-name`, `-clear-queue` and the aborts) and the awaited members (`-wait-for-idle`, `-reload`, `-compact`, `-navigate-tree`, `-execute-bash`, `-send-user-message`, `-send-custom-message`, `-export-to-html`, `-summarize-for-bug-report`). Each reads or changes the session the host owns; a blocked awaited member never holds a later call, and a cancelled `:ctx` runs Pi's abort for `-compact`, `-navigate-tree` and `-execute-bash`.

### Upgrading

- Lisp: `agent-session-session-id`, `-session-file`, `-model`, `-thinking-level` and `-system-prompt`, which held the values at creation, now ask the host for the session's current values.
- Lisp: `agent-session-active-tools` is removed, because Pi's `AgentSession` has no `activeTools` property. Use `agent-session-get-active-tool-names`.
