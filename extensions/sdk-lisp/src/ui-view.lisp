;;;; SPDX-License-Identifier: MIT
;;;; pig additive (D614): plugin UI mounts.
;;;;
;;;; An extension mounts a kit view in a slot the attached client draws, updates it, and answers the user's intents. The host
;;;; replicates what the client draws (CTX-VIEW-ABILITIES); with no client attached it is NIL and a mount signals
;;;; HOST-CALL-ERROR with code "unsupported". A failed operation signals HOST-CALL-ERROR whose HOST-CALL-ERROR-CODE is "invalid",
;;;; "unsupported", "limit", "stale", "terminal" or "not_found". The wire is the one the Go SDK speaks: the ui.view.mount, update,
;;;; hide, show, finish, remove and state calls, and the ui.view.intent request.

(in-package #:pig)

(defstruct (view-ref (:constructor view-ref (key &optional (epoch 0) (rev 0))))
  "One revision of one mount incarnation. Pass the ref the last operation returned to the next update."
  key epoch rev)

(defstruct (view-status (:constructor %view-status (mount state reason)))
  "The reply of every mount operation. STATE is \"live\", \"hidden\", \"finished\", \"removed\" or \"broken\"; REASON is \"\" or, for
broken, \"session_replaced\", \"owner_gone\" or \"shutdown\"."
  mount state reason)

(defun view-ref-wire (ref)
  (obj "key" (view-ref-key ref) "epoch" (view-ref-epoch ref) "rev" (view-ref-rev ref)))

(defun view-shape (type &key enum properties required items max-items)
  "A shape of the closed JSON subset an intent argument or answer is checked against: TYPE \"boolean\", \"string\", \"integer\",
\"number\", \"enum\" (ENUM, at most 32 strings), \"object\" (PROPERTIES, an alist of name and shape, at most 32; REQUIRED, names) or
\"array\" (ITEMS, MAX-ITEMS at most 64), nested to depth 4."
  (let ((shape (obj "type" type)))
    (when enum (setf (gethash "enum" shape) (coerce enum 'simple-vector)))
    (when properties
      (let ((table (obj)))
        (loop for (name . value) in properties do (setf (gethash name table) value))
        (setf (gethash "properties" shape) table)))
    (when required (setf (gethash "required" shape) (coerce required 'simple-vector)))
    (when items (setf (gethash "items" shape) items))
    (when max-items (setf (gethash "maxItems" shape) max-items))
    shape))

(defun view-intent (name arg &key node-key answer cancellable timeout-ms)
  "One intent the view's nodes may raise. NAME is \"<extension>.<verb>\"; TIMEOUT-MS defaults to 30000 and is 100..300000."
  (let ((intent (obj "name" name "arg" arg)))
    (when node-key (setf (gethash "nodeKey" intent) node-key))
    (when answer (setf (gethash "answer" intent) answer))
    (when cancellable (setf (gethash "cancellable" intent) t))
    (when timeout-ms (setf (gethash "timeoutMs" intent) timeout-ms))
    intent))

(defun view-asset (id mime size sha256 delivery)
  "A bounded image a view refers to by id; DELIVERY is \"inline\" or \"managed\"."
  (obj "id" id "mime" mime "size" size "sha256" sha256 "delivery" delivery))

(defun view-target (&key tool-call-id message-type entry-type)
  "Ties a tool-card, message or entry mount to what it decorates; give exactly one."
  (let ((target (obj)))
    (when tool-call-id (setf (gethash "toolCallId" target) tool-call-id))
    (when message-type (setf (gethash "messageType" target) message-type))
    (when entry-type (setf (gethash "entryType" target) entry-type))
    target))

(defun ctx-view-abilities (ctx)
  "What the attached client draws, a JSON object with \"slots\", \"nodes\", \"markdown\", \"imageFormats\", \"limits\" (\"nodes\", \"depth\",
\"snapshotBytes\", \"textBytes\", \"mounts\", \"intentArgBytes\") and \"input\" (\"keyboard\", \"pointer\", \"text\"); NIL while no client is attached."
  (let ((ext (ctx-extension ctx)))
    (sb-thread:with-mutex ((extension-view-lock ext)) (extension-view-abilities ext))))

(defun view-inline-wire (encoded)
  "The view body with the data of every image it references: a mount carries its images whole."
  (let ((images (kit-encoded-images encoded)))
    (if (null images)
        (kit-encoded-body encoded)
        (let ((body (make-hash-table :test 'equal)))
          (maphash (lambda (k v) (setf (gethash k body) v)) (kit-encoded-body encoded))
          (setf (gethash "images" body)
                (kit-vector (mapcar (lambda (image) (obj "ref" (kit-img-ref image) "mimeType" (kit-img-mime-type image)
                                                          "data" (kit-base64 (kit-img-data image))))
                                    images)))
          body))))

(defun view-content-args (ctx view intents assets fallback)
  (let* ((ext (ctx-extension ctx))
         (args (obj "view" (view-inline-wire (kit-encode ext view)))))
    (when intents (setf (gethash "intents" args) (coerce intents 'simple-vector)))
    (when assets (setf (gethash "assets" args) (coerce assets 'simple-vector)))
    (when fallback
      (setf (gethash "fallback" args) (gethash "root" (kit-encoded-body (kit-encode ext (kit-view fallback))))))
    args))

(defun view-call (ctx method args)
  (let ((result (ctx-call-host ctx method args)))
    (unless (and (hash-table-p result) (hash-table-p (jget result "mount")))
      (error 'host-call-error :message (format nil "host reply to ~A is not a mount status" method) :code "invalid_reply"))
    (let ((mount (jget result "mount")))
      (%view-status (view-ref (jget mount "key" "") (jget mount "epoch" 0) (jget mount "rev" 0))
                    (jget result "state" "") (jget result "reason" "")))))

(defun ctx-mount-view (ctx key slot view &key target intents assets fallback)
  "Mount VIEW (a kit view) under KEY in SLOT, a slot the attached client draws. TARGET is a VIEW-TARGET, INTENTS VIEW-INTENTs, ASSETS
VIEW-ASSETs, FALLBACK a kit node the client draws when it cannot draw the view."
  (let ((args (view-content-args ctx view intents assets fallback)))
    (setf (gethash "key" args) key (gethash "slot" args) slot)
    (when target (setf (gethash "target" args) target))
    (view-call ctx "ui.view.mount" args)))

(defun ctx-update-view (ctx mount view &key intents assets fallback)
  "Replace the content of MOUNT, a VIEW-REF. Signals code \"stale\" unless MOUNT carries the epoch and rev the last operation returned."
  (let ((args (view-content-args ctx view intents assets fallback)))
    (setf (gethash "mount" args) (view-ref-wire mount))
    (view-call ctx "ui.view.update" args)))

(defun ctx-hide-view (ctx mount) (view-call ctx "ui.view.hide" (obj "mount" (view-ref-wire mount))))
(defun ctx-show-view (ctx mount) (view-call ctx "ui.view.show" (obj "mount" (view-ref-wire mount))))
(defun ctx-finish-view (ctx mount)
  "End MOUNT as completed. Terminal: a later operation on the mount signals code \"terminal\"."
  (view-call ctx "ui.view.finish" (obj "mount" (view-ref-wire mount))))
(defun ctx-remove-view (ctx mount)
  "End MOUNT as withdrawn. Terminal."
  (view-call ctx "ui.view.remove" (obj "mount" (view-ref-wire mount))))
(defun ctx-view-state (ctx key) (view-call ctx "ui.view.state" (obj "key" key)))

(defun on-view-intent (ext handler)
  "Answer the intents of EXT's mounts. HANDLER is called with the request context and the invocation, a JSON object with \"mount\" (\"key\",
\"epoch\"), \"rev\", \"nodeKey\", \"intent\", \"arg\" and \"ticket\", and returns a VIEW-INTENT-RESULT. CTX-CANCELLED-P turns true when the host
cancels a cancellable intent; an answer after that is dropped. A later call replaces the handler."
  (unless (functionp handler) (error "on-view-intent requires a function"))
  (sb-thread:with-mutex ((extension-view-lock ext)) (setf (extension-view-intent-handler ext) handler))
  ext)

(defun view-intent-result (&optional (outcome "done") &key answer error)
  "A handler's answer: OUTCOME \"done\" (ANSWER), \"refused\" or \"failed\" (ERROR)."
  (let ((result (obj "outcome" outcome)))
    (when answer (setf (gethash "answer" result) answer))
    (when error (setf (gethash "error" result) error))
    result))

(def-state-hook "ui-view" (ext state)
  ;; every state is a full snapshot, so an absent uiAbilities clears the replica
  (let ((abilities (jget state "uiAbilities")))
    (sb-thread:with-mutex ((extension-view-lock ext))
      (setf (extension-view-abilities ext) (and (hash-table-p abilities) abilities)))))

(def-ready-hook "ui-view" (ext ready)
  (let ((abilities (jget (jget ready "state") "uiAbilities")))
    (sb-thread:with-mutex ((extension-view-lock ext))
      (setf (extension-view-abilities ext) (and (hash-table-p abilities) abilities)))))

(def-request "ui.view.intent" (ext ctx req)
  (let ((handler (sb-thread:with-mutex ((extension-view-lock ext)) (extension-view-intent-handler ext))))
    (if handler
        (funcall handler ctx (or (jget req "args") (obj)))
        (view-intent-result "refused" :error "no intent handler"))))
