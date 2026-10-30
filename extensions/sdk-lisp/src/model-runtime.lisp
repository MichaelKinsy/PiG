;;;; SPDX-License-Identifier: MIT
;;;; Pi's ModelRuntime (core/model-runtime.ts) and ModelRegistry(runtime): the host owns the object, this holds its handle.

(in-package #:pig)

(defstruct (model-runtime (:include graph-object) (:constructor %make-model-runtime (ctx handle))))

(defstruct (runtime-registry (:constructor make-runtime-registry (runtime)))
  "Pi's `new ModelRegistry(runtime)`: every member answers from RUNTIME."
  runtime)

(defun mrt-true (value) (eq value t))

(defun mrt-now-ms ()
  (multiple-value-bind (seconds microseconds) (sb-ext:get-time-of-day) (+ (* seconds 1000) (floor microseconds 1000))))

(defmacro def-handle-calls (&rest specs)
  "Each spec is (name (parameters...) method [:async] [:true]): a function of an object and the parameters that calls the host METHOD on the object
(:async: Pi's method returns a Promise; :true: the result is a Lisp boolean)."
  `(progn
     ,@(loop for (name params method . flags) in specs
             collect (let* ((vars (loop for p in params unless (member p lambda-list-keywords) collect (if (consp p) (car p) p)))
                            (call `(,(if (member :async flags) 'graph-call-async 'graph-call) object ,method ,@vars)))
                       `(defun ,name (object ,@params) ,(if (member :true flags) `(mrt-true ,call) call))))))

(defun runtime-create (ctx &key auth-path (models-path nil models-path-p) models-store-path allow-model-network model-refresh-timeout-ms catalog-base-url
                              (refresh-on-create nil refresh-on-create-p))
  "ModelRuntime.create(options). MODELS-PATH :null is Pi's `modelsPath: null` (no models.json, an in-memory store); leaving it out is the agent directory's models.json."
  (let ((options (obj)))
    (when auth-path (setf (gethash "authPath" options) auth-path))
    (when models-path-p (setf (gethash "modelsPath" options) (or models-path :null)))
    (when models-store-path (setf (gethash "modelsStorePath" options) models-store-path))
    (when allow-model-network (setf (gethash "allowModelNetwork" options) t))
    (when model-refresh-timeout-ms (setf (gethash "modelRefreshTimeoutMs" options) model-refresh-timeout-ms))
    (when catalog-base-url (setf (gethash "catalogBaseUrl" options) catalog-base-url))
    (when refresh-on-create-p (setf (gethash "refreshOnCreate" options) (if refresh-on-create t :false)))
    (%make-model-runtime ctx (graph-new ctx "ModelRuntime" "create" options))))

(def-handle-calls
  (runtime-get-models (&optional provider) "getModels")
  (runtime-get-model (provider model-id) "getModel")
  (runtime-get-physical-model (provider model-id) "getPhysicalModel")
  (runtime-get-models-of-type (model-type &optional provider) "getModelsOfType")
  (runtime-get-model-of-type (model-type provider model-id) "getModelOfType")
  (runtime-get-all-models (&optional provider) "getAllModels")
  (runtime-get-available-of-type (model-type &optional provider) "getAvailableOfType" :async)
  (runtime-get-all-available (&optional provider) "getAllAvailable" :async)
  (runtime-get-available (&optional provider) "getAvailable" :async)
  (runtime-get-available-snapshot () "getAvailableSnapshot")
  (runtime-get-error () "getError")
  (runtime-check-auth (provider) "checkAuth" :async)
  (runtime-get-registered-provider-ids () "getRegisteredProviderIds")
  (runtime-get-registered-provider-config (provider) "getRegisteredProviderConfig")
  (runtime-get-compatibility-request-config (model) "getCompatibilityRequestConfig")
  (runtime-is-using-oauth-p (provider) "isUsingOAuth" :true)
  (runtime-is-using-subscription-p (provider) "isUsingSubscription" :true)
  (runtime-has-configured-auth-p (provider) "hasConfiguredAuth" :true)
  (runtime-get-provider-auth-status (provider) "getProviderAuthStatus")
  (runtime-set-runtime-api-key (provider api-key) "setRuntimeApiKey" :async)
  (runtime-remove-runtime-api-key (provider) "removeRuntimeApiKey" :async)
  (runtime-list-credentials () "listCredentials" :async)
  (runtime-logout (provider) "logout" :async)
  (runtime-register-provider (provider config) "registerProvider")
  (runtime-unregister-provider (provider) "unregisterProvider")
  (runtime-unregister-virtual-model (provider model-id) "unregisterVirtualModel")
  (runtime-resolve-model (model messages options) "resolveModel" :async))

