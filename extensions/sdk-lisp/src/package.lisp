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
   #:abort-signal #:abort-signal-set-p #:abort-signal-wait #:make-abort-signal #:signal-abort #:abort-signal-subscribe
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
   #:ctx-model #:ctx-model-provider #:ctx-model-qualified #:ctx-session-name #:ctx-get-session-name #:ctx-get-session-file
   #:ctx-extension-source-info #:ctx-get-settings #:ctx-config-home #:ctx-tool-call-index #:ctx-tool-input #:ctx-tool-params
   #:ctx-tools #:ctx-execute-tool #:ctx-set-editor-text #:ctx-paste-to-editor #:ctx-refresh-tools #:ctx-get-entries #:ctx-get-branch
   #:register-tool #:ctx-register-tool #:tool-loadout #:tool-loadout-declared #:tool-loadout-callable #:tool-loadout-registered #:tool-loadout-exposures
   #:tool-loadout-namespaces #:tool-loadout-prompt-guidelines
   #:tool-loadout-get-exposure #:tool-loadout-get-namespace #:tool-loadout-get-prompt-guidelines #:on-project-trust
   #:bash-operations #:make-bash-operations #:bash-operations-exec #:bash-exec-options #:make-bash-exec-options
   #:bash-exec-options-on-data #:bash-exec-options-signal #:bash-exec-options-timeout #:bash-exec-options-env
   #:ctx-footer-data #:footer-get-git-branch #:footer-get-extension-statuses
   #:footer-get-available-provider-count #:footer-on-branch-change
   ;; ==== lane T: type engine, tool schema, pi-ai ====
   #:type-schema-error #:type-schema-error-message #:big-int #:make-big-int #:big-int-p #:big-int-value #:type-to-json
   #:type-refinement #:make-type-refinement #:type-refinement-p #:type-refinement-check #:type-refinement-message
   #:type-schema-p #:type-kind-p #:type-optional-p #:type-readonly-p #:type-immutable-p #:type-unsafe-p #:type-enum-value-p #:type-refine-p
   #:type-any-p #:type-array-p #:type-big-int-p #:type-boolean-p #:type-enum-p #:type-integer-p #:type-intersect-p #:type-literal-p
   #:type-never-p #:type-null-p #:type-number-p #:type-object-p #:type-record-p #:type-ref-p #:type-string-p #:type-symbol-p
   #:type-undefined-p #:type-union-p #:type-unknown-p #:type-void-p #:type-call-p #:type-constructor-p #:type-cyclic-p #:type-dependent-p
   #:type-function-p #:type-generic-p #:type-identifier-p #:type-infer-p #:type-parameter-p #:type-rest-p #:type-template-literal-p
   #:type-this-p #:type-tuple-p
   #:type-any #:type-unknown #:type-string #:type-number #:type-integer #:type-boolean #:type-null #:type-never #:type-undefined #:type-void
   #:type-symbol #:type-big-int #:type-ref #:type-array #:type-union #:type-intersect #:type-object #:type-optional #:type-readonly
   #:type-immutable #:type-unsafe #:type-refine #:type-with #:type-literal #:type-enum #:type-record
   #:type-call #:type-capitalize #:type-lowercase #:type-uncapitalize #:type-uppercase #:type-conditional #:type-constructor
   #:type-constructor-parameters #:type-parameters #:type-instance-type #:type-return-type #:type-cyclic #:type-dependent #:type-evaluate
   #:type-exclude #:type-extract #:type-extends #:type-function #:type-generic #:type-identifier #:type-index #:type-infer #:type-instantiate
   #:type-interface #:type-key-of #:type-mapped #:type-module #:type-non-nullable #:type-omit #:type-pick #:type-parameter #:type-partial
   #:type-required #:type-readonly-object #:type-readonly-type #:type-record-key #:type-record-pattern #:type-record-value #:type-rest
   #:type-script #:type-template-literal #:type-this #:type-tuple
   #:type-extends-true #:type-extends-union #:type-extends-false #:type-extends-true-p #:type-extends-union-p #:type-extends-false-p
   #:type-extends-true-like-p #:type-extends-match #:running-extension
   #:+default-max-agent-retry-delay-ms+ #:string-enum #:get-supported-thinking-levels #:clamp-thinking-level #:calculate-cost #:content-text
   #:get-current-tools #:get-current-system-message #:get-system-message-text #:get-current-system-prompt #:render-system-message-update
   #:create-initial-system-message #:normalize-context #:get-initial-system-message #:without-initial-system-message
   #:collapse-system-messages #:resolve-transcript #:get-declared-tools #:has-non-additive-tool-changes-p #:resolve-transcript-tools
   #:retry-delay-ms #:retryable-assistant-error-p #:context-overflow-p #:recoverable-length-p #:env-api-key-auth #:get-model-type
   #:model-type-p #:has-api-p #:models-are-equal-p
   ;; ==== end lane T ====
   ;; ==== lane H: pi-tui helpers ====
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
