(in-package "lisp-sdk-fixture")
;;; the probe commands, tools and event handlers of lane U1: the component kit, ui.custom and overlays, widgets, footer and header, the
;;; working indicator (see def-fixture in fixture.lisp). Each is the Python fixture's probe of the same name (python-sdk-fixture/main.py).

(defun kp-nl (&rest lines) (format nil "~{~A~^~%~}" lines))

(defun kp-notify (ctx text) (pig:ctx-notify ctx text "info"))

(defun kp-bool (value) (if value "true" "false"))

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
  ;; kit-surfaces shows the kit probe's tree on every other view surface: a pushed widget, the header, the footer and a tool result
  (pig:command ext "kit-surfaces" "Show the kit probe on every view surface (D107)"
               (lambda (ctx args) (declare (ignore args))
                 (pig:ctx-set-widget ctx "kit-probe" (kp-probe-view))
                 (pig:ctx-set-header-view ctx (kp-probe-view))
                 (pig:ctx-set-footer-view ctx (kp-probe-view))
                 (pig:tool ext "kit_view_tool" "Render its result as the kit probe" (pig:obj "type" "object") (lambda (ctx params) (declare (ignore ctx params)) "kit"))
                 (pig::tool-renderers ext "kit_view_tool" :result-view (lambda (ctx result options render width) (declare (ignore ctx result options render width)) (kp-probe-view)))))
  ;; the view renderers are lane P's API (pig:: until its exports are merged)
  (pig::message-view-renderer ext "kit-message" (lambda (ctx message options width) (declare (ignore ctx message options width)) (kp-probe-view)))
  (pig::entry-view-renderer ext "kit-entry" (lambda (ctx entry options width) (declare (ignore ctx entry options width)) (kp-probe-view)))
  (pig::tool-renderer ext (lambda (tool next)
                            (if (equal tool "kit_resolved_tool")
                                (pig::make-tool-renderers :call-view (lambda (ctx args render width) (declare (ignore ctx args render width)) (kp-probe-view))
                                                          :result-view (lambda (ctx result options render width) (declare (ignore ctx result options render width)) (kp-probe-view)))
                                (funcall next))))
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
  (pig:command ext "report_geometry" "Report the terminal geometry"
               (lambda (ctx args) (declare (ignore args)) (kp-notify ctx (format nil "geometry:~Dx~D" (pig:ctx-width ctx) (pig:ctx-height ctx)))))
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
