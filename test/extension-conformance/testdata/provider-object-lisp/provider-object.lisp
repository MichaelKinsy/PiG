;;;; The Lisp Provider-object fixture (provider_object_test.go): the Python fixture's carrier provider, and a probe that reads it back as a foreign Provider.
(defpackage "provider-object" (:use "COMMON-LISP"))
(in-package "provider-object")

(defun zero-usage ()
  (pig:obj "input" 0 "output" 0 "cacheRead" 0 "cacheWrite" 0 "totalTokens" 0
           "cost" (pig:obj "input" 0 "output" 0 "cacheRead" 0 "cacheWrite" 0 "total" 0)))

(defun carrier-model (&optional (id "carrier-model"))
  (pig:obj "id" id "name" "Carrier" "provider" "carrier-provider" "api" "openai-completions" "baseUrl" "http://127.0.0.1:9"
           "reasoning" :false "input" (vector "text") "cost" (pig:obj "input" 0 "output" 0 "cacheRead" 0 "cacheWrite" 0)
           "contextWindow" 4000 "maxTokens" 100))

(defun carrier-stream (model options)
  (let* ((meta (or (pig:jget (pig:provider-stream-options-values options) "metadata") (pig:obj)))
         (events (pig:make-model-event-stream))
         (message (pig:obj "role" "assistant" "api" (pig:jget model "api") "provider" (pig:jget model "provider") "model" (pig:jget model "id")
                           "content" (vector) "usage" (zero-usage) "stopReason" "stop" "timestamp" 1)))
    (when (eq (pig:jget meta "fail") t) (error "carrier stream failed"))
    (if (eq (pig:jget meta "wait") t)
        (pig:abort-signal-subscribe (pig:provider-stream-options-signal options)
                                    (lambda ()
                                      (setf (gethash "stopReason" message) "aborted" (gethash "errorMessage" message) "carrier cancelled")
                                      (pig:model-stream-push events (pig:obj "type" "error" "reason" "aborted" "error" message))))
        (let ((text (or (pig:jget meta "method") "carrier answer")))
          ;; The caller's request callbacks reach the Provider by name.
          (let ((on-response (pig:provider-stream-options-on-response options)))
            (when on-response (funcall on-response (pig:obj "status" 207 "headers" (pig:obj "x-carrier" "response")) model)))
          (let ((on-event (pig:provider-stream-options-on-provider-stream-event options)))
            (when on-event (funcall on-event (pig:obj "carrier" "event") model)))
          (let ((on-payload (pig:provider-stream-options-on-payload options)))
            (when on-payload
              (let ((replaced (funcall on-payload (pig:obj "carrier" "payload") model)))
                (when (hash-table-p replaced) (setf text (pig:jget replaced "text"))))))
          (setf (gethash "content" message) (vector (pig:obj "type" "text" "text" text)))
          (pig:model-stream-push events (pig:obj "type" "done" "reason" "stop" "message" message))))
    events))

(defun options-with (options values)
  (let ((copy (copy-structure options))) (setf (pig:provider-stream-options-values copy) values) copy))

