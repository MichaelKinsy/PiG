;;;; SPDX-License-Identifier: MIT
;;;; The session graph: the objects an extension constructs around a session (SessionManager, SettingsManager, DefaultResourceLoader,
;;;; DefaultPackageManager, ProjectTrustStore, ModelRuntime ...). The host owns each object for the connection and names it by a handle.
;;;; This file holds the base every graph class uses (GRAPH-*) and Pi's SessionManager.

(in-package #:pig)

;;; ---------------------------------------------------------------- base

(eval-when (:compile-toplevel :load-toplevel :execute)
  (defun graph-camel (name)
    "\"get-default-model\" -> \"getDefaultModel\": Pi's method name of a kebab-case Lisp name."
    (with-output-to-string (out)
      (let ((up nil))
        (loop for c across (string-downcase name)
              do (cond ((char= c #\-) (setf up t))
                       (up (write-char (char-upcase c) out) (setf up nil))
                       (t (write-char c out))))))))

(defstruct (graph-object (:conc-name gobj-)) ctx handle)

(defstruct (graph-callbacks (:constructor make-graph-callbacks ()))
  (lock (sb-thread:make-mutex :name "pig graph"))
  (table (make-hash-table :test 'equal))
  (counter 0))

(defun graph-bool (value)
  "A wire boolean as a Lisp boolean."
  (and value (not (eq value :false)) (not (eq value :null))))

(defun graph-bool-arg (value)
  "A Lisp boolean (or an explicit :false) as the wire boolean."
  (if (or (eq value :false) (not value)) :false t))

(defun graph-args (args) (coerce args 'simple-vector))

(defun graph-callback-id (ctx fn)
  "The name the host calls FN by (\"cbN\"), or NIL for no function. A function stays until the connection ends: the host may run it again on a later reload."
  (when fn
    (let ((state (extension-extra (ctx-extension ctx) :graph #'make-graph-callbacks)))
      (sb-thread:with-mutex ((graph-callbacks-lock state))
        (let ((id (format nil "cb~D" (incf (graph-callbacks-counter state)))))
          (setf (gethash id (graph-callbacks-table state)) fn)
          id)))))

(def-request "sessionGraph.callback" (ext ctx req)
  (let* ((args (jget req "args"))
         (state (extension-extra ext :graph #'make-graph-callbacks))
         (id (jget args "id"))
         (fn (sb-thread:with-mutex ((graph-callbacks-lock state)) (gethash id (graph-callbacks-table state))))
         (values (jget args "args")))
    (unless fn (error "unknown session graph callback ~A" id))
    (apply fn (if (and (vectorp values) (not (stringp values))) (map 'list (lambda (v) (if (eq v :null) nil v)) values) '()))))

(defun graph-new (ctx kind op &rest args)
  "Construct a host object (Pi's constructor or static factory OP of class KIND) and return its handle."
  (let* ((reply (ctx-call-host ctx "sessionGraph.new" (obj "kind" kind "op" op "args" (graph-args args))))
         (handle (and (hash-table-p reply) (jget reply "handle"))))
    (unless (and (stringp handle) (plusp (length handle))) (error "host returned no session graph handle"))
    handle))

(defun graph-invoke (ctx handle method &rest args)
  (ctx-call-host ctx "sessionGraph.call" (obj "handle" handle "method" method "args" (graph-args args))))

(defun graph-invoke-async (ctx handle method &rest args)
  "A method that returns a Promise in Pi: the call returns when the Promise settles."
  (ctx-call-host ctx "sessionGraph.async" (obj "handle" handle "method" method "args" (graph-args args))))

(defun graph-static (ctx kind method &rest args)
  (ctx-call-host ctx "sessionGraph.call" (obj "kind" kind "method" method "args" (graph-args args))))

(defun graph-static-async (ctx kind method &rest args)
  (ctx-call-host ctx "sessionGraph.async" (obj "kind" kind "method" method "args" (graph-args args))))

(defun graph-release (ctx handle)
  "Drop the host's reference to an object (the host also drops them when the connection closes)."
  (ctx-call-host ctx "sessionGraph.release" (obj "handle" handle))
  nil)

(defun graph-call (object method &rest args)
  (apply #'graph-invoke (gobj-ctx object) (gobj-handle object) method args))

(defun graph-call-async (object method &rest args)
  (apply #'graph-invoke-async (gobj-ctx object) (gobj-handle object) method args))

(defmacro def-graph-methods (prefix &body specs)
  "Define PREFIX-NAME for each (NAME LAMBDA-LIST . OPTIONS) of SPECS: a call of the host method NAME (camel case) on the object, with the
parameters of LAMBDA-LIST in order. Options: :bool (the result is a Lisp boolean), :async (the method returns a Promise in Pi), :bool-args (parameters sent as wire booleans)."
  `(progn
     ,@(loop for (name lambda-list . options) in specs
             for params = (remove-if (lambda (p) (member p lambda-list-keywords))
                                     (mapcar (lambda (p) (if (consp p) (car p) p)) lambda-list))
             for call = `(,(if (getf options :async) 'graph-call-async 'graph-call) object ,(graph-camel (string name))
                          ,@(mapcar (lambda (p) (if (member p (getf options :bool-args)) `(graph-bool-arg ,p) p)) params))
             collect `(defun ,(intern (string-upcase (format nil "~A-~A" prefix name)) :pig) (object ,@lambda-list)
                        ,(format nil "Pi's ~A." (graph-camel (string name)))
                        ,(if (getf options :bool) `(graph-bool ,call) call)))))

;;; ---------------------------------------------------------------- SessionManager

(defstruct (sm (:include graph-object) (:constructor %make-sm (ctx handle)))
  "Pi's SessionManager: an append-only session log the host owns. A manager passed to a child session is the very log it appends to.")

(defun sm-new (ctx op &rest args) (%make-sm ctx (apply #'graph-new ctx "SessionManager" op args)))

(defun sm-create (ctx cwd &optional session-dir options)
  "SessionManager.create(cwd, sessionDir?, options?): a new persisted session. OPTIONS is Pi's NewSessionOptions (id, parentSession)."
  (sm-new ctx "create" cwd session-dir options))

(defun sm-open (ctx path &optional session-dir cwd-override)
  "SessionManager.open(path, sessionDir?, cwdOverride?)."
  (sm-new ctx "open" path session-dir cwd-override))

(defun sm-continue-recent (ctx cwd &optional session-dir)
  "SessionManager.continueRecent(cwd, sessionDir?): the most recent session of the directory, or a new one."
  (sm-new ctx "continueRecent" cwd session-dir))

(defun sm-in-memory (ctx &optional cwd options entries)
  "SessionManager.inMemory(cwd = the process directory, options?, entries?): a session that is never written."
  (sm-new ctx "inMemory" (or cwd (namestring (sb-posix:getcwd))) options entries))

(defun sm-fork-from (ctx source-path target-cwd &optional session-dir options)
  "SessionManager.forkFrom(sourcePath, targetCwd, sessionDir?, options?): a new session holding the whole history of the source."
  (sm-new ctx "forkFrom" source-path target-cwd session-dir options))

(defun sm-find-by-id (ctx cwd id &optional session-dir)
  "The path of the session file with that exact id, or NIL."
  (graph-static ctx "SessionManager" "findById" cwd id session-dir))

(defun sm-iso-ms (text)
  "Date.toISOString text (2026-01-01T00:00:00.000Z) as milliseconds since the Unix epoch."
  (flet ((n (start end) (parse-integer text :start start :end end)))
    (+ (* 1000 (- (encode-universal-time (n 17 19) (n 14 16) (n 11 13) (n 8 10) (n 5 7) (n 0 4) 0) 2208988800))
       (if (> (length text) 20) (n 20 23) 0))))

(defun sm-info (raw)
  "Pi's SessionInfo with created and modified as milliseconds since the Unix epoch."
  (let ((info (obj)))
    (maphash (lambda (key value) (setf (gethash key info) value)) raw)
    (setf (gethash "created" info) (sm-iso-ms (jget raw "created"))
          (gethash "modified" info) (sm-iso-ms (jget raw "modified")))
    info))

(defun sm-progress-id (ctx on-progress)
  (graph-callback-id ctx (and on-progress
                              (lambda (loaded total partial)
                                (funcall on-progress loaded total (and partial (map 'vector #'sm-info partial)))))))

(defun sm-infos (listing) (map 'vector #'sm-info (if (vectorp listing) listing #())))

(defun sm-list (ctx cwd &optional session-dir on-progress)
  "The sessions of a directory, most recently modified first, as Pi's SessionInfo objects (created and modified in epoch milliseconds).
ON-PROGRESS is called with (loaded total partial) as session files are read; PARTIAL is the sessions read so far, or NIL."
  (sm-infos (graph-static-async ctx "SessionManager" "list" cwd session-dir (sm-progress-id ctx on-progress))))

(defun sm-list-all (ctx &optional session-dir on-progress)
  "The sessions of every project (or of SESSION-DIR), most recently modified first."
  (sm-infos (graph-static-async ctx "SessionManager" "listAll" session-dir (sm-progress-id ctx on-progress))))

(def-graph-methods "sm"
  (get-cwd ()) (get-session-dir ()) (get-session-id ()) (get-session-file ()) (get-session-name ())
  (get-leaf-id ()) (get-header ()) (get-leaf-entry ()) (get-entry (id)) (get-label (id)) (get-entry-count ())
  (get-entries ()) (get-branch (&optional from-id)) (get-children (parent-id)) (get-tree ())
  (build-context-entries ()) (build-session-projection ()) (build-session-context ())
  (is-persisted () :bool t) (uses-default-session-dir () :bool t)
  (append-message (message)) (append-thinking-level-change (thinking-level)) (append-model-change (provider model-id))
  (append-usage (kind provider model usage &optional note))
  (append-compaction (summary first-kept-entry-id tokens-before &optional details from-hook usage))
  (append-custom-entry (custom-type &optional data)) (append-session-info (name))
  (append-custom-message-entry (custom-type content display &optional details) :bool-args (display))
  (append-context-edit (target-id replacement)) (append-label-change (target-id label))
  (branch (branch-from-id)) (reset-leaf ())
  (branch-with-summary (branch-from-id summary &optional details from-hook usage))
  (create-branched-session (leaf-id)) (new-session (&optional options)) (set-session-file (session-file)))
