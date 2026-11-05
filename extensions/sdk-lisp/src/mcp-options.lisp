;;;; SPDX-License-Identifier: MIT
;;;; The function options of createMcpExtension (loadConfig, createTransport, credentials, openUrl, updateConfig). They stay in this process: the
;;;; factory names them to the host as session graph callbacks and the host runs them with JSON arguments.

(in-package #:pig)

(defstruct (mcp-auth-provider (:constructor make-mcp-auth-provider (ctx handle)))
  "Pi's AuthProvider of an HTTP server, held by the host for a transport: it supplies bearer tokens and refreshes them after a rejection."
  ctx handle)

(defun mcp-auth-token (provider)
  "The current access token, NIL for none."
  (graph-invoke (mcp-auth-provider-ctx provider) (mcp-auth-provider-handle provider) "token"))

(defun mcp-auth-on-unauthorized (provider status headers server-url &optional token)
  "The host refreshes the credentials after a response (STATUS, ordered HEADERS as (name value) lists) a request of the transport got; the transport then retries once."
  (let ((wire (obj "status" status "headers" (map 'simple-vector (lambda (h) (coerce h 'simple-vector)) headers) "serverUrl" server-url)))
    (when (and token (plusp (length token))) (setf (gethash "token" wire) token))
    (graph-invoke (mcp-auth-provider-ctx provider) (mcp-auth-provider-handle provider) "onUnauthorized" wire)
    nil))

;;; ---------------------------------------------------------------- transports

