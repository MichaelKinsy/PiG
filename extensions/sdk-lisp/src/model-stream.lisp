;;;; SPDX-License-Identifier: MIT
;;;; Model calls: the registry's stream, stream-simple and complete, Context.complete, the provider request callbacks and the fetch transport
;;;; of one stream (Pi's ProviderRequestOptions). The host owns the request; the extension serves its callbacks.

(in-package #:pig)

;;; ---------------------------------------------------------------- the streams of one extension

(defstruct (ms-state (:constructor make-ms-state ()))
  (lock (sb-thread:make-mutex :name "pig model streams"))
  (seq 0)
  (streams (make-hash-table :test 'equal))      ; stream id -> model-event-stream
  (callbacks (make-hash-table :test 'equal)))   ; stream id -> (model . ((callback-name . function-or-transport) ...))

(defun ms-state-of (ext) (extension-extra ext :model-stream #'make-ms-state))

(defun model-stream-register (ext id stream)
  "Make the host's model_stream_event notifications of stream ID reach STREAM."
  (let ((state (ms-state-of ext)))
    (sb-thread:with-mutex ((ms-state-lock state)) (setf (gethash id (ms-state-streams state)) stream))))

(defun model-stream-unregister (ext id)
  (let ((state (ms-state-of ext)))
    (sb-thread:with-mutex ((ms-state-lock state))
      (remhash id (ms-state-streams state))
      (remhash id (ms-state-callbacks state)))))

(def-notify "model_stream_event" (ext args)
  (let* ((state (ms-state-of ext))
         (stream (sb-thread:with-mutex ((ms-state-lock state)) (gethash (jget args "streamId" "") (ms-state-streams state)))))
    (when stream
      (if (eq (jget args "started") t)
          (model-stream-mark-started stream)
          (model-stream-push stream (or (jget args "event") (obj)))))))

;;; ---------------------------------------------------------------- base64 and bodies

(defparameter +ms-base64+ "ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz0123456789+/")

(defun ms-base64-encode (octets)
  (with-output-to-string (out)
    (loop for i from 0 below (length octets) by 3
          do (let* ((n (min 3 (- (length octets) i)))
                    (word (logior (ash (aref octets i) 16)
                                  (if (> n 1) (ash (aref octets (+ i 1)) 8) 0)
                                  (if (> n 2) (aref octets (+ i 2)) 0))))
               (dotimes (k 4)
                 (write-char (if (<= k n) (char +ms-base64+ (ldb (byte 6 (* 6 (- 3 k))) word)) #\=) out))))))

(defun ms-base64-decode (text)
  (let ((octets (make-array (floor (* 3 (length text)) 4) :element-type '(unsigned-byte 8) :fill-pointer 0))
        (word 0) (bits 0))
    (loop for char across text
          for value = (position char +ms-base64+)
          do (when value
               (setf word (logior (ash word 6) value) bits (+ bits 6))
               (when (>= bits 8)
                 (decf bits 8)
                 (vector-push (ldb (byte 8 bits) word) octets)
                 (setf word (ldb (byte bits 0) word)))))
    (coerce octets 'octets)))

(defun ms-body-reader (body)
  "A function of a size that returns up to that many octets of BODY, and an empty vector at its end. BODY is a string, octets,
or a function that returns the next chunk (a string or octets) and NIL at the end."
  (let ((pending (make-array 0 :element-type '(unsigned-byte 8))) (position 0) (more (functionp body)))
    (unless more
      (setf pending (if (stringp body) (sb-ext:string-to-octets body :external-format :utf-8) (coerce body 'octets))))
    (lambda (size)
      (loop while (and (>= position (length pending)) more)
            do (let ((chunk (funcall body)))
                 (if (null chunk)
                     (setf more nil)
                     (setf pending (if (stringp chunk) (sb-ext:string-to-octets chunk :external-format :utf-8) (coerce chunk 'octets))
                           position 0))))
      (let ((end (min (length pending) (+ position size))))
        (prog1 (subseq pending position end) (setf position end))))))

(defun ms-header-pairs (headers)
  "[[name, value], ...] of a header table whose values are a string or a vector of strings."
  (let ((pairs '()))
    (when (hash-table-p headers)
      (maphash (lambda (name value)
                 (dolist (entry (if (stringp value) (list value) (coerce value 'list)))
                   (push (vector name (princ-to-string entry)) pairs)))
               headers))
    (coerce (nreverse pairs) 'simple-vector)))

;;; ---------------------------------------------------------------- the fetch transport

(defstruct (ms-transport (:constructor make-ms-transport (fetch)))
  "The fetch option of one stream: fetch ids map to (signal . reader) while the response body is open."
  fetch
  (lock (sb-thread:make-mutex :name "pig model fetch"))
  (closed nil)
  (entries (make-hash-table :test 'equal)))

(defun ms-transport-serve (transport name value cancelled)
  "Serve the host's fetch, fetchRead or fetchClose callback of the stream (runtime-node/model-fetch.mjs)."
  (let* ((value (if (hash-table-p value) value (obj)))
         (id (princ-to-string (jget value "id"))))
    (cond ((string= name "fetch") (ms-serve-fetch transport id value cancelled))
          ((string= name "fetchRead")
           (let ((entry (sb-thread:with-mutex ((ms-transport-lock transport)) (gethash id (ms-transport-entries transport))))
                 (size (jget value "size")))
             (unless (and entry (cdr entry)) (error "unknown model fetch response ~A" (jget value "id")))
             (unless (and (integerp size) (<= 1 size (* 64 1024))) (error "invalid model fetch read size"))
             (let* ((unsubscribe (and cancelled (abort-signal-subscribe cancelled (lambda () (signal-abort (car entry))))))
                    (chunk (unwind-protect (funcall (cdr entry) size) (when unsubscribe (funcall unsubscribe)))))
               (obj "data" (ms-base64-encode chunk) "done" (if (zerop (length chunk)) t :false)))))
          (t ;; fetchClose is idempotent: a deferred Body.Close may follow the terminal stream event
           (let ((entry (sb-thread:with-mutex ((ms-transport-lock transport))
                          (let ((entry (gethash id (ms-transport-entries transport))))
                            (when (and entry (cdr entry)) (remhash id (ms-transport-entries transport)) entry)))))
             (when entry (signal-abort (car entry)))
             nil)))))

(defun ms-serve-fetch (transport id value cancelled)
  (let* ((signal (make-abort-signal)) (entry (cons signal nil)))
    (sb-thread:with-mutex ((ms-transport-lock transport))
      (when (ms-transport-closed transport) (error "model fetch transport is closed"))
      (setf (gethash id (ms-transport-entries transport)) entry))
    (let* ((encoded (jget value "body"))
           (request (obj "url" (jget value "url") "method" (jget value "method")
                         "headers" (let ((h (jget value "headers"))) (if (hash-table-p h) h (obj)))
                         "body" (and (stringp encoded) (plusp (length encoded)) (ms-base64-decode encoded))
                         "signal" signal))
           (unsubscribe (and cancelled (abort-signal-subscribe cancelled (lambda () (signal-abort signal)))))
           (response (unwind-protect
                          (handler-case (funcall (ms-transport-fetch transport) request)
                            (error (e)
                              (sb-thread:with-mutex ((ms-transport-lock transport))
                                (when (eq (gethash id (ms-transport-entries transport)) entry) (remhash id (ms-transport-entries transport))))
                              (error e)))
                       (when unsubscribe (funcall unsubscribe)))))
      (let* ((body (jget response "body"))
             (reader (and body (ms-body-reader body)))
             closed aborted)
        (sb-thread:with-mutex ((ms-transport-lock transport))
          (setf closed (ms-transport-closed transport) aborted (or closed (abort-signal-set-p signal)))
          (if (or aborted (null reader))
              (when (eq (gethash id (ms-transport-entries transport)) entry) (remhash id (ms-transport-entries transport)))
              (setf (cdr entry) reader)))
        (when aborted (error (if closed "model fetch transport is closed" "model fetch was cancelled")))
        (obj "status" (let ((status (jget response "status")))
                        (if (integerp status) status (error "a model fetch response needs an integer status")))
             "statusText" (let ((text (jget response "statusText"))) (if (stringp text) text ""))
             "headers" (ms-header-pairs (jget response "headers")) "body" (if reader t :false))))))

(defun ms-transport-dispose (transport)
  "End the transport with its stream: set every fetch's signal."
  (let ((entries '()))
    (sb-thread:with-mutex ((ms-transport-lock transport))
      (setf (ms-transport-closed transport) t)
      (maphash (lambda (id entry) (declare (ignore id)) (push entry entries)) (ms-transport-entries transport))
      (clrhash (ms-transport-entries transport)))
    (dolist (entry entries) (signal-abort (car entry)))))

(def-request "model_stream_callback" (ext ctx req)
  (let* ((args (let ((a (jget req "args"))) (if (hash-table-p a) a (obj))))
         (id (jget args "streamId" "")) (name (jget args "callback" ""))
         (state (ms-state-of ext))
         (owned (sb-thread:with-mutex ((ms-state-lock state)) (gethash id (ms-state-callbacks state))))
         (transport (cdr (assoc "fetch" (cdr owned) :test #'equal))))
    (cond ((and (member name '("fetch" "fetchRead" "fetchClose") :test #'equal) (ms-transport-p transport))
           (ms-transport-serve transport name (jget args "value") (ctx-cancel ctx)))
          ((and (equal name "fetchClose") (null owned)) nil)
          (t (let ((callback (cdr (assoc name (cdr owned) :test #'equal))))
               (when (or (null callback) (equal name "fetch")) (error "unknown model stream callback ~A/~A" id name))
               (let ((result (funcall callback (jget args "value") (car owned))))
                 (if (equal name "onPayload") (obj "defined" (if result t :false) "value" result) result)))))))

;;; ---------------------------------------------------------------- starting a stream

(defparameter +ms-callback-options+ '("onPayload" "onResponse" "transformHeaders" "fetch"))

(defun ms-copy-object (object)
  (let ((copy (make-hash-table :test 'equal)))
    (when (hash-table-p object) (maphash (lambda (key value) (setf (gethash key copy) value)) object))
    copy))

(defun ms-run-stream (ctx id stream model call transport)
  (unwind-protect
       (handler-case
           (progn (ctx-call-host ctx "modelStream" call)
                  ;; the host sent every event before it answered the call: a stream with no terminal event failed
                  (model-stream-push stream (model-stream-error-event "model stream ended without a terminal event" model)))
         (error (e) (model-stream-push stream (model-stream-error-event e model))))
    (model-stream-unregister (ctx-extension ctx) id)
    (when transport (ms-transport-dispose transport))))

(defun model-stream-start (ctx model request options simple &optional runtime)
  "Python ModelRegistry._stream: start the model call on a thread of its own and return its event stream. OPTIONS holds the request options;
a function in onPayload, onResponse, transformHeaders or fetch stays in this process and the host is told which exist. RUNTIME names the
session graph's ModelRuntime the call runs on."
  (let* ((state (ms-state-of (ctx-extension ctx))) (stream (make-model-event-stream))
         (payload (ms-copy-object request)) (callbacks '()) (id nil) (transport nil))
    (sb-thread:with-mutex ((ms-state-lock state))
      (setf id (format nil "model-stream-~D" (incf (ms-state-seq state))))
      (setf (gethash id (ms-state-streams state)) stream))
    (when (hash-table-p options)
      (maphash (lambda (key value)
                 (if (and (member key +ms-callback-options+ :test #'string=) (functionp value))
                     (push (cons key value) callbacks)
                     (setf (gethash key payload) value)))
               options))
    (let ((fetch (assoc "fetch" callbacks :test #'string=)))
      (when fetch (setf transport (make-ms-transport (cdr fetch)) (cdr fetch) transport)))
    (when callbacks
      (sb-thread:with-mutex ((ms-state-lock state)) (setf (gethash id (ms-state-callbacks state)) (cons model callbacks))))
    (let ((call (obj "streamId" id "model" model "request" payload "simple" (if simple t :false))))
      (when runtime (setf (gethash "runtime" call) runtime))
      (dolist (callback callbacks) (setf (gethash (car callback) call) t))
      (sb-thread:make-thread #'ms-run-stream :arguments (list ctx id stream model call transport) :name "pig-model-stream"))
    stream))

(defun registry-stream (registry model request &optional options)
  "Stream the response of MODEL to REQUEST (messages, systemPrompt, tools), a model-event-stream."
  (model-stream-start (model-registry-ctx registry) model request options nil))

(defun registry-stream-simple (registry model request &optional options)
  (model-stream-start (model-registry-ctx registry) model request options t))

(defun registry-complete (registry model request &optional options)
  "The final message of the model's response, blocking for it."
  (model-stream-result (registry-stream registry model request options)))

(defun ctx-complete (ctx model request auth)
  "A completion with the host's provider infrastructure and the credentials in AUTH."
  (call-result ctx "complete" (obj "model" model "request" request "auth" auth)))
