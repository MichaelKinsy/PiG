;;;; SPDX-License-Identifier: MIT
;;;; Lane U2: editor components, autocomplete providers and terminal input against the in-process fake host (sdk-tests.lisp's host).
(in-package #:pig-tests)

;;; ---------------------------------------------------------------- a host that logs every frame and answers calls

(defstruct (edh (:constructor make-edh (host handler)))
  host handler (lock (sb-thread:make-mutex)) (frames '()) (thread nil))

(defun edh-frames (edh) (sb-thread:with-mutex ((edh-lock edh)) (reverse (edh-frames edh))))

(defun edh-reply (edh call-id result)
  (hsend (edh-host edh) "type" "call_result" "id" call-id "call_result" (pig:obj "result" result)))

(defun edh-loop (edh)
  (loop
    (let ((frame (handler-case (hread (edh-host edh)) (error () :eof))))
      (when (eq frame :eof) (return))
      (sb-thread:with-mutex ((edh-lock edh)) (push frame (edh-frames edh)))
      (when (equal (pig:jget frame "type") "call")
        (let* ((call (pig:jget frame "call"))
               (result (funcall (edh-handler edh) (pig:jget call "method") (pig:jget call "args") (pig:jget frame "id"))))
          (unless (eq result :defer) (edh-reply edh (pig:jget frame "id") result)))))))

(defun ed-start (ext handler &key (mode "tui") (has-ui t))
  "Serve EXT against a fresh socket whose ready payload carries MODE and HAS-UI; frames are logged and calls answered by HANDLER, (lambda (method args id)),
whose value is the call's result (or :DEFER to answer later with EDH-REPLY)."
  (let* ((path (format nil "/tmp/pig-lisp-ed-~D-~D.sock" (sb-posix:getpid) (random 1000000)))
         (listener (make-instance 'sb-bsd-sockets:local-socket :type :stream)))
    (ignore-errors (delete-file path))
    (sb-bsd-sockets:socket-bind listener path)
    (sb-bsd-sockets:socket-listen listener 4)
    (let* ((thread (sb-thread:make-thread (lambda () (pig::serve ext (pig::connect-unix path))) :name "test-extension"))
           (conn (pig::socket-conn (sb-bsd-sockets:socket-accept listener)))
           (host (make-host :listener listener :conn conn :thread thread :ext ext :path path)))
      (pig::read-frame conn)
      (pig::send-frame conn (pig:obj "type" "ready" "ready" (pig:obj "cwd" "/work" "mode" mode "width" 80
                                                                       "state" (pig:obj "hasUI" (if has-ui t :false)))))
      (let ((edh (make-edh host handler)))
        (setf (edh-thread edh) (sb-thread:make-thread #'edh-loop :arguments (list edh) :name "test-host-reader"))
        edh))))

(defmacro with-edh ((edh ext handler &rest options) &body body)
  `(let ((,edh (ed-start ,ext ,handler ,@options)))
     (unwind-protect (progn ,@body) (stop-host (edh-host ,edh)))))

(defun ed-find (edh predicate &optional (n 1))
  "The N-th logged frame that satisfies PREDICATE, or NIL."
  (let ((count 0))
    (dolist (frame (edh-frames edh))
      (when (and (funcall predicate frame) (= (incf count) n)) (return frame)))))

(defun ed-wait (edh predicate &optional (n 1))
  "The N-th frame that satisfies PREDICATE, waiting up to ten seconds for it."
  (loop repeat 2000
        do (let ((frame (ed-find edh predicate n))) (when frame (return-from ed-wait frame)))
           (sleep 0.005))
  (error "no frame matched: ~S" (mapcar (lambda (f) (pig:to-json f)) (edh-frames edh))))

(defun ed-notify (method) (lambda (frame) (and (equal (pig:jget frame "type") "notify") (equal (pig:jget (pig:jget frame "notify") "method") method))))
(defun ed-call (method) (lambda (frame) (and (equal (pig:jget frame "type") "call") (equal (pig:jget (pig:jget frame "call") "method") method))))
(defun ed-response (id) (lambda (frame) (and (equal (pig:jget frame "type") "response") (equal (pig:jget frame "id") id))))
(defun ed-args (frame)
  (pig:jget (or (pig:jget frame "notify") (pig:jget frame "call")) "args"))
(defun ed-result (frame) (pig:jget (pig:jget frame "response") "result"))
(defun ed-error (frame) (pig:jget (pig:jget (pig:jget frame "response") "error") "message"))
(defun ed-count (edh predicate) (count-if predicate (edh-frames edh)))

(defun ed-command (edh id name)
  (hsend (edh-host edh) "type" "request" "id" id "request" (pig:obj "method" "command" "tool" name "args" ""))
  (ed-wait edh (ed-response id)))

(defun ed-host-notify (edh method &rest members)
  (hsend (edh-host edh) "type" "notify" "notify" (pig:obj "method" method "args" (apply #'pig:obj members))))

(defun ed-ops (edh)
  "The ui.editor.base calls so far as (op . args), in order."
  (loop for frame in (edh-frames edh)
        when (funcall (ed-call "ui.editor.base") frame)
          collect (cons (pig:jget (ed-args frame) "op") (pig:jget (ed-args frame) "args"))))

(defun ed-ops-of (edh op) (remove op (ed-ops edh) :key #'car :test-not #'equal))

;;; ---------------------------------------------------------------- the stock editor behind ui.editor.base

(defstruct (edstock (:constructor make-edstock ())) (text "") (hook-keys '()))

(defun edstock-handler (stock)
  "A host handler answering ui.editor.base like a stock editor: it holds text, appends keys, renders base:TEXT, and answers a key listed in HOOK-KEYS
(an alist of key to action id) with that hook when the component asked for it."
  (lambda (method args id)
    (declare (ignore id))
    (if (not (equal method "ui.editor.base"))
        (pig:obj)
        (let ((op (pig:jget args "op")) (a (pig:jget args "args")))
          (cond
            ((equal op "handleInput")
             (let* ((data (pig:jget a "data"))
                    (hook (cdr (assoc data (edstock-hook-keys stock) :test #'equal)))
                    (hooks (let ((h (pig:jget a "hooks"))) (if (vectorp h) (coerce h 'list) '()))))
               (if (and hook (member hook hooks :test #'equal))
                   (pig:obj "hook" hook)
                   (progn (setf (edstock-text stock) (concatenate 'string (edstock-text stock) data)) (pig:obj)))))
            ((equal op "setText") (setf (edstock-text stock) (pig:jget a "text")) (pig:obj))
            ((equal op "insertTextAtCursor") (setf (edstock-text stock) (concatenate 'string (edstock-text stock) (pig:jget a "text"))) (pig:obj))
            ((member op '("getText" "getExpandedText") :test #'equal) (pig:obj "text" (edstock-text stock)))
            ((equal op "getLines") (pig:obj "lines" (vector (edstock-text stock) "second")))
            ((equal op "getCursor") (pig:obj "line" 2 "col" 5))
            ((equal op "render") (pig:obj "lines" (vector (concatenate 'string "base:" (edstock-text stock)))))
            ((equal op "isShowingAutocomplete") (pig:obj "value" t))
            ((equal op "getPaddingX") (pig:obj "n" 4))
            ((equal op "getAutocompleteMaxVisible") (pig:obj "n" 9))
            ((equal op "borderColor") (pig:obj "text" (concatenate 'string "[" (pig:jget a "text") "]")))
            ((member op '("getDisableSubmit" "getFocused") :test #'equal) (pig:obj "value" t))
            (t (pig:obj)))))))

(defun ed-render-lines (frame) (coerce (pig:jget (ed-args frame) "lines") 'list))

(defun ed-editor-extension (factory &key (name "ed") (probe nil))
  "An extension with the commands install (FACTORY), clear, and probe (notifies whether ctx-get-editor-component is FACTORY)."
  (let ((ext (pig:make-extension name)))
    (pig:command ext "install" "" (lambda (ctx args) (declare (ignore args)) (pig:ctx-set-editor-component ctx factory)))
    (pig:command ext "clear" "" (lambda (ctx args) (declare (ignore args)) (pig:ctx-set-editor-component ctx nil)))
    (pig:command ext "probe" "" (lambda (ctx args) (declare (ignore args))
                                  (let ((got (pig:ctx-get-editor-component ctx)))
                                    (pig:ctx-notify ctx (cond ((null got) "absent") ((eq got factory) "installed") (t "other")) "info"))))
    (when probe (funcall probe ext))
    ext))

(defun ed-overriding-factory (base)
  "Swallows q, rewrites a to A, upper-cases setText, brackets insertText, prefixes addToHistory and frames the base's rows."
  (pig:make-editor-component
   base
   :handle-input (lambda (data) (unless (string= data "q") (pig:editor-base-handle-input base (if (string= data "a") "A" data))))
   :set-text (lambda (text) (pig:editor-base-set-text base (string-upcase text)))
   :insert-text-at-cursor (lambda (text) (pig:editor-base-insert-text-at-cursor base (concatenate 'string "<" text ">")))
   :add-to-history (lambda (text) (pig:editor-base-add-to-history base (concatenate 'string "history:" text)))
   :render (lambda (width) (cons (format nil "[w=~D]" width) (pig:editor-base-render base width)))))

;;; ---------------------------------------------------------------- ctx-set-editor-component

(deftest an-installed-editor-runs-its-overrides-over-the-stock-editor
  (let ((stock (make-edstock)))
    (with-edh (edh (ed-editor-extension #'ed-overriding-factory) (edstock-handler stock))
      (ed-command edh "r1" "install")
      (let ((install (ed-find edh (ed-notify "ui.editor.install"))))
        (check-equal "editor-1" (pig:jget (ed-args install) "key"))
        (check (eq t (pig:jget (ed-args install) "delegated"))))
      ;; a key of another editor is not this one's
      (ed-host-notify edh "ui.editor.input" "key" "editor-0" "data" "z")
      (ed-host-notify edh "ui.editor.input" "key" "editor-1" "data" "q")
      (ed-wait edh (ed-notify "ui.editor.inputDone") 1)
      (check-equal nil (ed-ops-of edh "handleInput") "q is swallowed and z never arrived")
      (check-equal '("[w=80]" "base:") (ed-render-lines (ed-wait edh (ed-notify "ui.editor.render") 1)))
      (ed-host-notify edh "ui.editor.input" "key" "editor-1" "data" "a")
      (ed-wait edh (ed-notify "ui.editor.inputDone") 2)
      (check-equal "A" (pig:jget (cdr (first (ed-ops-of edh "handleInput"))) "data"))
      (let ((frame (ed-wait edh (ed-notify "ui.editor.render") 2)))
        (check-equal '("[w=80]" "base:A") (ed-render-lines frame))
        (check-equal 2 (pig:jget (ed-args frame) "seq"))
        (check-equal 80 (pig:jget (ed-args frame) "width")))
      ;; the host's setText, insertText and addToHistory reach the overrides, which reach the stock editor
      (ed-host-notify edh "ui.editor.setText" "key" "editor-1" "text" "hello")
      (ed-wait edh (ed-notify "ui.editor.render") 3)
      (check-equal "HELLO" (edstock-text stock))
      (ed-host-notify edh "ui.editor.insertText" "key" "editor-1" "text" "hi")
      (ed-host-notify edh "ui.editor.addToHistory" "key" "editor-1" "text" "cmd")
      (ed-wait edh (ed-notify "ui.editor.render") 4)
      (ed-wait edh (lambda (frame) (and (funcall (ed-call "ui.editor.base") frame) (equal (pig:jget (ed-args frame) "op") "addToHistory"))))
      (check-equal "HELLO<hi>" (edstock-text stock))
      (check-equal "history:cmd" (pig:jget (cdr (first (ed-ops-of edh "addToHistory"))) "text"))
      ;; a width change renders again at the new width
      (ed-host-notify edh "width_change" "width" 100)
      (check-equal '("[w=100]" "base:HELLO<hi>") (ed-render-lines (ed-wait edh (ed-notify "ui.editor.render") 5))))))

(deftest default-component-methods-are-the-stock-editors
  (let ((stock (make-edstock)))
    (with-edh (edh (ed-editor-extension (lambda (base) (pig:make-editor-component base))) (edstock-handler stock))
      (ed-command edh "r1" "install")
      (ed-host-notify edh "ui.editor.input" "key" "editor-1" "data" "k")
      (ed-wait edh (ed-notify "ui.editor.inputDone"))
      (check-equal "k" (edstock-text stock))
      (check-equal '("base:k") (ed-render-lines (ed-wait edh (ed-notify "ui.editor.render") 1))))))

(deftest the-editor-reports-its-factory-until-the-host-restores-its-own
  (let ((n 0) (factory (lambda (base) (pig:make-editor-component base))))
    (with-edh (edh (ed-editor-extension factory) (edstock-handler (make-edstock)))
      (flet ((probe ()
               (ed-command edh (format nil "p~D" (incf n)) "probe")
               (pig:jget (ed-args (ed-wait edh (ed-call "ui.notify") n)) "message")))
        (check-equal "absent" (probe))
        (ed-command edh "i1" "install")
        (check-equal "installed" (probe))
        (ed-command edh "c1" "clear")
        (check (eq t (pig:jget (ed-args (ed-wait edh (ed-call "ui.setEditorComponent"))) "clear")))
        (check-equal "absent" (probe))
        (ed-command edh "i2" "install")
        (check-equal "installed" (probe))
        ;; the host restored its own editor (the notify is read before the next request)
        (ed-host-notify edh "ui.editor.closed" "key" "editor-2")
        (check-equal "absent" (probe))))))

(deftest a-factory-that-signals-clears-the-host-editor
  (with-edh (edh (ed-editor-extension (lambda (base) (declare (ignore base)) (error "no editor today"))) (edstock-handler (make-edstock)))
    (check-equal "no editor today" (ed-error (ed-command edh "r1" "install")))
    (check-equal "editor-1" (pig:jget (ed-args (ed-wait edh (ed-notify "ui.editor.clear"))) "key"))))

(deftest a-factory-may-call-the-base
  (let ((stock (make-edstock)) (seen nil))
    (with-edh (edh (ed-editor-extension (lambda (base)
                                         (pig:editor-base-set-text base "eager")
                                         (setf seen (list (pig:editor-base-get-text base)
                                                          (multiple-value-list (pig:editor-base-get-cursor base))
                                                          (pig:editor-base-get-expanded-text base)
                                                          (pig:editor-base-is-showing-autocomplete-p base)
                                                          (pig:editor-base-get-lines base)))
                                         (pig:make-editor-component base)))
                   (edstock-handler stock))
      (ed-command edh "r1" "install")
      (check-equal '("eager" (2 5) "eager" t ("eager" "second")) seen))))

(deftest embedding-components-tell-the-host
  (with-edh (edh (ed-editor-extension (lambda (base) (pig:make-editor-component base :embed-working-status t))) (edstock-handler (make-edstock)))
    (ed-command edh "r1" "install")
    (let ((options (ed-args (ed-wait edh (ed-notify "ui.editor.options")))))
      (check-equal "editor-1" (pig:jget options "key"))
      (check (eq t (pig:jget options "embedWorkingStatus"))))))

(deftest the-host-tells-a-component-its-working-status-and-invalidate
  (let ((stock (make-edstock)) (heard '()) (invalidated 0))
    (with-edh (edh (ed-editor-extension
                    (lambda (base)
                      (pig:make-editor-component
                       base
                       :set-working-status-indicator (lambda (status) (push (and status (list (pig:editor-status-kind status) (pig:editor-status-message status)
                                                                                              (pig:editor-status-frames status) (pig:editor-status-frame status)
                                                                                              (pig:editor-status-spinner-color status)
                                                                                              (pig:editor-status-message-color status)
                                                                                              (pig:editor-status-indicator-verbatim status)))
                                                                                   heard))
                       :invalidate (lambda () (incf invalidated)))))
                   (edstock-handler stock))
      (ed-command edh "r1" "install")
      (flet ((configure (&rest members)
               (hsend (edh-host edh) "type" "notify"
                                     "notify" (pig:obj "method" "ui.editor.configure"
                                                       "args" (pig:obj "key" "editor-1" "workingStatus" (if members (apply #'pig:obj members) :null)))))
             (settle () (ed-wait edh (ed-notify "ui.editor.render") 1) (sleep 0.1)))
        (configure)                      ; no status and none heard before: nothing to tell
        (configure "kind" "working" "message" "Thinking" "frames" (vector "a" "b") "frame" 1 "spinnerColor" "accent" "messageColor" "dim" "indicatorVerbatim" t)
        (configure "kind" "working" "message" "Thinking" "frames" (vector "a" "b") "frame" 1 "spinnerColor" "accent" "messageColor" "dim" "indicatorVerbatim" t)
        (configure "kind" "working" "message" "Thinking" "frames" (vector "a" "b") "frame" 0 "spinnerColor" "accent" "messageColor" "dim" "indicatorVerbatim" t)
        (configure)
        (ed-host-notify edh "ui.editor.invalidate" "key" "editor-1")
        (settle)
        (loop repeat 400 until (and (= invalidated 1) (= (length heard) 3)) do (sleep 0.005))
        (check-equal '(nil ("working" "Thinking" ("a" "b") 0 "accent" "dim" t) ("working" "Thinking" ("a" "b") 1 "accent" "dim" t)) heard)
        (check-equal 1 invalidated)))))

(deftest the-base-hooks-run-the-components-app-handlers
  (let ((stock (make-edstock)) (ran '()))
    (setf (edstock-hook-keys stock) '(("e" . "app.interrupt") ("d" . "app.exit") ("v" . "app.clipboard.pasteImage") ("c" . "app.clear")))
    (with-edh (edh (ed-editor-extension
                    (lambda (base)
                      (flet ((say (what) (lambda () (push what ran))))
                        (setf (pig:editor-base-on-escape base) (say "escape") (pig:editor-base-on-ctrl-d base) (say "ctrl-d")
                              (pig:editor-base-on-paste-image base) (say "paste-image")
                              (pig:editor-base-on-extension-shortcut base) (lambda (data) (when (string= data "S") (push "shortcut" ran) t)))
                        (pig:editor-base-on-action base "app.clear" (say "clear"))
                        (pig:editor-base-on-action base "app.zeta" (say "zeta"))
                        (pig:editor-base-on-action base "app.alpha" (say "alpha"))
                        (pig:editor-base-on-action base "app.unused" (say "unused"))
                        (remhash "app.unused" (pig:editor-base-action-handlers base)))
                      (pig:make-editor-component base)))
                   (edstock-handler stock))
      (ed-command edh "r1" "install")
      (loop for key in '("e" "d" "v" "c" "S" "x") for n from 1
            do (ed-host-notify edh "ui.editor.input" "key" "editor-1" "data" key)
               (ed-wait edh (ed-notify "ui.editor.inputDone") n))
      (check-equal '("escape" "ctrl-d" "paste-image" "clear" "shortcut") (reverse ran))
      (check-equal "x" (edstock-text stock) "only the key without a handler is the editor's")
      (check-equal 5 (length (ed-ops-of edh "handleInput")) "the shortcut key never reaches the host")
      (check-equal '("app.interrupt" "app.exit" "app.clipboard.pasteImage" "app.alpha" "app.clear" "app.zeta" "extensionShortcut")
                   (coerce (pig:jget (cdr (car (last (ed-ops-of edh "handleInput")))) "hooks") 'list)))))

(deftest the-base-members-are-calls-on-the-host-editor
  (let ((stock (make-edstock)) (seen nil))
    (with-edh (edh (ed-editor-extension
                    (lambda (base)
                      (setf (pig:editor-base-disable-submit base) t (pig:editor-base-focused base) nil)
                      (pig:editor-base-set-padding-x base 3)
                      (pig:editor-base-set-autocomplete-max-visible base 7)
                      (pig:editor-base-set-autocomplete-provider base (pig:obj "basePath" "/work"))
                      (pig:editor-base-set-autocomplete-provider base nil)
                      (pig:editor-base-on-submit base "sub")
                      (pig:editor-base-on-change base "chg")
                      (pig:editor-base-handle-mouse base (pig:obj "x" 1))
                      (setf seen (list (pig:editor-base-get-padding-x base) (pig:editor-base-get-autocomplete-max-visible base)
                                       (pig:editor-base-border-color base "x") (pig:editor-base-disable-submit base) (pig:editor-base-focused base)))
                      (pig:make-editor-component base)))
                   (edstock-handler stock))
      (ed-command edh "r1" "install")
      (check-equal '(4 9 "[x]" t t) seen)
      (flet ((arg (op key &optional (n 0)) (pig:jget (cdr (nth n (ed-ops-of edh op))) key)))
        (check (eq t (arg "setDisableSubmit" "value")))
        (check (eq :false (arg "setFocused" "value")))
        (check-equal 3 (arg "setPaddingX" "n"))
        (check-equal 7 (arg "setAutocompleteMaxVisible" "n"))
        (check-equal "/work" (pig:jget (arg "setAutocompleteProvider" "provider") "basePath"))
        (check (eq :null (arg "setAutocompleteProvider" "provider" 1)))
        (check-equal "sub" (arg "onSubmit" "text"))
        (check-equal "chg" (arg "onChange" "text"))
        (check-equal 1 (pig:jget (arg "handleMouse" "event") "x"))))))

(deftest a-component-that-signals-is-reported-and-the-worker-goes-on
  (let ((stock (make-edstock)))
    (with-edh (edh (ed-editor-extension
                    (lambda (base)
                      (pig:make-editor-component
                       base :handle-input (lambda (data) (if (string= data "!") (error "bad key") (pig:editor-base-handle-input base data))))))
                   (edstock-handler stock))
      (ed-command edh "r1" "install")
      (ed-host-notify edh "ui.editor.input" "key" "editor-1" "data" "!")
      (ed-host-notify edh "ui.editor.input" "key" "editor-1" "data" "k")
      (ed-wait edh (ed-notify "ui.editor.inputDone") 2)
      (let ((report (ed-args (ed-wait edh (ed-notify "ui.notify")))))
        (check-equal "editor handleInput failed: bad key" (pig:jget report "message"))
        (check-equal "error" (pig:jget report "level")))
      (check-equal "k" (edstock-text stock)))))

(deftest a-second-install-replaces-the-first-editor
  (let ((stock (make-edstock)))
    (with-edh (edh (ed-editor-extension (lambda (base) (pig:make-editor-component base))) (edstock-handler stock))
      (ed-command edh "r1" "install")
      (ed-command edh "r2" "install")
      (check-equal "editor-2" (pig:jget (ed-args (ed-wait edh (ed-notify "ui.editor.install") 2)) "key"))
      (ed-host-notify edh "ui.editor.input" "key" "editor-1" "data" "old")
      (ed-host-notify edh "ui.editor.input" "key" "editor-2" "data" "new")
      (ed-wait edh (ed-notify "ui.editor.inputDone"))
      (check-equal "new" (edstock-text stock)))))

(defun ed-check-no-editor (options)
  "Run install and probe in a host started with OPTIONS: the factory never runs and the host hears of no editor."
  (let* ((ran nil) (ext (ed-editor-extension (lambda (base) (setf ran t) (pig:make-editor-component base))))
         (edh (apply #'ed-start ext (edstock-handler (make-edstock)) options)))
    (unwind-protect
         (progn
           (ed-command edh "r1" "install")
           (ed-command edh "r2" "probe")
           (check (null ran) "the factory never ran")
           (check-equal 0 (ed-count edh (ed-notify "ui.editor.install")))
           (check-equal "absent" (pig:jget (ed-args (ed-wait edh (ed-call "ui.notify"))) "message")))
      (stop-host (edh-host edh)))))

(deftest no-editor-is-installed-without-a-ui-or-in-rpc-mode
  (ed-check-no-editor '(:mode "rpc"))
  (ed-check-no-editor '(:has-ui nil)))

;;; ---------------------------------------------------------------- ctx-add-autocomplete-provider

(defun ed-wrapping-factory (calls)
  "A factory whose provider upper-cases the current provider's labels and appends -w to a completion."
  (lambda (ctx current)
    (declare (ignorable ctx))
    (push :factory (car calls))
    (pig:make-autocomplete-provider
     :trigger-characters '("#")
     :get-suggestions (lambda (ctx lines line col force)
                        (let ((result (pig:autocomplete-get-suggestions current ctx lines line col force)))
                          (when result
                            (setf (gethash "prefix" result) (string-upcase (pig:jget result "prefix"))))
                          result))
     :apply-completion (lambda (ctx lines line col item prefix)
                         (let ((result (pig:autocomplete-apply-completion current ctx lines line col item prefix)))
                           (setf (aref (pig:jget result "lines") 0) (concatenate 'string (aref (pig:jget result "lines") 0) "-w"))
                           result))
     :should-trigger-file-completion (lambda (ctx lines line col)
                                       (and (pig:autocomplete-should-trigger-file-completion current ctx lines line col) (string/= (aref lines 0) "no"))))))

(defun ed-autocomplete-host (method args id)
  "A host whose base provider answers ui.autocomplete.invoke; the add call stays pending until the test answers it."
  (declare (ignore id))
  (cond ((equal method "ui.addAutocompleteProvider") :defer)
        ((equal method "ui.autocomplete.invoke")
         (let ((op (pig:jget args "operation")))
           (cond ((equal op "getSuggestions")
                  (if (equal (aref (pig:jget args "lines") 0) "empty")
                      nil
                      (pig:obj "items" (vector (pig:obj "value" (if (eq t (pig:jget args "force")) "forced" "keep")))
                               "prefix" (format nil "p~D:~D:~A" (pig:jget args "cursorLine") (pig:jget args "cursorCol") (pig:jget args "id")))))
                 ((equal op "applyCompletion")
                  (pig:obj "lines" (vector (pig:jget (pig:jget args "item") "value")) "cursorLine" 0 "cursorCol" 3))
                 (t t))))
        (t (pig:obj))))

(defun ed-autocomplete-extension (calls)
  (let ((ext (pig:make-extension "ac")))
    (pig:command ext "register" "" (lambda (ctx args) (declare (ignore args)) (pig:ctx-add-autocomplete-provider ctx (ed-wrapping-factory calls))))
    (pig:command ext "bad" "" (lambda (ctx args) (declare (ignore args)) (pig:ctx-add-autocomplete-provider ctx (lambda (ctx current) (declare (ignore ctx current)) :not-a-provider))))
    (pig:command ext "missing" "" (lambda (ctx args) (declare (ignore args)) (pig:ctx-add-autocomplete-provider ctx)))
    ext))

(defun ed-ask (edh id method &rest members)
  "Send the host request METHOD with args MEMBERS and the response frame."
  (hsend (edh-host edh) "type" "request" "id" id "request" (pig:obj "method" method "args" (apply #'pig:obj members)))
  (ed-wait edh (ed-response id)))

(defun ed-register-wrapper (edh command current-id &key (file-trigger t))
  "Run COMMAND (which adds a factory), let the host wrap it around the base provider CURRENT-ID and finish the add call. The wrap's response frame."
  (hsend (edh-host edh) "type" "request" "id" "reg" "request" (pig:obj "method" "command" "tool" command "args" ""))
  (let* ((add (ed-wait edh (ed-call "ui.addAutocompleteProvider")))
         (wrapped (ed-ask edh "wrap" "autocomplete.sync" "operation" "wrap" "factoryId" (pig:jget (ed-args add) "factoryId")
                                      "current" (pig:obj "id" current-id "triggerCharacters" (vector "$") "hasFileTrigger" (if file-trigger t :false)))))
    (edh-reply edh (pig:jget add "id") (pig:obj))
    (ed-wait edh (ed-response "reg"))
    (values wrapped add)))

(deftest an-autocomplete-factory-wraps-the-host-provider-and-the-host-drives-it
  (with-edh (edh (ed-autocomplete-extension (list nil)) #'ed-autocomplete-host)
    (multiple-value-bind (wrapped add) (ed-register-wrapper edh "register" "cur-1")
      (let ((factory-id (pig:jget (ed-args add) "factoryId")) (result (ed-result wrapped)))
        (check (and (stringp factory-id) (= 32 (length factory-id))))
        (check-equal '("#") (coerce (pig:jget result "triggerCharacters") 'list))
        (check (eq t (pig:jget result "hasFileTrigger")))
        ;; the wrapper reaches the base through the host, naming the current provider and the request's cursor
        (let ((suggestions (ed-result (ed-ask edh "s1" "autocomplete.suggest" "id" (pig:jget result "id") "operation" "getSuggestions"
                                                          "lines" (vector "q") "cursorLine" 0 "cursorCol" 1 "force" :false)))
              (invoke (ed-wait edh (ed-call "ui.autocomplete.invoke"))))
          (check-equal "keep" (pig:jget (aref (pig:jget suggestions "items") 0) "value"))
          (check-equal "P0:1:CUR-1" (pig:jget suggestions "prefix") "the wrapper upper-cased the base's prefix")
          (check-equal "cur-1" (pig:jget (ed-args invoke) "id"))
          (check-equal "getSuggestions" (pig:jget (ed-args invoke) "operation"))
          (check (eq :false (pig:jget (ed-args invoke) "force"))))))))

(deftest autocomplete-operations-answer-through-the-wrapper
  (with-edh (edh (ed-autocomplete-extension (list nil)) #'ed-autocomplete-host)
    (let* ((id (pig:jget (ed-result (ed-register-wrapper edh "register" "cur-2")) "id")) (n 0))
      (flet ((operate (&rest members)
               (apply #'ed-ask edh (format nil "o~D" (incf n)) "autocomplete.sync" "id" id members)))
        ;; absence is null; force reaches the base as true
        (check (eq :null (ed-result (operate "operation" "getSuggestions" "lines" (vector "empty") "cursorLine" 0 "cursorCol" 5))))
        (check (eq :false (pig:jget (ed-args (ed-wait edh (ed-call "ui.autocomplete.invoke") 1)) "force")))
        (check-equal "forced" (pig:jget (aref (pig:jget (ed-result (operate "operation" "getSuggestions" "lines" (vector "x") "cursorLine" 0 "cursorCol" 1 "force" t))
                                                        "items")
                                              0)
                                        "value"))
        (check (eq t (pig:jget (ed-args (ed-wait edh (ed-call "ui.autocomplete.invoke") 2)) "force")))
        (let ((completion (ed-result (operate "operation" "applyCompletion" "lines" (vector "in") "cursorLine" 0 "cursorCol" 2
                                                                             "item" (pig:obj "value" "done") "prefix" "in"))))
          (check-equal "done-w" (aref (pig:jget completion "lines") 0))
          (check-equal 3 (pig:jget completion "cursorCol")))
        (check (eq t (ed-result (operate "operation" "shouldTriggerFileCompletion" "lines" (vector "yes") "cursorLine" 0 "cursorCol" 3))))
        (check (eq :false (ed-result (operate "operation" "shouldTriggerFileCompletion" "lines" (vector "no") "cursorLine" 0 "cursorCol" 2)))
               "false stays false on the wire")
        (check-equal "unknown autocomplete operation: nope" (ed-error (operate "operation" "nope")))
        ;; the host dropped the provider (the notify is read before the next request)
        (ed-host-notify edh "autocomplete.release" "id" id)
        (check (search "unknown autocomplete provider" (ed-error (operate "operation" "getSuggestions" "lines" (vector "x") "cursorLine" 0 "cursorCol" 1))))))))

(deftest an-autocomplete-factory-must-return-a-provider
  (with-edh (edh (ed-autocomplete-extension (list nil)) #'ed-autocomplete-host)
    (hsend (edh-host edh) "type" "request" "id" "bad" "request" (pig:obj "method" "command" "tool" "bad" "args" ""))
    (let ((add (ed-wait edh (ed-call "ui.addAutocompleteProvider"))))
      (check-equal "autocomplete factory must return a provider"
                   (ed-error (ed-ask edh "w1" "autocomplete.sync" "operation" "wrap" "factoryId" (pig:jget (ed-args add) "factoryId")
                                                                 "current" (pig:obj "id" "c" "triggerCharacters" (vector) "hasFileTrigger" :false))))
      (edh-reply edh (pig:jget add "id") (pig:obj))
      (ed-wait edh (ed-response "bad")))
    (check-equal "autocomplete factory is missing" (ed-error (ed-command edh "m" "missing")))))

(deftest autocomplete-is-ignored-without-a-ui
  (with-edh (edh (ed-autocomplete-extension (list nil)) #'ed-autocomplete-host :has-ui nil)
    (ed-command edh "r1" "register")
    (ed-command edh "r2" "missing")
    (check-equal 0 (ed-count edh (ed-call "ui.addAutocompleteProvider")))))

(deftest a-dropped-current-provider-is-released-on-the-host
  (with-edh (edh (ed-autocomplete-extension (list nil)) #'ed-autocomplete-host)
    ;; the wrapper holds the current provider; once the host releases the wrapper nothing does
    (let ((id (sb-thread:join-thread (sb-thread:make-thread (lambda () (pig:jget (ed-result (ed-register-wrapper edh "register" "gone-1")) "id"))))))
      (ed-host-notify edh "autocomplete.release" "id" id)
      (ed-ask edh "after" "autocomplete.sync" "id" id "operation" "nope"))
    (loop repeat 100 until (ed-find edh (ed-notify "ui.autocomplete.release")) do (sb-ext:gc :full t) (sleep 0.05))
    (let ((release (ed-find edh (ed-notify "ui.autocomplete.release"))))
      (check release "the host is told the extension dropped the provider")
      (when release (check-equal "gone-1" (pig:jget (ed-args release) "id"))))))

;;; ---------------------------------------------------------------- ctx-on-terminal-input

(defun ed-terminal-extension (log)
  (let ((ext (pig:make-extension "term")) (unsubscribers '()))
    (pig:command ext "subscribe" ""
                 (lambda (ctx args) (declare (ignore args))
                   (push (pig:ctx-on-terminal-input ctx (lambda (data) (push (list :first data) (car log))
                                                                 (if (string= data "a") (pig:make-terminal-input-result :data "b") nil)))
                         unsubscribers)
                   (push (pig:ctx-on-terminal-input ctx (lambda (data) (push (list :second data) (car log))
                                                                 (cond ((string= data "x") (pig:obj "consume" t))
                                                                       ((string= data "e") (error "listener failed"))
                                                                       ((string= data "z") (pig:make-terminal-input-result :data ""))
                                                                       (t (pig:obj "data" :null)))))
                         unsubscribers)))
    (pig:command ext "unsubscribe" ""
                 (lambda (ctx args) (declare (ignore ctx args))
                   (loop for unsubscribe in (reverse unsubscribers) do (funcall unsubscribe) (funcall unsubscribe))
                   (setf unsubscribers '())))
    ext))

(defun ed-terminal-input (edh id data) (ed-result (ed-ask edh id "terminal_input" "data" data)))

(defun ed-no-handler (method args id) (declare (ignore method args id)) (pig:obj))

(deftest terminal-input-handlers-run-in-order-and-the-host-hears-the-first-and-last
  (let ((log (list nil)))
    (with-edh (edh (ed-terminal-extension log) #'ed-no-handler)
      (check-equal 0 (ed-count edh (ed-call "ui.onTerminalInput")))
      (ed-command edh "s" "subscribe")
      (check-equal 1 (ed-count edh (ed-call "ui.onTerminalInput")) "the host is told on the first subscription only")
      ;; the first handler's replacement is what the second sees, and the verdict carries it
      (let ((verdict (ed-terminal-input edh "t1" "a")))
        (check (eq :false (pig:jget verdict "consume")))
        (check-equal "b" (pig:jget verdict "data")))
      (check-equal '((:first "a") (:second "b")) (reverse (car log)))
      ;; an unchanged chunk has no data member
      (let ((verdict (ed-terminal-input edh "t2" "k")))
        (check (eq :false (pig:jget verdict "consume")))
        (check (null (nth-value 1 (gethash "data" verdict)))))
      ;; a consumed chunk ends the chain
      (let ((verdict (ed-terminal-input edh "t3" "x")))
        (check (eq t (pig:jget verdict "consume")))
        (check (null (nth-value 1 (gethash "data" verdict)))))
      ;; a handler that signals is reported on stderr and does not consume
      (check (eq :false (pig:jget (ed-terminal-input edh "t4" "e") "consume")))
      ;; an empty replacement drops the chunk: the verdict's data is the empty string
      (check-equal "" (pig:jget (ed-terminal-input edh "t5" "z") "data"))
      (check (null (ed-find edh (ed-call "ui.offTerminalInput"))))
      (ed-command edh "u" "unsubscribe")
      (check-equal 1 (ed-count edh (ed-call "ui.offTerminalInput")) "the host is told on the last unsubscribe only, however often it is called")
      (let ((verdict (ed-terminal-input edh "t6" "a")))
        (check (eq :false (pig:jget verdict "consume")))
        (check (null (nth-value 1 (gethash "data" verdict))) "no handler is left")))))

(deftest terminal-input-keeps-no-subscription-without-a-ui
  (with-edh (edh (ed-terminal-extension (list nil)) #'ed-no-handler :has-ui nil)
    (ed-command edh "s" "subscribe")
    (ed-command edh "u" "unsubscribe")
    (check-equal 0 (ed-count edh (ed-call "ui.onTerminalInput")))
    (check-equal 0 (ed-count edh (ed-call "ui.offTerminalInput")))
    (check (eq :false (pig:jget (ed-terminal-input edh "t1" "x") "consume")))))
