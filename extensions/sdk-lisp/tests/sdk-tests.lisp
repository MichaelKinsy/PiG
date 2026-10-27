;;;; SPDX-License-Identifier: MIT
;;;; The SDK against an in-process fake host that speaks the wire.
(in-package #:pig-tests)

(defstruct host listener conn thread ext path)

(defun start-host (ext)
  "Listen on a fresh socket, serve EXT against it in a thread, accept it and complete the register handshake.
Returns the host and the register envelope."
  (let* ((path (format nil "/tmp/pig-lisp-test-~D-~D.sock" (sb-posix:getpid) (random 1000000)))
         (listener (make-instance 'sb-bsd-sockets:local-socket :type :stream)))
    (ignore-errors (delete-file path))
    (sb-bsd-sockets:socket-bind listener path)
    (sb-bsd-sockets:socket-listen listener 4)
    (let* ((thread (sb-thread:make-thread (lambda () (pig::serve ext (pig::connect-unix path))) :name "test-extension"))
           (conn (pig::socket-conn (sb-bsd-sockets:socket-accept listener)))
           (host (make-host :listener listener :conn conn :thread thread :ext ext :path path))
           (register (pig::read-frame conn)))
      (pig::send-frame conn (pig:obj "type" "ready"
                                     "ready" (pig:obj "cwd" "/work" "mode" "tui" "width" 80
                                                      "state" (pig:obj "hasUI" t))))
      (values host register))))

(defun stop-host (host)
  (ignore-errors (pig::send-frame (host-conn host) (pig:obj "type" "shutdown")))
  (sb-thread:join-thread (host-thread host) :default nil :timeout 5)
  (ignore-errors (pig::conn-close (host-conn host)))
  (ignore-errors (sb-bsd-sockets:socket-close (host-listener host)))
  (ignore-errors (delete-file (host-path host))))

(defmacro with-host ((host ext &optional register) &body body)
  `(multiple-value-bind (,host reg) (start-host ,ext)
     (declare (ignorable reg))
     (let (,@(when register `((,register reg))))
       (unwind-protect (progn ,@body) (stop-host ,host)))))