(defun runtime-get-auth (runtime provider-or-model &optional overrides)
  "The AuthResult ({\"auth\": ..., \"env\"?, \"source\"?}) of a provider id or a model, NIL when unconfigured. OVERRIDES: apiKey, env, minOAuthValidityMs."
  (unless (or (stringp provider-or-model) (hash-table-p provider-or-model))
    (error "runtime-get-auth takes a provider id or a model object"))
  (graph-invoke-async (gobj-ctx runtime) (gobj-handle runtime) "getAuth" provider-or-model overrides))

(defun runtime-refresh (runtime &optional options)
  "{\"aborted\", \"errors\"}; OPTIONS: allowNetwork, providers, force."
  (graph-invoke-async (gobj-ctx runtime) (gobj-handle runtime) "refresh" (or options (obj))))

(defun runtime-login (runtime provider kind prompt &key notify agent-name get-device-id)
  "Authenticate PROVIDER with the auth method KIND (\"api_key\" or \"oauth\"); PROMPT, (lambda (prompt)), answers the login prompts and NOTIFY receives events. Returns Pi's Credential."
  (let* ((ctx (gobj-ctx runtime))
         (interaction (obj "prompt" (graph-callback-id ctx prompt) "notify" (graph-callback-id ctx notify)))
         (options (and (or agent-name get-device-id)
                       (obj "agentName" (or agent-name :null) "getDeviceId" (or (graph-callback-id ctx get-device-id) :null)))))
    (graph-invoke-async ctx (gobj-handle runtime) "login" provider kind interaction options)))

