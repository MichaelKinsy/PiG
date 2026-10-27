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
