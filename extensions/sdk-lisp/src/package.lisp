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
   ;; ==== end lane H ====
   ;; ==== lane C: pi-coding-agent pure helpers ====
   ;; ==== end lane C ====
   ;; ==== lane M: model calls and streams, model types ====
   ;; ==== end lane M ====
   ;; ==== lane S2: session manager, settings manager, resource loader, package manager, project trust ====
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
   ;; ==== end lane U2 ====
   ))