(defun mrt-routed (ctx model route)
  "The host's callback for a virtual model's ROUTE: it must answer a model; a nil state keeps the current one."
  (lambda (request)
    (let ((result (funcall route ctx request)))
      (unless (and (hash-table-p result) (jget result "model") (not (eq (jget result "model") :null)))
        (error "virtual model ~A/~A routed to no model" (virtual-model-provider model) (virtual-model-id model)))
      (if (member (jget result "state") '(nil :null))
          (let ((copy (obj)))
            (maphash (lambda (k v) (unless (equal k "state") (setf (gethash k copy) v))) result)
            copy)
          result))))

(defun runtime-register-virtual-model (runtime model)
  "Register a virtual-model; its route, (lambda (ctx request)), runs in this process when a request resolves the model."
  (let ((ctx (gobj-ctx runtime)))
    (graph-invoke ctx (gobj-handle runtime) "registerVirtualModel"
                  (obj "provider" (virtual-model-provider model) "id" (virtual-model-id model) "name" (virtual-model-name model)
                       "thinkingLevels" (coerce (virtual-model-thinking-levels model) 'simple-vector)
                       "contextWindow" (virtual-model-context-window model) "maxTokens" (virtual-model-max-tokens model)
                       "input" (coerce (virtual-model-input model) 'simple-vector)
                       "route" (graph-callback-id ctx (mrt-routed ctx model (virtual-model-route model)))))))

(defun mrt-never-fails (runtime method model field empty context options)
  "Pi reports every failure of classify and generateImages as a result."
  (handler-case (graph-invoke-async (gobj-ctx runtime) (gobj-handle runtime) method model context options)
    (error (e)
      (obj "api" (jget model "api") "provider" (jget model "provider") "model" (jget model "id") field empty
           "stopReason" (if (ctx-cancelled-p (gobj-ctx runtime)) "aborted" "error")
           "errorMessage" (princ-to-string e) "timestamp" (mrt-now-ms)))))

(defun runtime-classify (runtime model context &optional options)
  "Never signals: a failure is a result with stopReason error or aborted."
  (mrt-never-fails runtime "classify" model "answers" (obj) context options))

(defun runtime-generate-images (runtime model context &optional options)
  "Never signals: a failure is a result with stopReason error or aborted."
  (mrt-never-fails runtime "generateImages" model "output" (vector) context options))

(defun runtime-stream (runtime model request &optional options)
  "Stream a model response through the runtime; the stream is a model-stream."
  (model-stream-start (gobj-ctx runtime) model request options nil (gobj-handle runtime)))

(defun runtime-stream-simple (runtime model request &optional options)
  (model-stream-start (gobj-ctx runtime) model request options t (gobj-handle runtime)))

(defun runtime-complete (runtime model request &optional options)
  (model-stream-result (runtime-stream runtime model request options)))

(defun runtime-complete-simple (runtime model request &optional options)
  (model-stream-result (runtime-stream-simple runtime model request options)))

(defun runtime-register-native-provider (runtime provider)
  "The provider's catalog, authentication and API run in this process, for this runtime."
  (graph-invoke (gobj-ctx runtime) (gobj-handle runtime) "registerNativeProvider"
                (provider-declare-native (ctx-extension (gobj-ctx runtime)) provider)))

(defun mrt-provider (runtime declaration)
  (and declaration (provider-from-declaration (gobj-ctx runtime) declaration)))

(defun runtime-get-registered-native-provider (runtime provider-name)
  (mrt-provider runtime (graph-invoke (gobj-ctx runtime) (gobj-handle runtime) "getRegisteredNativeProvider" provider-name)))

(defun runtime-get-provider (runtime provider)
  "The Provider object of PROVIDER, NIL when the runtime has none by that id. A builtin or composed provider signals the D78 error."
  (mrt-provider runtime (graph-invoke (gobj-ctx runtime) (gobj-handle runtime) "getProvider" provider)))

(defun runtime-get-providers (runtime)
  (map 'list (lambda (declaration) (mrt-provider runtime declaration))
       (graph-invoke (gobj-ctx runtime) (gobj-handle runtime) "getProviders")))

(defun runtime-provider-display-name (runtime provider)
  (or (graph-invoke (gobj-ctx runtime) (gobj-handle runtime) "providerName" provider) provider))

;;; ModelRegistry(runtime) --------------------------------------------------------

(defun mrt-reg (registry) (runtime-registry-runtime registry))

(defmacro def-registry-forwards (&rest specs)
  "Each spec is (name (parameters...) runtime-function): the registry member that forwards to the runtime."
  `(progn ,@(loop for (name params target) in specs
                  collect (let ((vars (loop for p in params unless (member p lambda-list-keywords) collect (if (consp p) (car p) p))))
                            `(defun ,name (registry ,@params) (,target (mrt-reg registry) ,@vars))))))

