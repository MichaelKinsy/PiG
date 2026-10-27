(in-package "lisp-sdk-fixture")
;;; the probe commands, tools and event handlers of lane P (see def-fixture in fixture.lisp)

(defun pv-bool (value) (if (eq value t) "true" "false"))

(defun pv-append (path text)
  (with-open-file (out path :direction :output :if-exists :append :if-does-not-exist :create :external-format :utf-8)
    (write-string text out)))

(defun pv-transform-markdown (markdown context)
  ;; "trace:<dir>:<name>" records the body's entry in <dir>/trace; the body named "first" returns only once <dir>/release exists.
  (when (and (>= (length markdown) 6) (string= markdown "trace:" :end1 6))
    (let* ((rest (subseq markdown 6)) (colon (position #\: rest :from-end t))
           (directory (subseq rest 0 colon)) (name (subseq rest (1+ colon))))
      (pv-append (concatenate 'string directory "/trace") (format nil "entered ~A~%" name))
      (when (string= name "first")
        (loop until (probe-file (concatenate 'string directory "/release")) do (sleep 0.01))
        (pv-append (concatenate 'string directory "/trace") (format nil "returned first~%")))))
  (format nil "md:~A:~A:streaming=~A:width=~D" markdown (let ((type (pig:jget context "messageType"))) (if (stringp type) type ""))
          (pv-bool (pig:jget context "isStreaming")) (let ((width (pig:jget context "availableWidth"))) (if (integerp width) width 0))))

(defun pv-resolved-line (prefix tool)
  (lambda (ctx args render width)
    (declare (ignore ctx render width))
    (list (format nil "~A:~A:~A" prefix tool (pig:jget args "q")))))

(defun pv-resolve-tool (tool next)
  (cond ((equal tool "conformance_tool_renderer")
         (pig:make-tool-renderers :render-call (pv-resolved-line "resolved" tool)))
        ((equal tool "conformance_no_renderer") nil)
        ((equal tool "conformance_fill")
         (or (funcall next) (pig:make-tool-renderers :render-call (pv-resolved-line "filled" tool))))
        ((equal tool "conformance_wrap")
         (let ((renderers (copy-structure (funcall next))))
           (setf (pig:tool-renderers-render-call renderers) (pv-resolved-line "wrapped" tool))
           renderers))
        (t (funcall next))))

(def-fixture "provider-renderers" (ext)
  (pig:message-renderer
   ext "conformance-message"
   (lambda (ctx message options width)
     (declare (ignore ctx))
     (if (equal (pig:jget message "content") "padding-options")
         (list (pig:to-json options))
         (list (format nil "renderer:~A:expanded=~A:width=~D" (let ((c (pig:jget message "content"))) (if (stringp c) c "")) (pv-bool (pig:jget options "expanded")) width)))))
  (pig:entry-renderer
   ext "conformance-entry"
   (lambda (ctx entry options width)
     (declare (ignore ctx))
     (list (format nil "entryrenderer:~A:expanded=~A:width=~D" (let ((d (pig:jget entry "data"))) (if (stringp d) d "")) (pv-bool (pig:jget options "expanded")) width))))
  (pig:markdown-transformer ext #'pv-transform-markdown)
  (pig:tool-renderer ext #'pv-resolve-tool)
  (pig:command ext "late_tool_renderer" "Register a tool renderer resolver after loading"
               (lambda (ctx args)
                 (declare (ignore ctx args))
                 (pig:tool-renderer ext (lambda (tool next)
                                          (if (equal tool "conformance_late")
                                              (pig:make-tool-renderers :render-call (pv-resolved-line "late" tool))
                                              (funcall next)))))))

(def-fixture "provider-response" (ext)
  (pig:on ext "after_provider_response"
          (lambda (ctx data)
            (pig:ctx-notify ctx (format nil "provider-response=~A:~A:~A" (pig:jget data "type") (pig:jget data "status") (pig:jref data "headers" "x-probe")) "info")
            (pig:obj "cancel" t)))
  (pig:on ext "after_provider_response"
          (lambda (ctx data) (declare (ignore data)) (pig:ctx-notify ctx "provider-response=second" "info") nil)))

(def-fixture "provider-late-registrations" (ext)
  (pig:command ext "provider_probe_register" "Register a provider with one model"
               (lambda (ctx args)
                 (declare (ignore ctx args))
                 (pig:register-provider
                  ext "conformance-probe"
                  (pig:from-json "{\"baseUrl\":\"https://probe.invalid/v1\",\"api\":\"openai-completions\",\"apiKey\":\"probe-key\",\"models\":[{\"id\":\"probe-model\",\"name\":\"Probe Model\",\"reasoning\":false,\"input\":[\"text\"],\"cost\":{\"input\":0,\"output\":0,\"cacheRead\":0,\"cacheWrite\":0},\"contextWindow\":1000,\"maxTokens\":100}]}"))))
  (pig:command ext "provider_probe_unregister" "Unregister the provider"
               (lambda (ctx args) (declare (ignore ctx args)) (pig:unregister-provider ext "conformance-probe"))))

;;; the OAuth providers the cross-transport conformance suite drives (oauth_conformance_test.go)

(defun pv-oauth-store ()
  (pig:make-oauth-credential-store
   :credential-status (lambda () (pig:make-oauth-credential-status :present t :auth-type "oauth" :source "conformance"))
   :store-credentials (lambda (credentials)
                        (unless (and (equal (pig:jget credentials "accountId") "account-store") (equal (pig:jget credentials "scope") "scope-store"))
                          (error "credential metadata lost"))
                        "/conf/creds.json")
   :delete-credentials (lambda () t)))

(defun pv-copy-credentials (credentials &rest members)
  (let ((copy (make-hash-table :test 'equal)))
    (maphash (lambda (k v) (setf (gethash k copy) v)) credentials)
    (loop for (key value) on members by #'cddr do (setf (gethash key copy) value))
    copy))

(def-fixture "provider-oauth" (ext)
  (pig:register-oauth-provider
   ext "conformance-oauth-object" (pig:obj "name" "Conformance OAuth Object")
   (pig:make-oauth-provider
    :name "Conformance OAuth Object"
    :login (lambda (callbacks)
             (declare (ignore callbacks))
             (pig:oauth-credentials :access "object-access" :refresh "object-refresh" :expires 1700000000000.25d0
                                    :extra (pig:obj "meta" (pig:obj "k" (vector 1 :null "")) "projectId" "")))
    :refresh-token (lambda (credentials)
                     (pv-copy-credentials credentials "access" (concatenate 'string "refreshed-" (pig:jget credentials "refresh"))
                                                      "expires" (+ (pig:jget credentials "expires") 0.5d0)))
    :get-api-key (lambda (credentials)
                   (format nil "key:~A:~A" (if (equal (pig:jget credentials "type") "oauth") "typed" "untyped")
                           (if (nth-value 1 (gethash "meta" credentials)) "meta" "nometa")))))
  (pig:register-oauth-provider
   ext "conformance-oauth-large" (pig:obj "name" "Conformance OAuth Large")
   (pig:make-oauth-provider
    :name "Conformance OAuth Large"
    :login (lambda (callbacks) (declare (ignore callbacks)) (pig:oauth-credentials :access "large-access" :refresh "large-refresh" :expires (expt 2 60)))
    :get-api-key (lambda (credentials)
                   (let ((expires (pig:jget credentials "expires")))
                     (format nil "key:~D:~D" (round (- expires (expt 2 60))) (- (round expires) (expt 2 60)))))))
  (pig:register-oauth-provider
   ext "conformance-oauth" (pig:obj "name" "Conformance OAuth")
   (pig:make-oauth-provider
    :name "Conformance OAuth" :is-subscription t
    :login (lambda (callbacks)
             (pig:oauth-on-device-code callbacks (pig:make-oauth-device-code-info :user-code "CONF-USER-CODE" :verification-uri "https://conf.example/verify"))
             (pig:oauth-on-progress callbacks "waiting")
             (let ((value (pig:oauth-on-prompt callbacks (pig:make-oauth-prompt :message "paste the code"))))
               (pig:oauth-credentials :access (concatenate 'string "access-" value) :refresh "refresh-tok" :expires 4242
                                      :account-id "account-login" :scope "scope-login")))
    :refresh-token (lambda (credentials)
                     (pig:oauth-credentials :access (concatenate 'string "refreshed-" (pig:jget credentials "refresh")) :refresh (pig:jget credentials "refresh")
                                            :expires 9999 :account-id (pig:jget credentials "accountId") :scope (pig:jget credentials "scope")))
    :get-api-key (lambda (credentials)
                   (when (equal (pig:jget credentials "access") "boom") (error "getApiKey exploded"))
                   (concatenate 'string "key:" (pig:jget credentials "access")))
    :credential-store (pv-oauth-store))))

;;; the login and sprite definitions every SDK submits (conformance_test.go: login-probe, sprite-probe)

(defun pv-grid (rows width) (loop repeat rows collect (make-string width :initial-element #\A)))

(defun pv-login-definition ()
  (pig:make-login-definition :brand (pv-grid 5 41) :hero (pv-grid 14 32) :mascot (pv-grid 14 16) :palette (pig:obj "A" "#123ABC")
                             :name "Conformance Pig" :description "Cross-language login fixture" :tagline "One canonical definition across every SDK"))

(defun pv-sprite-definition ()
  (pig:make-sprite-definition :id "conformance-pig" :name "Conformance Pig" :tagline "One canonical sprite across every SDK"
                              :mascot (pv-grid 14 16) :palette (pig:obj "A" "#123ABC")))

(def-fixture "provider-login-sprite" (ext)
  (pig:command ext "login-probe" "Exercise semantic login submission and host errors"
               (lambda (ctx args)
                 (declare (ignore args))
                 (block probe
                 (let ((definition (pv-login-definition)))
                   (pig:ctx-set-login ctx definition)
                   (setf (first (pig:login-definition-brand definition)) (subseq (first (pig:login-definition-brand definition)) 0 40))
                   (handler-case (pig:ctx-set-login ctx definition)
                     (pig:host-call-error (e) (pig:ctx-notify ctx (pig:host-call-error-message e) "error") (return-from probe nil)))
                   (error "invalid login definition was accepted")))))
  (pig:command ext "sprite-probe" "Exercise sprite registration and host errors"
               (lambda (ctx args)
                 (declare (ignore args))
                 (block probe
                 (let ((definition (pv-sprite-definition)))
                   (pig:ctx-register-sprite ctx definition)
                   (setf (first (pig:sprite-definition-mascot definition)) (subseq (first (pig:sprite-definition-mascot definition)) 0 15))
                   (handler-case (pig:ctx-register-sprite ctx definition)
                     (pig:host-call-error (e) (pig:ctx-notify ctx (pig:host-call-error-message e) "error") (return-from probe nil)))
                   (error "invalid sprite definition was accepted"))))))

;;; an OAuth login that asks the host for every kind of interaction (lisp_provider_api_test.go: TestLispOAuthLoginCallbacksReachTheHost)

(def-fixture "provider-oauth-callbacks" (ext)
  (pig:register-oauth-provider
   ext "conformance-oauth-callbacks" (pig:obj "name" "Conformance OAuth Callbacks")
   (pig:make-oauth-provider
    :name "Conformance OAuth Callbacks"
    :login (lambda (callbacks)
             (pig:oauth-on-auth callbacks (pig:make-oauth-auth-info :url "https://auth.example/start" :instructions "Open the link"))
             (pig:oauth-on-device-code callbacks (pig:make-oauth-device-code-info :user-code "ABCD" :verification-uri "https://auth.example/device"
                                                                                  :open-browser-on-confirm :false :copy-code-on-confirm t
                                                                                  :interval-seconds 5 :expires-in-seconds 600))
             (pig:oauth-on-progress callbacks "Waiting")
             (let* ((typed (pig:oauth-on-prompt callbacks (pig:make-oauth-prompt :message "Account:" :placeholder "acme" :allow-empty t)))
                    (picked (pig:oauth-on-select callbacks (pig:make-oauth-select-prompt
                                                            :message "Team:" :options (list (pig:make-oauth-select-option :id "red" :label "Red team")
                                                                                            (pig:make-oauth-select-option :id "blue" :label "Blue team")))))
                    (manual (pig:oauth-on-manual-code-input callbacks)))
               (pig:oauth-credentials :access (format nil "~A/~A/~A" typed picked manual) :refresh "r" :expires 1)))
    :refresh-token #'identity :get-api-key (lambda (credentials) (pig:jget credentials "access")))))

;;; the value types of providers and renderers, built and read back in the extension (lisp_provider_api_test.go: TestLispProviderValueTypes)

(defun pv-describe (&rest parts) (format nil "~{~A~^|~}" parts))

(def-fixture "provider-surface" (ext)
  (pig:command
   ext "provider_surface" "Build the provider value types and report what they hold"
   (lambda (ctx args)
     (declare (ignore args))
     (let* ((signal (pig:make-abort-signal))
            (auth-context (pig:make-auth-context :env (lambda (name) (concatenate 'string "env:" name)) :file-exists (lambda (path) (equal path "/there"))))
            (input (pig:make-api-key-auth-input :ctx auth-context :credential (pig:obj "key" "k") :signal signal))
            (result (pig:make-auth-result :auth (pig:obj "apiKey" "a") :env (pig:obj "E" "1") :source "from-result"))
            (check (pig:make-auth-check :type "api_key" :source "from-check"))
            (interaction (pig:make-auth-interaction :prompt (lambda (prompt) (pig:jget prompt "message")) :notify (lambda (event) (declare (ignore event)) nil) :signal signal))
            (options (pig:make-login-options :get-device-id (lambda () "device-7") :agent-name "agent-7"))
            (api-key (pig:make-api-key-auth :name "Key name" :resolve (lambda (input) (declare (ignore input)) result)
                                            :check (lambda (input) (declare (ignore input)) check)
                                            :login (lambda (interaction) (declare (ignore interaction)) (pig:obj))))
            (oauth (pig:make-oauth-auth :name "OAuth name" :login (lambda (interaction options) (declare (ignore interaction options)) (pig:obj))
                                        :refresh (lambda (credential signal) (declare (ignore signal)) credential) :to-auth (lambda (credential) credential)
                                        :is-subscription t :login-label "Sign in"))
            (auth (pig:make-provider-auth :api-key api-key :oauth oauth))
            (stream-options (pig:make-provider-stream-options :values (pig:obj "v" 1) :signal signal :on-payload #'identity :on-response #'identity
                                                              :on-provider-stream-event #'identity))
            (operation-options (pig:make-provider-operation-options :values (pig:obj "o" 2) :signal signal))
            (publication (pig:make-models-publication :persist :null :update #'identity))
            (refresh (pig:make-refresh-models-context :publish #'identity :credential (pig:obj "c" 1) :stored (pig:obj "s" 2) :allow-network t :force t
                                                      :signal signal :base-url "https://base"))
            (provider (pig:make-provider :id "surface" :name "Surface" :auth auth :get-models (lambda () nil) :stream #'identity :stream-simple #'identity
                                         :base-url "https://provider" :headers (pig:obj "X" "1") :filter-models #'identity :get-all-models #'identity
                                         :filter-all-models #'identity :refresh-models #'identity :fetch-deferred #'identity :cancel-deferred #'identity
                                         :generate-images #'identity :classify #'identity))
            (renderers (pig:make-tool-renderers :render-call #'identity :render-result #'identity :render-shell "self" :call-view #'identity :result-view #'identity))
            (render-context (pig:make-tool-render-context :args (pig:obj "a" 1) :tool-call-id "call-1" :cwd "/cwd" :execution-started t :args-complete t
                                                          :is-partial t :expanded t :show-images t :is-error t :output-pad 3 :duration-ms 12
                                                          :state (pig:obj "s" 1) :invalidate (lambda () "invalidated")))
            (device (pig:make-oauth-device-code-info :user-code "U" :verification-uri "V"))
            (status (pig:make-oauth-credential-status :present t :auth-type "oauth" :source "src"))
            (store (pig:make-oauth-credential-store :credential-status (lambda () status) :store-credentials #'identity :delete-credentials (lambda () t)))
            (oauth-provider (pig:make-oauth-provider :name "OP" :is-subscription t :credential-store store))
            (cancelled (handler-case (error 'pig:oauth-cancelled) (pig:oauth-cancelled (c) (princ-to-string c)))))
       (pig:ctx-notify
        ctx
        (pv-describe
         "surface"
         (typep input 'pig:api-key-auth-input) (typep auth-context 'pig:auth-context) (typep result 'pig:auth-result) (typep check 'pig:auth-check)
         (typep interaction 'pig:auth-interaction) (typep options 'pig:login-options) (typep api-key 'pig:api-key-auth) (typep oauth 'pig:oauth-auth)
         (typep stream-options 'pig:provider-stream-options) (typep operation-options 'pig:provider-operation-options)
         (typep publication 'pig:models-publication) (typep refresh 'pig:refresh-models-context) (typep provider 'pig:provider) (pig:provider-p provider)
         (typep renderers 'pig:tool-renderers) (typep render-context 'pig:tool-render-context) (typep device 'pig:oauth-device-code-info)
         (typep store 'pig:oauth-credential-store) (typep oauth-provider 'pig:oauth-provider) (typep status 'pig:oauth-credential-status)
         (funcall (pig:auth-context-env (pig:api-key-auth-input-ctx input)) "X") (funcall (pig:auth-context-file-exists (pig:api-key-auth-input-ctx input)) "/there")
         (eq signal (pig:api-key-auth-input-signal input)) (eq signal (pig:auth-interaction-signal interaction))
         (pig:api-key-auth-name api-key) (pig:oauth-auth-name oauth) (pig:oauth-auth-is-subscription oauth) (pig:oauth-auth-login-label oauth)
         (funcall (pig:login-options-get-device-id options)) (pig:login-options-agent-name options)
         (pig:provider-base-url provider) (pig:provider-handle provider) (null (pig:provider-get-all-models provider)) (null (pig:provider-filter-all-models provider))
         (null (pig:provider-generate-images provider)) (null (pig:provider-classify provider))
         (pig:jget (pig:provider-operation-options-values operation-options) "o") (eq signal (pig:provider-operation-options-signal operation-options))
         (pig:jget (pig:refresh-models-context-credential refresh) "c") (pig:jget (pig:refresh-models-context-stored refresh) "s")
         (pig:refresh-models-context-allow-network refresh) (eq signal (pig:refresh-models-context-signal refresh))
         (pig:tool-renderers-render-shell renderers) (and (pig:tool-renderers-render-result renderers) (pig:tool-renderers-call-view renderers)
                                                         (pig:tool-renderers-result-view renderers) t)
         (pig:tool-render-context-tool-call-id render-context) (pig:tool-render-context-cwd render-context)
         (pig:tool-render-context-execution-started render-context) (pig:tool-render-context-args-complete render-context)
         (pig:tool-render-context-expanded render-context) (pig:tool-render-context-show-images render-context) (pig:tool-render-context-is-error render-context)
         (pig:tool-render-context-output-pad render-context) (pig:jget (pig:tool-render-context-args render-context) "a")
         (funcall (pig:tool-render-context-invalidate render-context))
         cancelled))))))
