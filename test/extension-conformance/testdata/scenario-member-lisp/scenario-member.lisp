;;;; The Lisp scenario member: a second extension of the SDK under test, loaded beside the scenario driver so that,
;;;; when the scenario runs shared, both are members of one cell (one process). It registers one command and reads
;;;; no Spec.

(defpackage "scenario-member" (:use "COMMON-LISP"))
(in-package "scenario-member")

(defun new-extension ()
  (let ((ext (pig:make-extension "scenario-member-lisp")))
    (pig:command ext "member-ping" "The cell member's command" (lambda (ctx path) (declare (ignore ctx path)) nil))
    ext))
