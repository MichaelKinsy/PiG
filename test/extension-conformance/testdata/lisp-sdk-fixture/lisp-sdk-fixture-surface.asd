;;;; The surface extension: the harness probes plus the tools of the model-types, pi-tui and host-surface rows (def-surface-fixture).
(asdf:defsystem "lisp-sdk-fixture-surface"
  :depends-on ("lisp-sdk-fixture-probes")
  :components ((:file "extension-surface")))
