;;;; SPDX-License-Identifier: MIT
;;;; Providers: provider configs, native Provider objects, and the proxies of Providers other extensions registered.

(in-package #:pig)

;;; ---------------------------------------------------------------- the types a Provider is made of

(defstruct auth-context "Request-time environment: ENV is (lambda (name)) and FILE-EXISTS (lambda (path))." env file-exists)
(defstruct api-key-auth-input ctx credential signal)
(defstruct auth-result "AUTH is the request auth hash-table; ENV and SOURCE are optional." auth env source)
(defstruct auth-check type source)
(defstruct auth-interaction "PROMPT is (lambda (prompt)) -> string and NOTIFY (lambda (event))." prompt notify signal)
(defstruct login-options "GET-DEVICE-ID is a function without arguments, or NIL when the app supplies no ID." get-device-id agent-name)
(defstruct api-key-auth name resolve check login)
(defstruct oauth-auth "IS-SUBSCRIPTION is T, :FALSE or NIL (absent)." name login refresh to-auth is-subscription login-label)
(defstruct provider-auth api-key oauth)
(defstruct provider-stream-options values signal on-payload on-response on-provider-stream-event)
(defstruct provider-operation-options values signal)
(defstruct models-publication "PERSIST is :OMITTED when the publication has no persist member." (persist :omitted) update)
(defstruct refresh-models-context publish credential stored allow-network force signal base-url)
(defstruct provider
  "A Pi Provider. Its functions take what Python's do: STREAM and STREAM-SIMPLE (model context options) return a model-event-stream."
  id name auth get-models stream stream-simple base-url headers filter-models get-all-models filter-all-models
  refresh-models fetch-deferred cancel-deferred generate-images classify
  (handle nil))

;;; ---------------------------------------------------------------- state

(defstruct (prov-state (:conc-name ps-))
  (lock (sb-thread:make-mutex :name "pig provider"))
  (native (make-hash-table :test 'equal))      ; key -> Provider object
  (callbacks (make-hash-table :test 'equal))   ; callback id -> method -> function
  (updates (make-hash-table :test 'equal))     ; publication token -> function
  (streams (make-hash-table :test 'equal))     ; provider name -> streamSimple function
  (operations (make-hash-table :test 'equal))  ; provider name -> kind -> api -> function
  (remote (make-hash-table :test 'equal))      ; provider id -> proxy
  (oauth (make-hash-table :test 'equal))       ; provider name -> oauth-provider
  (queued '())                                 ; declarations of the register payload, in order
  (forwarding '())
  (stopped nil))

(defun prov-of (ext) (extension-extra ext :provider #'make-prov-state))

(defmacro with-prov ((ps) &body body) `(sb-thread:with-recursive-lock ((ps-lock ,ps)) ,@body))

(defvar *prov-random* (make-random-state t))
(defvar *prov-random-lock* (sb-thread:make-mutex :name "pig uuid"))

(defun prov-uuid ()
  (sb-thread:with-mutex (*prov-random-lock*) (format nil "~(~32,'0x~)" (random (expt 2 128) *prov-random*))))

(defun prov-vec (items) (cond ((null items) (vector)) ((listp items) (coerce items 'simple-vector)) (t items)))
(defun prov-object (value) (if (hash-table-p value) value (obj)))
(defun prov-bool (value) (if value t :false))

(defun prov-copy-table (table)
  (let ((copy (make-hash-table :test 'equal)))
    (maphash (lambda (k v) (setf (gethash k copy) v)) table)
    copy))

(defun prov-api-key (provider) (and (provider-auth provider) (provider-auth-api-key (provider-auth provider))))
(defun prov-oauth (provider) (and (provider-auth provider) (provider-auth-oauth (provider-auth provider))))

(defparameter *prov-methods*
  (list (cons "getModels" #'provider-get-models) (cons "getAllModels" #'provider-get-all-models)
        (cons "filterModels" #'provider-filter-models) (cons "filterAllModels" #'provider-filter-all-models)
        (cons "refreshModels" #'provider-refresh-models) (cons "stream" #'provider-stream)
        (cons "streamSimple" #'provider-stream-simple) (cons "fetchDeferred" #'provider-fetch-deferred)
        (cons "cancelDeferred" #'provider-cancel-deferred) (cons "generateImages" #'provider-generate-images)
        (cons "classify" #'provider-classify)
        (cons "auth.apiKey.check" (lambda (p) (let ((a (prov-api-key p))) (and a (api-key-auth-check a)))))
        (cons "auth.apiKey.resolve" (lambda (p) (let ((a (prov-api-key p))) (and a (api-key-auth-resolve a)))))
        (cons "auth.apiKey.login" (lambda (p) (let ((a (prov-api-key p))) (and a (api-key-auth-login a)))))
        (cons "auth.oauth.login" (lambda (p) (let ((a (prov-oauth p))) (and a (oauth-auth-login a)))))
        (cons "auth.oauth.refresh" (lambda (p) (let ((a (prov-oauth p))) (and a (oauth-auth-refresh a)))))
        (cons "auth.oauth.toAuth" (lambda (p) (let ((a (prov-oauth p))) (and a (oauth-auth-to-auth a))))))
  "The wire methods of a Provider, in declaration order, with the function that reads each from a Provider.")

(defun prov-method (provider method)
  (let ((reader (cdr (assoc method *prov-methods* :test #'string=)))) (and reader (funcall reader provider))))

;;; ---------------------------------------------------------------- declarations

(defun prov-declaration (provider key)
  "The declaration of a Provider object that the host receives, under KEY."
  (unless (and (stringp (provider-id provider))
               (plusp (length (string-trim '(#\Space #\Tab #\Newline #\Return) (provider-id provider)))))
    (error "Provider id must not be empty"))
  (let* ((models (handler-case (let ((fn (or (provider-get-all-models provider) (provider-get-models provider))))
                                 (and fn (funcall fn)))
                   ;; Pi Models treats a throwing getModels as an empty catalog at registration.
                   (error () nil)))
         (auth (obj))
         (decl (obj "id" (provider-id provider) "key" key "name" (provider-name provider) "models" (prov-vec models) "auth" auth
                    "methods" (coerce (loop for (method) in *prov-methods* when (prov-method provider method) collect method)
                                      'simple-vector)))
         (api-key (prov-api-key provider)) (oauth (prov-oauth provider)))
    (when (provider-base-url provider) (setf (gethash "baseUrl" decl) (provider-base-url provider)))
    (when (provider-headers provider) (setf (gethash "headers" decl) (provider-headers provider)))
    (when api-key (setf (gethash "apiKey" auth) (obj "name" (api-key-auth-name api-key))))
    (when oauth
      (let ((entry (obj "name" (oauth-auth-name oauth))) (subscription (oauth-auth-is-subscription oauth)))
        (when subscription (setf (gethash "isSubscription" entry) (prov-bool (eq subscription t))))
        (when (oauth-auth-login-label oauth) (setf (gethash "loginLabel" entry) (oauth-auth-login-label oauth)))
        (setf (gethash "oauth" auth) entry
              (gethash "oauth" decl) (obj "name" (oauth-auth-name oauth) "isSubscription" (prov-bool (eq subscription t))
                                          "has_login" t "has_refresh" t "has_get_api_key" t))))
    decl))

(defun provider-declare-native (ext provider)
  "Keep PROVIDER's callbacks in EXT under a fresh key and return the declaration that names them."
  (let* ((key (prov-uuid)) (decl (prov-declaration provider key)) (ps (prov-of ext)))
    (with-prov (ps) (setf (gethash key (ps-native ps)) provider))
    decl))

(defun prov-forget-native (ext key)
  (let ((ps (prov-of ext))) (with-prov (ps) (remhash key (ps-native ps)))))

(defun prov-restore (ps name kept)
  "Give provider NAME back the stream function and operations KEPT (as (stream . operations)) holds. The caller holds the lock."
  (destructuring-bind (stream . operations) kept
    (if stream (setf (gethash name (ps-streams ps)) stream) (remhash name (ps-streams ps)))
    (if operations (setf (gethash name (ps-operations ps)) operations) (remhash name (ps-operations ps)))))

(defun prov-held (ps name)
  (cons (gethash name (ps-streams ps))
        (let ((operations (gethash name (ps-operations ps))))
          (and operations (let ((copy (make-hash-table :test 'equal)))
                            (maphash (lambda (kind apis) (setf (gethash kind copy) (prov-copy-table apis))) operations)
                            copy)))))

(defun prov-take-declaration (ps name config)
  "Keep the functions of a provider config in PS and return the declaration the host receives. The caller holds the lock."
  (let* ((config (prov-copy-table (prov-object config)))
         (callback (gethash "streamSimple" config))
         (operations (make-hash-table :test 'equal)))
    (remhash "streamSimple" config)
    (when (and callback (not (functionp callback))) (error "streamSimple must be callable"))
    (dolist (kind '("images" "classifiers"))
      (let ((implementations (gethash kind config)))
        (remhash kind config)
        (when (hash-table-p implementations)
          (maphash (lambda (api implementation)
                     (unless (functionp implementation)
                       (error "provider ~A implementation for ~A must be callable" kind api)))
                   implementations)
          (setf (gethash kind operations) (prov-copy-table implementations)))))
    (ensure-jsonable config (format nil "provider config for ~A" name))
    (when callback (setf (gethash name (ps-streams ps)) callback))
    ;; A re-registration replaces only the kinds it defines, as registerProvider merges defined values.
    (let ((kept (or (gethash name (ps-operations ps)) (setf (gethash name (ps-operations ps)) (make-hash-table :test 'equal)))))
      (maphash (lambda (kind implementations) (when (plusp (hash-table-count implementations)) (setf (gethash kind kept) implementations)))
               operations)
      (when (zerop (hash-table-count kept)) (remhash name (ps-operations ps))))
    (let ((decl (obj "name" name "config" config)))
      (when callback (setf (gethash "stream_simple" decl) t))
      (flet ((apis (kind) (sort (loop for api being the hash-keys of (or (gethash kind operations) (obj)) collect api) #'string<)))
        (when (plusp (length (apis "images"))) (setf (gethash "image_apis" decl) (prov-vec (apis "images"))))
        (when (plusp (length (apis "classifiers"))) (setf (gethash "classifier_apis" decl) (prov-vec (apis "classifiers")))))
      decl)))

(defun prov-drop-queued (ps name)
  "Forget the queued registration of provider NAME and the functions held for it. The caller holds the lock."
  (remhash name (ps-streams ps))
  (remhash name (ps-operations ps))
  (setf (ps-queued ps) (remove-if (lambda (d) (equal (jget d "name") name)) (ps-queued ps))))

;;; ---------------------------------------------------------------- registration

(defun prov-call (ext call method args)
  "Call METHOD on the host as CALL (a function (method args), the context's way of calling) or, without one, as the extension."
  (if call (funcall call method args) (host-call ext method args)))

(defun prov-ctx-call (ctx) (lambda (method args) (ctx-call-host ctx method args)))

(defun prov-register-now (ext name config call)
  "Send a provider registration through the host. A registration the host refuses leaves the functions the provider had."
  (let* ((ps (prov-of ext)) kept declaration)
    (with-prov (ps)
      (setf kept (prov-held ps name) declaration (prov-take-declaration ps name config)))
    (handler-case (prov-call ext call "registerProvider" declaration)
      (error (e)
        (with-prov (ps) (prov-restore ps name kept))
        (error e)))))

(defun prov-queue-or (ext queue now)
  "Run QUEUE under the registration lock while the extension is loading, else NOW (outside it): the registration the host accepts."
  (when (sb-thread:with-mutex ((extension-reg-lock ext))
          (cond ((extension-live ext) t)
                (t (funcall queue) nil)))
    (funcall now)))

(defun prov-register-native (ext provider call)
  (unless (provider-p provider) (error "register-native-provider requires a provider"))
  (let* ((ps (prov-of ext)) (declaration (provider-declare-native ext provider))
         (entry (obj "name" (provider-id provider) "config" (obj) "native" declaration)))
    (handler-case
        (prov-queue-or ext
                       (lambda () (with-prov (ps) (prov-drop-queued ps (provider-id provider))
                                    (setf (ps-queued ps) (append (ps-queued ps) (list entry)))))
                       (lambda () (prov-call ext call "registerProvider" entry)))
      (error (e) (prov-forget-native ext (gethash "key" declaration)) (error e)))
    nil))

(defun prov-register (ext name config call)
  (if (provider-p name)
      (prov-register-native ext name call)
      (let ((ps (prov-of ext)))
        (prov-queue-or ext
                       (lambda () (with-prov (ps) (setf (ps-queued ps) (append (ps-queued ps) (list (prov-take-declaration ps name config))))))
                       (lambda () (prov-register-now ext name config call)))
        nil)))

(defun prov-unregister (ext name call)
  (let ((ps (prov-of ext)))
    (prov-queue-or ext
                   (lambda () (with-prov (ps) (prov-drop-queued ps name)))
                   (lambda ()
                     (prov-call ext call "unregisterProvider" (obj "name" name))
                     (with-prov (ps) (prov-restore ps name (cons nil nil)))
                     (prov-forget-oauth ext name)))
    nil))

(defun register-native-provider (ext provider)
  "Register a Provider object: queued while the factory runs, else applied at once (Pi's registerProvider(provider))."
  (prov-register-native ext provider nil))

(defun register-provider (ext name &optional config)
  "Register or override a model provider by NAME and CONFIG (a hash-table: its \"streamSimple\" is a function
(ctx model context options) -> model-event-stream, \"images\" and \"classifiers\" map an API to a function (model context options)
-> result). NAME may also be a Provider object. Queued while the factory runs, applied through the host afterwards: a registration
the host refuses signals."
  (prov-register ext name config nil))

(defun unregister-provider (ext name)
  "Remove a provider registration: queued ones are dropped while the factory runs, else the host removes the provider and the
extension drops the functions it held for it."
  (prov-unregister ext name nil))

(defun ctx-register-provider (ctx name &optional config) (prov-register (ctx-extension ctx) name config (prov-ctx-call ctx)))
(defun ctx-unregister-provider (ctx name) (prov-unregister (ctx-extension ctx) name (prov-ctx-call ctx)))
(defun ctx-register-native-provider (ctx provider) (prov-register-native (ctx-extension ctx) provider (prov-ctx-call ctx)))

(declaim (ftype function prov-forget-oauth))

(def-register-contributor "providers" (ext payload)
  (let ((ps (prov-of ext)))
    (setf (gethash "providers" payload) (with-prov (ps) (prov-vec (ps-queued ps))))))

;;; ---------------------------------------------------------------- the host calling a Provider object

(defun prov-signal-for (signal aborted)
  (if aborted (let ((fresh (make-abort-signal))) (signal-abort fresh) fresh) signal))

(defun prov-auth-input (callback credential signal)
  (make-api-key-auth-input
   :ctx (make-auth-context :env (lambda (name) (let ((v (funcall callback "env" (obj "name" name)))) (and (stringp v) v)))
                           :file-exists (lambda (path) (eq (funcall callback "fileExists" (obj "path" path)) t)))
   :credential (let ((c credential)) (and (hash-table-p c) c))
   :signal signal))

(defun prov-auth-struct-wire (result)
  (etypecase result
    (null nil)
    (hash-table result)
    (auth-result (let ((wire (obj "auth" (auth-result-auth result))))
                   (when (auth-result-env result) (setf (gethash "env" wire) (auth-result-env result)))
                   (when (auth-result-source result) (setf (gethash "source" wire) (auth-result-source result)))
                   wire))
    (auth-check (let ((wire (obj "type" (auth-check-type result))))
                  (when (auth-check-source result) (setf (gethash "source" wire) (auth-check-source result)))
                  wire))))

(defun prov-stream-options (params callback signal)
  (let ((options (make-provider-stream-options :values (prov-object (jget params "options")) :signal signal))
        (wired (let ((c (jget params "callbacks"))) (if (vectorp c) (coerce c 'list) '()))))
    (when (eq (jget params "aborted") t) (setf (provider-stream-options-signal options) (prov-signal-for signal t)))
    (flet ((forward (wire) (and (member wire wired :test #'equal)
                                (lambda (value &optional model)
                                  (funcall callback wire (obj "value" (or value :null) "model" (or model :null)))))))
      (setf (provider-stream-options-on-payload options) (forward "onPayload")
            (provider-stream-options-on-response options) (forward "onResponse")
            (provider-stream-options-on-provider-stream-event options) (forward "onProviderStreamEvent")))
    options))

(defun prov-forward-stream (ext ctx stream)
  "Notify every event of STREAM to the host until it ends; the connection ending stops the wait."
  (let ((ps (prov-of ext)) (id (ctx-request-id ctx)))
    (with-prov (ps) (push stream (ps-forwarding ps)))
    (unwind-protect
         (model-stream-forward stream (lambda (event) (notify-host ext "tool_update" (obj "request_id" id "result" event)))
                               :stopped (lambda () (ps-stopped ps)))
      (with-prov (ps) (setf (ps-forwarding ps) (remove stream (ps-forwarding ps)))))))

(def-stop-hook "providers" (ext)
  (let ((ps (prov-of ext)))
    (with-prov (ps) (setf (ps-stopped ps) t) (dolist (stream (ps-forwarding ps)) (model-stream-wake stream)))))

(defun prov-dispatch (ext ctx req)
  (let* ((ps (prov-of ext)) (args (prov-object (jget req "args"))) (method (jget args "method"))
         (params (prov-object (jget args "params"))) (key (jget req "tool")))
    (when (equal method "update")
      (let ((update (with-prov (ps) (gethash (jget params "token") (ps-updates ps)))))
        (unless update (error "Provider publication is no longer active"))
        (return-from prov-dispatch (funcall update))))
    (let ((provider (with-prov (ps) (gethash key (ps-native ps)))))
      (unless provider (error "Provider object is no longer registered"))
      (let* ((callback (lambda (m p) (call-result ctx "provider.callback" (obj "provider" key "method" m "params" p))))
             (fn (prov-method provider method))
             (signal (ctx-cancel ctx)))
        (unless fn (error "Provider method ~A is absent" method))
        (cond
          ((member method '("getModels" "getAllModels") :test #'equal) (prov-vec (funcall fn)))
          ((member method '("filterModels" "filterAllModels") :test #'equal)
           (let* ((models (jget params "models"))
                  (result (prov-vec (funcall fn models (let ((c (jget params "credential"))) (and (hash-table-p c) c))))))
             (obj "models" result
                  "indices" (map 'simple-vector (lambda (model) (or (position model models :test #'eq) -1)) result))))
          ((member method '("auth.apiKey.check" "auth.apiKey.resolve") :test #'equal)
           (prov-auth-struct-wire (funcall fn (prov-auth-input callback (jget params "credential") signal))))
          ((and (> (length method) 6) (string= method ".login" :start1 (- (length method) 6)))
           (let ((interaction (make-auth-interaction
                               :prompt (lambda (prompt) (funcall callback "prompt" (obj "prompt" prompt)))
                               :notify (lambda (event) (funcall callback "notify" (obj "event" event)) nil)
                               :signal signal)))
             (if (equal method "auth.apiKey.login")
                 (funcall fn interaction)
                 (let* ((wire (prov-object (jget params "options")))
                        (options (make-login-options :agent-name (let ((a (jget wire "agentName"))) (and (stringp a) a)))))
                   (when (eq (jget wire "getDeviceId") t)
                     (setf (login-options-get-device-id options) (lambda () (funcall callback "getDeviceId" (obj)))))
                   (funcall fn interaction options)))))
          ((equal method "auth.oauth.refresh") (funcall fn (jget params "credential") signal))
          ((equal method "auth.oauth.toAuth") (funcall fn (jget params "credential")))
          ((equal method "refreshModels")
           (funcall fn (make-refresh-models-context
                        :publish (lambda (publication)
                                   (let* ((token (prov-uuid))
                                          (update (models-publication-update publication))
                                          (value (if (eq (models-publication-persist publication) :omitted)
                                                     (obj)
                                                     (obj "persist" (models-publication-persist publication))))
                                          (wire (obj "publication" value)))
                                     (when update
                                       (with-prov (ps) (setf (gethash token (ps-updates ps)) update))
                                       (setf (gethash "token" wire) token))
                                     (unwind-protect (funcall callback "publish" wire)
                                       (with-prov (ps) (remhash token (ps-updates ps))))))
                        :credential (let ((c (jget params "credential"))) (and (hash-table-p c) c))
                        :stored (let ((c (jget params "stored"))) (and (hash-table-p c) c))
                        :allow-network (eq (jget params "allowNetwork") t)
                        :force (let ((f (jget params "force"))) (cond ((eq f t) t) ((eq f :false) :false)))
                        :signal signal
                        :base-url (let ((b (jget params "baseUrl"))) (and (stringp b) b)))))
          ((member method '("generateImages" "classify") :test #'equal)
           (funcall fn (jget params "model") (jget params "context")
                    (make-provider-operation-options :values (prov-object (jget params "options")) :signal signal)))
          (t
           (let ((options (prov-stream-options params callback signal)))
             (if (equal method "cancelDeferred")
                 (funcall fn (jget params "model") (jget params "handle") options)
                 (let ((stream (funcall fn (jget params "model")
                                        (if (equal method "fetchDeferred") (jget params "handle") (jget params "context"))
                                        options)))
                   (notify-host ext "tool_update" (obj "request_id" (ctx-request-id ctx) "result" (obj "type" "provider_started")))
                   (prov-forward-stream ext ctx stream)
                   nil)))))))))

(def-request "provider_call" (ext ctx req) (prov-dispatch ext ctx req))
(def-request "provider_stream" (ext ctx req) (prov-dispatch ext ctx req))
(def-request "provider_sync" (ext ctx req) (prov-dispatch ext ctx req))

(defun prov-dispatch-callback (ext req)
  (let* ((ps (prov-of ext)) (args (prov-object (jget req "args")))
         (callbacks (with-prov (ps) (gethash (jget req "tool") (ps-callbacks ps)))))
    (unless callbacks (error "Provider callback is no longer active"))
    (funcall (gethash (jget args "method") callbacks) (prov-object (jget args "params")))))

(def-request "provider_object_callback" (ext ctx req) (prov-dispatch-callback ext req))
(def-request "provider_object_callback_sync" (ext ctx req) (prov-dispatch-callback ext req))

(def-notify "provider_release" (ext args)
  (prov-forget-native ext (jget args "key")))

(def-request "provider_operation" (ext ctx req)
  (let* ((ps (prov-of ext)) (args (prov-object (jget req "args"))) (provider (jget req "tool" "")) (kind (jget args "kind"))
         (api (jget args "api")))
    (unless (member kind '("images" "classifiers") :test #'equal) (error "Unknown provider operation ~S" kind))
    (let ((implementation (with-prov (ps) (let ((kinds (gethash provider (ps-operations ps))))
                                            (and kinds (gethash api (or (gethash kind kinds) (obj))))))))
      (unless implementation
        (error "Provider ~A has no ~A implementation for ~A" provider (if (equal kind "images") "image" "classifier") (to-json api)))
      (funcall implementation (prov-object (jget args "model")) (prov-object (jget args "context"))
               (make-provider-operation-options :values (prov-object (jget args "options")) :signal (ctx-cancel ctx))))))

(def-request "provider_stream_simple" (ext ctx req)
  (let* ((ps (prov-of ext)) (args (prov-object (jget req "args")))
         (callback (with-prov (ps) (gethash (jget req "tool" "") (ps-streams ps)))) (signal (ctx-cancel ctx)))
    (unless callback (error "unknown provider stream ~S" (jget req "tool")))
    (let* ((stream (funcall callback ctx (jget args "model") (jget args "context") (jget args "options")))
           (unsubscribe (abort-signal-subscribe signal (lambda () (model-stream-end stream nil)))))
      (unwind-protect
           (progn
             (loop (multiple-value-bind (event more) (model-stream-next stream)
                     (unless more (return))
                     (when (abort-signal-set-p signal) (error "provider stream aborted"))
                     (notify-host ext "provider_stream_event" (obj "request_id" (ctx-request-id ctx) "result" event))))
             (when (abort-signal-set-p signal) (error "provider stream aborted"))
             (model-stream-result stream))
        (funcall unsubscribe)))))

;;; ---------------------------------------------------------------- Providers other extensions registered

(defun prov-cancel-remote (ext callback-id)
  (ignore-errors
   (send-frame (extension-conn ext)
               (obj "type" "call" "call" (obj "method" "cancelModelStream" "args" (obj "streamId" callback-id))))))

(defun prov-run (ext handle method params callbacks callback-id stream-id signal)
  "Make the provider.object call and wait for its answer. CALLBACKS (method -> function) serve the host's callbacks meanwhile,
and SIGNAL, once set, cancels the call (the cancel is sent after the call, so the host knows it)."
  (let ((ps (prov-of ext)) (unsubscribe nil))
    (with-prov (ps) (setf (gethash callback-id (ps-callbacks ps)) callbacks))
    (unwind-protect
         (let ((*host-call-sent* (lambda ()
                                   (when signal
                                     (setf unsubscribe (abort-signal-subscribe signal (lambda () (prov-cancel-remote ext callback-id))))))))
           (host-call ext "provider.object" (obj "handle" handle "method" method "params" params
                                                 "callbackId" callback-id "streamId" stream-id)))
      (when unsubscribe (funcall unsubscribe))
      (with-prov (ps) (remhash callback-id (ps-callbacks ps))))))

(defun prov-invoke (ext handle method &key params callbacks signal)
  (let ((id (prov-uuid)))
    (prov-run ext handle method (or params (obj)) (or callbacks (make-hash-table :test 'equal)) id "" signal)))

(defun prov-table (&rest members)
  (let ((table (make-hash-table :test 'equal)))
    (loop for (k v) on members by #'cddr do (setf (gethash k table) v))
    table))

(defun prov-remote-stream (ext handle method model transcript options)
  "Start the remote stream METHOD of the Provider at HANDLE; it returns once the host reports the stream started."
  (let* ((ps (prov-of ext)) (options (or options (make-provider-stream-options)))
         (stream (make-model-event-stream)) (id (prov-uuid)) (callbacks (make-hash-table :test 'equal)) (wired '()))
    (model-stream-register ext id stream)
    (loop for (wire fn) in (list (list "onPayload" (provider-stream-options-on-payload options))
                                 (list "onResponse" (provider-stream-options-on-response options))
                                 (list "onProviderStreamEvent" (provider-stream-options-on-provider-stream-event options)))
          do (when fn
               (push wire wired)
               (setf (gethash wire callbacks) (let ((fn fn)) (lambda (args) (funcall fn (jget args "value") model))))))
    (let* ((signal (provider-stream-options-signal options))
           (params (obj "model" model (if (equal method "fetchDeferred") "handle" "context") transcript
                        "options" (or (provider-stream-options-values options) (obj))
                        "callbacks" (prov-vec (reverse wired))
                        "aborted" (prov-bool (and signal (abort-signal-set-p signal))))))
      (start-owned-thread
       ext "pig-provider-stream"
       (lambda ()
         (let ((condition nil))
           (handler-case (progn (prov-run ext handle method params callbacks id id signal)
                                (setf condition (make-condition 'simple-error :format-control "Provider stream ended without a terminal event")))
             (error (e) (setf condition e)))
           (model-stream-mark-started stream condition)
           (model-stream-push stream (model-stream-error-event condition model))
           (model-stream-unregister ext id))))
      (model-stream-wait-started stream)
      stream)))

(defun start-owned-thread (ext name function)
  "Run FUNCTION on a thread the connection owns: it is joined, with the request threads, when the connection stops."
  (let ((thread (sb-thread:make-thread
                 (lambda ()
                   (unwind-protect (handler-case (funcall function)
                                     (error (e) (format *error-output* "~A: ~A failed: ~A~%" (extension-name ext) name e)))
                     (sb-thread:with-mutex ((extension-state-lock ext))
                       (setf (extension-request-threads ext) (remove sb-thread:*current-thread* (extension-request-threads ext))))))
                 :name name)))
    (sb-thread:with-mutex ((extension-state-lock ext)) (push thread (extension-request-threads ext)))
    thread))

(defun prov-remote-auth (ext handle decl methods)
  (let ((auth (make-provider-auth)) (declared (prov-object (jget decl "auth"))))
    (flet ((callbacks (input)
             (let ((ctx (api-key-auth-input-ctx input)))
               (prov-table "env" (lambda (args) (funcall (auth-context-env ctx) (jget args "name")))
                           "fileExists" (lambda (args) (prov-bool (funcall (auth-context-file-exists ctx) (jget args "path")))))))
           (interaction (input)
             (prov-table "prompt" (lambda (args) (funcall (auth-interaction-prompt input) (jget args "prompt")))
                         "notify" (lambda (args) (funcall (auth-interaction-notify input) (jget args "event")) nil))))
      (when (hash-table-p (jget declared "apiKey"))
        (flet ((call (method input)
                 (prov-invoke ext handle method :params (obj "credential" (or (api-key-auth-input-credential input) :null))
                                                :callbacks (callbacks input) :signal (api-key-auth-input-signal input))))
          (setf (provider-auth-api-key auth)
                (make-api-key-auth
                 :name (jget (jget declared "apiKey") "name")
                 :resolve (lambda (input)
                            (let ((r (call "auth.apiKey.resolve" input)))
                              (and (hash-table-p r) (make-auth-result :auth (jget r "auth") :env (jget r "env") :source (jget r "source")))))
                 :check (and (member "auth.apiKey.check" methods :test #'equal)
                             (lambda (input)
                               (let ((r (call "auth.apiKey.check" input)))
                                 (and (hash-table-p r) (make-auth-check :type (jget r "type") :source (jget r "source"))))))
                 :login (and (member "auth.apiKey.login" methods :test #'equal)
                             (lambda (interaction)
                               (prov-invoke ext handle "auth.apiKey.login" :callbacks (interaction interaction)
                                                                           :signal (auth-interaction-signal interaction))))))))
      (when (hash-table-p (jget declared "oauth"))
        (let ((data (jget declared "oauth")))
          (setf (provider-auth-oauth auth)
                (make-oauth-auth
                 :name (jget data "name")
                 :login (lambda (input &optional options)
                          (let ((wire (obj)) (callbacks (interaction input)))
                            (when (and options (login-options-get-device-id options))
                              (setf (gethash "getDeviceId" wire) t
                                    (gethash "getDeviceId" callbacks) (let ((get (login-options-get-device-id options))) (lambda (args) (declare (ignore args)) (funcall get)))))
                            (when (and options (login-options-agent-name options))
                              (setf (gethash "agentName" wire) (login-options-agent-name options)))
                            (prov-invoke ext handle "auth.oauth.login" :params (obj "options" wire) :callbacks callbacks
                                                                       :signal (auth-interaction-signal input))))
                 :refresh (lambda (credential signal)
                            (prov-invoke ext handle "auth.oauth.refresh" :params (obj "credential" credential) :signal signal))
                 :to-auth (lambda (credential) (prov-invoke ext handle "auth.oauth.toAuth" :params (obj "credential" credential)))
                 :is-subscription (let ((s (jget data "isSubscription"))) (and (member s '(t :false)) s))
                 :login-label (let ((l (jget data "loginLabel"))) (and (stringp l) l)))))))
    auth))

(defun prov-remote-provider (ext decl)
  (let* ((handle (jget decl "handle")) (methods (coerce (or (jget decl "methods") (vector)) 'list))
         (token (prov-uuid)))
    (host-call ext "provider.retain" (obj "handle" handle "token" token))
    (labels ((invoke (method &optional params signal) (prov-invoke ext handle method :params params :signal signal))
             (streaming (method)
               (lambda (model transcript &optional options) (prov-remote-stream ext handle method model transcript options)))
             (operation (method)
               (lambda (model request &optional options)
                 (invoke method (obj "model" model "context" request
                                     "options" (or (and options (provider-operation-options-values options)) (obj)))
                         (and options (provider-operation-options-signal options)))))
             (filtering (method)
               (lambda (models credential)
                 (let ((result (invoke method (obj "models" models "credential" (or credential :null)))))
                   (let ((out (copy-seq (jget result "models"))))
                     (loop for index across (jget result "indices") for i from 0
                           do (when (>= index 0)
                                (let ((model (aref models index)))
                                  (clrhash model)
                                  (maphash (lambda (k v) (setf (gethash k model) v)) (aref (jget result "models") i))
                                  (setf (aref out i) model))))
                     out)))))
      (let ((provider (make-provider
                       :id (jget decl "id") :name (jget decl "name") :handle handle
                       :auth (prov-remote-auth ext handle decl methods)
                       :get-models (lambda () (invoke "getModels"))
                       :stream (streaming "stream") :stream-simple (streaming "streamSimple")
                       :base-url (jget decl "baseUrl") :headers (jget decl "headers"))))
        (when (member "generateImages" methods :test #'equal) (setf (provider-generate-images provider) (operation "generateImages")))
        (when (member "classify" methods :test #'equal) (setf (provider-classify provider) (operation "classify")))
        (when (member "getAllModels" methods :test #'equal) (setf (provider-get-all-models provider) (lambda () (invoke "getAllModels"))))
        (when (member "filterModels" methods :test #'equal) (setf (provider-filter-models provider) (filtering "filterModels")))
        (when (member "filterAllModels" methods :test #'equal) (setf (provider-filter-all-models provider) (filtering "filterAllModels")))
        (when (member "refreshModels" methods :test #'equal)
          (setf (provider-refresh-models provider)
                (lambda (input)
                  (prov-invoke
                   ext handle "refreshModels"
                   :params (let ((params (obj "credential" (or (refresh-models-context-credential input) :null)
                                              "stored" (or (refresh-models-context-stored input) :null)
                                              "allowNetwork" (prov-bool (refresh-models-context-allow-network input))
                                              "force" (let ((f (refresh-models-context-force input))) (cond ((null f) :null) (t f))))))
                             (when (refresh-models-context-base-url input)
                               (setf (gethash "baseUrl" params) (refresh-models-context-base-url input)))
                             params)
                   :callbacks (prov-table "publish"
                                          (lambda (args)
                                            (let ((value (jget args "publication")) (token (jget args "token")))
                                              (prov-bool
                                               (funcall (refresh-models-context-publish input)
                                                        (make-models-publication
                                                         :persist (multiple-value-bind (v found) (gethash "persist" value) (if found v :omitted))
                                                         :update (and (stringp token) (lambda () (invoke "update" (obj "token" token))))))))))
                   :signal (refresh-models-context-signal input)))))
        (when (member "fetchDeferred" methods :test #'equal) (setf (provider-fetch-deferred provider) (streaming "fetchDeferred")))
        (when (member "cancelDeferred" methods :test #'equal)
          (setf (provider-cancel-deferred provider)
                (lambda (model handle-arg &optional options)
                  (invoke "cancelDeferred"
                          (obj "model" model "handle" handle-arg
                               "options" (or (and options (provider-stream-options-values options)) (obj)))
                          (and options (provider-stream-options-signal options)))
                  nil)))
        (let ((conn (extension-conn ext)))
          (sb-ext:finalize provider
                           (lambda () (ignore-errors
                                       (send-frame conn (obj "type" "call" "call" (obj "method" "provider.release"
                                                                                       "args" (obj "handle" handle "token" token))))))
                           :dont-save t))
        provider))))

(defun provider-from-declaration (ctx declaration)
  "The Provider object DECLARATION names: this extension's own object, else a proxy that calls the extension that registered it."
  (let* ((ext (ctx-extension ctx)) (ps (prov-of ext)))
    (with-prov (ps)
      (let ((local (gethash (jget declaration "key") (ps-native ps))))
        (or local
            (let* ((id (jget declaration "id")) (cached (gethash id (ps-remote ps))))
              (if (and cached (equal (provider-handle cached) (jget declaration "handle")))
                  cached
                  (setf (gethash id (ps-remote ps)) (prov-remote-provider ext declaration)))))))))
