;;;; SPDX-License-Identifier: MIT
;;;; Lane P: providers, OAuth, renderers, virtual models and MCP registration against the in-process fake host.
(in-package #:pig-tests)

(defun pt-run (host id method responder &rest members)
  "Send the request METHOD, answer each host call with (funcall RESPONDER call-method args), and return (values result error-message frames)
once the response arrives. A responder that returns (values nil message) answers with an error."
  (apply #'request host id method members)
  (let ((frames '()))
    (loop
      (let* ((frame (hread host)) (type (pig:jget frame "type")))
        (when (eq frame :eof) (error "end of file waiting for the response of ~A" id))
        (push frame frames)
        (cond ((equal type "call")
               (let ((call (pig:jget frame "call")))
                 (multiple-value-bind (result message) (funcall responder (pig:jget call "method") (pig:jget call "args"))
                   (hsend host "type" "call_result" "id" (pig:jget frame "id")
                              "call_result" (if message (pig:obj "error" (pig:obj "message" message)) (pig:obj "result" result))))))
              ((equal type "response")
               (let ((response (pig:jget frame "response")))
                 (return (values (pig:jget response "result") (pig:jref response "error" "message") (nreverse frames))))))))))

(defun pt-calls (frames &optional method)
  "The call members of the call frames in FRAMES, those of METHOD only when given."
  (loop for frame in frames
        when (and (equal (pig:jget frame "type") "call") (or (null method) (equal (pig:jref frame "call" "method") method)))
          collect (pig:jget frame "call")))

(defun pt-notifies (frames method)
  (loop for frame in frames
        when (and (equal (pig:jget frame "type") "notify") (equal (pig:jref frame "notify" "method") method))
          collect (pig:jref frame "notify" "args")))

(defvar *pt-updated* nil)

(defun pt-none (method args) (declare (ignore method args)) nil)

(defun pt-model (id) (pig:obj "id" id "name" id "provider" "tp" "api" "openai-completions"))

(defun pt-message (text)
  (pig:obj "role" "assistant" "content" (vector (pig:obj "type" "text" "text" text)) "stopReason" "stop" "api" "a" "provider" "tp" "model" "m"))

(defun pt-done-stream (text)
  (let ((stream (pig:make-model-event-stream)))
    (pig:model-stream-push stream (pig:obj "type" "start"))
    (pig:model-stream-push stream (pig:obj "type" "done" "reason" "stop" "message" (pt-message text)))
    stream))

(defun pt-provider ()
  (pig:make-provider
   :id "tp" :name "Test Provider" :base-url "http://base" :headers (pig:obj "X-A" "1")
   :auth (pig:make-provider-auth
          :api-key (pig:make-api-key-auth
                    :name "Key"
                    :resolve (lambda (input)
                               (let ((key (or (pig:jget (pig:api-key-auth-input-credential input) "key")
                                              (funcall (pig:auth-context-env (pig:api-key-auth-input-ctx input)) "TP_KEY"))))
                                 (and key (pig:make-auth-result :auth (pig:obj "apiKey" key) :source "test"))))
                    :check (lambda (input) (declare (ignore input)) (pig:make-auth-check :type "api_key" :source "src"))
                    :login (lambda (interaction)
                             (pig:obj "type" "api_key" "key" (funcall (pig:auth-interaction-prompt interaction)
                                                                      (pig:obj "type" "secret" "message" "k"))))
          :oauth (pig:make-oauth-auth
                  :name "OA" :is-subscription t :login-label "Login"
                  :login (lambda (interaction options)
                           (pig:obj "access" (format nil "~A@~A" (funcall (pig:auth-interaction-prompt interaction) (pig:obj "message" "p"))
                                                     (if (pig:login-options-get-device-id options)
                                                         (funcall (pig:login-options-get-device-id options))
                                                         "none"))
                                    "agent" (or (pig:login-options-agent-name options) :null)))
                  :refresh (lambda (credential signal) (declare (ignore signal))
                             (let ((copy (pig::prov-copy-table credential))) (setf (gethash "access" copy) "rotated") copy))
                  :to-auth (lambda (credential) (pig:obj "apiKey" (pig:jget credential "access"))))))
   :get-models (lambda () (list (pt-model "m1")))
   :get-all-models (lambda () (list (pt-model "m1") (pt-model "all-only")))
   :filter-models (lambda (models credential) (declare (ignore credential)) (list (aref models 1)))
   :stream (lambda (model context options)
             (declare (ignore model context))
             (when (pig:provider-stream-options-on-payload options)
               (funcall (pig:provider-stream-options-on-payload options) (pig:obj "p" 1) (pt-model "m1")))
             (pt-done-stream (pig:jget (pig:provider-stream-options-values options) "text" "streamed")))
   :stream-simple (lambda (model context options) (declare (ignore model context options)) (pt-done-stream "simple"))
   :refresh-models (lambda (input)
                     (funcall (pig:refresh-models-context-publish input)
                              (pig:make-models-publication :persist :null :update (lambda () (setf *pt-updated* (pig:refresh-models-context-base-url input))))))
   :cancel-deferred (lambda (model handle options) (declare (ignore model options))
                      (unless (equal (pig:jget handle "id") "deferred") (error "wrong handle")))
   :classify (lambda (model request options) (declare (ignore model options)) (pig:obj "answers" request))))

(defun pt-ext (&rest setup)
  (let ((ext (pig:make-extension "pt")))
    (dolist (fn setup) (funcall fn ext))
    ext))

;;; ---------------------------------------------------------------- the register payload

(deftest registrations-queued-during-the-factory-reach-the-register-payload
  (let ((ext (pt-ext)))
    (pig:register-provider ext "cfg" (pig:obj "baseUrl" "https://x" "streamSimple" (lambda (ctx m c o) (declare (ignore ctx m c o)) nil)
                                              "images" (pig:obj "z-api" #'identity "a-api" #'identity)
                                              "classifiers" (pig:obj "c-api" #'identity)))
    (pig:register-provider ext "gone" (pig:obj "streamSimple" #'identity))
    (pig:unregister-provider ext "gone")
    (pig:register-provider ext (pt-provider))
    (pig:register-mcp-server ext "b" (pig:obj "command" "x"))
    (pig:register-mcp-server ext "a" (pig:obj "command" "a"))
    (pig:register-mcp-server ext "b" (pig:obj "command" "b2"))
    (pig:register-mcp-server ext "c" (pig:obj "command" "c"))
    (pig:unregister-mcp-server ext "c")
    (pig:register-virtual-model ext (pig:make-virtual-model :provider "v" :id "m" :name "M" :route #'identity :input '("text") :max-tokens 7))
    (pig:register-virtual-model ext (pig:make-virtual-model :provider "v" :id "n" :name "N" :route #'identity))
    (pig:unregister-virtual-model ext "v" "n")
    (pig:message-renderer ext "mt" (lambda (ctx m o w) (declare (ignore ctx m o w)) nil))
    (pig:entry-renderer ext "et" (lambda (ctx m o w) (declare (ignore ctx m o w)) nil))
    (pig:markdown-transformer ext (lambda (md context) (declare (ignore context)) md))
    (pig:tool-renderer ext (lambda (tool next) (declare (ignore tool)) (funcall next)))
    (with-host (host ext register)
      (let* ((r (pig:jget register "register")) (providers (pig:jget r "providers")))
        (check-equal 2 (length providers))
        (let ((cfg (aref providers 0)))
          (check-equal "cfg" (pig:jget cfg "name"))
          (check-equal "https://x" (pig:jref cfg "config" "baseUrl"))
          (check-equal nil (nth-value 1 (gethash "streamSimple" (pig:jget cfg "config"))))
          (check-equal t (pig:jget cfg "stream_simple"))
          (check-equal '("a-api" "z-api") (coerce (pig:jget cfg "image_apis") 'list))
          (check-equal '("c-api") (coerce (pig:jget cfg "classifier_apis") 'list)))
        (let* ((native (pig:jref (aref providers 1) "native")))
          (check-equal "tp" (pig:jget (aref providers 1) "name"))
          (check-equal "tp" (pig:jget native "id"))
          (check-equal 2 (length (pig:jget native "models")))
          (check (stringp (pig:jget native "key")))
          (check-equal "http://base" (pig:jget native "baseUrl"))
          (check-equal "Key" (pig:jref native "auth" "apiKey" "name"))
          (check-equal t (pig:jref native "auth" "oauth" "isSubscription"))
          (check-equal "Login" (pig:jref native "auth" "oauth" "loginLabel"))
          (check-equal '("getModels" "getAllModels" "filterModels" "refreshModels" "stream" "streamSimple" "cancelDeferred" "classify"
                         "auth.apiKey.check" "auth.apiKey.resolve" "auth.apiKey.login" "auth.oauth.login" "auth.oauth.refresh"
                         "auth.oauth.toAuth")
                       (coerce (pig:jget native "methods") 'list)))
        (check-equal '("b" "a") (map 'list (lambda (s) (pig:jget s "name")) (pig:jget r "mcp_servers")))
        (check-equal "b2" (pig:jref (aref (pig:jget r "mcp_servers") 0) "config" "command"))
        (check-equal '("m") (map 'list (lambda (m) (pig:jget m "id")) (pig:jget r "virtual_models")))
        (check-equal 7 (pig:jref (aref (pig:jget r "virtual_models") 0) "maxTokens"))
        (check-equal '("text") (coerce (pig:jref (aref (pig:jget r "virtual_models") 0) "input") 'list))
        (check-equal '("n") (map 'list (lambda (m) (pig:jget m "id")) (pig:jget r "unregister_virtual_models")))
        (check-equal "mt" (pig:jref (aref (pig:jget r "message_renderers") 0) "custom_type"))
        (check-equal "et" (pig:jref (aref (pig:jget r "entry_renderers") 0) "custom_type"))
        (check-equal t (pig:jget r "markdown_transformer"))
        (check-equal 1 (pig:jget r "tool_renderers"))))))

(deftest a-provider-without-renderers-or-registrations-adds-nothing-but-empty-lists
  (with-host (host (pt-ext) register)
    (let ((r (pig:jget register "register")))
      (check-equal 0 (length (pig:jget r "providers")))
      (check-equal 0 (length (pig:jget r "message_renderers")))
      (dolist (key '("markdown_transformer" "tool_renderers" "mcp_servers" "virtual_models" "unregister_virtual_models"))
        (check (not (nth-value 1 (gethash key r))) key)))))

(deftest provider-objects-need-an-id
  (check-error error (pig:register-native-provider (pt-ext) (pig:make-provider :id "  " :name "x")))
  (check-error error (pig:register-native-provider (pt-ext) "tp")))

;;; ---------------------------------------------------------------- late registrations

(defun pt-command-ext (thunk)
  "An extension whose command \"c\" runs THUNK with the extension and the context."
  (let ((ext (pig:make-extension "late")))
    (pig:command ext "c" "C" (lambda (ctx args) (declare (ignore args)) (funcall thunk ext ctx)))
    ext))

(deftest registrations-after-load-are-calls-to-the-host
  (let ((ext (pt-command-ext
              (lambda (ext ctx)
                (declare (ignorable ctx))
                (pig:register-provider ext "late" (pig:obj "baseUrl" "u" "streamSimple" #'identity))
                (pig:register-provider ext (pt-provider))
                (pig:unregister-provider ext "late")
                (pig:register-mcp-server ext "m" (pig:obj "command" "x"))
                (setf *pt-updated* (pig:ctx-get-mcp-servers ctx))
                (pig:unregister-mcp-server ext "m")
                (pig:register-virtual-model ext (pig:make-virtual-model :provider "v" :id "m" :name "M" :route #'identity))
                (pig:unregister-virtual-model ext "v" "m")
                (pig:message-renderer ext "lm" #'identity)
                (pig:entry-renderer ext "le" #'identity)
                (pig:tool-renderer ext (lambda (tool next) (declare (ignore tool)) (funcall next)))))))
    (with-host (host ext)
      (multiple-value-bind (result message frames)
          (pt-run host "r" "command"
                  (lambda (method args) (declare (ignore args))
                    (if (equal method "registerMcpServer") (pig:obj "servers" (vector (pig:obj "name" "m" "config" (pig:obj)))) nil))
                  "tool" "c" "args" "")
        (declare (ignore result))
        (check-equal nil message)
        (let ((calls (pt-calls frames)))
          (check-equal '("registerProvider" "registerProvider" "unregisterProvider" "registerMcpServer" "unregisterMcpServer"
                         "registerVirtualModel" "unregisterVirtualModel" "registerDeclaration" "registerDeclaration")
                       (mapcar (lambda (c) (pig:jget c "method")) calls))
          (check-equal "late" (pig:jref (first calls) "args" "name"))
          (check-equal t (pig:jref (first calls) "args" "stream_simple"))
          (check (stringp (pig:jref (second calls) "args" "native" "key")))
          (check-equal "m" (pig:jref (fourth calls) "args" "name"))
          (check-equal "lm" (pig:jref (eighth calls) "args" "message_renderers" 0 "custom_type"))
          (check-equal "le" (pig:jref (ninth calls) "args" "entry_renderers" 0 "custom_type")))
        (check-equal 1 (length (pt-notifies frames "tool_renderers")))
        (check-equal 1 (pig:jget (first (pt-notifies frames "tool_renderers")) "count"))
        (check-equal "m" (pig:jref *pt-updated* 0 "name"))))))

(deftest a-refused-provider-registration-keeps-the-functions-the-provider-had
  (let* ((seen nil)
         (ext (pt-command-ext
               (lambda (ext ctx)
                 (declare (ignore ctx))
                 (pig:register-provider ext "p" (pig:obj "streamSimple" (lambda (ctx m c o) (declare (ignore ctx m c o)) (pt-done-stream "old"))))
                 (setf seen (handler-case (pig:register-provider ext "p" (pig:obj "streamSimple" (lambda (ctx m c o) (declare (ignore ctx m c o)) nil)))
                              (pig:host-call-error (e) (pig:host-call-error-message e))))))))
    (with-host (host ext)
      (let ((n 0))
        (pt-run host "r" "command" (lambda (method args) (declare (ignore method args))
                                     (if (= (incf n) 2) (values nil "refused") nil))
                "tool" "c" "args" ""))
      (check-equal "refused" seen)
      (multiple-value-bind (result) (pt-run host "s" "provider_stream_simple" #'pt-none "tool" "p"
                                            "args" (pig:obj "model" (pt-model "m") "context" (pig:obj) "options" (pig:obj)))
        (check-equal "old" (pig:jref result "content" 0 "text"))))))

;;; ---------------------------------------------------------------- the host calling a native Provider

(defun pt-native-host (&optional (provider (pt-provider)))
  "Start a host for an extension that registered PROVIDER; (values host key)."
  (multiple-value-bind (host register) (start-host (pt-ext (lambda (ext) (pig:register-native-provider ext provider))))
    (values host (pig:jref (aref (pig:jref register "register" "providers") 0) "native" "key"))))

(defmacro with-native-host ((host key &optional provider) &body body)
  `(multiple-value-bind (,host ,key) (pt-native-host ,@(and provider (list provider)))
     (unwind-protect (progn ,@body) (stop-host ,host))))

(defun pt-provider-call (host key method params responder &optional (id "r"))
  (pt-run host id "provider_call" responder "tool" key "args" (pig:obj "method" method "params" params)))

(deftest the-host-reads-the-models-and-filters-them-by-identity
  (with-native-host (host key)
    (check-equal '("m1") (map 'list (lambda (m) (pig:jget m "id")) (pt-provider-call host key "getModels" (pig:obj) #'pt-none)))
    (check-equal 2 (length (pt-provider-call host key "getAllModels" (pig:obj) #'pt-none "r2")))
    (let ((result (pt-provider-call host key "filterModels"
                                    (pig:obj "models" (vector (pt-model "a") (pt-model "b")) "credential" (pig:obj "key" "selected"))
                                    #'pt-none "r3")))
      (check-equal '("b") (map 'list (lambda (m) (pig:jget m "id")) (pig:jget result "models")))
      (check-equal '(1) (coerce (pig:jget result "indices") 'list)))))

(deftest auth-methods-resolve-through-the-callbacks-the-host-answers
  (with-native-host (host key)
    (let ((result (pt-provider-call host key "auth.apiKey.resolve" (pig:obj "credential" (pig:obj "key" "stored")) #'pt-none)))
      (check-equal "stored" (pig:jref result "auth" "apiKey"))
      (check-equal "test" (pig:jget result "source"))
      (check (not (nth-value 1 (gethash "env" result)))))
    (multiple-value-bind (result message frames)
        (pt-provider-call host key "auth.apiKey.resolve" (pig:obj "credential" :null)
                          (lambda (method args)
                            (check-equal "provider.callback" method)
                            (check-equal "env" (pig:jget args "method"))
                            (check-equal "TP_KEY" (pig:jref args "params" "name"))
                            "from-env")
                          "r2")
      (declare (ignore message))
      (check-equal "from-env" (pig:jref result "auth" "apiKey"))
      (check-equal key (pig:jget (first (pt-calls frames "provider.callback")) "args" "provider")))
    (let ((check-result (pt-provider-call host key "auth.apiKey.check" (pig:obj) #'pt-none "r3")))
      (check-equal "api_key" (pig:jget check-result "type"))
      (check-equal "src" (pig:jget check-result "source")))))

(deftest logins-prompt-and-ask-for-the-device-id-only-when-the-host-lists-it
  (with-native-host (host key)
    (let ((prompts '()))
      (flet ((answer (method args)
               (declare (ignore method))
               (cond ((equal (pig:jget args "method") "prompt") (push (pig:jget args "params") prompts) "typed")
                     ((equal (pig:jget args "method") "getDeviceId") "device-1")
                     (t nil))))
        (check-equal "typed" (pig:jget (pt-provider-call host key "auth.apiKey.login" (pig:obj) #'answer) "key"))
        (let ((oauth (pt-provider-call host key "auth.oauth.login"
                                       (pig:obj "options" (pig:obj "getDeviceId" t "agentName" "agent-x")) #'answer "r2")))
          (check-equal "typed@device-1" (pig:jget oauth "access"))
          (check-equal "agent-x" (pig:jget oauth "agent")))
        (check-equal "typed@none" (pig:jget (pt-provider-call host key "auth.oauth.login" (pig:obj) #'answer "r3") "access"))
        (check-equal 3 (length prompts))))))

(deftest oauth-refresh-and-to-auth-keep-the-credential-members
  (with-native-host (host key)
    (let* ((credential (pig:obj "type" "oauth" "access" "a" "refresh" "r" "expires" 1.5d0 "extra" (pig:obj "k" (vector 1 :null ""))))
           (rotated (pt-provider-call host key "auth.oauth.refresh" (pig:obj "credential" credential) #'pt-none)))
      (check-equal "rotated" (pig:jget rotated "access"))
      (check-equal "{\"type\":\"oauth\",\"access\":\"rotated\",\"refresh\":\"r\",\"expires\":1.5,\"extra\":{\"k\":[1,null,\"\"]}}"
                   (pig:to-json rotated))
      (check-equal "k" (pig:jget (pt-provider-call host key "auth.oauth.toAuth" (pig:obj "credential" (pig:obj "access" "k")) #'pt-none "r2") "apiKey")))))

(deftest refresh-models-publishes-and-the-host-can-run-the-update
  (with-native-host (host key)
    (setf *pt-updated* nil)
    (let ((token nil))
      (pt-provider-call host key "refreshModels"
                        (pig:obj "credential" :null "allowNetwork" t "force" t "baseUrl" "https://regional")
                        (lambda (method args)
                          (check-equal "provider.callback" method)
                          (check-equal "publish" (pig:jget args "method"))
                          (let ((publication (pig:jref args "params" "publication")))
                            (check (eq :null (pig:jget publication "persist")))
                            (setf token (pig:jref args "params" "token"))
                            (check (stringp token))
                            ;; the host runs the update while the publication is open
                            (pt-provider-call host key "update" (pig:obj "token" token) #'pt-none "u")
                            t)))
      (check-equal "https://regional" *pt-updated*)
      (multiple-value-bind (result message) (pt-provider-call host key "update" (pig:obj "token" token) #'pt-none "late")
        (declare (ignore result))
        (check-equal "Provider publication is no longer active" message)))))

(deftest deferred-and-operation-methods-reach-the-provider
  (with-native-host (host key)
    (check-equal nil (pt-provider-call host key "cancelDeferred" (pig:obj "model" (pt-model "m1") "handle" (pig:obj "id" "deferred")
                                                                         "callbacks" (vector))
                                       #'pt-none))
    (multiple-value-bind (result message)
        (pt-provider-call host key "cancelDeferred" (pig:obj "model" (pt-model "m1") "handle" (pig:obj "id" "other")) #'pt-none "r2")
      (declare (ignore result))
      (check-equal "wrong handle" message))
    (check-equal "q" (pig:jref (pt-provider-call host key "classify" (pig:obj "model" (pt-model "m1") "context" "q") #'pt-none "r3") "answers"))
    (multiple-value-bind (result message) (pt-provider-call host key "generateImages" (pig:obj) #'pt-none "r4")
      (declare (ignore result))
      (check-equal "Provider method generateImages is absent" message))
    (multiple-value-bind (result message) (pt-run host "r5" "provider_call" #'pt-none "tool" "nope" "args" (pig:obj "method" "getModels"))
      (declare (ignore result))
      (check-equal "Provider object is no longer registered" message))))

(deftest a-provider-stream-notifies-its-events-and-calls-the-request-callbacks
  (with-native-host (host key)
    (multiple-value-bind (result message frames)
        (pt-provider-call host key "stream"
                          (pig:obj "model" (pt-model "m1") "context" (pig:obj) "options" (pig:obj "text" "hello")
                                   "callbacks" (vector "onPayload"))
                          (lambda (method args)
                            (check-equal "provider.callback" method)
                            (check-equal "onPayload" (pig:jget args "method"))
                            (check-equal "m1" (pig:jref args "params" "model" "id"))
                            (pig:obj "text" "replaced")))
      (check-equal nil message)
      (check-equal nil result)
      (let ((updates (pt-notifies frames "tool_update")))
        (check-equal '("provider_started" "start" "done") (mapcar (lambda (u) (pig:jref u "result" "type")) updates))
        (check-equal "r" (pig:jget (first updates) "request_id"))
        (check-equal "hello" (pig:jref (third updates) "result" "message" "content" 0 "text"))))))

(deftest a-cancelled-provider-stream-sees-an-aborted-signal
  (let* ((signal-set nil)
         (provider (pig:make-provider :id "ab" :name "Ab" :auth (pig:make-provider-auth)
                                      :stream (lambda (model context options)
                                                (declare (ignore model context))
                                                (setf signal-set (pig:abort-signal-set-p (pig:provider-stream-options-signal options)))
                                                (pt-done-stream "x")))))
    (with-native-host (host key provider)
      (pt-provider-call host key "stream" (pig:obj "model" (pt-model "m") "context" (pig:obj) "options" (pig:obj) "aborted" t) #'pt-none)
      (check-equal t signal-set))))

;;; ---------------------------------------------------------------- config providers

(deftest stream-simple-config-providers-stream-through-provider-stream-event
  (let* ((cancelled nil)
         (ext (pt-ext (lambda (ext)
                        (pig:register-provider ext "cfg" (pig:obj "streamSimple"
                                                                    (lambda (ctx model context options)
                                                                      (declare (ignore ctx context))
                                                                      (check-equal "text" (pig:jget options "kind"))
                                                                      (setf cancelled model)
                                                                      (pt-done-stream "cfg-answer"))))))))
    (with-host (host ext)
      (multiple-value-bind (result message frames)
          (pt-run host "r" "provider_stream_simple" #'pt-none "tool" "cfg"
                  "args" (pig:obj "model" (pt-model "m") "context" (pig:obj) "options" (pig:obj "kind" "text")))
        (check-equal nil message)
        (check-equal "cfg-answer" (pig:jref result "content" 0 "text"))
        (check-equal '("start" "done") (mapcar (lambda (u) (pig:jref u "result" "type")) (pt-notifies frames "provider_stream_event")))
        (check-equal "m" (pig:jget cancelled "id")))
      (multiple-value-bind (result message) (pt-run host "r2" "provider_stream_simple" #'pt-none "tool" "unknown" "args" (pig:obj))
        (declare (ignore result))
        (check-equal "unknown provider stream \"unknown\"" message)))))

(deftest a-cancelled-stream-simple-request-ends-with-an-error
  (let* ((stream (pig:make-model-event-stream))
         (ext (pt-ext (lambda (ext)
                        (pig:register-provider ext "cfg" (pig:obj "streamSimple" (lambda (ctx model context options)
                                                                                    (declare (ignore ctx model context options))
                                                                                    stream)))))))
    (with-host (host ext)
      (request host "r" "provider_stream_simple" "tool" "cfg" "args" (pig:obj "model" (pt-model "m") "context" (pig:obj) "options" (pig:obj)))
      (hread-type host "request_state")
      (hsend host "type" "cancel" "cancel" (pig:obj "request_id" "r" "reason" "stop"))
      (check-equal "provider stream aborted" (pig:jref (pig:jget (hread-type host "response") "response") "error" "message")))))

(deftest image-and-classifier-operations-run-the-registered-function
  (let ((ext (pt-ext (lambda (ext)
                       (pig:register-provider ext "cfg"
                                              (pig:obj "images" (pig:obj "img" (lambda (model context options)
                                                                                 (pig:obj "model" (pig:jget model "id") "context" context
                                                                                          "options" (pig:provider-operation-options-values options))))))))))
    (with-host (host ext)
      (let ((result (pt-run host "r" "provider_operation" #'pt-none "tool" "cfg"
                            "args" (pig:obj "kind" "images" "api" "img" "model" (pt-model "m") "context" "c" "options" (pig:obj "o" 1)))))
        (check-equal "m" (pig:jget result "model"))
        (check-equal 1 (pig:jref result "options" "o")))
      (multiple-value-bind (result message)
          (pt-run host "r2" "provider_operation" #'pt-none "tool" "cfg" "args" (pig:obj "kind" "classifiers" "api" "x"))
        (declare (ignore result))
        (check-equal "Provider cfg has no classifier implementation for \"x\"" message))
      (multiple-value-bind (result message) (pt-run host "r3" "provider_operation" #'pt-none "tool" "cfg" "args" (pig:obj "kind" "bogus"))
        (declare (ignore result))
        (check-equal "Unknown provider operation \"bogus\"" message)))))

;;; ---------------------------------------------------------------- remote Providers

(defun pt-remote-declaration (&optional (handle "h1"))
  (pig:obj "id" "tp" "key" "remote-key" "name" "Remote" "handle" handle
           "methods" (vector "getModels" "getAllModels" "filterModels" "stream" "classify" "auth.apiKey.resolve" "auth.apiKey.login"
                             "auth.oauth.login" "auth.oauth.refresh" "auth.oauth.toAuth" "cancelDeferred")
           "auth" (pig:obj "apiKey" (pig:obj "name" "Key") "oauth" (pig:obj "name" "OA" "isSubscription" t "loginLabel" "L"))
           "baseUrl" "http://remote" "headers" (pig:obj "X" "1")))

(deftest a-provider-another-extension-registered-is-a-proxy-that-calls-it
  (let* ((found nil) (capture nil)
         (ext (pt-command-ext (lambda (ext ctx)
                                (declare (ignore ext))
                                (let ((registry (pig:ctx-model-registry ctx)))
                                  (setf found (pig:registry-get-registered-native-provider registry "tp"))
                                  (setf capture (list (eq found (pig:registry-get-provider registry "tp"))
                                                      (pig:registry-get-provider-name registry "tp")
                                                      (pig:registry-get-provider registry "nobody")))))))
         (calls '()))
    (with-host (host ext)
      (pt-run host "r" "command"
              (lambda (method args)
                (push (cons method args) calls)
                (cond ((equal method "getModelRegistryState")
                       (pig:obj "providers" (pig:obj "tp" (pig:obj "configured" t))
                                "registered" (vector (pig:obj "name" "tp" "native" (pt-remote-declaration)))))
                      (t nil)))
              "tool" "c" "args" "")
      (check (provider-p-check found))
      (check-equal '(t "Remote" nil) capture)
      (check-equal "provider.retain" (car (find "provider.retain" (reverse calls) :key #'car :test #'equal)))
      (check-equal "h1" (pig:jget (cdr (find "provider.retain" calls :key #'car :test #'equal)) "handle"))
      (check-equal "http://remote" (pig:provider-base-url found))
      (check (pig:provider-filter-models found))
      (check (null (pig:provider-filter-all-models found)))
      (check (pig:provider-cancel-deferred found))
      (check (null (pig:provider-refresh-models found)))
      (check-equal t (pig:oauth-auth-is-subscription (pig:provider-auth-oauth (pig:provider-auth found)))))))

(defun provider-p-check (x) (pig:provider-p x))

(deftest a-proxy-calls-provider-object-with-its-callbacks-and-filters-in-place
  (let* ((result nil)
         (ext (pt-command-ext
               (lambda (ext ctx)
                 (declare (ignore ext))
                 (let* ((provider (pig:registry-get-registered-native-provider (pig:ctx-model-registry ctx) "tp"))
                        (models (pig:provider-get-models provider))
                        (filtered (funcall (pig:provider-filter-models provider) (vector (pt-model "x") (pt-model "y")) :null))
                        (resolve (funcall (pig:api-key-auth-resolve (pig:provider-auth-api-key (pig:provider-auth provider)))
                                          (pig:make-api-key-auth-input
                                           :ctx (pig:make-auth-context :env (lambda (name) (concatenate 'string "env:" name))
                                                                       :file-exists (lambda (path) (declare (ignore path)) nil))
                                           :credential (pig:obj "key" "k"))))
                        (login (funcall (pig:oauth-auth-login (pig:provider-auth-oauth (pig:provider-auth provider)))
                                        (pig:make-auth-interaction :prompt (lambda (prompt) (pig:jget prompt "message")) :notify #'identity)
                                        (pig:make-login-options :get-device-id (lambda () "dev") :agent-name "ag"))))
                   (setf result (list (map 'list (lambda (m) (pig:jget m "id")) models)
                                      (map 'list (lambda (m) (pig:jget m "id")) filtered)
                                      (pig:auth-result-source resolve) login)))))))
    (with-host (host ext)
      (let ((seen '()))
        (pt-run host "r" "command"
                (lambda (method args)
                  (cond ((equal method "getModelRegistryState")
                         (pig:obj "registered" (vector (pig:obj "name" "tp" "native" (pt-remote-declaration)))))
                        ((equal method "provider.object")
                         (push (cons (pig:jget args "method") args) seen)
                         (let ((m (pig:jget args "method")))
                           (cond ((equal m "getModels") (vector (pt-model "m1")))
                                 ((equal m "filterModels")
                                  (pig:obj "models" (vector (pig:obj "id" "y" "changed" t)) "indices" (vector 1)))
                                 ((equal m "auth.apiKey.resolve")
                                  ;; the proxy's callbacks answer env lookups
                                  (check (stringp (pig:jget args "callbackId")))
                                  (pig:obj "auth" (pig:obj "apiKey" "k") "source" "remote-source"))
                                 ((equal m "auth.oauth.login")
                                  (check-equal t (pig:jref args "params" "options" "getDeviceId"))
                                  (check-equal "ag" (pig:jref args "params" "options" "agentName"))
                                  (pig:obj "access" "remote-login"))
                                 (t nil))))
                        (t nil)))
                "tool" "c" "args" "")
        (check-equal '("m1") (first result))
        (check-equal '("y") (second result))
        (check-equal "remote-source" (third result))
        (check-equal "remote-login" (pig:jget (fourth result) "access"))
        (check-equal "" (pig:jget (cdr (assoc "getModels" seen :test #'equal)) "streamId"))))))

(deftest a-proxy-stream-ends-with-an-error-event-when-the-call-fails
  (let* ((outcome nil)
         (ext (pt-command-ext
               (lambda (ext ctx)
                 (declare (ignore ext))
                 (let* ((provider (pig:registry-get-registered-native-provider (pig:ctx-model-registry ctx) "tp"))
                        (stream (funcall (pig:provider-stream provider) (pt-model "m1") (pig:obj "messages" (vector)) nil)))
                   (setf outcome (pig:model-stream-result stream)))))))
    (with-host (host ext)
      (let ((stream-id nil))
        (pt-run host "r" "command"
                (lambda (method args)
                  (cond ((equal method "getModelRegistryState")
                         (pig:obj "registered" (vector (pig:obj "name" "tp" "native" (pt-remote-declaration)))))
                        ((equal method "provider.object")
                         (setf stream-id (pig:jget args "streamId"))
                         (hsend host "type" "notify" "notify" (pig:obj "method" "model_stream_event" "args" (pig:obj "streamId" stream-id "started" t)))
                         (values nil "owner gone"))
                        (t nil)))
                "tool" "c" "args" "")
        (check (plusp (length stream-id)))
        (check-equal "error" (pig:jget outcome "stopReason"))
        (check-equal "owner gone" (pig:jget outcome "errorMessage"))))))

;;; ---------------------------------------------------------------- OAuth

(defun pt-oauth-ext ()
  (let ((ext (pt-ext)) (store-calls '()))
    (pig:register-oauth-provider
     ext "oa" (pig:obj "name" "OA")
     (pig:make-oauth-provider
      :name "OA Name" :is-subscription t
      :login (lambda (callbacks)
               (pig:oauth-on-auth callbacks (pig:make-oauth-auth-info :url "https://auth" :instructions "go"))
               (pig:oauth-on-device-code callbacks (pig:make-oauth-device-code-info :user-code "U" :verification-uri "V" :copy-code-on-confirm :false))
               (pig:oauth-on-progress callbacks "waiting")
               (let ((typed (pig:oauth-on-prompt callbacks (pig:make-oauth-prompt :message "code" :placeholder "ph" :allow-empty t)))
                     (picked (pig:oauth-on-select callbacks (pig:make-oauth-select-prompt
                                                             :message "pick" :options (list (pig:make-oauth-select-option :id "a" :label "A"))))))
                 (pig:oauth-credentials :access (format nil "~A/~A/~A" typed picked (pig:oauth-on-manual-code-input callbacks))
                                        :refresh "r" :expires 42 :account-id "acc")))
      :refresh-token (lambda (credentials) (let ((copy (pig::prov-copy-table credentials))) (setf (gethash "access" copy) "fresh") copy))
      :get-api-key (lambda (credentials) (concatenate 'string "key:" (pig:jget credentials "access")))
      :credential-store (pig:make-oauth-credential-store
                         :credential-status (lambda () (pig:make-oauth-credential-status :present t :auth-type "oauth" :source "store"))
                         :store-credentials (lambda (credentials) (push credentials store-calls) "/path")
                         :delete-credentials (lambda () t))))
    ext))

(deftest an-oauth-provider-registers-a-descriptor-without-callables
  (with-host (host (pt-oauth-ext) register)
    (let* ((entry (aref (pig:jref register "register" "providers") 0)) (oauth (pig:jref entry "config" "oauth")))
      (check-equal "oa" (pig:jget entry "name"))
      (check-equal "OA Name" (pig:jget oauth "name"))
      (check-equal t (pig:jget oauth "isSubscription"))
      (check-equal t (pig:jget oauth "has_login"))
      (check-equal t (pig:jget oauth "has_credential_store")))))

(deftest an-oauth-login-drives-the-hosts-login-ui-and-returns-the-credentials
  (with-host (host (pt-oauth-ext))
    (multiple-value-bind (result message frames)
        (pt-run host "r" "oauth_login"
                (lambda (method args)
                  (cond ((equal method "oauth.cb.onPrompt") (check-equal "code" (pig:jget args "message")) (check-equal t (pig:jget args "allowEmpty"))
                         (pig:obj "value" "typed"))
                        ((equal method "oauth.cb.onSelect") (check-equal "a" (pig:jref args "options" 0 "id")) (pig:obj "value" "a"))
                        ((equal method "oauth.cb.onManualCodeInput") (pig:obj "value" "manual"))
                        ((equal method "oauth.cb.onAuth") (check-equal "go" (pig:jget args "instructions")) nil)
                        ((equal method "oauth.cb.onDeviceCode")
                         (check-equal "U" (pig:jget args "userCode")) (check-equal :false (pig:jget args "copyCodeOnConfirm"))
                         (check (not (nth-value 1 (gethash "openBrowserOnConfirm" args)))) nil)
                        ((equal method "oauth.cb.onProgress") (check-equal "waiting" (pig:jget args "message")) nil)
                        (t (error "unexpected ~A" method))))
                "tool" "oa")
      (check-equal nil message)
      (check-equal "typed/a/manual" (pig:jget result "access"))
      (check-equal "{\"refresh\":\"r\",\"access\":\"typed/a/manual\",\"expires\":42,\"accountId\":\"acc\"}" (pig:to-json result))
      (check-equal '("oauth.cb.onAuth" "oauth.cb.onDeviceCode" "oauth.cb.onProgress" "oauth.cb.onPrompt" "oauth.cb.onSelect"
                     "oauth.cb.onManualCodeInput")
                   (mapcar (lambda (c) (pig:jget c "method")) (pt-calls frames)))
      (check (every (lambda (c) (equal "r" (pig:jget c "parent_request_id"))) (pt-calls frames)))
      (let ((states (loop for f in frames when (equal (pig:jget f "type") "request_state")
                          collect (list (pig:jref f "request_state" "state") (pig:jref f "request_state" "reason")))))
        (check (member '("blocked" "user") states :test #'equal))
        (check (member '("progress" nil) states :test #'equal))))))

(deftest a-dismissed-login-prompt-ends-the-login-as-cancelled
  (with-host (host (pt-oauth-ext))
    (multiple-value-bind (result message)
        (pt-run host "r" "oauth_login" (lambda (method args) (declare (ignore args))
                                         (if (equal method "oauth.cb.onPrompt") (pig:obj "cancel" t) nil))
                "tool" "oa")
      (declare (ignore result))
      (check-equal "Login cancelled" message))))

(deftest oauth-refresh-key-and-store-requests
  (with-host (host (pt-oauth-ext))
    (check-equal "fresh" (pig:jget (pt-run host "r1" "oauth_refresh" #'pt-none "tool" "oa" "args" (pig:obj "access" "old" "refresh" "r")) "access"))
    (check-equal "key:tok" (pig:jget (pt-run host "r2" "oauth_get_api_key" #'pt-none "tool" "oa" "args" (pig:obj "access" "tok")) "apiKey"))
    (let ((status (pt-run host "r3" "oauth_credential_status" #'pt-none "tool" "oa")))
      (check-equal t (pig:jget status "present")) (check-equal "oauth" (pig:jget status "authType")) (check-equal "store" (pig:jget status "source")))
    (check-equal "/path" (pig:jget (pt-run host "r4" "oauth_store_credentials" #'pt-none "tool" "oa" "args" (pig:obj "access" "x")) "path"))
    (check-equal t (pig:jget (pt-run host "r5" "oauth_delete_credentials" #'pt-none "tool" "oa") "deleted"))
    (multiple-value-bind (result message) (pt-run host "r6" "oauth_login" #'pt-none "tool" "nobody")
      (declare (ignore result))
      (check-equal "unknown oauth provider: nobody" message))))

(deftest oauth-credentials-keep-big-expiries-and-provider-members
  (let ((c (pig:oauth-credentials :access "a" :refresh "r" :expires (expt 2 60) :extra (pig:obj "meta" (pig:obj "k" 1) "expires" 5)
                                  :scope "s")))
    (check-equal "{\"meta\":{\"k\":1},\"refresh\":\"r\",\"access\":\"a\",\"expires\":1152921504606847000,\"scope\":\"s\"}"
                 (pig:to-json (pig:from-json (pig:to-json c))) "a 2**60 expiry is the JavaScript number")
    (check (not (nth-value 1 (gethash "expires" (pig:oauth-credentials :expires nil)))))))

;;; ---------------------------------------------------------------- virtual models

(deftest virtual-model-routes-run-in-the-extension
  (let ((ext (pt-ext (lambda (ext)
                       (pig:register-virtual-model
                        ext (pig:make-virtual-model :provider "v" :id "m" :name "M"
                                                    :route (lambda (ctx request)
                                                             (declare (ignore ctx))
                                                             (check (typep (pig:jget request "signal") (quote pig:abort-signal)))
                                                             (pig:obj "model" (pig:jget request "model") "thinkingLevel" "low" "state" :null))))))))
    (with-host (host ext)
      (let ((routed (pt-run host "r" "virtual_model_route" #'pt-none
                            "args" (pig:obj "provider" "v" "id" "m" "request" (pig:obj "model" (pt-model "phys") "state" :null)))))
        (check-equal "phys" (pig:jref routed "model" "id"))
        (check-equal "low" (pig:jget routed "thinkingLevel"))
        (check (not (nth-value 1 (gethash "state" routed))) "a null state keeps the current state"))
      (multiple-value-bind (result message) (pt-run host "r2" "virtual_model_route" #'pt-none "args" (pig:obj "provider" "v" "id" "x"))
        (declare (ignore result))
        (check-equal "unknown virtual model v/x" message)))))

(deftest a-virtual-model-route-must-return-a-model
  (let ((ext (pt-ext (lambda (ext)
                       (pig:register-virtual-model ext (pig:make-virtual-model :provider "v" :id "m" :name "M" :route (lambda (ctx request) (declare (ignore ctx request)) "bad")))))))
    (with-host (host ext)
      (multiple-value-bind (result message) (pt-run host "r" "virtual_model_route" #'pt-none "args" (pig:obj "provider" "v" "id" "m"))
        (declare (ignore result))
        (check-equal "virtual model route must return a mapping with a model and a thinkingLevel" message)))))

(deftest a-refused-late-virtual-model-registration-keeps-the-previous-route
  (let* ((ext (pt-command-ext
               (lambda (ext ctx) (declare (ignore ctx))
                 (pig:register-virtual-model ext (pig:make-virtual-model :provider "v" :id "m" :name "M" :route (lambda (c r) (declare (ignore c r)) (pig:obj "model" (pig:obj "id" "new") "thinkingLevel" "off"))))))))
    (pig:register-virtual-model ext (pig:make-virtual-model :provider "v" :id "m" :name "M" :route (lambda (c r) (declare (ignore c r)) (pig:obj "model" (pig:obj "id" "old") "thinkingLevel" "off"))))
    (with-host (host ext)
      (pt-run host "r" "command" (lambda (method args) (declare (ignore method args)) (values nil "refused")) "tool" "c" "args" "")
      (check-equal "old" (pig:jref (pt-run host "r2" "virtual_model_route" #'pt-none "args" (pig:obj "provider" "v" "id" "m")) "model" "id")))))

;;; ---------------------------------------------------------------- MCP servers

(deftest mcp-servers-come-from-the-state-pushes-and-registration-replies
  (let* ((seen '())
         (ext (pt-command-ext (lambda (ext ctx) (declare (ignore ext)) (push (pig:ctx-get-mcp-servers ctx) seen)))))
    (with-host (host ext)
      (pt-run host "r" "command" #'pt-none "tool" "c" "args" "")
      (hsend host "type" "notify" "notify" (pig:obj "method" "state_update"
                                                    "args" (pig:obj "state" (pig:obj "mcpServers" (vector (pig:obj "name" "s" "config" (pig:obj "z" 1 "a" 2)
                                                                                                                   "extensionPath" "/p"))))))
      (loop repeat 200 until (plusp (length (pig::mcp-servers (pig::mcp-of (host-ext host))))) do (sleep 0.01))
      (pt-run host "r2" "command" #'pt-none "tool" "c" "args" "")
      (check-equal 0 (length (second seen)))
      (check-equal "{\"z\":1,\"a\":2}" (pig:to-json (pig:jref (first seen) 0 "config"))))))

;;; ---------------------------------------------------------------- renderers

(deftest message-and-entry-renderers-return-lines-and-markdown-transforms
  (let ((ext (pt-ext (lambda (ext)
                       (pig:message-renderer ext "mt" (lambda (ctx message options width)
                                                        (declare (ignore ctx))
                                                        (list (format nil "~A:~A:~A" (pig:jget message "content") (pig:jget options "expanded") width))))
                       (pig:entry-renderer ext "et" (lambda (ctx entry options width) (declare (ignore ctx options width)) (vector (pig:jget entry "data"))))
                       (pig:markdown-transformer ext (lambda (markdown context)
                                                       (when (equal markdown "boom") (error "no"))
                                                       (if (equal markdown "other") 5 (format nil "md:~A:~A" markdown (pig:jget context "messageType")))))))))
    (with-host (host ext)
      (check-equal '("hi:false:41") (coerce (pig:jget (pt-run host "r" "render_message" #'pt-none "tool" "mt"
                                                              "args" (pig:obj "message" (pig:obj "content" "hi") "options" (pig:obj "expanded" :false) "width" 41))
                                                      "lines") 'list))
      (check-equal '("e") (coerce (pig:jget (pt-run host "r2" "render_entry" #'pt-none "tool" "et" "args" (pig:obj "entry" (pig:obj "data" "e") "width" 5)) "lines") 'list))
      (check-equal "md:# T:assistant" (pt-run host "r3" "markdown_transform" #'pt-none "args" (pig:obj "markdown" "# T" "context" (pig:obj "messageType" "assistant"))))
      (check-equal nil (pt-run host "r4" "markdown_transform" #'pt-none "args" (pig:obj "markdown" "boom")))
      (check-equal nil (pt-run host "r5" "markdown_transform" #'pt-none "args" (pig:obj "markdown" "other")))
      (multiple-value-bind (result message) (pt-run host "r6" "render_message" #'pt-none "tool" "none" "args" (pig:obj))
        (declare (ignore result))
        (check-equal "unknown message renderer \"none\"" message)))))

(deftest tool-renderers-set-the-declaration-flags-and-render-with-the-cards-state
  (let ((ext (pt-ext (lambda (ext)
                       (pig:tool ext "t" "T" (pig:obj "type" "object") (lambda (ctx p) (declare (ignore ctx p)) "x"))
                       (pig:tool-renderers ext "t" :render-shell "self"
                                                   :render-call (lambda (ctx args render width)
                                                                  (declare (ignore ctx))
                                                                  (let ((calls (incf (gethash "calls" (pig:tool-render-context-state render) 0))))
                                                                    (list (format nil "call:~A:~A:~A:~A" (pig:jget args "topic") calls width
                                                                                  (pig:tool-render-context-is-partial render)))))
                                                   :render-result (lambda (ctx result options render width)
                                                                    (declare (ignore ctx options))
                                                                    (list (format nil "result:~A:~A:~A:~A" (pig:jref result "content" 0 "text") width
                                                                                  (pig:tool-render-context-duration-ms render)
                                                                                  (gethash "calls" (pig:tool-render-context-state render))))))))))
    (with-host (host ext register)
      (let ((decl (aref (pig:jref register "register" "tools") 0)))
        (check-equal "self" (pig:jget decl "render_shell"))
        (check-equal t (pig:jget decl "renders_call"))
        (check-equal t (pig:jget decl "renders_result")))
      (flet ((render (id phase &rest members)
               (coerce (pig:jget (apply #'pt-run host id "render_tool" #'pt-none "tool" "t" members) "lines") 'list)))
        (check-equal '("call:x:1:20:T") (render "r1" "call" "args" (pig:obj "card" "c1" "phase" "call" "width" 20 "args" (pig:obj "topic" "x")
                                                                     "context" (pig:obj "isPartial" t))))
        (check-equal '("call:x:2:20:NIL") (render "r2" "call" "args" (pig:obj "card" "c1" "phase" "call" "width" 20 "args" (pig:obj "topic" "x")
                                                                        "context" (pig:obj))))
        (check-equal '("result:ok:30:12:2") (render "r3" "result" "args" (pig:obj "card" "c1" "phase" "result" "width" 30
                                                                           "result" (pig:obj "content" (vector (pig:obj "type" "text" "text" "ok")))
                                                                           "context" (pig:obj "durationMs" 12))))
        (hsend host "type" "notify" "notify" (pig:obj "method" "tool_render_release" "args" (pig:obj "card" "c1")))
        (sleep 0.05)
        (check-equal '("call:x:1:20:NIL") (render "r4" "call" "args" (pig:obj "card" "c1" "phase" "call" "width" 20 "args" (pig:obj "topic" "x")
                                                                        "context" (pig:obj))))))))

(deftest resolvers-answer-none-next-or-their-own-renderers
  (let ((ext (pt-ext (lambda (ext)
                       (pig:tool-renderer ext (lambda (tool next)
                                                (cond ((equal tool "own") (pig:make-tool-renderers :render-call (lambda (ctx args render width) (declare (ignore ctx args render width)) (list "own"))
                                                                                                    :render-shell "self"))
                                                      ((equal tool "none") nil)
                                                      ((equal tool "wrap") (let ((renderers (copy-structure (funcall next))))
                                                                             (setf (pig:tool-renderers-render-call renderers) (lambda (ctx args render width) (declare (ignore ctx args render width)) (list "wrapped")))
                                                                             renderers))
                                                      (t (funcall next)))))))))
    (with-host (host ext)
      (flet ((resolve (tool &optional (next (pig:obj "render_shell" "default")))
               (pt-run host (format nil "r-~A" tool) "resolve_tool_renderers" #'pt-none "args" (pig:obj "tool" tool "next" next))))
        (let ((own (resolve "own")))
          (check-equal "own" (pig:jget own "use"))
          (check-equal "self" (pig:jget own "render_shell"))
          (check-equal t (pig:jget own "renders_call"))
          (check (not (nth-value 1 (gethash "renders_result" own))))
          (check-equal "own" (pig:jref (pt-run host "rc" "render_tool" #'pt-none "tool" "own" "args" (pig:obj "card" "c" "phase" "call" "renderers" (pig:jget own "renderers"))) "lines" 0)))
        (check-equal "none" (pig:jget (resolve "none") "use"))
        (check-equal "next" (pig:jget (resolve "other") "use"))
        (let ((wrap (resolve "wrap" (pig:obj "render_shell" "self"))))
          (check-equal "own" (pig:jget wrap "use"))
          (check-equal "self" (pig:jget wrap "render_shell")))
        (check-equal "next" (pig:jget (resolve "other" (pig:obj)) "use"))))))

;;; ---------------------------------------------------------------- login and sprite

(deftest login-and-sprite-definitions-are-host-calls
  (let ((ext (pt-command-ext (lambda (ext ctx)
                               (declare (ignore ext))
                               (pig:ctx-set-login ctx (pig:make-login-definition :brand '("b") :hero '("h1" "h2") :mascot '("m") :palette (pig:obj "a" "#fff")
                                                                                 :name "N" :description "D" :tagline "T"))
                               (pig:ctx-register-sprite ctx (pig:make-sprite-definition :id "s" :name "S" :tagline "t" :mascot '("..") :palette (pig:obj "x" "#000")))))))
    (with-host (host ext)
      (multiple-value-bind (result message frames) (pt-run host "r" "command" #'pt-none "tool" "c" "args" "")
        (declare (ignore result))
        (check-equal nil message)
        (let ((login (first (pt-calls frames "ui.setLogin"))) (sprite (first (pt-calls frames "ui.registerSprite"))))
          (check-equal "{\"brand\":[\"b\"],\"hero\":[\"h1\",\"h2\"],\"mascot\":[\"m\"],\"palette\":{\"a\":\"#fff\"},\"name\":\"N\",\"description\":\"D\",\"tagline\":\"T\"}"
                       (pig:to-json (pig:jget login "args")))
          (check-equal "{\"id\":\"s\",\"name\":\"S\",\"tagline\":\"t\",\"mascot\":[\"..\"],\"palette\":{\"x\":\"#000\"}}"
                       (pig:to-json (pig:jget sprite "args"))))))))
