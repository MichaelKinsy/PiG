;;;; SPDX-License-Identifier: MIT
(in-package #:pig)

(define-condition pig-error (error) ()
  (:documentation "Root of the conditions the SDK signals."))

(define-condition json-error (pig-error)
  ((message :initarg :message :reader json-error-message)
   (offset :initarg :offset :initform nil :reader json-error-offset))
  (:report (lambda (c s)
             (format s "JSON error~@[ at byte ~D~]: ~A" (json-error-offset c) (json-error-message c)))))

(define-condition host-call-error (pig-error)
  ((message :initarg :message :reader host-call-error-message)
   (code :initarg :code :initform nil :reader host-call-error-code))
  (:report (lambda (c s) (write-string (host-call-error-message c) s)))
  (:documentation "The host answered a call with an error, or the call could not complete."))

(define-condition shell-timeout-error (host-call-error)
  ((shell-exit :initarg :shell-exit :reader shell-timeout-error-shell-exit))
  (:documentation "The host's error for a local shell command that timed out after its shell had exited: Pi's ShellTimeoutError with a shellExit, which Pi does not
export, so this condition is internal. Its SHELL-EXIT is the wire object {exitCode, afterSeconds}. Pi's bash tool tells that error by class, so only this condition,
signalled again from an exec unchanged, keeps the shell's exit: a condition made with its message is the plain timeout."))

(define-condition host-call-cancelled (host-call-error) ()
  (:documentation "The request that owned the call was cancelled."))

(define-condition unsupported-capability (pig-error)
  ((member :initarg :member :reader unsupported-capability-member)
   (lane :initarg :lane :reader unsupported-capability-lane))
  (:report (lambda (c s)
             (format s "~A is not served by the Lisp SDK yet (gap lane ~A; see docs/extension-api-parity.md)"
                     (unsupported-capability-member c) (unsupported-capability-lane c))))
  (:documentation "A member of the extension API the Lisp SDK does not serve. Never a silent fallback."))
