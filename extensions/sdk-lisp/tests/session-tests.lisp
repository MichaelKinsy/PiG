;;;; SPDX-License-Identifier: MIT
;;;; Session operations, child agent sessions and the agent session runtime, against the in-process fake host (sdk-tests.lisp).
(in-package #:pig-tests)

(defun sess-answer (host method &optional (result (pig:obj)))
  "Read the extension's next host call, check it is METHOD, answer RESULT; the call's arguments."
  (let* ((frame (hread-type host "call")) (call (pig:jget frame "call")))
    (check-equal method (pig:jget call "method"))
    (hsend host "type" "call_result" "id" (pig:jget frame "id") "call_result" (pig:obj "result" result))
    (pig:jget call "args")))

(defun sess-run-command (ext name &optional (check-body #'identity))
  "Serve EXT, run its command NAME as request r1 and call CHECK-BODY with the host before reading r1's response."
  (with-host (host ext)
    (request host "r1" "command" "tool" name "args" "")
    (funcall check-body host)
    (pig:jget (hread-type host "response") "response")))

(deftest fork-navigate-and-switch-send-their-wire-shapes
  (let ((ext (pig:make-extension "ops")) (results '()))
    (pig:command ext "go" "Go"
                 (lambda (ctx args) (declare (ignore args))
                   (push (pig:ctx-fork ctx "e1" :position "at") results)
                   (push (pig:ctx-navigate-tree ctx "e2" :summarize t :custom-instructions "short" :replace-instructions t :label "L") results)
                   (push (pig:ctx-switch-session ctx "/s/other.jsonl") results)
                   (push (pig:ctx-new-session ctx :parent-session "/s/parent.jsonl") results)))
    (let ((response (sess-run-command
                     ext "go"
                     (lambda (host)
                       (let ((fork (sess-answer host "fork" (pig:obj "cancelled" t)))
                             (navigate (sess-answer host "navigateTree" (pig:obj "cancelled" :false)))
                             (switch (sess-answer host "switchSession" (pig:obj "cancelled" t)))
                             (new (sess-answer host "newSession" (pig:obj "cancelled" :false))))
                         (check-equal (pig:obj "entryId" "e1" "position" "at") fork)
                         (check-equal (pig:obj "targetId" "e2" "summarize" t "customInstructions" "short" "replaceInstructions" t "label" "L") navigate)
                         (check-equal (pig:obj "sessionPath" "/s/other.jsonl") switch)
                         (check-equal (pig:obj "parentSession" "/s/parent.jsonl") new))))))
      (check (eq :null (pig:jget response "error" :null)))
      (check-equal '(t :false t :false) (mapcar (lambda (r) (pig:jget r "cancelled")) (reverse results))))))

(deftest new-session-runs-setup-and-with-session-callbacks
  (let ((ext (pig:make-extension "ops")) (seen '()))
    (pig:command ext "go" "Go"
                 (lambda (ctx args) (declare (ignore args))
                   (pig:ctx-new-session
                    ctx
                    :setup (lambda (manager)
                             (push (pig:setup-append-custom-entry manager "note" (pig:obj "a" 1)) seen))
                    :with-session (lambda (replaced)
                                    (push (list (pig:ctx-cwd replaced) (pig:ctx-mode replaced) (pig:ctx-has-ui-p replaced)
                                                (pig:jget (pig:ctx-get-model-info replaced) "id"))
                                          seen)))))
    (let ((response
            (sess-run-command
             ext "go"
             (lambda (host)
               (let* ((frame (hread-type host "call")) (args (pig:jget (pig:jget frame "call") "args"))
                      (setup (pig:jget args "setup")) (with-session (pig:jget args "withSession")))
                 (check-equal "newSession" (pig:jget (pig:jget frame "call") "method"))
                 (check (and (stringp setup) (stringp with-session) (string/= setup with-session)))
                 (request host "r2" "setup" "args" (pig:obj "handle" setup))
                 (let ((write (sess-answer host "sessionWrite" "entry-1")))
                   (check-equal (pig:obj "method" "appendCustomEntry" "args" (pig:obj "customType" "note" "data" (pig:obj "a" 1))) write))
                 (check (eq :null (pig:jget (pig:jget (hread-type host "response") "response") "error" :null)))
                 (request host "r3" "with_session"
                          "args" (pig:obj "handle" with-session
                                          "ready" (pig:obj "cwd" "/replaced" "mode" "rpc" "state" (pig:obj "hasUI" :false))))
                 (sess-answer host "getModelInfo" (pig:obj "id" "model-after"))
                 (check (eq :null (pig:jget (pig:jget (hread-type host "response") "response") "error" :null)))
                 (hsend host "type" "call_result" "id" (pig:jget frame "id") "call_result" (pig:obj "result" (pig:obj "cancelled" :false))))))))
      (check (eq :null (pig:jget response "error" :null)))
      (check-equal '("entry-1" ("/replaced" "rpc" nil "model-after")) (reverse seen)))))

(deftest a-with-session-error-fails-the-call-with-that-error
  (let* ((ext (pig:make-extension "ops")) (caught nil))
    (pig:command ext "go" "Go"
                 (lambda (ctx args) (declare (ignore args))
                   (handler-case (pig:ctx-switch-session ctx "/s.jsonl" :with-session (lambda (replaced) (declare (ignore replaced)) (error "callback failed")))
                     (error (e) (setf caught e)))))
    (sess-run-command
     ext "go"
     (lambda (host)
       (let* ((frame (hread-type host "call")) (handle (pig:jget (pig:jget (pig:jget frame "call") "args") "withSession")))
         (request host "r2" "with_session" "args" (pig:obj "handle" handle))
         (check-equal "callback failed" (pig:jget (pig:jget (pig:jget (hread-type host "response") "response") "error") "message"))
         (hsend host "type" "call_result" "id" (pig:jget frame "id") "call_result" (pig:obj "error" (pig:obj "message" "callback failed"))))))
    (check (and caught (not (typep caught 'pig:host-call-error))))
    (check-equal "callback failed" (princ-to-string caught))))

(defun sess-notify-event (host id seq &rest members)
  (hsend host "type" "notify" "notify" (pig:obj "method" "childSession.event" "args" (apply #'pig:obj "id" id "seq" seq members))))

(deftest a-child-session-prompts-listens-and-serves-its-tools
  (let ((ext (pig:make-extension "child")) (events '()) (texts '()) (ran nil) (out nil))
    (pig:command ext "go" "Go"
                 (lambda (ctx args) (declare (ignore args))
                   (let ((session (pig:ctx-create-agent-session
                                   ctx :model (pig:obj "provider" "p" "id" "m") :in-memory t :tools '("t1")
                                       :resources (pig:make-resource-options :no-extensions t :append-system-prompt '("A"))
                                       :custom-tools (list (pig:child-tool "t1" "d" (pig:obj "type" "object")
                                                                           (lambda (c p) (declare (ignore c p)) (setf ran t) "ok"))))))
                     (pig:agent-session-subscribe session (lambda (e)
                                                            (push (pig:jget e "type") events)
                                                            (when (equal (pig:jget e "type") "message_update")
                                                              (push (pig:jget (pig:jget e "message") "text") texts))))
                     (pig:agent-session-prompt session "hi")
                     (setf out (list (pig:agent-session-session-id session) (pig:agent-session-thinking-level session)
                                     (pig:agent-session-messages session) (pig:jget (pig:agent-session-state session) "lastAssistantText")
                                     (reverse events)))
                     (pig:agent-session-dispose session)
                     (pig:agent-session-dispose session))))
    (sess-run-command
     ext "go"
     (lambda (host)
       (let* ((create (sess-answer host "childSession.create"
                                   (pig:obj "sessionId" "sid" "sessionFile" :null "thinkingLevel" "high" "activeTools" (vector "t1")
                                            "systemPrompt" "sp" "model" (pig:obj "id" "m")))))
         (check-equal "c1" (pig:jget create "id"))
         (check-equal (pig:obj "provider" "p" "id" "m") (pig:jget create "model"))
         (check-equal t (pig:jget create "inMemory"))
         (check-equal (vector "t1") (pig:jget create "tools"))
         (check-equal (pig:obj "noExtensions" t "appendSystemPrompt" (vector "A")) (pig:jget create "resources"))
         (check-equal "t1" (pig:jget (pig:jref create "customTools" 0) "name")))
       (let ((prompt (hread-type host "call")))
         (check-equal "childSession.prompt" (pig:jget (pig:jget prompt "call") "method"))
         (check-equal (pig:obj "text" "hi" "id" "c1") (pig:jget (pig:jget prompt "call") "args"))
         ;; the session's tool runs while its prompt is outstanding
         (request host "r2" "tool_call" "tool" "t1" "args" (pig:obj) "tool_call_id" "tc" "child_session" "c1")
         (check-equal "ok" (pig:jget (pig:jget (pig:jget (hread-type host "response") "response") "result") "content"))
         (sess-notify-event host "c1" 1 "event" (pig:obj "type" "message_update" "message" (pig:obj "text" "a")) "stream" (pig:obj "seq" 1))
         (sess-notify-event host "c1" 2 "stream" (pig:obj "base" 1 "seq" 2 "ops" (vector (pig:obj "op" "append" "path" (vector "message" "text") "value" "b"))))
         (sess-notify-event host "c1" 3 "event" (pig:obj "type" "agent_end"))
         (hsend host "type" "call_result" "id" (pig:jget prompt "id") "call_result" (pig:obj "result" (pig:obj "events" 3))))
       ;; the readers ask the host for the live value, not what the session had at creation
       (check-equal (pig:obj "op" "sessionId" "id" "c1") (sess-answer host "childSession.op" "live-sid"))
       (check-equal (pig:obj "op" "thinkingLevel" "id" "c1") (sess-answer host "childSession.op" "low"))
       (sess-answer host "childSession.messages" (vector (pig:obj "role" "assistant")))
       (sess-answer host "childSession.state" (pig:obj "lastAssistantText" "done"))
       (check-equal (pig:obj "id" "c1") (sess-answer host "childSession.dispose"))))
    (check ran)
    (check-equal "live-sid" (first out))
    (check-equal "low" (second out))
    (check-equal 1 (length (third out)))
    (check-equal "done" (fourth out))
    (check-equal '("message_update" "message_update" "agent_end") (fifth out))
    (check-equal '("a" "ab") (reverse texts))))

(deftest a-rejected-prompt-signals-and-a-failed-create-forgets-the-session
  (let ((ext (pig:make-extension "child")) (messages '()))
    (pig:command ext "go" "Go"
                 (lambda (ctx args) (declare (ignore args))
                   (handler-case (pig:ctx-create-agent-session ctx)
                     (pig:host-call-error (e) (push (pig:host-call-error-message e) messages)))
                   (let ((session (pig:ctx-create-agent-session ctx)))
                     (handler-case (pig:agent-session-prompt session "x")
                       (pig:host-call-error (e) (push (pig:host-call-error-message e) messages))))))
    (sess-run-command
     ext "go"
     (lambda (host)
       (let ((frame (hread-type host "call")))
         (hsend host "type" "call_result" "id" (pig:jget frame "id") "call_result" (pig:obj "error" (pig:obj "message" "no model"))))
       (check-equal "c2" (pig:jget (sess-answer host "childSession.create") "id"))
       (let ((frame (hread-type host "call")))
         (hsend host "type" "call_result" "id" (pig:jget frame "id") "call_result" (pig:obj "error" (pig:obj "message" "No API key"))))))
    (check-equal '("no model" "No API key") (reverse messages))))

(deftest the-runtime-cancels-a-replacement-and-disposes
  (let ((ext (pig:make-extension "runtime")) (cancelled nil) (invalidated 0) (disposed-runtime nil))
    (pig:command ext "go" "Go"
                 (lambda (ctx args) (declare (ignore args))
                   (let* ((session (pig:ctx-create-agent-session ctx :in-memory t))
                          (runtime (pig:create-agent-session-runtime
                                    ctx (lambda (options)
                                          (declare (ignore options))
                                          (pig:make-session-runtime-result
                                           :session session
                                           :services (pig:make-agent-session-services :cwd "/work" :agent-dir "/agent")))
                                    (pig:make-session-runtime-options :cwd "/work" :agent-dir "/agent"
                                                                      :session-manager (pig::%make-sm ctx "sm1")))))
                     (pig:agent-session-runtime-set-before-session-invalidate runtime (lambda () (incf invalidated)))
                     (setf cancelled (pig:agent-session-runtime-fork runtime "e1" :position "at"))
                     (check (typep runtime 'pig:agent-session-runtime))
                     (check-equal "/work" (pig:agent-session-runtime-cwd runtime))
                     (pig:agent-session-runtime-dispose runtime)
                     (setf disposed-runtime t))))
    (sess-run-command
     ext "go"
     (lambda (host)
       (sess-answer host "childSession.create" (pig:obj "sessionId" "sid"))
       ;; createAgentSessionRuntime: the stored cwd of the manager's session is checked
       (sess-answer host "sessionGraph.call" :null)
       (let ((emit (sess-answer host "childSession.emit" (pig:obj "emitted" t "result" (pig:obj "cancel" t)))))
         (check-equal (pig:obj "type" "session_before_fork" "entryId" "e1" "position" "at") (pig:jget emit "event")))
       (check-equal (pig:obj "id" "c1") (sess-answer host "childSession.abort"))
       (check-equal (pig:obj "type" "session_shutdown" "reason" "quit")
                    (pig:jget (sess-answer host "childSession.emit" (pig:obj "emitted" :false)) "event"))
       (sess-answer host "childSession.dispose")))
    (check-equal t (pig:jget cancelled "cancelled"))
    (check-equal 1 invalidated)
    (check disposed-runtime)))

(deftest refresh-context-and-the-session-manager-name-the-session
  (let ((ext (pig:make-extension "child")) (manager nil))
    (pig:command ext "go" "Go"
                 (lambda (ctx args) (declare (ignore args))
                   (let ((session (pig:ctx-create-agent-session ctx :in-memory t)))
                     (pig:agent-session-refresh-context session)
                     (setf manager (pig:agent-session-session-manager session)))))
    (sess-run-command
     ext "go"
     (lambda (host)
       (sess-answer host "childSession.create" (pig:obj "sessionId" "sid"))
       (check-equal (pig:obj "id" "c1") (sess-answer host "childSession.refreshContext"))
       (check-equal (pig:obj "id" "c1") (sess-answer host "childSession.sessionManager" (pig:obj "handle" "h9")))))
    (check-equal "h9" (pig::gobj-handle manager))))

(deftest session-members-send-childsession-op-and-opasync-with-pi-shapes
  (let ((ext (pig:make-extension "members")) (out '()))
    (pig:command ext "go" "Go"
                 (lambda (ctx args) (declare (ignore args))
                   (let ((session (pig:ctx-create-agent-session ctx :in-memory t)))
                     (push (pig:agent-session-streaming-p session) out)
                     (push (pig:agent-session-session-file session) out)
                     (push (pig:agent-session-get-tool-definition session "t") out)
                     (pig:agent-session-set-auto-retry-enabled session nil)
                     (pig:agent-session-set-thinking-level session "high" :persist t)
                     (push (pig:agent-session-clear-queue session) out)
                     (push (pig:agent-session-compact session :custom-instructions "short") out)
                     (pig:agent-session-send-custom-message session "note" "hi" :display t :trigger-turn t)
                     (pig:agent-session-send-user-message session "text" :deliver-as "steer")
                     (push (handler-case (pig:agent-session-wait-for-idle session) (pig:host-call-error (e) (princ-to-string e))) out)
                     (setf out (nreverse out)))))
    (sess-run-command
     ext "go"
     (lambda (host)
       (sess-answer host "childSession.create" (pig:obj))
       (flet ((op (method want result) (check-equal want (sess-answer host method result))))
         (op "childSession.op" (pig:obj "op" "isStreaming" "id" "c1") t)
         (op "childSession.op" (pig:obj "op" "sessionFile" "id" "c1") :null)
         (op "childSession.op" (pig:obj "op" "getToolDefinition" "args" (pig:obj "name" "t") "id" "c1") :null)
         (op "childSession.op" (pig:obj "op" "setAutoRetryEnabled" "args" (pig:obj "enabled" :false) "id" "c1") :null)
         (op "childSession.op" (pig:obj "op" "setThinkingLevel" "args" (pig:obj "level" "high" "persist" t) "id" "c1") :null)
         (op "childSession.op" (pig:obj "op" "clearQueue" "id" "c1") (pig:obj "steering" (vector "s") "followUp" (vector)))
         (op "childSession.opAsync" (pig:obj "op" "compact" "args" (pig:obj "customInstructions" "short") "id" "c1")
             (pig:obj "summary" "sum"))
         (op "childSession.opAsync"
             (pig:obj "op" "sendCustomMessage" "id" "c1"
                      "args" (pig:obj "message" (pig:obj "customType" "note" "content" "hi" "display" t)
                                      "options" (pig:obj "triggerTurn" t)))
             :null)
         (op "childSession.opAsync"
             (pig:obj "op" "sendUserMessage" "id" "c1"
                      "args" (pig:obj "content" "text" "options" (pig:obj "deliverAs" "steer")))
             :null)
         (let* ((frame (hread-type host "call")))
           (check-equal "waitForIdle" (pig:jget (pig:jget (pig:jget frame "call") "args") "op"))
           (hsend host "type" "call_result" "id" (pig:jget frame "id") "call_result" (pig:obj "error" (pig:obj "message" "idle failed")))))))
    (check-equal t (first out))
    (check-equal nil (second out))
    (check-equal nil (third out))
    (check-equal (vector "s") (pig:jget (fourth out) "steering"))
    (check-equal "sum" (pig:jget (fifth out) "summary"))
    (check-equal "idle failed" (sixth out))))

(defparameter *member-lanes*
  ;; Pi 1.5.0 agent-session.ts: the members declared `async` go over childSession.opAsync, every other one over childSession.op.
  '(("isStreaming" "op" (lambda (s) (pig:agent-session-streaming-p s)))
    ("isIdle" "op" (lambda (s) (pig:agent-session-idle-p s)))
    ("isCompacting" "op" (lambda (s) (pig:agent-session-compacting-p s)))
    ("isRetrying" "op" (lambda (s) (pig:agent-session-retrying-p s)))
    ("isBashRunning" "op" (lambda (s) (pig:agent-session-bash-running-p s)))
    ("hasPendingBashMessages" "op" (lambda (s) (pig:agent-session-has-pending-bash-messages-p s)))
    ("retryAttempt" "op" (lambda (s) (pig:agent-session-retry-attempt s)))
    ("pendingMessageCount" "op" (lambda (s) (pig:agent-session-pending-message-count s)))
    ("sessionId" "op" (lambda (s) (pig:agent-session-session-id s)))
    ("sessionFile" "op" (lambda (s) (pig:agent-session-session-file s)))
    ("sessionName" "op" (lambda (s) (pig:agent-session-session-name s)))
    ("systemPrompt" "op" (lambda (s) (pig:agent-session-system-prompt s)))
    ("thinkingLevel" "op" (lambda (s) (pig:agent-session-thinking-level s)))
    ("model" "op" (lambda (s) (pig:agent-session-model s)))
    ("routedModel" "op" (lambda (s) (pig:agent-session-routed-model s)))
    ("scopedModels" "op" (lambda (s) (pig:agent-session-scoped-models s)))
    ("steeringMode" "op" (lambda (s) (pig:agent-session-steering-mode s)))
    ("followUpMode" "op" (lambda (s) (pig:agent-session-follow-up-mode s)))
    ("autoCompactionEnabled" "op" (lambda (s) (pig:agent-session-auto-compaction-enabled-p s)))
    ("autoRetryEnabled" "op" (lambda (s) (pig:agent-session-auto-retry-enabled-p s)))
    ("supportsThinking" "op" (lambda (s) (pig:agent-session-supports-thinking-p s)))
    ("getAvailableThinkingLevels" "op" (lambda (s) (pig:agent-session-get-available-thinking-levels s)))
    ("getLastAssistantText" "op" (lambda (s) (pig:agent-session-get-last-assistant-text s)))
    ("getActiveToolNames" "op" (lambda (s) (pig:agent-session-get-active-tool-names s)))
    ("getCallableToolNames" "op" (lambda (s) (pig:agent-session-get-callable-tool-names s)))
    ("getSteeringMessages" "op" (lambda (s) (pig:agent-session-get-steering-messages s)))
    ("getFollowUpMessages" "op" (lambda (s) (pig:agent-session-get-follow-up-messages s)))
    ("getAllTools" "op" (lambda (s) (pig:agent-session-get-all-tools s)))
    ("getToolDefinition" "op" (lambda (s) (pig:agent-session-get-tool-definition s "t")))
    ("getContextUsage" "op" (lambda (s) (pig:agent-session-get-context-usage s)))
    ("getSessionStats" "op" (lambda (s) (pig:agent-session-get-session-stats s)))
    ("getUserMessagesForForking" "op" (lambda (s) (pig:agent-session-get-user-messages-for-forking s)))
    ("promptTemplates" "op" (lambda (s) (pig:agent-session-prompt-templates s)))
    ("cacheWarmingStatus" "op" (lambda (s) (pig:agent-session-cache-warming-status s)))
    ("hasExtensionHandlers" "op" (lambda (s) (pig:agent-session-has-extension-handlers-p s "e")))
    ("cycleModel" "opAsync" (lambda (s) (pig:agent-session-cycle-model s)))
    ("setThinkingLevel" "op" (lambda (s) (pig:agent-session-set-thinking-level s "low")))
    ("cycleThinkingLevel" "op" (lambda (s) (pig:agent-session-cycle-thinking-level s)))
    ("setScopedModels" "op" (lambda (s) (pig:agent-session-set-scoped-models s '())))
    ("setActiveToolsByName" "op" (lambda (s) (pig:agent-session-set-active-tools-by-name s '())))
    ("setSteeringMode" "op" (lambda (s) (pig:agent-session-set-steering-mode s "all")))
    ("setFollowUpMode" "op" (lambda (s) (pig:agent-session-set-follow-up-mode s "all")))
    ("setAutoCompactionEnabled" "op" (lambda (s) (pig:agent-session-set-auto-compaction-enabled s t)))
    ("setAutoRetryEnabled" "op" (lambda (s) (pig:agent-session-set-auto-retry-enabled s t)))
    ("setCacheWarmingMode" "op" (lambda (s) (pig:agent-session-set-cache-warming-mode s "off")))
    ("setSessionName" "op" (lambda (s) (pig:agent-session-set-session-name s "n")))
    ("clearQueue" "op" (lambda (s) (pig:agent-session-clear-queue s)))
    ("abortBash" "op" (lambda (s) (pig:agent-session-abort-bash s)))
    ("abortBranchSummary" "op" (lambda (s) (pig:agent-session-abort-branch-summary s)))
    ("abortCompaction" "op" (lambda (s) (pig:agent-session-abort-compaction s)))
    ("abortRetry" "op" (lambda (s) (pig:agent-session-abort-retry s)))
    ("recordBashResult" "op" (lambda (s) (pig:agent-session-record-bash-result s "c" (pig:obj))))
    ("exportToJsonl" "op" (lambda (s) (pig:agent-session-export-to-jsonl s "p")))
    ("setModel" "opAsync" (lambda (s) (pig:agent-session-set-model s (pig:obj))))
    ("waitForIdle" "opAsync" (lambda (s) (pig:agent-session-wait-for-idle s)))
    ("reload" "opAsync" (lambda (s) (pig:agent-session-reload s)))
    ("compact" "opAsync" (lambda (s) (pig:agent-session-compact s)))
    ("navigateTree" "opAsync" (lambda (s) (pig:agent-session-navigate-tree s "e")))
    ("executeBash" "opAsync" (lambda (s) (pig:agent-session-execute-bash s "c")))
    ("exportToHtml" "opAsync" (lambda (s) (pig:agent-session-export-to-html s)))
    ("summarizeForBugReport" "opAsync" (lambda (s) (pig:agent-session-summarize-for-bug-report s)))
    ("sendCustomMessage" "opAsync" (lambda (s) (pig:agent-session-send-custom-message s "t" "c")))
    ("sendUserMessage" "opAsync" (lambda (s) (pig:agent-session-send-user-message s "c")))))

(deftest every-session-member-goes-over-the-lane-pi-declares-for-it
  (let ((ext (pig:make-extension "lanes")))
    (pig:command ext "go" "Go"
                 (lambda (ctx args) (declare (ignore args))
                   (let ((session (pig:ctx-create-agent-session ctx :in-memory t)))
                     (dolist (entry *member-lanes*) (funcall (eval (third entry)) session)))))
    (sess-run-command
     ext "go"
     (lambda (host)
       (sess-answer host "childSession.create" (pig:obj))
       (dolist (entry *member-lanes*)
         (let* ((frame (hread-type host "call")) (call (pig:jget frame "call"))
                (lane (if (equal (pig:jget call "method") "childSession.opAsync") "opAsync" "op")))
           (check-equal (first entry) (pig:jget (pig:jget call "args") "op"))
           (unless (equal lane (second entry))
             (check-equal (format nil "~A is on the wrong lane" (first entry)) (format nil "~A is on the ~A lane" (first entry) lane)))
           (hsend host "type" "call_result" "id" (pig:jget frame "id") "call_result" (pig:obj "result" :null))))))))

(defun source-child-members ()
  "The members child-session.lisp defines with def-child-member, as (op . async-p), read from the source forms: a member added there without a row in *member-lanes* is seen here, which a hand table alone never sees."
  (let ((members '()) (*read-eval* nil) (*package* (find-package :pig)))
    (with-open-file (in (repo-path "extensions/sdk-lisp/src/child-session.lisp"))
      (loop for form = (read in nil :eof)
            until (eq form :eof)
            do (when (and (consp form) (symbolp (first form)) (string= (symbol-name (first form)) "DEF-CHILD-MEMBER"))
                 (destructuring-bind (name params doc (op &optional args) convert &key async abort) (rest form)
                   (declare (ignore name params doc args convert abort))
                   (push (cons op (and async t)) members)))))
    (nreverse members)))

(deftest every-def-child-member-has-a-row-in-the-lane-table
  ;; Pi 1.5.0 agent-session.ts declares eleven members `async`; the source's :async members are exactly those, and each source member has a row.
  (let ((source (source-child-members)))
    (check (> (length source) 50) "the scan found the def-child-member forms")
    (dolist (member source)
      (let ((row (assoc (car member) *member-lanes* :test #'equal)))
        (unless row
          (check-equal (format nil "~A has a row in *member-lanes*" (car member)) (format nil "def-child-member ~A has no row in *member-lanes*" (car member))))
        (when row
          (check-equal (second row) (if (cdr member) "opAsync" "op") (format nil "~A: the source's lane differs from the table's" (car member))))))
    (dolist (row *member-lanes*)
      (unless (assoc (first row) source :test #'equal)
        (check-equal (format nil "~A is defined by a def-child-member" (first row)) (format nil "the row ~A has no def-child-member in the source" (first row)))))
    (check-equal 11 (count-if #'cdr source) "the members the source declares async")))
