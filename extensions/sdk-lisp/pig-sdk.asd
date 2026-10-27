;;;; SPDX-License-Identifier: MIT
;;;; ASDF system of the PiG Common Lisp extension SDK. It depends on nothing
;;;; but the SBCL contribs that ship with SBCL.

(asdf:defsystem "pig-sdk"
  :description "Write PiG extensions in Common Lisp (SBCL)."
  :license "MIT"
  :depends-on ("sb-bsd-sockets" "sb-posix")
  :serial t
  :pathname "src/"
  :components ((:file "package")
               (:file "errors")
               (:file "json")
               (:file "wire")
               (:file "extension")
               (:file "context")
               (:file "dispatch")
               (:file "runner")))

(asdf:defsystem "pig-sdk/tests"
  :depends-on ("pig-sdk")
  :serial t
  :pathname "tests/"
  :components ((:file "harness")
               (:file "json-tests")
               (:file "sdk-tests"))
  :perform (asdf:test-op (op c) (declare (ignore op c)) (uiop:symbol-call :pig-tests :run-all)))
