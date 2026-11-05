;;;; SPDX-License-Identifier: MIT
;;;; Pi's createExtensionRuntime() result and ExtensionRunner (core/extensions/runner.ts): the host owns both over the extensions a
;;;; resource loader loaded; the functions that bind actions into them stay in this process and the host calls them.

(in-package #:pig)

;;; ---------------------------------------------------------------- ExtensionRuntime

(defstruct (extension-runtime (:include graph-object) (:conc-name erun-) (:constructor %make-extension-runtime (ctx handle))))

(defun create-extension-runtime (ctx)
  "Pi's createExtensionRuntime(): the state every extension of a load registers against."
  (%make-extension-runtime ctx (graph-new ctx "ExtensionRuntime" "create")))

(defun loaded-extensions-runtime (ctx loaded)
  "LoadExtensionsResult.runtime of a resource loader's loaded-extensions result."
  (let ((loader (loaded-extension-loader loaded)))
    (unless loader (error "the extensions were not read from a resource loader"))
    (%make-extension-runtime ctx (graph-new ctx "ExtensionRuntime" "of" (gobj-handle loader)))))

(def-handle-calls
  (extension-runtime-get-flag-values () "getFlagValues")
  (extension-runtime-set-flag-value (name value) "setFlagValue")
  (extension-runtime-pending-provider-registrations () "getPendingProviderRegistrations")
  (extension-runtime-pending-native-provider-registrations () "getPendingNativeProviderRegistrations")
  (extension-runtime-pending-virtual-model-registrations () "getPendingVirtualModelRegistrations")
  (extension-runtime-mcp-servers () "getMcpServers")
  (extension-runtime-assert-active () "assertActive")
  (extension-runtime-invalidate (&optional message) "invalidate"))

;;; ---------------------------------------------------------------- tables of bound functions

(defparameter *runner-actions*
  '(:send-message :send-user-message :append-entry :set-session-name :get-session-name :set-label :get-active-tools :get-all-tools :get-settings
    :set-active-tools :refresh-tools :get-commands :set-model :get-thinking-level :set-thinking-level))
(defparameter *runner-context-actions*
  '(:get-model :get-scoped-models :is-idle :is-project-trusted :get-signal :abort :has-pending-messages :shutdown :get-context-usage :compact
    :get-system-prompt :get-system-prompt-options :execute-tool :get-callable-tools))
