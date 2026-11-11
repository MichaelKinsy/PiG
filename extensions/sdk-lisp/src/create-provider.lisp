;;;; SPDX-License-Identifier: MIT
;;;; pi-ai's createProvider (packages/ai/src/models.ts:1077): builds a provider from parts. A single API streams every chat model, a table of APIs
;;;; dispatches on the model's api, and a model whose api has no entry produces a stream error; the one-shot operation tables dispatch on it the same way.
;;;; Pure and synchronous: it calls no host function.

(in-package #:pig)

(defstruct (provider-streams (:constructor make-provider-streams (&key stream stream-simple fetch-deferred cancel-deferred)))
  "pi-ai's ProviderStreams, the chat implementation of one API: STREAM and STREAM-SIMPLE are (lambda (model context options)) returning a model-stream;
FETCH-DEFERRED and CANCEL-DEFERRED, (lambda (model handle options)), are optional."
  stream stream-simple fetch-deferred cancel-deferred)

(defun cp-model-type (model)
  (let ((type (jget model "type"))) (if (stringp type) type "chat")))

(defun cp-js-text (value) (if (or (null value) (eq value :null)) "undefined" (princ-to-string value)))

(defun cp-now-ms ()
  (multiple-value-bind (seconds microseconds) (sb-ext:get-time-of-day) (+ (* seconds 1000) (floor microseconds 1000))))

(defun cp-zero-usage ()
  (obj "input" 0 "output" 0 "cacheRead" 0 "cacheWrite" 0 "totalTokens" 0
       "cost" (obj "input" 0 "output" 0 "cacheRead" 0 "cacheWrite" 0 "total" 0)))

(defun cp-setup-error-stream (model error)
  "pi-ai's lazyStream of a setup that threw: the stream ends with an error event carrying the message of an empty assistant message."
  (let ((stream (create-assistant-message-event-stream)))
    (model-stream-push stream (obj "type" "error" "reason" "error"
                                   "error" (obj "role" "assistant" "content" (vector) "api" (jget model "api") "provider" (jget model "provider")
                                                "model" (jget model "id") "usage" (cp-zero-usage) "stopReason" "error"
                                                "errorMessage" (princ-to-string error) "timestamp" (cp-now-ms))))
    stream))

(defun cp-operation-error (model field empty message)
  (obj "api" (jget model "api") "provider" (jget model "provider") "model" (jget model "id") field empty
       "stopReason" "error" "errorMessage" message "timestamp" (cp-now-ms)))

(defun cp-table-get (table key)
  (and table (stringp key) (nth-value 0 (gethash key table))))

(defun create-provider (&key id auth models name base-url headers fetch-models filter-models filter-all-models api images classifiers)
  "createProvider(options). AUTH is required: every provider has auth semantics. MODELS is the static baseline of every type (a model without a type is a chat
model). API is a provider-streams or a hash-table of them by api; IMAGES and CLASSIFIERS are hash-tables from api to (lambda (model request options)). FETCH-MODELS,
(lambda (refresh-models-context)), fetches a dynamic model overlay of every type, restored and published transactionally; models of unknown types are dropped."
  (let* ((single (and (provider-streams-p api) api))
         (by-api (and api (not single) api))
         (streams (cond (single (list single))
                        (by-api (loop for v being the hash-values of by-api when v collect v))))
         (lock (sb-thread:make-mutex :name "pig provider models"))
         (dynamic '()))
    (unless (or streams (and images (plusp (hash-table-count images))) (and classifiers (plusp (hash-table-count classifiers))))
      (error "Provider ~A: at least one of \"api\", \"images\", or \"classifiers\" is required." id))
    (labels ((current-models ()
               (sb-thread:with-mutex (lock)
                 (let ((merged (copy-list models)))
                   (dolist (model dynamic merged)
                     (let ((position (position-if (lambda (entry) (and (equal (cp-model-type entry) (cp-model-type model)) (equal (jget entry "id") (jget model "id")))) merged)))
                       (if position (setf (nth position merged) model) (setf merged (append merged (list model)))))))))
             (api-for (model) (or single (cp-table-get by-api (jget model "api"))))
             (set-dynamic (value) (lambda () (sb-thread:with-mutex (lock) (setf dynamic value))))
             (signalled-p (context) (and (refresh-models-context-signal context) (abort-signal-set-p (refresh-models-context-signal context))))
             (refresh-models (context)
               (let ((stored (refresh-models-context-stored context)))
                 (when stored
                   (let ((restored (loop for model across (let ((m (jget stored "models"))) (if (vectorp m) m (vector)))
                                         when (equal (jget model "provider") id) collect model)))
                     (unless (funcall (refresh-models-context-publish context) (make-models-publication :update (set-dynamic restored)))
                       (return-from refresh-models nil)))))
               (when (or (not (refresh-models-context-allow-network context)) (signalled-p context)) (return-from refresh-models nil))
               (let ((fetched (funcall fetch-models context)))
                 (when (signalled-p context) (return-from refresh-models nil))
                 (let ((refreshed (loop for model across (coerce fetched 'simple-vector)
                                        when (member (cp-model-type model) '("chat" "image" "classifier") :test #'equal) collect model)))
                   (funcall (refresh-models-context-publish context)
                            (make-models-publication :persist (obj "models" (coerce refreshed 'simple-vector) "checkedAt" (cp-now-ms))
                                                     :update (set-dynamic refreshed))))))
             (dispatch (simple)
               (lambda (model context options)
                 (let ((implementation (api-for model)))
                   (if implementation
                       (funcall (if simple (provider-streams-stream-simple implementation) (provider-streams-stream implementation)) model context options)
                       (cp-setup-error-stream model (format nil "Provider ~A has no API implementation for ~S" id (cp-js-text (jget model "api"))))))))
             (deferred-p (accessor) (some accessor streams)))
      (make-provider
       :id id :name (or name id) :auth auth
       :get-models (lambda () (remove-if-not (lambda (model) (equal (cp-model-type model) "chat")) (current-models)))
       :stream (dispatch nil) :stream-simple (dispatch t) :base-url base-url :headers headers
       :filter-models filter-models :filter-all-models filter-all-models :get-all-models #'current-models
       :refresh-models (and fetch-models #'refresh-models)
       :fetch-deferred
       (and (deferred-p #'provider-streams-fetch-deferred)
            (lambda (model handle options)
              (let ((implementation (api-for model)))
                (if (and implementation (provider-streams-fetch-deferred implementation))
                    (handler-case (funcall (provider-streams-fetch-deferred implementation) model handle options)
                      (error (e) (cp-setup-error-stream model e)))
                    (cp-setup-error-stream model (format nil "Provider ~A does not support deferred responses for ~S" id (cp-js-text (jget model "api"))))))))
       :cancel-deferred
       (and (deferred-p #'provider-streams-cancel-deferred)
            (lambda (model handle options)
              (let ((implementation (api-for model)))
                (unless (and implementation (provider-streams-cancel-deferred implementation))
                  (error "Provider ~A cannot cancel deferred responses for ~S" id (cp-js-text (jget model "api"))))
                (funcall (provider-streams-cancel-deferred implementation) model handle options)
                nil)))
       :generate-images
       (and images (plusp (hash-table-count images))
            (lambda (model request options)
              (let ((implementation (cp-table-get images (jget model "api"))))
                (if implementation
                    (funcall implementation model request options)
                    (cp-operation-error model "output" (vector) (format nil "Provider ~A has no image generation implementation for ~S" id (cp-js-text (jget model "api"))))))))
       :classify
       (and classifiers (plusp (hash-table-count classifiers))
            (lambda (model request options)
              (let ((implementation (cp-table-get classifiers (jget model "api"))))
                (if implementation
                    (funcall implementation model request options)
                    (cp-operation-error model "answers" (obj) (format nil "Provider ~A has no classifier implementation for ~S" id (cp-js-text (jget model "api"))))))))))))