(defun make-carrier-provider ()
  (let* ((lock (sb-thread:make-mutex)) (model (carrier-model)) (models (list model)))
    (flet ((get-models () (sb-thread:with-mutex (lock) (copy-list models)))
           (credential-key (credential) (and credential (pig:jget credential "key"))))
      (pig:make-provider
       :id "carrier-provider" :name "Carrier Provider" :base-url (pig:jget model "baseUrl") :headers (pig:obj "X-Carrier" "present")
       :auth (pig:make-provider-auth
              :api-key (pig:make-api-key-auth
                        :name "Carrier API key"
                        :resolve (lambda (input)
                                   (let ((key (or (credential-key (pig:api-key-auth-input-credential input))
                                                  (funcall (pig:auth-context-env (pig:api-key-auth-input-ctx input)) "CARRIER_KEY"))))
                                     (pig:make-auth-result :auth (if key (pig:obj "apiKey" key) (pig:obj)) :source "caller context")))
                        :check (lambda (input)
                                 (pig:make-auth-check :type "api_key"
                                                      :source (funcall (pig:auth-context-env (pig:api-key-auth-input-ctx input)) "CARRIER_SOURCE")))
                        :login (lambda (interaction)
                                 (pig:obj "type" "api_key"
                                          "key" (funcall (pig:auth-interaction-prompt interaction) (pig:obj "type" "secret" "message" "Carrier key")))))
              :oauth (pig:make-oauth-auth
                      :name "Carrier OAuth" :is-subscription t :login-label "Carrier login"
                      :login (lambda (interaction options)
                               (pig:obj "type" "oauth"
                                        "access" (concatenate 'string
                                                              (funcall (pig:auth-interaction-prompt interaction) (pig:obj "type" "text" "message" "Carrier OAuth"))
                                                              "@" (if (pig:login-options-get-device-id options)
                                                                      (funcall (pig:login-options-get-device-id options))
                                                                      "none"))
                                        "refresh" "refresh" "expires" 100))
                      :refresh (lambda (credential signal)
                                 (declare (ignore signal))
                                 (let ((copy (make-hash-table :test 'equal)))
                                   (maphash (lambda (k v) (setf (gethash k copy) v)) credential)
                                   (setf (gethash "access" copy) "rotated" (gethash "expires" copy) 200)
                                   copy))
                      :to-auth (lambda (credential) (pig:obj "apiKey" (pig:jget credential "access") "baseUrl" "https://carrier.invalid"))))
       :get-models #'get-models
       :stream (lambda (model context options) (declare (ignore context)) (carrier-stream model options))
       :stream-simple (lambda (model context options)
                        (declare (ignore context))
                        (carrier-stream model (options-with options (pig:obj "metadata" (pig:obj "method" "simple")))))
       :filter-models (lambda (models credential) (if (equal (credential-key credential) "selected") (list (aref models 0)) '()))
       :get-all-models (lambda () (append (get-models) (list (carrier-model "carrier-all-only"))))
       :filter-all-models (lambda (models credential) (if (equal (credential-key credential) "all") models '()))
       :refresh-models (lambda (input)
                         (when (pig:refresh-models-context-force input)
                           (funcall (pig:refresh-models-context-publish input)
                                    (pig:make-models-publication
                                     :persist nil
                                     ;; An SDK that drops RefreshModelsContext.baseUrl publishes "refreshed:NIL".
                                     :update (lambda ()
                                               (sb-thread:with-mutex (lock)
                                                 (setf models (append models (list (carrier-model
                                                                                    (format nil "refreshed:~A" (pig:refresh-models-context-base-url input))))))))))))
       :fetch-deferred (lambda (model handle options)
                         (carrier-stream model (pig:make-provider-stream-options
                                                :values (pig:obj "metadata" (pig:obj "method" (pig:jget handle "id")))
                                                :signal (pig:provider-stream-options-signal options))))
       :cancel-deferred (lambda (model handle options)
                          (declare (ignore model options))
                          (unless (equal (pig:jget handle "id") "deferred") (error "wrong deferred handle")))))))

(defun write-file (path text)
  (with-open-file (out path :direction :output :if-exists :supersede :external-format :utf-8) (write-string text out)))

(defun probe (ctx path)
  (let* ((registry (pig:ctx-model-registry ctx))
         (provider (pig:registry-get-registered-native-provider registry "carrier-provider")))
    (assert provider)
    (assert (eq provider (pig:registry-get-provider registry (pig:provider-id provider))))
    (let* ((models (funcall (pig:provider-get-models provider))) (model (first (coerce models 'list))))
      (assert (eq (first (coerce (funcall (pig:provider-filter-models provider) models (pig:obj "type" "api_key" "key" "selected")) 'list)) model))
      (assert (zerop (length (funcall (pig:provider-filter-models provider) models :null))))
      (let* ((signal (pig:make-abort-signal))
             (input (pig:make-api-key-auth-input
                     :ctx (pig:make-auth-context :env (lambda (name) (concatenate 'string "injected:" name)) :file-exists (lambda (path) (declare (ignore path)) nil))
                     :signal signal))
             (api-key (pig:provider-auth-api-key (pig:provider-auth provider)))
             (oauth (pig:provider-auth-oauth (pig:provider-auth provider)))
             (check (funcall (pig:api-key-auth-check api-key) input))
             (auth (funcall (pig:api-key-auth-resolve api-key) input))
             (interaction (pig:make-auth-interaction :prompt (lambda (prompt) (pig:jget prompt "message")) :notify (lambda (event) (declare (ignore event)) nil) :signal signal))
             (api-login (funcall (pig:api-key-auth-login api-key) interaction))
             (login (funcall (pig:oauth-auth-login oauth) interaction (pig:make-login-options :get-device-id (lambda () "caller-device"))))
             (rotated (funcall (pig:oauth-auth-refresh oauth) login signal))
             (to-auth (funcall (pig:oauth-auth-to-auth oauth) rotated))
             (order '()))
        (funcall (pig:provider-refresh-models provider)
                 (pig:make-refresh-models-context
                  :publish (lambda (publication)
                             (assert (eq (pig:models-publication-persist publication) :null))
                             (push "persist" order)
                             (funcall (pig:models-publication-update publication))
                             (push "update" order)
                             t)
                  :force t :signal signal :base-url "https://regional.invalid/v1"))
        (flet ((content (stream) (pig:jget (pig:model-stream-result stream) "content")))
          (let* ((result (content (funcall (pig:provider-stream provider) model (pig:obj "messages" (vector)) (pig:make-provider-stream-options))))
                 (simple (content (funcall (pig:provider-stream-simple provider) model (pig:obj "messages" (vector)) (pig:make-provider-stream-options))))
                 (deferred (content (funcall (pig:provider-fetch-deferred provider) model (pig:obj "id" "deferred") (pig:make-provider-stream-options))))
                 (cancellation (pig:make-abort-signal)))
            (funcall (pig:provider-cancel-deferred provider) model (pig:obj "id" "deferred") (pig:make-provider-stream-options))
            (let ((waiting (funcall (pig:provider-stream provider) model (pig:obj "messages" (vector))
                                    (pig:make-provider-stream-options :values (pig:obj "metadata" (pig:obj "wait" t)) :signal cancellation))))
              (pig:signal-abort cancellation)
              (let ((cancelled (pig:model-stream-result waiting)))
                (flet ((clean (&rest members) (let ((table (pig:obj))) (loop for (k v) on members by #'cddr do (when v (setf (gethash k table) v))) table)))
                  (write-file path (pig:to-json
                                    (pig:obj "headers" (pig:provider-headers provider)
                                             "check" (clean "type" (pig:auth-check-type check) "source" (pig:auth-check-source check))
                                             "auth" (clean "auth" (pig:auth-result-auth auth) "env" (pig:auth-result-env auth) "source" (pig:auth-result-source auth))
                                             "apiLogin" api-login "login" login "rotated" rotated "oauth" to-auth
                                             "order" (coerce (reverse order) 'simple-vector)
                                             "models" (map 'simple-vector (lambda (m) (pig:jget m "id")) (funcall (pig:provider-get-models provider)))
                                             "result" result "simple" simple "deferred" deferred
                                             "cancelled" (pig:jget cancelled "stopReason")))))))))))))

(defun new-extension ()
  (let ((ext (pig:make-extension "provider-object-lisp")))
    (unless (equal (sb-ext:posix-getenv "CARRIER_ROLE") "reader")
      (pig:register-native-provider ext (make-carrier-provider)))
    (pig:command ext "remote-carrier-probe" "Exercise a foreign Provider" #'probe)
    ext))
