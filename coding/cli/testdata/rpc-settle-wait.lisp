;;;; rpc-settle-wait.lisp: the agent_settled handler and the hold command wait on a plain TCP read.
;;;; They dial RPC_SETTLE_ADDR, announce themselves with a "waiting" line and return when the peer writes a line. The input handler dials RPC_SETTLE_INPUT_ADDR instead. Without the address they return at once. The wait is no SDK call, so nothing but the read itself tells the extension runtime from a handler that computes.
(defpackage "rpc-settle-wait" (:use "COMMON-LISP"))
(in-package "rpc-settle-wait")

(defun wait-on-peer (env)
  (let ((addr (sb-posix:getenv env)))
    (when (plusp (length addr))
      (let ((colon (position #\: addr :from-end t))
            (socket (make-instance 'sb-bsd-sockets:inet-socket :type :stream :protocol :tcp)))
        (sb-bsd-sockets:socket-connect socket (sb-bsd-sockets:make-inet-address (subseq addr 0 colon)) (parse-integer addr :start (1+ colon)))
        (let ((stream (sb-bsd-sockets:socket-make-stream socket :input t :output t :buffering :none :external-format :utf-8)))
          (unwind-protect (progn (write-line "waiting" stream) (finish-output stream) (read-line stream nil nil))
            (close stream)))))))

(defun new-extension ()
  (let ((ext (pig:make-extension "rpc-settle-wait")))
    (pig:on ext "agent_settled" (lambda (ctx data) (declare (ignore ctx data)) (wait-on-peer "RPC_SETTLE_ADDR") nil))
    (pig:on ext "input" (lambda (ctx data) (declare (ignore ctx data)) (wait-on-peer "RPC_SETTLE_INPUT_ADDR") nil))
    (pig:command ext "hold" "wait on a socket read" (lambda (ctx args) (declare (ignore ctx args)) (wait-on-peer "RPC_SETTLE_ADDR")))
    ext))
