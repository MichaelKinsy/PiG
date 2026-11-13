;;;; SPDX-License-Identifier: MIT
;;;; The event stream of one assistant response: what a model call returns and what a provider's stream function builds.

(in-package #:pig)

;; One queued event: the event as the author pushed it, a copy of it as it was at the push (the event itself when the SDK built it), and the condition of copying it.
(defstruct (ms-item (:constructor make-ms-item (event wire error))) event wire error)

(defstruct (model-event-stream (:constructor make-model-event-stream ()))
  "Ordered events of one response, pushed by one thread and read by another. Events and results are JSON hash-tables."
  (lock (sb-thread:make-mutex :name "pig model stream"))
  (queue (sb-thread:make-waitqueue))
  (head '()) (tail nil)
  (terminal nil) (result nil)
  ;; the result as it was when the author ended the stream, which the host receives (NIL for a stream the SDK itself fills: RESULT is then what the host receives)
  (result-wire nil) (result-error nil)
  (started nil) (start-error nil)
  (id nil)
  ;; a stream made by create-assistant-message-event-stream times its response (see ms-time-message)
  (timed nil) (started-at 0) (started-clock 0))

(defun ms-unix-ms ()
  (multiple-value-bind (seconds microseconds) (sb-ext:get-time-of-day)
    (+ (* seconds 1000) (floor microseconds 1000))))

(defun create-assistant-message-event-stream ()
  "pi-ai's createAssistantMessageEventStream: the final message gets durationMs, measured from this call, unless it already has one or
its timestamp predates the stream."
  (let ((stream (make-model-event-stream)))
    (setf (model-event-stream-timed stream) t
          (model-event-stream-started-at stream) (ms-unix-ms)
          (model-event-stream-started-clock stream) (get-internal-real-time))
    stream))

(defun ms-time-message (stream message)
  "AssistantMessageEventStream's #time (packages/ai/src/utils/event-stream.ts). The caller holds the lock."
  (when (and (model-event-stream-timed stream) (not (model-event-stream-terminal stream))
             (hash-table-p message) (not (nth-value 1 (gethash "durationMs" message))))
    (let ((timestamp (jget message "timestamp")))
      (unless (and (realp timestamp) (< timestamp (model-event-stream-started-at stream)))
        (setf (gethash "durationMs" message)
              (max 0 (floor (+ (* 1000 (- (get-internal-real-time) (model-event-stream-started-clock stream)))
                               (/ internal-time-units-per-second 2))
                            internal-time-units-per-second)))))))

(defconstant +json-snapshot-depth+ 1000 "How deeply nested a value json-snapshot copies; a table that contains itself is deeper.")

(defun json-snapshot (value &optional (depth 0))
  "A copy of the JSON value VALUE that shares nothing mutable with it: objects and arrays are copied (an object keeps its member order) and a lazy member is forced. A value nested
deeper than +JSON-SNAPSHOT-DEPTH+ (a cycle) or a list that is not proper signals an error."
  (when (> depth +json-snapshot-depth+) (error "cannot copy a value nested more than ~D deep (a cycle?)" +json-snapshot-depth+))
  (let ((value (force-lazy value)) (depth (1+ depth)))
    (typecase value
      (hash-table (let ((copy (make-hash-table :test (hash-table-test value) :size (max 1 (hash-table-count value)))))
                    (maphash (lambda (key member) (setf (gethash key copy) (json-snapshot member depth))) value)
                    copy))
      (string (if (simple-string-p value) value (coerce value 'simple-string)))
      (vector (map 'simple-vector (lambda (member) (json-snapshot member depth)) value))
      (cons (mapcar (lambda (member) (json-snapshot member depth)) value))
      (t value))))

(defun ms-snapshot (value)
  "(values copy nil), or (values nil condition) when VALUE cannot be copied."
  (handler-case (values (json-snapshot value) nil)
    (error (condition) (values nil condition))))

(defun model-stream-enqueue (stream event &key snapshot)
  "Append EVENT; a done or error event ends the stream, and anything pushed after the end is dropped. SNAPSHOT queues a copy of EVENT for the host, for an event an author built and
may go on changing; a value that cannot be copied fails the request that forwards the stream and does not signal here."
  (sb-thread:with-mutex ((model-event-stream-lock stream))
    (unless (model-event-stream-terminal stream)
      (let* ((type (jget event "type"))
             (ends (member type '("done" "error") :test #'equal))
             (key (if (equal type "done") "message" "error"))
             (final (and ends (jget event key))))
        (when ends (ms-time-message stream final))
        ;; pig divergence (D615): the host receives the event as it was at the push, where Pi's consumer reads the shared message at its own turn.
        (multiple-value-bind (wire failure) (if snapshot (ms-snapshot event) (values event nil))
          (let ((cell (list (make-ms-item event wire failure))))
            (if (model-event-stream-head stream)
                (setf (cdr (model-event-stream-tail stream)) cell)
                (setf (model-event-stream-head stream) cell))
            (setf (model-event-stream-tail stream) cell))
          (when ends
            (setf (model-event-stream-terminal stream) t
                  (model-event-stream-result stream) final
                  (model-event-stream-result-wire stream) (and snapshot wire (jget wire key))
                  (model-event-stream-result-error stream) failure))))
      (sb-thread:condition-broadcast (model-event-stream-queue stream))))
  stream)

(defun model-stream-push (stream event)
  "Append EVENT; a done or error event ends the stream, and anything pushed after the end is dropped. The host receives the event as it was when this returned: a copy is queued, so
the author may go on changing the message it shows, and the forwarding thread never reads a table the author is changing. MODEL-STREAM-NEXT and -EVENTS return EVENT itself. That is
stronger than Pi, whose consumer reads the shared message at its own turn: after a synchronous burst of pushes or between microtask awaits it can see later changes (D615)."
  (model-stream-enqueue stream event :snapshot t))

(defun model-stream-end (stream &optional result)
  "End the stream with RESULT (a message hash-table or NIL) without a terminal event. The result is the message as it is now."
  (sb-thread:with-mutex ((model-event-stream-lock stream))
    (unless (model-event-stream-terminal stream)
      (ms-time-message stream result)
      (setf (model-event-stream-terminal stream) t (model-event-stream-result stream) result)
      (multiple-value-bind (wire failure) (ms-snapshot result)
        (setf (model-event-stream-result-wire stream) wire (model-event-stream-result-error stream) failure))
      (sb-thread:condition-broadcast (model-event-stream-queue stream))))
  stream)

(defun ms-check-transport (stopped)
  (when (and stopped (funcall stopped)) (error "Provider connection closed")))

(defun model-stream-next (stream &key stopped wire)
  "(values event t) for the next event, blocking for it; (values nil nil) once the stream ended and is drained. STOPPED is NIL or a function
that tells whether the transport shut down: then this signals an error instead (call MODEL-STREAM-WAKE when that happens). WIRE returns the event as the host receives
it, as it was when the author pushed it, and signals the error of copying it."
  (sb-thread:with-mutex ((model-event-stream-lock stream))
    (loop while (and (null (model-event-stream-head stream)) (not (model-event-stream-terminal stream)))
          do (ms-check-transport stopped)
             (sb-thread:condition-wait (model-event-stream-queue stream) (model-event-stream-lock stream)))
    (ms-check-transport stopped)
    (if (model-event-stream-head stream)
        (let ((item (pop (model-event-stream-head stream))))
          (cond ((not wire) (values (ms-item-event item) t))
                ((ms-item-error item) (error (ms-item-error item)))
                (t (values (ms-item-wire item) t))))
        (values nil nil))))

(defun model-stream-wake (stream)
  (sb-thread:with-mutex ((model-event-stream-lock stream))
    (sb-thread:condition-broadcast (model-event-stream-queue stream))))

(defun model-stream-forward (stream emit &key stopped wire)
  "Call EMIT on every event of STREAM until it ends (see MODEL-STREAM-NEXT for STOPPED and WIRE)."
  (loop (multiple-value-bind (event present) (model-stream-next stream :stopped stopped :wire wire)
          (unless present (return))
          (funcall emit event))))

(defun model-stream-events (stream &key stopped)
  "The events not read yet, as a list, once the stream ended."
  (let ((events '()))
    (model-stream-forward stream (lambda (event) (push event events)) :stopped stopped)
    (nreverse events)))

(defun model-stream-result (stream)
  "The final message (NIL when the stream ended without one), blocking until the stream ended."
  (sb-thread:with-mutex ((model-event-stream-lock stream))
    (loop until (model-event-stream-terminal stream)
          do (sb-thread:condition-wait (model-event-stream-queue stream) (model-event-stream-lock stream)))
    (model-event-stream-result stream)))

(defun model-stream-wire-result (stream)
  "The final message as the host receives it: as it was when the author ended the stream. Blocks until the stream ended; signals the error of copying it."
  (sb-thread:with-mutex ((model-event-stream-lock stream))
    (loop until (model-event-stream-terminal stream)
          do (sb-thread:condition-wait (model-event-stream-queue stream) (model-event-stream-lock stream)))
    (when (model-event-stream-result-error stream) (error (model-event-stream-result-error stream)))
    (or (model-event-stream-result-wire stream) (model-event-stream-result stream))))

(defun model-stream-mark-started (stream &optional error)
  "The producer began (a string or a condition says it failed to). Only the first mark counts."
  (sb-thread:with-mutex ((model-event-stream-lock stream))
    (unless (model-event-stream-started stream)
      (setf (model-event-stream-start-error stream) error (model-event-stream-started stream) t)
      (sb-thread:condition-broadcast (model-event-stream-queue stream)))))

(defun model-stream-wait-started (stream)
  "Block until the producer began; signal the error it began with."
  (sb-thread:with-mutex ((model-event-stream-lock stream))
    (loop until (model-event-stream-started stream)
          do (sb-thread:condition-wait (model-event-stream-queue stream) (model-event-stream-lock stream))))
  (let ((failure (model-event-stream-start-error stream)))
    (cond ((typep failure 'condition) (error failure))
          (failure (error "~A" failure)))))

(defun model-stream-error-event (failure model)
  "The terminal error event of a model call that failed in transport. FAILURE is a condition or a message."
  (flet ((text (value) (if (stringp value) value "")))
    (let* ((provider (jget model "provider"))
           (model-id (let ((id (text (jget model "modelId")))) (if (plusp (length id)) id (text (jget model "id")))))
           (zero (obj "input" 0 "output" 0 "cacheRead" 0 "cacheWrite" 0 "total" 0)))
      (obj "type" "error" "reason" "error"
           "error" (obj "role" "assistant" "content" (vector) "api" (text (jget model "api"))
                        "provider" (text (if (hash-table-p provider) (jget provider "id") provider)) "model" model-id
                        "usage" (obj "input" 0 "output" 0 "cacheRead" 0 "cacheWrite" 0 "totalTokens" 0 "cost" zero)
                        "stopReason" "error" "errorMessage" (if (stringp failure) failure (condition-message failure))
                        "timestamp" (ms-unix-ms))))))
