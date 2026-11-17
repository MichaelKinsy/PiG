;;;; The Lisp fixture of the socket retirement rows (socket_retirement_runtime_test.go). The test names the gate directory in PIG_RETIREMENT_GATES.
(defpackage "retire-probe" (:use "COMMON-LISP"))
(in-package "retire-probe")

(defun gate (name)
  (concatenate 'string (sb-ext:posix-getenv "PIG_RETIREMENT_GATES") "/" name))

(defun exists-p (name)
  (probe-file (gate name)))

(defun touch (name)
  (with-open-file (out (gate name) :direction :output :if-exists :supersede :if-does-not-exist :create)
    (declare (ignore out))))

(defun new-extension ()
  ;; The factory holds while the test keeps the factory gate, before the SDK connects to the host.
  (when (exists-p "factory-hold")
    (touch "factory-holding")
    (loop while (exists-p "factory-hold") do (sleep 0.005)))
  (let ((ext (pig:make-extension "retire-probe")))
    (pig:command ext "retire-hold" "reload, then hold the command"
                 (lambda (ctx args)
                   (declare (ignore args))
                   (pig:ctx-reload ctx)
                   (touch "reloaded")
                   (loop until (exists-p "release-command") do (sleep 0.005))))
    (pig:command ext "retire-ping" "answer from this generation"
                 (lambda (ctx args)
                   (declare (ignore ctx args))
                   (touch "pinged")))
    ext))
