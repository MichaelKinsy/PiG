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
  "A child agent session. The readers hold what the session had when it was created; agent-session-state reads the current values."
  extension id ctx
  (tools (make-hash-table :test 'equal))
  (lock (sb-thread:make-mutex :name "pig child session"))
  (queue (sb-thread:make-waitqueue))
  (listeners '()) (pending '()) (delivered 0)
  (replica nil)                         ; (seq . payload) of the message_update the host last sent; the reader thread owns it
  (stopped nil) (running nil) (disposed nil) (thread nil)
  (session-id "") (session-file nil) (model nil) (thinking-level "") (active-tools #()) (system-prompt "")
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
      (flet ((text (key) (let ((v (jget info key))) (if (stringp v) v "")))
             (present (key) (let ((v (jget info key))) (and (stringp v) (plusp (length v)) v)))
             (vec (key) (let ((v (jget info key))) (if (and (vectorp v) (not (stringp v))) v #()))))
        (setf (agent-session-session-id session) (text "sessionId")
              (agent-session-session-file session) (present "sessionFile")
              (agent-session-model session) (let ((m (jget info "model"))) (and (hash-table-p m) m))
              (agent-session-thinking-level session) (text "thinkingLevel")
              (agent-session-active-tools session) (vec "activeTools")
              (agent-session-system-prompt session) (text "systemPrompt")
              (agent-session-model-fallback-message session) (present "modelFallbackMessage")
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
  "Abort the run, release the session in the host and remove its listeners. Idempotent; a failure is reported on stderr, as Pi's dispose never fails."
  (unless (sb-thread:with-mutex ((agent-session-lock session))
            (prog1 (agent-session-disposed session) (setf (agent-session-disposed session) t)))
    (child-stop session)
    (handler-case (child-call session "childSession.dispose")
      (error (e) (format *error-output* "pig: dispose of child session ~A failed: ~A~%" (agent-session-id session) e)))))

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
      (format *error-output* "pig: child session ~A: ~A~%" (agent-session-id session) e)
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
            (error (e) (format *error-output* "pig: child session ~A listener raised: ~A~%" (agent-session-id session) e)))))
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
