(in-package "lisp-sdk-fixture")
;;; the probe commands, tools and event handlers of lane M (see def-fixture in fixture.lisp): the model calls of testdata/python-sdk-fixture/main.py
;;; (model-stream-probe, model-stream-callback-probe, model-stream-fetch-probe) and the typed model tools of the model-types rows.

(defun mp-fail (control &rest arguments) (error "~?" control arguments))

(defun mp-equal (a b)
  "Deep equality of two JSON values: objects by members whatever their order, numbers by value."
  (cond ((and (hash-table-p a) (hash-table-p b))
         (and (= (hash-table-count a) (hash-table-count b))
              (loop for key being the hash-keys of a using (hash-value value)
                    always (multiple-value-bind (other found) (gethash key b) (and found (mp-equal value other))))))
        ((and (vectorp a) (vectorp b) (not (stringp a)) (not (stringp b)))
         (and (= (length a) (length b)) (every #'mp-equal a b)))
        ((and (numberp a) (numberp b)) (= a b))
        (t (equal a b))))

(defun mp-check (label expected actual)
  (unless (mp-equal expected actual) (mp-fail "~A: ~A, want ~A" label (pig:to-json actual) (pig:to-json expected))))

(defun mp-copy (object &rest members)
  (let ((copy (make-hash-table :test 'equal)))
    (maphash (lambda (key value) (setf (gethash key copy) value)) object)
    (loop for (key value) on members by #'cddr do (setf (gethash key copy) value))
    copy))

(defun mp-zero-cost () (pig:obj "input" 0 "output" 0 "cacheRead" 0 "cacheWrite" 0 "total" 0))

(defun mp-field-rich-request ()
  (pig:obj
   "systemPrompt" "conformance-system"
   "messages" (pig:arr
               (pig:obj "role" "system"
                        "content" (pig:arr (pig:obj "type" "text" "text" "signed system" "textSignature" "system-signature"))
                        "sections" (pig:obj "zeta" "last-first" "alpha" :null "middle" "middle") "timestamp" 41)
               (pig:obj "role" "user" "content" "hello" "timestamp" 42)
               (pig:obj "role" "assistant" "content" (pig:arr (pig:obj "type" "text" "text" "prior" "textSignature" "signed"))
                        "api" "openai-responses" "provider" "prior-provider" "model" "prior-model"
                        "usage" (pig:obj "input" 1 "output" 2 "cacheRead" 3 "cacheWrite" 4 "totalTokens" 10
                                         "cost" (pig:obj "input" 0.1d0 "output" 0.2d0 "cacheRead" 0.3d0 "cacheWrite" 0.4d0 "total" 1.0d0))
                        "stopReason" "stop" "timestamp" 43))
   "tools" (pig:arr (pig:obj "name" "lookup" "description" "lookup" "parameters" (pig:obj "type" "object")
                             "constrainedSampling" (pig:obj "type" "grammar" "variants" (pig:obj "openai_lark" "start: NUMBER"))))))

(defun mp-stream-options ()
  (pig:obj "timeoutMs" 0 "websocketConnectTimeoutMs" 1234 "maxRetries" 2 "maxRetryDelayMs" 3000
           "maxTokens" 321 "temperature" 0.65d0 "samplingParams" (pig:obj "topP" 0.8d0)
           "thinkingBudgets" (pig:obj "minimal" 11 "low" 22 "medium" 33 "high" 44) "reasoning" "high" "isReasoning" t
           "env" (pig:obj "WIRE_ENV" "request-value" "SECOND_ENV" "distinct-value")
           "headers" (pig:obj "X-Wire" "yes" "X-Remove" :null) "sessionId" "conformance-session" "transport" "sse"))

(defun mp-event-types (stream) (mapcar (lambda (event) (pig:jget event "type")) (pig:model-stream-events stream)))

(defun mp-model-stream-probe (ctx)
  (let* ((registry (pig:ctx-model-registry ctx))
         (current (pig:registry-find registry "conformance" "current"))
         (found (pig:registry-find registry "conformance" "declared"))
         (slash (pig:registry-find registry "conformance" "org/model/name"))
         (limits (pig:obj "maxRequestBytes" 12345
                          "images" (pig:obj "maxPerMessage" 7 "maxPerRequest" 11
                                            "resize" (pig:obj "maxWidth" 321 "maxHeight" 123 "maxBytes" 45678 "jpegQuality" 67)))))
    (unless (and current (equal (pig:jget current "id") "current")) (mp-fail "find current = ~A" (pig:to-json current)))
    (unless (and found (equal (pig:jget found "id") "declared") (equal (pig:jget found "provider") "conformance"))
      (mp-fail "find declared = ~A" (pig:to-json found)))
    (unless (and slash (equal (pig:jget slash "id") "org/model/name")) (mp-fail "find slash = ~A" (pig:to-json slash)))
    (unless (and (mp-equal limits (pig:jget slash "inputLimits")) (mp-equal limits (pig:jget (pig:ctx-get-model-info ctx) "inputLimits")))
      (mp-fail "model inputLimits = ~A" (pig:to-json slash)))
    (dolist (key '("baseUrl" "input" "cost" "thinkingLevelMap" "promptCache" "contextWindow" "maxTokens" "samplingParams" "headers" "compat"))
      (unless (nth-value 1 (gethash key slash)) (mp-fail "find slash lacks ~A: ~A" key (pig:to-json slash))))
    (unless (and (zerop (length (pig:jget slash "input"))) (eql (pig:jget (pig:jget slash "cost") "input") 0)
                 (= (length (pig:jget (pig:jget slash "cost") "tiers")) 1)
                 (eq (pig:jget (pig:jget slash "compat") "supportsStrictMode") :false))
      (mp-fail "find slash shape = ~A" (pig:to-json slash)))
    (when (pig:registry-find registry "conformance" "missing") (mp-fail "find missing returned a model"))
    (when (pig:registry-find registry "conformance" "override-only") (mp-fail "find override-only returned a model"))
    (mp-check "auth"
              (pig:obj "ok" t "apiKey" "conformance-key" "headers" (pig:obj "X-Conformance-Auth" "yes")
                       "baseUrl" "https://models.invalid/v1" "env" (pig:obj "CONFORMANCE_AUTH" "yes"))
              (pig:registry-get-api-key-and-headers registry found))
    (let* ((model (pig:obj "provider" "conformance" "modelId" "declared" "api" "openai-responses"))
           (request (mp-field-rich-request)) (options (mp-stream-options))
           (types (pig:arr "start" "text_start" "text_delta" "text_end" "done"))
           (stream (pig:registry-stream registry model request options)))
      (mp-check "stream events" types (coerce (mp-event-types stream) 'simple-vector))
      (unless (equal (pig:jget (aref (pig:jget (pig:model-stream-result stream) "content") 0) "text") "streamed")
        (mp-fail "stream result = ~A" (pig:to-json (pig:model-stream-result stream))))
      (let ((simple (pig:registry-stream-simple registry model request options)))
        (mp-check "simple events" types (coerce (mp-event-types simple) 'simple-vector))
        (unless (equal (pig:jget (pig:model-stream-result simple) "stopReason") "stop") (mp-fail "simple did not stop")))
      (unless (equal (pig:jget (pig:registry-complete registry model request options) "stopReason") "stop") (mp-fail "complete did not stop"))
      (unless (equal (pig:jget (pig:model-stream-result (pig:registry-stream registry model request options)) "stopReason") "stop")
        (mp-fail "result without iteration did not stop"))
      (let ((unknown (pig:registry-complete registry (pig:obj "provider" "conformance" "modelId" "unknown" "api" "openai-responses") request options)))
        (unless (and (equal (pig:jget unknown "stopReason") "error") (search "unknown model" (pig:jget unknown "errorMessage")))
          (mp-fail "unknown result = ~A" (pig:to-json unknown))))
      (let ((failed (pig:registry-complete registry (pig:obj "provider" "conformance" "modelId" "protocol-error" "api" "openai-responses") request options)))
        (let ((timestamp (pig:jget failed "timestamp")))
          (unless (and (integerp timestamp) (plusp timestamp)) (mp-fail "transport timestamp = ~A" timestamp)))
        (remhash "timestamp" failed)
        (mp-check "transport result"
                  (pig:obj "role" "assistant" "content" (vector) "api" "openai-responses" "provider" "conformance" "model" "protocol-error"
                           "usage" (pig:obj "input" 0 "output" 0 "cacheRead" 0 "cacheWrite" 0 "totalTokens" 0 "cost" (mp-zero-cost))
                           "stopReason" "error" "errorMessage" "transport boom")
                  failed)))
    (pig:ctx-notify ctx "model-stream=ok" "info")))

(defun mp-callback-model ()
  (pig:obj "provider" "conformance" "id" "declared" "modelId" "declared" "api" "openai-responses"))

(defun mp-hello-request (system-prompt)
  (pig:obj "systemPrompt" system-prompt "messages" (pig:arr (pig:obj "role" "user" "content" "hello" "timestamp" 1))))

(defun mp-model-stream-callback-probe (ctx)
  (let ((registry (pig:ctx-model-registry ctx)) (model (mp-callback-model)) (request (mp-hello-request "callbacks"))
        payload callback-model response)
    (let ((stream (pig:registry-stream
                   registry model request
                   (pig:obj "onPayload" (lambda (value callback-model-value)
                                          (setf payload value callback-model (pig:jget callback-model-value "id"))
                                          (mp-copy value "mark" "on-payload"))
                            "onResponse" (lambda (value callback-model-value) (declare (ignore callback-model-value)) (setf response value) nil)
                            "transformHeaders" (lambda (headers callback-model-value) (declare (ignore callback-model-value))
                                                 (mp-copy headers "x-transformed" "yes"))))))
      (unless (equal (pig:jget (pig:model-stream-result stream) "stopReason") "stop") (mp-fail "callback stream did not stop")))
    (unless (and (eq (pig:jget payload "original") t) (equal callback-model "declared")) (mp-fail "onPayload saw ~A ~A" (pig:to-json payload) callback-model))
    (unless (and (eql (pig:jget response "status") 201) (equal (pig:jget (pig:jget response "headers") "x-upstream") "seen"))
      (mp-fail "onResponse saw ~A" (pig:to-json response)))
    (unless (equal (pig:jget (pig:model-stream-result (pig:registry-stream registry model request (pig:obj))) "stopReason") "stop")
      (mp-fail "plain stream did not stop"))
    (pig:ctx-notify ctx "model-callbacks=ok" "info")))

(defun mp-fetch-body ()
  (let ((body (make-array 70000 :element-type '(unsigned-byte 8))))
    (dotimes (i (length body) body) (setf (aref body i) (mod i 251)))))

(defun mp-model-stream-fetch-probe (ctx)
  (let ((registry (pig:ctx-model-registry ctx)) (model (mp-callback-model)) (request (mp-hello-request "fetch")) (seen nil))
    (let ((stream (pig:registry-stream
                   registry model request
                   (pig:obj "fetch" (lambda (call)
                                      (let ((host (pig:jget (pig:jget call "headers") "X-Host")))
                                        (setf seen (pig:obj "url" (pig:jget call "url") "method" (pig:jget call "method")
                                                            "host" (and host (aref host 0))
                                                            "body" (sb-ext:octets-to-string (pig:jget call "body") :external-format :utf-8))))
                                      (pig:obj "status" 207 "statusText" "Answered" "headers" (pig:obj "x-sdk-fetch" "answered")
                                               "body" (mp-fetch-body)))))))
      (unless (equal (pig:jget (pig:model-stream-result stream) "stopReason") "stop") (mp-fail "fetch stream did not stop")))
    (mp-check "fetch saw" (pig:obj "url" "https://fetch.invalid/v1/chat?x=1" "method" "POST" "host" "1" "body" "ping-body") seen)
    (unless (equal (pig:jget (pig:model-stream-result (pig:registry-stream registry model request (pig:obj))) "stopReason") "stop")
      (mp-fail "plain stream did not stop"))
    (pig:ctx-notify ctx "model-fetch=ok" "info")))

;;; Context.complete and a model stream cancelled with its request (lisp_model_calls_test.go)

(defvar *mp-cancel-result* nil)

(defun mp-complete-probe (ctx)
  (let ((result (pig:ctx-complete ctx (pig:obj "provider" "p" "id" "m") (mp-hello-request "complete") (pig:obj "apiKey" "sk-complete"))))
    (pig:ctx-notify ctx (format nil "complete=~A" (pig:jget result "text")) "info")))

(defun mp-cancel-probe (ctx)
  (setf *mp-cancel-result*
        (pig:registry-complete (pig:ctx-model-registry ctx) (pig:obj "provider" "conformance" "modelId" "hang" "api" "openai-responses")
                               (mp-hello-request "cancel"))))

(defun mp-cancel-report (ctx)
  (pig:ctx-notify ctx (if *mp-cancel-result*
                          (format nil "cancel=~A:~A" (pig:jget *mp-cancel-result* "stopReason") (pig:jget *mp-cancel-result* "model"))
                          "cancel=pending")
                  "info"))

;;; the typed model tools (model_types_test.go): each answers one JSON object

(defun mp-ids (models) (map 'list (lambda (model) (format nil "~A/~A" (pig:jget model "provider") (pig:jget model "id"))) models))

(defun mp-approval (text)
  (pig:obj "state" (pig:obj "text" text)
           "questions" (pig:obj "tone" (pig:obj "type" "choice" "instructions" "Which tone?" "criteria" (pig:obj "warm" "Warm" "cold" "Cold"))
                                "approved" (pig:obj "type" "bool" "instructions" "Does this express approval?"
                                                    "criteria" (pig:obj "true" "Approval" "false" "No approval")))))

(defun mp-typed-reads (ctx params)
  (declare (ignore params))
  (let* ((registry (pig:ctx-model-registry ctx))
         (found (pig:registry-find-of-type registry "classifier" "typesafe" "jev-latest"))
         (of-type (pig:registry-get-model-of-type registry "classifier" "typesafe" "jev-latest")))
    (pig:to-json
     (pig:obj "ofTypeWindow" (pig:jget of-type "contextWindow")
              "ofTypeMissing" (pig:registry-get-model-of-type registry "classifier" "typesafe" "missing")
              "classifiers" (mp-ids (pig:registry-get-models-of-type registry "classifier"))
              "chat" (mp-ids (pig:registry-get-models-of-type registry "chat"))
              "images" (mp-ids (pig:registry-get-models-of-type registry "image" "openrouter"))
              "available" (mp-ids (pig:registry-get-available-of-type registry "classifier" "typesafe"))
              "foundWindow" (pig:jget found "contextWindow")
              "missing" (pig:registry-find-of-type registry "classifier" "typesafe" "missing")))))

(defun mp-classify-probe (ctx params)
  (declare (ignore params))
  (let* ((registry (pig:ctx-model-registry ctx))
         (model (pig:registry-find-of-type registry "classifier" "typesafe" "jev-latest"))
         (result (pig:registry-classify registry model (mp-approval "Looks good") (pig:obj "apiKey" "sk-conf")))
         (failed (pig:registry-classify registry model (mp-approval "fail")))
         (with-images (pig:registry-classify registry model
                                             (mp-copy (mp-approval "Looks good")
                                                      "images" (pig:arr (pig:obj "type" "image" "data" "aW1hZ2U=" "mimeType" "image/png"))))))
    (pig:to-json
     (pig:obj "stop" (pig:jget result "stopReason")
              "answers" (loop for key being the hash-keys of (pig:jget result "answers") collect key)
              "approved" (pig:jget (pig:jget (pig:jget result "answers") "approved") "probability")
              "imagesApproved" (pig:jget (pig:jget (pig:jget with-images "answers") "approved") "probability")
              "model" (pig:jget result "model")
              "failedStop" (pig:jget failed "stopReason") "failedMessage" (pig:jget failed "errorMessage") "failedProvider" (pig:jget failed "provider")))))

(defun mp-images-probe (ctx params)
  (declare (ignore params))
  (let* ((registry (pig:ctx-model-registry ctx))
         (model (pig:registry-find-of-type registry "image" "openrouter" "flux"))
         (prompt (pig:obj "input" (pig:arr (pig:obj "type" "text" "text" "a red circle"))))
         (result (pig:registry-generate-images registry model prompt (pig:obj "apiKey" "sk-img")))
         (failed (pig:registry-generate-images registry (mp-copy model "id" "gone") prompt)))
    (pig:to-json
     (pig:obj "stop" (pig:jget result "stopReason") "model" (pig:jget result "model") "output" (pig:jget result "output")
              "failedStop" (pig:jget failed "stopReason") "failedMessage" (pig:jget failed "errorMessage")
              "failedModel" (pig:jget failed "model") "failedOutput" (pig:jget failed "output")))))

(defun mp-route (ctx request)
  (declare (ignore ctx request))
  (pig:obj "model" (pig:obj "provider" "p" "id" "m") "thinkingLevel" "off"))

(defun mp-virtual-probe (ctx params)
  (declare (ignore params))
  (let ((registry (pig:ctx-model-registry ctx)) (refused ""))
    (pig:registry-register-virtual-model registry (pig:make-virtual-model :provider "router" :id "late" :name "Late" :route #'mp-route :context-window 1000))
    (handler-case (pig:registry-register-virtual-model registry (pig:make-virtual-model :provider "router" :id "claimed" :name "Claimed" :route #'mp-route))
      (error (e) (setf refused (string-trim '(#\Space #\Newline) (princ-to-string e)))))
    (pig:registry-unregister-virtual-model registry "router" "late")
    (pig:to-json (pig:obj "first" t "refused" refused))))

(defun mp-virtual-visible (ctx params)
  (declare (ignore params))
  (let ((registry (pig:ctx-model-registry ctx)))
    (flet ((listed () (if (find-if (lambda (model) (and (equal (pig:jget model "provider") "router") (equal (pig:jget model "id") "seen")))
                                   (pig:registry-get-all registry))
                          t :false)))
      (pig:registry-register-virtual-model registry (pig:make-virtual-model :provider "router" :id "seen" :name "Seen" :route #'mp-route :context-window 1000))
      (let ((registered (if (pig:registry-find registry "router" "seen") t :false)) (is-listed (listed)))
        (pig:registry-unregister-virtual-model registry "router" "seen")
        (pig:to-json (pig:obj "registered" registered "listed" is-listed
                              "gone" (if (null (pig:registry-find registry "router" "seen")) t :false)
                              "unlisted" (if (eq (listed) :false) t :false)))))))

(def-fixture "model-calls" (ext)
  (pig:command ext "model-stream-probe" "Exercise model streaming" (lambda (ctx args) (declare (ignore args)) (mp-model-stream-probe ctx)))
  (pig:command ext "model-stream-callback-probe" "Exercise the provider request callbacks of model_registry.stream"
               (lambda (ctx args) (declare (ignore args)) (mp-model-stream-callback-probe ctx)))
  (pig:command ext "model-stream-fetch-probe" "Exercise the fetch option of model_registry.stream"
               (lambda (ctx args) (declare (ignore args)) (mp-model-stream-fetch-probe ctx))))

;;; the provider operations of the model-types rows: image and classifier implementations, a provider config, a Provider object and a late registration

(defvar *mp-cancelled* 0 "How many hung classifications ended because their request was cancelled.")

(defun mp-zero-rates () (pig:obj "input" 0 "output" 0 "cacheRead" 0 "cacheWrite" 0))

(defun mp-operation-key (options)
  (pig:jget (pig:provider-operation-options-values options) "apiKey"))

(defun mp-image-impl (model request options)
  (let ((prompt (pig:jget (aref (pig:jget request "input") 0) "text")))
    (when (equal prompt "fail") (error "image failed"))
    (pig:obj "api" (pig:jget model "api") "provider" (pig:jget model "provider") "model" (pig:jget model "id")
             "responseId" (mp-operation-key options)
             "output" (pig:arr (pig:obj "type" "image" "data" (format nil "img:~A:~A" (pig:jget model "id") prompt) "mimeType" "image/png"))
             "stopReason" "stop" "timestamp" 1)))

(defun mp-classify-impl (model request options)
  (let ((state (pig:jget (pig:jget request "state") "text")))
    (cond ((equal state "fail") (error "classifier failed"))
          ((equal state "hang")
           (pig:abort-signal-wait (pig:provider-operation-options-signal options))
           (incf *mp-cancelled*)
           (error "classifier cancelled")))
    (let ((answers (pig:obj)) (images (pig:jget request "images")))
      (dolist (key (reverse (loop for key being the hash-keys of (pig:jget request "questions") collect key)))
        (setf (gethash key answers)
              (pig:obj "type" "bool" "probability" (/ (+ (length state) (* 10 (if (vectorp images) (length images) 0))) 100d0))))
      (pig:obj "api" (pig:jget model "api") "provider" (pig:jget model "provider") "model" (pig:jget model "id")
               "answers" answers "stopReason" "stop" "timestamp" 2))))

(defun mp-late-stream (ctx model request options)
  (declare (ignore ctx request))
  (let ((stream (pig:make-model-event-stream)))
    (pig:model-stream-push
     stream
     (pig:obj "type" "done" "reason" "stop"
              "message" (pig:obj "role" "assistant" "api" (pig:jget model "api") "provider" (pig:jget model "provider") "model" (pig:jget model "id")
                                 "content" (pig:arr (pig:obj "type" "text" "text" (format nil "late:~A:~A" (pig:jget model "id") (pig:jget options "apiKey"))))
                                 "stopReason" "stop" "timestamp" 1)))
    stream))

(defun mp-late-provider (ctx params)
  (let ((registry (pig:ctx-model-registry ctx)) (mode (pig:jget params "mode")))
    (pig:registry-register-provider
     registry "late"
     (pig:obj "api" "late-chat-api" "baseUrl" "https://late.test/v1" "apiKey" "late-key"
              "models" (pig:arr (pig:obj "id" "chat" "name" "Chat" "reasoning" :false "input" (pig:arr "text") "cost" (mp-zero-rates)
                                         "contextWindow" 1000 "maxTokens" 100)
                                (pig:obj "id" "flux" "name" "Flux" "type" "image" "api" "late-images" "input" (pig:arr "text")
                                         "output" (pig:arr "image") "cost" (mp-zero-rates))
                                (pig:obj "id" "cls" "name" "Cls" "type" "classifier" "api" "late-classifier" "input" (pig:arr "text")
                                         "contextWindow" 1000 "cost" (mp-zero-rates)))
              "images" (pig:obj "late-images" #'mp-image-impl) "classifiers" (pig:obj "late-classifier" #'mp-classify-impl)
              "streamSimple" #'mp-late-stream))
    (when (equal mode "again") (pig:registry-register-provider registry "late" (pig:obj "baseUrl" "https://late.test/v2")))
    (when (equal mode "gone") (pig:registry-unregister-provider registry "late"))
    (pig:to-json (pig:obj "mode" mode))))

(defun mp-ops-status (ctx params)
  (declare (ignore ctx params))
  (pig:to-json (pig:obj "cancelled" *mp-cancelled*)))

(defun mp-register-providers (ext)
  (pig:register-provider
   ext "ops"
   (pig:obj "baseUrl" "https://ops.test/v1" "apiKey" "ops-key"
            "models" (pig:arr (pig:obj "id" "flux" "name" "Flux" "type" "image" "api" "test-images" "input" (pig:arr "text")
                                       "output" (pig:arr "image" "text") "cost" (mp-zero-rates))
                              (pig:obj "id" "cls" "name" "Cls" "type" "classifier" "api" "test-classifier" "input" (pig:arr "text")
                                       "contextWindow" 1000 "cost" (mp-zero-rates)))
            "images" (pig:obj "test-images" #'mp-image-impl)
            "classifiers" (pig:obj "test-classifier" #'mp-classify-impl)))
  (pig:register-provider
   ext
   (pig:make-provider
    :id "pixels" :name "Pixels"
    :auth (pig:make-provider-auth
           :api-key (pig:make-api-key-auth :name "Pixels key"
                                           :resolve (lambda (input) (declare (ignore input)) (pig:make-auth-result :auth (pig:obj "apiKey" "pixels-key")))))
    :get-models (lambda ()
                  (pig:arr (pig:obj "id" "flux" "name" "Flux" "type" "image" "api" "test-images" "input" (pig:arr "text") "output" (pig:arr "image")
                                    "cost" (mp-zero-rates) "baseUrl" "https://pixels.test/v1")
                           (pig:obj "id" "cls" "name" "Cls" "type" "classifier" "api" "test-classifier" "input" (pig:arr "text")
                                    "contextWindow" 1000 "cost" (mp-zero-rates) "baseUrl" "https://pixels.test/v1")))
    :stream (lambda (&rest arguments) (declare (ignore arguments)) nil)
    :stream-simple (lambda (&rest arguments) (declare (ignore arguments)) nil)
    :generate-images #'mp-image-impl :classify #'mp-classify-impl)))

(def-fixture "model-call-extras" (ext)
  (pig:command ext "complete-probe" "Exercise Context.complete" (lambda (ctx args) (declare (ignore args)) (mp-complete-probe ctx)))
  (pig:command ext "model-stream-cancel-probe" "Start a model stream that the host never ends"
               (lambda (ctx args) (declare (ignore args)) (mp-cancel-probe ctx)))
  (pig:command ext "model-stream-cancel-report" "Report how the cancelled model stream ended"
               (lambda (ctx args) (declare (ignore args)) (mp-cancel-report ctx))))

(def-surface-fixture "model-types" (ext)
  (mp-register-providers ext)
  (loop for (name handler) in (list (list "typed_reads" #'mp-typed-reads) (list "classify_probe" #'mp-classify-probe)
                                    (list "images_probe" #'mp-images-probe) (list "virtual_probe" #'mp-virtual-probe)
                                    (list "virtual_visible" #'mp-virtual-visible)
                                    (list "late_provider" #'mp-late-provider) (list "ops_status" #'mp-ops-status))
        do (let ((handler handler))
             (pig:tool ext name name (pig:obj "type" "object") (lambda (ctx params) (funcall handler ctx params))))))
