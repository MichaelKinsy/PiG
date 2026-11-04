;;;; SPDX-License-Identifier: MIT
;;;; sbcl --script tests/run-engine-vectors.lisp : every vector case of test/parity/sdk-vectors/pi-ai/Type.json that the host's type engine answers,
;;;; run through the SDK's own Type members against the host PIG_EXT_SOCKET names (TestTypeEngineVectorsThroughTheHost answers that socket with
;;;; the host's engine). Prints ENGINE_CASES_RUN=<n>; exits 1 when a case differs from Pi's answer.
(require :asdf)
(require :sb-bsd-sockets)
(require :sb-posix)
(setf *compile-verbose* nil *compile-print* nil)
(defparameter *root*
  (merge-pathnames "../" (make-pathname :name nil :type nil :defaults (or *load-truename* *default-pathname-defaults*))))

(handler-bind ((warning #'muffle-warning))
  (asdf:load-asd (merge-pathnames "pig-sdk.asd" *root*))
  (asdf:load-system "pig-sdk")
  (dolist (file '("tests/harness" "tests/sdk-tests" "tests/type-tests"))
    (load (merge-pathnames (concatenate 'string file ".lisp") *root*))))
(funcall (intern "TT-RUN-ENGINE-CASES" "PIG-TESTS"))