(defparameter *runner-provider-actions* '(:register-provider :unregister-provider))
(defparameter *runner-command-actions* '(:wait-for-idle :new-session :fork :navigate-tree :switch-session :reload))
(defparameter *runner-ui-members*
  '(:select :confirm :input :editor :notify :set-status :set-working-message :set-working-visible :set-hidden-thinking-label :set-widget :set-title
    :paste-to-editor :set-editor-text :get-editor-text :get-tools-expanded :set-tools-expanded))

(defun runner-table (ctx plist allowed what &optional adapt)
  "Name each function of PLIST (alternating keyword and function) for the host under Pi's member name; a NIL function is Pi's undefined."
  (let ((wire (obj)))
    (loop for (key function) on plist by #'cddr
          do (unless (member key allowed) (error "~A has no member ~(~A~)" what key))
             (when function
               (setf (gethash (graph-camel (string key)) wire)
                     (graph-callback-id ctx (if (eq key adapt) (runner-signal-wire function) function)))))
    wire))

(defun runner-signal-wire (function)
  (lambda () (let ((signal (funcall function))) (and signal (obj "aborted" (if (abort-signal-set-p signal) t :false))))))

(defun runner-plist-hash (plist)
  (let ((table (make-hash-table :test 'eq)))
    (loop for (key function) on plist by #'cddr do (when function (setf (gethash key table) function)))
    table))

;;; ---------------------------------------------------------------- ExtensionRunner

(defstruct (extension-runner (:include graph-object) (:conc-name erunner-) (:constructor %make-extension-runner (ctx handle session-manager model-registry)))
  session-manager model-registry
  (context-actions (make-hash-table :test 'eq))
  (command-actions (make-hash-table :test 'eq)))

(defun make-extension-runner (ctx extensions &key runtime (cwd "") session-manager model-registry)
  "new ExtensionRunner(extensions, runtime, cwd, sessionManager, modelRegistry). EXTENSIONS are the extension hash-tables of a loaded-extensions result
(an extension built in this process cannot join: load it with additional-extension-paths); MODEL-REGISTRY is a model-runtime."
  (let ((refs (map 'simple-vector
                   (lambda (extension)
                     (let ((loader (and (hash-table-p extension) (loaded-extension-loader extension))))
                       (unless loader
                         (error "extension ~A was not read from a resource loader: an extension built in this process cannot join the runner, load it with additional-extension-paths"
                                (if (hash-table-p extension) (jget extension "path") extension)))
                       (obj "loader" (gobj-handle loader) "resolvedPath" (jget extension "resolvedPath"))))
                   (jget-list extensions)))
        (wire (obj "cwd" cwd)))
    (setf (gethash "extensions" wire) refs)
    (when runtime (setf (gethash "runtime" wire) (gobj-handle runtime)))
    (when session-manager (setf (gethash "sessionManager" wire) (gobj-handle session-manager)))
    (when model-registry (setf (gethash "modelRegistry" wire) (gobj-handle model-registry)))
    (%make-extension-runner ctx (graph-new ctx "ExtensionRunner" "new" wire) session-manager model-registry)))

(defun jget-list (value)
  "A JSON array, or a list, as a list."
  (if (listp value) value (coerce value 'list)))

(defun runner-bind-core (runner actions context-actions &optional provider-actions)
  "bindCore: ACTIONS, CONTEXT-ACTIONS and PROVIDER-ACTIONS are plists from Pi's member names as keywords (:send-message, :is-idle, :register-provider ...) to functions
taking Pi's arguments as JSON values; a NIL function is Pi's undefined. :get-signal answers an abort-signal or NIL."
  (let* ((ctx (gobj-ctx runner))
         (wire (list (runner-table ctx actions *runner-actions* "ExtensionActions")
                     (runner-table ctx context-actions *runner-context-actions* "ExtensionContextActions" :get-signal))))
    (when provider-actions (setf wire (append wire (list (runner-table ctx provider-actions *runner-provider-actions* "ProviderActions")))))
    (apply #'graph-invoke ctx (gobj-handle runner) "bindCore" wire)
    (setf (erunner-context-actions runner) (runner-plist-hash context-actions))
    nil))

(defun runner-bind-command-context (runner &optional actions)
  "bindCommandContext(actions?): ACTIONS is a plist (:wait-for-idle :new-session :fork :navigate-tree :switch-session :reload); NIL restores the defaults."
  (if actions
      (progn (graph-invoke (gobj-ctx runner) (gobj-handle runner) "bindCommandContext"
                           (runner-table (gobj-ctx runner) actions *runner-command-actions* "ExtensionCommandContextActions"))
             (setf (erunner-command-actions runner) (runner-plist-hash actions)))
      (progn (graph-invoke (gobj-ctx runner) (gobj-handle runner) "bindCommandContext")
             (setf (erunner-command-actions runner) (make-hash-table :test 'eq))))
  nil)

(defun runner-on-error (runner listener)
  "onError(listener): LISTENER, (lambda (report)), runs for each error the runner reports. Returns the function that removes it."
  (let ((handle (graph-invoke (gobj-ctx runner) (gobj-handle runner) "onError" (graph-callback-id (gobj-ctx runner) listener))))
    (lambda () (graph-invoke (gobj-ctx runner) (gobj-handle runner) "offError" handle) nil)))

(def-handle-calls
  (runner-emit-error (error) "emitError")
  (runner-get-extension-paths () "getExtensionPaths")
  (runner-get-all-registered-tools () "getAllRegisteredTools")
  (runner-get-tool-definition (tool-name) "getToolDefinition")
  (runner-get-flags () "getFlags")
  (runner-set-flag-value (name value) "setFlagValue")
  (runner-get-flag-values () "getFlagValues")
  (runner-get-shortcuts (resolved-keybindings) "getShortcuts")
  (runner-get-shortcut-diagnostics () "getShortcutDiagnostics")
  (runner-has-handlers-p (event-type) "hasHandlers" :true)
  (runner-get-message-renderer-p (custom-type) "getMessageRenderer" :true)
  (runner-get-entry-renderer-p (custom-type) "getEntryRenderer" :true)
  (runner-get-registered-commands () "getRegisteredCommands")
  (runner-get-command-diagnostics () "getCommandDiagnostics")
  (runner-get-command (name) "getCommand")
  (runner-shutdown () "shutdown")
  (runner-get-active-tools () "getActiveTools")
  (runner-invalidate (&optional message) "invalidate")
  (runner-report-unhandled-mcp-servers () "reportUnhandledMcpServers")
  (runner-has-ui-p () "hasUI" :true)
  (runner-emit (event) "emit" :async)
  (runner-emit-tool-call (event) "emitToolCall" :async)
  (runner-emit-tool-result (event) "emitToolResult" :async)
  (runner-emit-input (text images source &optional streaming-behavior) "emitInput" :async)
  (runner-emit-context (messages) "emitContext" :async)
  (runner-emit-before-agent-start (trigger system-prompt-options) "emitBeforeAgentStart" :async)
  (runner-emit-before-provider-request (payload) "emitBeforeProviderRequest" :async)
  (runner-emit-before-provider-headers (headers) "emitBeforeProviderHeaders" :async)
  (runner-emit-user-bash (event) "emitUserBash" :async)
  (runner-emit-message-end (event) "emitMessageEnd" :async)
  (runner-emit-resources-discover (cwd reason) "emitResourcesDiscover" :async)
  (runner-emit-cache-warming-decision (event) "emitCacheWarmingDecision" :async))

(defun runner-get-model-registry (runner) "The registry the runner was built with." (erunner-model-registry runner))

(defun runner-get-markdown-transformers (runner) (let ((n (graph-call runner "getMarkdownTransformers"))) (if (integerp n) n 0)))

(defun runner-resolve-tool-renderers (runner tool-name &optional base)
  "resolveToolRenderers(toolName, base): the renderers of calls to TOOL-NAME as {renderCall, renderResult, renderShell?} (which renderers exist), the extensions'
resolvers in load order and then BASE, a function of this process that answers the renderers to use when no extension chose. NIL when none apply."
  (graph-call runner "resolveToolRenderers" tool-name (graph-callback-id (gobj-ctx runner) (and base (lambda (&rest args) (declare (ignore args)) (funcall base))))))

(defun runner-emit-boundary (runner base-event build-context)
  "emitBoundary(baseEvent, buildContext): BUILD-CONTEXT, (lambda (entries)), answers the context preview of drafted entries."
  (graph-call-async runner "emitBoundary" base-event
                    (graph-callback-id (gobj-ctx runner) (lambda (&optional entries) (funcall build-context entries)))))

(defun runner-set-ui-context (runner &optional ui (mode "print"))
  "setUIContext(ui?, mode): UI is a plist from the ExtensionUIContext data members as keywords (:select :confirm :input :editor :notify :set-status
:set-working-message :set-working-visible :set-hidden-thinking-label :set-widget :set-title :paste-to-editor :set-editor-text :get-editor-text
:get-tools-expanded :set-tools-expanded) to functions of this process; NIL is no UI."
  (graph-call runner "setUIContext" (and ui (runner-table (gobj-ctx runner) ui *runner-ui-members* "ExtensionUIContext")) mode)
  nil)

;;; ---------------------------------------------------------------- the runner's UI context

(defstruct (runner-ui (:constructor runner-get-ui-context (runner))) runner)

(defun runner-ui-call (ui member &rest args)
  (apply #'graph-call-async (runner-ui-runner ui) "ui" member args))

(defmacro def-runner-ui (&rest specs)
  `(progn ,@(loop for (name params member . flags) in specs
                  collect (let ((vars (loop for p in params unless (member p lambda-list-keywords) collect (if (consp p) (car p) p))))
                            `(defun ,name (ui ,@params)
                               ,(let ((call `(runner-ui-call ui ,member ,@vars)))
                                  (cond ((member :true flags) `(graph-bool ,call))
                                        ((member :text flags) `(or ,call ""))
                                        (t `(progn ,call nil)))))))))

(def-runner-ui
  (runner-ui-notify (message &optional kind) "notify")
  (runner-ui-set-status (key text) "setStatus")
  (runner-ui-set-working-message (&optional message) "setWorkingMessage")
  (runner-ui-set-working-visible (visible) "setWorkingVisible")
  (runner-ui-set-hidden-thinking-label (&optional label) "setHiddenThinkingLabel")
  (runner-ui-set-widget (key content &optional options) "setWidget")
  (runner-ui-set-title (title) "setTitle")
  (runner-ui-paste-to-editor (text) "pasteToEditor")
  (runner-ui-set-editor-text (text) "setEditorText")
  (runner-ui-get-editor-text () "getEditorText" :text)
  (runner-ui-get-tools-expanded-p () "getToolsExpanded" :true)
  (runner-ui-set-tools-expanded (expanded) "setToolsExpanded"))

(defun runner-ui-select (ui title options &optional opts)
  "ui.select: the chosen option, NIL when cancelled."
  (runner-ui-call ui "select" title (coerce options 'simple-vector) opts))
(defun runner-ui-confirm (ui title message &optional opts) (graph-bool (runner-ui-call ui "confirm" title message opts)))
(defun runner-ui-input (ui title &optional placeholder opts) (runner-ui-call ui "input" title placeholder opts))
(defun runner-ui-editor (ui title &optional prefill) (runner-ui-call ui "editor" title prefill))

;;; ---------------------------------------------------------------- contexts

(defstruct (runner-context (:constructor %make-runner-context (runner spec tool-call-id default-signal)))
  "Pi's ExtensionContext (and its tool and command forms) of a runner: the getters read the runner at call time."
  runner spec tool-call-id default-signal)

(defun runner-create-context (runner) (%make-runner-context runner (obj "kind" "context") nil nil))
(defun runner-create-tool-context (runner tool-call-id &optional signal)
  "SIGNAL is the default cancellation of nested calls."
  (%make-runner-context runner (obj "kind" "tool" "toolCallId" tool-call-id) tool-call-id signal))
(defun runner-create-command-context (runner) (%make-runner-context runner (obj "kind" "command") nil nil))

(defun rctx-get (context member &rest args)
  (apply #'graph-call (runner-context-runner context) "context" (runner-context-spec context) member args))

(defun rctx-assert-active (context) (rctx-get context "assertActive") nil)

(defun runner-context-ui (context) (rctx-assert-active context) (runner-get-ui-context (runner-context-runner context)))
(defun runner-context-mode (context) (or (rctx-get context "mode") ""))
(defun runner-context-has-ui-p (context) (graph-bool (rctx-get context "hasUI")))
(defun runner-context-cwd (context) (or (rctx-get context "cwd") ""))
(defun runner-context-session-manager (context) (rctx-assert-active context) (erunner-session-manager (runner-context-runner context)))
(defun runner-context-model-registry (context) (rctx-assert-active context) (erunner-model-registry (runner-context-runner context)))
(defun runner-context-model (context) (rctx-get context "model"))
(defun runner-context-scoped-models (context) (or (rctx-get context "scopedModels") (vector)))
(defun runner-context-thinking-level (context) (or (rctx-get context "thinkingLevel") ""))
(defun runner-context-is-idle-p (context) (graph-bool (rctx-get context "isIdle")))
(defun runner-context-is-project-trusted-p (context) (graph-bool (rctx-get context "isProjectTrusted")))
(defun runner-context-abort (context) (rctx-get context "abort") nil)
(defun runner-context-has-pending-messages-p (context) (graph-bool (rctx-get context "hasPendingMessages")))
(defun runner-context-shutdown (context) (rctx-get context "shutdown") nil)
(defun runner-context-get-context-usage (context) (rctx-get context "getContextUsage"))
(defun runner-context-get-system-prompt (context) (or (rctx-get context "getSystemPrompt") ""))
(defun runner-context-tools (context) (or (rctx-get context "tools") (vector)))
(defun runner-context-get-system-prompt-options (context) (rctx-get context "getSystemPromptOptions"))

(defun runner-context-signal (context)
  "ctx.signal: NIL when no run is active; otherwise an abort-signal set when the host's was aborted at the time of the read."
  (let ((wire (rctx-get context "signal")))
    (when wire
      (let ((signal (make-abort-signal)))
        (when (graph-bool (jget wire "aborted")) (signal-abort signal))
        signal))))

(defun rctx-action (context table name)
  (rctx-assert-active context)
  (gethash name (funcall table (runner-context-runner context))))

(defun runner-context-compact (context &optional options)
  "ctx.compact(options?): calls the bound compact function here, so its callbacks are this process's."
  (let ((compact (rctx-action context #'erunner-context-actions :compact)))
    (when compact (funcall compact (or options (obj))))
    nil))

(defun runner-context-execute-tool (context name args &optional options)
  "ctx.executeTool(name, args, options?): calls the bound execute-tool function with OPTIONS (a hash-table; its \"signal\" defaults to the one the context was created
with). Without a bound function the outcome is Pi's error outcome."
  (let ((execute (rctx-action context #'erunner-context-actions :execute-tool))
        (merged (obj)) (id (runner-context-tool-call-id context)))
    (when options (maphash (lambda (k v) (setf (gethash k merged) v)) options))
    (when (and (null (jget merged "signal")) (runner-context-default-signal context))
      (setf (gethash "signal" merged) (runner-context-default-signal context)))
    (if execute
        (funcall execute id name args merged)
        (obj "toolCall" (obj "type" "toolCall" "id" (format nil "~A/0" id) "name" name "arguments" (obj))
             "result" (obj "content" (vector (obj "type" "text" "text" "Nested tool calls are not available in this context")) "details" (obj))
             "isError" t))))

(defmacro def-runner-command-actions (&rest specs)
  `(progn ,@(loop for (name params key default) in specs
                  collect (let ((vars (loop for p in params unless (member p lambda-list-keywords) collect (if (consp p) (car p) p))))
                            `(defun ,name (context ,@params)
                               (let ((action (rctx-action context #'erunner-command-actions ,key)))
                                 (if action (funcall action ,@(mapcar (lambda (v) (if (eq v 'options) '(or options (obj)) v)) vars)) ,default)))))))

(def-runner-command-actions
  (runner-context-wait-for-idle () :wait-for-idle nil)
  (runner-context-new-session (&optional options) :new-session (obj "cancelled" :false))
  (runner-context-fork (entry-id &optional options) :fork (obj "cancelled" :false))
  (runner-context-navigate-tree (target-id &optional options) :navigate-tree (obj "cancelled" :false))
  (runner-context-switch-session (session-path &optional options) :switch-session (obj "cancelled" :false))
  (runner-context-reload () :reload nil))
