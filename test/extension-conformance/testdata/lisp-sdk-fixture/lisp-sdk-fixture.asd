;;;; The Lisp SDK harness fixture of the SDK conformance rows (test/extension-conformance: makeSubprocessLispHarness). Each lane's probes-*.lisp
;;;; registers the tools, commands and event handlers its tests drive, as testdata/python-sdk-fixture/main.py does for Python; extension.lisp builds
;;;; the extension. The pi-tui, model-types and host-surface tools are in the surface system (lisp-sdk-fixture-surface.asd).
(asdf:defsystem "lisp-sdk-fixture"
  :depends-on ("lisp-sdk-fixture-probes")
  :components ((:file "extension")))
