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
       (sess-answer host "childSession.messages" (vector (pig:obj "role" "assistant")))
       (sess-answer host "childSession.state" (pig:obj "lastAssistantText" "done"))
       (check-equal (pig:obj "id" "c1") (sess-answer host "childSession.dispose"))))
    (check ran)
    (check-equal "sid" (first out))
    (check-equal "high" (second out))
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
       (check-equal (pig:obj "type" "session_shutdown" "reason" "quit")
                    (pig:jget (sess-answer host "childSession.emit" (pig:obj "emitted" :false)) "event"))
       (sess-answer host "childSession.dispose")))
    (check-equal t (pig:jget cancelled "cancelled"))
    (check-equal 1 invalidated)
    (check disposed-runtime)))
