;;;; SPDX-License-Identifier: MIT
;;;; OAuth capability of a model provider: login, token refresh and key resolution the host calls into, and the login callbacks that
;;;; drive the host's login UI. Credentials are JSON objects (hash-tables), so every provider-owned member survives each step.

(in-package #:pig)

(define-condition oauth-cancelled (pig-error) ()
  (:report "Login cancelled")
  (:documentation "Signalled by a login prompt the user dismissed. A flow that lets it propagate ends the login as cancelled."))

(defstruct oauth-auth-info url (instructions ""))
(defstruct oauth-device-code-info
  "OPEN-BROWSER-ON-CONFIRM and COPY-CODE-ON-CONFIRM are T, :FALSE or NIL (the client's default)."
  (user-code "") (verification-uri "") open-browser-on-confirm copy-code-on-confirm (interval-seconds 0) (expires-in-seconds 0))
(defstruct oauth-prompt (message "") (placeholder "") allow-empty)
(defstruct oauth-select-option (id "") (label ""))
(defstruct oauth-select-prompt (message "") (options '()))
(defstruct oauth-credential-status present (auth-type "") (source ""))
(defstruct oauth-credential-store
  "A provider's own credential persistence: CREDENTIAL-STATUS () -> oauth-credential-status, STORE-CREDENTIALS (credentials) -> path,
DELETE-CREDENTIALS () -> boolean."
  credential-status store-credentials delete-credentials)
(defstruct oauth-provider
  "An OAuth capability attached to a model provider; only the functions that are set are advertised. LOGIN is (lambda (callbacks)) ->
credentials, REFRESH-TOKEN (credentials) -> credentials, GET-API-KEY (credentials) -> string."
  (name "") login refresh-token get-api-key credential-store is-subscription
  ;; MODIFY-MODELS is Pi's legacy projection (oauth.modifyModels, types.ts:2034): (lambda (models credentials)) -> models, the provider's chat models as model objects and the stored credential.
  ;; USES-CALLBACK-SERVER is T for Pi's deprecated flag: canonical auth flows ignore it (types.ts:2026), so it changes nothing.
  modify-models uses-callback-server)
(defstruct (oauth-login-callbacks (:constructor make-oauth-login-callbacks (ext request-id))) ext request-id)

(defun oauth-credentials (&key (access "") (refresh "") (expires 0) project-id account-id scope extra)
  "An OAuth credentials object (JSON). EXTRA is a hash-table of provider-owned members; EXPIRES NIL leaves the property out."
  (let ((wire (make-hash-table :test 'equal)))
    (when extra (maphash (lambda (k v) (setf (gethash k wire) v)) extra))
    (remhash "expires" wire)
    (setf (gethash "refresh" wire) refresh (gethash "access" wire) access)
    (when expires (setf (gethash "expires" wire) expires))
    (loop for (key value) in (list (list "projectId" project-id) (list "accountId" account-id) (list "scope" scope))
          do (when (and value (plusp (length value))) (setf (gethash key wire) value)))
    wire))

;;; ---------------------------------------------------------------- login callbacks

(defun oauth-info-wire (info)
  (etypecase info
    (hash-table info)
    (oauth-auth-info (let ((wire (obj "url" (oauth-auth-info-url info))))
                       (when (plusp (length (oauth-auth-info-instructions info)))
                         (setf (gethash "instructions" wire) (oauth-auth-info-instructions info)))
                       wire))
    (oauth-device-code-info
     (let ((wire (obj "userCode" (oauth-device-code-info-user-code info)
                      "verificationUri" (oauth-device-code-info-verification-uri info)
                      "intervalSeconds" (oauth-device-code-info-interval-seconds info)
                      "expiresInSeconds" (oauth-device-code-info-expires-in-seconds info))))
       (loop for (key value) in (list (list "openBrowserOnConfirm" (oauth-device-code-info-open-browser-on-confirm info))
                                      (list "copyCodeOnConfirm" (oauth-device-code-info-copy-code-on-confirm info)))
             do (when value (setf (gethash key wire) value)))
       wire))
    (oauth-prompt (let ((wire (obj "message" (oauth-prompt-message info))))
                    (when (plusp (length (oauth-prompt-placeholder info))) (setf (gethash "placeholder" wire) (oauth-prompt-placeholder info)))
                    (when (oauth-prompt-allow-empty info) (setf (gethash "allowEmpty" wire) t))
                    wire))
    (oauth-select-prompt
     (obj "message" (oauth-select-prompt-message info)
          "options" (map 'simple-vector (lambda (o) (obj "id" (oauth-select-option-id o) "label" (oauth-select-option-label o)))
                         (oauth-select-prompt-options info))))))

(defun oauth-call (callbacks method args)
  (let ((ext (oauth-login-callbacks-ext callbacks)) (id (oauth-login-callbacks-request-id callbacks)))
    (host-call ext method args nil id)))

(defun oauth-progress (callbacks)
  (send-request-state (oauth-login-callbacks-ext callbacks) (oauth-login-callbacks-request-id callbacks) "progress"))

(defun oauth-notify-call (callbacks method args)
  (oauth-call callbacks method args)
  (oauth-progress callbacks)
  nil)

(defun oauth-on-auth (callbacks info) (oauth-notify-call callbacks "oauth.cb.onAuth" (oauth-info-wire info)))
(defun oauth-on-device-code (callbacks info) (oauth-notify-call callbacks "oauth.cb.onDeviceCode" (oauth-info-wire info)))
(defun oauth-on-progress (callbacks message) (oauth-notify-call callbacks "oauth.cb.onProgress" (obj "message" message)))

(defun oauth-input-call (callbacks method args)
  (let ((ext (oauth-login-callbacks-ext callbacks)) (id (oauth-login-callbacks-request-id callbacks)))
    (send-request-state ext id "blocked" "user")
    (let ((payload (prov-object (oauth-call callbacks method args))))
      (oauth-progress callbacks)
      (when (eq (jget payload "cancel") t) (error 'oauth-cancelled))
      (let ((value (jget payload "value" ""))) (if (eq value :null) "" value)))))

(defun oauth-on-prompt (callbacks prompt)
  "Ask the user for text; signals oauth-cancelled when the user dismissed the prompt."
  (oauth-input-call callbacks "oauth.cb.onPrompt" (oauth-info-wire prompt)))
(defun oauth-on-select (callbacks prompt)
  "Ask the user to pick one option; the id of the choice."
  (oauth-input-call callbacks "oauth.cb.onSelect" (oauth-info-wire prompt)))
(defun oauth-on-manual-code-input (callbacks)
  (oauth-input-call callbacks "oauth.cb.onManualCodeInput" nil))

;;; ---------------------------------------------------------------- registration

(defun prov-forget-oauth (ext name)
  (let ((ps (prov-of ext))) (with-prov (ps) (remhash name (ps-oauth ps)))))

(defun register-oauth-provider (ext name config provider)
  "Register the model provider NAME that also contributes the OAuth capability PROVIDER. CONFIG is a provider config as register-provider's: it may carry the functions of the same registration (\"streamSimple\", \"images\", \"classifiers\", \"refreshModels\"), because a name is one registration and the host keeps the last registration of a name."
  (let ((ps (prov-of ext))
        (descriptor (obj "name" (if (plusp (length (oauth-provider-name provider))) (oauth-provider-name provider) name)
                         "isSubscription" (prov-bool (eq (oauth-provider-is-subscription provider) t))
                         "has_login" (prov-bool (oauth-provider-login provider))
                         "has_refresh" (prov-bool (oauth-provider-refresh-token provider))
                         "has_get_api_key" (prov-bool (oauth-provider-get-api-key provider))))
        kept previous declaration)
    (when (oauth-provider-credential-store provider) (setf (gethash "has_credential_store" descriptor) t))
    (when (oauth-provider-modify-models provider) (setf (gethash "has_modify_models" descriptor) t))
    (when (eq (oauth-provider-uses-callback-server provider) t) (setf (gethash "usesCallbackServer" descriptor) t))
    (flet ((take ()
             ;; The caller holds the provider lock.
             (let ((decl (prov-take-declaration ps name config)))
               (setf (gethash "oauth" (gethash "config" decl)) descriptor)
               decl)))
      (prov-queue-or ext
                     (lambda () (with-prov (ps)
                                  (setf (ps-queued ps) (append (ps-queued ps) (list (take))))
                                  (setf (gethash name (ps-oauth ps)) provider)))
                     (lambda ()
                       ;; A refused registration keeps the OAuth provider and the functions held before.
                       (with-prov (ps)
                         (setf kept (prov-held ps name) previous (gethash name (ps-oauth ps)))
                         (setf declaration (take))
                         (setf (gethash name (ps-oauth ps)) provider))
                       (handler-case (prov-call ext nil "registerProvider" declaration)
                         (error (e)
                           (with-prov (ps)
                             (prov-restore ps name kept)
                             (if previous (setf (gethash name (ps-oauth ps)) previous) (remhash name (ps-oauth ps))))
                           (error e)))))
      nil)))

;;; ---------------------------------------------------------------- the host calling the provider

(defun oauth-provider-for (ext req)
  (let* ((ps (prov-of ext)) (name (jget req "tool" "")) (provider (with-prov (ps) (gethash name (ps-oauth ps)))))
    (unless provider (error "unknown oauth provider: ~A" name))
    provider))

(defun oauth-store-for (ext req)
  (or (oauth-provider-credential-store (oauth-provider-for ext req)) (error "provider has no credential store")))

(def-request "oauth_login" (ext ctx req)
  (let ((provider (oauth-provider-for ext req)))
    (unless (oauth-provider-login provider) (error "provider does not support login"))
    (funcall (oauth-provider-login provider) (make-oauth-login-callbacks ext (ctx-request-id ctx)))))

(def-request "oauth_refresh" (ext ctx req)
  (let ((provider (oauth-provider-for ext req)))
    (unless (oauth-provider-refresh-token provider) (error "provider does not support refresh"))
    (funcall (oauth-provider-refresh-token provider) (jget req "args"))))

(def-request "oauth_get_api_key" (ext ctx req)
  (let ((provider (oauth-provider-for ext req)))
    (obj "apiKey" (if (oauth-provider-get-api-key provider) (funcall (oauth-provider-get-api-key provider) (jget req "args")) ""))))
(def-request "oauth_modify_models" (ext ctx req)
  ;; types.ts:2034: the legacy projection of the provider's chat models by the stored credential; the models it returns replace them.
  (let ((provider (oauth-provider-for ext req)) (args (prov-object (jget req "args"))))
    (unless (oauth-provider-modify-models provider) (error "provider does not support modifyModels"))
    (obj "models" (prov-vec (funcall (oauth-provider-modify-models provider) (jget args "models") (jget args "credentials"))))))

(def-request "oauth_credential_status" (ext ctx req)
  (let ((status (funcall (oauth-credential-store-credential-status (oauth-store-for ext req)))))
    (obj "present" (prov-bool (oauth-credential-status-present status))
         "authType" (oauth-credential-status-auth-type status) "source" (oauth-credential-status-source status))))

(def-request "oauth_store_credentials" (ext ctx req)
  (obj "path" (funcall (oauth-credential-store-store-credentials (oauth-store-for ext req)) (jget req "args"))))

(def-request "oauth_delete_credentials" (ext ctx req)
  (obj "deleted" (prov-bool (funcall (oauth-credential-store-delete-credentials (oauth-store-for ext req))))))
