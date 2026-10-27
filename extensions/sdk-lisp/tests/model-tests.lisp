;;;; SPDX-License-Identifier: MIT
;;;; Lane M: the event stream (against the vectors of Pi's own pi-ai) and the model calls against the fake host.
(in-package #:pig-tests)

;;; ---------------------------------------------------------------- the event stream

(defun mt-message (kind timestamp preset)
  (let ((message (pig:obj "role" "assistant" "content" (vector) "stopReason" (if (equal kind "error") "error" "stop"))))
    (cond ((equal timestamp "now") (setf (gethash "timestamp" message) (pig::ms-unix-ms)))
          ((equal timestamp "old") (setf (gethash "timestamp" message) 1)))
    (when (integerp preset) (setf (gethash "durationMs" message) preset))
    message))

(deftest event-stream-times-its-final-message-as-pi-does
  (let ((cases (pig:jget (pig:from-json (read-text-file (repo-path "test/parity/sdk-vectors/pi-ai/createAssistantMessageEventStream.json"))) "cases")))
    (check (plusp (length cases)))
    (loop for case across cases
          do (let* ((scenario (aref (pig:jget case "args") 0)) (answer (pig:jget case "result"))
                    (kind (pig:jget scenario "kind")) (timestamp (pig:jget scenario "timestamp")) (preset (pig:jget scenario "preset"))
                    (stream (pig:create-assistant-message-event-stream))
                    (final (mt-message kind timestamp preset)) (later (mt-message kind "now" nil)))
               (cond ((equal kind "done") (pig:model-stream-push stream (pig:obj "type" "done" "reason" "stop" "message" final)))
                     ((equal kind "error") (pig:model-stream-push stream (pig:obj "type" "error" "reason" "error" "error" final)))
                     ((equal kind "end") (pig:model-stream-end stream final))
                     (t (pig:model-stream-push stream (pig:obj "type" "done" "reason" "stop" "message" final))
                        (pig:model-stream-end stream later)))
               (let ((value (pig:jget final "durationMs")))
                 (check-equal (pig:jget answer "present") (if value t :false) (pig:jget case "name"))
                 (check-equal (pig:jget answer "kept") (if (and (integerp preset) (eql value preset)) t :false) (pig:jget case "name"))
                 (check-equal (pig:jget answer "laterTimed") (if (pig:jget later "durationMs") t :false) (pig:jget case "name"))
                 (when value (check (and (integerp value) (>= value 0)) (pig:jget case "name"))))))))

(deftest an-event-stream-the-sdk-makes-for-a-host-operation-is-not-timed
  (let ((stream (pig:make-model-event-stream))
        (message (pig:obj "role" "assistant" "stopReason" "stop" "timestamp" (expt 2 50))))
    (pig:model-stream-push stream (pig:obj "type" "done" "reason" "stop" "message" message))
    (check (null (pig:jget message "durationMs")))
    (check (eq message (pig:model-stream-result stream)))))

(deftest event-stream-delivers-events-in-order-and-ends-at-the-terminal-event
  (let ((stream (pig:make-model-event-stream)))
    (sb-thread:make-thread
     (lambda ()
       (loop for type in '("start" "text_delta") do (pig:model-stream-push stream (pig:obj "type" type)) (sleep 0.01))
       (pig:model-stream-push stream (pig:obj "type" "done" "message" (pig:obj "id" "final")))
       (pig:model-stream-push stream (pig:obj "type" "late"))))
    (check-equal '("start" "text_delta" "done") (mapcar (lambda (event) (pig:jget event "type")) (pig:model-stream-events stream)))
    (check-equal "final" (pig:jget (pig:model-stream-result stream) "id"))
    (check-equal '(nil nil) (multiple-value-list (pig:model-stream-next stream)))))

(deftest event-stream-ended-without-a-result-yields-none
  (let ((stream (pig:make-model-event-stream)))
    (pig:model-stream-end stream nil)
    (check (null (pig:model-stream-result stream)))))

(deftest event-stream-observes-transport-shutdown-only-where-asked
  (let ((stream (pig:make-model-event-stream)) (stopped nil))
    (let ((waiter (sb-thread:make-thread
                   (lambda () (handler-case (progn (pig:model-stream-next stream :stopped (lambda () stopped)) :returned)
                                (error (e) (princ-to-string e)))))))
      (sleep 0.05)
      (setf stopped t)
      (pig::model-stream-wake stream)
      (check-equal "Provider connection closed" (sb-thread:join-thread waiter :timeout 5)))
    ;; without a stopped function the stream is the producer's alone
    (pig:model-stream-push stream (pig:obj "type" "start"))
    (check-equal "start" (pig:jget (pig:model-stream-next stream) "type"))))

;;; ---------------------------------------------------------------- the registry's state reads

(defvar *mt-state*
  (pig:from-json "{\"models\":[{\"id\":\"gpt\",\"provider\":\"openai\"},{\"id\":\"opus\",\"provider\":\"anthropic\"},{\"id\":\"mini\",\"provider\":\"openai\"}],
\"typedModels\":[{\"type\":\"image\",\"id\":\"flux\",\"provider\":\"openrouter\"},{\"type\":\"classifier\",\"id\":\"jev\",\"provider\":\"typesafe\"},{\"type\":\"classifier\",\"id\":\"jev\",\"provider\":\"openrouter\"}],
\"providers\":{\"openai\":{\"name\":\"OpenAI\",\"configured\":true,\"availableModelIds\":[\"gpt\"],\"authStatus\":{\"configured\":true,\"source\":\"env\"}},\"anthropic\":{\"configured\":false}},
\"registered\":[{\"name\":\"ops\",\"config\":{\"baseUrl\":\"https://ops\"}},{\"name\":\"bare\"}],\"error\":\"models.json is bad\"}"))

(defun mt-ids (models) (map 'list (lambda (model) (format nil "~A/~A" (pig:jget model "provider") (pig:jget model "id"))) models))

(deftest registry-reads-answer-from-the-registry-state
  (with-host (host (probe-extension))
    (let ((n 0))
      (flet ((read-with (thunk)
               (probe-call host thunk *mt-state* (format nil "s~D" (incf n)))
               *thunk-result*))
        (check-equal '("openai/gpt" "anthropic/opus" "openai/mini")
                     (mt-ids (read-with (lambda (c) (pig:registry-get-all (pig:ctx-model-registry c))))))
        (check-equal '("openai/gpt") (mt-ids (read-with (lambda (c) (pig:registry-get-available (pig:ctx-model-registry c))))))
        (check-equal "models.json is bad" (read-with (lambda (c) (pig:registry-get-error (pig:ctx-model-registry c)))))
        (check-equal "OpenAI" (read-with (lambda (c) (pig:registry-get-provider-display-name (pig:ctx-model-registry c) "openai"))))
        (check-equal "nobody" (read-with (lambda (c) (pig:registry-get-provider-display-name (pig:ctx-model-registry c) "nobody"))))
        (check-equal "env" (pig:jget (read-with (lambda (c) (pig:registry-get-provider-auth-status (pig:ctx-model-registry c) "openai"))) "source"))
        (check-equal :false (pig:jget (read-with (lambda (c) (pig:registry-get-provider-auth-status (pig:ctx-model-registry c) "nobody"))) "configured"))
        (check-equal '("ops" "bare") (coerce (read-with (lambda (c) (pig:registry-get-registered-provider-ids (pig:ctx-model-registry c)))) 'list))
        (check-equal "https://ops" (pig:jget (read-with (lambda (c) (pig:registry-get-registered-provider-config (pig:ctx-model-registry c) "ops"))) "baseUrl"))
        (check (null (read-with (lambda (c) (pig:registry-get-registered-provider-config (pig:ctx-model-registry c) "bare")))))
        (check (null (read-with (lambda (c) (pig:registry-get-registered-provider-config (pig:ctx-model-registry c) "none")))))))))

(deftest typed-reads-take-chat-models-from-models-and-the-rest-from-typed-models
  (with-host (host (probe-extension))
    (let ((n 0))
      (flet ((read-with (thunk)
               (probe-call host thunk *mt-state* (format nil "t~D" (incf n)))
               *thunk-result*))
        (check-equal '("openai/gpt" "anthropic/opus" "openai/mini")
                     (mt-ids (read-with (lambda (c) (pig:registry-get-models-of-type (pig:ctx-model-registry c) "chat")))))
        (check-equal '("typesafe/jev" "openrouter/jev")
                     (mt-ids (read-with (lambda (c) (pig:registry-get-models-of-type (pig:ctx-model-registry c) "classifier")))))
        (check-equal '("openrouter/flux")
                     (mt-ids (read-with (lambda (c) (pig:registry-get-models-of-type (pig:ctx-model-registry c) "image" "openrouter")))))
        (check-equal '() (mt-ids (read-with (lambda (c) (pig:registry-get-models-of-type (pig:ctx-model-registry c) "audio")))))
        (check-equal "typesafe" (pig:jget (read-with (lambda (c) (pig:registry-find-of-type (pig:ctx-model-registry c) "classifier" "typesafe" "jev"))) "provider"))
        (check (null (read-with (lambda (c) (pig:registry-find-of-type (pig:ctx-model-registry c) "classifier" "typesafe" "missing")))))
        (check-equal "gpt" (pig:jget (read-with (lambda (c) (pig:registry-get-model-of-type (pig:ctx-model-registry c) "chat" "openai" "gpt"))) "id"))))))

(deftest registry-host-calls-carry-their-wire-members
  (with-host (host (probe-extension))
    (let ((call (probe-call host (lambda (c) (pig:registry-get-api-key-and-headers
                                              (pig:ctx-model-registry c) (pig:obj "provider" (pig:obj "id" "p") "modelId" "m" "id" "ignored")))
                            (pig:obj "ok" t "apiKey" "k") "a1")))
      (check-equal "getModelAuth" (pig:jget call "method"))
      (check-equal "p" (pig:jget (pig:jget call "args") "provider"))
      (check-equal "m" (pig:jget (pig:jget call "args") "modelId"))
      (check-equal "k" (pig:jget *thunk-result* "apiKey")))
    (probe-call host (lambda (c) (pig:registry-get-api-key-for-provider (pig:ctx-model-registry c) "p"))
                (pig:obj "auth" (pig:obj "apiKey" "sk-1")) "a2")
    (check-equal "sk-1" *thunk-result*)
    (probe-call host (lambda (c) (pig:registry-get-api-key-for-provider (pig:ctx-model-registry c) "p")) nil "a3")
    (check (null *thunk-result*))
    (probe-call host (lambda (c) (pig:registry-refresh (pig:ctx-model-registry c) (pig:obj "force" t)))
                (pig:obj "aborted" :false "errors" (vector) "extra" 1) "a4")
    (check-equal '("aborted" "errors") (loop for key being the hash-keys of *thunk-result* collect key))))

;;; ---------------------------------------------------------------- classify, generateImages, complete

(defvar *mt-model* (pig:obj "api" "typesafe-system-one" "provider" "typesafe" "id" "jev-latest"))

(deftest classify-and-generate-images-send-model-context-and-options
  (with-host (host (probe-extension))
    (let ((call (probe-call host (lambda (c) (pig:registry-classify (pig:ctx-model-registry c) *mt-model* (pig:obj "state" (pig:obj "text" "x")) (pig:obj "apiKey" "sk")))
                            (pig:obj "stopReason" "stop" "answers" (pig:obj "b" 1 "a" 2)) "c1")))
      (check-equal "classify" (pig:jget call "method"))
      (check-equal "jev-latest" (pig:jget (pig:jget (pig:jget call "args") "model") "id"))
      (check-equal "sk" (pig:jget (pig:jget (pig:jget call "args") "options") "apiKey"))
      (check-equal '("b" "a") (loop for key being the hash-keys of (pig:jget *thunk-result* "answers") collect key)))
    (let ((call (probe-call host (lambda (c) (pig:registry-generate-images (pig:ctx-model-registry c) *mt-model* (pig:obj "input" (vector))))
                            (pig:obj "stopReason" "stop") "c2")))
      (check-equal "generateImages" (pig:jget call "method"))
      (check (not (nth-value 1 (gethash "options" (pig:jget call "args"))))))))

(defun mt-fail-call (host thunk id message)
  (setf *thunk* thunk)
  (request host id "command" "tool" "c" "args" "")
  (let ((call (hread-type host "call")))
    (hsend host "type" "call_result" "id" (pig:jget call "id") "call_result" (pig:obj "error" (pig:obj "message" message)))
    (hread-type host "response")
    *thunk-result*))

(deftest classify-and-generate-images-report-a-failure-as-a-result
  (with-host (host (probe-extension))
    (let ((failed (mt-fail-call host (lambda (c) (pig:registry-classify (pig:ctx-model-registry c) *mt-model* (pig:obj))) "f1" "host went away")))
      (check-equal "error" (pig:jget failed "stopReason"))
      (check-equal "host went away" (pig:jget failed "errorMessage"))
      (check-equal '("typesafe-system-one" "typesafe" "jev-latest") (mapcar (lambda (k) (pig:jget failed k)) '("api" "provider" "model")))
      (check (zerop (hash-table-count (pig:jget failed "answers"))))
      (check (plusp (pig:jget failed "timestamp"))))
    (let ((failed (mt-fail-call host (lambda (c) (pig:registry-generate-images (pig:ctx-model-registry c) *mt-model* (pig:obj))) "f2" "no image model")))
      (check-equal "error" (pig:jget failed "stopReason"))
      (check-equal 0 (length (pig:jget failed "output"))))))

(deftest a-cancelled-classification-is-an-aborted-result
  (with-host (host (probe-extension))
    (setf *thunk* (lambda (c) (pig:registry-classify (pig:ctx-model-registry c) *mt-model* (pig:obj))))
    (request host "k1" "command" "tool" "c" "args" "")
    (check-equal "classify" (pig:jget (pig:jget (hread-type host "call") "call") "method"))
    (hsend host "type" "cancel" "id" "k1" "cancel" (pig:obj "request_id" "k1" "reason" "user"))
    (hread-type host "response")
    (check-equal "aborted" (pig:jget *thunk-result* "stopReason"))
    (check-equal "jev-latest" (pig:jget *thunk-result* "model"))))

(deftest context-complete-sends-model-request-and-auth
  (with-host (host (probe-extension))
    (let ((call (probe-call host (lambda (c) (pig:ctx-complete c (pig:obj "id" "m") (pig:obj "messages" (vector)) (pig:obj "apiKey" "k")))
                            (pig:obj "text" "answer") "x1")))
      (check-equal "complete" (pig:jget call "method"))
      (check-equal '("model" "request" "auth") (loop for key being the hash-keys of (pig:jget call "args") collect key))
      (check-equal "answer" (pig:jget *thunk-result* "text")))))

;;; ---------------------------------------------------------------- streams

(defun mt-send-event (host id event)
  (hsend host "type" "notify" "notify" (pig:obj "method" "model_stream_event" "args" (pig:obj "streamId" id "event" event))))

(defun mt-finish-call (host call &optional error)
  (hsend host "type" "call_result" "id" (pig:jget call "id")
              "call_result" (if error (pig:obj "error" (pig:obj "message" error)) (pig:obj "result" (pig:obj)))))

(defun mt-done-event (text)
  (pig:obj "type" "done" "reason" "stop" "message" (pig:obj "role" "assistant" "stopReason" "stop" "text" text)))

(deftest a-stream-forwards-the-hosts-events-and-the-call-carries-the-merged-request
  (with-host (host (probe-extension))
    (setf *thunk* (lambda (c)
                    (let ((stream (pig:registry-stream (pig:ctx-model-registry c) (pig:obj "provider" "p" "modelId" "m")
                                                       (pig:obj "messages" (vector)) (pig:obj "maxTokens" 7))))
                      (list (mapcar (lambda (event) (pig:jget event "type")) (pig:model-stream-events stream))
                            (pig:jget (pig:model-stream-result stream) "text")))))
    (request host "m1" "command" "tool" "c" "args" "")
    (let* ((frame (hread-type host "call")) (call (pig:jget frame "call")) (args (pig:jget call "args")))
      (check-equal "modelStream" (pig:jget call "method"))
      (check-equal "model-stream-1" (pig:jget args "streamId"))
      (check-equal :false (pig:jget args "simple"))
      (check-equal 7 (pig:jget (pig:jget args "request") "maxTokens"))
      (check (vectorp (pig:jget (pig:jget args "request") "messages")))
      (check (not (nth-value 1 (gethash "onPayload" args))))
      (hsend host "type" "notify" "notify" (pig:obj "method" "model_stream_event" "args" (pig:obj "streamId" "model-stream-1" "started" t)))
      (mt-send-event host "model-stream-1" (pig:obj "type" "start"))
      (mt-send-event host "model-stream-1" (mt-done-event "streamed"))
      (mt-finish-call host frame))
    (hread-type host "response")
    (check-equal '(("start" "done") "streamed") *thunk-result*)))

(deftest stream-simple-asks-for-the-simple-path-and-complete-answers-the-final-message
  (with-host (host (probe-extension))
    (setf *thunk* (lambda (c) (pig:registry-complete (pig:ctx-model-registry c) (pig:obj "provider" "p" "modelId" "m") (pig:obj) )))
    (request host "m2" "command" "tool" "c" "args" "")
    (let ((frame (hread-type host "call")))
      (mt-send-event host "model-stream-1" (mt-done-event "completed"))
      (mt-finish-call host frame))
    (hread-type host "response")
    (check-equal "completed" (pig:jget *thunk-result* "text"))
    (setf *thunk* (lambda (c) (pig:model-stream-result (pig:registry-stream-simple (pig:ctx-model-registry c) (pig:obj "provider" "p") (pig:obj)))))
    (request host "m3" "command" "tool" "c" "args" "")
    (let ((frame (hread-type host "call")))
      (check-equal t (pig:jget (pig:jget (pig:jget frame "call") "args") "simple"))
      (mt-send-event host "model-stream-2" (mt-done-event "simple"))
      (mt-finish-call host frame))
    (hread-type host "response")
    (check-equal "simple" (pig:jget *thunk-result* "text"))))

(deftest a-failed-model-call-ends-the-stream-with-an-error-event-naming-the-model
  (with-host (host (probe-extension))
    (let* ((model (pig:obj "provider" "conformance" "modelId" "protocol-error" "api" "openai-responses"))
           (result (mt-fail-call host (lambda (c) (pig:registry-complete (pig:ctx-model-registry c) model (pig:obj))) "e1" "transport boom")))
      (check-equal '("assistant" "error" "transport boom" "conformance" "protocol-error" "openai-responses")
                   (mapcar (lambda (k) (pig:jget result k)) '("role" "stopReason" "errorMessage" "provider" "model" "api")))
      (check (plusp (pig:jget result "timestamp")))
      (check-equal 0 (length (pig:jget result "content")))
      (check-equal 0 (pig:jget (pig:jget result "usage") "totalTokens")))))

(deftest a-call-that-answers-without-a-terminal-event-fails-the-stream
  (with-host (host (probe-extension))
    (setf *thunk* (lambda (c) (pig:registry-complete (pig:ctx-model-registry c) (pig:obj "provider" "p" "id" "m") (pig:obj))))
    (request host "n1" "command" "tool" "c" "args" "")
    (let ((frame (hread-type host "call")))
      (mt-send-event host "model-stream-1" (pig:obj "type" "start"))
      (mt-finish-call host frame))
    (hread-type host "response")
    (check-equal "error" (pig:jget *thunk-result* "stopReason"))
    (check-equal "model stream ended without a terminal event" (pig:jget *thunk-result* "errorMessage"))
    (check-equal "m" (pig:jget *thunk-result* "model"))))

(deftest a-cancelled-request-ends-its-stream-with-an-error-event
  (with-host (host (probe-extension))
    (setf *thunk* (lambda (c) (pig:registry-complete (pig:ctx-model-registry c) (pig:obj "provider" "p" "modelId" "m") (pig:obj))))
    (request host "q1" "command" "tool" "c" "args" "")
    (hread-type host "call")
    (hsend host "type" "cancel" "id" "q1" "cancel" (pig:obj "request_id" "q1" "reason" "user"))
    (hread-type host "response")
    (check-equal "error" (pig:jget *thunk-result* "stopReason"))
    (check (search "cancelled" (pig:jget *thunk-result* "errorMessage")))))

(defun mt-callback (host id args)
  "Send the host's model_stream_callback request and return the response's (values result error-message)."
  (request host id "model_stream_callback" "args" args)
  (let ((response (pig:jget (hread-type host "response") "response")))
    (values (pig:jget response "result") (let ((e (pig:jget response "error"))) (and (hash-table-p e) (pig:jget e "message"))))))

(deftest the-provider-request-callbacks-stay-in-the-extension-and-are-flagged
  (with-host (host (probe-extension))
    (let ((seen '()))
      (setf *thunk* (lambda (c)
                      (pig:model-stream-result
                       (pig:registry-stream (pig:ctx-model-registry c) (pig:obj "provider" "p" "id" "declared") (pig:obj "messages" (vector))
                                            (pig:obj "onPayload" (lambda (payload model) (push (list :payload (pig:jget payload "original") (pig:jget model "id")) seen)
                                                                   (let ((copy (pig::ms-copy-object payload))) (setf (gethash "mark" copy) "on-payload") copy))
                                                     "onResponse" (lambda (response model) (declare (ignore model)) (push (list :response (pig:jget response "status")) seen) nil)
                                                     "transformHeaders" (lambda (headers model) (declare (ignore model))
                                                                          (let ((copy (pig::ms-copy-object headers))) (setf (gethash "x-transformed" copy) "yes") copy))
                                                     "maxTokens" 3)))))
      (request host "p1" "command" "tool" "c" "args" "")
      (let* ((frame (hread-type host "call")) (args (pig:jget (pig:jget frame "call") "args")))
        (check-equal '(t t t :false) (list (pig:jget args "onPayload") (pig:jget args "onResponse") (pig:jget args "transformHeaders") (if (nth-value 1 (gethash "fetch" args)) t :false)))
        (check-equal 3 (pig:jget (pig:jget args "request") "maxTokens"))
        (check (not (nth-value 1 (gethash "onPayload" (pig:jget args "request")))))
        (let ((payload (mt-callback host "cb1" (pig:obj "streamId" "model-stream-1" "callback" "onPayload" "value" (pig:obj "original" t)))))
          (check-equal t (pig:jget payload "defined"))
          (check-equal "on-payload" (pig:jget (pig:jget payload "value") "mark"))
          (check-equal t (pig:jget (pig:jget payload "value") "original")))
        (mt-callback host "cb2" (pig:obj "streamId" "model-stream-1" "callback" "onResponse" "value" (pig:obj "status" 201)))
        (let ((headers (mt-callback host "cb3" (pig:obj "streamId" "model-stream-1" "callback" "transformHeaders" "value" (pig:obj "x-in" "1")))))
          (check-equal '("1" "yes") (list (pig:jget headers "x-in") (pig:jget headers "x-transformed"))))
        (multiple-value-bind (result message) (mt-callback host "cb4" (pig:obj "streamId" "model-stream-1" "callback" "nope"))
          (check (null result))
          (check-equal "unknown model stream callback model-stream-1/nope" message))
        (mt-send-event host "model-stream-1" (mt-done-event "x"))
        (mt-finish-call host frame))
      (hread-type host "response")
      (check-equal '((:response 201) (:payload t "declared")) seen))))

(deftest an-undefined-on-payload-result-is-not-defined
  (with-host (host (probe-extension))
    (setf *thunk* (lambda (c) (pig:model-stream-result
                               (pig:registry-stream (pig:ctx-model-registry c) (pig:obj "id" "m") (pig:obj) (pig:obj "onPayload" (lambda (payload model) (declare (ignore payload model)) nil))))))
    (request host "u1" "command" "tool" "c" "args" "")
    (let ((frame (hread-type host "call")))
      (check-equal :false (pig:jget (mt-callback host "cb1" (pig:obj "streamId" "model-stream-1" "callback" "onPayload" "value" (pig:obj))) "defined"))
      (mt-send-event host "model-stream-1" (mt-done-event "x"))
      (mt-finish-call host frame))
    (hread-type host "response")))

(deftest the-fetch-option-serves-the-hosts-fetch-reads-and-close
  (with-host (host (probe-extension))
    (let ((seen nil) (signal nil) (body-size 70000))
      (setf *thunk* (lambda (c)
                      (pig:model-stream-result
                       (pig:registry-stream
                        (pig:ctx-model-registry c) (pig:obj "provider" "p" "id" "m") (pig:obj)
                        (pig:obj "fetch" (lambda (call)
                                           (setf signal (pig:jget call "signal"))
                                           (setf seen (list (pig:jget call "url") (pig:jget call "method")
                                                            (aref (pig:jget (pig:jget call "headers") "X-Host") 0)
                                                            (sb-ext:octets-to-string (pig:jget call "body") :external-format :utf-8)))
                                           (pig:obj "status" 207 "statusText" "Answered"
                                                    "headers" (pig:obj "x-sdk-fetch" "answered")
                                                    "body" (let ((offset 0))
                                                             (lambda ()
                                                               (when (< offset body-size)
                                                                 (let* ((end (min body-size (+ offset 30000)))
                                                                        (chunk (make-array (- end offset) :element-type '(unsigned-byte 8))))
                                                                   (dotimes (i (length chunk)) (setf (aref chunk i) (mod (+ offset i) 251)))
                                                                   (setf offset end)
                                                                   chunk))))))))))))
      (request host "g1" "command" "tool" "c" "args" "")
      (let* ((frame (hread-type host "call")) (args (pig:jget (pig:jget frame "call") "args")))
        (check-equal t (pig:jget args "fetch"))
        (let ((response (mt-callback host "g2" (pig:obj "streamId" "model-stream-1" "callback" "fetch"
                                                        "value" (pig:obj "id" 5 "url" "https://fetch.invalid/v1/chat?x=1" "method" "POST"
                                                                         "headers" (pig:obj "X-Host" (vector "1")) "body" "cGluZy1ib2R5")))))
          (check-equal '(207 "Answered" t) (list (pig:jget response "status") (pig:jget response "statusText") (pig:jget response "body")))
          (check-equal '(("x-sdk-fetch" "answered")) (map 'list #'identity (map 'list (lambda (pair) (coerce pair 'list)) (pig:jget response "headers")))))
        (check-equal '("https://fetch.invalid/v1/chat?x=1" "POST" "1" "ping-body") seen)
        (let ((total 0) (reads 0))
          (loop
            (let ((chunk (mt-callback host (format nil "r~D" reads) (pig:obj "streamId" "model-stream-1" "callback" "fetchRead" "value" (pig:obj "id" 5 "size" 65536)))))
              (incf reads)
              (when (eq (pig:jget chunk "done") t) (return))
              (let ((octets (pig::ms-base64-decode (pig:jget chunk "data"))))
                (check (<= (length octets) 30000))
                (dotimes (i (length octets)) (unless (= (aref octets i) (mod (+ total i) 251)) (check nil "body bytes") (return)))
                (incf total (length octets)))))
          (check-equal 70000 total))
        (multiple-value-bind (result message) (mt-callback host "rb" (pig:obj "streamId" "model-stream-1" "callback" "fetchRead" "value" (pig:obj "id" 5 "size" 0)))
          (check (null result))
          (check-equal "unknown model fetch response 5" message))
        (check-equal nil (nth-value 1 (mt-callback host "c1" (pig:obj "streamId" "model-stream-1" "callback" "fetchClose" "value" (pig:obj "id" 5)))))
        (check (pig:abort-signal-set-p signal))
        (check-equal nil (nth-value 1 (mt-callback host "c2" (pig:obj "streamId" "model-stream-1" "callback" "fetchClose" "value" (pig:obj "id" 5)))))
        (mt-send-event host "model-stream-1" (mt-done-event "x"))
        (mt-finish-call host frame))
      (hread-type host "response"))))

(deftest base64-round-trips-every-padding
  (dolist (text '("" "a" "ab" "abc" "abcd" "any carnal pleas"))
    (let ((octets (sb-ext:string-to-octets text :external-format :utf-8)))
      (check-equal text (sb-ext:octets-to-string (pig::ms-base64-decode (pig::ms-base64-encode octets)) :external-format :utf-8))))
  (check-equal "cGluZy1ib2R5" (pig::ms-base64-encode (sb-ext:string-to-octets "ping-body" :external-format :utf-8))))
