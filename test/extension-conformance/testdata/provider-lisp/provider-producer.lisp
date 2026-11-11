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
    ext))
