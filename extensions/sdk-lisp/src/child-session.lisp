;;;; SPDX-License-Identifier: MIT
;;;; Child agent sessions: Pi's createAgentSession for an extension. The host owns each session for the connection and runs the agent
;;;; loop; the extension prompts it, listens to its events, aborts and disposes it, and serves the custom tools the session calls.
;;;;
;;;; Async contract (Pi's promises, kept as blocking calls): ctx-create-agent-session returns once resources are loaded and the model
;;;; resolved; agent-session-prompt returns when the run has settled, after every listener ran through its last event, and a rejected
;;;; prompt signals host-call-error; agent-session-abort returns once the session is idle; agent-session-dispose is idempotent.
;;;; Listeners run in event order on one thread of the session, never on the thread that reads the host, so a listener may call the host
;;;; or the session. The session ends with the connection: its run is aborted and no listener runs afterwards.

(in-package #:pig)

(defstruct (child-registry (:constructor child-make-registry ()))
  (lock (sb-thread:make-mutex :name "pig child sessions"))
  (by-id (make-hash-table :test 'equal))
  (closed nil)
  (next 0))

(defun child-registry-of (ext) (extension-extra ext :child-sessions #'child-make-registry))

(defstruct (agent-session (:constructor child-make-session (extension id ctx)))
  "A child agent session. Every reader (agent-session-session-id, agent-session-model, ...) asks the host for the session's current value."
  extension id ctx
  (tools (make-hash-table :test 'equal))
  (lock (sb-thread:make-mutex :name "pig child session"))
  (queue (sb-thread:make-waitqueue))
  (listeners '()) (pending '()) (delivered 0)
  (replica nil)                         ; (seq . payload) of the message_update the host last sent; the reader thread owns it
  (stopped nil) (running nil) (disposed nil) (thread nil)
  (model-fallback-message nil) (extension-errors #()))

(defun child-lookup (ext id)
  (let ((registry (child-registry-of ext)))
    (sb-thread:with-mutex ((child-registry-lock registry)) (gethash id (child-registry-by-id registry)))))

(setf *child-tool-handler*
      (lambda (ext id name)
        (let ((session (child-lookup ext id))) (and session (gethash name (agent-session-tools session))))))

;;; ---------------------------------------------------------------- options

(defstruct (child-tool (:constructor child-tool (name description schema handler &key label prompt-snippet prompt-guidelines execution-mode)))
  "A custom tool of a child session: HANDLER is (lambda (ctx params)) as a registered tool's; SCHEMA is the object parameter schema."
  name description schema handler label prompt-snippet prompt-guidelines execution-mode)

(defun make-resource-options (&key no-extensions no-skills no-prompt-templates no-themes no-context-files
                                additional-extension-paths additional-skill-paths additional-prompt-template-paths
                                extension-names skill-names system-prompt append-system-prompt)
  "The DefaultResourceLoader options of Pi that select the resources of a child session, NIL when none is given. A list is a set of names or
paths; NIL is Pi's undefined and an empty vector selects nothing (for EXTENSION-NAMES and SKILL-NAMES)."
  (let ((wire (obj)))
    (loop for (flag key) in `((,no-extensions "noExtensions") (,no-skills "noSkills") (,no-prompt-templates "noPromptTemplates")
                              (,no-themes "noThemes") (,no-context-files "noContextFiles"))
          do (when flag (setf (gethash key wire) t)))
    (loop for (value key) in `((,additional-extension-paths "additionalExtensionPaths") (,additional-skill-paths "additionalSkillPaths")
                               (,additional-prompt-template-paths "additionalPromptTemplatePaths") (,extension-names "extensionNames")
                               (,skill-names "skillNames") (,system-prompt "systemPrompt") (,append-system-prompt "appendSystemPrompt"))
          do (when value (setf (gethash key wire) (if (listp value) (coerce value 'simple-vector) value))))
    (and (plusp (hash-table-count wire)) wire)))

(defun make-session-start-event (reason &optional previous-session-file)
  "The session_start event a session's extensions receive when they bind: REASON is \"startup\", \"reload\", \"new\", \"resume\" or \"fork\"."
  (let ((event (obj "reason" reason)))
    (when previous-session-file (setf (gethash "previousSessionFile" event) previous-session-file))
    event))

(defun child-declaration (tool extension-name)
  (unless (hash-table-p (child-tool-schema tool))
    (error "Tool ~S registered by extension ~S must define an object parameter schema." (child-tool-name tool) extension-name))
  (ensure-jsonable (child-tool-schema tool) (format nil "tool schema for ~A" (child-tool-name tool)))
  (let ((decl (obj "name" (child-tool-name tool) "label" (or (child-tool-label tool) (child-tool-name tool))
                   "description" (child-tool-description tool) "parameters" (child-tool-schema tool))))
    (when (child-tool-prompt-snippet tool) (setf (gethash "prompt_snippet" decl) (child-tool-prompt-snippet tool)))
    (when (child-tool-prompt-guidelines tool)
      (setf (gethash "prompt_guidelines" decl) (coerce (child-tool-prompt-guidelines tool) 'simple-vector)))
    (when (child-tool-execution-mode tool) (setf (gethash "execution_mode" decl) (child-tool-execution-mode tool)))
    decl))

;;; ---------------------------------------------------------------- creation

(defun ctx-create-agent-session (ctx &key cwd agent-dir model thinking-level tools exclude-tools no-tools custom-tools settings in-memory
                                       resources session-manager settings-manager resource-loader model-runtime scoped-models session-start-event)
  "Pi's createAgentSession: create a child agent session in the host and return once it is ready. The child shares the host's model runtime.
CWD and AGENT-DIR default to the host's; MODEL is a model object (ctx-get-model-info, registry-find); TOOLS and EXCLUDE-TOOLS are name or `*` pattern
lists (an empty vector allows nothing); NO-TOOLS is \"all\" or \"builtin\"; CUSTOM-TOOLS are child-tool objects this extension serves; SETTINGS
are in-memory settings; IN-MEMORY never writes the session log; RESOURCES is make-resource-options; SESSION-MANAGER, SETTINGS-MANAGER,
RESOURCE-LOADER and MODEL-RUNTIME are the objects this extension built, which the session uses (each replaces the option that builds its own);
SCOPED-MODELS is a list of {model, thinkingLevel}; SESSION-START-EVENT is make-session-start-event."
  (let* ((ext (ctx-extension ctx)) (registry (child-registry-of ext)) (declarations '())
         (session (child-make-session ext (sb-thread:with-mutex ((child-registry-lock registry))
                                            (format nil "c~D" (incf (child-registry-next registry))))
                                      (make-ctx ext)))
         (args (obj "id" (agent-session-id session))))
    (loop for (value key) in `((,cwd "cwd") (,agent-dir "agentDir") (,model "model") (,thinking-level "thinkingLevel")
                               (,no-tools "noTools") (,settings "settings") (,resources "resources")
                               (,session-start-event "sessionStartEvent"))
          do (when value (setf (gethash key args) value)))
    (loop for (value key) in `((,tools "tools") (,exclude-tools "excludeTools") (,scoped-models "scopedModels"))
          do (when value (setf (gethash key args) (coerce value 'simple-vector))))
    (when in-memory (setf (gethash "inMemory" args) t))
    (loop for (value key) in `((,session-manager "sessionManager") (,settings-manager "settingsManager")
                               (,resource-loader "resourceLoader") (,model-runtime "modelRuntime"))
          do (when value (setf (gethash key args) (gobj-handle value))))
    (dolist (tool custom-tools)
      (push (child-declaration tool (extension-name ext)) declarations)
      (setf (gethash (child-tool-name tool) (agent-session-tools session)) (child-tool-handler tool)))
    (when declarations (setf (gethash "customTools" args) (coerce (nreverse declarations) 'simple-vector)))
    ;; the tools are served from the moment the host knows the session, which can be before the call returns
    (sb-thread:with-mutex ((child-registry-lock registry))
      (when (child-registry-closed registry) (error "Extension connection is closed"))
      (setf (gethash (agent-session-id session) (child-registry-by-id registry)) session))
    (let ((info (handler-bind ((error (lambda (e) (declare (ignore e))
                                        (sb-thread:with-mutex ((child-registry-lock registry))
                                          (remhash (agent-session-id session) (child-registry-by-id registry))))))
                  (or (ctx-call-host ctx "childSession.create" args) (obj)))))
      (flet ((present (key) (let ((v (jget info key))) (and (stringp v) (plusp (length v)) v)))
             (vec (key) (let ((v (jget info key))) (if (and (vectorp v) (not (stringp v))) v #()))))
        (setf (agent-session-model-fallback-message session) (present "modelFallbackMessage")
              (agent-session-extension-errors session) (vec "extensionErrors"))))
    session))

;;; ---------------------------------------------------------------- calls

(defun child-call-args (session method args)
  "A host call that belongs to the session, not to the request that happens to use it: the session outlives the command that created it."
  (setf (gethash "id" args) (agent-session-id session))
  (host-call (agent-session-extension session) method args))

(defun child-call (session method &rest members)
  (child-call-args session method (apply #'obj members)))

(defun child-wait-delivered (session seq)
  (sb-thread:with-mutex ((agent-session-lock session))
    (loop while (and (< (agent-session-delivered session) seq) (not (agent-session-stopped session)))
          do (sb-thread:condition-wait (agent-session-queue session) (agent-session-lock session)))))

(defun child-images (images args)
  (when (and images (plusp (length images))) (setf (gethash "images" args) (coerce images 'simple-vector)))
  args)

(defun agent-session-prompt (session text &key expand-prompt-templates images streaming-behavior source)
  "Run the agent on TEXT and return when the run has settled, after the listeners saw its last event. A rejected prompt signals
host-call-error. STREAMING-BEHAVIOR is \"steer\" or \"followUp\"; a prompt to a running session needs it. EXPAND-PROMPT-TEMPLATES is T or :false."
  (let ((args (child-images images (obj "text" text))))
    (unless (null expand-prompt-templates)
      (setf (gethash "expandPromptTemplates" args) (if (eq expand-prompt-templates :false) :false t)))
    (when streaming-behavior (setf (gethash "streamingBehavior" args) streaming-behavior))
    (when source (setf (gethash "source" args) source))
    (let ((result (or (child-call-args session "childSession.prompt" args) (obj))))
      (child-wait-delivered session (sess-int (jget result "events") 0))
      nil)))

(defun child-queue (session behavior text images)
  (child-call-args session "childSession.queue" (child-images images (obj "behavior" behavior "text" text)))
  nil)

(defun agent-session-steer (session text &optional images)
  "Queue TEXT for the running session after the current turn's tool calls, before its next model call."
  (child-queue session "steer" text images))

(defun agent-session-follow-up (session text &optional images)
  "Queue TEXT for after the run's current work finishes."
  (child-queue session "followUp" text images))

(defun agent-session-abort (session)
  "Abort the run and return once the session is idle. An aborted run settles normally, so its prompt returns."
  (child-call session "childSession.abort") nil)

(defun agent-session-bind-extensions (session)
  "The extensions the session's resources loaded start, headless, and receive session_start."
  (child-call session "childSession.bindExtensions") nil)

(defun agent-session-state (session)
  "The session's current state: sessionId, sessionFile, model, isStreaming, isIdle, thinkingLevel, activeTools, systemPrompt,
pendingMessageCount and lastAssistantText (absent when there is none)."
  (or (child-call session "childSession.state") (obj)))

(defun agent-session-messages (session)
  "The transcript the session holds, in Pi's message shape."
  (let ((messages (child-call session "childSession.messages"))) (if (vectorp messages) messages (vector))))

(defun agent-session-refresh-context (session)
  "The session's finalized transcript is read again from its log, after the caller appended entries to the log agent-session-session-manager returns."
  (child-call session "childSession.refreshContext") nil)

(defun agent-session-session-manager (session)
  "The log the session appends to: the very one a session manager passed to ctx-create-agent-session names."
  (let* ((reply (child-call session "childSession.sessionManager"))
         (handle (and (hash-table-p reply) (jget reply "handle"))))
    (unless (and (stringp handle) (plusp (length handle))) (error "host returned no session graph handle"))
    (%make-sm (agent-session-ctx session) handle)))

(defun child-emit-event (session event)
  "(values result emitted-p): whether an extension of the session has a handler for EVENT's type, and its result for EVENT."
  (let ((reply (or (child-call session "childSession.emit" "event" event) (obj))))
    (values (let ((r (jget reply "result"))) (if (eq r :null) nil r)) (eq (jget reply "emitted") t))))

;;; ---------------------------------------------------------------- events

(defun agent-session-subscribe (session listener)
  "Call LISTENER with each event of the session (Pi's AgentSessionEvent as a hash-table, shared by the listeners) in order until the returned
function unsubscribes it, the session is disposed or the connection ends."
  (sb-thread:with-mutex ((agent-session-lock session))
    (setf (agent-session-listeners session) (append (agent-session-listeners session) (list listener))))
  (lambda ()
    (sb-thread:with-mutex ((agent-session-lock session))
      (setf (agent-session-listeners session) (remove listener (agent-session-listeners session) :count 1)))))

(defun child-stop (session)
  "End the delivery: queued events are dropped and no listener runs again."
  (sb-thread:with-mutex ((agent-session-lock session))
    (setf (agent-session-disposed session) t (agent-session-stopped session) t
          (agent-session-listeners session) '() (agent-session-pending session) '())
    (sb-thread:condition-broadcast (agent-session-queue session))))

(defun agent-session-dispose (session)
  "Abort the run, release the session in the host and remove its listeners. Idempotent; a failure is reported in the extension's log, as Pi's dispose never fails."
  (unless (sb-thread:with-mutex ((agent-session-lock session))
            (prog1 (agent-session-disposed session) (setf (agent-session-disposed session) t)))
    (child-stop session)
    (handler-case (child-call session "childSession.dispose")
      (error (e) (diagnose "pig: dispose of child session ~A failed: ~A~%" (agent-session-id session) e)))))

(defun child-apply-update (session stream keyframe)
  "The full message_update event the stream member produces, or NIL when it cannot be: the event is then not delivered, but its number still counts."
  (handler-case
      (let ((root (if keyframe
                      keyframe
                      (let ((base (sess-int (jget stream "base") 0)) (replica (agent-session-replica session)))
                        (unless (and replica (eql (car replica) base))
                          (error "message_update delta applies to payload ~D, the replica holds a different one" base))
                        (apply-stream-ops (cdr replica) (jget stream "ops"))))))
        (setf (agent-session-replica session) (cons (sess-int (jget stream "seq") 0) root))
        (clone-stream-value root))
    (error (e)
      (setf (agent-session-replica session) nil)
      (diagnose "pig: child session ~A: ~A~%" (agent-session-id session) e)
      nil)))

(defun child-deliver-loop (session)
  (loop
    (let (seq event listeners)
      (sb-thread:with-mutex ((agent-session-lock session))
        (when (or (agent-session-stopped session) (null (agent-session-pending session)))
          (setf (agent-session-running session) nil)
          (return))
        (destructuring-bind (s . e) (pop (agent-session-pending session))
          (setf seq s event e listeners (copy-list (agent-session-listeners session)))))
      (when event
        (dolist (listener listeners)
          (handler-case (funcall listener event)
            (error (e) (diagnose "pig: child session ~A listener raised: ~A~%" (agent-session-id session) e)))))
      (sb-thread:with-mutex ((agent-session-lock session))
        (setf (agent-session-delivered session) seq)
        (sb-thread:condition-broadcast (agent-session-queue session))))))

(defun child-enqueue (session seq event)
  (let ((start nil))
    (sb-thread:with-mutex ((agent-session-lock session))
      (unless (agent-session-stopped session)
        (setf (agent-session-pending session) (append (agent-session-pending session) (list (cons seq event))))
        (unless (agent-session-running session) (setf (agent-session-running session) t start t))))
    (when start
      (setf (agent-session-thread session)
            (sb-thread:make-thread #'child-deliver-loop :arguments (list session) :name (format nil "pig-child-session-~A" (agent-session-id session)))))))

(def-notify "childSession.event" (ext args)
  (let ((session (child-lookup ext (sess-str (jget args "id")))))
    (when session
      (let ((stream (jget args "stream")))
        (child-enqueue session (sess-int (jget args "seq") 0)
                       (if (hash-table-p stream) (child-apply-update session stream (jget args "event")) (jget args "event")))))))

(def-stop-hook "child-sessions" (ext)
  "End every session without telling the host, which disposes them as the connection closes."
  (let ((registry (child-registry-of ext)) (sessions '()))
    (sb-thread:with-mutex ((child-registry-lock registry))
      (setf (child-registry-closed registry) t)
      (maphash (lambda (id session) (declare (ignore id)) (push session sessions)) (child-registry-by-id registry))
      (clrhash (child-registry-by-id registry)))
    (dolist (session sessions) (child-stop session))
    (dolist (session sessions)
      (let ((thread (agent-session-thread session)))
        (when (and thread (not (eq thread sb-thread:*current-thread*)))
          (sb-thread:join-thread thread :default nil :timeout 2))))))

;;; ---------------------------------------------------------------- members of Pi's AgentSession
;;;
;;; Each member is the host's own AgentSession member (childSession.op): the value is read from, or the change made on, the session the host
;;; owns, so a call returns what the session holds now. Pi's undefined is NIL, a bool is T or NIL, a list is a vector, an object a hash-table.
;;; A member Pi declares `async` (agent-session-wait-for-idle, -reload, -compact, -navigate-tree, -execute-bash, -export-to-html,
;;; -summarize-for-bug-report, -set-model, -send-custom-message, -send-user-message) blocks until its Promise settles and signals host-call-error
;;; on rejection; it starts in call order and a later call from another thread (an abort) never waits for it. Given :CTX, a cancelled ctx runs Pi's
;;; abort for agent-session-compact, -navigate-tree and -execute-bash (abortCompaction, abortBranchSummary, abortBash); the other awaited members have
;;; no abort in Pi. Scope: a tool definition carries data, not its execute function or hooks; bash output chunks reach subscribers, not a callback.

(defun child-args (&rest members)
  "An argument object of the non-NIL members (alternating key and value)."
  (let ((args (obj)))
    (loop for (key value) on members by #'cddr do (when value (setf (gethash key args) value)))
    args))

(defun child-op (session op &optional args)
  (let ((payload (obj "op" op)))
    (when args (setf (gethash "args" payload) args))
    (child-call-args session "childSession.op" payload)))

(defun child-op-async (session op args &key abort ctx)
  (when (and ctx (ctx-cancelled-p ctx)) (error 'host-call-cancelled :message (format nil "~A: context cancelled" op)))
  (let ((payload (obj "op" op)) (done nil) (watcher nil))
    (when args (setf (gethash "args" payload) args))
    (when (and abort ctx)
      (setf watcher (sb-thread:make-thread
                     (lambda ()
                       (loop until (or done (ctx-cancelled-p ctx)) do (sleep 0.005))
                       (when (and (not done) (ctx-cancelled-p ctx))
                         (handler-case (child-op session abort)
                           (error (e) (unless (agent-session-disposed session)
                                        (diagnose "pig: ~A of child session ~A failed: ~A~%" abort (agent-session-id session) e))))))
                     :name "pig-child-session-abort")))
    (unwind-protect (child-call-args session "childSession.opAsync" payload)
      (setf done t)
      (when watcher (sb-thread:join-thread watcher :default nil)))))

(defun child-bool (value) (eq value t))
(defun child-number (value) (if (integerp value) value 0))
(defun child-string (value) (if (stringp value) value ""))
(defun child-maybe-string (value) (and (stringp value) value))
(defun child-object (value) (and (hash-table-p value) value))
(defun child-vector (value) (if (and (vectorp value) (not (stringp value))) value #()))
(defun child-void (value) (declare (ignore value)) nil)
(defun child-flag (value) (if value t :false))

(defmacro def-child-member (name (&rest params) doc (op &optional args) convert &key async abort)
  "Define NAME, a function of a SESSION and PARAMS that runs the member OP on the session with the argument object ARGS and returns CONVERT of its value."
  (let ((call (if async
                  `(child-op-async session ,op ,args ,@(and abort `(:abort ,abort :ctx ctx)))
                  `(child-op session ,op ,args))))
    `(defun ,name (session ,@params) ,doc (,convert ,call))))

;;; state reads

(def-child-member agent-session-streaming-p () "`session.isStreaming`." ("isStreaming") child-bool)
(def-child-member agent-session-idle-p () "`session.isIdle`: neither streaming nor compacting." ("isIdle") child-bool)
(def-child-member agent-session-compacting-p () "`session.isCompacting`." ("isCompacting") child-bool)
(def-child-member agent-session-retrying-p () "`session.isRetrying`." ("isRetrying") child-bool)
(def-child-member agent-session-bash-running-p () "`session.isBashRunning`." ("isBashRunning") child-bool)
(def-child-member agent-session-has-pending-bash-messages-p ()
  "`session.hasPendingBashMessages`: bash results wait to be flushed after the agent turn." ("hasPendingBashMessages") child-bool)
(def-child-member agent-session-retry-attempt () "`session.retryAttempt`." ("retryAttempt") child-number)
(def-child-member agent-session-pending-message-count () "`session.pendingMessageCount`: the queued steering and follow-up messages."
  ("pendingMessageCount") child-number)
(def-child-member agent-session-session-id () "`session.sessionId`." ("sessionId") child-string)
(def-child-member agent-session-session-file () "`session.sessionFile`; NIL for an in-memory session." ("sessionFile") child-maybe-string)
(def-child-member agent-session-session-name () "`session.sessionName`; NIL when the session has no name." ("sessionName") child-maybe-string)
(def-child-member agent-session-system-prompt () "`session.systemPrompt`." ("systemPrompt") child-string)
(def-child-member agent-session-thinking-level () "`session.thinkingLevel`." ("thinkingLevel") child-string)
(def-child-member agent-session-model () "`session.model`: the session's model object, NIL without one." ("model") child-object)
(def-child-member agent-session-routed-model ()
  "`session.routedModel`: under a virtual selection, {model, thinkingLevel} of the latest successful response, NIL otherwise."
  ("routedModel") child-object)
(def-child-member agent-session-scoped-models ()
  "`session.scopedModels`: the {model, thinkingLevel} the session cycles through, empty for all available models." ("scopedModels") child-vector)
(def-child-member agent-session-steering-mode () "`session.steeringMode`: \"all\" or \"one-at-a-time\"." ("steeringMode") child-string)
(def-child-member agent-session-follow-up-mode () "`session.followUpMode`: \"all\" or \"one-at-a-time\"." ("followUpMode") child-string)
(def-child-member agent-session-auto-compaction-enabled-p () "`session.autoCompactionEnabled`." ("autoCompactionEnabled") child-bool)
(def-child-member agent-session-auto-retry-enabled-p () "`session.autoRetryEnabled`." ("autoRetryEnabled") child-bool)
(def-child-member agent-session-supports-thinking-p () "`session.supportsThinking()`." ("supportsThinking") child-bool)
(def-child-member agent-session-get-available-thinking-levels () "`session.getAvailableThinkingLevels()`."
  ("getAvailableThinkingLevels") child-vector)
(def-child-member agent-session-get-last-assistant-text () "`session.getLastAssistantText()`; NIL when the session has none."
  ("getLastAssistantText") child-maybe-string)
(def-child-member agent-session-active-tools () "`session.getActiveToolNames()` (the readers of the creation snapshot, now live)."
  ("getActiveToolNames") child-vector)
(def-child-member agent-session-get-active-tool-names () "`session.getActiveToolNames()`." ("getActiveToolNames") child-vector)
(def-child-member agent-session-get-callable-tool-names () "`session.getCallableToolNames()`." ("getCallableToolNames") child-vector)
(def-child-member agent-session-get-steering-messages () "`session.getSteeringMessages()`: the steering messages not yet delivered."
  ("getSteeringMessages") child-vector)
(def-child-member agent-session-get-follow-up-messages () "`session.getFollowUpMessages()`: the follow-up messages not yet delivered."
  ("getFollowUpMessages") child-vector)
(def-child-member agent-session-get-all-tools () "`session.getAllTools()`: each tool's data, not its hooks." ("getAllTools") child-vector)
(def-child-member agent-session-get-tool-definition (name)
  "`session.getToolDefinition(name)`: the definition's data, NIL when no tool has the name; its execute function and hooks are not carried."
  ("getToolDefinition" (obj "name" name)) child-object)
(def-child-member agent-session-get-context-usage () "`session.getContextUsage()`: {tokens, contextWindow, percent}, NIL without a usable context window."
  ("getContextUsage") child-object)
(def-child-member agent-session-get-session-stats () "`session.getSessionStats()`." ("getSessionStats") child-object)
(def-child-member agent-session-get-user-messages-for-forking () "`session.getUserMessagesForForking()`: {entryId, text} each."
  ("getUserMessagesForForking") child-vector)
(def-child-member agent-session-prompt-templates () "`session.promptTemplates`." ("promptTemplates") child-vector)
(def-child-member agent-session-cache-warming-status () "`session.cacheWarmingStatus`: NIL when the session has no cache warmer."
  ("cacheWarmingStatus") child-object)
(def-child-member agent-session-has-extension-handlers-p (event-type)
  "`session.hasExtensionHandlers(eventType)`: an extension the session loaded handles the event."
  ("hasExtensionHandlers" (obj "eventType" event-type)) child-bool)

;;; changes

(def-child-member agent-session-cycle-model (&key direction persist)
  "`session.cycleModel(direction, options)`: DIRECTION is \"forward\" (the default) or \"backward\". The {model, thinkingLevel, isScoped} chosen, NIL when there is no model to cycle to."
  ("cycleModel" (child-args "direction" direction "persist" (and persist t))) child-object :async t)
(def-child-member agent-session-set-thinking-level (level &key persist)
  "`session.setThinkingLevel(level, options)`; the level is clamped to the model."
  ("setThinkingLevel" (child-args "level" level "persist" (and persist t))) child-void)
(def-child-member agent-session-cycle-thinking-level (&key persist)
  "`session.cycleThinkingLevel(options)`: the new level, NIL when the model does not support thinking."
  ("cycleThinkingLevel" (child-args "persist" (and persist t))) child-maybe-string)
(def-child-member agent-session-set-scoped-models (models)
  "`session.setScopedModels(models)`: MODELS is a list of {model, thinkingLevel}."
  ("setScopedModels" (obj "models" (coerce models 'simple-vector))) child-void)
(def-child-member agent-session-set-active-tools-by-name (names) "`session.setActiveToolsByName(names)`."
  ("setActiveToolsByName" (obj "names" (coerce names 'simple-vector))) child-void)
(def-child-member agent-session-set-steering-mode (mode) "`session.setSteeringMode(mode)`: \"all\" or \"one-at-a-time\"."
  ("setSteeringMode" (obj "mode" mode)) child-void)
(def-child-member agent-session-set-follow-up-mode (mode) "`session.setFollowUpMode(mode)`: \"all\" or \"one-at-a-time\"."
  ("setFollowUpMode" (obj "mode" mode)) child-void)
(def-child-member agent-session-set-auto-compaction-enabled (enabled) "`session.setAutoCompactionEnabled(enabled)`."
  ("setAutoCompactionEnabled" (obj "enabled" (child-flag enabled))) child-void)
(def-child-member agent-session-set-auto-retry-enabled (enabled) "`session.setAutoRetryEnabled(enabled)`."
  ("setAutoRetryEnabled" (obj "enabled" (child-flag enabled))) child-void)
(def-child-member agent-session-set-cache-warming-mode (mode) "`session.setCacheWarmingMode(mode)`: \"off\", \"streaming\" or \"idle\"."
  ("setCacheWarmingMode" (obj "mode" mode)) child-void)
(def-child-member agent-session-set-session-name (name) "`session.setSessionName(name)`." ("setSessionName" (obj "name" name)) child-void)
(def-child-member agent-session-clear-queue ()
  "`session.clearQueue()`: remove the queued messages and return {steering, followUp}, each a vector of strings."
  ("clearQueue") child-object)
(def-child-member agent-session-abort-bash () "`session.abortBash()`." ("abortBash") child-void)
(def-child-member agent-session-abort-branch-summary () "`session.abortBranchSummary()`." ("abortBranchSummary") child-void)
(def-child-member agent-session-abort-compaction () "`session.abortCompaction()`." ("abortCompaction") child-void)
(def-child-member agent-session-abort-retry () "`session.abortRetry()`." ("abortRetry") child-void)
(def-child-member agent-session-record-bash-result (command result &key exclude-from-context)
  "`session.recordBashResult(command, result, options)`: RESULT is a BashResult object."
  ("recordBashResult" (child-args "command" command "result" result "excludeFromContext" (and exclude-from-context (child-flag (eq exclude-from-context t)))))
  child-void)
(def-child-member agent-session-export-to-jsonl (output-path) "`session.exportToJsonl(outputPath)`: write the session as JSONL and return its path."
  ("exportToJsonl" (obj "outputPath" output-path)) child-string)

;;; awaited members

(def-child-member agent-session-set-model (model &key persist)
  "`session.setModel(model, options)`: MODEL is a model object whose provider and id name a model of the host's registry. Rejected for a model whose provider has no credentials."
  ("setModel" (child-args "model" model "persist" (and persist t)) ) child-void :async t)
(def-child-member agent-session-wait-for-idle () "`session.waitForIdle()`: return when the session is neither streaming nor compacting."
  ("waitForIdle") child-void :async t)
(def-child-member agent-session-reload ()
  "`session.reload()`: settings, extensions and resources are loaded again. Pi rejects it while the session runs or compacts."
  ("reload") child-void :async t)
(def-child-member agent-session-compact (&key custom-instructions ctx)
  "`session.compact(customInstructions)`: manual compaction; a session with nothing to compact signals host-call-error. Returns {summary, firstKeptEntryId, tokensBefore, ...}. A cancelled CTX runs abortCompaction."
  ("compact" (child-args "customInstructions" custom-instructions)) child-object :async t :abort "abortCompaction")
(def-child-member agent-session-navigate-tree (target-id &key summarize custom-instructions replace-instructions label ctx)
  "`session.navigateTree(targetId, options)`: {cancelled, aborted?, editorText?, summaryEntry?}. A cancelled CTX runs abortBranchSummary."
  ("navigateTree" (child-args "targetId" target-id "summarize" (and summarize t) "customInstructions" custom-instructions
                              "replaceInstructions" (and replace-instructions t) "label" label))
  child-object :async t :abort "abortBranchSummary")
(def-child-member agent-session-execute-bash (command &key exclude-from-context ctx)
  "`session.executeBash(command, onChunk, options)`: the command runs outside the agent loop and the result {output, exitCode?, cancelled, truncated, fullOutputPath?} is recorded for the next turn. A cancelled CTX runs abortBash."
  ("executeBash" (child-args "command" command "excludeFromContext" (and exclude-from-context (child-flag (eq exclude-from-context t)))))
  child-object :async t :abort "abortBash")
(def-child-member agent-session-export-to-html (&key output-path theme-name)
  "`session.exportToHtml(outputPath, options)`: write the session as an HTML file and return its path; no OUTPUT-PATH is Pi's default location."
  ("exportToHtml" (child-args "outputPath" output-path "themeName" theme-name)) child-string :async t)
(def-child-member agent-session-summarize-for-bug-report (&key hint) "`session.summarizeForBugReport({hint})`."
  ("summarizeForBugReport" (child-args "hint" hint)) child-string :async t)
(def-child-member agent-session-send-custom-message (custom-type content &key display details trigger-turn deliver-as)
  "`session.sendCustomMessage(message, options)`: the message is {customType, content, display, details?}; DISPLAY is T to show it."
  ("sendCustomMessage" (obj "message" (child-args "customType" custom-type "content" content "display" (child-flag display) "details" details)
                            "options" (child-args "triggerTurn" (and trigger-turn t) "deliverAs" deliver-as)))
  child-void :async t)
(def-child-member agent-session-send-user-message (content &key deliver-as expand-prompt-templates)
  "`session.sendUserMessage(content, options)`: CONTENT is a string or a vector of text and image content blocks; DELIVER-AS is \"steer\" or \"followUp\"."
  ("sendUserMessage" (obj "content" content
                          "options" (child-args "deliverAs" deliver-as
                                                "expandPromptTemplates" (and expand-prompt-templates (child-flag (eq expand-prompt-templates t))))))
  child-void :async t)
