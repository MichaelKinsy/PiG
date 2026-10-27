;;;; SPDX-License-Identifier: MIT
;;;; Pi's createAgentSessionServices, createAgentSessionFromServices and createAgentSessionRuntime (core/agent-session-services.ts,
;;;; core/agent-session-runtime.ts). The runtime owns the current agent session and its cwd-bound services; the factory that builds
;;;; them stays in this process and runs on every session replacement. The host answers the calls the runtime makes on the session it
;;;; replaces: the session_before_switch, session_before_fork and session_shutdown events through the session's extensions,
;;;; refresh-context and the session's log. A replacement method tears the current runtime down, creates and applies the next one,
;;;; and returns once the new session is rebound; a failing factory's error is signalled to the caller.
;;;;
;;;; Pi's withSession option (it takes the ReplacedSessionContext of AgentSession.createReplacedSessionContext, a C2Sessions gap) and
;;;; projectTrustContextFactory (a record of the host's UI functions, which cannot cross the process boundary) are not served: a value
;;;; for either signals before anything happens.

(in-package #:pig)

;;; ---------------------------------------------------------------- services

(defstruct (agent-session-services (:constructor make-agent-session-services (&key cwd agent-dir model-runtime settings-manager resource-loader diagnostics)))
  "The coherent cwd-bound services of one session cwd; DIAGNOSTICS is a vector of {type, message}."
  cwd agent-dir model-runtime settings-manager resource-loader diagnostics)

(defun rt-join (directory name)
  (cond ((and (plusp (length name)) (char= (char name 0) #\/)) name)
        ((or (zerop (length directory)) (char= (char directory (1- (length directory))) #\/)) (concatenate 'string directory name))
        (t (concatenate 'string directory "/" name))))

(defun create-agent-session-services (ctx &key cwd agent-dir settings-manager model-runtime extension-flag-values
                                            resource-loader-options resource-loader-reload-options)
  "Pi's createAgentSessionServices: the objects of one session cwd plus the diagnostics found creating them; it creates no session.
EXTENSION-FLAG-VALUES is a list of (name . value) in command-line order (T or :false for a boolean flag, a string for a string flag), the
order of the diagnostics. RESOURCE-LOADER-OPTIONS is a plist of make-resource-loader keywords other than :cwd, :agent-dir and :settings-manager,
which are the services' own; RESOURCE-LOADER-RELOAD-OPTIONS a plist of loader-reload keywords."
  (let* ((paths (graph-static ctx "AgentSessionServices" "resolve" cwd agent-dir))
         (cwd (jget paths "cwd")) (agent-dir (jget paths "agentDir"))
         (model-runtime (or model-runtime (runtime-create ctx :auth-path (rt-join agent-dir "auth.json")
                                                              :models-path (rt-join agent-dir "models.json"))))
         (settings-manager (or settings-manager (settings-create ctx cwd agent-dir)))
         (loader (apply #'make-resource-loader ctx :cwd cwd :agent-dir agent-dir :settings-manager settings-manager
                        resource-loader-options))
         (flags (map 'simple-vector (lambda (flag) (vector (car flag) (cdr flag))) extension-flag-values)))
    (apply #'loader-reload loader resource-loader-reload-options)
    (make-agent-session-services
     :cwd cwd :agent-dir agent-dir :model-runtime model-runtime :settings-manager settings-manager :resource-loader loader
     :diagnostics (or (graph-static ctx "AgentSessionServices" "apply" (gobj-handle model-runtime) (gobj-handle settings-manager)
                                    (gobj-handle loader) cwd agent-dir flags)
                      (vector)))))

(defun create-agent-session-from-services (ctx services &key session-manager session-start-event model thinking-level scoped-models
                                                         tools exclude-tools no-tools custom-tools)
  "Pi's createAgentSessionFromServices: an agent session over SERVICES (see ctx-create-agent-session for the other keywords)."
  (ctx-create-agent-session ctx :cwd (agent-session-services-cwd services) :agent-dir (agent-session-services-agent-dir services)
                                :model-runtime (agent-session-services-model-runtime services)
                                :settings-manager (agent-session-services-settings-manager services)
                                :resource-loader (agent-session-services-resource-loader services)
                                :session-manager session-manager :session-start-event session-start-event :model model
                                :thinking-level thinking-level :scoped-models scoped-models :tools tools
                                :exclude-tools exclude-tools :no-tools no-tools :custom-tools custom-tools))

;;; ---------------------------------------------------------------- the runtime

(define-condition session-import-file-not-found-error (pig-error)
  ((file-path :initarg :file-path :reader session-import-file-not-found-error-file-path))
  (:report (lambda (c s) (format s "File not found: ~A" (session-import-file-not-found-error-file-path c))))
  (:documentation "Pi's SessionImportFileNotFoundError: agent-session-runtime-import-from-jsonl named a file that does not exist."))

(define-condition missing-session-cwd-error (pig-error)
  ((session-file :initarg :session-file :reader missing-session-cwd-error-session-file)
   (session-cwd :initarg :session-cwd :reader missing-session-cwd-error-session-cwd)
   (fallback-cwd :initarg :fallback-cwd :reader missing-session-cwd-error-fallback-cwd))
  (:report (lambda (c s)
             (format s "Stored session working directory does not exist: ~A~@[~%Session file: ~A~]~%Current working directory: ~A"
                     (missing-session-cwd-error-session-cwd c)
                     (let ((file (missing-session-cwd-error-session-file c))) (and (plusp (length file)) file))
                     (missing-session-cwd-error-fallback-cwd c))))
  (:documentation "Pi's MissingSessionCwdError: the working directory a session file stored does not exist."))

(defstruct (session-runtime-options (:constructor make-session-runtime-options (&key cwd agent-dir session-manager session-start-event)))
  "The target of a session to build: the second argument of create-agent-session-runtime and the argument of its factory."
  cwd agent-dir session-manager session-start-event)

(defstruct (session-runtime-result (:constructor make-session-runtime-result (&key session services diagnostics model-fallback-message)))
  "What a factory returns: the session it created, its cwd-bound services, the diagnostics collected setting up, and the model fallback message or NIL."
  session services diagnostics model-fallback-message)

(defstruct (agent-session-runtime (:constructor rt-make (ctx create-runtime)))
  "The current agent session, its services and the factory that builds the next ones."
  ctx create-runtime (rebind nil) (before-invalidate nil) session services (diagnostics (vector)) (model-fallback-message nil))

(defun agent-session-runtime-cwd (runtime) (agent-session-services-cwd (agent-session-runtime-services runtime)))

(defun agent-session-runtime-set-rebind-session (runtime &optional function)
  "FUNCTION, (lambda (session)), runs with the new session after every replacement; NIL removes it."
  (setf (agent-session-runtime-rebind runtime) function) nil)

(defun agent-session-runtime-set-before-session-invalidate (runtime &optional function)
  "FUNCTION runs after the current session's session_shutdown handlers finished and before the session is disposed; NIL removes it."
  (setf (agent-session-runtime-before-invalidate runtime) function) nil)

(defun rt-exists-p (path)
  (and (plusp (length path)) (handler-case (progn (sb-posix:stat path) t) (error () nil))))

(defun rt-present (value) (and value (not (eq value :null)) (not (eq value :false)) (not (equal value ""))))

(defun rt-normalize (path)
  "posixpath.abspath of an absolute PATH: no empty, `.` or `..` segment."
  (let ((parts '()))
    (loop with start = 0
          for slash = (position #\/ path :start start)
          do (let ((part (subseq path start slash)))
               (cond ((or (string= part "") (string= part ".")))
                     ((string= part "..") (pop parts))
                     (t (push part parts))))
             (if slash (setf start (1+ slash)) (return)))
    (format nil "/~{~A~^/~}" (reverse parts))))

(defun rt-resolve-path (path)
  (let* ((path (string-trim '(#\Space #\Tab #\Newline #\Return) path))
         (home (sb-posix:getenv "HOME"))
         (path (if (and home (plusp (length path)) (char= (char path 0) #\~) (or (= (length path) 1) (char= (char path 1) #\/)))
                   (concatenate 'string home (subseq path 1))
                   path)))
    (rt-normalize (if (and (plusp (length path)) (char= (char path 0) #\/)) path (rt-join (sb-posix:getcwd) path)))))

(defun rt-assert-session-cwd-exists (ctx manager fallback-cwd)
  "A persisted session whose stored working directory is gone signals a missing-session-cwd-error."
  (declare (ignorable ctx))
  (let ((session-file (sm-get-session-file manager)))
    (when (rt-present session-file)
      (let ((session-cwd (sm-get-cwd manager)))
        (when (and (rt-present session-cwd) (not (rt-exists-p session-cwd)))
          (error 'missing-session-cwd-error :session-file session-file :session-cwd session-cwd :fallback-cwd fallback-cwd))))))

(defun rt-user-message-text (content)
  (if (stringp content)
      content
      (format nil "~{~A~}" (loop for part across (if (vectorp content) content #())
                                 when (and (hash-table-p part) (equal (jget part "type") "text") (stringp (jget part "text")))
                                   collect (jget part "text")))))

(defun rt-file (manager) (let ((file (sm-get-session-file manager))) (and (rt-present file) file)))

(defun rt-apply (runtime result)
  (setf (agent-session-runtime-session runtime) (session-runtime-result-session result)
        (agent-session-runtime-services runtime) (session-runtime-result-services result)
        (agent-session-runtime-diagnostics runtime) (session-runtime-result-diagnostics result)
        (agent-session-runtime-model-fallback-message runtime) (session-runtime-result-model-fallback-message result)))

(defun rt-emit-before (runtime event)
  (multiple-value-bind (result emitted) (child-emit-event (agent-session-runtime-session runtime) event)
    (and emitted (hash-table-p result) (eq (jget result "cancel") t))))

(defun rt-emit-before-switch (runtime reason &optional target-session-file)
  (let ((event (obj "type" "session_before_switch" "reason" reason)))
    (when target-session-file (setf (gethash "targetSessionFile" event) target-session-file))
    (rt-emit-before runtime event)))

(defun rt-teardown (runtime reason &optional target-session-file)
  "Settle any active response first so the aborted turn is persisted to the outgoing session before it is replaced."
  (let ((session (agent-session-runtime-session runtime)) (event (obj "type" "session_shutdown" "reason" reason)))
    (agent-session-abort session)
    (when target-session-file (setf (gethash "targetSessionFile" event) target-session-file))
    (child-emit-event session event)
    (when (agent-session-runtime-before-invalidate runtime) (funcall (agent-session-runtime-before-invalidate runtime)))
    (agent-session-dispose session)))

(defun rt-replace (runtime cwd manager reason previous-session-file)
  (rt-apply runtime
            (funcall (agent-session-runtime-create-runtime runtime)
                     (make-session-runtime-options
                      :cwd cwd :agent-dir (agent-session-services-agent-dir (agent-session-runtime-services runtime))
                      :session-manager manager
                      :session-start-event (make-session-start-event reason previous-session-file)))))

(defun rt-finish (runtime)
  (when (agent-session-runtime-rebind runtime) (funcall (agent-session-runtime-rebind runtime) (agent-session-runtime-session runtime))))

(defun rt-reject-unserved (with-session &optional project-trust-context-factory)
  (when with-session
    (error "the with-session option is not supported yet: it takes the ReplacedSessionContext of AgentSession.createReplacedSessionContext, which is a C2Sessions gap in test/parity/sdk-exports.toml; use the setup option, or read agent-session-runtime-session once the call returned"))
  (when project-trust-context-factory
    (error "the project-trust-context-factory option is not supported: it builds a record of the host's UI functions, which cannot cross the process boundary")))

(defun rt-result (cancelled &optional selected-text)
  (let ((result (obj "cancelled" (if cancelled t :false))))
    (when selected-text (setf (gethash "selectedText" result) selected-text))
    result))

(defun agent-session-runtime-switch-session (runtime session-path &key cwd-override with-session project-trust-context-factory)
  "Switch to the session file SESSION-PATH; CWD-OVERRIDE is the working directory to use when the stored one is gone. The result is {cancelled}."
  (rt-reject-unserved with-session project-trust-context-factory)
  (if (rt-emit-before-switch runtime "resume" session-path)
      (rt-result t)
      (let* ((ctx (agent-session-runtime-ctx runtime))
             (previous (rt-file (agent-session-session-manager (agent-session-runtime-session runtime))))
             (manager (sm-open ctx session-path nil cwd-override)))
        (rt-assert-session-cwd-exists ctx manager (agent-session-runtime-cwd runtime))
        (rt-teardown runtime "resume" (rt-file manager))
        (rt-replace runtime (sm-get-cwd manager) manager "resume" previous)
        (rt-finish runtime)
        (rt-result nil))))

(defun agent-session-runtime-new-session (runtime &key parent-session setup with-session)
  "Start a new session; PARENT-SESSION is the parent session file and SETUP a function that runs with the new session's log
before the session reads it (the session's context is refreshed after it). The result is {cancelled}."
  (rt-reject-unserved with-session)
  (if (rt-emit-before-switch runtime "new")
      (rt-result t)
      (let* ((ctx (agent-session-runtime-ctx runtime)) (cwd (agent-session-runtime-cwd runtime))
             (current (agent-session-session-manager (agent-session-runtime-session runtime)))
             (previous (rt-file current))
             (manager (if (sm-is-persisted current) (sm-create ctx cwd (sm-get-session-dir current)) (sm-in-memory ctx cwd))))
        (when (rt-present parent-session) (sm-new-session manager (obj "parentSession" parent-session)))
        (rt-teardown runtime "new" (rt-file manager))
        (rt-replace runtime cwd manager "new" previous)
        (when setup
          (funcall setup (agent-session-session-manager (agent-session-runtime-session runtime)))
          (agent-session-refresh-context (agent-session-runtime-session runtime)))
        (rt-finish runtime)
        (rt-result nil))))

(defun agent-session-runtime-fork (runtime entry-id &key position with-session)
  "Fork at ENTRY-ID; POSITION is \"before\" (the default) or \"at\". The result is {cancelled} and, when the fork was before a user message, selectedText."
  (rt-reject-unserved with-session)
  (let ((position (or position "before")))
    (when (rt-emit-before runtime (obj "type" "session_before_fork" "entryId" entry-id "position" position))
      (return-from agent-session-runtime-fork (rt-result t)))
    (let* ((ctx (agent-session-runtime-ctx runtime)) (cwd (agent-session-runtime-cwd runtime))
           (current (agent-session-session-manager (agent-session-runtime-session runtime)))
           (entry (sm-get-entry current entry-id)) (selected-text nil) (target-leaf nil))
      (unless (hash-table-p entry) (error "Invalid entry ID for forking"))
      (if (equal position "at")
          (setf target-leaf (jget entry "id"))
          (let ((message (let ((m (jget entry "message"))) (if (hash-table-p m) m (obj)))))
            (unless (and (equal (jget entry "type") "message") (equal (jget message "role") "user"))
              (error "Invalid entry ID for forking"))
            (setf target-leaf (jget entry "parentId") selected-text (rt-user-message-text (jget message "content")))))
      (let ((previous (rt-file current)))
        (flet ((finish (manager cwd)
                 (rt-replace runtime cwd manager "fork" previous)
                 (rt-finish runtime)
                 (rt-result nil selected-text)))
          (cond
            ((sm-is-persisted current)
             (unless previous (error "Persisted session is missing a session file"))
             (let ((session-dir (sm-get-session-dir current)))
               (cond
                 ((not (rt-present target-leaf))
                  (let ((manager (sm-create ctx cwd session-dir)))
                    (sm-new-session manager (obj "parentSession" previous))
                    (rt-teardown runtime "fork" (rt-file manager))
                    (finish manager cwd)))
                 (t
                  (unless (rt-exists-p previous)
                    (error "This session has not been saved yet. Send a message before cloning or forking it."))
                  (let ((manager (sm-open ctx previous session-dir)))
                    (unless (rt-present (sm-create-branched-session manager target-leaf)) (error "Failed to create forked session"))
                    (rt-teardown runtime "fork" (rt-file manager))
                    (finish manager (sm-get-cwd manager)))))))
            (t
             (rt-teardown runtime "fork" (rt-file current))
             (if (rt-present target-leaf)
                 (sm-create-branched-session current target-leaf)
                 (sm-new-session current (and previous (obj "parentSession" previous))))
             (finish current cwd))))))))

(defun rt-split-extension (name)
  "(values stem extension) as os.path.splitext: the extension starts at the last dot that is not the name's first character."
  (let ((dot (position #\. name :from-end t)))
    (if (and dot (plusp dot) (find-if-not (lambda (c) (char= c #\.)) name :end dot))
        (values (subseq name 0 dot) (subseq name dot))
        (values name ""))))

(defun rt-copy-file (source destination)
  (with-open-file (in source :element-type '(unsigned-byte 8))
    (with-open-file (out destination :element-type '(unsigned-byte 8) :direction :output :if-exists :error)
      (let ((buffer (make-array 65536 :element-type '(unsigned-byte 8))))
        (loop for count = (read-sequence buffer in) while (plusp count) do (write-sequence buffer out :end count))))))

(defun agent-session-runtime-import-from-jsonl (runtime input-path &optional cwd-override)
  "Copy the session file INPUT-PATH into the current session directory (unless it is already there) and switch to it. The result is
{cancelled}. A file that does not exist signals session-import-file-not-found-error."
  (let ((resolved (rt-resolve-path input-path)) (ctx (agent-session-runtime-ctx runtime)))
    (unless (rt-exists-p resolved) (error 'session-import-file-not-found-error :file-path resolved))
    (let* ((session-dir (sm-get-session-dir (agent-session-session-manager (agent-session-runtime-session runtime))))
           (base (subseq resolved (1+ (position #\/ resolved :from-end t))))
           (destination (rt-join session-dir base))
           (already-stored (string= (rt-resolve-path destination) resolved)))
      (ensure-directories-exist (rt-join session-dir "x"))
      (unless already-stored
        (multiple-value-bind (stem extension) (rt-split-extension base)
          (loop for suffix from 1 while (rt-exists-p destination)
                do (setf destination (rt-join session-dir (format nil "~A-~D~A" stem suffix extension))))))
      (if (rt-emit-before-switch runtime "resume" destination)
          (rt-result t)
          (let ((previous (rt-file (agent-session-session-manager (agent-session-runtime-session runtime)))))
            (unless already-stored (rt-copy-file resolved destination))
            (let ((manager (sm-open ctx destination session-dir cwd-override)))
              (rt-assert-session-cwd-exists ctx manager (agent-session-runtime-cwd runtime))
              (rt-teardown runtime "resume" (rt-file manager))
              (rt-replace runtime (sm-get-cwd manager) manager "resume" previous)
              (rt-finish runtime)
              (rt-result nil)))))))

(defun agent-session-runtime-dispose (runtime)
  "Emit session_shutdown with the reason \"quit\" to the current session's extensions, run the before-invalidate function and dispose the session."
  (let ((session (agent-session-runtime-session runtime)))
    (child-emit-event session (obj "type" "session_shutdown" "reason" "quit"))
    (when (agent-session-runtime-before-invalidate runtime) (funcall (agent-session-runtime-before-invalidate runtime)))
    (agent-session-dispose session)
    nil))

(defun create-agent-session-runtime (ctx create-runtime options)
  "Pi's createAgentSessionRuntime: build the initial runtime from CREATE-RUNTIME, (lambda (session-runtime-options)) returning a
session-runtime-result, and keep it for the replacements. OPTIONS is a session-runtime-options."
  (rt-assert-session-cwd-exists ctx (session-runtime-options-session-manager options) (session-runtime-options-cwd options))
  (let ((runtime (rt-make ctx create-runtime)))
    (rt-apply runtime (funcall create-runtime options))
    runtime))
