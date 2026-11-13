;;;; SPDX-License-Identifier: MIT
;;;; pig additive (D614): plugin UI mounts.
;;;;
;;;; An extension mounts a kit view in a slot the attached client draws, updates it, and answers the user's intents. The host
;;;; replicates what the client draws (CTX-VIEW-ABILITIES); with no client attached it is NIL and a mount signals
;;;; HOST-CALL-ERROR with code "unsupported". A failed operation signals HOST-CALL-ERROR whose HOST-CALL-ERROR-CODE is "invalid",
;;;; "unsupported", "limit", "stale", "terminal" or "not_found". The wire is the one the Go SDK speaks: the ui.view.mount, update,
;;;; hide, show, finish, remove, state and diagnostics calls, and the ui.view.intent request. The members the D614 amendment adds
;;;; (alternatives, drawing, a11y, focus, a finish result) are JSON objects in the wire's own shape, built with OBJ.

(in-package #:pig)

(defstruct (view-ref (:constructor view-ref (key &optional (epoch 0) (rev 0))))
  "One revision of one mount incarnation. Pass the ref the last operation returned to the next update."
  key epoch rev)

(defstruct (view-status (:constructor %view-status (mount state reason &optional result)))
  "The reply of every mount operation. STATE is \"live\", \"hidden\", \"finished\", \"removed\" or \"broken\"; REASON is \"\" or, for
broken, \"session_replaced\", \"owner_gone\" or \"shutdown\"; RESULT is the JSON value a finished mount was given, or NIL."
  mount state reason result)

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

(defun view-intent (name arg &key node-key answer cancellable timeout-ms when-hidden dismiss)
  "One intent the view's nodes may raise. NAME is \"<extension>.<verb>\"; TIMEOUT-MS defaults to 30000 and is 100..300000.
WHEN-HIDDEN lets a client raise the intent while the mount is hidden: it is how the user opens a view the extension hid, and the
handler shows the mount. At most one intent of a mount has it; it names no node and its ARG accepts the empty object.
DISMISS names the intent a client raises, with the empty object, when the user dismisses a dialog or overlay mount: the handler
closes the mount. The same three rules hold for it. Without one, Escape leaves the mount and it stays open until the extension
closes it."
  (let ((intent (obj "name" name "arg" arg)))
    (when node-key (setf (gethash "nodeKey" intent) node-key))
    (when answer (setf (gethash "answer" intent) answer))
    (when cancellable (setf (gethash "cancellable" intent) t))
    (when timeout-ms (setf (gethash "timeoutMs" intent) timeout-ms))
    (when when-hidden (setf (gethash "whenHidden" intent) t))
    (when dismiss (setf (gethash "dismiss" intent) t))
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
\"snapshotBytes\", \"textBytes\", \"mounts\", \"intentArgBytes\"), \"input\" (\"keyboard\", \"pointer\", \"text\") and, when the client states them,
\"claims\" (objects with \"name\" and \"value\"), \"slotClaims\" (\"slot\", \"claims\") and \"drawing\" (\"profiles\", \"shapes\", \"pixels\"); NIL while
no client is attached."
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

(defun view-clause (claim op &optional (value nil value-p))
  "One test of a claim: OP \"present\", \"equals\" (VALUE a boolean, an integer or a string), \"contains\" (VALUE a vector of strings the
claimed list must hold) or \"atLeast\" (VALUE an integer)."
  (let ((clause (obj "claim" claim "op" op)))
    (when value-p (setf (gethash "value" clause) value))
    clause))

(defun view-when (&rest groups)
  "A predicate over the client's claims: it holds when all the VIEW-CLAUSEs of any one of GROUPS (lists of clauses) hold. At most 8 groups
of 8 clauses."
  (obj "any" (coerce (mapcar (lambda (group) (obj "all" (coerce group 'simple-vector))) groups) 'simple-vector)))

(defun view-alternative (when view &key drawing)
  "A kit VIEW a client is sent in place of the mount's view when WHEN (a VIEW-WHEN) holds for that client's claims. DRAWING is a JSON object."
  (list when view drawing))

(defun view-content-args (ctx view intents assets fallback &key title alternatives drawing a11y focus bindings)
  (let* ((ext (ctx-extension ctx))
         (args (obj "view" (view-inline-wire (kit-encode ext view)))))
    (when (and title (string/= title "")) (setf (gethash "title" args) title))
    (when alternatives
      (setf (gethash "alternatives" args)
            (coerce (mapcar (lambda (alt)
                              (destructuring-bind (when alt-view alt-drawing) alt
                                (let ((wire (obj "when" when "view" (view-inline-wire (kit-encode ext alt-view)))))
                                  (when alt-drawing (setf (gethash "drawing" wire) alt-drawing))
                                  wire)))
                            alternatives)
                    'simple-vector)))
    (when drawing (setf (gethash "drawing" args) drawing))
    (when a11y (setf (gethash "a11y" args) (coerce a11y 'simple-vector)))
    (when focus (setf (gethash "focus" args) (coerce focus 'simple-vector)))
    (when bindings (setf (gethash "bindings" args) (coerce bindings 'simple-vector)))
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
                    (jget result "state" "") (jget result "reason" "") (jget result "result")))))

(defun ctx-mount-view (ctx key slot view &key target title intents assets fallback alternatives drawing a11y focus bindings)
  "Mount VIEW (a kit view) under KEY in SLOT, a slot the attached client draws. TARGET is a VIEW-TARGET, INTENTS VIEW-INTENTs, ASSETS
VIEW-ASSETs, FALLBACK a kit node the client draws when it cannot draw the view. ALTERNATIVES are VIEW-ALTERNATIVEs (at most 8; a client
draws the first whose predicate holds for its claims), DRAWING a JSON object (\"profile\" \"scene\" with \"shapes\" or \"pixels\" with
\"pixels\", and \"regions\"), A11Y and FOCUS lists of JSON objects naming view nodes by id (at most 256 each); a focus object's
\"request\" is \"mount\" or \"update\". TITLE is the short text a client labels the mount with (at most 64 bytes on one line). BINDINGS
is a list of JSON objects (at most 32), each with \"key\" (a key id such as \"d\" or \"shift+left\") or \"gesture\" (\"swipe-left\",
\"swipe-right\", \"swipe-up\", \"swipe-down\"), the \"intent\" it raises (one that names no node), and optionally \"arg\" and \"name\"; a
client fires one only while the mount holds the focus and the focused node did not take the key."
  (let ((args (view-content-args ctx view intents assets fallback :title title :alternatives alternatives :drawing drawing :a11y a11y :focus focus
                                 :bindings bindings)))
    (setf (gethash "key" args) key (gethash "slot" args) slot)
    (when target (setf (gethash "target" args) target))
    (view-call ctx "ui.view.mount" args)))

(defun ctx-update-view (ctx mount view &key title intents assets fallback alternatives drawing a11y focus bindings)
  "Replace the content of MOUNT, a VIEW-REF, its TITLE included. Signals code \"stale\" unless MOUNT carries the epoch and rev the last operation returned."
  (let ((args (view-content-args ctx view intents assets fallback :title title :alternatives alternatives :drawing drawing :a11y a11y :focus focus
                                 :bindings bindings)))
    (setf (gethash "mount" args) (view-ref-wire mount))
    (view-call ctx "ui.view.update" args)))

(defun ctx-hide-view (ctx mount) (view-call ctx "ui.view.hide" (obj "mount" (view-ref-wire mount))))
(defun ctx-show-view (ctx mount) (view-call ctx "ui.view.show" (obj "mount" (view-ref-wire mount))))
(defun ctx-finish-view (ctx mount &key (result nil result-p))
  "End MOUNT as completed. Terminal: a later operation on the mount signals code \"terminal\". RESULT, any JSON value of at most 65536
encoded bytes, is the extension's last word on the mount; the reply and CTX-VIEW-STATE carry it as VIEW-STATUS-RESULT."
  (let ((args (obj "mount" (view-ref-wire mount))))
    (when result-p (setf (gethash "result" args) result))
    (view-call ctx "ui.view.finish" args)))
(defun ctx-remove-view (ctx mount)
  "End MOUNT as withdrawn. Terminal."
  (view-call ctx "ui.view.remove" (obj "mount" (view-ref-wire mount))))
(defun ctx-view-state (ctx key) (view-call ctx "ui.view.state" (obj "key" key)))

(defun ctx-view-diagnostics (ctx key)
  "The notices of the mount under KEY, oldest first, a list of JSON objects with \"code\", \"severity\", \"count\", \"epoch\" and, when
set, \"message\", \"path\", \"claim\", \"intent\", \"measured\", \"limit\" and \"rev\". The host keeps at most 32 and counts repeats."
  (let* ((result (ctx-call-host ctx "ui.view.diagnostics" (obj "key" key)))
         (notices (and (hash-table-p result) (jget result "notices"))))
    (unless (vectorp notices)
      (error 'host-call-error :message "host reply to ui.view.diagnostics has no notices" :code "invalid_reply"))
    (coerce notices 'list)))

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

(defun on-view-abilities (ext handler)
  "Run HANDLER, (lambda (ctx abilities)), when what the attached client draws changed: the first client attached, the abilities changed, or the
last client left (ABILITIES is NIL; a client that draws no slot is an object whose \"slots\" is empty). It is where an extension mounts its
views for a client that arrives while the session is idle, and stops work no client can see. HANDLER receives the abilities of the change it is
delivered for, and CTX-VIEW-ABILITIES already reports them when it runs, so a mount it makes is checked against that client. The state an
extension starts in is not a change: an extension loaded while a client is attached reads CTX-VIEW-ABILITIES at session_start. A session no
client attaches to never runs HANDLER. Handlers run in registration order on a worker thread, one change at a time in the order the changes
arrived, never on the thread that reads the host's replies, so a handler can make a blocking host call; the host does not wait for them, and one
that signals is reported in the extension's log and does not stop the others."
  (unless (functionp handler) (error "on-view-abilities requires a function"))
  (kit-feed-subscribe (kit-state-abilities-feed (kit-state ext)) (lambda (abilities) (funcall handler (make-ctx ext) abilities)))
  ext)

(defun view-apply-state (ext state)
  "Replace the replicated client abilities with the uiAbilities member of STATE, a whole state (an absent member is no client), and deliver them
to the ON-VIEW-ABILITIES handlers when they differ from the ones the state before carried. The first state is where the extension starts."
  (when (hash-table-p state)
    (let ((abilities (let ((member (jget state "uiAbilities"))) (and (hash-table-p member) member)))
          (changed nil))
      (sb-thread:with-mutex ((extension-view-lock ext))
        (setf changed (and (extension-view-abilities-known ext) (not (ts-json-equal (extension-view-abilities ext) abilities)))
              (extension-view-abilities ext) abilities
              (extension-view-abilities-known ext) t))
      (when changed (kit-feed-publish ext (kit-state-abilities-feed (kit-state ext)) abilities)))))

(def-state-hook "ui-view" (ext state) (view-apply-state ext state))
(def-ready-hook "ui-view" (ext ready) (view-apply-state ext (jget ready "state")))

(def-request "ui.view.intent" (ext ctx req)
  (let ((handler (sb-thread:with-mutex ((extension-view-lock ext)) (extension-view-intent-handler ext))))
    (if handler
        (funcall handler ctx (or (jget req "args") (obj)))
        (view-intent-result "refused" :error "no intent handler"))))
