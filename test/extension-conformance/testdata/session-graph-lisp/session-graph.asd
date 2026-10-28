;;;; The Lisp fixture of the session graph conformance rows (session_graph_test.go): the script of session_graph_python.py, one tool per script.
;;;; scripts.lisp owns new-extension and DEF-SCRIPT; graph-a.lisp, graph-b.lisp and graph-s1.lisp each hold the scripts of one lane.
(asdf:defsystem "session-graph"
  :depends-on ("pig-sdk")
  :serial t
  :components ((:file "scripts")
               (:file "graph-a")
               (:file "graph-b")
               (:file "graph-s1")
               (:file "extension")))
