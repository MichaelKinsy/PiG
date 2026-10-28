(in-package "lisp-sdk-fixture")
;;; the probe commands, tools and event handlers of lane U2 (see def-fixture in fixture.lisp)

;;; ---- editor components (editor_component_test.go, editor_component_members_test.go, editor_getter_test.go)

(defun lfx-editor-raw-p (text) (and (>= (length text) 4) (string= "raw:" text :end2 4)))

(defun lfx-editor-join (separator lines)
  (with-output-to-string (out)
    (loop for line in lines for first = t then nil
          do (unless first (write-string separator out)) (write-string line out))))

(defun lfx-editor-handle (base data)
  (cond ((string= data "q"))
        ((string= data "L")
         (pig:editor-base-set-text base (concatenate 'string "raw:lines=" (lfx-editor-join "|" (pig:editor-base-get-lines base)))))
        (t (pig:editor-base-handle-input base (if (string= data "a") "A" data)))))

(defun lfx-conformance-editor (base &key embed handle-input set-working-status-indicator invalidate)
  "Swallows \"q\", rewrites \"a\" to \"A\", upper-cases the host's setText, and frames super.render."
  (pig:make-editor-component
   base :embed-working-status embed
        :handle-input (or handle-input (lambda (data) (lfx-editor-handle base data)))
        :set-text (lambda (text) (pig:editor-base-set-text base (if (lfx-editor-raw-p text) text (string-upcase text))))
        :insert-text-at-cursor (lambda (text) (pig:editor-base-insert-text-at-cursor base (if (lfx-editor-raw-p text) text (concatenate 'string "<" text ">"))))
        :add-to-history (lambda (text) (pig:editor-base-add-to-history base (if (lfx-editor-raw-p text) text (concatenate 'string "history:" text))))
        :render (lambda (width) (cons "[custom editor]" (pig:editor-base-render base width)))
        :set-working-status-indicator set-working-status-indicator
        :invalidate invalidate))

(defun lfx-editor-factory (base) (lfx-conformance-editor base))
(defun lfx-editor-embed-factory (base) (lfx-conformance-editor base :embed t))

(defun lfx-editor-bool (value) (if value "true" "false"))

(defun lfx-editor-hooks-factory (base)
  "A CustomEditor that sets the handlers Pi keeps as public fields, reads and sets the members it inherits from Pi's Editor on \"M\", and hears
the host's working status and invalidate. Each handler writes what ran into the host's text."
  (flet ((say (what) (lambda () (pig:editor-base-set-text base (concatenate 'string "raw:hook=" what)))))
    (setf (pig:editor-base-on-escape base) (say "escape")
          (pig:editor-base-on-ctrl-d base) (say "ctrl-d")
          (pig:editor-base-on-paste-image base) (say "paste-image"))
    (pig:editor-base-on-action base "app.clear" (say "clear"))
    ;; action-handlers is the live table: an entry removed from it is no longer the component's
    (pig:editor-base-on-action base "app.unused" (say "unused"))
    (remhash "app.unused" (pig:editor-base-action-handlers base))
    (setf (pig:editor-base-on-extension-shortcut base)
          (lambda (data)
            (when (string= data "S")
              (pig:editor-base-set-text base "raw:hook=shortcut")
              t)))
    (lfx-conformance-editor
             base
             :handle-input
             (lambda (data)
               (if (string/= data "M")
                   (lfx-editor-handle base data)
                   (let* ((padding (pig:editor-base-get-padding-x base))
                          (visible (pig:editor-base-get-autocomplete-max-visible base))
                          (border (pig:editor-base-border-color base "x"))
                          (disabled (progn (setf (pig:editor-base-disable-submit base) t) (pig:editor-base-disable-submit base)))
                          (focused (progn (setf (pig:editor-base-focused base) t) (pig:editor-base-focused base))))
                     (pig:editor-base-set-autocomplete-provider
                      base (pig:obj "commands" (vector (pig:obj "name" "help" "description" "Show help")) "basePath" "/work"))
                     (pig:editor-base-on-submit base "sub")
                     (pig:editor-base-on-change base "chg")
                     (pig:editor-base-set-text base (format nil "raw:members=padding=~D,max=~D,border=~A,disable=~A,focused=~A"
                                                            padding visible border (lfx-editor-bool disabled) (lfx-editor-bool focused))))))
             :set-working-status-indicator
             (lambda (status)
               (pig:editor-base-set-text
                base (if status
                         (format nil "raw:status=~A/~A/~D" (pig:editor-status-kind status) (pig:editor-status-message status)
                                 (pig:editor-status-frame status))
                         "raw:status=none")))
             :invalidate (lambda () (pig:editor-base-set-text base "raw:invalidated")))))

(def-fixture "editor-components" (ext)
  (pig:command ext "editor-install-hooks" "Install a custom editor that sets CustomEditor's handlers"
               (lambda (ctx args) (declare (ignore args)) (pig:ctx-set-editor-component ctx #'lfx-editor-hooks-factory)))
  (pig:command ext "editor-install-eager" "Install a custom editor that calls super in its factory"
               (lambda (ctx args)
                 (declare (ignore args))
                 (pig:ctx-set-editor-component
                  ctx (lambda (base)
                        (handler-case
                            (progn (pig:editor-base-set-text base "eager")
                                   (pig:ctx-notify ctx (concatenate 'string "eager:" (pig:editor-base-get-text base)) "info")
                                   ;; the stock editor's other queries and setters, at the values it already holds
                                   (pig:editor-base-get-cursor base)
                                   (pig:editor-base-get-expanded-text base)
                                   (pig:editor-base-is-showing-autocomplete-p base)
                                   (pig:editor-base-set-padding-x base 0)
                                   (pig:editor-base-set-autocomplete-max-visible base 5))
                          (error (e) (pig:ctx-notify ctx (format nil "eager-error:~A" e) "info")))
                        (lfx-conformance-editor base)))))
  (pig:command ext "editor-install-embed" "Install a custom editor that embeds the working status"
               (lambda (ctx args) (declare (ignore args)) (pig:ctx-set-editor-component ctx #'lfx-editor-embed-factory)))
  (pig:command ext "editor-install" "Install a custom editor component"
               (lambda (ctx args) (declare (ignore args)) (pig:ctx-set-editor-component ctx #'lfx-editor-factory)))
  (pig:command ext "editor-clear" "Restore the host's editor"
               (lambda (ctx args) (declare (ignore args)) (pig:ctx-set-editor-component ctx nil)))
  (pig:command ext "editor-get-probe" "Report the factory get-editor-component returns"
               (lambda (ctx args)
                 (declare (ignore args))
                 (let ((got (pig:ctx-get-editor-component ctx)))
                   (pig:ctx-notify ctx (format nil "editor_get:~A:end" (cond ((null got) "absent") ((eq got #'lfx-editor-factory) "installed") (t "other")))
                                   "info")))))

;;; ---- raw terminal input (TestConformance_TerminalInput)

(defun lfx-term-verdict (ctx data)
  (let ((high (string (code-char #xD83D))) (low (string (code-char #xDE00))) (emoji (string (code-char #x1F600))))
    (cond ((or (string= data high) (string= data low) (string= data emoji))
           (pig:make-terminal-input-result :data (concatenate 'string "seen:" data)))
          ((string= data (format nil "~C[96~~" #\Esc))
           (pig:make-terminal-input-result
            :data (pig:to-json (vector (pig:ctx-get-editor-text ctx) (if (pig:ctx-tools-expanded-p ctx) t :false)))))
          ((string= data (format nil "~C[98~~" #\Esc)) (pig:make-terminal-input-result :data "rewritten"))
          ((string= data (format nil "~C[97~~" #\Esc)) (sleep 0.2) (pig:make-terminal-input-result :consume t))
          (t (pig:make-terminal-input-result :consume (string= data (format nil "~C[99~~" #\Esc)))))))

(def-fixture "terminal-input" (ext)
  (let ((unsubscribers '()))
    (pig:command ext "term_subscribe" "Subscribe to raw terminal input"
                 (lambda (ctx args) (declare (ignore args))
                   (push (pig:ctx-on-terminal-input ctx (lambda (data) (lfx-term-verdict ctx data))) unsubscribers)))
    (pig:command ext "term_unsubscribe" "Release the raw input subscription"
                 (lambda (ctx args) (declare (ignore ctx args))
                   (loop while unsubscribers do (funcall (pop unsubscribers)))))))

;;; ---- autocomplete provider factories (TestAutocompleteFactoriesAcrossSDKs)

(defun lfx-copy-obj (table)
  (let ((copy (pig:obj))) (maphash (lambda (key value) (setf (gethash key copy) value)) table) copy))

(defun lfx-autocomplete-factory (tag)
  "A factory whose provider wraps the current one: A drops \"drop\" and labels each item with the call count, B appends a \"tail\" item."
  (lambda (ctx current)
    (pig:ctx-notify ctx (concatenate 'string "factory:" tag) "info")
    (let ((calls 0) (tag-a (string= tag "A")))
      (pig:make-autocomplete-provider
       :trigger-characters (if tag-a '("$") '("#" "$"))
       :get-suggestions
       (lambda (ctx lines line col force)
         (incf calls)
         (let ((result (pig:autocomplete-get-suggestions current ctx lines line col force)))
           (when result
             (let ((items (coerce (pig:jget result "items") 'list)) (copy (lfx-copy-obj result)))
               (setf (gethash "items" copy)
                     (coerce
                      (if tag-a
                          (loop for item in items
                                unless (equal (pig:jget item "value") "drop")
                                  collect (let ((labelled (lfx-copy-obj item)))
                                            (setf (gethash "label" labelled) (format nil "~A:~D" (pig:jget item "value") calls))
                                            labelled))
                          (append items (list (pig:obj "value" "tail" "label" (format nil "tail:~D" calls)))))
                      'simple-vector))
               copy))))
       :apply-completion
       (lambda (ctx lines line col item prefix)
         (let* ((result (pig:autocomplete-apply-completion current ctx lines line col item prefix))
                (cursor-line (pig:jget result "cursorLine")) (result-lines (pig:jget result "lines")))
           (setf (aref result-lines cursor-line) (concatenate 'string (aref result-lines cursor-line) "-" tag))
           (setf (gethash "cursorCol" result) (+ (pig:jget result "cursorCol") 2))
           result))
       :should-trigger-file-completion
       (lambda (ctx lines line col) (pig:autocomplete-should-trigger-file-completion current ctx lines line col))))))

(def-fixture "autocomplete" (ext)
  (pig:command ext "autocomplete-register" "Register retained provider wrappers"
               (lambda (ctx args)
                 (declare (ignore args))
                 (dolist (tag '("A" "B"))
                   (pig:ctx-add-autocomplete-provider ctx (lfx-autocomplete-factory tag))
                   (pig:ctx-notify ctx (concatenate 'string "registered:" tag) "info")))))
