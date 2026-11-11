;;;; The probe files of the Lisp SDK harness fixture, shared by the harness system (lisp-sdk-fixture.asd) and the surface system (lisp-sdk-fixture-surface.asd).
(asdf:defsystem "lisp-sdk-fixture-probes"
  :depends-on ("pig-sdk")
  :serial t
  :components ((:file "fixture")
               (:file "probes-types")
               (:file "probes-tui")
               (:file "probes-tui-alt")
               (:file "probes-coding")
               (:file "probes-model")
               (:file "probes-graph-a")
               (:file "probes-graph-b")
               (:file "probes-session")
               (:file "probes-providers")
               (:file "probes-kit")
               (:file "probes-editor")
               (:file "probes-core")
               (:file "probes-surface")
               ))
