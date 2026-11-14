(defpackage "provider-producer" (:use "COMMON-LISP"))
(in-package "provider-producer")

(defun zero-usage ()
  (pig:obj "input" 0 "output" 0 "cacheRead" 0 "cacheWrite" 0 "totalTokens" 0
           "cost" (pig:obj "input" 0 "output" 0 "cacheRead" 0 "cacheWrite" 0 "total" 0)))

(defun last-content (request)
  (let ((messages (pig:jget request "messages")))
    (and (vectorp messages) (plusp (length messages)) (pig:jget (aref messages (1- (length messages))) "content"))))

(defun cancel-request-p (content)
  (or (equal content "cancel") (and (vectorp content) (not (stringp content)) (plusp (length content)) (equal (pig:jref content 0 "text") "cancel"))))

(defun produce-stream (ctx model request options)
  (unless (equal (pig:jget options "apiKey") "extension-key") (error "wrong key"))
  (when (cancel-request-p (last-content request))
    (pig:ctx-exec ctx "provider-started")
    (loop until (pig:ctx-cancelled-p ctx) do (sleep 0.001))
    (error "provider stream aborted"))
  (let* ((text "custom provider response")
         (message (pig:obj "role" "assistant" "api" (pig:jget model "api") "provider" (pig:jget model "provider") "model" (pig:jget model "id")
                           "content" (vector (pig:obj "type" "text" "text" text)) "stopReason" "stop" "timestamp" 1 "usage" (zero-usage)))
         (result (pig:make-model-event-stream)))
    (dolist (event (list (pig:obj "type" "start" "partial" message) (pig:obj "type" "text_start" "contentIndex" 0 "partial" message)
                         (pig:obj "type" "text_delta" "contentIndex" 0 "delta" text "partial" message)
                         (pig:obj "type" "text_end" "contentIndex" 0 "content" text "partial" message)
                         (pig:obj "type" "done" "reason" "stop" "message" message)))
      (pig:model-stream-push result event))
    result))

