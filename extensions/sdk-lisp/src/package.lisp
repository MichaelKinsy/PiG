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
    #:has-trust-requiring-project-resources #:host-agent-dir #:host-config-dir-name #:host-info-unavailable
    #:host-info-unavailable-message #:host-package-dir #:host-shell-config #:host-version #:loaded-extension-loader
    #:loader-extend-resources #:loader-get-agents-files #:loader-get-append-system-prompt #:loader-get-append-system-prompt-sources
    #:loader-get-extensions #:loader-get-prompts #:loader-get-skills #:loader-get-system-prompt #:loader-get-system-prompt-source
    #:loader-get-themes #:loader-load-project-trust-extensions #:loader-reload #:make-package-manager #:make-project-trust-store
    #:make-resource-loader #:pkgmgr-add-source-to-settings #:pkgmgr-check-for-available-updates #:pkgmgr-get-installed-path
    #:pkgmgr-install #:pkgmgr-install-and-persist #:pkgmgr-list-configured-packages #:pkgmgr-remove #:pkgmgr-remove-and-persist
    #:pkgmgr-remove-source-from-settings #:pkgmgr-resolve #:pkgmgr-resolve-extension-sources #:pkgmgr-set-progress-callback
    #:pkgmgr-update #:settings-apply-overrides #:settings-create #:settings-drain-errors #:settings-flush #:settings-from-storage
    #:settings-get-all-model-thinking-levels #:settings-get-autocomplete-max-visible #:settings-get-block-images
    #:settings-get-branch-summary-settings #:settings-get-branch-summary-skip-prompt #:settings-get-cache-warming-mode
    #:settings-get-clear-on-shrink #:settings-get-code-block-indent #:settings-get-collapse-changelog
    #:settings-get-compaction-enabled #:settings-get-compaction-keep-recent-tokens #:settings-get-compaction-reserve-tokens
    #:settings-get-compaction-settings #:settings-get-default-model #:settings-get-default-project-trust
    #:settings-get-default-provider #:settings-get-default-thinking-level #:settings-get-default-tools
    #:settings-get-double-escape-action #:settings-get-editor-padding-x #:settings-get-enable-analytics
    #:settings-get-enable-install-telemetry #:settings-get-enable-skill-commands #:settings-get-enabled-models
    #:settings-get-extension-paths #:settings-get-external-editor-command #:settings-get-follow-up-mode
    #:settings-get-fullscreen-copy-on-select #:settings-get-fullscreen-exit-output #:settings-get-fullscreen-scrollbar
    #:settings-get-fullscreen-wheel-scroll-lines #:settings-get-global-settings #:settings-get-hide-thinking-block
    #:settings-get-http-idle-timeout-ms #:settings-get-image-auto-resize #:settings-get-image-width-cells
    #:settings-get-last-changelog-version #:settings-get-mermaid-rendering-mode #:settings-get-model-thinking-level
    #:settings-get-npm-command #:settings-get-or-create-device-id #:settings-get-output-pad #:settings-get-packages
    #:settings-get-project-settings #:settings-get-prompt-template-paths #:settings-get-provider-retry-settings
    #:settings-get-quiet-startup #:settings-get-retry-enabled #:settings-get-retry-settings #:settings-get-session-dir
    #:settings-get-session-share-destination #:settings-get-settings #:settings-get-shell-command-prefix #:settings-get-shell-path
    #:settings-get-show-cache-miss-notices #:settings-get-show-hardware-cursor #:settings-get-show-images
    #:settings-get-show-terminal-progress #:settings-get-skill-paths #:settings-get-steering-mode
    #:settings-get-terminal-capability-overrides #:settings-get-theme #:settings-get-theme-paths #:settings-get-theme-setting
    #:settings-get-thinking-budgets #:settings-get-tracking-id #:settings-get-transport #:settings-get-tree-filter-mode
    #:settings-get-tui-mode #:settings-get-warnings #:settings-get-web-socket-connect-timeout-ms #:settings-in-memory
    #:settings-is-project-trusted #:settings-manager #:settings-reload #:settings-remove-model-thinking-level
    #:settings-set-autocomplete-max-visible #:settings-set-block-images #:settings-set-cache-warming-mode
    #:settings-set-clear-on-shrink #:settings-set-collapse-changelog #:settings-set-compaction-enabled #:settings-set-default-model
    #:settings-set-default-model-and-provider #:settings-set-default-project-trust #:settings-set-default-provider
    #:settings-set-default-thinking-level #:settings-set-double-escape-action #:settings-set-editor-padding-x
    #:settings-set-enable-analytics #:settings-set-enable-install-telemetry #:settings-set-enable-skill-commands
    #:settings-set-enabled-models #:settings-set-extension-paths #:settings-set-follow-up-mode
    #:settings-set-fullscreen-copy-on-select #:settings-set-fullscreen-exit-output #:settings-set-fullscreen-scrollbar
    #:settings-set-fullscreen-wheel-scroll-lines #:settings-set-hide-thinking-block #:settings-set-http-idle-timeout-ms
    #:settings-set-image-auto-resize #:settings-set-image-width-cells #:settings-set-last-changelog-version
    #:settings-set-mermaid-rendering-mode #:settings-set-model-thinking-level #:settings-set-npm-command #:settings-set-output-pad
    #:settings-set-packages #:settings-set-project-extension-paths #:settings-set-project-packages
    #:settings-set-project-prompt-template-paths #:settings-set-project-skill-paths #:settings-set-project-theme-paths
    #:settings-set-project-trusted #:settings-set-prompt-template-paths #:settings-set-quiet-startup #:settings-set-retry-enabled
    #:settings-set-session-share-destination #:settings-set-shell-command-prefix #:settings-set-shell-path
    #:settings-set-show-cache-miss-notices #:settings-set-show-hardware-cursor #:settings-set-show-images
    #:settings-set-show-terminal-progress #:settings-set-skill-paths #:settings-set-steering-mode #:settings-set-theme
    #:settings-set-theme-paths #:settings-set-transport #:settings-set-tree-filter-mode #:settings-set-tui-mode
    #:settings-set-warnings #:shell-config #:shell-config-args #:shell-config-command-transport #:shell-config-shell #:sm
    #:sm-append-compaction #:sm-append-context-edit #:sm-append-custom-entry #:sm-append-custom-message-entry
    #:sm-append-label-change #:sm-append-message #:sm-append-model-change #:sm-append-session-info #:sm-append-thinking-level-change
    #:sm-append-usage #:sm-branch #:sm-branch-with-summary #:sm-build-context-entries #:sm-build-session-context
    #:sm-build-session-projection #:sm-continue-recent #:sm-create #:sm-create-branched-session #:sm-find-by-id #:sm-fork-from
    #:sm-get-branch #:sm-get-children #:sm-get-cwd #:sm-get-entries #:sm-get-entry #:sm-get-entry-count #:sm-get-header
    #:sm-get-label #:sm-get-leaf-entry #:sm-get-leaf-id #:sm-get-session-dir #:sm-get-session-file #:sm-get-session-id
    #:sm-get-session-name #:sm-get-tree #:sm-in-memory #:sm-is-persisted #:sm-list #:sm-list-all #:sm-new-session #:sm-open
    #:sm-reset-leaf #:sm-set-session-file #:sm-uses-default-session-dir #:trust-store-get #:trust-store-get-entry #:trust-store-set
    #:trust-store-set-many
   ;; ==== end lane S2 ====
   ;; ==== lane S3: model runtime and registry, create-provider, extension runner and factories, MCP options, event bus, builtin tools ====
    #:bash-tool-options #:builtin-tool #:builtin-tool-constrained-sampling #:builtin-tool-definition #:builtin-tool-definition-constrained-sampling
    #:builtin-tool-definition-description #:builtin-tool-definition-execute #:builtin-tool-definition-execute-with
    #:builtin-tool-definition-execution-mode #:builtin-tool-definition-label #:builtin-tool-definition-name #:builtin-tool-definition-output-schema
    #:builtin-tool-definition-parameters #:builtin-tool-definition-prepare-arguments #:builtin-tool-definition-prompt-guidelines
    #:builtin-tool-definition-prompt-snippet #:builtin-tool-definition-render-shell #:builtin-tool-description #:builtin-tool-execute
    #:builtin-tool-execution-mode #:builtin-tool-label #:builtin-tool-name #:builtin-tool-output-schema #:builtin-tool-parameters
    #:builtin-tool-prompt-guidelines #:builtin-tool-prompt-snippet #:copy-builtin-tool-definition #:create-bash-tool #:create-bash-tool-definition
    #:create-codemode-extension #:create-coding-tools #:create-edit-tool #:create-edit-tool-definition #:create-extension-runtime #:create-find-tool
    #:create-find-tool-definition #:create-grep-tool #:create-grep-tool-definition #:create-local-bash-operations #:create-local-powershell-operations
    #:create-ls-tool #:create-ls-tool-definition #:create-mcp-extension #:create-powershell-tool #:create-powershell-tool-definition #:create-provider
    #:create-read-only-tools #:create-read-tool #:create-read-tool-definition #:create-tool-search-extension #:create-write-tool
    #:create-write-tool-definition #:ctx-events #:edit-operations #:edit-tool-options #:event-bus #:events-emit #:events-on #:extension-events
    #:extension-factory #:extension-factory-p #:extension-factory-wire #:extension-runner #:extension-runtime #:extension-runtime-assert-active
    #:extension-runtime-get-flag-values #:extension-runtime-invalidate #:extension-runtime-mcp-servers
    #:extension-runtime-pending-native-provider-registrations #:extension-runtime-pending-provider-registrations
    #:extension-runtime-pending-virtual-model-registrations #:extension-runtime-set-flag-value #:find-operations #:find-tool-options #:grep-operations
    #:grep-tool-options #:loaded-extensions-runtime #:ls-operations #:ls-stat #:ls-tool-options #:make-bash-tool-options
    #:make-builtin-tool-definition #:make-edit-operations #:make-edit-tool-options #:make-extension-runner #:make-find-operations
    #:make-find-tool-options #:make-grep-operations #:make-grep-tool-options #:make-ls-operations #:make-ls-stat #:make-ls-tool-options
    #:make-mcp-credential-store #:make-mcp-server-store #:make-mcp-transport #:make-model-image-resize-options #:make-power-shell-tool-options
    #:make-provider-streams #:make-read-operations #:make-read-tool-options #:make-runtime-registry #:make-tools-options #:make-write-operations
    #:make-write-tool-options #:mcp-auth-on-unauthorized #:mcp-auth-provider #:mcp-auth-token #:mcp-credential-store #:mcp-server-store
    #:mcp-transport #:mcp-transport-emit-close #:mcp-transport-emit-error #:mcp-transport-emit-message #:mcp-transport-on-close
    #:mcp-transport-on-error #:mcp-transport-on-message #:model-image-resize-options #:model-runtime #:power-shell-tool-options #:provider-streams
    #:read-operations #:read-tool-options #:register-builtin-tool-definition #:runner-bind-command-context #:runner-bind-core #:runner-context
    #:runner-context-abort #:runner-context-compact #:runner-context-cwd #:runner-context-execute-tool #:runner-context-fork
    #:runner-context-get-context-usage #:runner-context-get-system-prompt #:runner-context-get-system-prompt-options
    #:runner-context-has-pending-messages-p #:runner-context-has-ui-p #:runner-context-is-idle-p #:runner-context-is-project-trusted-p
    #:runner-context-mode #:runner-context-model #:runner-context-model-registry #:runner-context-navigate-tree #:runner-context-new-session
    #:runner-context-reload #:runner-context-scoped-models #:runner-context-session-manager #:runner-context-shutdown #:runner-context-signal
    #:runner-context-switch-session #:runner-context-thinking-level #:runner-context-tools #:runner-context-ui #:runner-context-wait-for-idle
    #:runner-create-command-context #:runner-create-context #:runner-create-tool-context #:runner-emit #:runner-emit-before-agent-start
    #:runner-emit-before-provider-headers #:runner-emit-before-provider-request #:runner-emit-boundary #:runner-emit-cache-warming-decision
    #:runner-emit-context #:runner-emit-error #:runner-emit-input #:runner-emit-message-end #:runner-emit-resources-discover #:runner-emit-tool-call
    #:runner-emit-tool-result #:runner-emit-user-bash #:runner-get-active-tools #:runner-get-all-registered-tools #:runner-get-command
    #:runner-get-command-diagnostics #:runner-get-entry-renderer-p #:runner-get-extension-paths #:runner-get-flag-values #:runner-get-flags
    #:runner-get-markdown-transformers #:runner-get-message-renderer-p #:runner-get-model-registry #:runner-get-registered-commands
    #:runner-get-shortcut-diagnostics #:runner-get-shortcuts #:runner-get-tool-definition #:runner-get-ui-context #:runner-has-handlers-p
    #:runner-has-ui-p #:runner-invalidate #:runner-on-error #:runner-report-unhandled-mcp-servers #:runner-resolve-tool-renderers
    #:runner-set-flag-value #:runner-set-ui-context #:runner-shutdown #:runner-ui #:runner-ui-confirm #:runner-ui-editor #:runner-ui-get-editor-text
    #:runner-ui-get-tools-expanded-p #:runner-ui-input #:runner-ui-notify #:runner-ui-paste-to-editor #:runner-ui-select #:runner-ui-set-editor-text
    #:runner-ui-set-hidden-thinking-label #:runner-ui-set-status #:runner-ui-set-title #:runner-ui-set-tools-expanded #:runner-ui-set-widget
    #:runner-ui-set-working-message #:runner-ui-set-working-visible #:runtime-check-auth #:runtime-classify #:runtime-complete
    #:runtime-complete-simple #:runtime-create #:runtime-generate-images #:runtime-get-all-available #:runtime-get-all-models #:runtime-get-auth
    #:runtime-get-available #:runtime-get-available-of-type #:runtime-get-available-snapshot #:runtime-get-compatibility-request-config
    #:runtime-get-error #:runtime-get-model #:runtime-get-model-of-type #:runtime-get-models #:runtime-get-models-of-type #:runtime-get-physical-model
    #:runtime-get-provider #:runtime-get-provider-auth-status #:runtime-get-providers #:runtime-get-registered-native-provider
    #:runtime-get-registered-provider-config #:runtime-get-registered-provider-ids #:runtime-has-configured-auth-p #:runtime-is-using-oauth-p
    #:runtime-is-using-subscription-p #:runtime-list-credentials #:runtime-login #:runtime-logout #:runtime-provider-display-name #:runtime-refresh
    #:runtime-register-native-provider #:runtime-register-provider #:runtime-register-virtual-model #:runtime-registry #:runtime-registry-classify
    #:runtime-registry-complete #:runtime-registry-find #:runtime-registry-find-of-type #:runtime-registry-generate-images #:runtime-registry-get-all
    #:runtime-registry-get-api-key-and-headers #:runtime-registry-get-api-key-for-provider #:runtime-registry-get-available
    #:runtime-registry-get-available-of-type #:runtime-registry-get-error #:runtime-registry-get-model-of-type #:runtime-registry-get-models-of-type
    #:runtime-registry-get-provider #:runtime-registry-get-provider-auth #:runtime-registry-get-provider-auth-status
    #:runtime-registry-get-provider-display-name #:runtime-registry-get-registered-native-provider #:runtime-registry-get-registered-provider-config
    #:runtime-registry-get-registered-provider-ids #:runtime-registry-has-configured-auth-p #:runtime-registry-is-using-oauth-p
    #:runtime-registry-refresh #:runtime-registry-register-native-provider #:runtime-registry-register-provider
    #:runtime-registry-register-virtual-model #:runtime-registry-stream #:runtime-registry-stream-simple #:runtime-registry-unregister-provider
    #:runtime-registry-unregister-virtual-model #:runtime-remove-runtime-api-key #:runtime-resolve-model #:runtime-set-runtime-api-key
    #:runtime-stream #:runtime-stream-simple #:runtime-unregister-provider #:runtime-unregister-virtual-model #:tools-options #:write-operations
    #:write-tool-options
   ;; ==== end lane S3 ====
   ;; ==== lane S1: session operations, child sessions, agent session runtime and services ====
   ;; ==== end lane S1 ====
   ;; ==== lane P: providers, OAuth, renderers, virtual models, MCP registration ====
   ;; ==== end lane P ====
   ;; ==== lane U1: component kit, ui.custom, overlays, widgets, theme ====
   ;; ==== end lane U1 ====
   ;; ==== lane U2: editor components, autocomplete providers, terminal input ====
   #:ctx-set-editor-component #:ctx-clear-editor-component #:ctx-get-editor-component
   #:editor-status #:make-editor-status #:editor-status-kind #:editor-status-message #:editor-status-frames #:editor-status-frame
   #:editor-status-spinner-color #:editor-status-message-color #:editor-status-indicator-verbatim
   #:editor-component #:make-editor-component #:editor-component-base #:editor-component-embed-working-status
   #:editor-component-handle-input #:editor-component-handle-mouse #:editor-component-render #:editor-component-set-text
   #:editor-component-insert-text-at-cursor #:editor-component-add-to-history #:editor-component-set-working-status-indicator
   #:editor-component-invalidate
   #:editor-base #:editor-base-on-escape #:editor-base-on-ctrl-d #:editor-base-on-paste-image #:editor-base-on-extension-shortcut
   #:editor-base-action-handlers #:editor-base-on-action
   #:editor-base-handle-input #:editor-base-handle-mouse #:editor-base-render #:editor-base-set-text #:editor-base-insert-text-at-cursor
   #:editor-base-add-to-history #:editor-base-get-text #:editor-base-get-expanded-text #:editor-base-get-lines #:editor-base-get-cursor
   #:editor-base-is-showing-autocomplete-p #:editor-base-set-padding-x #:editor-base-set-autocomplete-max-visible
   #:editor-base-get-padding-x #:editor-base-get-autocomplete-max-visible #:editor-base-border-color
   #:editor-base-disable-submit #:editor-base-focused #:editor-base-set-autocomplete-provider #:editor-base-on-submit #:editor-base-on-change
   #:editor-base-set-working-status-indicator #:editor-base-invalidate
   #:ctx-add-autocomplete-provider #:autocomplete-provider #:make-autocomplete-provider #:autocomplete-provider-trigger-characters
   #:autocomplete-get-suggestions #:autocomplete-apply-completion #:autocomplete-should-trigger-file-completion
   #:ctx-on-terminal-input #:terminal-input-result #:make-terminal-input-result #:terminal-input-result-consume #:terminal-input-result-data
   ;; ==== end lane U2 ====
   ))