(defstruct (mcp-transport (:constructor make-mcp-transport (&key start send send-ordered close set-protocol-version)))
  "Pi's McpTransport: START, (SEND message signal) and CLOSE are functions; SEND-ORDERED, (lambda (message signal placed)), writes in the order the writes took their place
(PLACED is called once every later send is written after the message); SET-PROTOCOL-VERSION is told the negotiated version. SIGNAL is an abort-signal set once the sender no
longer waits for a reply. What the transport receives is passed to MCP-TRANSPORT-EMIT-MESSAGE, -EMIT-ERROR and -EMIT-CLOSE (at most once)."
  start send send-ordered close set-protocol-version
  (lock (sb-thread:make-mutex :name "pig mcp listeners"))
  (listeners (list (cons :message '()) (cons :error '()) (cons :close '())))
  (next 0)
  (close-emitted nil))

(defun mcp-add-listener (transport kind listener)
  (sb-thread:with-mutex ((mcp-transport-lock transport))
    (let ((key (incf (mcp-transport-next transport))))
      (setf (cdr (assoc kind (mcp-transport-listeners transport)))
            (append (cdr (assoc kind (mcp-transport-listeners transport))) (list (cons key listener))))
      (lambda ()
        (sb-thread:with-mutex ((mcp-transport-lock transport))
          (setf (cdr (assoc kind (mcp-transport-listeners transport)))
                (remove key (cdr (assoc kind (mcp-transport-listeners transport))) :key #'car)))))))

(defun mcp-transport-on-message (transport listener) "Call LISTENER with each JSON-RPC message; returns the remover." (mcp-add-listener transport :message listener))
(defun mcp-transport-on-error (transport listener) "Call LISTENER with each error that does not close the transport." (mcp-add-listener transport :error listener))
(defun mcp-transport-on-close (transport listener) "Call LISTENER once when the transport closes." (mcp-add-listener transport :close listener))

(defun mcp-emit (transport kind &rest arguments)
  (dolist (entry (sb-thread:with-mutex ((mcp-transport-lock transport)) (copy-list (cdr (assoc kind (mcp-transport-listeners transport))))))
    (apply (cdr entry) arguments)))

(defun mcp-transport-emit-message (transport message) (mcp-emit transport :message message))
(defun mcp-transport-emit-error (transport error) (mcp-emit transport :error error))
(defun mcp-transport-emit-close (transport)
  (when (sb-thread:with-mutex ((mcp-transport-lock transport))
          (prog1 (not (mcp-transport-close-emitted transport)) (setf (mcp-transport-close-emitted transport) t)))
    (mcp-emit transport :close)))

(defstruct (mcp-shim (:constructor %make-mcp-shim (ctx transport)))
  "Serves one transport to the host: its operations arrive as one callback and what the transport produces is queued for the host's long poll."
  ctx transport id
  (lock (sb-thread:make-mutex :name "pig mcp shim"))
  (queue (sb-thread:make-waitqueue))
  (events '()) (closed nil) (aborts '()))

(defun mcp-shim-push (shim event &optional closing)
  (sb-thread:with-mutex ((mcp-shim-lock shim))
    (unless (mcp-shim-closed shim)
      (setf (mcp-shim-events shim) (append (mcp-shim-events shim) (list event)))
      (when closing (setf (mcp-shim-closed shim) t)))
    (sb-thread:condition-broadcast (mcp-shim-queue shim))))

(defun mcp-shim-receive (shim)
  (sb-thread:with-mutex ((mcp-shim-lock shim))
    (loop while (null (mcp-shim-events shim))
          do (when (mcp-shim-closed shim) (return-from mcp-shim-receive (obj "events" (vector (obj "type" "close")))))
             (sb-thread:condition-wait (mcp-shim-queue shim) (mcp-shim-lock shim)))
    (let ((events (mcp-shim-events shim)))
      (setf (mcp-shim-events shim) '())
      (obj "events" (coerce events 'simple-vector)))))

(defun mcp-shim-send (shim seq message ordered)
  (let* ((transport (mcp-shim-transport shim))
         (signal (make-abort-signal))
         (placed-lock (sb-thread:make-mutex)) (placed-done nil))
    (sb-thread:with-mutex ((mcp-shim-lock shim)) (push (cons seq signal) (mcp-shim-aborts shim)))
    (flet ((placed ()
             (unless (sb-thread:with-mutex (placed-lock) (prog1 placed-done (setf placed-done t)))
               (when ordered (mcp-shim-push shim (obj "type" "placed" "seq" seq))))))
      (unwind-protect
           (if (and ordered (mcp-transport-send-ordered transport))
               (funcall (mcp-transport-send-ordered transport) message signal #'placed)
               (progn (placed) (funcall (mcp-transport-send transport) message signal)))
        (placed)
        (sb-thread:with-mutex ((mcp-shim-lock shim)) (setf (mcp-shim-aborts shim) (remove seq (mcp-shim-aborts shim) :key #'car)))
        (signal-abort signal)))
    nil))

(defun mcp-shim-serve (shim operation &rest args)
  (let ((transport (mcp-shim-transport shim)))
    (cond ((equal operation "start") (funcall (mcp-transport-start transport)) nil)
          ((equal operation "setProtocolVersion")
           (when (mcp-transport-set-protocol-version transport) (funcall (mcp-transport-set-protocol-version transport) (first args)))
           nil)
          ((equal operation "receive") (mcp-shim-receive shim))
          ((equal operation "send") (mcp-shim-send shim (first args) (second args) (graph-bool (third args))))
          ((equal operation "abort")
           (let ((entry (sb-thread:with-mutex ((mcp-shim-lock shim)) (assoc (first args) (mcp-shim-aborts shim)))))
             (when entry (signal-abort (cdr entry))))
           nil)
          ((equal operation "close")
           (unwind-protect (funcall (mcp-transport-close transport))
             (mcp-shim-push shim (obj "type" "close") t))
           nil)
          (t (error "an MCP transport has no operation ~A" operation)))))

(defun mcp-make-shim (ctx transport)
  (let ((shim (%make-mcp-shim ctx transport)))
    (setf (mcp-shim-id shim) (graph-callback-id ctx (lambda (operation &rest args) (apply #'mcp-shim-serve shim operation args))))
    (mcp-transport-on-message transport (lambda (message) (mcp-shim-push shim (obj "type" "message" "message" message))))
    (mcp-transport-on-error transport (lambda (error) (mcp-shim-push shim (obj "type" "error" "error" (princ-to-string error)))))
    (mcp-transport-on-close transport (lambda () (mcp-shim-push shim (obj "type" "close") t)))
    shim))

;;; ---------------------------------------------------------------- credentials

(defstruct (mcp-credential-store (:constructor make-mcp-credential-store (&key for-server tokens remove)))
  "What the MCP extension asks of its credentials option: (FOR-SERVER name server-url) returns an mcp-server-store, (TOKENS name server-url) the stored tokens or NIL,
(REMOVE name server-url) deletes the stored state and returns whether credentials were stored."
  for-server tokens remove)

(defstruct (mcp-server-store (:constructor make-mcp-server-store (&key load save with-refresh-lock)))
  "One server's store: (LOAD) the stored state or NIL, (SAVE state), (WITH-REFRESH-LOCK thunk) runs THUNK while no other process refreshes the server's tokens."
  load save with-refresh-lock)

(defstruct (mcp-hold (:constructor make-mcp-hold ()))
  (lock (sb-thread:make-mutex :name "pig mcp hold")) (queue (sb-thread:make-waitqueue))
  entered done unlock failure error thread)

(defstruct (mcp-credentials (:constructor make-mcp-credentials (store)))
  store (lock (sb-thread:make-mutex :name "pig mcp credentials")) (next 0) (stores (make-hash-table :test 'equal)) (holds (make-hash-table :test 'equal)))

(defun mcp-credentials-id (credentials prefix)
  (sb-thread:with-mutex ((mcp-credentials-lock credentials)) (format nil "~A~D" prefix (incf (mcp-credentials-next credentials)))))

(defun mcp-hold-wait (hold predicate)
  (sb-thread:with-mutex ((mcp-hold-lock hold))
    (loop until (funcall predicate hold) do (sb-thread:condition-wait (mcp-hold-queue hold) (mcp-hold-lock hold)))))

(defun mcp-hold-set (hold slot value)
  (sb-thread:with-mutex ((mcp-hold-lock hold))
    (ecase slot (:entered (setf (mcp-hold-entered hold) value)) (:done (setf (mcp-hold-done hold) value)) (:unlock (setf (mcp-hold-unlock hold) value)))
    (sb-thread:condition-broadcast (mcp-hold-queue hold))))

(defun mcp-acquire (credentials store-id store)
  (let ((hold (make-mcp-hold)))
    (setf (mcp-hold-thread hold)
          (sb-thread:make-thread
           (lambda ()
             (unwind-protect
                  (handler-case
                      (funcall (mcp-server-store-with-refresh-lock store)
                               (lambda ()
                                 (mcp-hold-set hold :entered t)
                                 (mcp-hold-wait hold #'mcp-hold-unlock)
                                 (when (mcp-hold-failure hold) (error "~A" (mcp-hold-failure hold)))
                                 nil))
                    (error (e) (setf (mcp-hold-error hold) e)))
               (mcp-hold-set hold :done t)))
           :name "pig-mcp-refresh-lock"))
    (mcp-hold-wait hold (lambda (h) (or (mcp-hold-entered h) (mcp-hold-done h))))
    (unless (mcp-hold-entered hold)
      (error (or (mcp-hold-error hold) "with-refresh-lock returned without running its function")))
    (let ((id (mcp-credentials-id credentials (format nil "~A." store-id))))
      (sb-thread:with-mutex ((mcp-credentials-lock credentials)) (setf (gethash id (mcp-credentials-holds credentials)) hold))
      (obj "lock" id))))

(defun mcp-release (credentials lock-id failure failed)
  (let ((hold (sb-thread:with-mutex ((mcp-credentials-lock credentials))
                (prog1 (gethash lock-id (mcp-credentials-holds credentials)) (remhash lock-id (mcp-credentials-holds credentials))))))
    (unless hold (error "unknown refresh lock ~A" lock-id))
    (setf (mcp-hold-failure hold) (and failed failure))
    (mcp-hold-set hold :unlock t)
    (mcp-hold-wait hold #'mcp-hold-done)
    (when (mcp-hold-error hold) (error (mcp-hold-error hold)))
    nil))

(defun mcp-credentials-serve (credentials operation &rest args)
  (let ((store (mcp-credentials-store credentials)))
    (cond
      ((equal operation "forServer")
       (let ((server-store (funcall (mcp-credential-store-for-server store) (first args) (second args))))
         (unless server-store (error "for-server returned no store"))
         (let ((id (mcp-credentials-id credentials "s")))
           (sb-thread:with-mutex ((mcp-credentials-lock credentials)) (setf (gethash id (mcp-credentials-stores credentials)) server-store))
           (obj "store" id))))
      ((equal operation "tokens") (funcall (mcp-credential-store-tokens store) (first args) (second args)))
      ((equal operation "remove") (if (funcall (mcp-credential-store-remove store) (first args) (second args)) t :false))
      (t (let ((server-store (sb-thread:with-mutex ((mcp-credentials-lock credentials))
                               (prog1 (gethash (first args) (mcp-credentials-stores credentials))
                                 (when (equal operation "release") (remhash (first args) (mcp-credentials-stores credentials)))))))
           (cond ((equal operation "release") nil)
                 ((null server-store) (error "unknown credential store ~A" (first args)))
                 ((equal operation "load") (funcall (mcp-server-store-load server-store)))
                 ((equal operation "save") (funcall (mcp-server-store-save server-store) (second args)) nil)
                 ((equal operation "lock") (mcp-acquire credentials (first args) server-store))
                 ((equal operation "unlock") (mcp-release credentials (second args) (third args) (graph-bool (fourth args))))
                 (t (error "a credential store has no operation ~A" operation))))))))

(defun mcp-register-functions (ctx functions options)
  "Name each function of the plist FUNCTIONS to the host in the OPTIONS hash-table."
  (flet ((callback (key wire function)
           (when (getf functions key) (setf (gethash wire options) (graph-callback-id ctx function)))))
    (let ((load-config (getf functions :load-config)) (open-url (getf functions :open-url))
          (update-config (getf functions :update-config)) (create-transport (getf functions :create-transport))
          (credentials (getf functions :credentials)))
      (callback :load-config "loadConfig" (lambda (context) (funcall load-config context)))
      (callback :open-url "openUrl" (lambda (url) (funcall open-url url)))
      (callback :update-config "updateConfig" (lambda (entry patch) (funcall update-config entry patch)))
      (callback :create-transport "createTransport"
                (lambda (entry cwd auth)
                  (let ((transport (funcall create-transport entry cwd (and (stringp auth) (plusp (length auth)) (make-mcp-auth-provider ctx auth)))))
                    (unless transport (error "create-transport returned no transport"))
                    (obj "transport" (mcp-shim-id (mcp-make-shim ctx transport))
                         "setsProtocolVersion" (if (mcp-transport-set-protocol-version transport) t :false)))))
      (when credentials
        (setf (gethash "credentials" options)
              (graph-callback-id ctx (let ((shim (make-mcp-credentials credentials)))
                                       (lambda (operation &rest args) (apply #'mcp-credentials-serve shim operation args)))))))))