(defun snapshot-stream (ctx model request options)
  "A producer thread appends to one shared partial message and pushes it, without yielding between the two: an event must show the message as it was at the push."
  (declare (ignore ctx request options))
  (let* ((block (pig:obj "type" "text" "text" ""))
         (message (pig:obj "role" "assistant" "api" (pig:jget model "api") "provider" (pig:jget model "provider") "model" (pig:jget model "id")
                           "content" (vector block) "stopReason" "stop" "timestamp" 1 "usage" (zero-usage)))
         (result (pig:make-model-event-stream)))
    (sb-thread:make-thread
     (lambda ()
       (pig:model-stream-push result (pig:obj "type" "start" "partial" message))
       (let ((text ""))
         (dotimes (i 100)
           (setf text (concatenate 'string text "x"))
           (setf (gethash "text" block) text)
           (pig:model-stream-push result (pig:obj "type" "text_delta" "contentIndex" 0 "delta" "x" "partial" message)))
         (setf (gethash "text" block) (concatenate 'string text "-AFTER")))
       (pig:model-stream-push result (pig:obj "type" "done" "reason" "stop" "message" message)))
     :name "snapshot producer")
    result))

(defun cancel-burst-stream (ctx model request options)
  "The producer thread is cancelled in the middle of a burst: it pushes start and three text_delta events, waits until its request is cancelled, and goes on pushing."
  (declare (ignore request options))
  (let* ((block (pig:obj "type" "text" "text" ""))
         (message (pig:obj "role" "assistant" "api" (pig:jget model "api") "provider" (pig:jget model "provider") "model" (pig:jget model "id")
                           "content" (vector block) "stopReason" "stop" "timestamp" 1 "usage" (zero-usage)))
         (result (pig:make-model-event-stream)))
    (sb-thread:make-thread
     (lambda ()
       (pig:model-stream-push result (pig:obj "type" "start" "partial" message))
       (dotimes (i 6)
         (when (= i 3)
           (loop repeat 2000 until (pig:ctx-cancelled-p ctx) do (sleep 0.005)))
         (setf (gethash "text" block) (make-string (1+ i) :initial-element #\x))
         (pig:model-stream-push result (pig:obj "type" "text_delta" "contentIndex" 0 "delta" "x" "partial" message)))
       (pig:model-stream-push result (pig:obj "type" "done" "reason" "stop" "message" message)))
     :name "cancel burst producer")
    result))

(defun cancel-end-stream (ctx model request options)
  "The producer answers the cancel of its request by ending its stream with a result: it pushes start and three text_delta events, waits until its request is cancelled, and ends the stream with the message, pushing nothing more."
  (declare (ignore request options))
  (let* ((block (pig:obj "type" "text" "text" ""))
         (message (pig:obj "role" "assistant" "api" (pig:jget model "api") "provider" (pig:jget model "provider") "model" (pig:jget model "id")
                           "content" (vector block) "stopReason" "stop" "timestamp" 1 "usage" (zero-usage)))
         (result (pig:make-model-event-stream)))
    (sb-thread:make-thread
     (lambda ()
       (pig:model-stream-push result (pig:obj "type" "start" "partial" message))
       (dotimes (i 3)
         (setf (gethash "text" block) (make-string (1+ i) :initial-element #\x))
         (pig:model-stream-push result (pig:obj "type" "text_delta" "contentIndex" 0 "delta" "x" "partial" message)))
       (loop repeat 2000 until (pig:ctx-cancelled-p ctx) do (sleep 0.005))
       (pig:model-stream-end result message))
     :name "cancel end producer")
    result))

(defun cancel-error-stream (ctx model request options)
  "The producer answers the abort of its request with an error event that holds its partial message: it pushes start and three text_delta events, waits until its request's signal aborts, and pushes the error event (reason aborted) whose message is the partial with stopReason aborted and its errorMessage."
  (declare (ignore request options))
  (let* ((block (pig:obj "type" "text" "text" ""))
         (message (pig:obj "role" "assistant" "api" (pig:jget model "api") "provider" (pig:jget model "provider") "model" (pig:jget model "id")
                           "content" (vector block) "stopReason" "stop" "timestamp" 1 "usage" (zero-usage)))
         (result (pig:make-model-event-stream)))
    (sb-thread:make-thread
     (lambda ()
       (pig:model-stream-push result (pig:obj "type" "start" "partial" message))
       (dotimes (i 3)
         (setf (gethash "text" block) (make-string (1+ i) :initial-element #\x))
         (pig:model-stream-push result (pig:obj "type" "text_delta" "contentIndex" 0 "delta" "x" "partial" message)))
       (loop repeat 2000 until (pig:ctx-cancelled-p ctx) do (sleep 0.005))
       (setf (gethash "stopReason" message) "aborted" (gethash "errorMessage" message) "the author saw the abort")
       (pig:model-stream-push result (pig:obj "type" "error" "reason" "aborted" "error" message)))
     :name "cancel error producer")
    result))

(defun cancel-throw-stream (ctx model request options)
  "streamSimple waits until its request's signal aborts and fails, showing nothing."
  (declare (ignore model request options))
  (loop repeat 2000 until (pig:ctx-cancelled-p ctx) do (sleep 0.005))
  (error "the author's stream threw at the abort"))

(defun cancel-never-stream (ctx model request options)
  "The author never reacts to the abort of its request: it pushes start and one text_delta whose text is the id of its process, waits until its request's signal aborts, and returns: it pushes nothing more and never ends its stream."
  (declare (ignore request options))
  (let* ((block (pig:obj "type" "text" "text" ""))
         (message (pig:obj "role" "assistant" "api" (pig:jget model "api") "provider" (pig:jget model "provider") "model" (pig:jget model "id")
                           "content" (vector block) "stopReason" "stop" "timestamp" 1 "usage" (zero-usage)))
         (result (pig:make-model-event-stream)))
    (sb-thread:make-thread
     (lambda ()
       (pig:model-stream-push result (pig:obj "type" "start" "partial" message))
       (let ((pid #+win32 "0" #-win32 (format nil "~D" (sb-unix:unix-getpid))))
         (setf (gethash "text" block) pid)
         (pig:model-stream-push result (pig:obj "type" "text_delta" "contentIndex" 0 "delta" pid "partial" message)))
       (loop until (pig:ctx-cancelled-p ctx) do (sleep 0.005)))
     :name "cancel never producer")
    result))

(defun new-extension ()
  (let ((ext (pig:make-extension "provider-producer")))
    (pig:register-provider ext "extension-provider"
                           (pig:obj "api" "issue-8964-extension-api" "baseUrl" "https://extension.invalid" "apiKey" "extension-key"
                                    "models" (vector (pig:obj "id" "faux" "name" "Faux" "reasoning" :false "input" (vector "text")
                                                              "cost" (pig:obj "input" 0 "output" 0 "cacheRead" 0 "cacheWrite" 0)
                                                              "contextWindow" 128000 "maxTokens" 4096))
                                    "streamSimple" #'produce-stream))
    (pig:register-provider ext "snapshot-provider"
                           (pig:obj "api" "snapshot-extension-api" "baseUrl" "https://extension.invalid" "apiKey" "extension-key"
                                    "models" (vector (pig:obj "id" "faux" "name" "Faux" "reasoning" :false "input" (vector "text")
                                                              "cost" (pig:obj "input" 0 "output" 0 "cacheRead" 0 "cacheWrite" 0)
                                                              "contextWindow" 128000 "maxTokens" 4096))
                                    "streamSimple" #'snapshot-stream))
    (pig:register-provider ext "cancel-burst-provider"
                           (pig:obj "api" "cancel-burst-extension-api" "baseUrl" "https://extension.invalid" "apiKey" "extension-key"
                                    "models" (vector (pig:obj "id" "faux" "name" "Faux" "reasoning" :false "input" (vector "text")
                                                              "cost" (pig:obj "input" 0 "output" 0 "cacheRead" 0 "cacheWrite" 0)
                                                              "contextWindow" 128000 "maxTokens" 4096))
                                    "streamSimple" #'cancel-burst-stream))
    (pig:register-provider ext "cancel-end-provider"
                           (pig:obj "api" "cancel-end-extension-api" "baseUrl" "https://extension.invalid" "apiKey" "extension-key"
                                    "models" (vector (pig:obj "id" "faux" "name" "Faux" "reasoning" :false "input" (vector "text")
                                                              "cost" (pig:obj "input" 0 "output" 0 "cacheRead" 0 "cacheWrite" 0)
                                                              "contextWindow" 128000 "maxTokens" 4096))
                                    "streamSimple" #'cancel-end-stream))
    (pig:register-provider ext "cancel-error-provider"
                           (pig:obj "api" "cancel-error-extension-api" "baseUrl" "https://extension.invalid" "apiKey" "extension-key"
                                    "models" (vector (pig:obj "id" "faux" "name" "Faux" "reasoning" :false "input" (vector "text")
                                                              "cost" (pig:obj "input" 0 "output" 0 "cacheRead" 0 "cacheWrite" 0)
                                                              "contextWindow" 128000 "maxTokens" 4096))
                                    "streamSimple" #'cancel-error-stream))
    (pig:register-provider ext "cancel-throw-provider"
                           (pig:obj "api" "cancel-throw-extension-api" "baseUrl" "https://extension.invalid" "apiKey" "extension-key"
                                    "models" (vector (pig:obj "id" "faux" "name" "Faux" "reasoning" :false "input" (vector "text")
                                                              "cost" (pig:obj "input" 0 "output" 0 "cacheRead" 0 "cacheWrite" 0)
                                                              "contextWindow" 128000 "maxTokens" 4096))
                                    "streamSimple" #'cancel-throw-stream))
    (pig:register-provider ext "cancel-never-provider"
                           (pig:obj "api" "cancel-never-extension-api" "baseUrl" "https://extension.invalid" "apiKey" "extension-key"
                                    "models" (vector (pig:obj "id" "faux" "name" "Faux" "reasoning" :false "input" (vector "text")
                                                              "cost" (pig:obj "input" 0 "output" 0 "cacheRead" 0 "cacheWrite" 0)
                                                              "contextWindow" 128000 "maxTokens" 4096))
                                    "streamSimple" #'cancel-never-stream))
    ;; A provider whose model names an api that no extension registers and that has no streamSimple.
    (pig:register-provider ext "unknown-api-provider"
                           (pig:obj "api" "unknown-api-no-extension-registered" "baseUrl" "https://extension.invalid" "apiKey" "extension-key"
                                    "models" (vector (pig:obj "id" "faux" "name" "Faux" "reasoning" :false "input" (vector "text")
                                                              "cost" (pig:obj "input" 0 "output" 0 "cacheRead" 0 "cacheWrite" 0)
                                                              "contextWindow" 128000 "maxTokens" 4096))))
    ext))
