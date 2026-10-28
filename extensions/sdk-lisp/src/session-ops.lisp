;;;; SPDX-License-Identifier: MIT
;;;; Session operations: newSession, fork, navigateTree, switchSession with their withSession and setup callbacks.

(in-package #:pig)

(defstruct (sess-registry (:constructor sess-make-registry ()))
  "The callbacks of replacement calls in flight, by the handle each call names."
  (lock (sb-thread:make-mutex :name "pig session callbacks"))
  (next 0)
  (with-session (make-hash-table :test 'equal))
  (setup (make-hash-table :test 'equal)))

(defstruct (sess-entry (:constructor sess-make-entry (callback))) callback (error nil))

(defun sess-registry-of (ext) (extension-extra ext :session-ops #'sess-make-registry))

(defun sess-str (value) (if (stringp value) value ""))
(defun sess-int (value default) (if (integerp value) value default))

;;; ---------------------------------------------------------------- the setup session manager

(defstruct (setup-manager (:include session-manager) (:constructor make-setup-manager (ctx)))
  "The session manager of the setup callback of ctx-new-session: the reads of ctx-session-manager and the appends that seed the replacement Session.")

(defun sess-write (manager method &rest members)
  (ctx-call-host (session-manager-ctx manager) "sessionWrite" (obj "method" method "args" (apply #'obj members))))

(defun setup-append-message (manager message) (sess-write manager "appendMessage" "message" message))
(defun setup-append-custom-entry (manager custom-type &optional data)
  (sess-write manager "appendCustomEntry" "customType" custom-type "data" data))
(defun setup-append-custom-message-entry (manager custom-type content display &optional details)
  (sess-write manager "appendCustomMessageEntry" "customType" custom-type "content" content
                                                  "display" (if display t :false) "details" details))
(defun setup-append-session-info (manager name) (sess-write manager "appendSessionInfo" "name" name))
(defun setup-append-model-change (manager provider model-id)
  (sess-write manager "appendModelChange" "provider" provider "modelId" model-id))
(defun setup-append-thinking-level-change (manager level)
  (sess-write manager "appendThinkingLevelChange" "thinkingLevel" level))
(defun setup-append-label-change (manager target-id label)
  (sess-write manager "appendLabelChange" "targetId" target-id "label" label))

;;; ---------------------------------------------------------------- replacement calls

(defun sess-add-callback (ext kind callback)
  "(values handle entry): name CALLBACK for the host, which runs it with a request before the replacement call returns."
  (let ((registry (sess-registry-of ext)) (entry (sess-make-entry callback)))
    (sb-thread:with-mutex ((sess-registry-lock registry))
      (let ((handle (format nil "~A~:[~;:setup~]:~D" (extension-name ext) (eq kind :setup) (incf (sess-registry-next registry)))))
        (setf (gethash handle (if (eq kind :setup) (sess-registry-setup registry) (sess-registry-with-session registry))) entry)
        (values handle entry)))))

(defun sess-remove-callback (ext kind handle)
  (let ((registry (sess-registry-of ext)))
    (sb-thread:with-mutex ((sess-registry-lock registry))
      (remhash handle (if (eq kind :setup) (sess-registry-setup registry) (sess-registry-with-session registry))))))

(defun sess-call-with-callbacks (ctx method args callbacks)
  "Call METHOD with ARGS; CALLBACKS is a list of (kind member function) naming each function for the host. A callback's error fails the
call with that error, as Pi's awaited callback rejects the call."
  (let ((ext (ctx-extension ctx)) (entries '()))
    (unwind-protect
         (progn
           (loop for (kind member function) in callbacks
                 do (when function
                      (unless (functionp function) (error "~A must be a function" member))
                      (multiple-value-bind (handle entry) (sess-add-callback ext kind function)
                        (push (list kind handle entry) entries)
                        (setf (gethash member args) handle))))
           (handler-case (ctx-call-host ctx method args)
             (host-call-error (e)
               (let ((failed (find-if #'sess-entry-error entries :key #'third)))
                 (if failed (error (sess-entry-error (third failed))) (error e))))))
      (loop for (kind handle) in entries do (sess-remove-callback ext kind handle)))))

(defun ctx-new-session (ctx &key parent-session setup with-session)
  "Start a new session. SETUP, (lambda (manager)), seeds the new session through a setup-manager before it starts; WITH-SESSION,
(lambda (replaced-ctx)), runs with a context of the replacement Session. The result is {cancelled}."
  (let ((args (obj)))
    (when parent-session (setf (gethash "parentSession" args) parent-session))
    (sess-call-with-callbacks ctx "newSession" args `((:setup "setup" ,setup) (:with-session "withSession" ,with-session)))))

(defun ctx-fork (ctx entry-id &key position with-session)
  "Fork at an entry; POSITION is \"before\" (the default) or \"at\". The result is {cancelled}."
  (let ((args (obj "entryId" entry-id)))
    (when position (setf (gethash "position" args) position))
    (sess-call-with-callbacks ctx "fork" args `((:with-session "withSession" ,with-session)))))

(defun ctx-navigate-tree (ctx target-id &key summarize custom-instructions replace-instructions label)
  "Move to another point of the session tree. The result is {cancelled}."
  (let ((args (obj "targetId" target-id)))
    (when summarize (setf (gethash "summarize" args) t))
    (when custom-instructions (setf (gethash "customInstructions" args) custom-instructions))
    (when replace-instructions (setf (gethash "replaceInstructions" args) t))
    (when label (setf (gethash "label" args) label))
    (ctx-call-host ctx "navigateTree" args)))

(defun ctx-switch-session (ctx session-path &key with-session)
  "Switch to a session file. The result is {cancelled}."
  (sess-call-with-callbacks ctx "switchSession" (obj "sessionPath" session-path) `((:with-session "withSession" ,with-session))))

(defun sess-run-callback (ext kind args run)
  (let* ((handle (sess-str (jget args "handle")))
         (registry (sess-registry-of ext))
         (entry (sb-thread:with-mutex ((sess-registry-lock registry))
                  (gethash handle (if (eq kind :setup) (sess-registry-setup registry) (sess-registry-with-session registry))))))
    (unless entry (error "unknown ~A callback ~A" (if (eq kind :setup) "setup" "withSession") handle))
    (handler-bind ((error (lambda (e) (setf (sess-entry-error entry) e))))
      (funcall run (sess-entry-callback entry)))
    nil))

(def-request "setup" (ext ctx req)
  (sess-run-callback ext :setup (jget req "args") (lambda (callback) (funcall callback (make-setup-manager ctx)))))

(def-request "with_session" (ext ctx req)
  (let* ((args (jget req "args")) (ready (or (jget args "ready") (obj))) (replaced (copy-ctx ctx)))
    (setf (ctx-replacement replaced)
          (list :cwd (sess-str (jget ready "cwd")) :mode (sess-str (jget ready "mode"))
                :has-ui (eq (jget (jget ready "state") "hasUI") t)))
    (sess-run-callback ext :with-session args (lambda (callback) (funcall callback replaced)))))
