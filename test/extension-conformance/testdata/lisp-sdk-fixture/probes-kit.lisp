(in-package "lisp-sdk-fixture")
;;; the probe commands, tools and event handlers of this file: the component kit, ui.custom and overlays, widgets, footer and header, the
;;; working indicator (see def-fixture in fixture.lisp). Each is the Python fixture's probe of the same name (python-sdk-fixture/main.py).

(defun kp-nl (&rest lines) (format nil "~{~A~^~%~}" lines))

(defun kp-notify (ctx text) (pig:ctx-notify ctx text "info"))

(defun kp-bool (value) (if value "true" "false"))

;;; ---------------------------------------------------------------- the redraw panel and the abilities watcher
;;; D614: the same way in every SDK fixture (conformance TestConformance_UIViewAnswerRedraw and TestConformance_UIViewAbilitiesEvent).

(defvar *kp-redraw-command* nil
  "The context of the command that mounted the redraw panel: an update made through it is the extension's, not an intent handler's.")
(defvar *kp-watch-armed* nil)
(defvar *kp-watch-block* nil)
(defvar *kp-watch-released* nil)
(defvar *kp-watch-events* 0)

(defun kp-text-view (text) (pig:kit-view (pig:kit-text text 0 0)))

(defun kp-redraw-intents ()
  (let ((int (pig:view-shape "integer")))
    (loop for name in '("fx.redraw" "fx.foreign")
          collect (pig:view-intent name (pig:view-shape "object" :properties (list (cons "n" int)) :required '("n"))
                                   :answer (pig:view-shape "object" :properties (list (cons "rev" int)) :required '("rev"))))))

(defun kp-describe-abilities (ab)
  (if ab
      (format nil "slots=~{~A~^+~},mounts=~D" (coerce (pig:jget ab "slots") 'list) (pig:jref ab "limits" "mounts"))
      "none"))

(defun kp-redraw-intent (ctx inv)
  "Answer fx.redraw and fx.foreign: both replace the view of the mount the intent came from and answer done with the new rev. fx.redraw
updates through the handler's context; fx.foreign through the context of the command that mounted the panel."
  (let* ((own (equal (pig:jget inv "intent") "fx.redraw"))
         (ab (pig:ctx-view-abilities ctx))
         (n (+ (pig:jget (pig:jget inv "arg") "n") (if ab (pig:jref ab "limits" "intentArgBytes") 0)))
         (mount (pig:view-ref (pig:jref inv "mount" "key") (pig:jref inv "mount" "epoch") (pig:jget inv "rev"))))
    (handler-case
        (let ((st (pig:ctx-update-view (if own ctx *kp-redraw-command*) mount
                                       (kp-text-view (format nil "fixture-~A-~D" (if own "redraw" "foreign") n))
                                       :intents (kp-redraw-intents))))
          (pig:view-intent-result "done" :answer (pig:obj "rev" (pig:view-ref-rev (pig:view-status-mount st)))))
      (pig:host-call-error (e) (pig:view-intent-result "failed" :error (pig:host-call-error-code e))))))

(defun kp-register-view-rows (ext)
  (pig:command ext "ui_view_redraw" "Mount the panel whose intents redraw it"
               (lambda (ctx args) (declare (ignore args))
                 (let ((st (pig:ctx-mount-view ctx "redraw" "panel" (kp-text-view "fixture-redraw-0") :intents (kp-redraw-intents))))
                   (setf *kp-redraw-command* ctx)
                   (kp-notify ctx (format nil "view-redraw:rev=~D" (pig:view-ref-rev (pig:view-status-mount st)))))))
  (pig:on-view-abilities
   ext
   (lambda (ctx ab)
     (incf *kp-watch-events*)
     (when *kp-watch-armed*
       (when *kp-watch-block*
         (setf *kp-watch-block* nil)
         (kp-notify ctx "view-event-blocked")
         (loop until *kp-watch-released* do (sleep 0.01))
         (setf *kp-watch-released* nil)
         (kp-notify ctx "view-event-released"))
       (cond ((null ab) (kp-notify ctx "view-event:none"))
             (t (kp-notify ctx (format nil "view-event:~A,same=~A" (kp-describe-abilities ab)
                                       (kp-bool (equal (kp-describe-abilities ab) (kp-describe-abilities (pig:ctx-view-abilities ctx))))))
                (kp-notify ctx (format nil "view-arrived:~A"
                                       (handler-case
                                           (pig:view-status-state
                                            (pig:ctx-mount-view ctx "arrived" "panel"
                                                                (kp-text-view (format nil "fixture-arrived-~D" (pig:jref ab "limits" "mounts")))))
                                         (pig:host-call-error (e) (pig:host-call-error-code e))))))))))
  (pig:command ext "ui_view_watch" "Arm the abilities watcher"
               (lambda (ctx args) (declare (ignore args)) (setf *kp-watch-armed* t) (kp-notify ctx "view-watch:armed")))
  (pig:command ext "ui_view_block" "Block the abilities watcher on its next event"
               (lambda (ctx args) (declare (ignore args)) (setf *kp-watch-released* nil *kp-watch-block* t) (kp-notify ctx "view-block:set")))
  (pig:command ext "ui_view_release" "Release the blocked abilities watcher"
               (lambda (ctx args) (declare (ignore args)) (setf *kp-watch-released* t) (kp-notify ctx "view-release:done")))
  (pig:command ext "ui_view_events" "Report how often the abilities watcher ran"
               (lambda (ctx args) (declare (ignore args)) (kp-notify ctx (format nil "view-events:~D" *kp-watch-events*)))))

;;; ---------------------------------------------------------------- kit-probe and mouse-probe

(defun kp-probe-view ()
  "The component kit's conformance view (D107, docs/plan/extension-component-kit.md section 10)."
  (pig:kit-view
   (pig:kit-container
    (list (pig:kit-dynamic-border "accent")
          (pig:kit-text "Kit probe" 2 0 :bg "customMessageBg")
          (pig:kit-markdown (kp-nl "- one" "- **two**") 1 0)
          (pig:kit-hstack (list (pig:kit-stack-entry (pig:kit-truncated-text "left side" 0 0) :grow 1)
                                (pig:kit-stack-entry (pig:kit-truncated-text "right" 0 0) :grow 1))
                          :gap 1)
          (pig:kit-spacer 1)
          (pig:kit-select-list "kit-tracks"
                               (loop for i below 5 collect (pig:kit-select-item (format nil "k~D" i) (format nil "Track ~D" i) (format nil "Artist ~D" i)))
                               3 :selected-index 2)))
   :focus "kit-tracks" :theme (pig:obj "accent" "#d75f00")))

(defun kp-kit-probe-component ()
  "Logs every input and view event and closes on select with the log joined by \",\"."
  (let ((log '()))
    (pig:make-custom-component
     :view (lambda (width) (declare (ignore width)) (kp-probe-view))
     :handle-input (lambda (data) (push (concatenate 'string "input:" data) log) nil)
     :handle-view-event (lambda (event)
                          (push (format nil "~A:~D:~A" (pig:kit-event-type event) (pig:kit-event-index event)
                                        (if (pig:kit-event-item event) (pig:jget (pig:kit-event-item event) "value") ""))
                                log)
                          (and (equal (pig:kit-event-type event) "select")
                               (pig:make-custom-result :done t :value (format nil "~{~A~^,~}" (reverse log))))))))

(defun kp-viewport-component ()
  "Captures viewport input until it receives \"t\", then stops; closes with \"viewport=done\" on \"q\"."
  (let ((capturing t))
    (pig:make-custom-component
     :render (lambda (width) (declare (ignore width)) (list "viewport probe"))
     :captures-viewport-input (lambda () capturing)
     :handle-input (lambda (data)
                     (cond ((equal data "t") (setf capturing nil) nil)
                           ((equal data "q") (pig:make-custom-result :done t :value "viewport=done")))))))

(defun kp-mouse-component ()
  "Logs every mouse event it receives, all of its fields, and closes on a click with the log joined by \",\"."
  (let ((log '()))
    (pig:make-custom-component
     :render (lambda (width) (declare (ignore width)) (list "mouse probe" "row 1" "row 2" "row 3"))
     :handle-input (lambda (data) (declare (ignore data)) nil)
     :handle-mouse (lambda (event)
                     (push (format nil "~A/~A/~D,~D/~D,~D/~Dx~D/w~D/c~D/~A~A~A"
                                   (pig:mouse-event-type event) (pig:mouse-event-button event) (pig:mouse-event-x event) (pig:mouse-event-y event)
                                   (pig:mouse-event-screen-x event) (pig:mouse-event-screen-y event) (pig:mouse-event-width event)
                                   (pig:mouse-event-height event) (pig:mouse-event-wheel-delta event) (pig:mouse-event-click-count event)
                                   (if (pig:mouse-event-shift event) "S" "") (if (pig:mouse-event-alt event) "A" "") (if (pig:mouse-event-ctrl event) "C" ""))
                           log)
                     (and (equal (pig:mouse-event-type event) "click")
                          (pig:make-custom-result :done t :value (format nil "~{~A~^,~}" (reverse log))))))))

;;; ---------------------------------------------------------------- the widget kinds' Pi vector cases

(defvar *kp-cases* (make-hash-table :test 'equal)
  "The widget kinds' Pi vector cases (D107, docs/plan/extension-component-kit.md section 10): \"<module>/<export>/<case name>\" -> a function building the view.")

(defmacro kp-case (key form) `(setf (gethash ,key *kp-cases*) (lambda () ,form)))
(defmacro kp-cases (prefix names form)
  `(dolist (name ',names) (setf (gethash (concatenate 'string ,prefix name) *kp-cases*) (lambda () ,form))))

(defun kp-view (&rest nodes) (pig:kit-view (pig:kit-container nodes)))
(defun kp-container-of (node) (pig:kit-container (list node)))


(defparameter *kp-branch-summary* (kp-nl "The branch explored **two** options:" "" "- keep the cache" "- drop the cache" "" "It chose `drop`."))
(defun kp-branch (&key expanded (output-pad 1))
  (let ((node (pig:kit-branch-summary *kp-branch-summary* :output-pad output-pad :id "b")))
    (pig:kit-branch-summary-set-expanded node expanded)
    (kp-view node)))
(defun kp-branch-empty ()
  (let ((node (pig:kit-branch-summary ""))) (pig:kit-branch-summary-set-expanded node t) (pig:kit-view node)))
(let ((prefix "pi-coding-agent/BranchSummaryMessageComponent/"))
  (kp-cases prefix ("collapsed" "click toggles" "right click ignored") (kp-branch)) 
  (setf (gethash (concatenate 'string prefix "collapsed") *kp-cases*) (lambda () (kp-branch)))
  (setf (gethash (concatenate 'string prefix "expanded") *kp-cases*) (lambda () (kp-branch :expanded t)))
  (setf (gethash (concatenate 'string prefix "outputPad 0 collapsed") *kp-cases*) (lambda () (kp-branch :output-pad 0)))
  (setf (gethash (concatenate 'string prefix "outputPad 0 expanded") *kp-cases*) (lambda () (kp-branch :expanded t :output-pad 0)))
  (setf (gethash (concatenate 'string prefix "empty summary expanded") *kp-cases*) #'kp-branch-empty))

(defparameter *kp-compaction-summary*
  (kp-nl "Compacted the first **forty** turns:" "" "1. the build setup" "2. the failing test" "" "The fix is pending."))
(defun kp-compaction (&key (tokens 123456) expanded (output-pad 1))
  (let ((node (pig:kit-compaction-summary *kp-compaction-summary* tokens :output-pad output-pad :id "c")))
    (pig:kit-compaction-summary-set-expanded node expanded)
    (kp-view node)))
(let ((prefix "pi-coding-agent/CompactionSummaryMessageComponent/"))
  (kp-cases prefix ("collapsed" "click toggles" "right click ignored") (kp-compaction))
  (setf (gethash (concatenate 'string prefix "expanded") *kp-cases*) (lambda () (kp-compaction :expanded t)))
  (setf (gethash (concatenate 'string prefix "outputPad 0 collapsed") *kp-cases*) (lambda () (kp-compaction :output-pad 0)))
  (setf (gethash (concatenate 'string prefix "outputPad 0 expanded") *kp-cases*) (lambda () (kp-compaction :expanded t :output-pad 0)))
  (setf (gethash (concatenate 'string prefix "tokens 999") *kp-cases*) (lambda () (kp-compaction :tokens 999)))
  (setf (gethash (concatenate 'string prefix "tokens 1234567 expanded") *kp-cases*) (lambda () (kp-compaction :tokens 1234567 :expanded t)))
  (setf (gethash (concatenate 'string prefix "empty summary tokens 0 expanded") *kp-cases*)
        (lambda () (let ((node (pig:kit-compaction-summary "" 0))) (pig:kit-compaction-summary-set-expanded node t) (pig:kit-view node)))))

(defparameter *kp-skill-content* (kp-nl "Review the diff for **bugs** first." "" "- correctness" "- naming" "" "Report findings as a list."))
(defun kp-skill (&key expanded (output-pad 1))
  (let ((node (pig:kit-skill-invocation "code-review" *kp-skill-content* :output-pad output-pad :id "s")))
    (pig:kit-skill-invocation-set-expanded node expanded)
    (kp-view node)))
(let ((prefix "pi-coding-agent/SkillInvocationMessageComponent/"))
  (kp-cases prefix ("collapsed" "click toggles" "right click ignored") (kp-skill))
  (setf (gethash (concatenate 'string prefix "expanded") *kp-cases*) (lambda () (kp-skill :expanded t)))
  (setf (gethash (concatenate 'string prefix "outputPad 0 collapsed") *kp-cases*) (lambda () (kp-skill :output-pad 0)))
  (setf (gethash (concatenate 'string prefix "outputPad 0 expanded") *kp-cases*) (lambda () (kp-skill :expanded t :output-pad 0)))
  (setf (gethash (concatenate 'string prefix "empty skill expanded") *kp-cases*)
        (lambda () (let ((node (pig:kit-skill-invocation "" ""))) (pig:kit-skill-invocation-set-expanded node t) (pig:kit-view node)))))

(defparameter *kp-custom-text* (kp-nl "Build **passed** in 12s" "" "- 214 tests" "- 0 failures"))
(defun kp-custom (&key (text *kp-custom-text*) expanded (output-pad 1))
  (let ((node (pig:kit-custom-message "ci-status" text :output-pad output-pad :id "m")))
    (pig:kit-custom-message-set-expanded node expanded)
    (kp-view node)))
(let ((prefix "pi-coding-agent/CustomMessageComponent/"))
  (kp-case (concatenate 'string prefix "default") (kp-custom))
  (kp-case (concatenate 'string prefix "outputPad 0") (kp-custom :output-pad 0))
  (kp-case (concatenate 'string prefix "expanded changes nothing") (kp-custom :expanded t))
  (kp-case (concatenate 'string prefix "empty text") (kp-custom :text ""))
  (kp-case (concatenate 'string prefix "no id") (pig:kit-view (pig:kit-custom-message "note" "plain"))))

(defun kp-cancellable (&key (message "Working") spinner-color message-color indicator frame)
  (pig:kit-view (pig:kit-container (list (pig:kit-cancellable-loader "c" message :spinner-color spinner-color :message-color message-color
                                                                                 :indicator indicator :frame frame)))
                :focus "c"))
(defun kp-bordered (&key (message "Fetching models") (cancellable t) frame)
  (pig:kit-view (pig:kit-container (list (pig:kit-bordered-loader "b" message :cancellable cancellable :frame frame))) :focus "b"))
(let ((prefix "pi-tui/CancellableLoader/"))
  (kp-cases prefix ("default" "escape aborts" "escape twice aborts twice" "other keys are ignored") (kp-cancellable))
  (kp-case (concatenate 'string prefix "default message") (kp-cancellable :message "Loading..."))
  (kp-case (concatenate 'string prefix "colors") (kp-cancellable :spinner-color "accent" :message-color "muted"))
  (kp-case (concatenate 'string prefix "frame 3") (kp-cancellable :frame 3))
  (kp-case (concatenate 'string prefix "indicator frames") (kp-cancellable :indicator (pig:kit-loader-indicator :frames '("*" "+" "x") :interval-ms 50) :frame 1))
  (kp-case (concatenate 'string prefix "hidden indicator") (kp-cancellable :indicator (pig:kit-loader-indicator :frames (vector))))
  (kp-case (concatenate 'string prefix "wide message")
           (kp-cancellable :message "A very long message that wraps around the narrow widths of the table" :spinner-color "accent")))
(let ((prefix "pi-coding-agent/BorderedLoader/"))
  (kp-cases prefix ("cancellable" "escape aborts" "escape twice aborts twice" "other keys are ignored") (kp-bordered))
  (kp-cases prefix ("not cancellable" "escape ignored when not cancellable") (kp-bordered :cancellable nil))
  (kp-case (concatenate 'string prefix "default message") (kp-bordered :message "Loading..."))
  (kp-case (concatenate 'string prefix "frame 4") (kp-bordered :frame 4))
  (kp-case (concatenate 'string prefix "wide message") (kp-bordered :message "Fetching the models of every configured provider, one after another")))

(defun kp-input (&key (focus t) (value "") prompt (placeholder "") placeholder-color)
  (pig:kit-view (pig:kit-container (list (pig:kit-input "i" value :prompt prompt :placeholder placeholder :placeholder-color placeholder-color)))
                :focus (and focus "i")))
(let ((prefix "pi-tui/Input/"))
  (kp-case (concatenate 'string prefix "empty") (kp-input :focus nil))
  (kp-cases prefix ("empty focused" "typing" "left then insert" "backspace and delete" "home and end" "kill word and yank" "kill to end" "paste scrolls" "submit")
            (kp-input))
  (kp-case (concatenate 'string prefix "placeholder") (kp-input :focus nil :placeholder "type here"))
  (kp-case (concatenate 'string prefix "placeholder focused colored") (kp-input :placeholder "type here" :placeholder-color "dim"))
  (kp-case (concatenate 'string prefix "custom prompt") (kp-input :prompt "$ " :value "ls -la"))
  (kp-case (concatenate 'string prefix "value") (kp-input :focus nil :value "hello world"))
  (kp-cases prefix ("value focused" "click moves the cursor" "right click ignored") (kp-input :value "hello world"))
  (kp-case (concatenate 'string prefix "wide characters")
           (kp-input :value (coerce (list (code-char #x6F22) (code-char #x5B57) #\Space #\a #\n #\d #\Space (code-char #x1F600) #\Space #\t #\e #\x #\t) 'string)))
  (kp-case (concatenate 'string prefix "submit newline key") (kp-input :value "preset"))
  (kp-case (concatenate 'string prefix "escape") (kp-input :value "keep")))

(defparameter *kp-editor-commands*
  (pig:kit-autocomplete (list (pig:kit-slash-command "help" "Show help") (pig:kit-slash-command "model" "Switch the model" "<name>")
                              (pig:kit-slash-command "quit" "Exit") (pig:kit-slash-command "session" "Session info")
                              (pig:kit-slash-command "settings" "Open settings") (pig:kit-slash-command "share")
                              (pig:kit-slash-command "skills" "List skills") (pig:kit-slash-command "compact" "Compact the context"))))

(defun kp-editor (&key (focus t) (value "") (padding-x 0) (max-visible 0) border-color disable-submit history autocomplete)
  (pig:kit-view (pig:kit-container (list (pig:kit-editor "e" value :padding-x padding-x :autocomplete-max-visible max-visible :border-color border-color
                                                                   :disable-submit disable-submit :history history :autocomplete autocomplete)))
                :focus (and focus "e")))

(defun kp-numbered (n) (format nil "~{~A~^~%~}" (loop for i from 1 to n collect (format nil "line ~D" i))))

(let ((prefix "pi-tui/Editor/") (twelve (kp-numbered 12)) (fox "The quick brown fox jumps over the lazy dog and keeps running"))
  (kp-case (concatenate 'string prefix "empty") (kp-editor :focus nil))
  (kp-case (concatenate 'string prefix "empty focused") (kp-editor))
  (kp-case (concatenate 'string prefix "value") (kp-editor :focus nil :value "hello world"))
  (kp-case (concatenate 'string prefix "multiline value focused") (kp-editor :value (kp-nl "one" "two" "three")))
  (kp-case (concatenate 'string prefix "wrapped line") (kp-editor :value (concatenate 'string fox " through the field")))
  (kp-case (concatenate 'string prefix "wide characters")
           (kp-editor :value (coerce (list (code-char #x6F22) (code-char #x5B57) #\Space #\a #\n #\d #\Space (code-char #x1F600) #\Space #\t #\e #\x #\t) 'string)))
  (kp-cases prefix ("typing" "newline" "backspace and delete" "kill word and yank" "undo" "submit" "paste" "paste large") (kp-editor))
  (kp-case (concatenate 'string prefix "cursor movement") (kp-editor :value (kp-nl "one" "two" "three")))
  (kp-case (concatenate 'string prefix "submit multiline") (kp-editor :value (kp-nl "one" "two")))
  (kp-case (concatenate 'string prefix "submit disabled") (kp-editor :value "keep" :disable-submit t))
  (kp-case (concatenate 'string prefix "padding") (kp-editor :value "padded text" :padding-x 2))
  (kp-case (concatenate 'string prefix "padding wrapped") (kp-editor :value fox :padding-x 3))
  (kp-case (concatenate 'string prefix "border color") (kp-editor :value "colored" :border-color "accent"))
  (kp-case (concatenate 'string prefix "history") (kp-editor :history '("first" "second" "third")))
  (kp-case (concatenate 'string prefix "history then edit") (kp-editor :history '("first" "second")))
  (kp-cases prefix ("scrolls past the visible lines" "scroll indicator below" "more rows show more lines" "few rows show five lines")
            (kp-editor :value twelve))
  (kp-case (concatenate 'string prefix "page up and down") (kp-editor :value (kp-numbered 30)))
  (kp-case (concatenate 'string prefix "click moves the cursor") (kp-editor :value (kp-nl "hello" "world")))
  (kp-case (concatenate 'string prefix "click on the border") (kp-editor :value (kp-nl "hello" "world")))
  (kp-case (concatenate 'string prefix "right click ignored") (kp-editor :value "hello"))
  (kp-cases prefix ("slash opens the menu" "slash narrows" "slash no match" "slash tab accepts" "slash arrow then tab" "slash enter accepts"
                    "slash escape closes" "slash click selects" "text after the slash opens nothing")
            (kp-editor :autocomplete *kp-editor-commands*))
  (kp-case (concatenate 'string prefix "slash max visible 3") (kp-editor :max-visible 3 :autocomplete *kp-editor-commands*))
  (kp-case (concatenate 'string prefix "slash with padding") (kp-editor :padding-x 2 :autocomplete *kp-editor-commands*))
  (kp-case (concatenate 'string prefix "autocomplete menu does not follow disabled submit") (kp-editor :disable-submit t :autocomplete *kp-editor-commands*)))

(defun kp-scroll-child (kind)
  (cond ((equal kind "md") (pig:kit-markdown (kp-nl "# Title" "" "- one" "- two with a long line that wraps at narrow widths" "" "`code`") 1 0))
        ((equal kind "region") (pig:kit-text (kp-nl "Region content" "second row") 1 0))
        ((equal kind "empty") (pig:kit-container))
        ((equal kind "branch") (pig:kit-branch-summary *kp-branch-summary* :id "b"))
        (t (pig:kit-text (kp-nl "Alpha line of the log" "Beta line of the log" "Gamma line of the log that is long enough to wrap at the narrow widths" "Delta") 1 0))))

(defun kp-scroll (&rest options) (kp-view (apply #'pig:kit-scroll-view "s" (kp-scroll-child "text") options)))
(defun kp-region (child &rest options) (kp-view (apply #'pig:kit-mouse-region "m" (kp-scroll-child child) options)))
(let ((prefix "pi-tui/ScrollView/"))
  (kp-case (concatenate 'string prefix "default") (kp-scroll))
  (kp-case (concatenate 'string prefix "scrollbar always") (kp-scroll :scrollbar "always"))
  (kp-case (concatenate 'string prefix "scrollbar auto") (kp-scroll :scrollbar "auto"))
  (kp-case (concatenate 'string prefix "scrollbar colors") (kp-scroll :scrollbar "always" :scrollbar-track-color "dim" :scrollbar-thumb-color "accent"))
  (kp-case (concatenate 'string prefix "follow end") (kp-scroll :follow "end"))
  (kp-case (concatenate 'string prefix "primary and contain") (kp-scroll :primary t :overscroll "contain"))
  (kp-case (concatenate 'string prefix "hide delay") (kp-scroll :scrollbar "auto" :scrollbar-hide-delay-ms 50))
  (kp-case (concatenate 'string prefix "scroll top clamps") (kp-scroll :scroll-top 3))
  (kp-case (concatenate 'string prefix "markdown child") (kp-view (pig:kit-scroll-view "s" (kp-scroll-child "md") :scrollbar "always")))
  (kp-case (concatenate 'string prefix "after other rows")
           (kp-view (pig:kit-text "Header" 1 0) (pig:kit-scroll-view "s" (kp-scroll-child "text") :scrollbar "always") (pig:kit-text "Footer" 1 0)))
  (kp-case (concatenate 'string prefix "empty child") (kp-view (pig:kit-scroll-view "s" (kp-scroll-child "empty") :scrollbar "always")))
  (kp-case (concatenate 'string prefix "wheel and click pass") (kp-view (pig:kit-scroll-view "s" (kp-scroll-child "text")) (pig:kit-text "after" 1 0))))
(let ((prefix "pi-tui/MouseRegion/"))
  (kp-case (concatenate 'string prefix "renders its child") (kp-region "region"))
  (kp-case (concatenate 'string prefix "every type by default") (kp-region "region"))
  (kp-case (concatenate 'string prefix "types filter") (kp-region "region" :types '("click")))
  (kp-case (concatenate 'string prefix "not handled") (kp-region "region" :handled :false))
  (kp-case (concatenate 'string prefix "capture") (kp-region "region" :capture t))
  (kp-case (concatenate 'string prefix "focus and render") (kp-region "region" :focus t :render :false))
  (kp-case (concatenate 'string prefix "local coordinates")
           (kp-view (pig:kit-text "Above the region" 1 0) (pig:kit-mouse-region "m" (kp-scroll-child "region")) (pig:kit-text "Below" 1 0)))
  (kp-case (concatenate 'string prefix "the child takes the event first") (kp-region "branch"))
  (kp-case (concatenate 'string prefix "right button") (kp-region "region")))

(defun kp-widget-component (ctx view)
  "kit-widget's component: takes the keys and mouse events the view leaves to the extension, and notifies each callback its nodes fire as
kit-widget-event=<type>:<value> (a change report is not one). It never closes itself."
  (pig:make-custom-component
   :view (lambda (width) (declare (ignore width)) view)
   :handle-input (lambda (data) (declare (ignore data)) nil)
   :handle-mouse (lambda (event) (declare (ignore event)) nil)
   :handle-view-event (lambda (event)
                        (let ((type (pig:kit-event-type event)) (mouse (pig:kit-event-mouse event)))
                          (cond ((and (equal type "mouse") mouse)
                                 (kp-notify ctx (format nil "kit-widget-event=mouse:~A/~A/~A/~A" (pig:jget mouse "type") (pig:jget mouse "button")
                                                        (pig:jget mouse "x") (pig:jget mouse "y"))))
                                ((not (equal type "change")) (kp-notify ctx (format nil "kit-widget-event=~A:~A" type (pig:kit-event-value event)))))
                          nil))))

;;; ---------------------------------------------------------------- images: kit-images, kit-kinds

(defun kp-crc32 (octets)
  (let ((crc #xFFFFFFFF))
    (loop for byte across octets
          do (setf crc (logxor crc byte))
             (dotimes (i 8) (setf crc (if (logbitp 0 crc) (logxor (ash crc -1) #xEDB88320) (ash crc -1)))))
    (logxor crc #xFFFFFFFF)))

(defun kp-u32 (value) (loop for shift in '(24 16 8 0) collect (ldb (byte 8 shift) value)))

(defun kp-adler32 (bytes)
  (let ((a 1) (b 0)) (dolist (byte bytes) (setf a (mod (+ a byte) 65521) b (mod (+ b a) 65521))) (logior (ash b 16) a)))

(defun kp-png (n)
  "Image n of kit-images: a 1x1 RGBA PNG whose pixel encodes n, so each n has its own bytes and ref."
  (flet ((chunk (kind data)
           (let ((body (append (map 'list #'char-code kind) data)))
             (append (kp-u32 (length data)) body (kp-u32 (kp-crc32 (coerce body '(vector (unsigned-byte 8)))))))))
    (let* ((pixel (list 0 (ldb (byte 8 0) n) (ldb (byte 8 8) n) #x5F #xFF))
           ;; zlib header, one stored deflate block of the five scanline bytes, then their Adler-32
           (zlib (append (list #x78 #x01 #x01 #x05 #x00 #xFA #xFF) pixel (kp-u32 (kp-adler32 pixel)))))
      (coerce (append '(#x89 #x50 #x4E #x47 #x0D #x0A #x1A #x0A)
                      (chunk "IHDR" (append (kp-u32 1) (kp-u32 1) (list 8 6 0 0 0)))
                      (chunk "IDAT" zlib)
                      (chunk "IEND" '()))
              '(simple-array (unsigned-byte 8) (*))))))

(defun kp-hex-octets (hex)
  (let ((octets (make-array (floor (length hex) 2) :element-type '(unsigned-byte 8))))
    (dotimes (i (length octets) octets) (setf (aref octets i) (parse-integer hex :start (* 2 i) :end (+ 2 (* 2 i)) :radix 16)))))

(defparameter *kp-kinds-png* "89504e470d0a1a0a0000000d49484452000000010000000108060000001f15c48900000010494441547801010500faff002a005fff026a01892888e8cd0000000049454e44ae426082"
  "kit-kinds' image, a 1x1 PNG every SDK fixture embeds byte for byte.")

;;; ---------------------------------------------------------------- the focused components

(defstruct (kp-list (:constructor kp-make-list ())) (items '("alpha" "beta" "gamma")) (selected 0) (disposed nil))

(defun kp-focused-component (state)
  (pig:make-custom-component
   :render (lambda (width)
             (cons (format nil "focused width=~D" width)
                   (loop for item in (kp-list-items state) for i from 0 collect (concatenate 'string (if (= i (kp-list-selected state)) "> " "  ") item))))
   :handle-input (lambda (data)
                   (let ((count (length (kp-list-items state))) (escape (string (code-char 27))))
                     (cond ((equal data (concatenate 'string escape "[A")) (setf (kp-list-selected state) (mod (1- (kp-list-selected state)) count)) nil)
                           ((equal data (concatenate 'string escape "[B")) (setf (kp-list-selected state) (mod (1+ (kp-list-selected state)) count)) nil)
                           ((equal data (concatenate 'string escape "[6~")) (setf (kp-list-selected state) (min (+ (kp-list-selected state) 2) (1- count))) nil)
                           ((member data (list (string #\Return) (string #\Newline)) :test #'equal)
                            (pig:make-custom-result :done t :value (nth (kp-list-selected state) (kp-list-items state))))
                           ((equal data escape) (pig:make-custom-result :done t))
                           (t nil))))
   :dispose (lambda () (setf (kp-list-disposed state) t))))

(defstruct (kp-timer (:constructor kp-make-timer ())) (frame 0) (lock (sb-thread:make-mutex)) invalidate (stop nil) thread (disposed nil) (detached nil))

(defun kp-timer-component (timer)
  (setf (kp-timer-thread timer)
        (sb-thread:make-thread (lambda ()
                                 (loop until (kp-timer-stop timer)
                                       do (sleep 0.02)
                                          (let ((invalidate (sb-thread:with-mutex ((kp-timer-lock timer))
                                                              (incf (kp-timer-frame timer))
                                                              (kp-timer-invalidate timer))))
                                            (when invalidate (funcall invalidate)))))
                               :name "kit-probe-timer"))
  (pig:make-custom-component
   :render (lambda (width) (list (format nil "timer frame=~D width=~D" (sb-thread:with-mutex ((kp-timer-lock timer)) (kp-timer-frame timer)) width)))
   :handle-input (lambda (data)
                   (and (equal data (string #\Return))
                        (pig:make-custom-result :done t :value (sb-thread:with-mutex ((kp-timer-lock timer)) (kp-timer-frame timer)))))
   :set-invalidate (lambda (callback)
                     (sb-thread:with-mutex ((kp-timer-lock timer)) (setf (kp-timer-invalidate timer) callback (kp-timer-detached timer) (null callback))))
   :dispose (lambda ()
              (setf (kp-timer-stop timer) t)
              (sb-thread:join-thread (kp-timer-thread timer) :default nil)
              (setf (kp-timer-disposed timer) t))))

(defun kp-width-probe-component ()
  (let ((width 0))
    (pig:make-custom-component
     :render (lambda (w) (setf width w) (list (format nil "overlay width=~D" w)))
     :handle-input (lambda (data) (and (equal data (string #\Return)) (pig:make-custom-result :done t :value width))))))

(defun kp-handle-probe-component ()
  (pig:make-custom-component
   :render (lambda (width) (declare (ignore width)) (list "overlay"))
   :handle-input (lambda (data) (if (equal data "q") (pig:make-custom-result :done t :value "closed") nil))))

(defun kp-mark (path line)
  (with-open-file (out path :direction :output :if-exists :append :if-does-not-exist :create :external-format :utf-8)
    (write-string line out)
    (write-char #\Newline out)))

;;; ---------------------------------------------------------------- the commands

(def-fixture "kit-views" (ext)
  (pig:command ext "kit-probe" "Exercise the component kit (D107)"
               (lambda (ctx args) (declare (ignore args))
                 (let ((result (pig:ctx-custom ctx (kp-kit-probe-component) :options (pig:obj "title" "Kit"))))
                   (kp-notify ctx (format nil "kit=~A" (or result ""))))))
  (pig:command ext "kit-widget" "Show a widget kind's Pi vector case (D107)"
               (lambda (ctx key)
                 (let ((build (gethash key *kp-cases*)))
                   (unless build (error "kit-widget: unknown case ~S" key))
                   (pig:ctx-custom ctx (kp-widget-component ctx (funcall build)) :options (pig:obj "title" "Kit")))))
  (pig:command ext "mouse-probe" "Exercise extension mouse input"
               (lambda (ctx args) (declare (ignore args))
                 (let ((result (pig:ctx-custom ctx (kp-mouse-component) :options (pig:obj "overlay" t))))
                   (kp-notify ctx (format nil "mouse=~A" (or result ""))))))
  (pig:command ext "viewport-probe" "Exercise a component that captures viewport input"
               (lambda (ctx args) (declare (ignore args))
                 (let ((result (pig:ctx-custom ctx (kp-viewport-component) :options (pig:obj "overlay" t))))
                   (kp-notify ctx (format nil "~A" (or result ""))))))
  ;; kit-surfaces shows the kit probe's tree on every other view surface: a pushed widget, the header, the footer and a tool result
  (pig:command ext "kit-surfaces" "Show the kit probe on every view surface (D107)"
               (lambda (ctx args) (declare (ignore args))
                 (pig:ctx-set-widget ctx "kit-probe" (kp-probe-view))
                 (pig:ctx-set-header-view ctx (kp-probe-view))
                 (pig:ctx-set-footer-view ctx (kp-probe-view))
                 (pig:tool ext "kit_view_tool" "Render its result as the kit probe" (pig:obj "type" "object") (lambda (ctx params) (declare (ignore ctx params)) "kit"))
                 (pig::tool-renderers ext "kit_view_tool" :result-view (lambda (ctx result options render width) (declare (ignore ctx result options render width)) (kp-probe-view)))))
  ;; the view renderers are the providers' API (pig:: until its exports are merged)
  (pig::message-view-renderer ext "kit-message" (lambda (ctx message options width) (declare (ignore ctx message options width)) (kp-probe-view)))
  (pig::entry-view-renderer ext "kit-entry" (lambda (ctx entry options width) (declare (ignore ctx entry options width)) (kp-probe-view)))
  ;; conformance-click-message and conformance-click-entry: a click-toggled card, the state of a message renderer's WeakMap in Pi. The event
  ;; handler counts the clicks of the "card" mouse region in the options' state; the view draws the state after the request's events.
  (flet ((click-handler (ctx subject options event)
           (declare (ignore ctx subject))
           (when (and (equal (pig:kit-event-type event) "mouse") (equal (pig:kit-event-node event) "card"))
             (let ((state (pig:message-render-options-state options)) (mouse (pig:kit-event-mouse event)))
               (setf (gethash "clicks" state) (1+ (gethash "clicks" state 0))
                     (gethash "last" state) (format nil "~D,~D" (pig:jget mouse "x") (pig:jget mouse "y"))))))
         (click-view (prefix text options)
           (let ((state (pig:message-render-options-state options)))
             (pig:kit-view (pig:kit-mouse-region
                            "card" (pig:kit-text (format nil "~A:~A:clicks=~D:last=~A:expanded=~A" prefix text (gethash "clicks" state 0)
                                                         (gethash "last" state "none") (kp-bool (eq (pig:jget options "expanded") t)))
                                                 0 0)
                            :types '("click"))))))
    (pig:message-view-renderer ext "conformance-click-message"
                               (lambda (ctx message options width)
                                 (declare (ignore ctx width))
                                 (click-view "clickcard" (let ((c (pig:jget message "content"))) (if (stringp c) c "")) options)))
    (pig:message-view-event ext "conformance-click-message" #'click-handler)
    (pig:entry-view-renderer ext "conformance-click-entry"
                             (lambda (ctx entry options width)
                               (declare (ignore ctx width))
                               (click-view "clickentry" (let ((d (pig:jget entry "data"))) (if (stringp d) d "")) options)))
    (pig:entry-view-event ext "conformance-click-entry" #'click-handler))
  (pig::tool-renderer ext (lambda (tool next)
                            (cond ((equal tool "kit_resolved_tool")
                                   (pig::make-tool-renderers :call-view (lambda (ctx args render width) (declare (ignore ctx args render width)) (kp-probe-view))
                                                             :result-view (lambda (ctx result options render width) (declare (ignore ctx result options render width)) (kp-probe-view))))
                                  ;; a view renderer that reads the view form of the context's last-component: the kind of what its previous run returned, and the run's own n
                                  ((equal tool "last_view_probe")
                                   (pig::make-tool-renderers
                                    :call-view (lambda (ctx args render width)
                                                 (declare (ignore ctx width))
                                                 (let ((last (pig:tool-render-context-last-component render)))
                                                   (pig:kit-view (pig:kit-container
                                                                  (list (pig:kit-text (format nil "lastview:~A:n=~A" (cond ((null last) "none") ((pig::kit-view-p last) "view") (t "lines")) (pig:jget args "n"))
                                                                                      0 0))))))))
                                  (t (funcall next)))))
  ;; kit-images: step "a<k>" shows image 0 and step "b<n>" image n (1 <= n <= 64), each with its step as the text
  (pig:command ext "kit-images" "Exercise the component kit's image transport (D107)"
               (lambda (ctx args)
                 (let* ((rest (subseq args (min 1 (length args))))
                        (n (if (and (plusp (length rest)) (every #'digit-char-p rest)) (parse-integer rest) 0))
                        (kind (if (plusp (length args)) (char args 0) #\?))
                        (image (cond ((and (char= kind #\a) (>= n 1)) 0) ((and (char= kind #\b) (<= 1 n 64)) n)
                                     (t (error "kit-images: unknown step ~S" args)))))
                   (pig:ctx-set-widget ctx "kit-img" (kp-view (pig:kit-image (kp-png image) "image/png") (pig:kit-text args 0 0)) (pig:obj)))))
  ;; kit-kinds: a box, a settings list, a loader, an image and a lines node whose list annotation goes out only while a frontend draws
  (pig:command ext "kit-kinds" "Show every other component kit kind (D107)"
               (lambda (ctx args) (declare (ignore args))
                 (let ((stack (pig:kit-vstack
                               (list (pig:kit-box 1 0 :bg "customMessageBg" :children (list (pig:kit-text "boxed" 0 0)))
                                     (pig:kit-settings-list "kit-settings"
                                                            (list (pig:kit-setting-item "theme" "Theme" "dark" :description "Color theme" :values '("dark" "light"))
                                                                  (pig:kit-setting-item "wrap" "Wrap" "on" :description "Wrap lines" :values '("on" "off")))
                                                            3)
                                     (pig:kit-loader "Working" :spinner-color "accent" :message-color "muted" :indicator (pig:kit-loader-indicator :frames '("*")))
                                     (pig:kit-image (kp-hex-octets *kp-kinds-png*) "image/png")
                                     (pig:kit-lines '("track one" "track two")
                                                    :list (pig:kit-list (list (pig:kit-list-item "track one" "A") (pig:kit-list-item "track two" "B")) 1)))
                               :gap 1)))
                   (pig:ctx-set-widget ctx "kit-kinds" (pig:kit-view stack :theme (pig:obj "accent" "#d75f00")) (pig:obj)))))
  ;; kit-conversation: every conversation kind; "next" updates the nodes with an id as a Pi author updates kept components
  (pig:command ext "kit-conversation" "Show Pi's conversation components (D107)"
               (lambda (ctx args)
                 (let* ((next (equal args "next"))
                        (user (pig:kit-user-message (kp-nl "Fix **the** kit build" "" "- one" "- two")))
                        (streaming (pig:kit-assistant-message nil :id "kit-a1"))
                        (reply (concatenate 'string (kp-nl "Done. **Bold** reply" "" "1. a" "2. b") (if next (kp-nl "" "" "Then more kit.") "")))
                        (failed (pig:kit-assistant-message
                                 (pig:kit-message (list (pig:kit-thinking-block "secret") (pig:kit-text-block "Visible kit")) :stop-reason "error" :error-message "kit-boom-7"))))
                   (pig:kit-assistant-message-update-content
                    streaming (pig:kit-message (list (pig:kit-thinking-block "Reading the *kit* file") (pig:kit-text-block reply))) (not next))
                   (pig:kit-assistant-message-set-hide-thinking-block failed t)
                   (pig:kit-assistant-message-set-hidden-thinking-label failed "Pondering kit...")
                   (pig:kit-assistant-message-set-output-pad failed 0)
                   (flet ((card (id name call-id card-args &rest options)
                            (let ((tool (apply #'pig:kit-tool-execution name call-id card-args "/work/kit" :id id options)))
                              (pig:kit-tool-execution-set-args-complete tool)
                              (pig:kit-tool-execution-mark-execution-started tool)
                              tool)))
                     (let* ((ls (card "kit-t1" "ls" "call-1" (pig:obj "path" "src")))
                            (grep (card "kit-t2" "grep" "call-2" (pig:obj "pattern" "TODO")))
                            (read (card "" "read" "call-3" (pig:obj "path" "missing.txt")))
                            (custom (card "" "kit_tool" "call-4" (pig:obj "q" "x") :tool-definition pig:*kit-tool-definition-empty*))
                            (exited (pig:kit-bash-execution "ls -la" nil :id "kit-b1"))
                            (seq (pig:kit-bash-execution "seq 25" t))
                            (diff (pig:kit-diff (kp-nl " 1 keep" "-2 old kit line" "+2 new kit line" " 3 tail") :file-path "kit.go")))
                       (pig:kit-tool-execution-update-result ls (pig:kit-tool-result (list (pig:kit-text-content (kp-nl "a.go" "b.go")))) nil)
                       (if next
                           (pig:kit-tool-execution-update-result grep (pig:kit-tool-result (list (pig:kit-text-content (kp-nl "x.go:1: TODO kit" "y.go:2: TODO kit")))) nil)
                           (pig:kit-tool-execution-update-result grep (pig:kit-tool-result (list (pig:kit-text-content "x.go:1: TODO kit"))) t))
                       (pig:kit-tool-execution-update-result read (pig:kit-tool-result (list (pig:kit-text-content "ENOENT: kit")) :is-error t) nil)
                       (pig:kit-tool-execution-set-expanded read t)
                       (pig:kit-tool-execution-update-result custom (pig:kit-tool-result (list (pig:kit-text-content "answer 42"))) nil)
                       (pig:kit-bash-execution-append-output exited (format nil "a.txt~%"))
                       (pig:kit-bash-execution-append-output exited "b.txt")
                       (pig:kit-bash-execution-set-complete exited 2)
                       (pig:kit-bash-execution-set-expanded exited next)
                       (pig:kit-bash-execution-append-output seq (format nil "~{~D~^~%~}" (loop for i from 1 to 25 collect i)))
                       (pig:kit-bash-execution-set-complete seq 0)
                       (pig:ctx-set-widget ctx "kit-conversation"
                                           (pig:kit-view (pig:kit-container (list user streaming failed ls grep read custom exited seq diff)))
                                           (pig:obj)))))))
  ;; kit-output-pad: a bash tool card and a bash command with Pi 1.1.0's outputPad 3 and a final result's durationMs 1234; "next" calls
  ;; setOutputPad(2) on both and gives the result durationMs 61500
  (pig:command ext "kit-output-pad" "Show Pi's outputPad and durationMs (D107)"
               (lambda (ctx args)
                 (let* ((next (equal args "next"))
                        (tool (pig:kit-tool-execution "bash" "call-p1" (pig:obj "command" "echo kit") "/work/kit" :output-pad 3 :id "kit-p1"))
                        (command (pig:kit-bash-execution "echo pad" nil :output-pad 3 :id "kit-p2")))
                   (pig:kit-tool-execution-set-args-complete tool)
                   (pig:kit-tool-execution-mark-execution-started tool)
                   (pig:kit-tool-execution-update-result
                    tool (pig:kit-tool-result (list (pig:kit-text-content "kit out")) :duration-ms (if next 61500 1234)) nil)
                   (pig:kit-bash-execution-append-output command "padded")
                   (pig:kit-bash-execution-set-complete command 0)
                   (when next
                     (pig:kit-tool-execution-set-output-pad tool 2)
                     (pig:kit-bash-execution-set-output-pad command 2))
                   (pig:ctx-set-widget ctx "kit-output-pad" (kp-view tool command) (pig:obj))))))

(def-fixture "focused-components" (ext)
  (pig:command ext "focused-probe" "Exercise focused subprocess UI"
               (lambda (ctx args) (declare (ignore args))
                 (let* ((state (kp-make-list))
                        (selected (pig:ctx-custom ctx (kp-focused-component state)
                                                  :options (pig:obj "title" "Focused" "widthFraction" 0.5d0 "heightFraction" 0.5d0))))
                   (kp-notify ctx (format nil "focused=~A disposed=~A" (or selected "") (kp-bool (kp-list-disposed state)))))))
  (pig:command ext "timer-focused-probe" "Exercise timer-driven focused UI"
               (lambda (ctx args) (declare (ignore args))
                 (let* ((timer (kp-make-timer)) (frame (pig:ctx-custom ctx (kp-timer-component timer) :options (pig:obj "title" "Timer"))))
                   (kp-notify ctx (format nil "timer=~A disposed=~A detached=~A" (or frame 0) (kp-bool (kp-timer-disposed timer)) (kp-bool (kp-timer-detached timer)))))))
  (pig:command ext "overlay-width-probe" "Report the width overlay components render at"
               (lambda (ctx args) (declare (ignore args))
                 (let ((default-width (pig:ctx-custom ctx (kp-width-probe-component) :options (pig:obj "overlay" t)))
                       (percent-width (pig:ctx-custom ctx (kp-width-probe-component)
                                                      :options (pig:obj "overlay" t "overlayOptions" (pig:obj "width" "50%")))))
                   (kp-notify ctx (format nil "overlay-width default=~A percent=~A" default-width percent-width)))))
  (pig:command ext "blocked-custom-probe" "Open inline custom components with and without the blocked option"
               (lambda (ctx args) (declare (ignore args))
                 (pig:ctx-custom ctx (kp-width-probe-component)
                                 :options (pig:obj "blocked" (pig:obj "kind" "permission" "message" "Approve?")))
                 (pig:ctx-custom ctx (kp-width-probe-component))
                 (kp-notify ctx "blocked-custom done")))
  (pig:command ext "overlay-handle-probe" "Exercise the overlay handle ui.custom hands to on_handle"
               (lambda (ctx args) (declare (ignore args))
                 (let ((failures '()))
                   (flet ((expect (label actual expected)
                            (unless (equalp actual expected) (push (format nil "~A: ~S != ~S" label actual expected) failures))))
                     (pig:ctx-custom ctx (kp-handle-probe-component)
                                     :options (pig:obj "overlay" t)
                                     :on-handle (lambda (handle)
                                                  (expect "initial focused" (pig:overlay-focused-p handle) t)
                                                  (expect "initial hidden" (pig:overlay-hidden-p handle) nil)
                                                  (expect "initial bounds"
                                                          (let ((b (pig:overlay-get-bounds handle)))
                                                            (and b (list (pig:jget b "row") (pig:jget b "col") (pig:jget b "width") (pig:jget b "height"))))
                                                          (list 3 4 20 5))
                                                  (pig:overlay-focus handle)
                                                  (expect "focus" (pig:overlay-focused-p handle) t)
                                                  (pig:overlay-set-hidden handle t)
                                                  (expect "hidden" (list (pig:overlay-hidden-p handle) (pig:overlay-focused-p handle) (pig:overlay-get-bounds handle)) (list t nil nil))
                                                  (pig:overlay-set-hidden handle nil)
                                                  (expect "shown" (list (pig:overlay-hidden-p handle) (pig:overlay-focused-p handle)) (list nil nil))
                                                  (pig:overlay-focus handle)
                                                  (expect "refocus" (pig:overlay-focused-p handle) t)
                                                  (pig:overlay-unfocus handle)
                                                  (expect "unfocus" (pig:overlay-focused-p handle) nil)
                                                  (pig:overlay-unfocus handle :target nil))))
                   (when failures (error "~{~A~^; ~}" (reverse failures)))
                   (kp-notify ctx "overlay-handle=ok"))))
  (pig:command ext "overlay-invalidate-probe" "Count the host's invalidations of a mounted overlay and draw the theme"
               (lambda (ctx args)
                 (flet ((row () (let ((theme (pig:ctx-theme ctx))) (format nil "~A|~A" (pig:theme-name theme) (pig:theme-fg theme "accent" "x"))))
                        (input (data) (if (equal data "q") (pig:make-custom-result :done t :value "closed") nil)))
                   (let* ((invalidations 0) (kept (row))
                          (component (if (equal (string-trim " " (if (stringp args) args "")) "plain")
                                         ;; no :invalidate: the host's invalidation renders it again all the same
                                         (pig:make-custom-component
                                          :render (lambda (width) (declare (ignore width)) (list (format nil "plain live=~A" (row))))
                                          :handle-input #'input)
                                         (pig:make-custom-component
                                          :render (lambda (width) (declare (ignore width))
                                                    (list (format nil "inv=~D kept=~A live=~A" invalidations kept (pig:theme-name (pig:ctx-theme ctx)))))
                                          :handle-input #'input
                                          :invalidate (lambda () (incf invalidations) (setf kept (row)))))))
                     (pig:ctx-custom ctx component :options (pig:obj "overlay" t))
                     (kp-notify ctx "overlay-invalidate=ok")))))
  (pig:command ext "theme-read-probe" "Report the theme the extension holds, with no component mounted"
               (lambda (ctx args) (declare (ignore args))
                 (let ((theme (pig:ctx-theme ctx)))
                   (kp-notify ctx (format nil "theme-read=~A|~A" (pig:theme-name theme) (pig:theme-fg theme "accent" "x"))))))
  (pig:command ext "overlay-focus-probe" "Render the focus the host publishes to a mounted overlay"
               (lambda (ctx args) (declare (ignore args))
                 (let* ((handle nil)
                        (component (pig:make-custom-component
                                    :render (lambda (width) (declare (ignore width))
                                              (list (format nil "focused=~A" (if handle (kp-bool (pig:overlay-focused-p handle)) "pending"))))
                                    :handle-input (lambda (data) (if (equal data "q") (pig:make-custom-result :done t :value "closed") nil)))))
                   (pig:ctx-custom ctx component :options (pig:obj "overlay" t) :on-handle (lambda (h) (setf handle h)))
                   (kp-notify ctx "overlay-focus=ok")))))

(def-fixture "surfaces" (ext)
  (pig:command ext "headless-ui-probe" "Hand the SDK footer, header and custom component closures"
               (lambda (ctx path)
                 (pig:ctx-set-footer-renderer ctx (lambda (width) (declare (ignore width)) (kp-mark path "lisp:footer") (list "lisp:footer")))
                 (pig:ctx-set-header-renderer ctx (lambda (width) (declare (ignore width)) (kp-mark path "lisp:header") (list "lisp:header")))
                 (pig:ctx-custom ctx (pig:make-custom-component
                                      :render (lambda (width) (declare (ignore width)) (kp-mark path "lisp:custom") (list "lisp:custom"))
                                      :handle-input (lambda (data) (declare (ignore data)) (kp-mark path "lisp:custom") (pig:make-custom-result :done t :value "closed"))))
                 ;; options only, no component: Pi's RPC and no-op UIs resolve undefined, the TUI SDK reports it unsupported
                 (handler-case (kp-mark path (if (null (pig:ctx-custom ctx nil :options (pig:obj))) "lisp:custom-opaque:undefined" "lisp:custom-opaque:value"))
                   (error () (kp-mark path "lisp:custom-opaque:error")))))
  (pig:command ext "surface_footer" "Install a footer renderer"
               (lambda (ctx args) (declare (ignore args)) (pig:ctx-set-footer-renderer ctx (lambda (width) (list (format nil "footer@~D" width))))))
  (pig:command ext "surface_header" "Install a header renderer"
               (lambda (ctx args) (declare (ignore args)) (pig:ctx-set-header-renderer ctx (lambda (width) (list (format nil "header@~D" width))))))
  (pig:command ext "surface_static_footer" "Push static footer rows"
               (lambda (ctx args) (declare (ignore args)) (pig:ctx-set-footer ctx (list (format nil "static@~D" (pig:ctx-width ctx))))))
  (pig:command ext "surface_static_header" "Push static header rows"
               (lambda (ctx args) (declare (ignore args)) (pig:ctx-set-header ctx (list (format nil "static@~D" (pig:ctx-width ctx))))))
  (pig:command ext "surface_widget" "Set a string list widget wider than the pane"
               (lambda (ctx args) (declare (ignore args))
                 (pig:ctx-set-widget ctx "wide" (list (concatenate 'string (make-string 60 :initial-element #\A) " tail") "short"))))
  ;; D614 plugin UI mounts: the probe every SDK fixture implements the same way (conformance TestConformance_UIViewMounts).
  (flet ((view-text (ctx suffix)
           (let ((ab (pig:ctx-view-abilities ctx)))
             (pig:kit-view (pig:kit-text (format nil "fixture-view-~D~A" (if ab (pig:jref ab "limits" "intentArgBytes") 0) suffix) 0 0))))
         (code-of (thunk)
           (handler-case (progn (funcall thunk) "none")
             (pig:host-call-error (e) (pig:host-call-error-code e)))))
    (let ((cancelled-tag (list ""))
          (int (pig:view-shape "integer")))
      (pig:command ext "ui_view_abilities" "Report the replicated client abilities"
                   (lambda (ctx args) (declare (ignore args))
                     (let ((ab (pig:ctx-view-abilities ctx)))
                       (kp-notify ctx (if ab
                                          (format nil "view-abilities:mounts=~D,argbytes=~D,slots=~{~A~^+~},nodes=~{~A~^+~},markdown=~A"
                                                  (pig:jref ab "limits" "mounts") (pig:jref ab "limits" "intentArgBytes")
                                                  (coerce (pig:jget ab "slots") 'list) (coerce (pig:jget ab "nodes") 'list)
                                                  (kp-bool (eq (pig:jget ab "markdown") t)))
                                          "view-abilities:none")))))
      (pig:command ext "ui_view_probe" "Mount, update, hide, show, finish and remove a view"
                   (lambda (ctx args) (declare (ignore args))
                     (let* ((st (pig:ctx-mount-view ctx "probe" "panel" (view-text ctx "")))
                            (first (pig:view-status-mount st)))
                       (kp-notify ctx (format nil "view-mounted:epoch=~D,rev=~D,state=~A" (pig:view-ref-epoch first) (pig:view-ref-rev first) (pig:view-status-state st)))
                       (setf st (pig:ctx-update-view ctx first (view-text ctx "-2")))
                       (kp-notify ctx (format nil "view-updated:rev=~D" (pig:view-ref-rev (pig:view-status-mount st))))
                       (kp-notify ctx (format nil "view-stale:~A" (code-of (lambda () (pig:ctx-update-view ctx first (view-text ctx "-stale"))))))
                       (let ((cur (pig:view-status-mount st)))
                         (kp-notify ctx (format nil "view-hidden:~A" (pig:view-status-state (pig:ctx-hide-view ctx cur))))
                         (kp-notify ctx (format nil "view-shown:~A" (pig:view-status-state (pig:ctx-show-view ctx cur))))
                         (kp-notify ctx (format nil "view-finished:~A" (pig:view-status-state (pig:ctx-finish-view ctx cur))))
                         (kp-notify ctx (format nil "view-terminal:~A" (code-of (lambda () (pig:ctx-remove-view ctx cur))))))
                       (kp-notify ctx (format nil "view-final:~A" (pig:view-status-state (pig:ctx-view-state ctx "probe"))))
                       (setf st (pig:ctx-mount-view
                                 ctx "probe2" "panel" (view-text ctx "-live")
                                 :intents (list (pig:view-intent "fx.add" (pig:view-shape "object" :properties (list (cons "n" int)) :required '("n"))
                                                                 :answer (pig:view-shape "object" :properties (list (cons "sum" int)) :required '("sum")))
                                                (pig:view-intent "fx.wait" (pig:view-shape "object" :properties (list (cons "tag" (pig:view-shape "string"))) :required '("tag"))
                                                                 :cancellable t :timeout-ms 5000))))
                       (kp-notify ctx (format nil "view-second:epoch=~D,rev=~D" (pig:view-ref-epoch (pig:view-status-mount st)) (pig:view-ref-rev (pig:view-status-mount st)))))))
      ;; D614 amendment: the probe every SDK fixture implements the same way (conformance TestConformance_UIViewExtras).
      (labels ((x-claim (claims name)
                 (let ((claim (find name (coerce claims 'list) :key (lambda (c) (pig:jget c "name")) :test #'equal)))
                   (and claim (pig:jget claim "value"))))
               (x-picked (ab)
                 (let ((slot (find "panel" (coerce (pig:jget ab "slotClaims") 'list) :key (lambda (s) (pig:jget s "slot")) :test #'equal)))
                   (and slot (x-claim (pig:jget slot "claims") "display.colors"))))
               (x-list (id label) (pig:kit-view (pig:kit-select-list id (list (pig:kit-select-item id label)))))
               (x-base () (x-list "base" "fixture-x-base"))
               (x-rect (w h) (pig:obj "kind" "rect" "width" w "height" h))
               (x-rects (n) (coerce (loop repeat n collect (x-rect 1 1)) 'simple-vector))
               (x-scene (shapes) (pig:obj "profile" "scene" "shapes" shapes)))
        (pig:command ext "ui_view_extras" "Mount a view with alternatives, a drawing, a11y and focus, and probe the bounds"
                     (lambda (ctx args) (declare (ignore args))
                       (let* ((ab (pig:ctx-view-abilities ctx))
                              (claims (pig:jget ab "claims"))
                              (picked (x-picked ab))
                              (shapes (pig:jref ab "drawing" "shapes"))
                              (drawing (x-scene (x-rects (1- shapes)))))
                         (kp-notify ctx (format nil "view-x-claims:colors=~D,kind=~A,gestures=~{~A~^+~},hover=~A,panel.colors=~D,profiles=~{~A~^+~},shapes=~D,pixels=~D"
                                                (x-claim claims "display.colors") (x-claim claims "display.kind")
                                                (coerce (x-claim claims "input.gestures") 'list) (kp-bool (eq (x-claim claims "display.hover") t))
                                                picked (coerce (pig:jref ab "drawing" "profiles") 'list) shapes (pig:jref ab "drawing" "pixels")))
                         (setf (gethash "regions" drawing)
                               (vector (pig:obj "key" "r" "shape" (x-rect 4 4) "gestures" (vector "activate") "intent" "fx.tap" "name" "Tap")))
                         (let* ((st (pig:ctx-mount-view
                                     ctx "extras" "panel" (x-base)
                                     :intents (list (pig:view-intent "fx.tap" (pig:view-shape "object")))
                                     :alternatives (list (pig:view-alternative (pig:view-when (list (pig:view-clause "display.colors" "equals" picked)))
                                                                               (x-list "alt" (format nil "fixture-x-alt-~D" picked))
                                                                               :drawing drawing))
                                     :a11y (list (pig:obj "node" "alt" "name" (format nil "Extras ~D" picked) "role" "button" "checked" t
                                                          "relations" (vector (pig:obj "kind" "describedBy" "target" "base")))
                                                 (pig:obj "node" "base" "name" "Base"))
                                     :focus (list (pig:obj "node" "alt" "request" "mount" "restore" "previous"))))
                                (mount (pig:view-status-mount st)))
                           (kp-notify ctx (format nil "view-x-mounted:~A" (pig:view-status-state st)))
                           (flet ((attempt (label &rest content)
                                    (kp-notify ctx (format nil "view-x-~A:~A" label
                                                           (code-of (lambda () (apply #'pig:ctx-update-view ctx mount (x-base) content)))))))
                             (attempt "alts" :alternatives (loop repeat 9 collect (pig:view-alternative (pig:view-when (list (pig:view-clause "display.hover" "present")))
                                                                                                       (x-base))))
                             (let ((nested (x-rect 1 1)))
                               (dotimes (i 8) (setf nested (pig:obj "kind" "group" "children" (vector nested))))
                               (attempt "nest" :drawing (x-scene (vector nested))))
                             (attempt "points" :drawing (x-scene (vector (pig:obj "kind" "polyline"
                                                                                  "points" (coerce (loop for i below 1025 collect (pig:obj "x" i "y" i)) 'simple-vector)))))
                             (attempt "shapes" :drawing (x-scene (x-rects 4097)))
                             (attempt "a11y" :a11y (loop for i below 257 collect (pig:obj "node" (format nil "n~D" i))))
                             (attempt "over" :drawing (x-scene (x-rects (1+ shapes))))
                             (attempt "pixels"
                                      :drawing (pig:obj "profile" "pixels" "pixels" (pig:obj "width" 2 "height" 2 "asset" "img" "scaling" "nearest"))
                                      :assets (list (pig:view-asset "img" "image/png" 4 (make-string 64 :initial-element #\a) "inline"))))
                           (let ((old (pig:view-ref (pig:view-ref-key mount) (pig:view-ref-epoch mount) (+ 50 (pig:view-ref-rev mount))))
                                 (stale "none"))
                             (dotimes (i 3) (setf stale (code-of (lambda () (pig:ctx-update-view ctx old (x-base))))))
                             (kp-notify ctx (format nil "view-x-stale:~A" stale)))
                           (let ((notices (pig:ctx-view-diagnostics ctx "extras")))
                             (flet ((count-of (code)
                                      (loop for n in notices
                                            when (and (equal (pig:jget n "code") code) (equal (pig:jget n "path") "update")) sum (pig:jget n "count"))))
                               (kp-notify ctx (format nil "view-x-diag:invalid=~D,limit=~D,unsupported=~D,stale=~D"
                                                      (count-of "invalid") (count-of "limit") (count-of "unsupported") (count-of "stale")))))
                           (kp-notify ctx (format nil "view-x-bigresult:~A"
                                                  (code-of (lambda () (pig:ctx-finish-view ctx mount :result (make-string 65536 :initial-element #\r))))))))))
        (pig:command ext "ui_view_extras_end" "Read the notices of the extras mount and finish it with a result"
                     (lambda (ctx args) (declare (ignore args))
                       (let* ((picked (x-picked (pig:ctx-view-abilities ctx)))
                              (st (pig:ctx-view-state ctx "extras"))
                              (notices (pig:ctx-view-diagnostics ctx "extras")))
                         (kp-notify ctx (format nil "view-x-diag2:n=~D,last=~A" (length notices) (if notices (pig:jget (car (last notices)) "path") "")))
                         (let ((done (pig:ctx-finish-view ctx (pig:view-status-mount st) :result (pig:obj "picked" picked))))
                           (kp-notify ctx (format nil "view-x-finished:~A,picked=~D" (pig:view-status-state done) (pig:jget (pig:view-status-result done) "picked"))))
                         (let ((cur (pig:ctx-view-state ctx "extras")))
                           (kp-notify ctx (format nil "view-x-state:~A,picked=~D" (pig:view-status-state cur) (pig:jget (pig:view-status-result cur) "picked"))))))))
      ;; D614: a view an agent's tool shows. The command registers the tool; the tool mounts a titled panel, hides it, shows it and
      ;; updates it with a focus request, the same way in every SDK fixture (conformance TestConformance_UIViewToolShowsAndFocuses).
      ;; D521: bigint_tool registers a tool whose schema declares a bigint parameter and answers the kind of value it receives for it and the value.
      (pig:command ext "bigint_tool" "Register the tool that reports what a bigint argument arrives as"
                   (lambda (ctx args) (declare (ignore ctx args))
                     (pig:tool ext "bigint_arg" "Report the kind and value of n"
                               (pig:obj "type" "object" "required" (vector "n") "properties" (pig:obj "n" (pig:obj "type" "bigint")))
                               (lambda (ctx params) (declare (ignore ctx))
                                 (let ((value (pig:jget params "n")))
                                   (format nil "~A:~A" (if (stringp value) "string" "other") value))))))
      (pig:command ext "ui_view_tool" "Register the tool that shows a titled view"
                   (lambda (ctx args) (declare (ignore ctx args))
                     (pig:tool ext "view_show" "Mount, show and focus a titled view" (pig:obj "type" "object")
                               (lambda (ctx params)
                                 (if (equal (pig:jget params "op") "hide")
                                     (format nil "view-tool:~A" (pig:view-status-state (pig:ctx-hide-view ctx (pig:view-status-mount (pig:ctx-view-state ctx "shown")))))
                                     (let* ((title (pig:jget params "title"))
                                            (node (pig:jget params "node"))
                                            (view (pig:kit-view (pig:kit-select-list node (list (pig:kit-select-item node "fixture-shown")))))
                                            (intents (list (pig:view-intent "fx.reopen" (pig:view-shape "object") :when-hidden t)
                                                           (pig:view-intent "fx.tap" (pig:view-shape "object"))))
                                            (st (pig:ctx-mount-view ctx "shown" "panel" view :title title :intents intents :focus (list (pig:obj "node" node)))))
                                       (setf st (pig:ctx-hide-view ctx (pig:view-status-mount st)))
                                       (setf st (pig:ctx-show-view ctx (pig:view-status-mount st)))
                                       (setf st (pig:ctx-update-view ctx (pig:view-status-mount st) view :title (concatenate 'string title "/2") :intents intents
                                                                     :focus (list (pig:obj "node" node "request" "update"))))
                                       (format nil "view-tool:~A,rev=~D" (pig:view-status-state st) (pig:view-ref-rev (pig:view-status-mount st)))))))))
      ;; D614 grid node: the probe every SDK fixture implements the same way (conformance TestConformance_UIViewGrid). The argument is the
      ;; row's token: the cell values, the selection and one chip text are built from it. A cell is a plain table here, so this fixture can
      ;; also write a member the record lacks (the member line); the builders of the Go, Rust and Python kits cannot.
      (pig:command ext "ui_view_grid" "Mount a month as a grid and probe the bounds of the grid"
                   (lambda (ctx args)
                     (let* ((tok (string-trim " " args))
                            (cells (append
                                    (list (pig:kit-grid-cell :label "30" :dim t) (pig:kit-grid-cell :label "31" :dim t))
                                    (loop for d from 1 to 28
                                          collect (let ((value (format nil "~A-~D" tok d)) (label (format nil "~D" d)))
                                                    (case d
                                                      (1 (pig:kit-grid-cell :value value :label label :more 2
                                                                            :chips (list (pig:kit-grid-chip (format nil "~A standup" tok) "#3366cc")
                                                                                         (pig:kit-grid-chip "lunch" "accent"))))
                                                      (2 (pig:kit-grid-cell :value value :label label :mark "current" :color "#ff000080"))
                                                      (3 (pig:kit-grid-cell :value value :label label :chips (list (pig:kit-grid-chip "plain"))))
                                                      (28 (pig:kit-grid-cell :value value))
                                                      (t (pig:kit-grid-cell :value value :label label))))))))
                       (flet ((view-of (grid) (pig:kit-view (pig:kit-container (list (pig:kit-text (format nil "Month ~A" tok) 0 0) grid))))
                              (intents () (list (pig:view-intent "fx.pick" (pig:view-shape "object" :properties (list (cons "value" (pig:view-shape "string")))
                                                                                                    :required '("value"))
                                                                 :node-key "month")))
                              (fallback () (pig:kit-text (format nil "grid-fallback ~A" tok) 0 0)))
                         (let* ((st (pig:ctx-mount-view ctx "grid" "panel"
                                                        (view-of (pig:kit-grid "month" 7 cells :head '("Mo" "Tu" "We" "Th" "Fr" "Sa" "Su") :chip-rows 2
                                                                                               :selected (format nil "~A-2" tok)))
                                                        :intents (intents) :fallback (fallback)))
                                (mount (pig:view-status-mount st)))
                           (kp-notify ctx (format nil "view-g-mounted:~A" (pig:view-status-state st)))
                           (flet ((attempt (label columns cells)
                                    (kp-notify ctx (format nil "view-g-~A:~A" label
                                                           (code-of (lambda () (pig:ctx-update-view ctx mount (view-of (pig:kit-grid "month" columns cells))
                                                                                                    :intents (intents) :fallback (fallback))))))))
                             (attempt "cells" 7 (loop repeat 129 collect (pig:kit-grid-cell :label "x")))
                             (attempt "chips" 7 (list (pig:kit-grid-cell :value "a" :chips (loop repeat 9 collect (pig:kit-grid-chip "c")))))
                             (attempt "columns" 17 (list (pig:kit-grid-cell :value "a")))
                             (attempt "mark" 7 (list (pig:kit-grid-cell :value "a" :mark tok)))
                             (let ((odd (pig:kit-grid-cell :value "a")))
                               (setf (gethash "extra" odd) 1)
                               (attempt "member" 7 (list odd))))
                           (let ((cur (pig:ctx-view-state ctx "grid")))
                             (kp-notify ctx (format nil "view-g-state:~A,rev=~D" (pig:view-status-state cur) (pig:view-ref-rev (pig:view-status-mount cur))))))))))
      ;; D614 node ids: the probe every SDK fixture implements the same way (conformance TestConformance_UIViewNodeIds). The argument is
      ;; the row's token: the ids of the container, the text and the box are built from it.
      (pig:command ext "ui_view_node_ids" "Mount a view whose container, text and box carry an id"
                   (lambda (ctx args)
                     (let* ((tok (string-trim " " args))
                            (label (format nil "Label ~A" tok))
                            (root (pig:kit-container
                                   (list (pig:kit-text label 0 0 :bg "selectedBg" :id (format nil "~A-label" tok))
                                         (pig:kit-box 1 0 :bg "toolPendingBg" :id (format nil "~A-frame" tok)
                                                          :children (list (pig:kit-text "inside" 0 0))))
                                   :id (format nil "~A-root" tok)))
                            (st (pig:ctx-mount-view ctx "ids" "panel" (pig:kit-view root)
                                                    :a11y (list (pig:obj "node" (format nil "~A-label" tok) "name" label)))))
                       (kp-notify ctx (format nil "view-i-mounted:~A" (pig:view-status-state st))))))
      ;; D614 image id: the probe every SDK fixture implements the same way (conformance TestConformance_UIViewImageID). The argument is the row's token and a Kitty image id only that row uses: the image node carries both, its bytes are the token's.
      (pig:command ext "ui_view_image_id" "Mount a view whose image node carries a Kitty image id"
                   (lambda (ctx args)
                     (let* ((space (position #\Space args))
                            (tok (subseq args 0 space))
                            (number (parse-integer args :start (1+ space)))
                            (bytes (coerce (map 'list #'char-code (format nil "pig-image-~A" tok)) '(simple-array (unsigned-byte 8) (*))))
                            (image (pig:kit-image bytes "image/png" :id (format nil "~A-photo" tok) :image-id number :filename (format nil "~A.png" tok)))
                            (st (pig:ctx-mount-view ctx "image-id" "panel" (pig:kit-view image))))
                       (kp-notify ctx (format nil "view-m-mounted:~A" (pig:view-status-state st))))))
      ;; D614 bindings: the probe every SDK fixture implements the same way (conformance TestConformance_UIViewBindings).
      (labels ((b-intents ()
                 (list (pig:view-intent "fx.go" (pig:view-shape "object" :properties (list (cons "n" int)) :required '("n")))
                       (pig:view-intent "fx.tap" (pig:view-shape "object"))))
               (b-go (key n) (pig:obj "key" key "intent" "fx.go" "arg" (pig:obj "n" n)))
               (b-tap (key) (pig:obj "key" key "intent" "fx.tap")))
        (pig:command ext "ui_view_bindings" "Mount a view with bindings and probe what the host refuses"
                     (lambda (ctx args)
                       (let* ((space (position #\Space args))
                              (key (subseq args 0 space))
                              (n (parse-integer args :start (1+ space)))
                              (named (b-go key n)))
                         (setf (gethash "name" named) (format nil "Go ~D" n))
                         (let* ((st (pig:ctx-mount-view ctx "bind" "panel" (view-text ctx "-bind")
                                                        :intents (b-intents)
                                                        :bindings (list named (b-go "w" (1+ n)) (pig:obj "gesture" "swipe-left" "intent" "fx.tap"))))
                                (mount (pig:view-status-mount st)))
                           (kp-notify ctx (format nil "view-b-mounted:~A" (pig:view-status-state st)))
                           (flet ((attempt (label bindings)
                                    (kp-notify ctx (format nil "view-b-~A:~A" label
                                                           (code-of (lambda () (pig:ctx-update-view ctx mount (view-text ctx "-bind")
                                                                                                    :intents (b-intents) :bindings bindings)))))))
                             (attempt "many" (map 'list (lambda (c) (b-tap (string c))) "abcdefghijklmnopqrstuvwxyz0123456"))
                             (attempt "reserved" (list (b-tap "shift+tab")))
                             (attempt "order" (list (b-tap "ctrl+shift+p") (b-tap "shift+ctrl+p")))
                             (attempt "gesture" (list (pig:obj "gesture" "pinch" "intent" "fx.tap")))
                             (attempt "arg" (list (b-go "a" "x")))
                             (attempt "ok" (list (b-go "ctrl+shift+p" n) (b-go "shift+ctrl+q" (1+ n))))))))))
      ;; D614 slot dialog: the probe every SDK fixture implements the same way (conformance TestConformance_UIViewDialog).
      (pig:command ext "ui_view_dialog" "Mount a titled view with a bound key in the dialog slot, hide it and show it"
                   (lambda (ctx args)
                     (let* ((space (position #\Space args))
                            (key (subseq args 0 space))
                            (n (parse-integer args :start (1+ space)))
                            (st (handler-case
                                    (pig:ctx-mount-view ctx "dlg" "dialog" (view-text ctx "-dlg")
                                                        :title (format nil "Chart ~D" n)
                                                        :intents (list (pig:view-intent "fx.add" (pig:view-shape "object" :properties (list (cons "n" int)) :required '("n"))
                                                                                        :answer (pig:view-shape "object" :properties (list (cons "sum" int)) :required '("sum"))))
                                                        :bindings (list (pig:obj "key" key "intent" "fx.add" "arg" (pig:obj "n" n))))
                                  (pig:host-call-error (e) (pig:host-call-error-code e)))))
                       (if (stringp st)
                           (kp-notify ctx (format nil "view-d-mounted:~A" st))
                           (let ((mount (pig:view-status-mount st)))
                             (kp-notify ctx (format nil "view-d-mounted:~A" (pig:view-status-state st)))
                             (kp-notify ctx (format nil "view-d-hidden:~A" (pig:view-status-state (pig:ctx-hide-view ctx mount))))
                             (kp-notify ctx (format nil "view-d-shown:~A" (pig:view-status-state (pig:ctx-show-view ctx mount)))))))))
      ;; D614 dismiss: the probe every SDK fixture implements the same way (conformance TestConformance_UIViewDismiss).
      (flet ((tap () (pig:view-intent "fx.tap" (pig:view-shape "object")))
             (closing (name &rest members)
               (apply #'pig:view-intent name (or (getf members :arg) (pig:view-shape "object")) :dismiss t
                      (let ((rest (copy-list members))) (remf rest :arg) rest))))
        (pig:command ext "ui_view_dismiss" "Mount a dialog with a dismiss intent and probe what the host refuses"
                     (lambda (ctx args)
                       (let* ((st (pig:ctx-mount-view ctx "dismiss" "dialog" (view-text ctx "-dismiss")
                                                      :intents (list (tap) (closing args))))
                              (mount (pig:view-status-mount st)))
                         (kp-notify ctx (format nil "view-d-mounted:~A" (pig:view-status-state st)))
                         (flet ((attempt (label intents)
                                  (kp-notify ctx (format nil "view-d-~A:~A" label
                                                         (code-of (lambda () (pig:ctx-update-view ctx mount (view-text ctx "-dismiss")
                                                                                                  :intents intents)))))))
                           (attempt "two" (list (tap) (closing args) (closing (concatenate 'string args "2"))))
                           (attempt "node" (list (tap) (closing args :node-key "n")))
                           (attempt "arg" (list (tap) (closing args :arg (pig:view-shape "object" :properties (list (cons "value" (pig:view-shape "string")))
                                                                                                :required '("value")))))
                           (attempt "ok" (list (closing args) (tap))))))))
      (pig:command ext "ui_view_cancelled" "Report the intent whose handler was cancelled"
                   (lambda (ctx args) (declare (ignore args)) (kp-notify ctx (format nil "intent-cancelled:~A" (car cancelled-tag)))))
      (pig:command ext "ui_view_state" "Report the state of the second mount"
                   (lambda (ctx args) (declare (ignore args))
                     (let ((st (pig:ctx-view-state ctx "probe2")))
                       (kp-notify ctx (format nil "view-state:~A:~A" (pig:view-status-state st) (pig:view-status-reason st))))))
      (kp-register-view-rows ext)
      (pig:on-view-intent
       ext
       (lambda (ctx inv)
         (let ((intent (pig:jget inv "intent")) (arg (pig:jget inv "arg")))
           (cond ((and (> (length intent) 9) (string= "fx.close-" intent :end2 9))
                  ;; the user dismissed the dialog: the client raised the intent with {}, and its handler removes the mount
                  (cond ((and (hash-table-p arg) (zerop (hash-table-count arg)))
                         (pig:ctx-remove-view ctx (pig:view-ref (pig:jref inv "mount" "key") (pig:jref inv "mount" "epoch") (pig:jget inv "rev")))
                         (pig:view-intent-result "done"))
                        (t (pig:view-intent-result "refused" :error "dismiss takes {}"))))
                 ((member intent '("fx.redraw" "fx.foreign") :test #'equal) (kp-redraw-intent ctx inv))
                 ((equal intent "fx.reopen")
                  ;; the user opened the hidden view: its handler shows it
                  (pig:ctx-show-view ctx (pig:view-ref (pig:jref inv "mount" "key") (pig:jref inv "mount" "epoch") (pig:jget inv "rev")))
                  (pig:view-intent-result "done"))
                 ((equal intent "fx.pick")
                  (pig:view-intent-result "done" :answer (pig:obj "node" (pig:jget inv "nodeKey") "picked" (pig:jget arg "value"))))
                 ((equal intent "fx.add")
                  (let ((ab (pig:ctx-view-abilities ctx)))
                    (pig:view-intent-result "done" :answer (pig:obj "sum" (+ (* 2 (pig:jget arg "n")) (if ab (pig:jref ab "limits" "intentArgBytes") 0))))))
                 ((equal intent "fx.wait")
                  ;; the host cancels as soon as it reads this notice, so the cancel can land before the call returns, and a
                  ;; Lisp host call of a cancelled request signals: the notice's outcome is not what this handler reports
                  (ignore-errors (kp-notify ctx (format nil "intent-started:~A" (pig:jget arg "tag"))))
                  (pig:ctx-wait-cancelled ctx)
                  ;; a call made after the cancel is cancelled with its request, so the tag is kept for ui_view_cancelled
                  (setf (car cancelled-tag) (pig:jget arg "tag"))
                  (pig:view-intent-result "done" :answer (pig:obj)))
                 (t (pig:view-intent-result "refused" :error "unknown intent"))))))))
  (pig:command ext "report_geometry" "Report the terminal geometry"
               (lambda (ctx args) (declare (ignore args)) (kp-notify ctx (format nil "geometry:~Dx~D" (pig:ctx-width ctx) (pig:ctx-height ctx)))))
  (pig:own-thinking-runs ext t)
  (pig:command ext "thinking_runs_off" "Stop owning the thinking runs"
               (lambda (ctx args) (declare (ignore args)) (pig:ctx-own-thinking-runs ctx nil)))
  (pig:command ext "thinking_runs_on" "Own the thinking runs"
               (lambda (ctx args) (declare (ignore args)) (pig:ctx-own-thinking-runs ctx t)))
  (pig:command ext "ui_forward_probe" "Call the working, paste, editor-text and tools-expanded UI members"
               (lambda (ctx args) (declare (ignore args))
                 (pig:ctx-set-working-message ctx "Thinking hard")
                 (pig:ctx-set-working-message ctx "")
                 (pig:ctx-set-working-message ctx)
                 (pig:ctx-set-hidden-thinking-label ctx "Pondering")
                 (pig:ctx-set-hidden-thinking-label ctx "")
                 (pig:ctx-set-hidden-thinking-label ctx)
                 (pig:ctx-set-working-visible ctx nil)
                 (pig:ctx-set-working-indicator ctx (pig:obj "frames" (vector "-" "+") "intervalMs" 120))
                 (pig::ctx-paste-to-editor ctx (concatenate 'string "pasted " (string (code-char #x1F600))))
                 (pig::ctx-set-editor-text ctx "draft")
                 (pig:ctx-set-tools-expanded ctx t))))
