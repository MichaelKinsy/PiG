;;;; SPDX-License-Identifier: MIT
;;;; ASDF system of the PiG Common Lisp extension SDK. It depends on nothing
;;;; but the SBCL contribs that ship with SBCL.

(asdf:defsystem "pig-sdk"
  :description "Write PiG extensions in Common Lisp (SBCL)."
  :license "MIT"
  :depends-on ("sb-bsd-sockets" "sb-posix")
  :serial t
  :pathname "src/"
  :components ((:file "package")
               (:file "errors")
               (:file "json")
               (:file "wire")
               (:file "extension")
               (:file "context")
               (:file "hooks")
               (:file "dispatch")
               ;; ==== lane T ====
               (:file "type-engine")
               (:file "tool-schema")
               (:file "pi-ai")
               ;; ==== end lane T ====
               ;; ==== lane H ====
               (:file "tui-tables")
               (:file "tui-width")
               (:file "tui-text")
               (:file "tui-fuzzy")
               (:file "tui-keys")
               (:file "tui-state")
               (:file "tui-keybindings")
               (:file "tui-terminal")
               (:file "tui-colors")
               (:file "tui-theme")
               ;; ==== end lane H ====
               ;; ==== lane C ====
               (:file "compaction")
               (:file "truncate")
               (:file "conversation")
               (:file "coding-helpers")
               ;; ==== end lane C ====
               ;; ==== lane M ====
               (:file "model-stream")
               (:file "model-types")
               (:file "event-stream")
               ;; ==== end lane M ====
               ;; ==== lane S2 ====
               (:file "session-manager")
               (:file "settings-manager")
               (:file "resource-loader")
               (:file "package-manager")
               (:file "project-trust")
               (:file "host-info")
               ;; ==== end lane S2 ====
               ;; ==== lane S3 ====
               (:file "model-runtime")
               (:file "create-provider")
               (:file "extension-runner")
               (:file "extension-factories")
               (:file "mcp-options")
               (:file "event-bus")
               (:file "builtin-tool")
               ;; ==== end lane S3 ====
               ;; ==== lane S1 ====
               (:file "session-ops")
               (:file "child-session")
               (:file "agent-runtime")
               ;; ==== end lane S1 ====
               ;; ==== lane P ====
               (:file "provider")
               (:file "oauth")
               (:file "renderer")
               (:file "virtual-model")
               (:file "mcp")
               ;; ==== end lane P ====
               ;; ==== lane U1 ====
               (:file "kit")
               (:file "ui-custom")
               (:file "overlay")
               (:file "widgets")
               (:file "theme")
               ;; ==== end lane U1 ====
               ;; ==== lane U2 ====
               (:file "editor-component")
               (:file "autocomplete")
               (:file "terminal-input")
               ;; ==== end lane U2 ====
               (:file "runner")))

(asdf:defsystem "pig-sdk/tests"
  :depends-on ("pig-sdk")
  :serial t
  :pathname "tests/"
  :components ((:file "harness")
               (:file "json-tests")
               (:file "sdk-tests")
                ;; ==== lane T ====
                (:file "type-tests")
                ;; ==== end lane T ====
                ;; ==== lane H ====
                (:file "tui-tests")
                ;; ==== end lane H ====
                ;; ==== lane C ====
                (:file "coding-tests")
                ;; ==== end lane C ====
                ;; ==== lane M ====
                (:file "model-tests")
                ;; ==== end lane M ====
                ;; ==== lane S2 ====
                (:file "graph-tests")
                ;; ==== end lane S2 ====
                ;; ==== lane S3 ====
                (:file "runtime-tests")
                ;; ==== end lane S3 ====
                ;; ==== lane S1 ====
                (:file "session-tests")
                ;; ==== end lane S1 ====
                ;; ==== lane P ====
                (:file "provider-tests")
                ;; ==== end lane P ====
                ;; ==== lane U1 ====
                (:file "ui-tests")
                ;; ==== end lane U1 ====
                ;; ==== lane U2 ====
                (:file "editor-tests")
                ;; ==== end lane U2 ====
                )
  :perform (asdf:test-op (op c) (declare (ignore op c)) (uiop:symbol-call :pig-tests :run-all)))
