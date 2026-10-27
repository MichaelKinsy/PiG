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
   ;; ==== end lane S3 ====
   ;; ==== lane S1: session operations, child sessions, agent session runtime and services ====
   #:ctx-new-session #:ctx-fork #:ctx-navigate-tree #:ctx-switch-session
   #:setup-manager #:setup-append-message #:setup-append-custom-entry #:setup-append-custom-message-entry
   #:setup-append-session-info #:setup-append-model-change #:setup-append-thinking-level-change #:setup-append-label-change
   #:ctx-create-agent-session #:child-tool #:make-resource-options #:make-session-start-event
   #:agent-session #:agent-session-prompt #:agent-session-steer #:agent-session-follow-up #:agent-session-abort
   #:agent-session-bind-extensions #:agent-session-state #:agent-session-messages #:agent-session-refresh-context
   #:agent-session-session-manager #:agent-session-subscribe #:agent-session-dispose
   #:agent-session-session-id #:agent-session-session-file #:agent-session-model #:agent-session-thinking-level
   #:agent-session-active-tools #:agent-session-system-prompt #:agent-session-model-fallback-message #:agent-session-extension-errors
   #:agent-session-services #:make-agent-session-services #:agent-session-services-cwd #:agent-session-services-agent-dir
   #:agent-session-services-model-runtime #:agent-session-services-settings-manager #:agent-session-services-resource-loader
   #:agent-session-services-diagnostics #:create-agent-session-services #:create-agent-session-from-services
   #:session-runtime-options #:make-session-runtime-options #:session-runtime-options-cwd #:session-runtime-options-agent-dir
   #:session-runtime-options-session-manager #:session-runtime-options-session-start-event
   #:session-runtime-result #:make-session-runtime-result
   #:agent-session-runtime #:create-agent-session-runtime #:agent-session-runtime-session #:agent-session-runtime-services
   #:agent-session-runtime-cwd #:agent-session-runtime-diagnostics #:agent-session-runtime-model-fallback-message
   #:agent-session-runtime-set-rebind-session #:agent-session-runtime-set-before-session-invalidate
   #:agent-session-runtime-switch-session #:agent-session-runtime-new-session #:agent-session-runtime-fork
   #:agent-session-runtime-import-from-jsonl #:agent-session-runtime-dispose
   #:session-import-file-not-found-error #:session-import-file-not-found-error-file-path
   #:missing-session-cwd-error #:missing-session-cwd-error-session-file #:missing-session-cwd-error-session-cwd
   #:missing-session-cwd-error-fallback-cwd
   ;; ==== end lane S1 ====
   ;; ==== lane P: providers, OAuth, renderers, virtual models, MCP registration ====
   ;; ==== end lane P ====
   ;; ==== lane U1: component kit, ui.custom, overlays, widgets, theme ====
   ;; ==== end lane U1 ====
   ;; ==== lane U2: editor components, autocomplete providers, terminal input ====
   ;; ==== end lane U2 ====
   ))