(def-registry-forwards
  (runtime-registry-refresh (&optional options) runtime-refresh)
  (runtime-registry-get-error () runtime-get-error)
  (runtime-registry-get-all () runtime-get-models)
  (runtime-registry-get-available () runtime-get-available-snapshot)
  (runtime-registry-find-of-type (model-type provider model-id) runtime-get-model-of-type)
  (runtime-registry-get-provider-auth-status (provider) runtime-get-provider-auth-status)
  (runtime-registry-get-provider (provider) runtime-get-provider)
  (runtime-registry-stream (model request &optional options) runtime-stream)
  (runtime-registry-stream-simple (model request &optional options) runtime-stream-simple)
  (runtime-registry-complete (model request &optional options) runtime-complete)
  (runtime-registry-get-models-of-type (model-type &optional provider) runtime-get-models-of-type)
  (runtime-registry-get-available-of-type (model-type &optional provider) runtime-get-available-of-type)
  (runtime-registry-get-model-of-type (model-type provider model-id) runtime-get-model-of-type)
  (runtime-registry-classify (model context &optional options) runtime-classify)
  (runtime-registry-generate-images (model context &optional options) runtime-generate-images)
  (runtime-registry-get-provider-display-name (provider) runtime-provider-display-name)
  (runtime-registry-get-provider-auth (provider) runtime-get-auth)
  (runtime-registry-register-native-provider (provider) runtime-register-native-provider)
  (runtime-registry-unregister-provider (name) runtime-unregister-provider)
  (runtime-registry-register-virtual-model (model) runtime-register-virtual-model)
  (runtime-registry-unregister-virtual-model (provider model-id) runtime-unregister-virtual-model)
  (runtime-registry-get-registered-provider-config (provider) runtime-get-registered-provider-config)
  (runtime-registry-get-registered-native-provider (provider) runtime-get-registered-native-provider)
  (runtime-registry-get-registered-provider-ids () runtime-get-registered-provider-ids))

(defun runtime-registry-find (registry provider-id model-id)
  (let ((model (runtime-get-model (mrt-reg registry) provider-id model-id)))
    (and (hash-table-p model) model)))

(defun runtime-registry-has-configured-auth-p (registry model)
  (runtime-has-configured-auth-p (mrt-reg registry) (jget model "provider")))

(defun runtime-registry-is-using-oauth-p (registry model)
  (runtime-is-using-oauth-p (mrt-reg registry) (jget model "provider")))

(defun runtime-registry-get-api-key-and-headers (registry model)
  "Pi's getApiKeyAndHeaders over the runtime: {\"ok\": true, \"apiKey\"?, \"headers\"?, \"baseUrl\"?, \"env\"?} or {\"ok\": false, \"error\"}; it never signals."
  (let ((runtime (mrt-reg registry)) (provider (jget model "provider")))
    (flet ((failed (message)
             (obj "ok" :false "error" (if (equal message "authHeader requires a resolved API key")
                                          (format nil "No API key found for ~S" provider)
                                          message))))
      (handler-case
          (let ((resolution (runtime-get-auth runtime model)) (out (obj "ok" t)))
            (if (null resolution)
                (let ((compat (runtime-get-compatibility-request-config runtime model)))
                  (if (member (jget compat "authHeader") '(nil :false :null))
                      (progn (when (jget compat "headers") (setf (gethash "headers" out) (jget compat "headers"))) out)
                      (failed (format nil "No API key found for ~S" provider))))
                (let ((auth (or (jget resolution "auth") (obj))))
                  (dolist (key '("apiKey" "headers"))
                    (let ((v (jget auth key))) (when (and v (not (eq v :null))) (setf (gethash key out) v))))
                  (let ((base (jget auth "baseUrl"))) (when (and (stringp base) (plusp (length base))) (setf (gethash "baseUrl" out) base)))
                  (let ((env (jget resolution "env"))) (when (and env (not (eq env :null))) (setf (gethash "env" out) env)))
                  out)))
        (error (e) (failed (princ-to-string e)))))))

(defun runtime-registry-get-api-key-for-provider (registry provider)
  "The API key of PROVIDER's auth, NIL when there is none or resolving it fails."
  (handler-case (let ((key (jref (runtime-get-auth (mrt-reg registry) provider) "auth" "apiKey"))) (and (stringp key) key))
    (error () nil)))

(defun runtime-registry-register-provider (registry name &optional config)
  "Register a provider by NAME and a configuration without functions, or a native Provider object."
  (cond ((stringp name)
         (unless config (error "Provider config is required when registering by name"))
         (runtime-register-provider (mrt-reg registry) name config))
        (t (runtime-register-native-provider (mrt-reg registry) name))))
