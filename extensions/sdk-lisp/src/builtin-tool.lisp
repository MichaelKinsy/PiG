;;;; SPDX-License-Identifier: MIT
;;;; Pi's create<Tool>ToolDefinition, create<Tool>Tool, createCodingTools, createReadOnlyTools and createLocalBashOperations (core/tools/*.ts).
;;;; The host owns the implementation: the factories reach it through host calls (builtinTool.definition, builtinTool.execute, builtinTool.localExec).
;;;; An override that wraps a built-in is written as in Pi: take the definition, keep its execute function, register a copy whose execute post-processes.
;;;; The call is awaited: it returns the tool's result (a hash-table in the tool-result shape) or signals the error the tool threw; Pi's `operations` and
;;;; `spawn-hook` functions stay in this process and run, on other threads, while the call does.

(in-package #:pig)

(declaim (ftype function call-nested running-extension helper-call bash-ops-register bash-ops-release register-tool ctx-register-tool))

;;; ---------------------------------------------------------------- options

(defmacro def-tool-options (name &rest slots)
  "A struct NAME with a keyword constructor MAKE-NAME over SLOTS."
  `(defstruct (,name (:constructor ,(intern (format nil "MAKE-~A" name)) (&key ,@slots))) ,@slots))

(def-tool-options model-image-resize-options max-width max-height max-bytes jpeg-quality)
(def-tool-options read-operations read-file access detect-image-mime-type)
(def-tool-options read-tool-options auto-resize-images resize-options operations)
(def-tool-options write-operations write-file mkdir)
(def-tool-options write-tool-options operations)
(def-tool-options edit-operations read-file write-file access)
(def-tool-options edit-tool-options operations)
(def-tool-options grep-operations is-directory read-file)
(def-tool-options grep-tool-options operations)
(def-tool-options find-operations exists glob)
(def-tool-options find-tool-options operations)
(def-tool-options ls-stat is-directory)
(def-tool-options ls-operations exists stat readdir)
(def-tool-options ls-tool-options operations)
(def-tool-options bash-tool-options operations command-prefix shell-path expose-session-environment spawn-hook)
(def-tool-options power-shell-tool-options operations expose-session-environment spawn-hook)
(def-tool-options tools-options read bash power-shell write edit grep find ls)

(defstruct (bt-spec (:constructor make-bt-spec (name cwd)))
  "One create<Tool>ToolDefinition(cwd, options) call: which tool, where, and with what options. CALLBACKS holds the functions that stay here."
  name cwd (options (obj)) (callbacks (make-hash-table :test 'equal)) (bash-operations nil))

(defun bt-require (tool &rest members)
  (loop for (name value) on members by #'cddr
        do (unless (functionp value) (error "~A: operations.~A is required" tool name))))

(defun bt-set-operations (spec names) (setf (gethash "operations" (bt-spec-options spec)) (coerce names 'simple-vector)))

(defun bt-callback (spec member function) (setf (gethash member (bt-spec-callbacks spec)) function))

;;; base64: file contents cross as base64 strings
(defparameter *bt-base64* "ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz0123456789+/")

(defun bt-base64-encode (octets)
  (with-output-to-string (out)
    (loop for i from 0 below (length octets) by 3
          do (let* ((n (- (min (length octets) (+ i 3)) i))
                    (v (logior (ash (aref octets i) 16) (if (> n 1) (ash (aref octets (+ i 1)) 8) 0) (if (> n 2) (aref octets (+ i 2)) 0))))
               (dotimes (k 4)
                 (write-char (if (<= k n) (char *bt-base64* (ldb (byte 6 (- 18 (* 6 k))) v)) #\=) out))))))

(defun bt-base64-decode (text)
  (let ((octets (make-array (floor (* 3 (length text)) 4) :element-type '(unsigned-byte 8) :fill-pointer 0)) (bits 0) (count 0))
    (loop for c across text
          do (let ((v (position c *bt-base64*)))
               (when v
                 (setf bits (logior (ash bits 6) v)) (incf count 6)
                 (when (>= count 8)
                   (decf count 8)
                   (vector-push (ldb (byte 8 count) bits) octets)
                   (setf bits (ldb (byte count 0) bits))))))
    (coerce octets '(simple-array (unsigned-byte 8) (*)))))

(defun bt-octets (value)
  "The octets of a read-file result: an octet vector, or a string as UTF-8."
  (if (stringp value) (sb-ext:string-to-octets value :external-format :utf-8) value))

(defun bt-read-spec (cwd options)
  (let ((spec (make-bt-spec "read" cwd)) (table (bt-spec-options spec)))
    (when options
      (let ((resize (read-tool-options-resize-options options)) (ops (read-tool-options-operations options)))
        (when (read-tool-options-auto-resize-images options)
          (setf (gethash "autoResizeImages" table) (graph-bool-arg (read-tool-options-auto-resize-images options))))
        (when resize
          (setf (gethash "resizeOptions" table)
                (if (model-image-resize-options-p resize)
                    (let ((wire (obj)))
                      (loop for (key value) on (list "maxWidth" (model-image-resize-options-max-width resize) "maxHeight" (model-image-resize-options-max-height resize)
                                                     "maxBytes" (model-image-resize-options-max-bytes resize) "jpegQuality" (model-image-resize-options-jpeg-quality resize))
                              by #'cddr when value do (setf (gethash key wire) value))
                      wire)
                    resize)))
        (when ops
          (let ((read-file (read-operations-read-file ops)) (access (read-operations-access ops)) (detect (read-operations-detect-image-mime-type ops))
                (names (list "readFile" "access")))
            (bt-require "read" "read-file" read-file "access" access)
            (bt-callback spec "operations.readFile" (lambda (args) (bt-base64-encode (bt-octets (funcall read-file (jget args "path"))))))
            (bt-callback spec "operations.access" (lambda (args) (funcall access (jget args "path")) nil))
            (when detect
              (setf names (append names (list "detectImageMimeType")))
              (bt-callback spec "operations.detectImageMimeType" (lambda (args) (funcall detect (jget args "path")))))
            (bt-set-operations spec names)))))
    spec))

(defun bt-write-spec (cwd options)
  (let ((spec (make-bt-spec "write" cwd)) (ops (and options (write-tool-options-operations options))))
    (when ops
      (let ((write-file (write-operations-write-file ops)) (mkdir (write-operations-mkdir ops)))
        (bt-require "write" "write-file" write-file "mkdir" mkdir)
        (bt-callback spec "operations.writeFile" (lambda (args) (funcall write-file (jget args "path") (jget args "content")) nil))
        (bt-callback spec "operations.mkdir" (lambda (args) (funcall mkdir (jget args "path")) nil))
        (bt-set-operations spec '("writeFile" "mkdir"))))
    spec))

(defun bt-edit-spec (cwd options)
  (let ((spec (make-bt-spec "edit" cwd)) (ops (and options (edit-tool-options-operations options))))
    (when ops
      (let ((read-file (edit-operations-read-file ops)) (write-file (edit-operations-write-file ops)) (access (edit-operations-access ops)))
        (bt-require "edit" "read-file" read-file "write-file" write-file "access" access)
        (bt-callback spec "operations.readFile" (lambda (args) (bt-base64-encode (bt-octets (funcall read-file (jget args "path"))))))
        (bt-callback spec "operations.writeFile" (lambda (args) (funcall write-file (jget args "path") (jget args "content")) nil))
        (bt-callback spec "operations.access" (lambda (args) (funcall access (jget args "path")) nil))
        (bt-set-operations spec '("readFile" "writeFile" "access"))))
    spec))

(defun bt-grep-spec (cwd options)
  (let ((spec (make-bt-spec "grep" cwd)) (ops (and options (grep-tool-options-operations options))))
    (when ops
      (let ((is-directory (grep-operations-is-directory ops)) (read-file (grep-operations-read-file ops)) (names (list "isDirectory")))
        (bt-require "grep" "is-directory" is-directory)
        (bt-callback spec "operations.isDirectory" (lambda (args) (graph-bool-arg (funcall is-directory (jget args "path")))))
        (when read-file
          (setf names (append names (list "readFile")))
          (bt-callback spec "operations.readFile" (lambda (args) (funcall read-file (jget args "path")))))
        (bt-set-operations spec names)))
    spec))

(defun bt-find-spec (cwd options)
  (let ((spec (make-bt-spec "find" cwd)) (ops (and options (find-tool-options-operations options))))
    (when ops
      (let ((exists (find-operations-exists ops)) (glob (find-operations-glob ops)))
        (bt-require "find" "exists" exists "glob" glob)
        (bt-callback spec "operations.exists" (lambda (args) (graph-bool-arg (funcall exists (jget args "path")))))
        (bt-callback spec "operations.glob"
                     (lambda (args)
                       (coerce (funcall glob (jget args "pattern") (jget args "cwd")
                                        (obj "ignore" (let ((i (jget args "ignore"))) (if (vectorp i) i (vector))) "limit" (jget args "limit")))
                               'simple-vector)))
        (bt-set-operations spec '("exists" "glob"))))
    spec))

(defun bt-ls-spec (cwd options)
  (let ((spec (make-bt-spec "ls" cwd)) (ops (and options (ls-tool-options-operations options))))
    (when ops
      (let ((exists (ls-operations-exists ops)) (stat (ls-operations-stat ops)) (readdir (ls-operations-readdir ops)))
        (bt-require "ls" "exists" exists "stat" stat "readdir" readdir)
        (bt-callback spec "operations.exists" (lambda (args) (graph-bool-arg (funcall exists (jget args "path")))))
        (bt-callback spec "operations.stat"
                     (lambda (args)
                       (let* ((value (funcall stat (jget args "path"))) (directory (ls-stat-is-directory value)))
                         (obj "isDirectory" (graph-bool-arg (if (functionp directory) (funcall directory) directory))))))
        (bt-callback spec "operations.readdir" (lambda (args) (coerce (funcall readdir (jget args "path")) 'simple-vector)))
        (bt-set-operations spec '("exists" "stat" "readdir"))))
    spec))

(defun bt-shell-spec (name cwd options)
  (let ((spec (make-bt-spec name cwd)) (table nil))
    (setf table (bt-spec-options spec))
    (when options
      (let ((bash (equal name "bash")))
        (let ((expose (if bash (bash-tool-options-expose-session-environment options) (power-shell-tool-options-expose-session-environment options))))
          (when expose (setf (gethash "exposeSessionEnvironment" table) (graph-bool-arg expose))))
        (when bash
          (when (plusp (length (bash-tool-options-command-prefix options))) (setf (gethash "commandPrefix" table) (bash-tool-options-command-prefix options)))
          (when (plusp (length (bash-tool-options-shell-path options))) (setf (gethash "shellPath" table) (bash-tool-options-shell-path options))))
        (let ((operations (if bash (bash-tool-options-operations options) (power-shell-tool-options-operations options))))
          (when operations
            (unless (functionp (bash-operations-exec operations)) (error "~A: operations.exec is required" name))
            (bt-set-operations spec '("exec"))
            (setf (bt-spec-bash-operations spec) operations)))
        (let ((hook (if bash (bash-tool-options-spawn-hook options) (power-shell-tool-options-spawn-hook options))))
          (when hook
            (setf (gethash "spawnHook" table) t)
            (bt-callback spec "spawnHook" hook)))))
    spec))

(defun bt-spec-of (name cwd options)
  (cond ((equal name "read") (bt-read-spec cwd options)) ((equal name "write") (bt-write-spec cwd options))
        ((equal name "edit") (bt-edit-spec cwd options)) ((equal name "grep") (bt-grep-spec cwd options))
        ((equal name "find") (bt-find-spec cwd options)) ((equal name "ls") (bt-ls-spec cwd options))
        (t (bt-shell-spec name cwd options))))

;;; ---------------------------------------------------------------- callbacks the host runs

(defvar *bt-lock* (sb-thread:make-mutex :name "pig builtin tools"))
(defvar *bt-seq* 0)
(defvar *bt-callbacks* (make-hash-table :test 'equal)
  "Call handle -> member -> function: the functions of the calls in flight. Ids are unique across the process, so one table serves every extension of a packed cell.")
(defvar *bt-exec-data* (make-hash-table :test 'equal) "Execute id -> the output sink of a local execution.")

(defun bt-next-id (prefix) (sb-thread:with-mutex (*bt-lock*) (format nil "~A~D" prefix (incf *bt-seq*))))

(def-request "builtin_tool_callback" (ext ctx req)
  (let* ((args (jget req "args")) (handle (jget args "handle" "")) (member (jget args "member" ""))
         (callbacks (sb-thread:with-mutex (*bt-lock*) (gethash handle *bt-callbacks*)))
         (function (and callbacks (gethash member callbacks))))
    (unless callbacks (error "unknown built-in tool callbacks: ~A" handle))
    (unless function (error "no built-in tool callback ~A" member))
    (funcall function (let ((a (jget args "args"))) (if (hash-table-p a) a (obj))))))

(def-request "builtin_tool_exec_data" (ext ctx req)
  (let* ((args (jget req "args"))
         (sink (sb-thread:with-mutex (*bt-lock*) (gethash (jget args "executeId" "") *bt-exec-data*))))
    (when sink (funcall sink (bt-base64-decode (let ((d (jget args "data"))) (if (stringp d) d "")))))
    nil))

(defun bt-wire-result (result)
  "The host's AgentToolResult as the hash a tool returns, keeping the order of its members."
  (let ((copy (obj)))
    (maphash (lambda (key value)
               (setf (gethash (cond ((equal key "structuredContent") "structured_content") ((equal key "isError") "is_error") (t key)) copy) value))
             (if (hash-table-p result) result (obj)))
    copy))

(defun bt-watch-signal (signal function)
  "Call FUNCTION once SIGNAL is set; returns the function that ends the watch."
  (let* ((done nil)
         (thread (sb-thread:make-thread (lambda () (loop until done do (when (abort-signal-wait signal 0.01) (unless done (funcall function)) (return))))
                                        :name "pig-builtin-cancel")))
    (lambda () (setf done t) (sb-thread:join-thread thread :default nil))))

(defun bt-cancel (ext execute-id)
  (handler-case (host-call ext "executeTool.cancel" (obj "executeId" execute-id))
    (error () nil)))

(defun bt-execute (ext ctx spec tool-call-id params signal forward-signal on-update)
  "execute(toolCallId, params, signal, onUpdate, ctx) of a built-in tool's definition. CTX NIL is an undefined context."
  (ensure-jsonable params (format nil "arguments of tool ~A" (bt-spec-name spec)))
  (let* ((handle (and (plusp (hash-table-count (bt-spec-callbacks spec))) (bt-next-id "builtin-")))
         (execute-id nil)
         (payload (obj "name" (bt-spec-name spec) "cwd" (bt-spec-cwd spec) "toolCallId" tool-call-id "params" params)))
    (when (plusp (hash-table-count (bt-spec-options spec))) (setf (gethash "options" payload) (bt-spec-options spec)))
    (when ctx (setf (gethash "context" payload) t))
    (when handle
      (sb-thread:with-mutex (*bt-lock*) (setf (gethash handle *bt-callbacks*) (bt-spec-callbacks spec)))
      (setf (gethash "callbacks" payload) handle))
    (when (bt-spec-bash-operations spec)
      ;; the shell tool's exec rides the user_bash exec request under the same handle
      (setf handle (or handle (bt-next-id "builtin-")) (gethash "callbacks" payload) handle)
      (bash-ops-register (bt-spec-bash-operations spec) handle))
    (when (and signal (abort-signal-set-p signal)) (setf (gethash "aborted" payload) t))
    (setf execute-id (bt-next-id "e"))
    (unwind-protect
         (bt-wire-result (call-nested ext (and forward-signal ctx) "builtinTool.execute" payload execute-id on-update signal))
      (when handle
        (sb-thread:with-mutex (*bt-lock*) (remhash handle *bt-callbacks*))
        (when (bt-spec-bash-operations spec) (bash-ops-release handle))))))

;;; ---------------------------------------------------------------- definitions and tools

(defstruct (builtin-tool-definition)
  "The definition Pi's create<Tool>ToolDefinition returns: name, label, description, prompt snippet and guidelines, parameters, execution mode and prepare-arguments
(edit's, which the host runs), with an EXECUTE, (lambda (ctx params)), that runs the built-in tool on the host. Register a copy (COPY-BUILTIN-TOOL-DEFINITION) whose
execute wraps it to override the built-in. The host draws the built-in tool's call and result itself, so a definition that sets no renderers keeps the built-in's."
  name label description parameters output-schema constrained-sampling execution-mode prompt-snippet prompt-guidelines render-shell prepare-arguments execute spec)

(defun bt-definition-reply (spec)
  (let ((args (obj "name" (bt-spec-name spec) "cwd" (bt-spec-cwd spec))))
    (when (plusp (hash-table-count (bt-spec-options spec))) (setf (gethash "options" args) (bt-spec-options spec)))
    (helper-call "builtinTool.definition" args)))

(defun bt-prepare-arguments (spec params)
  "prepareArguments(args) of a built-in tool's definition, which the host runs; like Pi's synchronous hook it takes no signal."
  (ensure-jsonable params (format nil "arguments of tool ~A" (bt-spec-name spec)))
  (let ((args (obj "name" (bt-spec-name spec) "cwd" (bt-spec-cwd spec) "args" params)))
    (when (plusp (hash-table-count (bt-spec-options spec))) (setf (gethash "options" args) (bt-spec-options spec)))
    (helper-call "builtinTool.prepareArguments" args)))

(defun bt-present (value) (and value (not (eq value :null)) (not (eq value :false)) value))

(defun bt-create-definition (spec)
  (let ((reply (bt-definition-reply spec)))
    (make-builtin-tool-definition
     :name (jget reply "name") :label (jget reply "label") :description (jget reply "description") :parameters (jget reply "parameters")
     :spec spec
     :execute (lambda (ctx params)
                (bt-execute (ctx-extension ctx) ctx spec (ctx-tool-call-id ctx) params nil t
                            (and (ctx-tool-call-id ctx) (plusp (length (ctx-request-id ctx))) (lambda (partial) (ctx-on-update ctx partial)))))
     :prompt-snippet (let ((v (jget reply "promptSnippet"))) (and (stringp v) (plusp (length v)) v))
     :prompt-guidelines (let ((v (jget reply "promptGuidelines"))) (and (vectorp v) (plusp (length v)) (coerce v 'list)))
     :constrained-sampling (bt-present (jget reply "constrainedSampling"))
     :execution-mode (let ((v (jget reply "executionMode"))) (and (stringp v) (plusp (length v)) v))
     :output-schema (bt-present (jget reply "outputSchema"))
     :render-shell (and (equal (jget reply "renderShell") "self") "self")
     :prepare-arguments (and (eq (jget reply "preparesArguments") t) (lambda (params) (bt-prepare-arguments spec params))))))

(defun builtin-tool-definition-execute-with (definition ctx tool-call-id params &key signal on-update)
  "Pi's definition.execute(toolCallId, params, signal, onUpdate, ctx). A NIL signal is Pi's undefined: only the call itself ends it. The tool reads the
session's working directory, model and identity from CTX; the cwd given to the factory applies only to a tool without a context (BUILTIN-TOOL)."
  (bt-execute (ctx-extension ctx) ctx (builtin-tool-definition-spec definition) tool-call-id params signal nil on-update))

(defstruct (builtin-tool)
  "The AgentTool Pi's create<Tool>Tool returns: the built-in tool without an extension context, working in the directory given to the factory."
  name label description parameters output-schema constrained-sampling execution-mode prompt-snippet prompt-guidelines spec)

(defun builtin-tool-execute (tool tool-call-id params &key signal on-update)
  "Pi's tool.execute(toolCallId, params, signal, onUpdate). It runs over the connection of the running extension."
  (bt-execute (running-extension) nil (builtin-tool-spec tool) tool-call-id params signal nil on-update))

(defun bt-create-tool (spec)
  (let ((reply (bt-definition-reply spec)) (shell (member (bt-spec-name spec) '("bash" "powershell") :test #'equal)))
    (make-builtin-tool
     :name (jget reply "name") :label (jget reply "label") :description (jget reply "description") :parameters (jget reply "parameters")
     :output-schema (jget reply "outputSchema") :constrained-sampling (jget reply "constrainedSampling") :spec spec
     :execution-mode (let ((v (jget reply "executionMode"))) (and (stringp v) (plusp (length v)) v))
     :prompt-snippet (and shell (let ((v (jget reply "promptSnippet"))) (and (stringp v) (plusp (length v)) v)))
     :prompt-guidelines (and shell (let ((v (jget reply "promptGuidelines"))) (and (vectorp v) (plusp (length v)) (coerce v 'list)))))))

(defmacro def-builtin-factories (&rest specs)
  `(progn ,@(loop for (tool name) in specs
                  collect `(defun ,(intern (format nil "CREATE-~A-TOOL-DEFINITION" name)) (cwd &optional options)
                             ,(format nil "Pi's create~ToolDefinition(cwd, options): asks the host for the built-in tool's definition, so it may be called while the factory runs." name)
                             (bt-create-definition (bt-spec-of ,tool cwd options)))
                  collect `(defun ,(intern (format nil "CREATE-~A-TOOL" name)) (cwd &optional options)
                             (bt-create-tool (bt-spec-of ,tool cwd options))))))

(def-builtin-factories ("read" read) ("write" write) ("edit" edit) ("bash" bash) ("grep" grep) ("find" find) ("ls" ls) ("powershell" powershell))

(defun create-coding-tools (cwd &optional options)
  "Pi's createCodingTools(cwd, options): read, bash, edit and write."
  (flet ((member-options (accessor) (and options (funcall accessor options))))
    (list (create-read-tool cwd (member-options #'tools-options-read)) (create-bash-tool cwd (member-options #'tools-options-bash))
          (create-edit-tool cwd (member-options #'tools-options-edit)) (create-write-tool cwd (member-options #'tools-options-write)))))

(defun create-read-only-tools (cwd &optional options)
  "Pi's createReadOnlyTools(cwd, options): read, grep, find and ls."
  (flet ((member-options (accessor) (and options (funcall accessor options))))
    (list (create-read-tool cwd (member-options #'tools-options-read)) (create-grep-tool cwd (member-options #'tools-options-grep))
          (create-find-tool cwd (member-options #'tools-options-find)) (create-ls-tool cwd (member-options #'tools-options-ls)))))

(defun register-builtin-tool-definition (target definition)
  "Register DEFINITION, or a copy of it with another EXECUTE, as a tool of TARGET (an extension or a ctx): this overrides the built-in tool of its name."
  (apply (if (extension-p target) #'register-tool #'ctx-register-tool) target (builtin-tool-definition-name definition)
         (list :label (builtin-tool-definition-label definition) :description (builtin-tool-definition-description definition)
               :parameters (builtin-tool-definition-parameters definition) :execute (builtin-tool-definition-execute definition)
               :prompt-snippet (builtin-tool-definition-prompt-snippet definition) :prompt-guidelines (builtin-tool-definition-prompt-guidelines definition)
               :constrained-sampling (builtin-tool-definition-constrained-sampling definition)
               :render-shell (or (builtin-tool-definition-render-shell definition) "default")
               :prepare-arguments (builtin-tool-definition-prepare-arguments definition)
               :execution-mode (builtin-tool-definition-execution-mode definition) :output-schema (builtin-tool-definition-output-schema definition))))

;;; ---------------------------------------------------------------- local shell operations

(defun bt-local-exec (shell shell-path command cwd options)
  (let* ((ext (running-extension)) (execute-id (bt-next-id "e")) (failure nil)
         (on-data (bash-exec-options-on-data options)) (signal (bash-exec-options-signal options))
         (payload (obj "shell" shell "command" command "cwd" cwd "executeId" execute-id)) (stop nil))
    (when (plusp (length shell-path)) (setf (gethash "shellPath" payload) shell-path))
    (when (bash-exec-options-timeout options) (setf (gethash "timeout" payload) (bash-exec-options-timeout options)))
    (when (bash-exec-options-env options) (setf (gethash "env" payload) (bash-exec-options-env options)))
    (when (and signal (abort-signal-set-p signal)) (setf (gethash "aborted" payload) t))
    (sb-thread:with-mutex (*bt-lock*)
      (setf (gethash execute-id *bt-exec-data*)
            (lambda (data)
              (when failure (error failure))
              (when on-data
                (handler-case (funcall on-data data)
                  (error (e) (setf failure e) (error e)))))))
    (unwind-protect
         (progn
           (when signal (setf stop (bt-watch-signal signal (lambda () (bt-cancel ext execute-id)))))
           (let ((reply (handler-case (host-call ext "builtinTool.localExec" payload)
                          (error (e) (error (or failure e))))))
             (when failure (error failure))
             (obj "exitCode" (and (hash-table-p reply) (jget reply "exitCode")))))
      (when stop (funcall stop))
      (sb-thread:with-mutex (*bt-lock*) (remhash execute-id *bt-exec-data*)))))

(defun create-local-bash-operations (&key shell-path)
  "Pi's createLocalBashOperations(options): the bash-operations that run a command in the local shell, which an extension wraps to rewrite commands while it keeps Pi's
local shell behavior. The command runs on the host; (funcall (bash-operations-exec ops) command cwd options) streams its output to the options' on-data in order and
returns {\"exitCode\": ...}, or signals what Pi's exec rejects with (\"aborted\" when the signal was set, \"timeout:<seconds>\" when the timeout elapsed)."
  (make-bash-operations :exec (lambda (command cwd options) (bt-local-exec "bash" (or shell-path "") command cwd options))))

(defun create-local-powershell-operations ()
  "Pi's createLocalPowerShellOperations(): CREATE-LOCAL-BASH-OPERATIONS for PowerShell."
  (make-bash-operations :exec (lambda (command cwd options) (bt-local-exec "powershell" "" command cwd options))))
