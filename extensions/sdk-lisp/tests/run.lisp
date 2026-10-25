;;;; SPDX-License-Identifier: MIT
;;;; sbcl --script tests/run.lisp : the SDK's own tests, no ASDF and no network.
(require :sb-bsd-sockets)
(require :sb-posix)
(setf *compile-verbose* nil *compile-print* nil)
(defparameter *root*
  (merge-pathnames "../" (make-pathname :name nil :type nil :defaults (or *load-truename* *default-pathname-defaults*))))
(handler-bind ((warning #'muffle-warning))
  (dolist (file '("src/package" "src/errors" "src/json" "src/wire" "src/extension" "src/context" "src/dispatch" "src/runner"
                  "tests/harness" "tests/json-tests" "tests/sdk-tests"))
    (let ((path (merge-pathnames (concatenate 'string file ".lisp") *root*)))
      (when (probe-file path) (load path)))))
(funcall (intern "RUN-ALL" "PIG-TESTS"))
