;;;; The probe files of the Lisp SDK harness fixture, shared by the harness system (lisp-sdk-fixture.asd) and the surface system (lisp-sdk-fixture-surface.asd).
(asdf:defsystem "lisp-sdk-fixture-probes"
  :depends-on ("pig-sdk")
  :serial t
  :components ((:file "fixture")
               ;; ==== lane T ====
               (:file "probes-types")
               ;; ==== end lane T ====
               ;; ==== lane H ====
               (:file "probes-tui")
               ;; ==== end lane H ====
               ;; ==== lane TA ====
               (:file "probes-tui-alt")
               ;; ==== end lane TA ====
               ;; ==== lane C ====
               (:file "probes-coding")
               ;; ==== end lane C ====
               ;; ==== lane M ====
               (:file "probes-model")
               ;; ==== end lane M ====
               ;; ==== lane S2 ====
               (:file "probes-graph-a")
               ;; ==== end lane S2 ====
               ;; ==== lane S3 ====
               (:file "probes-graph-b")
               ;; ==== end lane S3 ====
               ;; ==== lane S1 ====
               (:file "probes-session")
               ;; ==== end lane S1 ====
               ;; ==== lane P ====
               (:file "probes-providers")
               ;; ==== end lane P ====
               ;; ==== lane U1 ====
               (:file "probes-kit")
               ;; ==== end lane U1 ====
               ;; ==== lane U2 ====
               (:file "probes-editor")
               ;; ==== end lane U2 ====
               ;; ==== lane CORE ====
               (:file "probes-core")
               ;; ==== end lane CORE ====
               ;; ==== lane HARNESS ====
               (:file "probes-surface")
               ;; ==== end lane HARNESS ====
               ))