(defun hsend (host &rest members) (pig::send-frame (host-conn host) (apply #'pig:obj members)))
(defun hread (host) (pig::read-frame (host-conn host)))
(defun hread-type (host type)
  "The next frame of TYPE, skipping request_state frames."
  (loop (let ((frame (hread host)))
          (when (eq frame :eof) (error "end of file waiting for ~A" type))
          (when (equal (pig:jget frame "type") type) (return frame)))))

(defun request (host id method &rest members)
  (hsend host "type" "request" "id" id "request" (apply #'pig:obj "method" method members)))

(defun simple-extension ()
  (let ((ext (pig:make-extension "fake")))
    (pig:tool ext "echo" "Echo" (pig:obj "type" "object")
              (lambda (ctx params) (declare (ignore ctx)) (format nil "echo:~A" (pig:jget params "text" ""))))
    (pig:tool ext "boom" "Fail" (pig:obj "type" "object") (lambda (ctx params) (declare (ignore ctx params)) (error "boom")))
    (pig:command ext "ping" "Ping" (lambda (ctx args) (pig:ctx-notify ctx (concatenate 'string "pong:" args) "info")))
    (pig:on ext :tool-call (lambda (ctx data) (declare (ignore ctx))
                             (setf (gethash "extra" (pig:jget data "input")) "edited")
                             (pig:obj "block" t "reason" "no")))
    ext))

(deftest register-declares-everything-in-order
  (with-host (host (simple-extension) register)
    (let ((r (pig:jget register "register")))
      (check-equal "register" (pig:jget register "type"))
      (check-equal "fake" (pig:jget r "name"))
      (check-equal '("echo" "boom") (map 'list (lambda (tool) (pig:jget tool "name")) (pig:jget r "tools")))
      (check-equal "ping" (pig:jget (aref (pig:jget r "commands") 0) "name"))
      (let ((handler (aref (pig:jget r "handlers") 0)))
        (check-equal "tool_call" (pig:jget handler "event"))
        (check-equal 1 (pig:jget handler "handler_id"))))))

(deftest ping-is-answered-with-the-nonce
  (with-host (host (simple-extension))
    (hsend host "type" "ping" "ping" (pig:obj "nonce" "n-1"))
    (check-equal "n-1" (pig:jget (pig:jget (hread-type host "pong") "pong") "nonce"))))

(deftest a-tool-call-reports-state-and-answers
  (with-host (host (simple-extension))
    (request host "r1" "tool_call" "tool" "echo" "args" (pig:obj "text" "hi") "tool_call_id" "tc1")
    (let ((first (hread host)) (second (hread host)) (third (hread host)))
      (check-equal "started" (pig:jget (pig:jget first "request_state") "state"))
      (check-equal "completed" (pig:jget (pig:jget second "request_state") "state"))
      (check-equal "response" (pig:jget third "type"))
      (check-equal "r1" (pig:jget third "id"))
      (check-equal "echo:hi" (pig:jget (pig:jget (pig:jget third "response") "result") "content")))))

(deftest a-failing-tool-answers-with-its-message
  (with-host (host (simple-extension))
    (request host "r1" "tool_call" "tool" "boom" "args" (pig:obj))
    (let ((response (pig:jget (hread-type host "response") "response")))
      (check-equal "boom" (pig:jget (pig:jget response "error") "message"))
      (check (eq :null (pig:jget response "result"))))))

(deftest a-host-call-carries-its-parent-and-reports-blocked
  (with-host (host (simple-extension))
    (request host "r2" "command" "tool" "ping" "args" "x")
    (check-equal "started" (pig:jget (pig:jget (hread host) "request_state") "state"))
    (let ((blocked (hread host)))
      (check-equal "blocked" (pig:jget (pig:jget blocked "request_state") "state"))
      (check-equal "host_call" (pig:jget (pig:jget blocked "request_state") "reason")))
    (let ((call (hread host)))
      (check-equal "call" (pig:jget call "type"))
      (check-equal "ui.notify" (pig:jget (pig:jget call "call") "method"))
      (check-equal "r2" (pig:jget (pig:jget call "call") "parent_request_id"))
      (check-equal "pong:x" (pig:jget (pig:jget (pig:jget call "call") "args") "message"))
      (hsend host "type" "call_result" "id" (pig:jget call "id") "call_result" (pig:obj))
      (check-equal "progress" (pig:jget (pig:jget (hread host) "request_state") "state")))
    (check-equal "r2" (pig:jget (hread-type host "response") "id"))))

(deftest a-host-error-reaches-the-handler
  (let ((ext (pig:make-extension "err")) (seen nil))
    (pig:command ext "c" "C" (lambda (ctx args) (declare (ignore args))
                               (handler-case (pig:ctx-notify ctx "m")
                                 (pig:host-call-error (e) (setf seen (pig:host-call-error-message e))))))
    (with-host (host ext)
      (request host "r" "command" "tool" "c" "args" "")
      (let ((call (hread-type host "call")))
        (hsend host "type" "call_result" "id" (pig:jget call "id")
                    "call_result" (pig:obj "error" (pig:obj "message" "denied" "code" "x"))))
      (hread-type host "response")
      (check-equal "denied" seen))))

(deftest cancel-reaches-a-cooperating-handler
  (let ((ext (pig:make-extension "cancel")) (started (sb-thread:make-semaphore)))
    (pig:tool ext "wait" "W" (pig:obj "type" "object")
              (lambda (ctx params) (declare (ignore params))
                (sb-thread:signal-semaphore started)
                (loop until (pig:ctx-cancelled-p ctx) do (sleep 0.005))
                "cancelled"))
    (with-host (host ext)
      (request host "r" "tool_call" "tool" "wait" "args" (pig:obj) "tool_call_id" "t")
      (sb-thread:wait-on-semaphore started)
      (hsend host "type" "cancel" "cancel" (pig:obj "request_id" "r" "reason" "user"))
      (check-equal "cancelled" (pig:jget (pig:jget (pig:jget (hread-type host "response") "response") "result") "content")))))

(deftest cancel-ends-a-blocked-host-call
  (let ((ext (pig:make-extension "cancel-call")) (message nil))
    (pig:command ext "c" "C" (lambda (ctx args) (declare (ignore args))
                               (handler-case (pig:ctx-is-idle-p ctx)
                                 (pig:host-call-cancelled (e) (setf message (pig:host-call-error-message e))))))
    (with-host (host ext)
      (request host "r" "command" "tool" "c" "args" "")
      (hread-type host "call")
      (hsend host "type" "cancel" "cancel" (pig:obj "request_id" "r"))
      (hread-type host "response")
      (check-equal "host call isIdle cancelled with its parent request" message))))

(deftest tool-call-event-reports-the-edited-input
  (with-host (host (simple-extension))
    (request host "r" "event" "event" "tool_call" "handler_id" 1
                  "args" (pig:obj "input" (pig:obj "a" 1)))
    (let ((result (pig:jget (pig:jget (hread-type host "response") "response") "result")))
      (check-equal "edited" (pig:jget (pig:jget result "_pigToolCallInput") "extra"))
      (check (eq t (pig:jget (pig:jget result "_pigToolCallResult") "block"))))))

(deftest event-stream-deltas-rebuild-the-payload
  (let ((ext (pig:make-extension "stream")) (texts '()))
    (pig:on ext "message_update" (lambda (ctx data) (declare (ignore ctx))
                                   (push (pig:jref data "message" "text") texts) nil))
    (with-host (host ext)
      (request host "a" "event" "event" "message_update" "handler_id" 1
                    "args" (pig:obj "message" (pig:obj "text" "he" "parts" (vector 1)))
                    "stream" (pig:obj "seq" 1 "key" ""))
      (hread-type host "response")
      (request host "b" "event" "event" "message_update" "handler_id" 1
                    "stream" (pig:obj "seq" 2 "base" 1
                                      "ops" (vector (pig:obj "op" "append" "path" (vector "message" "text") "value" "llo")
                                                    (pig:obj "op" "set" "path" (vector "message" "parts" 1) "value" 2))))
      (hread-type host "response")
      (check-equal '("hello" "he") texts))))

(deftest unknown-methods-and-shutdown
  (with-host (host (simple-extension))
    (request host "r" "frobnicate")
    (check-equal "unknown method: frobnicate"
                 (pig:jget (pig:jget (pig:jget (hread-type host "response") "response") "error") "message"))))

(deftest stale-context-throws-the-hosts-message
  (let ((ext (pig:make-extension "stale")) (result nil))
    (pig:command ext "c" "C" (lambda (ctx args) (declare (ignore args))
                               (setf result (handler-case (pig:ctx-cwd ctx) (error (e) (princ-to-string e))))))
    (with-host (host ext)
      (hsend host "type" "notify" "notify" (pig:obj "method" "invalidate" "args" (pig:obj "message" "this ctx is stale")))
      (request host "r" "command" "tool" "c" "args" "")
      (hread-type host "response")
      (check-equal "this ctx is stale" result))))

(deftest replicated-state-comes-from-ready
  (let ((ext (pig:make-extension "state")) (seen nil))
    (pig:command ext "c" "C" (lambda (ctx args) (declare (ignore args))
                               (setf seen (list (pig:ctx-cwd ctx) (pig:ctx-mode ctx) (pig:ctx-has-ui-p ctx)))))
    (with-host (host ext)
      (request host "r" "command" "tool" "c" "args" "")
      (hread-type host "response")
      (check-equal '("/work" "tui" t) seen))))

(deftest define-extension-is-the-factory
  (let ((*package* (find-package '#:pig-tests))) (eval '(pig:define-extension "declared"
          (:tool "t" "T" (pig:obj "type" "object") (lambda (ctx p) (declare (ignore ctx p)) "x"))
          (:command "c" "C" (lambda (ctx a) (declare (ignore ctx a)) nil))
          (:on :session-start (lambda (ctx d) (declare (ignore ctx d)) nil)))))
  (let ((ext (funcall 'new-extension)))
    (check-equal "declared" (pig:extension-name ext))
    (check-equal 1 (length (pig::extension-tools ext)))
    (check-equal "session_start" (pig:jget (first (pig::extension-handlers ext)) "event"))))

(defvar *thunk* nil)

(defun probe-extension ()
  (let ((ext (pig:make-extension "probe")))
    (pig:command ext "c" "C" (lambda (ctx args) (declare (ignore args)) (setf *thunk-result* (funcall *thunk* ctx))))
    ext))

(defvar *thunk-result* nil)

(defun probe-call (host thunk result &optional (id "r"))
  "Run THUNK, (lambda (ctx)), in a command, answer its one host call with RESULT, and return the call frame's call member."
  (setf *thunk* thunk)
  (request host id "command" "tool" "c" "args" "")
  (let ((call (hread-type host "call")))
    (hsend host "type" "call_result" "id" (pig:jget call "id") "call_result" (pig:obj "result" result))
    (hread-type host "response")
    (pig:jget call "call")))

(deftest session-manager-reads-are-sessionRead-calls
  (with-host (host (probe-extension))
    (flet ((check-read (thunk method &optional (args (pig:obj)))
             (let ((call (probe-call host thunk "x")))
               (check-equal "sessionRead" (pig:jget call "method") method)
               (check-equal method (pig:jget (pig:jget call "args") "method"))
               (check-equal (pig:to-json args) (pig:to-json (pig:jget (pig:jget call "args") "args")) method))))
      (check-read (lambda (c) (pig:session-get-cwd (pig:ctx-session-manager c))) "getCwd")
      (check-read (lambda (c) (pig:session-get-session-dir (pig:ctx-session-manager c))) "getSessionDir")
      (check-read (lambda (c) (pig:session-get-session-id (pig:ctx-session-manager c))) "getSessionId")
      (check-read (lambda (c) (pig:session-get-session-file (pig:ctx-session-manager c))) "getSessionFile")
      (check-read (lambda (c) (pig:session-get-session-name (pig:ctx-session-manager c))) "getSessionName")
      (check-read (lambda (c) (pig:session-get-leaf-id (pig:ctx-session-manager c))) "getLeafId")
      (check-read (lambda (c) (pig:session-get-header (pig:ctx-session-manager c))) "getHeader")
      (check-read (lambda (c) (pig:session-get-leaf-entry (pig:ctx-session-manager c))) "getLeafEntry")
      (check-read (lambda (c) (pig:session-get-entry (pig:ctx-session-manager c) "e1")) "getEntry" (pig:obj "id" "e1"))
      (check-read (lambda (c) (pig:session-get-label (pig:ctx-session-manager c) "e1")) "getLabel" (pig:obj "id" "e1"))
      (check-read (lambda (c) (pig:session-get-entries (pig:ctx-session-manager c))) "getEntries")
      (check-read (lambda (c) (pig:session-get-branch (pig:ctx-session-manager c) "b")) "getBranch" (pig:obj "fromId" "b"))
      (check-read (lambda (c) (pig:session-get-children (pig:ctx-session-manager c) "p")) "getChildren" (pig:obj "parentId" "p"))
      (check-read (lambda (c) (pig:session-get-tree (pig:ctx-session-manager c))) "getTree"))))

(deftest registry-reads-the-providers-state
  (with-host (host (probe-extension))
    (let ((state (pig:from-json "{\"providers\":{\"configured\":{\"configured\":true,\"usingOAuth\":true},\"plain\":{\"configured\":false}}}")))
      (probe-call host (lambda (c) (pig:registry-has-configured-auth (pig:ctx-model-registry c) (pig:obj "provider" "configured" "id" "m"))) state)
      (check-equal t *thunk-result*)
      (probe-call host (lambda (c) (pig:registry-has-configured-auth (pig:ctx-model-registry c) (pig:obj "provider" "plain" "id" "m"))) state "r2")
      (check-equal nil *thunk-result*)
      (probe-call host (lambda (c) (pig:registry-is-using-oauth (pig:ctx-model-registry c) (pig:obj "provider" "configured" "id" "m"))) state "r3")
      (check-equal t *thunk-result*)
      (probe-call host (lambda (c) (pig:registry-is-using-oauth (pig:ctx-model-registry c) (pig:obj "provider" "nobody" "id" "m"))) state "r4")
      (check-equal nil *thunk-result*))))

(deftest unserved-members-signal-unsupported-capability
  (check-error pig:unsupported-capability (pig:registry-get-provider-name nil "x"))
  (check-error pig:unsupported-capability (pig:registry-get-registered-native-provider nil "x")))

(deftest thin-host-calls-use-their-wire-method-and-members
  (with-host (host (probe-extension))
    (let ((n 0))
      (flet ((check-call (thunk method args &optional (reply (pig:obj)))
               (let ((call (probe-call host thunk reply (format nil "t~D" (incf n)))))
                 (check-equal method (pig:jget call "method") method)
                 (check-equal (pig:to-json args) (pig:to-json (or (pig:jget call "args") (pig:obj))) method))))
        (check-call (lambda (c) (pig:ctx-set-active-tools c '("a" "b"))) "setActiveTools" (pig:obj "tools" (pig:arr "a" "b")))
        (check-call (lambda (c) (pig:ctx-get-commands c)) "getCommands" (pig:obj) (pig:obj "commands" (pig:arr)))
        (check-call (lambda (c) (pig:ctx-get-context-usage c)) "getContextUsage" (pig:obj))
        (check-equal nil *thunk-result*)
        (check-call (lambda (c) (pig:ctx-get-system-prompt-options c)) "getSystemPromptOptions" (pig:obj) (pig:obj "result" (pig:obj "cwd" "/x")))
        (check-equal "/x" (pig:jget *thunk-result* "cwd"))
        (check-call (lambda (c) (pig:ctx-get-model-info c)) "getModelInfo" (pig:obj) (pig:obj "result" (pig:obj "id" "m")))
        (check-equal "m" (pig:jget *thunk-result* "id"))
        (check-call (lambda (c) (pig:ctx-get-model-auth c "p" "m")) "getModelAuth" (pig:obj "provider" "p" "modelId" "m"))
        (check-call (lambda (c) (pig:ctx-get-scoped-models c)) "getScopedModels" (pig:obj))
        (check-call (lambda (c) (pig:ctx-set-model c "p/m")) "setModel" (pig:obj "model" "p/m") (pig:obj "result" (pig:obj "success" t)))
        (check-equal t *thunk-result*)
        (check-call (lambda (c) (pig:ctx-set-thinking-level c "high")) "setThinkingLevel" (pig:obj "level" "high"))
        (check-call (lambda (c) (pig:ctx-set-label c "e1" "L")) "setLabel" (pig:obj "entryId" "e1" "label" "L"))
        (check-call (lambda (c) (pig:ctx-wait-for-idle c)) "waitForIdle" (pig:obj))
        (check-call (lambda (c) (pig:ctx-project-trusted-p c)) "isProjectTrusted" (pig:obj) (pig:obj "trusted" t))
        (check-equal t *thunk-result*)
        (check-call (lambda (c) (pig:ctx-get-session-id c)) "getSessionID" (pig:obj) (pig:obj "sessionId" "s1"))
        (check-equal "s1" *thunk-result*)
        (check-call (lambda (c) (pig:ctx-get-leaf-id c)) "getLeafID" (pig:obj) (pig:obj "leafId" "l1"))
        (check-equal "l1" *thunk-result*)
        (check-call (lambda (c) (pig:ctx-get-all-themes c)) "ui.getAllThemes" (pig:obj) (pig:obj "themes" (pig:arr)))
        (check-call (lambda (c) (pig:ctx-get-theme c "dark")) "ui.getTheme" (pig:obj "name" "dark") (pig:obj "theme" (pig:obj "n" 1)))
        (check-call (lambda (c) (pig:ctx-set-theme c "dark")) "ui.setTheme" (pig:obj "theme" "dark"))
        (check-call (lambda (c) (pig:ctx-get-editor-text c)) "ui.getEditorText" (pig:obj) (pig:obj "text" "hi"))
        (check-equal "hi" *thunk-result*)
        (check-call (lambda (c) (pig:ctx-tools-expanded-p c)) "ui.getToolsExpanded" (pig:obj) (pig:obj "expanded" t))
        (check-equal t *thunk-result*)
        (check-call (lambda (c) (pig:registry-find (pig:ctx-model-registry c) "p" "m")) "getModel" (pig:obj "provider" "p" "modelId" "m"))
        (check-call (lambda (c) (pig:registry-get-provider-auth (pig:ctx-model-registry c) "p")) "getProviderAuth" (pig:obj "provider" "p"))
        (check-call (lambda (c) (pig:registry-refresh (pig:ctx-model-registry c))) "refreshModelRegistry" (pig:obj))
        (check-call (lambda (c) (pig:registry-get-available-of-type (pig:ctx-model-registry c) "t" "p")) "getAvailableOfType" (pig:obj "type" "t" "provider" "p"))))))
