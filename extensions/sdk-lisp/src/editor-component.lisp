;;;; SPDX-License-Identifier: MIT
;;;; ctx-set-editor-component: Pi's CustomEditor over the host's default editor.
;;;; The host's keys reach the component's handlers, its rows are the editor on screen, and EDITOR-BASE (its "super") calls the host's editor.
;;;; Each installed editor runs its component on one worker thread, in the order the host produced the events; after each event the
;;;; component is rendered and the frame sent when it changed.

(in-package #:pig)

(defstruct (editor-status (:constructor make-editor-status (kind message frames frame spinner-color message-color indicator-verbatim)))
  "The working, compaction, summarization or retry status the host shows while a component embeds it."
  kind message frames frame spinner-color message-color indicator-verbatim)

(defstruct (editor-base (:constructor make-editor-base (extension key)))
  "The host's default editor, the super of an EDITOR-COMPONENT. Its handler slots are CustomEditor's public fields:
ON-ESCAPE, ON-CTRL-D, ON-PASTE-IMAGE (functions of no arguments), ON-EXTENSION-SHORTCUT (a function of the key that returns true when it handled it)
and ACTION-HANDLERS (a live table of functions by app action id such as \"app.clear\")."
  extension key on-escape on-ctrl-d on-paste-image on-extension-shortcut
  (action-handlers (make-hash-table :test 'equal)))

(defstruct (editor-component
            (:constructor make-editor-component
                (base &key embed-working-status
                        ((:handle-input handle-input-fn)) ((:handle-mouse handle-mouse-fn)) ((:render render-fn))
                        ((:set-text set-text-fn)) ((:insert-text-at-cursor insert-text-fn)) ((:add-to-history add-history-fn))
                        ((:set-working-status-indicator status-fn)) ((:invalidate invalidate-fn)))))
  "The editor an extension installs. BASE is the EDITOR-BASE the factory received. Each keyword overrides one method with a function of that
method's arguments; a method left out is the host's default editor's. EMBED-WORKING-STATUS makes the host draw the working status in the rows RENDER returns."
  base embed-working-status handle-input-fn handle-mouse-fn render-fn set-text-fn insert-text-fn add-history-fn status-fn invalidate-fn)

;;; ---------------------------------------------------------------- the base: calls on the host's editor

(defun edcomp-op (base op &optional args)
  (let ((reply (host-call (editor-base-extension base) "ui.editor.base"
                          (obj "key" (editor-base-key base) "op" op "args" (or args (obj))))))
    (if (hash-table-p reply) reply (obj))))

(defun edcomp-string (value &optional (default "")) (if (stringp value) value default))
(defun edcomp-int (value) (if (integerp value) value 0))
(defun edcomp-lines (value) (if (vectorp value) (map 'list (lambda (line) (edcomp-string line (princ-to-string line))) value) '()))

(defun editor-base-on-action (base action handler)
  "Register HANDLER for the app action ACTION (CustomEditor's onAction)."
  (setf (gethash action (editor-base-action-handlers base)) handler))

(defun edcomp-hooked (base)
  "The app actions whose handler the component set, for the host to answer instead of running, in the order Pi's handleInput consults them."
  (let ((handlers (editor-base-action-handlers base)) (ids '()) (others '()))
    (when (or (editor-base-on-escape base) (gethash "app.interrupt" handlers)) (push "app.interrupt" ids))
    (when (or (editor-base-on-ctrl-d base) (gethash "app.exit" handlers)) (push "app.exit" ids))
    (when (editor-base-on-paste-image base) (push "app.clipboard.pasteImage" ids))
    (maphash (lambda (action handler)
               (when (and handler (not (member action '("app.interrupt" "app.exit" "app.clipboard.pasteImage") :test #'string=)))
                 (push action others)))
             handlers)
    (append (nreverse ids) (sort others #'string<) (and (editor-base-on-extension-shortcut base) '("extensionShortcut")))))

(defun edcomp-hook-handler (base action)
  (cond ((and (string= action "app.interrupt") (editor-base-on-escape base)))
        ((and (string= action "app.exit") (editor-base-on-ctrl-d base)))
        ((string= action "app.clipboard.pasteImage") (editor-base-on-paste-image base))
        (t (gethash action (editor-base-action-handlers base)))))

(defun editor-base-handle-input (base data)
  "The default editor's handleInput, CustomEditor's: the component's ON-EXTENSION-SHORTCUT first, then the host's extension shortcuts and app actions,
each through the handler the component set for it, then the editing."
  (let ((shortcut (editor-base-on-extension-shortcut base)))
    (unless (and shortcut (funcall shortcut data))
      (let* ((hooks (edcomp-hooked base))
             (args (obj "data" data)))
        (when hooks (setf (gethash "hooks" args) (coerce hooks 'simple-vector)))
        (let* ((hook (jget (edcomp-op base "handleInput" args) "hook"))
               (handler (and (stringp hook) (plusp (length hook)) (edcomp-hook-handler base hook))))
          (when handler (funcall handler)))))
    nil))

(defun editor-base-handle-mouse (base event) (edcomp-op base "handleMouse" (obj "event" event)) nil)
(defun editor-base-render (base width) (edcomp-lines (jget (edcomp-op base "render" (obj "width" width)) "lines")))
(defun editor-base-set-text (base text) (edcomp-op base "setText" (obj "text" text)) nil)
(defun editor-base-insert-text-at-cursor (base text) (edcomp-op base "insertTextAtCursor" (obj "text" text)) nil)
(defun editor-base-add-to-history (base text) (edcomp-op base "addToHistory" (obj "text" text)) nil)
(defun editor-base-get-text (base) (edcomp-string (jget (edcomp-op base "getText") "text")))
(defun editor-base-get-expanded-text (base) (edcomp-string (jget (edcomp-op base "getExpandedText") "text")))
(defun editor-base-get-lines (base) (edcomp-lines (jget (edcomp-op base "getLines") "lines")))
(defun editor-base-get-cursor (base)
  "(values line col): a logical line and a UTF-16 column."
  (let ((cursor (edcomp-op base "getCursor"))) (values (edcomp-int (jget cursor "line")) (edcomp-int (jget cursor "col")))))
(defun editor-base-is-showing-autocomplete-p (base) (eq (jget (edcomp-op base "isShowingAutocomplete") "value") t))
(defun editor-base-set-padding-x (base padding) (edcomp-op base "setPaddingX" (obj "n" padding)) nil)
(defun editor-base-set-autocomplete-max-visible (base n) (edcomp-op base "setAutocompleteMaxVisible" (obj "n" n)) nil)
(defun editor-base-get-padding-x (base) (edcomp-int (jget (edcomp-op base "getPaddingX") "n")))
(defun editor-base-get-autocomplete-max-visible (base) (edcomp-int (jget (edcomp-op base "getAutocompleteMaxVisible") "n")))
(defun editor-base-border-color (base text)
  "The default editor's borderColor called on TEXT: its border color for the thinking level or bash mode."
  (edcomp-string (jget (edcomp-op base "borderColor" (obj "text" text)) "text")))
(defun editor-base-disable-submit (base) (eq (jget (edcomp-op base "getDisableSubmit") "value") t))
(defun (setf editor-base-disable-submit) (disabled base)
  (edcomp-op base "setDisableSubmit" (obj "value" (if disabled t :false))) disabled)
(defun editor-base-focused (base) (eq (jget (edcomp-op base "getFocused") "value") t))
(defun (setf editor-base-focused) (focused base)
  (edcomp-op base "setFocused" (obj "value" (if focused t :false))) focused)
(defun editor-base-set-autocomplete-provider (base provider)
  "Give the editor a CombinedAutocompleteProvider: PROVIDER is its wire object (commands and basePath), or NIL for none."
  (edcomp-op base "setAutocompleteProvider" (obj "provider" (or provider :null))) nil)
(defun editor-base-on-submit (base text) (edcomp-op base "onSubmit" (obj "text" text)) nil)
(defun editor-base-on-change (base text) (edcomp-op base "onChange" (obj "text" text)) nil)
(defun editor-base-set-working-status-indicator (base status) (declare (ignore base status)) nil)
(defun editor-base-invalidate (base) (declare (ignore base)) nil)

;;; ---------------------------------------------------------------- the component's methods: its override, else the base's

(defmacro edcomp-def-method (name (&rest args) fn base-function)
  `(defun ,name (component ,@args)
     (let ((fn (,fn component)))
       (if fn (funcall fn ,@args) (,base-function (editor-component-base component) ,@args)))))

(edcomp-def-method editor-component-handle-input (data) editor-component-handle-input-fn editor-base-handle-input)
(edcomp-def-method editor-component-handle-mouse (event) editor-component-handle-mouse-fn editor-base-handle-mouse)
(edcomp-def-method editor-component-render (width) editor-component-render-fn editor-base-render)
(edcomp-def-method editor-component-set-text (text) editor-component-set-text-fn editor-base-set-text)
(edcomp-def-method editor-component-insert-text-at-cursor (text) editor-component-insert-text-fn editor-base-insert-text-at-cursor)
(edcomp-def-method editor-component-add-to-history (text) editor-component-add-history-fn editor-base-add-to-history)
(edcomp-def-method editor-component-set-working-status-indicator (status) editor-component-status-fn
                   editor-base-set-working-status-indicator)
(edcomp-def-method editor-component-invalidate () editor-component-invalidate-fn editor-base-invalidate)

;;; ---------------------------------------------------------------- one installed editor and its worker thread

(defstruct (edcomp-state (:constructor make-edcomp-state ()))
  "Per extension: the installed session, the install counter, whether the width subscription exists."
  (lock (sb-thread:make-mutex :name "pig editor")) (session nil) (seq 0) (width-subscribed nil))

(defstruct (edcomp-session (:constructor make-edcomp-session (ext key factory)))
  ext key factory component
  (lock (sb-thread:make-mutex :name "pig editor events")) (wait (sb-thread:make-waitqueue)) (events '())
  (last-frame nil) (seq 0) (status-sent nil) (last-status nil))

(defun edcomp-state-of (ext) (extension-extra ext :editor-component #'make-edcomp-state))

(defun edcomp-post (session event)
  (sb-thread:with-mutex ((edcomp-session-lock session))
    (setf (edcomp-session-events session) (append (edcomp-session-events session) (list event)))
    (sb-thread:condition-notify (edcomp-session-wait session))))

(defun edcomp-next-event (session)
  (sb-thread:with-mutex ((edcomp-session-lock session))
    (loop while (null (edcomp-session-events session))
          do (sb-thread:condition-wait (edcomp-session-wait session) (edcomp-session-lock session)))
    (pop (edcomp-session-events session))))

(defun edcomp-guard (session what thunk)
  "(values result ok): an error of the extension's code is reported to the user and never ends the worker."
  (handler-case (values (funcall thunk) t)
    (error (e)
      (ignore-errors (notify-host (edcomp-session-ext session) "ui.notify"
                                  (obj "message" (format nil "editor ~A failed: ~A" what (condition-message e)) "level" "error")))
      (values nil nil))))

(defun edcomp-status-key (status)
  (and status (list (editor-status-kind status) (editor-status-message status) (editor-status-frames status) (editor-status-frame status)
                    (editor-status-spinner-color status) (editor-status-message-color status) (editor-status-indicator-verbatim status))))

(defun edcomp-configure (session raw)
  "Apply the host's configuration: a changed working status goes to the component's set-working-status-indicator."
  (let* ((status (and (hash-table-p raw)
                      (make-editor-status (edcomp-string (jget raw "kind")) (edcomp-string (jget raw "message"))
                                          (edcomp-lines (jget raw "frames")) (edcomp-int (jget raw "frame"))
                                          (edcomp-string (jget raw "spinnerColor")) (edcomp-string (jget raw "messageColor"))
                                          (eq (jget raw "indicatorVerbatim") t))))
         (key (edcomp-status-key status)))
    (unless (if (edcomp-session-status-sent session) (equal key (edcomp-session-last-status session)) (null status))
      (setf (edcomp-session-status-sent session) t (edcomp-session-last-status session) key)
      (edcomp-guard session "setWorkingStatusIndicator"
                    (lambda () (editor-component-set-working-status-indicator (edcomp-session-component session) status))))))

(defun edcomp-render-now (session)
  (let* ((ext (edcomp-session-ext session))
         (width (let ((w (sb-thread:with-mutex ((extension-state-lock ext)) (extension-width ext)))) (if (plusp w) w 80))))
    (multiple-value-bind (lines ok)
        (edcomp-guard session "render"
                      (lambda () (mapcar (lambda (line) (edcomp-string line (princ-to-string line)))
                                         (coerce (editor-component-render (edcomp-session-component session) width) 'list))))
      (when (and ok (not (equal (edcomp-session-last-frame session) (cons width lines))))
        (setf (edcomp-session-last-frame session) (cons width lines))
        (notify-host ext "ui.editor.render" (obj "key" (edcomp-session-key session) "lines" (coerce lines 'simple-vector)
                                                 "width" width "seq" (incf (edcomp-session-seq session))))))))

(defun edcomp-handle-event (session event)
  (let ((component (edcomp-session-component session)) (payload (cdr event)))
    (flet ((guard (what thunk) (edcomp-guard session what thunk)))
      (ecase (car event)
        (:input (guard "handleInput" (lambda () (editor-component-handle-input component payload)))
         (edcomp-render-now session)
         (notify-host (edcomp-session-ext session) "ui.editor.inputDone" (obj "key" (edcomp-session-key session)))
         (return-from edcomp-handle-event))
        (:mouse (guard "handleMouse" (lambda () (editor-component-handle-mouse component payload))))
        (:set-text (guard "setText" (lambda () (editor-component-set-text component payload))))
        (:insert-text (guard "insertTextAtCursor" (lambda () (editor-component-insert-text-at-cursor component payload))))
        (:add-to-history (guard "addToHistory" (lambda () (editor-component-add-to-history component payload))))
        (:configure (edcomp-configure session payload))
        (:invalidate (guard "invalidate" (lambda () (editor-component-invalidate component))))
        (:render nil))
      (edcomp-render-now session))))

(defun edcomp-run (session)
  (let ((ext (edcomp-session-ext session)) (self sb-thread:*current-thread*))
    (sb-thread:with-mutex ((extension-state-lock ext)) (push self (extension-request-threads ext)))
    (unwind-protect
         (loop (let ((event (edcomp-next-event session)))
                 (when (eq event :stop) (return))
                 (handler-case (edcomp-handle-event session event)
                   (error (e) (format *error-output* "~A: editor ~A: ~A~%" (extension-name ext) (edcomp-session-key session) e)))))
      (sb-thread:with-mutex ((extension-state-lock ext))
        (setf (extension-request-threads ext) (remove self (extension-request-threads ext)))))))

;;; ---------------------------------------------------------------- install, clear, and the host's notifies

(defun edcomp-close (session) (edcomp-post session :stop))

(defun edcomp-clear (ext)
  (let* ((state (edcomp-state-of ext))
         (previous (sb-thread:with-mutex ((edcomp-state-lock state)) (shiftf (edcomp-state-session state) nil))))
    (when previous (edcomp-close previous))))

(defun edcomp-subscribe-width (ext state)
  "Render the installed editor again when the terminal width changes, once per extension."
  (unless (sb-thread:with-mutex ((edcomp-state-lock state)) (shiftf (edcomp-state-width-subscribed state) t))
    (ctx-on-width-change (make-ctx ext)
                         (lambda (width) (declare (ignore width))
                           (let ((session (sb-thread:with-mutex ((edcomp-state-lock state)) (edcomp-state-session state))))
                             (when session (edcomp-post session '(:render))))))))

(defun edcomp-install (ext factory)
  "Install the component FACTORY builds. The host learns of the editor before the factory runs, so the factory may already call the EDITOR-BASE it
receives: such a call waits until the host's editor is ready."
  (let* ((state (edcomp-state-of ext))
         (key (sb-thread:with-mutex ((edcomp-state-lock state)) (format nil "editor-~D" (incf (edcomp-state-seq state)))))
         (session (make-edcomp-session ext key factory))
         (previous (sb-thread:with-mutex ((edcomp-state-lock state)) (shiftf (edcomp-state-session state) session)))
         (component nil) (built nil))
    (when previous (edcomp-close previous))
    (edcomp-subscribe-width ext state)
    (notify-host ext "ui.editor.install" (obj "key" key "delegated" t))
    (unwind-protect
         (progn (setf component (funcall factory (make-editor-base ext key)))
                (unless (typep component 'editor-component) (error "an editor factory must return an editor component, not ~S" component))
                (setf built t))
      (unless built
        (sb-thread:with-mutex ((edcomp-state-lock state))
          (when (eq (edcomp-state-session state) session) (setf (edcomp-state-session state) nil)))
        (notify-host ext "ui.editor.clear" (obj "key" key))))
    (setf (edcomp-session-component session) component)
    (sb-thread:make-thread #'edcomp-run :arguments (list session) :name (format nil "pig-~A" key))
    (when (eq (editor-component-embed-working-status component) t)
      (notify-host ext "ui.editor.options" (obj "key" key "embedWorkingStatus" t)))))

(defun edcomp-route (ext args kind field)
  "Post the host's event to the installed editor when it is the one the host names."
  (let* ((state (edcomp-state-of ext))
         (session (sb-thread:with-mutex ((edcomp-state-lock state)) (edcomp-state-session state))))
    (when (and session (equal (edcomp-session-key session) (jget args "key" "")))
      (let ((payload (and field (jget args field))))
        (edcomp-post session (cons kind (if (member field '("data" "text") :test #'equal) (edcomp-string payload) payload)))))))

(def-notify "ui.editor.input" (ext args) (edcomp-route ext args :input "data"))
(def-notify "ui.editor.mouse" (ext args) (edcomp-route ext args :mouse "event"))
(def-notify "ui.editor.setText" (ext args) (edcomp-route ext args :set-text "text"))
(def-notify "ui.editor.insertText" (ext args) (edcomp-route ext args :insert-text "text"))
(def-notify "ui.editor.addToHistory" (ext args) (edcomp-route ext args :add-to-history "text"))
(def-notify "ui.editor.configure" (ext args) (edcomp-route ext args :configure "workingStatus"))
(def-notify "ui.editor.invalidate" (ext args) (edcomp-route ext args :invalidate nil))
(def-notify "ui.editor.closed" (ext args)
  (let* ((state (edcomp-state-of ext))
         (session (sb-thread:with-mutex ((edcomp-state-lock state))
                    (let ((session (edcomp-state-session state)))
                      (when (and session (equal (edcomp-session-key session) (jget args "key" "")))
                        (setf (edcomp-state-session state) nil)
                        session)))))
    (when session (edcomp-close session))))

(def-stop-hook "editor-component" (ext) (edcomp-clear ext))

;;; ---------------------------------------------------------------- the context

(defun ctx-clear-editor-component (ctx)
  "Restore the host's editor."
  (edcomp-clear (ctx-extension ctx))
  (ctx-call-host ctx "ui.setEditorComponent" (obj "clear" t))
  nil)

(defun ctx-set-editor-component (ctx factory)
  "Install an editor in place of the host's, as Pi's ctx.ui.setEditorComponent does. FACTORY is (lambda (base)) and returns an EDITOR-COMPONENT
(base is the host's default editor, the component's super); NIL restores the host's editor. With no UI, or in RPC mode, it is ignored, as in Pi."
  (when (and (ctx-has-ui-p ctx) (string/= (ctx-mode ctx) "rpc"))
    (cond ((null factory) (ctx-clear-editor-component ctx))
          ((functionp factory) (edcomp-install (ctx-extension ctx) factory))
          (t (error "an editor factory must be a function or NIL"))))
  nil)

(defun ctx-get-editor-component (ctx)
  "The factory this extension installed with ctx-set-editor-component while that editor is the host's, as Pi's ctx.ui.getEditorComponent returns the
configured factory; NIL before an install, once this or another extension restored the host's editor, and with no UI or in RPC mode."
  (let ((state (edcomp-state-of (ctx-extension ctx))))
    (sb-thread:with-mutex ((edcomp-state-lock state))
      (let ((session (edcomp-state-session state))) (and session (edcomp-session-factory session))))))
