;;;; SPDX-License-Identifier: MIT
;;;; The rest of the extension context and registration surface of the Python SDK's Context and Extension: replicated session
;;;; state, nested tool calls, signal-bound exec, tool definitions with their loadout/reach/prepare hooks, the session log
;;;; mirror, and user_bash operations. Names not exported here are prefixed core-, cs-, nested-, bash-ops-.

(in-package #:pig)

;;; ---------------------------------------------------------------- replicated state

(defstruct (core-state (:conc-name cs-) (:constructor make-core-state ()))
  (lock (sb-thread:make-mutex :name "pig core state"))
  (model "") (model-provider "") (session-name "") (session-file "") (settings nil) (source-info nil)
  (nested (make-hash-table :test 'equal)) (execute-seq 0) (exec-seq 0)
  ;; the session log mirror
  (session-id "") (entries (make-array 0 :adjustable t :fill-pointer t)) (index (make-hash-table :test 'equal)) (leaf-id "")
  (subscribed nil) (sub-error nil) (sub-lock (sb-thread:make-mutex :name "pig session log")))

(defun core-state (ext) (extension-extra ext :core #'make-core-state))

(defun core-model-identity (model)
  "(values id provider-id) of a state snapshot's model: the id (the name when it has no id) and the provider's id, both \"\" for no model."
  (if (hash-table-p model)
      (let ((id (let ((i (jget model "id"))) (if (and (stringp i) (plusp (length i))) i (jget model "name"))))
            (provider (let ((p (jget model "provider"))) (if (hash-table-p p) (jget p "id") p))))
        (values (if (stringp id) id "") (if (stringp provider) provider "")))
      (values "" "")))

(defun cs-reset-log (cs)
  (setf (fill-pointer (cs-entries cs)) 0)
  (clrhash (cs-index cs)))

(defun cs-add-entry (cs entry)
  (let ((pos (vector-push-extend entry (cs-entries cs))) (id (jget entry "id")))
    (when (and (stringp id) (plusp (length id)))
      (setf (gethash id (cs-index cs)) (cons pos (let ((p (jget entry "parentId"))) (if (stringp p) p "")))))))

(defun cs-apply-session (cs session)
  "Apply a state snapshot's session member to the log mirror. The caller holds the lock."
  (let* ((leaf (let ((l (jget session "leafId"))) (if (stringp l) l "")))
         (appended (let ((a (jget session "entriesAppended"))) (if (and (vectorp a) (not (stringp a))) a (vector))))
         (count (let ((c (jget session "entryCount"))) (if (integerp c) c 0)))
         (session-id (jget session "sessionId")))
    (when (and (stringp session-id) (plusp (length session-id)) (string/= session-id (cs-session-id cs)))
      (setf (cs-session-id cs) session-id (cs-leaf-id cs) "")
      (cs-reset-log cs))
    ;; An unsubscribed extension holds no log; a push of no entries and a zero count is the shape sent to it.
    (cond ((or (not (cs-subscribed cs)) (and (zerop count) (zerop (length appended))))
           (when (plusp (length leaf)) (setf (cs-leaf-id cs) leaf)))
          (t (unless (= (- count (length appended)) (length (cs-entries cs))) (cs-reset-log cs))
             (loop for entry across appended do (cs-add-entry cs entry))
             (when (plusp (length leaf)) (setf (cs-leaf-id cs) leaf))))))

(defun core-apply-state (ext state)
  "Record the replicated values of a ready or state_update snapshot."
  (when (hash-table-p state)
    (let ((cs (core-state ext)))
      (sb-thread:with-mutex ((cs-lock cs))
        (when (nth-value 1 (gethash "model" state))
          (multiple-value-bind (id provider) (core-model-identity (jget state "model"))
            (setf (cs-model cs) id (cs-model-provider cs) provider)))
        (let ((session (jget state "session")))
          (when (hash-table-p session)
            (let ((name (jget session "sessionName"))) (setf (cs-session-name cs) (if (stringp name) name "")))
            (let ((file (jget session "sessionFile"))) (when (and (stringp file) (plusp (length file))) (setf (cs-session-file cs) file)))
            (cs-apply-session cs session)))
        (let ((settings (jget state "settings"))) (when (hash-table-p settings) (setf (cs-settings cs) settings)))))))

(def-ready-hook :core (ext ready)
  (let ((info (jget ready "sourceInfo")) (cs (core-state ext)))
    (sb-thread:with-mutex ((cs-lock cs)) (setf (cs-source-info cs) (and (hash-table-p info) info))))
  (core-apply-state ext (jget ready "state")))

(def-state-hook :core (ext state) (core-apply-state ext state))

(defmacro with-core-state ((cs ctx) &body body)
  `(let ((,cs (core-state (ctx-extension ,ctx)))) (sb-thread:with-mutex ((cs-lock ,cs)) ,@body)))

(defun ctx-model (ctx)
  "The active model's id (Pi's ctx.model.id), \"\" while the session has no model."
  (assert-active ctx)
  (with-core-state (cs ctx) (cs-model cs)))

(defun ctx-model-provider (ctx)
  "The active model's provider id (Pi's ctx.model.provider), \"\" while the session has no model."
  (assert-active ctx)
  (with-core-state (cs ctx) (cs-model-provider cs)))

(defun ctx-model-qualified (ctx)
  "The provider/id string ctx-set-model takes; just the id when the provider is unknown."
  (assert-active ctx)
  (with-core-state (cs ctx)
    (if (plusp (length (cs-model-provider cs))) (format nil "~A/~A" (cs-model-provider cs) (cs-model cs)) (cs-model cs))))

(defun ctx-session-name (ctx)
  "The session's name (\"\" while it has none), replicated from the host's state pushes."
  (assert-active ctx)
  (with-core-state (cs ctx) (cs-session-name cs)))

(defun ctx-get-session-name (ctx) (optional-string-field ctx "getSessionName" "name"))
(defun ctx-get-session-file (ctx) (optional-string-field ctx "getSessionFile" "sessionFile"))

(defun ctx-extension-source-info (ctx)
  "The provenance the host stamped on this extension (path, source, scope, origin, baseDir), or NIL."
  (with-core-state (cs ctx) (and (cs-source-info cs) (clone-stream-value (cs-source-info cs)))))

(defun ctx-get-settings (ctx)
  "A copy of the effective settings the host sent. Signals until it has sent them."
  (let ((settings (with-core-state (cs ctx) (cs-settings cs))))
    (unless settings (error "settings are not available: the host has not sent them"))
    (clone-stream-value settings)))

;;; ---------------------------------------------------------------- the pig config root

(defun core-home-directory ()
  (let ((home (sb-posix:getenv "HOME")))
    (unless (and home (plusp (length home))) (error "locate home directory: home environment variable is not defined"))
    home))

(defun core-clean-path (path)
  "Go's filepath.Clean for slash-separated paths."
  (let* ((rooted (and (plusp (length path)) (char= (char path 0) #\/))) (out '()) (start 0))
    (loop for slash = (position #\/ path :start start)
          do (let ((part (subseq path start slash)))
               (cond ((or (string= part "") (string= part ".")))
                     ((string= part "..")
                      (cond ((and out (string/= (car out) "..")) (pop out))
                            ((not rooted) (push ".." out))))
                     (t (push part out))))
             (if slash (setf start (1+ slash)) (return)))
    (let ((joined (format nil "~{~A~^/~}" (reverse out))))
      (cond (rooted (concatenate 'string "/" joined))
            ((string= joined "") ".")
            (t joined)))))

(defun core-expand-home (path)
  (cond ((string= path "~") (core-home-directory))
        ((and (>= (length path) 2) (string= path "~/" :end1 2))
         (core-clean-path (concatenate 'string (core-home-directory) "/" (subseq path 2))))
        (t path)))

(defun ctx-config-home (ctx)
  "The pig config root: PIG_HOME, else XDG_CONFIG_HOME/pig, else ~/.pig. An empty variable falls through; a leading ~ or ~/ expands."
  (declare (ignore ctx))
  (let ((pig-home (sb-posix:getenv "PIG_HOME")) (xdg (sb-posix:getenv "XDG_CONFIG_HOME")))
    (cond ((and pig-home (plusp (length pig-home))) (core-expand-home pig-home))
          ((and xdg (plusp (length xdg))) (core-clean-path (concatenate 'string (core-expand-home xdg) "/pig")))
          (t (core-clean-path (concatenate 'string (core-home-directory) "/.pig"))))))

;;; ---------------------------------------------------------------- tool arguments in JavaScript's order

(defun core-array-index-p (key)
  (or (string= key "0")
      (and (plusp (length key)) (<= (length key) 10) (every (lambda (c) (char<= #\0 c #\9)) key)
           (char/= (char key 0) #\0) (<= (parse-integer key) 4294967294))))

(defun core-js-ordered (value)
  "VALUE with every object's members in the order JavaScript's Object.keys gives: array-index keys ascending, then the others in insertion order."
  (cond ((hash-table-p value)
         (let ((keys '()) (copy (make-hash-table :test 'equal)))
           (maphash (lambda (k v) (declare (ignore v)) (push k keys)) value)
           (setf keys (nreverse keys))
           (let ((indexes (sort (remove-if-not #'core-array-index-p keys) #'< :key #'parse-integer)))
             (dolist (k (append indexes (remove-if #'core-array-index-p keys)))
               (setf (gethash k copy) (core-js-ordered (gethash k value)))))
           copy))
        ((and (vectorp value) (not (stringp value))) (map 'simple-vector #'core-js-ordered value))
        (t value)))

(defun ctx-tool-call-index (ctx)
  "The running tool call's position among the tool calls of the assistant message that issued it, or NIL (outside a tool handler,
and for a call no assistant message issued, such as a nested ctx-execute-tool call)."
  (ctx-tool-index ctx))

(defun ctx-tool-input (ctx)
  "The running tool call's arguments as the JSON text JSON.stringify gives for the object Pi hands execute, or NIL outside a tool handler."
  (and (ctx-tool-args ctx) (to-json (core-js-ordered (ctx-tool-args ctx)))))

(defun ctx-tool-params (ctx)
  "The running tool call's arguments with every object in Object.keys order, or NIL outside a tool handler or when they are not an object."
  (and (hash-table-p (ctx-tool-args ctx)) (core-js-ordered (ctx-tool-args ctx))))

;;; ---------------------------------------------------------------- background work owned by the connection

(defun core-spawn (ext name function)
  "Run FUNCTION on a thread the connection owns: shutdown waits for it."
  (sb-thread:with-mutex ((extension-state-lock ext))
    (let ((thread (sb-thread:make-thread
                   (lambda ()
                     (unwind-protect (funcall function)
                       (sb-thread:with-mutex ((extension-state-lock ext))
                         (setf (extension-request-threads ext) (remove sb-thread:*current-thread* (extension-request-threads ext))))))
                   :name name)))
      (push thread (extension-request-threads ext))
      thread)))

(defun core-cancel-later (ext method args)
  "Send the cancel METHOD of a call from a thread of its own, so the signal's setter never waits for the host."
  (core-spawn ext "pig-cancel" (lambda () (handler-case (host-call ext method args) (error () nil)))))

(defmacro core-reporting-blocked ((ctx) &body body)
  (let ((e (gensym "EXT")) (i (gensym "ID")) (c (gensym "CTX")))
    `(let* ((,c ,ctx) (,e (ctx-extension ,c)) (,i (ctx-request-id ,c)))
       (when (plusp (length ,i)) (send-request-state ,e ,i "blocked" "host_call" (ctx-request ,c)))
       (unwind-protect (progn ,@body)
         (when (plusp (length ,i)) (send-request-state ,e ,i "progress" nil (ctx-request ,c)))))))

(defun core-exec-with-signal (ctx call-args signal)
  "exec with an abort-signal the extension owns: the call is not tied to the calling request, and the signal's abort reaches the host as exec.cancel."
  (let* ((ext (ctx-extension ctx))
         (id (let ((cs (core-state ext))) (sb-thread:with-mutex ((cs-lock cs)) (format nil "exec-~D" (incf (cs-exec-seq cs))))))
         (payload (obj)) (unsubscribe nil))
    (maphash (lambda (k v) (setf (gethash k payload) v)) call-args)
    (setf (gethash "execId" payload) id (gethash "ownSignal" payload) t)
    (core-reporting-blocked (ctx)
      (unwind-protect
           (let ((*after-call-sent*
                   (lambda ()
                     (setf unsubscribe (abort-signal-subscribe signal (lambda () (core-cancel-later ext "exec.cancel" (obj "execId" id))))))))
             (host-call ext "exec" payload))
        (when unsubscribe (funcall unsubscribe))))))

(defun core-compact-async (ctx args on-complete on-error)
  (setf (gethash "awaitCompletion" args) t)
  (let ((ext (ctx-extension ctx)))
    (core-spawn ext "pig-compact"
                (lambda ()
                  (handler-case (let ((result (host-call ext "compact" args)))
                                  (when on-complete (funcall on-complete (if (hash-table-p result) result (obj)))))
                    (error (c) (when on-error (funcall on-error c))))))))

;;; ---------------------------------------------------------------- nested calls with partial results

(defstruct (nested (:constructor make-nested ()))
  (lock (sb-thread:make-mutex :name "pig nested")) (queue (sb-thread:make-waitqueue))
  (updates '()) (done nil) (closed nil))

(defstruct (nested-update (:constructor make-nested-update (partial))) partial (latch (make-latch)) (failure nil))

(defun nested-submit (nested partial)
  "Queue one partial result and wait until the caller's on-update ran for it: the condition it signalled, or NIL. A call that returned drops it."
  (let ((update (make-nested-update partial)))
    (sb-thread:with-mutex ((nested-lock nested))
      (when (nested-closed nested) (return-from nested-submit nil))
      (setf (nested-updates nested) (append (nested-updates nested) (list update)))
      (sb-thread:condition-broadcast (nested-queue nested)))
    (latch-wait (nested-update-latch update))
    (nested-update-failure update)))

(defun nested-finish (nested)
  (sb-thread:with-mutex ((nested-lock nested))
    (setf (nested-done nested) t)
    (sb-thread:condition-broadcast (nested-queue nested))))

(defun nested-deliver (nested on-update)
  "Run ON-UPDATE for each partial result until the call finished; the first condition it signalled (a raising callback still sees every later one)."
  (let ((failure nil))
    (loop
      (multiple-value-bind (batch done)
          (sb-thread:with-mutex ((nested-lock nested))
            (loop while (and (null (nested-updates nested)) (not (nested-done nested)))
                  do (sb-thread:condition-wait (nested-queue nested) (nested-lock nested)))
            (let ((batch (nested-updates nested)) (done (nested-done nested)))
              (setf (nested-updates nested) '())
              (when done (setf (nested-closed nested) t))
              (values batch done)))
        (dolist (update batch)
          (handler-case (when on-update (funcall on-update (nested-update-partial update)))
            (error (c) (setf (nested-update-failure update) c) (unless failure (setf failure c))))
          (latch-set (nested-update-latch update)))
        (when done (return failure))))))

(defun call-nested (ctx method payload execute-id on-update &key signal cancel-method cancel-args)
  "Call METHOD with PAYLOAD, which names the call EXECUTE-ID, and return its result. The host answers each partial result of the call with
the request execute_tool_update; ON-UPDATE, (lambda (partial)), runs for each on this thread, in order, before this returns. A condition ON-UPDATE
signalled is signalled from here once the call ended. With SIGNAL (an abort-signal) the call is not tied to the calling request, and
setting the signal sends CANCEL-METHOD with CANCEL-ARGS."
  (let* ((ext (ctx-extension ctx)) (cs (core-state ext)) (nested (make-nested)) (result nil) (call-failure nil) (unsubscribe nil)
         (parent (ctx-request ctx)) (request-id (ctx-request-id ctx)))
    (sb-thread:with-mutex ((cs-lock cs)) (setf (gethash execute-id (cs-nested cs)) nested))
    (core-reporting-blocked (ctx)
      (unwind-protect
           (progn
             (core-spawn ext "pig-nested-call"
                         (lambda ()
                           (unwind-protect
                                (handler-case
                                    (let ((*after-call-sent*
                                            (and signal cancel-method
                                                 (lambda ()
                                                   (setf unsubscribe
                                                         (abort-signal-subscribe
                                                          signal (lambda () (core-cancel-later ext cancel-method cancel-args))))))))
                                      (setf result (if signal
                                                       (host-call ext method payload)
                                                       (host-call ext method payload parent request-id))))
                                  (serious-condition (c) (setf call-failure c)))
                             (nested-finish nested))))
             (let ((failure (nested-deliver nested on-update)))
               (cond (failure (error failure))
                     (call-failure (error call-failure))
                     (t result))))
        (when unsubscribe (funcall unsubscribe))
        (sb-thread:with-mutex ((cs-lock cs)) (remhash execute-id (cs-nested cs)))))))

(def-request "execute_tool_update" (ext ctx req)
  (let* ((args (or (jget req "args") (obj)))
         (nested (let ((cs (core-state ext)))
                   (sb-thread:with-mutex ((cs-lock cs)) (gethash (let ((id (jget args "executeId"))) (if (stringp id) id "")) (cs-nested cs)))))
         (failure (and nested (nested-submit nested (or (jget args "result") (obj))))))
    (when failure (error "~A" (condition-message failure)))
    nil))

(defun core-require-tool-context (ctx what)
  (unless (and (ctx-tool-context-p ctx) (ctx-tool-call-id ctx) (plusp (length (ctx-request-id ctx))))
    (error "~A is only available while a tool runs" what)))

(defun ctx-tools (ctx)
  "The tools ctx-execute-tool can call, as AgentTool hash-tables. Only available while a tool runs."
  (core-require-tool-context ctx "ctx-tools")
  (reply-field ctx "getCallableTools" "tools"))

(defun ctx-execute-tool (ctx name args &key signal on-update)
  "Run another tool for the calling tool; the outcome is Pi's AgentToolCallOutcome (toolCall, result, isError, durationMs).
Tool failures come back with isError true; a transport failure or the cancellation of the calling request signals. SIGNAL (an abort-signal)
replaces the calling tool's cancellation; ON-UPDATE, (lambda (partial)), receives each partial result of the nested tool, on this thread, in order.
Only available while a tool runs."
  (core-require-tool-context ctx "ctx-execute-tool")
  (ensure-jsonable args (format nil "arguments of tool ~A" name))
  (when (and on-update (not (functionp on-update))) (error 'type-error :datum on-update :expected-type 'function))
  (when (and signal (not (abort-signal-p signal))) (error 'type-error :datum signal :expected-type 'abort-signal))
  (let* ((cs (core-state (ctx-extension ctx)))
         (execute-id (sb-thread:with-mutex ((cs-lock cs)) (format nil "e~D" (incf (cs-execute-seq cs)))))
         (payload (obj "callerId" (ctx-tool-call-id ctx) "name" name "args" args "executeId" execute-id)))
    (when on-update (setf (gethash "wantsUpdates" payload) t))
    (when signal (setf (gethash "ownSignal" payload) t))
    (or (call-nested ctx "executeTool" payload execute-id on-update
                     :signal signal :cancel-method "executeTool.cancel" :cancel-args (obj "executeId" execute-id))
        (obj))))

;;; ---------------------------------------------------------------- thin host calls

(defun ctx-set-editor-text (ctx text) (ctx-call-host ctx "ui.setEditorText" (obj "text" text)) nil)
(defun ctx-paste-to-editor (ctx text) (ctx-call-host ctx "ui.pasteToEditor" (obj "text" text)) nil)
(defun ctx-refresh-tools (ctx) (ctx-call-host ctx "refreshTools") nil)

;;; ---------------------------------------------------------------- the session log

(defun core-read-session-entries (path)
  "The entries of the session file at PATH (every line but the session header); NIL when it cannot be read."
  (when (plusp (length path))
    (handler-case
        (with-open-file (in path :external-format :utf-8)
          (loop for line = (read-line in nil nil) while line
                do (when (> (length (sb-ext:string-to-octets line :external-format :utf-8)) +max-frame-size+)
                     (return-from core-read-session-entries nil))
                collect (from-json line) into entries
                finally (return (remove-if (lambda (e) (equal (jget e "type") "session")) entries))))
      (error () nil))))

(defun core-log-page (ext args)
  (let ((result (host-call ext "watchSessionLog" args)))
    (unless (hash-table-p result)
      (error 'host-call-error :message "host reply to watchSessionLog is not an object" :code "invalid_reply"))
    result))

(defun core-subscribe-session-log (ext cs)
  "Install the session log: read the file, then page the host's log in until it is complete."
  (let* ((entries (core-read-session-entries (sb-thread:with-mutex ((cs-lock cs)) (cs-session-file cs))))
         (cursor (length entries)) (leaf ""))
    (loop
      (let* ((requested cursor) (result (core-log-page ext (obj "cursor" cursor)))
             (page (let ((p (jget result "entries"))) (if (and (vectorp p) (not (stringp p))) (coerce p 'list) '())))
             (next (let ((c (jget result "entryCount"))) (if (integerp c) c cursor))))
        (when (/= (- next (length page)) requested) (setf entries '()))
        (setf entries (append entries page) cursor next)
        (let ((l (jget result "leafId"))) (when (and (stringp l) (plusp (length l))) (setf leaf l)))
        (unless (eq (jget result "hasMore") t) (return))))
    (sb-thread:with-mutex ((cs-lock cs))
      (when (plusp (length leaf)) (setf (cs-leaf-id cs) leaf))
      (when (zerop (length (cs-entries cs)))
        (cs-reset-log cs)
        (dolist (entry entries) (cs-add-entry cs entry))))
    (loop
      (let* ((result (core-log-page ext (obj "cursor" cursor "complete" t)))
             (page (let ((p (jget result "entries"))) (if (and (vectorp p) (not (stringp p))) p (vector)))))
        (setf cursor (let ((c (jget result "entryCount"))) (if (integerp c) c cursor)))
        (let ((l (jget result "leafId"))) (when (and (stringp l) (plusp (length l))) (setf leaf l)))
        (sb-thread:with-mutex ((cs-lock cs))
          (cs-apply-session cs (obj "entriesAppended" page "entryCount" cursor "leafId" leaf)))
        (when (and (not (eq (jget result "hasMore") t)) (zerop (length page))) (return))))))

(defun core-ensure-session-log (ctx)
  "Enrol this extension in session-log replication on its first read; blocks until the log is installed."
  (let* ((ext (ctx-extension ctx)) (cs (core-state ext)))
    (sb-thread:with-mutex ((cs-sub-lock cs))
      (cond ((cs-subscribed cs) (when (cs-sub-error cs) (error (cs-sub-error cs))))
            (t (sb-thread:with-mutex ((cs-lock cs)) (setf (cs-subscribed cs) t))
               (handler-case (core-subscribe-session-log ext cs)
                 (error (c) (setf (cs-sub-error cs) c) (error c))))))))

(defun ctx-get-entries (ctx)
  "Every entry of the session log (a vector), from the local mirror the host keeps current."
  (core-ensure-session-log ctx)
  (with-core-state (cs ctx) (coerce (cs-entries cs) 'simple-vector)))

(defun ctx-get-branch (ctx)
  "The entries from the root to the current leaf (a vector); the whole log when the session has no leaf."
  (core-ensure-session-log ctx)
  (with-core-state (cs ctx)
    (let ((entries (cs-entries cs)) (index (cs-index cs)))
      (if (or (zerop (length (cs-leaf-id cs))) (zerop (hash-table-count index)))
          (coerce entries 'simple-vector)
          (let ((path '()) (seen (make-hash-table :test 'equal)) (current (cs-leaf-id cs)))
            (loop while (and (plusp (length current)) (not (gethash current seen)))
                  do (setf (gethash current seen) t)
                     (let ((meta (gethash current index)))
                       (unless meta (return))
                       (push (aref entries (car meta)) path)
                       (setf current (cdr meta))))
            (coerce path 'simple-vector))))))

;;; ---------------------------------------------------------------- tool definitions

(defstruct (tool-loadout (:constructor make-tool-loadout (declared callable registered exposures namespaces prompt-guidelines)))
  "Pi's ToolLoadout: the tools of a session as a tool's prepare-loadout hook sees them (vectors of AgentTool hash-tables)."
  declared callable registered exposures namespaces prompt-guidelines)

(defun tool-loadout-get-exposure (loadout name)
  "The tool's exposure; a tool without a definition is \"direct\"."
  (let ((exposure (jget (tool-loadout-exposures loadout) name))) (if (stringp exposure) exposure "direct")))

(defun tool-loadout-get-namespace (loadout name)
  (let ((namespace (jget (tool-loadout-namespaces loadout) name))) (and (hash-table-p namespace) namespace)))

(defun tool-loadout-get-prompt-guidelines (loadout name)
  "The tool's promptGuidelines as the system prompt has them (trimmed, without duplicates); empty for a tool without any."
  (let ((guidelines (jget (tool-loadout-prompt-guidelines loadout) name))) (if (vectorp guidelines) guidelines (vector))))

(defparameter *core-tool-exposures* '("direct" "model-only" "codemode" "deferred" "hidden"))

(defun register-tool (ext name &key (label "") (description "") parameters execute prompt-snippet prompt-guidelines constrained-sampling
                                  (render-shell "default") prepare-arguments execution-mode output-schema exposure namespace annotations
                                  default-active prepare-loadout reaches-tools render-call render-result call-view result-view)
  "Register or replace a tool from a definition (Pi's ToolDefinition). EXECUTE is (lambda (ctx params)); PARAMETERS is the object JSON Schema.
PREPARE-ARGUMENTS is (lambda (args)); PREPARE-LOADOUT is (lambda (tool-loadout)) returning a changes hash-table or NIL; REACHES-TOOLS is (lambda (input))
returning \"all\" or a sequence of words. DEFAULT-ACTIVE is t or :false. A running session is refreshed before this returns."
  (unless (and (stringp name) (plusp (length name))) (error "register-tool requires a definition with name"))
  (unless (hash-table-p parameters)
    (error "Tool ~S registered by extension ~S must define an object parameter schema." name (extension-name ext)))
  (unless (member render-shell '("default" "self") :test #'equal) (error "render_shell must be 'default' or 'self'"))
  (unless (member execution-mode '(nil "sequential" "parallel") :test #'equal) (error "execution_mode must be 'sequential' or 'parallel'"))
  (when (and exposure (not (member exposure *core-tool-exposures* :test #'equal)))
    (error "exposure must be one of ~{~S~^, ~}" *core-tool-exposures*))
  (dolist (hook (list prepare-loadout reaches-tools execute))
    (when (and hook (not (functionp hook))) (error 'type-error :datum hook :expected-type 'function)))
  (let ((decl (obj "name" name "label" label "description" description "parameters" parameters)))
    (flet ((put (key value) (when value (setf (gethash key decl) value))))
      (put "prompt_snippet" prompt-snippet)
      (put "prompt_guidelines" (and prompt-guidelines (coerce prompt-guidelines 'simple-vector)))
      (put "execution_mode" execution-mode)
      (put "constrained_sampling" constrained-sampling)
      (setf (gethash "render_shell" decl) render-shell)
      (put "renders_call" (and (or render-call call-view) t))
      (put "renders_result" (and (or render-result result-view) t))
      (put "output_schema" output-schema)
      (put "exposure" exposure)
      (put "namespace" namespace)
      (put "annotations" annotations)
      (put "default_active" default-active)
      (put "prepares_loadout" (and prepare-loadout t))
      (put "reaches_tools" (and reaches-tools t))
      (put "prepares_arguments" (and prepare-arguments t)))
    (ensure-jsonable decl (format nil "tool definition for ~A" name))
    (install-tool ext decl execute
                  (append (and prepare-arguments (list :prepare prepare-arguments))
                          (and prepare-loadout (list :loadout prepare-loadout))
                          (and reaches-tools (list :reaches reaches-tools))
                          (and render-call (list :render-call render-call))
                          (and render-result (list :render-result render-result))
                          (and call-view (list :call-view call-view))
                          (and result-view (list :result-view result-view))))))

(defun ctx-register-tool (ctx name &rest keys)
  "register-tool on the context's extension: replace a tool in place and refresh a running session before returning."
  (apply #'register-tool (ctx-extension ctx) name keys))

(defun core-tool-hook (ext name key what)
  (or (getf (sb-thread:with-mutex ((extension-reg-lock ext)) (gethash name (extension-tool-hooks ext))) key)
      (error "tool ~S has no ~A" name what)))

(def-request "tool_prepare_arguments" (ext ctx req)
  (let* ((name (jget req "tool" "")) (prepared (funcall (core-tool-hook ext name :prepare "prepare_arguments") (or (jget req "args") (obj)))))
    (unless (hash-table-p prepared) (error 'type-error :datum prepared :expected-type 'hash-table))
    prepared))

(def-request "tool_reaches_tools" (ext ctx req)
  (let* ((name (jget req "tool" "")) (reach (funcall (core-tool-hook ext name :reaches "reaches_tools") (or (jget req "args") (obj)))))
    (cond ((equal reach "all") "all")
          ((and (listp reach) (every #'stringp reach)) (coerce reach 'simple-vector))
          ((and (vectorp reach) (not (stringp reach)) (every #'stringp reach)) reach)
          (t (error "reaches_tools must return \"all\" or a sequence of strings")))))

(def-request "tool_prepare_loadout" (ext ctx req)
  (let* ((name (jget req "tool" "")) (hook (core-tool-hook ext name :loadout "prepare_loadout")) (payload (or (jget req "args") (obj))))
    (flet ((field (key default) (let ((v (jget payload key))) (if (and v (not (eq v :null))) v default))))
      (let ((changes (funcall hook (make-tool-loadout (field "declared" (vector)) (field "callable" (vector)) (field "registered" (vector))
                                                      (field "exposures" (obj)) (field "namespaces" (obj)) (field "promptGuidelines" (obj))))))
        (unless (or (null changes) (hash-table-p changes)) (error 'type-error :datum changes :expected-type 'hash-table))
        changes))))

(def-request "command_argument_completions" (ext ctx req)
  (let* ((name (jget req "tool" "")) (prefix (jget req "args"))
         (handler (gethash (list :completions name) (extension-command-handlers ext))))
    (unless handler (error "unknown command ~S" name))
    (let ((items (funcall handler (if (stringp prefix) prefix ""))))
      (and items (plusp (length items)) (coerce items 'simple-vector)))))

(defun on-project-trust (ext handler)
  "Register an awaited pre-runtime project_trust handler, (lambda (ctx event)); returns its unsubscribe function."
  (on ext "project_trust" handler))

;;; ---------------------------------------------------------------- user_bash operations

(defstruct (bash-operations (:constructor make-bash-operations (exec)))
  "Pi's BashOperations: pluggable command execution. EXEC is (lambda (command cwd options)) taking a bash-exec-options and returning
the exit code or a hash-table with exitCode; signal an error to reject (with the message \"aborted\" when the signal was set)."
  exec)

(defstruct (bash-exec-options (:constructor make-bash-exec-options (on-data signal timeout env)))
  "What operations exec receives: ON-DATA, (lambda (chunk)) with a string or octets, streams the output in order; SIGNAL is set
when the host aborts the command; TIMEOUT is in seconds; ENV, when not NIL, replaces the inherited environment."
  on-data signal timeout env)

(defvar *bash-ops* (make-hash-table :test 'equal :synchronized t))
(defvar *bash-ops-seq* (list 0))

(defun bash-ops-register (operations &optional handle)
  "Keep OPERATIONS until the host releases it; the handle (given, or new) names it on the wire."
  (let ((handle (or handle (format nil "bash-~D" (1+ (sb-ext:atomic-incf (car *bash-ops-seq*)))))))
    (setf (gethash handle *bash-ops*) operations)
    handle))

(defun bash-ops-get (handle) (values (gethash handle *bash-ops*)))

(defun core-bash-exec-function (operations)
  (cond ((bash-operations-p operations) (bash-operations-exec operations))
        ((hash-table-p operations) (let ((f (jget operations "exec"))) (and (functionp f) f)))))

(defun user-bash-operations-reply (value)
  "The reply naming the operations a user_bash handler returned, kept here until the host releases them; NIL for any other result."
  (when (and (hash-table-p value) (null (jget value "result")) (nth-value 1 (gethash "operations" value))
             (core-bash-exec-function (jget value "operations")))
    (obj "operations" (obj "handle" (bash-ops-register (jget value "operations"))))))

(defun core-base64 (octets)
  (let ((alphabet "ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz0123456789+/") (n (length octets)))
    (with-output-to-string (out)
      (loop for i from 0 below n by 3
            do (let* ((b0 (aref octets i)) (b1 (if (< (+ i 1) n) (aref octets (+ i 1)) 0)) (b2 (if (< (+ i 2) n) (aref octets (+ i 2)) 0))
                      (word (logior (ash b0 16) (ash b1 8) b2)))
                 (write-char (char alphabet (ldb (byte 6 18) word)) out)
                 (write-char (char alphabet (ldb (byte 6 12) word)) out)
                 (write-char (if (< (+ i 1) n) (char alphabet (ldb (byte 6 6) word)) #\=) out)
                 (write-char (if (< (+ i 2) n) (char alphabet (ldb (byte 6 0) word)) #\=) out))))))

(def-request "user_bash_exec" (ext ctx req)
  (let* ((handle (jget req "tool" "")) (operations (bash-ops-get handle)) (exec (and operations (core-bash-exec-function operations)))
         (args (or (jget req "args") (obj))) (finished nil) (request-id (ctx-request-id ctx)))
    (unless exec (error "unknown bash operations: ~A" handle))
    (flet ((on-data (chunk)
             (unless finished
               (notify-host ext "tool_update"
                            (obj "request_id" request-id
                                 "result" (obj "data" (core-base64 (if (stringp chunk) (sb-ext:string-to-octets chunk :external-format :utf-8)
                                                                       chunk))))))))
      (let ((result (unwind-protect
                         (funcall exec (jget args "command" "") (jget args "cwd" "")
                                  (make-bash-exec-options #'on-data (ctx-cancel ctx) (let ((tm (jget args "timeout"))) (and (realp tm) tm))
                                                          (let ((env (jget args "env"))) (and (hash-table-p env) env))))
                      (setf finished t))))
        (obj "exitCode" (if (hash-table-p result) (jget result "exitCode") result))))))

(def-notify "bash_operations_release" (ext args)
  (remhash (jget args "handle" "") *bash-ops*))
