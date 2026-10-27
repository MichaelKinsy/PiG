;;;; SPDX-License-Identifier: MIT
(defpackage #:pig
  (:nicknames #:pig-sdk)
  (:use #:cl)
  (:export
   ;; conditions
   #:pig-error #:json-error #:host-call-error #:host-call-cancelled #:unsupported-capability
   #:host-call-error-code #:host-call-error-message #:unsupported-capability-member #:unsupported-capability-lane
   ;; JSON
   #:obj #:arr #:from-json #:to-json #:decode-json #:encode-json #:jref #:jget
   ;; registration
   #:extension #:make-extension #:define-extension #:extension-name
   #:tool #:command #:shortcut #:flag #:on #:on-loaded #:run-extension #:tool-result
   ;; cancellation signal
   #:abort-signal #:abort-signal-set-p #:abort-signal-wait
   ;; context
   #:ctx-extension #:ctx-cwd #:ctx-mode #:ctx-has-ui-p #:ctx-cancelled-p #:ctx-cancellation-reason
   #:ctx-wait-cancelled #:ctx-signal #:ctx-tool-call-id #:ctx-request-id #:ctx-on-update
   #:ctx-notify #:ctx-set-status #:ctx-clear-status #:ctx-set-title
   #:ctx-select #:ctx-confirm #:ctx-input #:ctx-editor
   #:ctx-exec #:exec-result #:exec-result-stdout #:exec-result-stderr #:exec-result-exit-code #:exec-result-killed
   #:ctx-is-idle-p #:ctx-abort #:ctx-has-pending-messages-p #:ctx-shutdown #:ctx-compact #:ctx-reload
   #:ctx-get-flag #:ctx-get-system-prompt #:ctx-get-thinking-level #:ctx-get-all-tools #:ctx-get-active-tools
   #:ctx-send-message #:ctx-send-user-message #:ctx-append-entry #:ctx-set-session-name
   #:ctx-call-host
   #:ctx-set-active-tools #:ctx-get-commands #:ctx-get-context-usage #:ctx-get-system-prompt-options #:ctx-get-model-info
   #:ctx-get-model-auth #:ctx-get-scoped-models #:ctx-set-model #:ctx-set-thinking-level #:ctx-set-label #:ctx-wait-for-idle
   #:ctx-project-trusted-p #:ctx-get-session-id #:ctx-get-leaf-id #:ctx-get-all-themes #:ctx-get-theme #:ctx-set-theme
   #:ctx-on-width-change #:ctx-get-editor-text #:ctx-tools-expanded-p
   #:registry-find #:registry-get-provider-auth #:registry-refresh #:registry-get-available-of-type
   #:ctx-session-manager #:session-get-cwd #:session-get-session-dir #:session-get-session-id
   #:session-get-session-file #:session-get-session-name #:session-get-leaf-id #:session-get-header
   #:session-get-leaf-entry #:session-get-entry #:session-get-label #:session-get-entries
   #:session-get-branch #:session-get-children #:session-get-tree
   #:ctx-model-registry #:registry-has-configured-auth #:registry-is-using-oauth
   #:registry-get-registered-native-provider #:registry-get-provider-name
   #:ctx-footer-data #:footer-get-git-branch #:footer-get-extension-statuses
   #:footer-get-available-provider-count #:footer-on-branch-change
   ;; ==== lane T: type engine, tool schema, pi-ai ====
   ;; ==== end lane T ====
   ;; ==== lane H: pi-tui helpers ====
    #:tui-visible-width #:tui-strip-terminal-sequences #:tui-truncate-to-width #:tui-wrap-text-with-ansi #:tui-slice-by-column #:tui-slice-with-width
    #:tui-extract-segments #:tui-get-osc8-link-at-column #:tui-get-active-background-ansi #:+tui-cursor-marker+ #:tui-key #:tui-matches-key
    #:tui-parse-key #:tui-key-release-p #:tui-key-repeat-p #:tui-set-kitty-protocol-active #:tui-kitty-protocol-active-p #:tui-decode-kitty-printable
    #:tui-decode-printable-key #:tui-fuzzy-match #:tui-fuzzy-filter #:tui-fuzzy-match-matches #:tui-fuzzy-match-score #:*tui-keybindings*
    #:make-tui-keybinding-definition #:tui-keybinding-definition-default-keys #:tui-keybinding-definition-description #:tui-keybinding-conflict-key
    #:tui-keybinding-conflict-keybindings #:tui-make-keybindings-manager #:tui-keybindings-from-wire #:tui-keybindings-matches
    #:tui-keybindings-get-keys #:tui-keybindings-get-definition #:tui-keybindings-get-conflicts #:tui-keybindings-set-user-bindings
    #:tui-keybindings-get-user-bindings #:tui-keybindings-get-resolved-bindings #:tui-get-keybindings #:tui-set-keybindings #:make-tui-capabilities
    #:tui-capabilities-images #:tui-capabilities-true-color #:tui-capabilities-hyperlinks #:tui-hyperlink #:tui-detect-capabilities
    #:tui-get-capabilities #:tui-get-terminal-color-mode #:tui-reset-capabilities-cache #:tui-set-capability-overrides #:tui-set-capabilities
    #:tui-error #:tui-error-message #:tui-indexed-color #:tui-rgb-color #:tui-oklch-color #:tui-okhsl-color #:tui-parse-color #:tui-color-to-rgb
    #:tui-color-to-oklch #:tui-color-to-okhsl #:tui-color-to-hex #:tui-mix-colors #:tui-foreground-ansi #:tui-background-ansi #:tui-style-text
    #:tui-style-text-with-ansi #:tui-color-ansi #:tui-wire-color #:format-key-text #:key-text #:key-display-text #:key-hint #:raw-key-hint
    #:get-markdown-theme #:get-select-list-theme #:get-settings-list-theme #:get-editor-theme #:highlight-code #:init-theme #:markdown-theme-heading
    #:markdown-theme-link #:markdown-theme-link-url #:markdown-theme-code #:markdown-theme-code-block #:markdown-theme-code-block-border
    #:markdown-theme-quote #:markdown-theme-quote-border #:markdown-theme-hr #:markdown-theme-list-bullet #:markdown-theme-bold #:markdown-theme-italic
    #:markdown-theme-underline #:markdown-theme-strikethrough #:markdown-theme-highlight-code #:select-list-theme-selected-prefix
    #:select-list-theme-selected-text #:select-list-theme-description #:select-list-theme-scroll-info #:select-list-theme-no-match
    #:settings-list-theme-label #:settings-list-theme-value #:settings-list-theme-description #:settings-list-theme-cursor #:settings-list-theme-hint
    #:editor-theme-border-color #:editor-theme-select-list
   ;; ==== end lane H ====
   ;; ==== lane C: pi-coding-agent pure helpers ====
   ;; ==== end lane C ====
   ;; ==== lane M: model calls and streams, model types ====
   ;; ==== end lane M ====
   ;; ==== lane S2: session manager, settings manager, resource loader, package manager, project trust ====
   ;; ==== end lane S2 ====
   ;; ==== lane S3: model runtime and registry, create-provider, extension runner and factories, MCP options, event bus, builtin tools ====
   ;; ==== end lane S3 ====
   ;; ==== lane S1: session operations, child sessions, agent session runtime and services ====
   ;; ==== end lane S1 ====
   ;; ==== lane P: providers, OAuth, renderers, virtual models, MCP registration ====
   ;; ==== end lane P ====
   ;; ==== lane U1: component kit, ui.custom, overlays, widgets, theme ====
   ;; ==== end lane U1 ====
   ;; ==== lane U2: editor components, autocomplete providers, terminal input ====
   ;; ==== end lane U2 ====
   ))
