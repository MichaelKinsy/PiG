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
