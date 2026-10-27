;;;; SPDX-License-Identifier: MIT
;;;; The extension object: registration and the connection's shared state.

(in-package #:pig)

;;; ---------------------------------------------------------------- abort signal

(defstruct (abort-signal (:constructor make-abort-signal ()))
  "A cancellation flag that can be waited on: the SDK's form of an AbortSignal."
  (lock (sb-thread:make-mutex :name "pig signal"))
  (queue (sb-thread:make-waitqueue))
  (flag nil)
  (reason nil))

(defun abort-signal-set-p (signal)
  (abort-signal-flag signal))

(defun signal-abort (signal &optional reason)
  (sb-thread:with-mutex ((abort-signal-lock signal))
    (unless (abort-signal-flag signal)
      (setf (abort-signal-flag signal) t (abort-signal-reason signal) reason))
    (sb-thread:condition-broadcast (abort-signal-queue signal)))
  signal)

(defun abort-signal-wait (signal &optional timeout)
  "Block until SIGNAL is set. With TIMEOUT (seconds), return NIL if it was not set by then; otherwise T."
  (if timeout
      (let ((deadline (+ (get-internal-real-time) (* timeout internal-time-units-per-second))))
        (loop
          (when (abort-signal-flag signal) (return t))
          (when (>= (get-internal-real-time) deadline) (return nil))
          (sleep 0.005)))
      (sb-thread:with-mutex ((abort-signal-lock signal))
        (loop until (abort-signal-flag signal)
              do (sb-thread:condition-wait (abort-signal-queue signal) (abort-signal-lock signal)))
        t)))

;;; ---------------------------------------------------------------- bookkeeping structs

(defstruct (call-slot (:constructor make-call-slot (method parent-id)))
  method
  (parent-id "")
  (lock (sb-thread:make-mutex :name "pig call"))
  (queue (sb-thread:make-waitqueue))
  (done nil)
  (cancelled nil)
  (result nil))

(defstruct (request (:constructor make-request (id signal)))
  id
  signal
  (state :active)               ; :active, :completed, :cancelled
  (finished nil)
  (reason nil)
  (ticket nil))

(defstruct (footer-data)
  (branch nil)
  (statuses (make-hash-table :test 'equal))
  (count 0))

;;; ---------------------------------------------------------------- the extension

(defstruct (extension (:constructor %make-extension (name)))
  (name "" :type string)
  (reg-lock (sb-thread:make-mutex :name "pig registration"))
  (tools '()) (tool-handlers (make-hash-table :test 'equal))
  (commands '()) (command-handlers (make-hash-table :test 'equal))
  (shortcuts '()) (shortcut-handlers (make-hash-table :test 'equal))
  (flags '()) (flag-defaults (make-hash-table :test 'equal))
  (handlers '()) (event-handlers (make-hash-table)) (next-handler-id 0)
  (loaded-handlers '())
  (live nil)
  ;; connection and per-connection state
  (conn nil)
  (state-lock (sb-thread:make-mutex :name "pig state"))
  (call-counter 0)
  (pending (make-hash-table :test 'equal))
  (active (make-hash-table :test 'equal))
  (request-threads '())
  (shutdown nil)
  (cwd "") (mode "") (width 0) (height 0) (has-ui nil)
  (footer (make-footer-data))
  (branch-subscribers '()) (branch-subscriber-id 0)
  ;; terminal resizes: handlers run in order on one worker thread, never on the thread that reads the host
  (width-subscribers '()) (width-subscriber-id 0)
  (width-lock (sb-thread:make-mutex :name "pig width"))
  (width-wait (sb-thread:make-waitqueue))
  (width-jobs '()) (width-worker nil)
  (stale-message nil)
  (run-signal nil)               ; (run . abort-signal) while a run is active
  (replicas (make-hash-table :test 'equal))
  ;; state of the feature files, by keyword (hooks.lisp)
  (extras (make-hash-table :test 'eq))
  (extras-lock (sb-thread:make-mutex :name "pig extras"))
  ;; handlers wait for on-loaded handlers before serving
  (loaded-lock (sb-thread:make-mutex :name "pig loaded"))
  (loaded-queue (sb-thread:make-waitqueue))
  (loaded t)
  ;; requests start their handlers in the order the host sent them
  (gate-lock (sb-thread:make-mutex :name "pig gate"))
  (gate-queue (sb-thread:make-waitqueue))
  (gate-issued 0)
  (gate-begun 0)
  (gate-skipped '()))

(defmethod print-object ((e extension) stream)
  (print-unreadable-object (e stream :type t) (write-string (extension-name e) stream)))

(defun make-extension (name)
  "A new extension that registers as NAME."
  (unless (and (stringp name) (plusp (length name)))
    (error "make-extension: the name must be a non-empty string"))
  (%make-extension name))

;;; ---------------------------------------------------------------- registration

(declaim (ftype function host-call tool-schema-guard tool-schema-declare))

(defun ensure-jsonable (value what)
  (tool-schema-guard value)
  (handler-case (encode-json value)
    (json-error (e) (error "~A is not JSON: ~A" what e))))

(defun event-name (event)
  (etypecase event
    (string event)
    (keyword (substitute #\_ #\- (string-downcase (symbol-name event))))))

(defun publish-declaration (ext declarations)
  "Send a command, shortcut or flag registered after load (registerDeclaration). The caller holds the registration lock."
  (when (extension-live ext)
    (host-call ext "registerDeclaration" declarations)))

(defun tool (ext name description schema handler
             &key prompt-snippet prompt-guidelines source)
  "Register a tool. HANDLER is (lambda (ctx params)) and returns a string, a hash-table in the tool-result shape,
or (tool-result ...). SCHEMA is the parameter JSON Schema: a hash-table whose \"type\" is \"object\"."
  (unless (hash-table-p schema)
    (error "Tool ~S registered by extension ~S must define an object parameter schema." name (extension-name ext)))
  (ensure-jsonable schema (format nil "tool schema for ~A" name))
  (let ((decl (obj "name" name "description" description "parameters" schema)))
    (when prompt-snippet (setf (gethash "prompt_snippet" decl) prompt-snippet))
    (when prompt-guidelines (setf (gethash "prompt_guidelines" decl) (coerce prompt-guidelines 'simple-vector)))
    (when source (setf (gethash "source" decl) source))
    (tool-schema-declare ext name decl schema)
    (sb-thread:with-mutex ((extension-reg-lock ext))
      (let ((existing (position name (extension-tools ext)
                                :key (lambda (d) (gethash "name" d)) :test #'string=)))
        (if existing
            (setf (nth existing (extension-tools ext)) decl)
            (setf (extension-tools ext) (append (extension-tools ext) (list decl)))))
      (setf (gethash name (extension-tool-handlers ext)) handler)
      (when (extension-live ext)
        (host-call ext "registerTool" decl))))
  name)

(defun command (ext name description handler)
  "Register a slash command. HANDLER is (lambda (ctx args)) with the argument text."
  (unless (and (stringp name) (plusp (length name)))
    (error "Command registered by extension ~S must have a non-empty string name." (extension-name ext)))
  (unless (functionp handler)
    (error "Command \"/~A\" registered by extension ~S must define a handler." name (extension-name ext)))
  (let ((decl (obj "name" name "description" description)))
    (sb-thread:with-mutex ((extension-reg-lock ext))
      (setf (extension-commands ext) (append (extension-commands ext) (list decl)))
      (setf (gethash name (extension-command-handlers ext)) handler)
      (publish-declaration ext (obj "commands" (vector decl)))))
  name)

(defun shortcut (ext key description handler)
  "Register a keyboard shortcut. HANDLER is (lambda (ctx))."
  (let ((decl (obj "key" key "description" description)))
    (sb-thread:with-mutex ((extension-reg-lock ext))
      (setf (extension-shortcuts ext) (append (extension-shortcuts ext) (list decl)))
      (setf (gethash key (extension-shortcut-handlers ext)) handler)
      (publish-declaration ext (obj "shortcuts" (vector decl)))))
  key)

(defun flag (ext name &key (description "") (type "string") (default nil default-p))
  "Register a command-line flag. TYPE is \"boolean\" or \"string\"."
  (unless (member type '("boolean" "string") :test #'string=)
    (error "flag type must be \"boolean\" or \"string\""))
  (let ((decl (obj "name" name "description" description "type" type))
        (default (if (and default-p (not (eq default :null))) default nil)))
    (when default
      (ensure-jsonable default (format nil "flag default for ~A" name))
      (setf (gethash "default" decl) default))
    (sb-thread:with-mutex ((extension-reg-lock ext))
      (setf (extension-flags ext) (append (extension-flags ext) (list decl)))
      (when (null (gethash name (extension-flag-defaults ext)))
        (setf (gethash name (extension-flag-defaults ext)) default))
      (publish-declaration ext (obj "flags" (vector decl)))))
  name)

(defun on (ext event handler)
  "Subscribe HANDLER, (lambda (ctx data)), to EVENT, a wire event name or keyword (:tool-call).
Returns an idempotent function that unsubscribes."
  (let* ((event (event-name event))
         (id (sb-thread:with-mutex ((extension-reg-lock ext)) (incf (extension-next-handler-id ext))))
         (live nil)
         (removed nil)
         (removed-lock (sb-thread:make-mutex :name "pig unsubscribe")))
    (sb-thread:with-mutex ((extension-reg-lock ext))
      (setf (extension-handlers ext)
            (append (extension-handlers ext) (list (obj "event" event "handler_id" id))))
      (setf (gethash id (extension-event-handlers ext)) handler)
      (setf live (extension-live ext)))
    (when live
      (host-call ext "event.subscribe" (obj "event" event "handlerId" id)))
    (lambda ()
      (let ((first (sb-thread:with-mutex (removed-lock) (prog1 (not removed) (setf removed t)))))
        (when first
          (let ((connected (sb-thread:with-mutex ((extension-reg-lock ext))
                             (or (extension-live ext)
                                 (progn
                                   (setf (extension-handlers ext)
                                         (remove id (extension-handlers ext)
                                                 :key (lambda (d) (gethash "handler_id" d))))
                                   (remhash id (extension-event-handlers ext))
                                   nil)))))
            (when connected
              (handler-case (host-call ext "event.unsubscribe" (obj "event" event "handlerId" id))
                (error (e) (format *error-output* "extension: event.unsubscribe ~S: ~A~%" event e))))))))))

(defun on-loaded (ext handler)
  "Run HANDLER, (lambda (ctx)), once the extension has registered and received its state, before it serves any request."
  (unless (functionp handler) (error "on-loaded requires a function"))
  (setf (extension-loaded-handlers ext) (append (extension-loaded-handlers ext) (list handler)))
  handler)

;;; ---------------------------------------------------------------- tool results

(defun tool-result (content &key details is-error structured-content terminate preview)
  "A tool result: CONTENT is a string or a list/vector of content blocks (hash-tables such as
(obj \"type\" \"text\" \"text\" \"...\"))."
  (let ((result (obj "content" (if (or (stringp content) (vectorp content)) content (coerce content 'simple-vector)))))
    (when details (setf (gethash "details" result) details))
    (when is-error (setf (gethash "is_error" result) t))
    (when structured-content (setf (gethash "structured_content" result) structured-content))
    (when terminate (setf (gethash "terminate" result) t))
    (when preview (setf (gethash "preview" result) preview))
    result))

(defun normalize-tool-result (result)
  (cond ((hash-table-p result) result)
        ((stringp result) (obj "content" result))
        ((null result) (obj "content" (vector)))
        (t (obj "content" (princ-to-string result)))))

;;; ---------------------------------------------------------------- declarative form

(defmacro define-extension (name &body clauses)
  "Define the standard factory NEW-EXTENSION, which returns an extension named NAME with CLAUSES installed in order:
  (:tool name description schema handler &key prompt-snippet prompt-guidelines source)
  (:command name description handler)
  (:shortcut key description handler)
  (:flag name &key description type default)
  (:on event handler)
  (:on-loaded handler)
Each clause is the call of the function of the same name on the extension."
  (let ((ext (gensym "EXT"))
        ;; the factory is the caller's NEW-EXTENSION, whatever package the caller is in
        (factory (intern "NEW-EXTENSION" *package*)))
    `(defun ,factory ()
       (let ((,ext (make-extension ,name)))
         ,@(mapcar (lambda (clause)
                     (destructuring-bind (kind &rest arguments) clause
                       (let ((function (ecase kind
                                         (:tool 'tool) (:command 'command) (:shortcut 'shortcut)
                                         (:flag 'flag) (:on 'on) (:on-loaded 'on-loaded))))
                         `(,function ,ext ,@arguments))))
                   clauses)
         ,ext))))
