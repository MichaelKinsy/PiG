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
   #:js-type-error #:js-type-error-message #:+default-max-lines+ #:+default-max-bytes+ #:+grep-max-line-length+ #:truncate-head #:truncate-tail
   #:truncate-line #:format-size #:truncate-to-visual-lines #:truncation-result #:truncation-result-content #:truncation-result-truncated
   #:truncation-result-truncated-by #:truncation-result-total-lines #:truncation-result-total-bytes #:truncation-result-output-lines
   #:truncation-result-output-bytes #:truncation-result-last-line-partial #:truncation-result-first-line-exceeds-limit #:truncation-result-max-lines
   #:truncation-result-max-bytes #:visual-truncate-result #:visual-truncate-result-visual-lines #:visual-truncate-result-skipped-count
   #:compaction-settings #:make-compaction-settings #:compaction-settings-enabled #:compaction-settings-reserve-tokens
   #:compaction-settings-keep-recent-tokens #:*default-compaction-settings* #:calculate-context-tokens #:should-compact #:get-last-assistant-usage
   #:estimate-tokens #:find-turn-start-index #:find-cut-point #:cut-point-result #:cut-point-result-first-kept-entry-index
   #:cut-point-result-turn-start-index #:cut-point-result-split-turn-p #:convert-to-llm #:serialize-conversation #:session-entry-to-context-messages
   #:build-session-context #:+session-leaf-last+ #:file-operations #:file-operations-read #:file-operations-written #:file-operations-edited
   #:branch-preparation #:branch-preparation-messages #:branch-preparation-file-ops #:branch-preparation-total-tokens #:collect-entries-result
   #:collect-entries-result-entries #:collect-entries-result-common-ancestor-id #:collect-entries-for-branch-summary #:prepare-branch-entries
   #:tool-call-event-type-p #:bash-tool-result-p #:powershell-tool-result-p #:read-tool-result-p #:edit-tool-result-p #:write-tool-result-p
   #:grep-tool-result-p #:find-tool-result-p #:ls-tool-result-p #:message-role #:message-text #:get-language-from-path #:+current-session-version+
   #:*virtual-model-state-entry* #:get-latest-compaction-entry #:parsed-skill-block #:parsed-skill-block-name #:parsed-skill-block-location
   #:parsed-skill-block-content #:parsed-skill-block-user-message #:parse-skill-block #:format-skills-for-prompt #:resized-image #:make-resized-image
   #:resized-image-original-width #:resized-image-original-height #:resized-image-width #:resized-image-height #:resized-image-was-resized
   #:resized-image-data #:resized-image-mime-type #:format-dimension-note #:parsed-frontmatter #:parsed-frontmatter-frontmatter
   #:parsed-frontmatter-body #:parse-frontmatter #:strip-frontmatter #:parse-date #:diff-string #:diff-string-diff #:diff-string-first-changed-line
   #:generate-diff-string #:generate-unified-patch #:copy-to-clipboard #:converted-image #:converted-image-data #:converted-image-mime-type
   #:convert-to-png #:image-resize-options #:make-image-resize-options #:resize-image #:read-stored-credential #:with-file-mutation-queue
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
