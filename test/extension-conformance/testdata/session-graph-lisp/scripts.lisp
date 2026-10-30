(defpackage "session-graph" (:use "COMMON-LISP"))
(in-package "session-graph")

(defvar *scripts* '() "(tool-name . function) in definition order; each function is (lambda (ctx args)) and returns the script's report, a JSON object.")

(defmacro def-script (name (ctx args) &body body)
  "Define the script of the tool NAME, as session_graph_python.py defines the tool of the same name."
  `(let ((entry (assoc ,name *scripts* :test #'string=)) (function (lambda (,ctx ,args) (declare (ignorable ,ctx ,args)) ,@body)))
     (if entry (setf (cdr entry) function) (setf *scripts* (append *scripts* (list (cons ,name function)))))))
