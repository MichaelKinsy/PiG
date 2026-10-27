;;;; SPDX-License-Identifier: MIT
;;;; Host calls and the per-request context.

(in-package #:pig)

;;; ---------------------------------------------------------------- the call machinery

(defun frame-error-message (error-object)
  (if (hash-table-p error-object)
      (let ((message (jget error-object "message" "host call failed")))
        (if (stringp message) message "host call failed"))
      "host call failed"))

(defvar *after-call-sent* nil
  "When a function, HOST-CALL calls it once the call's frame is written, before it waits (the caller's order point).")

(defun host-call (ext method &optional args parent (request-id ""))
  "Call METHOD on the host and wait for the answer; the result value (NIL for null).
PARENT is the request that owns the call. Signals HOST-CALL-ERROR when the host answers with an error."
  (let* ((conn (extension-conn ext)) (slot nil) (id nil) (frame nil))
    (unless conn (error 'host-call-error :message "extension connection is not connected"))
    (sb-thread:with-mutex ((conn-write-lock conn))
      (let ((timer nil))
        (sb-thread:with-mutex ((extension-state-lock ext))
          (when (extension-shutdown ext)
            (error 'host-call-error :message "extension connection is closed or replaced"))
          (when parent
            (case (request-state parent)
              (:cancelled (error 'host-call-cancelled
                                 :message "host call cancelled with its parent request"))
              (:completed (setf request-id "" timer t))
              (t (setf request-id (request-id parent)))))
          (setf id (format nil "c~D" (incf (extension-call-counter ext)))
                slot (make-call-slot method request-id))
          (setf (gethash id (extension-pending ext)) slot))
        (let ((call (obj "method" method "args" args)))
          (when (plusp (length request-id)) (setf (gethash "parent_request_id" call) request-id))
          (when timer (setf (gethash "timer" call) t))
          (setf frame (obj "type" "call" "id" id "call" call))))
      (handler-case (send-frame-locked conn frame)
        (error (e)
          (sb-thread:with-mutex ((extension-state-lock ext)) (remhash id (extension-pending ext)))
          (error e))))
    (when *after-call-sent* (funcall *after-call-sent*))
    (wait-call ext id slot)))

(defun wait-call (ext id slot)
  (sb-thread:with-mutex ((call-slot-lock slot))
    (loop until (call-slot-done slot)
          do (sb-thread:condition-wait (call-slot-queue slot) (call-slot-lock slot))))
  (sb-thread:with-mutex ((extension-state-lock ext)) (remhash id (extension-pending ext)))
  (let ((result (call-slot-result slot)))
    (cond ((call-slot-cancelled slot)
           (error 'host-call-cancelled
                  :message (format nil "host call ~A cancelled with its parent request" (call-slot-method slot))))
          ((and (extension-shutdown ext) (null result))
           (error 'host-call-error :message "extension shut down during host call"))
          ((and (hash-table-p result) (hash-table-p (jget result "error")))
           (let ((err (jget result "error")))
             (error 'host-call-error :message (frame-error-message err)
                                     :code (let ((code (jget err "code"))) (and (stringp code) code)))))
          (t (let ((value (and (hash-table-p result) (jget result "result"))))
               (if (eq value :null) nil value))))))

(defun finish-slot (slot result &key cancelled)
  (sb-thread:with-mutex ((call-slot-lock slot))
    (setf (call-slot-result slot) result (call-slot-cancelled slot) cancelled (call-slot-done slot) t)
    (sb-thread:condition-broadcast (call-slot-queue slot))))

(defun deliver-call-result (ext env)
  (let ((slot (sb-thread:with-mutex ((extension-state-lock ext))
                (let ((slot (gethash (jget env "id" "") (extension-pending ext))))
                  slot))))
    (when slot (finish-slot slot (or (jget env "call_result") (obj))))))

(defun notify-host (ext method &optional args)
  (send-frame (extension-conn ext) (obj "type" "notify" "notify" (obj "method" method "args" args))))

(defun send-request-state (ext request-id state &optional reason parent)
  (let ((conn (extension-conn ext)))
    (sb-thread:with-mutex ((conn-write-lock conn))
      (when parent
        (sb-thread:with-mutex ((extension-state-lock ext))
          (when (or (request-finished parent)
                    (and (eq (request-state parent) :cancelled) (string/= state "progress")))
            (return-from send-request-state nil))))
      (let ((payload (obj "request_id" request-id "state" state)))
        (when reason (setf (gethash "reason" payload) reason))
        (send-frame-locked conn (obj "type" "request_state" "request_state" payload))))))

(defun respond (ext request-id result error-message)
  "Answer request REQUEST-ID: a result, or an error message."
  (let ((conn (extension-conn ext)))
    (sb-thread:with-mutex ((conn-write-lock conn))
      (sb-thread:with-mutex ((extension-state-lock ext))
        (let ((request (gethash request-id (extension-active ext))))
          (remhash request-id (extension-active ext))
          (when request
            (setf (request-finished request) t)
            (when (eq (request-state request) :active) (setf (request-state request) :completed))))
        ;; a call the handler left running continues, owned by no request
        (maphash (lambda (id slot) (declare (ignore id))
                   (when (equal (call-slot-parent-id slot) request-id) (setf (call-slot-parent-id slot) "")))
                 (extension-pending ext)))
      (send-frame-locked conn (obj "type" "request_state" "request_state" (obj "request_id" request-id "state" "completed")))
      (send-frame-locked conn (obj "type" "response" "id" request-id
                                   "response" (obj "result" result
                                                   "error" (if error-message (obj "message" error-message) nil)))))))

;;; ---------------------------------------------------------------- the context

(defstruct (ctx (:constructor %make-ctx (extension request tool-call-id request-id tool-context-p tool-args)))
  extension
  request
  tool-call-id
  (request-id "")
  tool-context-p
  tool-args
  (system-prompt nil))

(defun ctx-cancel (ctx)
  (and (ctx-request ctx) (request-signal (ctx-request ctx))))

(defun make-ctx (ext &optional request env)
  (let ((req (and env (jget env "request"))))
    (%make-ctx ext request
               (and req (jget req "tool_call_id"))
               (if request (request-id request) "")
               (and req (equal (jget req "method") "tool_call"))
               (and req (jget req "args")))))

(defun assert-active (ctx)
  (let ((message (sb-thread:with-mutex ((extension-state-lock (ctx-extension ctx)))
                   (extension-stale-message (ctx-extension ctx)))))
    (when message (error "~A" message))))

(defparameter *dialog-methods* '("ui.select" "ui.confirm" "ui.input" "ui.editor" "ui.custom"))
(defparameter *rpc-synchronous-ui-effects*
  '("ui.notify" "ui.setStatus" "ui.setWidget" "ui.setTitle" "ui.setEditorText" "ui.pasteToEditor"
    "ui.setWorkingMessage" "ui.setWorkingVisible" "ui.setWorkingIndicator" "ui.setHiddenThinkingLabel"
    "ui.setToolsExpanded" "ui.setFooter" "ui.setHeader"))

(defun ctx-call-host (ctx method &optional args)
  "Call a host method by its wire name and return its result: the escape hatch under every other host call."
  (let* ((ext (ctx-extension ctx))
         (request-id (ctx-request-id ctx))
         (rpc (string= (sb-thread:with-mutex ((extension-state-lock ext)) (extension-mode ext)) "rpc"))
         (reports (and (plusp (length request-id))
                       (not (and rpc (member method *rpc-synchronous-ui-effects* :test #'string=))))))
    (when (and reports (not (member method *dialog-methods* :test #'string=)))
      (send-request-state ext request-id "blocked" "host_call" (ctx-request ctx)))
    (unwind-protect (host-call ext method args (ctx-request ctx) request-id)
      (when reports (send-request-state ext request-id "progress" nil (ctx-request ctx))))))

(defun block-for-user (ctx)
  (when (plusp (length (ctx-request-id ctx)))
    (send-request-state (ctx-extension ctx) (ctx-request-id ctx) "blocked" "user" (ctx-request ctx))))

(defun reply-field (ctx method field &optional args)
  "One field every reply of METHOD carries; a missing field is a protocol error, never an empty value."
  (let* ((result (ctx-call-host ctx method args))
         (value (and (hash-table-p result) (jget result field))))
    (when (or (null value) (eq value :null))
      (error 'host-call-error :message (format nil "host reply to ~A has no ~S" method field) :code "invalid_reply"))
    value))

(defun optional-string-field (ctx method field)
  (let* ((result (ctx-call-host ctx method))
         (value (and (hash-table-p result) (jget result field))))
    (and (stringp value) (plusp (length value)) value)))

;;; replicated values --------------------------------------------------------

(defun ctx-cwd (ctx)
  (assert-active ctx)
  (sb-thread:with-mutex ((extension-state-lock (ctx-extension ctx))) (extension-cwd (ctx-extension ctx))))

(defun ctx-mode (ctx)
  "\"tui\", \"rpc\", \"json\" or \"print\" (the default when the host sent none)."
  (assert-active ctx)
  (let ((mode (sb-thread:with-mutex ((extension-state-lock (ctx-extension ctx))) (extension-mode (ctx-extension ctx)))))
    (if (plusp (length mode)) mode "print")))

(defun ctx-has-ui-p (ctx)
  (assert-active ctx)
  (sb-thread:with-mutex ((extension-state-lock (ctx-extension ctx))) (extension-has-ui (ctx-extension ctx))))

(defun ctx-signal (ctx)
  "The cancellation signal of the run in progress, or NIL while no run is active (Pi's ctx.signal)."
  (let ((ext (ctx-extension ctx)))
    (sb-thread:with-mutex ((extension-state-lock ext)) (cdr (extension-run-signal ext)))))

(defun ctx-cancelled-p (ctx)
  "Whether this request was cancelled, or the connection ended after the handler returned."
  (let ((request (ctx-request ctx)) (ext (ctx-extension ctx)))
    (cond ((null request) nil)
          ((eq (request-state request) :completed) (extension-shutdown ext))
          (t (abort-signal-set-p (request-signal request))))))

(defun ctx-wait-cancelled (ctx &optional timeout)
  "Block until the request is cancelled; with TIMEOUT seconds, NIL if it was not."
  (abort-signal-wait (ctx-cancel ctx) timeout))

(defun ctx-cancellation-reason (ctx)
  (and (ctx-request ctx) (request-reason (ctx-request ctx))))

(defun ctx-on-update (ctx partial)
  "Stream a partial result of the running tool; PARTIAL is a string or a tool-result hash-table."
  (unless (and (ctx-tool-call-id ctx) (plusp (length (ctx-request-id ctx))))
    (error "ctx-on-update is only available while a tool runs"))
  (notify-host (ctx-extension ctx) "tool_update"
               (obj "request_id" (ctx-request-id ctx)
                    "result" (if (stringp partial) (obj "content" partial) partial))))

;;; UI ----------------------------------------------------------------------

(defun ctx-notify (ctx message &optional (level ""))
  "Show MESSAGE. An empty LEVEL is Pi's omitted notify type (shown as info)."
  (ctx-call-host ctx "ui.notify" (obj "message" message "level" level))
  nil)

(defun ctx-set-status (ctx key text)
  (ctx-call-host ctx "ui.setStatus" (obj "key" key "text" text)) nil)

(defun ctx-clear-status (ctx key)
  (ctx-call-host ctx "ui.setStatus" (obj "key" key)) nil)

(defun ctx-set-title (ctx title)
  (ctx-call-host ctx "ui.setTitle" (obj "title" title)) nil)

(defun dialog-args (args timeout)
  (when timeout (setf (gethash "opts" args) (obj "timeout" timeout)))
  args)

(defun dialog-value (result member)
  "(values value defined-p): a dialog Pi left undefined (cancelled, timed out) is (values nil nil)."
  (if (and (hash-table-p result) (eq (jget result "ok") t))
      (values (jget result member) t)
      (values nil nil)))

(defun ctx-select (ctx title options &key timeout)
  "Ask the user to pick one of OPTIONS (strings). (values choice answered-p)."
  (block-for-user ctx)
  (dialog-value (ctx-call-host ctx "ui.select" (dialog-args (obj "title" title "options" (coerce options 'simple-vector)) timeout))
                "selected"))

(defun ctx-confirm (ctx title message &key timeout)
  "Ask the user to confirm: true only when the user confirmed."
  (block-for-user ctx)
  (let ((result (ctx-call-host ctx "ui.confirm" (dialog-args (obj "title" title "message" message) timeout))))
    (eq (and (hash-table-p result) (jget result "confirmed")) t)))

(defun ctx-input (ctx title &key placeholder timeout)
  "Ask the user for a line of text. (values text answered-p)."
  (block-for-user ctx)
  (let ((args (obj "title" title)))
    (when placeholder (setf (gethash "placeholder" args) placeholder))
    (dialog-value (ctx-call-host ctx "ui.input" (dialog-args args timeout)) "text")))

(defun ctx-editor (ctx title &key prefill)
  "Open the multi-line editor. (values text answered-p)."
  (block-for-user ctx)
  (let ((args (obj "title" title)))
    (when prefill (setf (gethash "prefill" args) prefill))
    (dialog-value (ctx-call-host ctx "ui.editor" args) "text")))

;;; exec ---------------------------------------------------------------------

(defstruct (exec-result (:constructor make-exec-result (stdout stderr exit-code killed)))
  stdout stderr exit-code killed)

(defun ctx-exec (ctx command &key args timeout cwd signal)
  "Run COMMAND through the host's executor. TIMEOUT is in milliseconds; a command it kills reports KILLED."
  (when signal (error 'unsupported-capability :member "ctx-exec :signal"))
  (let ((call-args (obj "command" command "args" (coerce args 'simple-vector)))
        (options (obj)))
    (when timeout (setf (gethash "timeout" options) timeout))
    (when cwd (setf (gethash "cwd" options) cwd))
    (when (plusp (hash-table-count options)) (setf (gethash "options" call-args) options))
    (let ((result (or (ctx-call-host ctx "exec" call-args) (obj))))
      (flet ((text (key) (let ((v (jget result key))) (if (stringp v) v "")))
             (truth (key) (eq (jget result key) t)))
        (make-exec-result (text "stdout") (text "stderr")
                          (let ((code (jget result "code"))) (if (integerp code) code 0))
                          (truth "killed"))))))

;;; agent and session control -------------------------------------------------

(defun ctx-is-idle-p (ctx) (eq (reply-field ctx "isIdle" "idle") t))
(defun ctx-has-pending-messages-p (ctx) (eq (reply-field ctx "hasPendingMessages" "pending") t))
(defun ctx-abort (ctx) (ctx-call-host ctx "abort") nil)
(defun ctx-shutdown (ctx) (ctx-call-host ctx "shutdown") nil)
(defun ctx-reload (ctx) (ctx-call-host ctx "reload") nil)

(defun ctx-compact (ctx &key custom-instructions)
  "Compact the session and wait for the outcome."
  (let ((args (obj)))
    (when custom-instructions (setf (gethash "customInstructions" args) custom-instructions))
    (ctx-call-host ctx "compact" args)
    nil))

(defun ctx-get-flag (ctx name)
  "The flag's value from the host, else the default it registered with, else NIL."
  (let* ((result (or (ctx-call-host ctx "getFlag" (obj "name" name)) (obj)))
         (value (jget result "value")))
    (if (and value (not (eq value :null)))
        value
        (gethash name (extension-flag-defaults (ctx-extension ctx))))))

(defun ctx-get-thinking-level (ctx) (reply-field ctx "getThinkingLevel" "level"))

(defun ctx-get-system-prompt (ctx)
  "The current system prompt. In a before_agent_start handler it is the prompt the emission's options render now."
  (if (ctx-system-prompt ctx)
      (system-prompt-get ctx)
      (reply-field ctx "getSystemPrompt" "prompt")))

(defun ctx-get-all-tools (ctx)
  "Every tool in the session's registry, as a vector of ToolInfo hash-tables."
  (let ((tools (reply-field ctx "getAllTools" "tools")))
    (loop for tool across tools
          do (when (and (hash-table-p tool) (eq (jget tool "reachesTools") t))
               (error 'unsupported-capability :member "ctx-get-all-tools of a tool with a reachesTools hook")))
    tools))

(defun ctx-get-active-tools (ctx) (reply-field ctx "getActiveTools" "tools"))
;;; thin host calls: one wire method each, the member names Pi's API gives them -------

(defun call-result (ctx method &optional args)
  "The \"result\" member of METHOD's reply (NIL for an absent or null member)."
  (let* ((reply (ctx-call-host ctx method args))
         (value (and (hash-table-p reply) (jget reply "result"))))
    (if (eq value :null) nil value)))

(defun nonempty-object (value) (and (hash-table-p value) (plusp (hash-table-count value)) value))

(defun success-and-error (ctx method args)
  "The (values success error-message) of a reply that carries success and an optional error."
  (let ((result (or (call-result ctx method args) (obj))))
    (values (eq (or (jget result "success") (jget result "ok")) t)
            (let ((e (jget result "error"))) (if (stringp e) e "")))))

(defun ctx-set-active-tools (ctx tool-names)
  (ctx-call-host ctx "setActiveTools" (obj "tools" (coerce tool-names 'vector))) nil)
(defun ctx-get-commands (ctx) (reply-field ctx "getCommands" "commands"))
(defun ctx-get-context-usage (ctx) (nonempty-object (call-result ctx "getContextUsage")))
(defun ctx-get-system-prompt-options (ctx)
  (let ((result (call-result ctx "getSystemPromptOptions")))
    (unless (hash-table-p result)
      (error 'host-call-error :message "host reply to getSystemPromptOptions is not an object" :code "invalid_reply"))
    result))
(defun ctx-get-model-info (ctx)
  (let ((result (call-result ctx "getModelInfo")))
    (and (hash-table-p result) (let ((id (jget result "id"))) (and (stringp id) (plusp (length id)))) result)))
(defun ctx-get-model-auth (ctx provider-id model-id)
  (call-result ctx "getModelAuth" (obj "provider" provider-id "modelId" model-id)))
(defun ctx-get-scoped-models (ctx) (or (call-result ctx "getScopedModels" (obj)) (vector)))
(defun ctx-set-model (ctx model)
  "(values success error-message) for the model named MODEL."
  (success-and-error ctx "setModel" (obj "model" model)))
(defun ctx-set-thinking-level (ctx level) (ctx-call-host ctx "setThinkingLevel" (obj "level" level)) nil)
(defun ctx-set-label (ctx entry-id label) (ctx-call-host ctx "setLabel" (obj "entryId" entry-id "label" label)) nil)
(defun ctx-wait-for-idle (ctx) (ctx-call-host ctx "waitForIdle") nil)
(defun ctx-project-trusted-p (ctx) (eq (reply-field ctx "isProjectTrusted" "trusted") t))
(defun ctx-get-session-id (ctx) (reply-field ctx "getSessionID" "sessionId"))
(defun ctx-get-leaf-id (ctx) (optional-string-field ctx "getLeafID" "leafId"))
(defun ctx-get-all-themes (ctx) (reply-field ctx "ui.getAllThemes" "themes"))
(defun ctx-get-theme (ctx name) (let ((r (call-result ctx "ui.getTheme" (obj "name" name)))) (and (hash-table-p r) (jget r "theme"))))
(defun ctx-set-theme (ctx name) (success-and-error ctx "ui.setTheme" (obj "theme" name)))
(defun ctx-get-editor-text (ctx) (reply-field ctx "ui.getEditorText" "text"))
(defun ctx-tools-expanded-p (ctx) (eq (reply-field ctx "ui.getToolsExpanded" "expanded") t))


(defun ctx-send-message (ctx custom-type content &key (display t) trigger-turn deliver-as details)
  (let ((options (obj))
        (message (obj "customType" custom-type "content" content "display" (if display t :false))))
    (when trigger-turn (setf (gethash "triggerTurn" options) t))
    (when deliver-as (setf (gethash "deliverAs" options) deliver-as))
    (when details (setf (gethash "details" message) details))
    (ctx-call-host ctx "sendMessage" (obj "message" message "options" options))
    nil))

(defun ctx-send-user-message (ctx content &key (deliver-as "followUp"))
  (ctx-call-host ctx "sendUserMessage" (obj "content" content "options" (obj "deliverAs" deliver-as)))
  nil)

(defun ctx-append-entry (ctx custom-type data)
  (ctx-call-host ctx "appendEntry" (obj "customType" custom-type "data" data)) nil)

(defun ctx-set-session-name (ctx name)
  (ctx-call-host ctx "setSessionName" (obj "name" name)) nil)

;;; session manager reads --------------------------------------------------------

(defstruct (session-manager (:constructor make-session-manager (ctx))) ctx)

(defun ctx-session-manager (ctx) (make-session-manager ctx))

(defun session-read (manager method &optional args)
  (ctx-call-host (session-manager-ctx manager) "sessionRead" (obj "method" method "args" (or args (obj)))))

(defun session-get-cwd (m) (session-read m "getCwd"))
(defun session-get-session-dir (m) (session-read m "getSessionDir"))
(defun session-get-session-id (m) (session-read m "getSessionId"))
(defun session-get-session-file (m) (session-read m "getSessionFile"))
(defun session-get-session-name (m) (session-read m "getSessionName"))
(defun session-get-leaf-id (m) (session-read m "getLeafId"))
(defun session-get-header (m) (session-read m "getHeader"))
(defun session-get-leaf-entry (m) (session-read m "getLeafEntry"))
(defun session-get-entry (m id) (session-read m "getEntry" (obj "id" id)))
(defun session-get-label (m id) (session-read m "getLabel" (obj "id" id)))
(defun session-get-entries (m) (session-read m "getEntries"))
(defun session-get-branch (m &optional from-id)
  (session-read m "getBranch" (obj "fromId" (or from-id :null))))
(defun session-get-children (m parent-id) (session-read m "getChildren" (obj "parentId" (or parent-id :null))))
(defun session-get-tree (m) (session-read m "getTree"))

;;; model registry reads ---------------------------------------------------------

(defstruct (model-registry (:constructor make-model-registry (ctx))) ctx)

(defun ctx-model-registry (ctx) (make-model-registry ctx))

(defun registry-state (registry)
  (ctx-call-host (model-registry-ctx registry) "getModelRegistryState"))

(defun registry-provider-state (registry model)
  (let* ((providers (jget (registry-state registry) "providers"))
         (provider (and (hash-table-p model) (jget model "provider"))))
    (or (and (stringp provider) (hash-table-p providers) (jget providers provider)) (obj))))

(defun registry-has-configured-auth (registry model)
  "Whether MODEL's provider has configured credentials."
  (eq (jget (registry-provider-state registry model) "configured") t))

(defun registry-is-using-oauth (registry model)
  (eq (jget (registry-provider-state registry model) "usingOAuth") t))

(defun registry-get-registered-native-provider (registry provider)
  (declare (ignore registry provider))
  (error 'unsupported-capability :member "registry-get-registered-native-provider (Provider objects)"))

(defun registry-get-provider-name (registry provider)
  (declare (ignore registry provider))
  (error 'unsupported-capability :member "registry-get-provider-name (Provider objects)"))

;;; model registry host calls

(defun registry-find (registry provider-id model-id)
  "The model PROVIDER-ID/MODEL-ID, or NIL when the registry has none."
  (let ((model (call-result (model-registry-ctx registry) "getModel" (obj "provider" provider-id "modelId" model-id))))
    (and (hash-table-p model) model)))

(defun registry-get-provider-auth (registry provider-id)
  (call-result (model-registry-ctx registry) "getProviderAuth" (obj "provider" provider-id)))

(defun registry-refresh (registry &optional options)
  "Refresh the registry; the result carries aborted and errors."
  (call-result (model-registry-ctx registry) "refreshModelRegistry" (or options (obj))))

(defun registry-get-available-of-type (registry model-type &optional provider)
  "The models of MODEL-TYPE whose provider has working credentials."
  (let ((args (obj "type" model-type)))
    (when provider (setf (gethash "provider" args) provider))
    (call-result (model-registry-ctx registry) "getAvailableOfType" args)))

;;; footer data -------------------------------------------------------------------

(defstruct (footer-view (:constructor make-footer-view (ext))) ext)

(defun ctx-footer-data (ctx)
  "Pi's ReadonlyFooterDataProvider, replicated from the host's state pushes."
  (make-footer-view (ctx-extension ctx)))

(defun footer-get-git-branch (footer)
  (let ((ext (footer-view-ext footer)))
    (sb-thread:with-mutex ((extension-state-lock ext)) (footer-data-branch (extension-footer ext)))))

(defun footer-get-extension-statuses (footer)
  (let ((ext (footer-view-ext footer)))
    (sb-thread:with-mutex ((extension-state-lock ext))
      (let ((copy (make-hash-table :test 'equal)))
        (maphash (lambda (k v) (setf (gethash k copy) v)) (footer-data-statuses (extension-footer ext)))
        copy))))

(defun footer-get-available-provider-count (footer)
  (let ((ext (footer-view-ext footer)))
    (sb-thread:with-mutex ((extension-state-lock ext)) (footer-data-count (extension-footer ext)))))

(defun footer-on-branch-change (footer callback)
  "Call CALLBACK, with no arguments, whenever the git branch changes. Returns the unsubscribe function."
  (let* ((ext (footer-view-ext footer))
         (id (sb-thread:with-mutex ((extension-state-lock ext))
               (let ((id (incf (extension-branch-subscriber-id ext))))
                 (setf (extension-branch-subscribers ext)
                       (append (extension-branch-subscribers ext) (list (cons id callback))))
                 id))))
    (lambda ()
      (sb-thread:with-mutex ((extension-state-lock ext))
        (setf (extension-branch-subscribers ext)
              (remove id (extension-branch-subscribers ext) :key #'car))))))

;;; terminal resizes -------------------------------------------------------------

(defun ctx-on-width-change (ctx handler)
  "Call HANDLER, with the new terminal width, after each resize. Rows sent with the footer or header carry the width they were
laid out for, so after a resize they stay hidden until the extension sends rows for the new width: this is that trigger.
Handlers run one at a time, in the order the widths arrived, on a worker thread (never the thread that reads the host); a handler
that signals is reported on stderr and does not stop the others. Returns the unsubscribe function."
  (let* ((ext (ctx-extension ctx))
         (id (sb-thread:with-mutex ((extension-state-lock ext))
               (let ((id (incf (extension-width-subscriber-id ext))))
                 (setf (extension-width-subscribers ext)
                       (append (extension-width-subscribers ext) (list (cons id handler))))
                 id))))
    (lambda ()
      (sb-thread:with-mutex ((extension-state-lock ext))
        (setf (extension-width-subscribers ext)
              (remove id (extension-width-subscribers ext) :key #'car))))))

(defun width-worker-loop (ext)
  (loop
    (let ((job (sb-thread:with-mutex ((extension-width-lock ext))
                 (loop while (null (extension-width-jobs ext))
                       do (sb-thread:condition-wait (extension-width-wait ext) (extension-width-lock ext)))
                 (pop (extension-width-jobs ext)))))
      (dolist (handler (cdr job))
        (handler-case (funcall handler (car job))
          (error (e) (format *error-output* "~A: width change handler failed: ~A~%" (extension-name ext) e)))))))

(defun dispatch-width-change (ext width)
  "Queue WIDTH for the handlers subscribed now."
  (let ((handlers (sb-thread:with-mutex ((extension-state-lock ext))
                    (mapcar #'cdr (extension-width-subscribers ext)))))
    (when handlers
      (sb-thread:with-mutex ((extension-width-lock ext))
        (setf (extension-width-jobs ext) (append (extension-width-jobs ext) (list (cons width handlers))))
        (unless (extension-width-worker ext)
          (setf (extension-width-worker ext)
                (sb-thread:make-thread #'width-worker-loop :arguments (list ext) :name "pig-width")))
        (sb-thread:condition-notify (extension-width-wait ext))))))

;;; before_agent_start prompt rendering -------------------------------------------

(defstruct (system-prompt (:constructor make-system-prompt (ctx options prompt)))
  ctx options prompt (key nil) (lock (sb-thread:make-mutex :name "pig prompt")))

(defun system-prompt-get (ctx)
  (let* ((sp (ctx-system-prompt ctx))
         (key (to-json (system-prompt-options sp))))
    (sb-thread:with-mutex ((system-prompt-lock sp))
      (unless (equal key (system-prompt-key sp))
        (setf (system-prompt-prompt sp)
              (reply-field ctx "renderSystemPrompt" "prompt" (obj "options" (system-prompt-options sp)))
              (system-prompt-key sp) key))
      (system-prompt-prompt sp))))
