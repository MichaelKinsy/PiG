(defpackage "lisp-sdk-fixture" (:use "COMMON-LISP"))
(in-package "lisp-sdk-fixture")

(defvar *registrations* '()
  "(key . function) in load order: each function is (lambda (ext)) and registers tools, commands, shortcuts, flags and handlers on EXT.")

(defvar *surface-registrations* '()
  "Like *registrations*, for the tools and providers that only the surface rows (model types, pi-tui helpers, host surface) read. The harness
extension leaves them out because TestConformance_TransportsMatch compares its registered tools with the other SDKs'; the surface extension adds them.")

(defun register-fixture (list key function)
  (let ((entry (assoc key (symbol-value list) :test #'string=)))
    (if entry (setf (cdr entry) function) (setf (symbol-value list) (append (symbol-value list) (list (cons key function)))))))

(defmacro def-fixture (key (ext) &body body)
  "Register BODY, run once per factory call with EXT the extension being built. KEY (a string naming the probe) replaces an earlier definition."
  `(register-fixture '*registrations* ,key (lambda (,ext) (declare (ignorable ,ext)) ,@body)))

(defmacro def-surface-fixture (key (ext) &body body)
  "Like DEF-FIXTURE, for the surface extension only."
  `(register-fixture '*surface-registrations* ,key (lambda (,ext) (declare (ignorable ,ext)) ,@body)))

(defun build-extension (&rest registrations)
  "A fresh extension carrying every registration of the REGISTRATIONS lists."
  (let ((ext (pig:make-extension "lisp-sdk-fixture")))
    (dolist (list registrations ext)
      (loop for (nil . function) in (symbol-value list) do (funcall function ext)))))
