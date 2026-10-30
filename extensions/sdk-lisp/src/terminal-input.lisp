;;;; SPDX-License-Identifier: MIT
;;;; ctx-on-terminal-input: raw terminal input, every chunk before the editor (Pi's ctx.ui.onTerminalInput).

(in-package #:pig)

(defstruct (terminal-input-result (:constructor make-terminal-input-result (&key consume data)))
  "A handler's verdict on one chunk, Pi's {consume?, data?}. A non-NIL DATA replaces the chunk for later handlers and for normal handling;
an empty replacement drops the chunk."
  consume data)

(defstruct (termin-state (:constructor make-termin-state ()))
  (lock (sb-thread:make-mutex :name "pig terminal input")) (handlers '()) (seq 0))

(defun termin-state-of (ext) (extension-extra ext :terminal-input #'make-termin-state))

(defun termin-verdict (verdict)
  "(values consume data) of a TERMINAL-INPUT-RESULT, a JSON object with consume and data, or NIL."
  (flet ((truthy (v) (and v (not (eq v :false)) (not (eq v :null))))
         (text (v) (cond ((or (null v) (eq v :null)) nil) ((stringp v) v) (t (princ-to-string v)))))
    (cond ((terminal-input-result-p verdict) (values (truthy (terminal-input-result-consume verdict)) (text (terminal-input-result-data verdict))))
          ((hash-table-p verdict) (values (truthy (jget verdict "consume")) (text (jget verdict "data"))))
          (t (values nil nil)))))

(defun termin-handlers (ext)
  (let ((state (termin-state-of ext)))
    (sb-thread:with-mutex ((termin-state-lock state)) (mapcar #'cdr (termin-state-handlers state)))))

(def-request "terminal_input" (ext ctx req)
  ;; The host is blocked on this reply and Pi's listener is synchronous, so the handlers run inline. One that signals is reported on stderr
  ;; and counts as not consuming, rather than capturing the keystroke.
  (let* ((args (jget req "args"))
         (original (let ((data (and (hash-table-p args) (jget args "data")))) (if (stringp data) data "")))
         (current original) (consume nil))
    (dolist (handler (termin-handlers ext))
      (multiple-value-bind (consumes data)
          (handler-case (termin-verdict (funcall handler current))
            (error (e) (format *error-output* "extension: recovered from exception in a terminal input handler: ~A~%" e) (values nil nil)))
        (when consumes (setf consume t) (return))
        (when data (setf current data))))
    (let ((verdict (obj "consume" (if consume t :false))))
      (when (and (not consume) (string/= current original)) (setf (gethash "data" verdict) current))
      verdict)))

(defun ctx-on-terminal-input (ctx handler)
  "Subscribe HANDLER, (lambda (data)), to the raw terminal input, every chunk before the editor. It returns a TERMINAL-INPUT-RESULT, a JSON object with
consume and data, or NIL, and must return promptly: the host waits for it. The host forwards input only while a handler is subscribed. With no UI nothing
is retained. Returns an idempotent function that unsubscribes."
  (let ((ext (ctx-extension ctx)))
    (cond
      ((not (ctx-has-ui-p ctx)) (lambda () nil))
      ((not (functionp handler)) (error "ctx-on-terminal-input requires a function"))
      (t (let* ((state (termin-state-of ext)) (done nil) first id)
           (sb-thread:with-mutex ((termin-state-lock state))
             (setf id (incf (termin-state-seq state))
                   (termin-state-handlers state) (append (termin-state-handlers state) (list (cons id handler)))
                   first (null (cdr (termin-state-handlers state)))))
           (when first (host-call ext "ui.onTerminalInput" (obj)))
           (lambda ()
             (let ((last nil))
               (sb-thread:with-mutex ((termin-state-lock state))
                 (unless done
                   (setf done t
                         (termin-state-handlers state) (remove id (termin-state-handlers state) :key #'car)
                         last (null (termin-state-handlers state)))))
               (when last (host-call ext "ui.offTerminalInput" (obj)))
               nil)))))))
