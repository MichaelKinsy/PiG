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
   ;; kit: nodes
   #:kit-container #:kit-container-add-child #:kit-box #:kit-box-add-child #:kit-text #:kit-truncated-text #:kit-markdown #:kit-text-style
   #:kit-spacer #:kit-dynamic-border #:kit-select-item #:kit-select-layout #:kit-select-list #:kit-setting-item #:kit-settings-list
   #:kit-image #:kit-image-ref #:kit-loader #:kit-loader-indicator #:kit-hstack #:kit-vstack #:kit-stack-entry #:kit-stack-add-child
   #:kit-list #:kit-list-item #:kit-lines #:kit-lines-image #:kit-lines-progress
   #:kit-user-message #:kit-user-message-set-output-pad
   #:kit-assistant-message #:kit-assistant-message-update-content #:kit-assistant-message-set-hide-thinking-block
   #:kit-assistant-message-set-hidden-thinking-label #:kit-assistant-message-set-output-pad
   #:kit-message #:kit-text-block #:kit-thinking-block #:kit-tool-call-block
   #:kit-text-content #:kit-image-content #:kit-tool-result #:*kit-tool-definition-builtin* #:*kit-tool-definition-empty*
   #:kit-tool-execution #:kit-tool-execution-update-args #:kit-tool-execution-mark-execution-started #:kit-tool-execution-set-args-complete
   #:kit-tool-execution-update-result #:kit-tool-execution-set-expanded #:kit-tool-execution-set-show-images
   #:kit-tool-execution-set-image-width-cells #:kit-tool-execution-set-output-pad
   #:kit-bash-execution #:kit-bash-execution-append-output #:kit-bash-execution-set-complete #:kit-bash-execution-set-expanded
   #:kit-bash-execution-set-output-pad #:kit-diff
   #:kit-branch-summary #:kit-branch-summary-set-expanded #:kit-branch-summary-set-output-pad
   #:kit-compaction-summary #:kit-compaction-summary-set-expanded #:kit-compaction-summary-set-output-pad
   #:kit-skill-invocation #:kit-skill-invocation-set-expanded #:kit-skill-invocation-set-output-pad
   #:kit-custom-message #:kit-custom-message-set-expanded #:kit-custom-message-set-output-pad
   #:kit-cancellable-loader #:kit-bordered-loader #:kit-input #:kit-slash-command #:kit-autocomplete #:kit-editor
   #:kit-scroll-view #:kit-mouse-region
   ;; kit: views, events and the view renderers' result
   #:kit-view #:kit-render-result #:kit-event-node #:kit-event-type #:kit-event-index #:kit-event-item #:kit-event-id #:kit-event-value
   #:kit-event-cursor #:kit-event-mouse
   ;; ui.custom and overlays
   #:make-custom-component #:make-custom-result #:ctx-custom #:make-mouse-event #:mouse-event-type #:mouse-event-button #:mouse-event-x
   #:mouse-event-y #:mouse-event-screen-x #:mouse-event-screen-y #:mouse-event-width #:mouse-event-height #:mouse-event-shift
   #:mouse-event-alt #:mouse-event-ctrl #:mouse-event-wheel-delta #:mouse-event-click-count
   #:overlay-handle #:overlay-hide #:overlay-set-hidden #:overlay-hidden-p #:overlay-focus #:overlay-unfocus #:overlay-focused-p
   #:overlay-get-bounds
   ;; widgets, footer, header, working indicator
   #:ctx-width #:ctx-height #:ctx-set-working-message #:ctx-set-working-visible #:ctx-set-working-indicator
   #:ctx-set-hidden-thinking-label #:ctx-set-tools-expanded #:ctx-bell
   #:ctx-set-widget #:ctx-set-widget-renderer #:ctx-set-footer #:ctx-clear-footer #:ctx-set-footer-renderer #:ctx-set-footer-view
   #:ctx-set-header #:ctx-clear-header #:ctx-set-header-renderer #:ctx-set-header-view
   ;; theme
   #:theme #:make-theme-from-palette #:theme-name #:theme-source-path #:theme-source-info #:theme-appearance #:theme-colors
   #:theme-fg #:theme-bg #:theme-bold #:theme-italic #:theme-underline #:theme-inverse #:theme-strikethrough #:theme-get-fg-ansi
   #:theme-get-bg-ansi #:theme-get-color-mode #:theme-style #:theme-get-thinking-border-color #:theme-get-bash-mode-border-color
   #:ctx-theme #:ctx-on-theme-change #:ctx-on-tools-expanded-change
   ;; ==== end lane U1 ====
   ;; ==== lane U2: editor components, autocomplete providers, terminal input ====
   ;; ==== end lane U2 ====
   ))
