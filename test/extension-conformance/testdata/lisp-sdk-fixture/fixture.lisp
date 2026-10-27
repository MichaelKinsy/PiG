(defpackage "lisp-sdk-fixture" (:use "COMMON-LISP"))
(in-package "lisp-sdk-fixture")

(defvar *registrations* '()
  "(key . function) in load order: each function is (lambda (ext)) and registers tools, commands, shortcuts, flags and handlers on EXT.")

(defmacro def-fixture (key (ext) &body body)
  "Register BODY, run once per factory call with EXT the extension being built. KEY (a string naming the probe) replaces an earlier definition."
  `(let ((entry (assoc ,key *registrations* :test #'string=)) (function (lambda (,ext) (declare (ignorable ,ext)) ,@body)))
     (if entry (setf (cdr entry) function) (setf *registrations* (append *registrations* (list (cons ,key function)))))))
