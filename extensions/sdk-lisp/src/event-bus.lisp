;;;; SPDX-License-Identifier: MIT
;;;; Pi's `pi.events` (core/event-bus.ts), bridged by value: every realm of a session shares one ordered listener registry held by the host.
;;;; A listener sits in it in registration order and a payload crosses as JSON.

(in-package #:pig)

(defvar *event-handler-seq* 0
  "Process-wide, like the host's realm: the host keys a listener by (OS process, handler id) and a packed cell runs several extensions in one process.")
(defvar *event-handler-seq-lock* (sb-thread:make-mutex :name "pig event handler ids"))

(defstruct (event-bus (:constructor %make-event-bus (extension ctx)))
  "`pi.events` of an extension (EXTENSION-EVENTS) or of a request (CTX-EVENTS)."
  extension ctx)

(defstruct (event-sub (:constructor make-event-sub (id channel handler)))
  id channel handler (sent nil) (off nil))

(defstruct (event-hub (:constructor make-event-hub ()))
  (lock (sb-thread:make-mutex :name "pig event hub"))
  (subs (make-hash-table :test 'equal))
  (pending '())
  (live nil))

(defun event-hub (ext) (extension-extra ext :event-bus #'make-event-hub))

(defun extension-events (ext)
  "EXT's `pi.events`. Subscriptions made while the factory runs are registered with the host before the extension registers."
  (%make-event-bus ext nil))

(defun ctx-events (ctx)
  "`ctx.events`: the event bus of the request CTX belongs to."
  (%make-event-bus (ctx-extension ctx) ctx))

(defun event-bus-call (bus method args)
  (let ((ctx (event-bus-ctx bus)) (ext (event-bus-extension bus)))
    (cond (ctx (ctx-call-host ctx method args))
          ((extension-conn ext) (host-call ext method args))
          (t (helper-loading-call (helper-socket-path) method args ext)))))

(defun events-on (bus channel handler)
  "Subscribe HANDLER, (lambda (ctx data)), to CHANNEL and return the idempotent unsubscribe function. A handler runs in a request of its own, so it may call
EVENTS-EMIT while the host waits for its answer; its error fails only that delivery, which the host reports as `Event handler error`."
  (unless (stringp channel) (error "event channel must be a string"))
  (unless (functionp handler) (error "event handler must be a function"))
  (let* ((hub (event-hub (event-bus-extension bus)))
         (sub (make-event-sub (sb-thread:with-mutex (*event-handler-seq-lock*) (format nil "lisp-~D" (incf *event-handler-seq*))) channel handler))
         (queued nil))
    (sb-thread:with-mutex ((event-hub-lock hub))
      (setf (gethash (event-sub-id sub) (event-hub-subs hub)) sub)
      ;; while the factory loads, the factory's own connection holds the listener at once; the subscriptions made while the extension connects wait for the register frame
      (if (or (event-hub-live hub) (and (null (extension-conn (event-bus-extension bus))) (helper-socket-path)))
          (setf (event-sub-sent sub) t)
          (setf queued t (event-hub-pending hub) (append (event-hub-pending hub) (list sub)))))
    (unless queued
      (handler-case (event-bus-call bus "events.on" (obj "channel" channel "handlerId" (event-sub-id sub) "value" t))
        (error (e)
          (sb-thread:with-mutex ((event-hub-lock hub)) (remhash (event-sub-id sub) (event-hub-subs hub)))
          (error e))))
    (let ((once (sb-thread:make-mutex :name "pig unsubscribe")) (removed nil))
      (lambda ()
        (unless (sb-thread:with-mutex (once) (prog1 removed (setf removed t)))
          (let ((sent (sb-thread:with-mutex ((event-hub-lock hub))
                        (setf (event-sub-off sub) t)
                        (unless (event-sub-sent sub)
                          (remhash (event-sub-id sub) (event-hub-subs hub))
                          (setf (event-hub-pending hub) (remove sub (event-hub-pending hub))))
                        (event-sub-sent sub))))
            (when sent
              ;; EventEmitter.off never throws: a failed off is reported
              (handler-case (event-bus-call bus "events.off" (obj "handlerId" (event-sub-id sub)))
                (error (e) (format *error-output* "extension: pi.events unsubscribe ~S: ~A~%" channel e))))))
        nil))))

(defun events-emit (bus channel &optional data)
  "Send DATA to every listener of CHANNEL and return after each listener's work. Signals for a payload without a JSON form, and for an emit on \"error\"
that no listener handles, as EventEmitter throws."
  (unless (stringp channel) (error "event channel must be a string"))
  (handler-case (encode-json data) (error () (error "event payload must be JSON-serializable")))
  (let ((reply (event-bus-call bus "events.emit" (obj "channel" channel "json" data "value" t))))
    (when (and (hash-table-p reply) (eq (jget reply "unhandledError") t))
      (error "pi.events.emit ~S: unhandled error event: ~A" channel (to-json data)))
    nil))

(defun event-dispatch (ext ctx args)
  "Run the listener the host's events.dispatch names. CTX is NIL for a dispatch while the factory loads."
  (let* ((id (jget args "handlerId" ""))
         (sub (let ((hub (event-hub ext))) (sb-thread:with-mutex ((event-hub-lock hub)) (gethash id (event-hub-subs hub))))))
    (unless (and sub (equal (event-sub-channel sub) (jget args "channel")))
      (error "Unknown event bus handler: ~A" id))
    (funcall (event-sub-handler sub) ctx (let ((data (jget args "json"))) (if (eq data :null) nil data)))
    nil))

(def-request "events.dispatch" (ext ctx req) (event-dispatch ext ctx (jget req "args")))

(defun event-release (ext handler-id)
  "Drop an unsubscribed listener once the host says no snapshot can name it."
  (let ((hub (event-hub ext)))
    (sb-thread:with-mutex ((event-hub-lock hub))
      (let ((sub (gethash (or handler-id "") (event-hub-subs hub))))
        (when (and sub (event-sub-off sub)) (remhash (event-sub-id sub) (event-hub-subs hub)))))))

(def-notify "events.release" (ext args) (event-release ext (jget args "handlerId" "")))

(defun event-load-call (ext method args)
  "One host call before the register frame, when the read loop has not started: this thread reads until the call's result arrives."
  (let ((conn (extension-conn ext)) (id (format nil "h~D" (incf (extension-call-counter ext)))))
    (send-frame conn (obj "type" "call" "id" id "call" (obj "method" method "args" args)))
    (loop
      (let ((env (read-frame conn)))
        (when (eq env :eof) (error "the host closed the connection during ~A" method))
        (let ((type (jget env "type")))
          (cond ((equal type "ping") (send-frame conn (obj "type" "pong" "pong" (obj "nonce" (jget (or (jget env "ping") (obj)) "nonce" "")))))
                ((and (equal type "call_result") (equal (jget env "id") id))
                 (let ((err (jref env "call_result" "error")))
                   (when (hash-table-p err) (error 'host-call-error :message (frame-error-message err)))
                   (return)))
                (t (error "expected a ~A result, got ~A" method type))))))))

(def-register-contributor "event-bus" (ext payload)
  ;; the factory's subscriptions, in subscription order, before the register frame, so the host has them when it registers the extension
  (let* ((hub (event-hub ext))
         (pending (sb-thread:with-mutex ((event-hub-lock hub))
                    (setf (event-hub-live hub) t)
                    (dolist (sub (event-hub-pending hub)) (setf (event-sub-sent sub) t))
                    (prog1 (event-hub-pending hub) (setf (event-hub-pending hub) '())))))
    (dolist (sub pending)
      (unless (sb-thread:with-mutex ((event-hub-lock hub)) (event-sub-off sub))
        (event-load-call ext "events.on" (obj "channel" (event-sub-channel sub) "handlerId" (event-sub-id sub) "value" t))))))
