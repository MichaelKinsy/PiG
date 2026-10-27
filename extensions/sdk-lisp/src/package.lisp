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
   ;; ==== end lane T ====
   ;; ==== lane H: pi-tui helpers ====
   ;; ==== end lane H ====
   ;; ==== lane C: pi-coding-agent pure helpers ====
   ;; ==== end lane C ====
   ;; ==== lane M: model calls and streams, model types ====
   #:ctx-complete #:registry-stream #:registry-stream-simple #:registry-complete #:registry-classify #:registry-generate-images
   #:registry-get-all #:registry-get-available #:registry-get-error #:registry-get-models-of-type #:registry-get-model-of-type
   #:registry-find-of-type #:registry-get-api-key-and-headers #:registry-get-api-key-for-provider #:registry-get-provider-auth-status
   #:registry-get-provider-display-name #:registry-get-registered-provider-ids #:registry-get-registered-provider-config
   #:registry-register-provider #:registry-unregister-provider #:registry-register-virtual-model #:registry-unregister-virtual-model
   #:model-event-stream #:make-model-event-stream #:create-assistant-message-event-stream
   #:model-stream-push #:model-stream-end #:model-stream-result #:model-stream-events #:model-stream-next
   ;; ==== end lane M ====
   ;; ==== lane S2: session manager, settings manager, resource loader, package manager, project trust ====
   ;; ==== end lane S2 ====
   ;; ==== lane S3: model runtime and registry, create-provider, extension runner and factories, MCP options, event bus, builtin tools ====
   ;; ==== end lane S3 ====
   ;; ==== lane S1: session operations, child sessions, agent session runtime and services ====
   ;; ==== end lane S1 ====
   ;; ==== lane P: providers, OAuth, renderers, virtual models, MCP registration ====
   #:abort-signal-subscribe #:make-abort-signal #:signal-abort
   #:registry-get-provider
   #:auth-context #:make-auth-context #:auth-context-env #:auth-context-file-exists
   #:api-key-auth-input #:make-api-key-auth-input #:api-key-auth-input-ctx #:api-key-auth-input-credential #:api-key-auth-input-signal
   #:auth-result #:make-auth-result #:auth-result-auth #:auth-result-env #:auth-result-source
   #:auth-check #:make-auth-check #:auth-check-type #:auth-check-source
   #:auth-interaction #:make-auth-interaction #:auth-interaction-prompt #:auth-interaction-notify #:auth-interaction-signal
   #:login-options #:make-login-options #:login-options-get-device-id #:login-options-agent-name
   #:api-key-auth #:make-api-key-auth #:api-key-auth-name #:api-key-auth-resolve #:api-key-auth-check #:api-key-auth-login
   #:oauth-auth #:make-oauth-auth #:oauth-auth-name #:oauth-auth-login #:oauth-auth-refresh #:oauth-auth-to-auth
   #:oauth-auth-is-subscription #:oauth-auth-login-label
   #:provider-auth #:make-provider-auth #:provider-auth-api-key #:provider-auth-oauth
   #:provider-stream-options #:make-provider-stream-options #:provider-stream-options-values #:provider-stream-options-signal
   #:provider-stream-options-on-payload #:provider-stream-options-on-response #:provider-stream-options-on-provider-stream-event
   #:provider-operation-options #:make-provider-operation-options #:provider-operation-options-values #:provider-operation-options-signal
   #:models-publication #:make-models-publication #:models-publication-persist #:models-publication-update
   #:refresh-models-context #:make-refresh-models-context #:refresh-models-context-publish #:refresh-models-context-credential
   #:refresh-models-context-stored #:refresh-models-context-allow-network #:refresh-models-context-force
   #:refresh-models-context-signal #:refresh-models-context-base-url
   #:provider #:provider-p #:make-provider #:provider-id #:provider-name #:provider-auth #:provider-get-models #:provider-stream
   #:provider-stream-simple #:provider-base-url #:provider-headers #:provider-filter-models #:provider-get-all-models
   #:provider-filter-all-models #:provider-refresh-models #:provider-fetch-deferred #:provider-cancel-deferred
   #:provider-generate-images #:provider-classify #:provider-handle
   #:register-native-provider #:register-provider #:unregister-provider #:register-oauth-provider
   #:ctx-register-provider #:ctx-unregister-provider #:ctx-register-native-provider
   #:oauth-cancelled #:oauth-credentials #:oauth-auth-info #:make-oauth-auth-info #:oauth-device-code-info #:make-oauth-device-code-info
   #:oauth-prompt #:make-oauth-prompt #:oauth-select-option #:make-oauth-select-option #:oauth-select-prompt #:make-oauth-select-prompt
   #:oauth-credential-status #:make-oauth-credential-status #:oauth-credential-store #:make-oauth-credential-store
   #:oauth-provider #:make-oauth-provider #:oauth-on-auth #:oauth-on-device-code #:oauth-on-progress #:oauth-on-prompt
   #:oauth-on-select #:oauth-on-manual-code-input
   #:virtual-model #:make-virtual-model #:register-virtual-model #:unregister-virtual-model
   #:ctx-register-virtual-model #:ctx-unregister-virtual-model
   #:register-mcp-server #:unregister-mcp-server #:ctx-register-mcp-server #:ctx-unregister-mcp-server #:ctx-get-mcp-servers
   #:tool-renderers #:make-tool-renderers #:tool-renderers-render-call #:tool-renderers-render-result #:tool-renderers-render-shell
   #:tool-renderers-call-view #:tool-renderers-result-view
   #:tool-render-context #:tool-render-context-args #:tool-render-context-tool-call-id #:tool-render-context-cwd
   #:tool-render-context-execution-started #:tool-render-context-args-complete #:tool-render-context-is-partial
   #:tool-render-context-expanded #:tool-render-context-show-images #:tool-render-context-is-error #:tool-render-context-output-pad
   #:tool-render-context-duration-ms #:tool-render-context-state #:tool-render-context-invalidate
   #:tool-renderer #:message-renderer #:message-view-renderer #:entry-renderer #:entry-view-renderer #:markdown-transformer
   #:login-definition #:make-login-definition #:sprite-definition #:make-sprite-definition #:ctx-set-login #:ctx-register-sprite
   ;; ==== end lane P ====
   ;; ==== lane U1: component kit, ui.custom, overlays, widgets, theme ====
   ;; ==== end lane U1 ====
   ;; ==== lane U2: editor components, autocomplete providers, terminal input ====
   ;; ==== end lane U2 ====
   ))
