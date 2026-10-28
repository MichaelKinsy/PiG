;;;; SPDX-License-Identifier: MIT
;;;; pi-tui's TuiAltScreen (packages/tui/src/tui-alt-screen.ts) for a Lisp extension: the alternate-screen TUI with a scrollable, application-owned
;;;; viewport. The host runs the layout, scroll, selection, overlay, focus and render logic (its Go port of the class); a TUI-ALT-SCREEN is the
;;;; extension's proxy for it. The extension supplies the TUI-TERMINAL and the TUI-COMPONENTs, and the host calls back (tui.alt.cb) for everything the
;;;; extension owns: terminal output, a component's render, input, mouse and focus, input listeners and the options' callbacks. Python SDK:
;;;; pig_sdk/tui_alt_screen.py.
;;;;
;;;; Async contract (Pi's promises, kept as blocking calls): TUI-ALT-SCREEN-COPY-ACTIVE-SELECTION-TO-CLIPBOARD, TUI-ALT-SCREEN-QUERY-TERMINAL-COLORS, the
;;;; terminal's DRAIN-INPUT and the COPY-SELECTION option block the calling thread and return a value or signal. Every other member is synchronous in
;;;; Pi and a host round trip here. Terminal output reaches the terminal in write order, each write acknowledged before the next. The host sends a
;;;; callback while the extension waits inside a tui.alt call, so a callback runs on its own request thread, and a component's RENDER, a listener or a
;;;; handler may call the screen (a nested call, a child of the callback's request).
;;;;
;;;; Names: Pi's member x of TuiAltScreen is the function tui-alt-screen-x (viewportTop is TUI-ALT-SCREEN-VIEWPORT-TOP, [VIEWPORT_TUI] is
;;;; TUI-ALT-SCREEN-VIEWPORT-TUI-P, onDebug is TUI-ALT-SCREEN-SET-ON-DEBUG). A Pi predicate or a hasX read ends in -P. JSON-shaped values (the overlay
;;;; options, a color report, the terminal's program status) are hash-tables as everywhere in the SDK; a listener's or a mouse handler's answer is a
;;;; plist: (:consume t :data "x") and (:handled t :capture nil :focus nil :render t).
;;;;
;;;; The screen ends with the connection: the host disposes it, and no callback runs afterwards.

(in-package #:pig)

;;; ---------------------------------------------------------------- Terminal and Component

(defstruct (tui-terminal
            (:constructor make-tui-terminal (&key (columns 80) (rows 24) kitty-protocol-active start stop drain-input write move-by hide-cursor
                                               show-cursor clear-line clear-from-cursor clear-screen set-title set-progress set-program-status
                                               restore-on-exit)))
  "pi-tui's Terminal: the extension's terminal. COLUMNS, ROWS and KITTY-PROTOCOL-ACTIVE are read for every call the screen makes, so a resize is
a SETF of them. The rest are functions the host calls through the screen; a NIL one does nothing. START is (lambda (on-input on-resize)): the
screen gives the terminal the two functions to call with typed input (a string) and on a resize. STOP, HIDE-CURSOR, SHOW-CURSOR, CLEAR-LINE,
CLEAR-FROM-CURSOR and CLEAR-SCREEN take nothing; DRAIN-INPUT is (lambda (max-ms idle-ms)); WRITE (lambda (data)); MOVE-BY (lambda (lines));
SET-TITLE (lambda (title)); SET-PROGRESS (lambda (active)); SET-PROGRAM-STATUS (lambda (status)) with Pi's ProgramStatus as a hash-table;
RESTORE-ON-EXIT (lambda (before after)), optional in Pi."
  columns rows kitty-protocol-active start stop drain-input write move-by hide-cursor show-cursor clear-line clear-from-cursor clear-screen
  set-title set-progress set-program-status restore-on-exit)

(defstruct (tui-component
            (:constructor make-tui-component (&key render invalidate handle-input handle-mouse focusable on-focus wants-key-release)))
  "pi-tui's Component, the child of a screen. RENDER is (lambda (width)) returning a list of strings. The rest is optional: INVALIDATE is (lambda ());
HANDLE-INPUT (lambda (data)); HANDLE-MOUSE (lambda (mouse-event)) returning NIL or a plist (:handled :capture :focus :render); FOCUSABLE is Pi's
Focusable, whose focus the host sets and ON-FOCUS, (lambda (focused-p)), hears; FOCUSED reads it; WANTS-KEY-RELEASE asks for key release input.
The host is never asked to deliver input or mouse to a component that has no handler."
  render invalidate handle-input handle-mouse focusable on-focus wants-key-release (focused nil))

;;; ---------------------------------------------------------------- the screens of an extension

(defstruct (tas-registry (:constructor make-tas-registry ()))
  (lock (sb-thread:make-mutex :name "pig tui alt registry"))
  (by-id (make-hash-table :test 'equal))
  (closed nil)
  (next 0))

(defun tas-registry-of (ext) (extension-extra ext :tui-alt #'make-tas-registry))

(defstruct (tui-alt-screen (:conc-name tas-) (:constructor %make-tui-alt-screen (extension id terminal)))
  "Pi's TuiAltScreen: build one with MAKE-TUI-ALT-SCREEN."
  extension id terminal
  (lock (sb-thread:make-mutex :name "pig tui alt screen"))
  (keys (make-hash-table :test 'eq))            ; component -> the key the host knows it by
  (objects (make-hash-table :test 'equal))      ; key -> component (holds the object, so the key is never reused)
  (children '())
  (listeners '())                               ; (id . function), newest last
  (listener-seq 0)
  (scheme-listeners '())
  (on-debug nil)
  (visible (make-hash-table :test 'equal))      ; component key -> the visible callback of its latest overlay
  (late-colors (make-hash-table :test 'equal))  ; query id -> the callback of a reply after the timeout
  (query-seq 0)
  (options '()))                                ; (name . function) of the option callbacks

(defvar *tas-callback-ctx* nil
  "The context of the tui.alt.cb request this thread is answering, if any. The host runs a call made while a callback that its frame waits on (a
component's render, an overlay's visible callback, an option's style function) is served, at once under the frame lock, so the call must be a child
of that callback's request, and the host orders calls per parent request: while a callback runs, the thread's calls are children of its request.")

(defun tas-find (ext id)
  (let ((registry (tas-registry-of ext)))
    (sb-thread:with-mutex ((tas-registry-lock registry)) (gethash id (tas-registry-by-id registry)))))

(def-stop-hook "tui-alt" (ext)
  ;; The host disposes the screens as the connection closes; forget them without telling it.
  (let ((registry (tas-registry-of ext)))
    (sb-thread:with-mutex ((tas-registry-lock registry))
      (setf (tas-registry-closed registry) t)
      (clrhash (tas-registry-by-id registry)))))

;;; ---------------------------------------------------------------- host calls

(defun tas-int (value) (if (realp value) (truncate value) 0))
(defun tas-bool (value) (if value t :false))
(defun tas-text (value) (if (stringp value) value ""))

(defun tas-terminal-block (screen)
  (let ((terminal (tas-terminal screen)))
    (obj "columns" (tas-int (tui-terminal-columns terminal)) "rows" (tas-int (tui-terminal-rows terminal))
         "kitty" (tas-bool (tui-terminal-kitty-protocol-active terminal)))))

(defun tas-invoke (screen ctx op fields)
  "Call tui.alt OP for SCREEN with the alternating key/value list FIELDS on CTX; the host's result object."
  (let ((args (obj "id" (tas-id screen) "op" op)))
    (loop for (key value) on fields by #'cddr do (setf (gethash key args) value))
    (setf (gethash "terminal" args) (tas-terminal-block screen))
    (let ((result (ctx-call-host ctx "tui.alt" args)))
      (if (hash-table-p result) result (obj)))))

(defun tas-call (screen op &rest fields)
  (tas-invoke screen (or *tas-callback-ctx* (make-ctx (tas-extension screen))) op fields))

(defmacro tas-on-failure ((cleanup) &body body)
  "BODY's values; CLEANUP runs when BODY does not return (a condition, a throw)."
  (let ((done (gensym "DONE")))
    `(let ((,done nil))
       (unwind-protect (multiple-value-prog1 (progn ,@body) (setf ,done t))
         (unless ,done ,cleanup)))))

(defun tas-state (screen) (tas-call screen "state"))

(defun tas-key-of (screen component)
  "The key the host knows COMPONENT by: allocated at first sight, the same for the same object."
  (sb-thread:with-mutex ((tas-lock screen))
    (or (gethash component (tas-keys screen))
        (let ((key (format nil "c~D" (1+ (hash-table-count (tas-keys screen))))))
          (setf (gethash component (tas-keys screen)) key
                (gethash key (tas-objects screen)) component)
          key))))

(defun tas-held-key (screen component)
  (or (sb-thread:with-mutex ((tas-lock screen)) (gethash component (tas-keys screen)))
      (error "unfocus :target requires NIL or a component this screen holds")))

(defun tas-caps (component)
  (obj "input" (tas-bool (tui-component-handle-input component)) "mouse" (tas-bool (tui-component-handle-mouse component))
       "focusable" (tas-bool (tui-component-focusable component)) "wantsKeyRelease" (tas-bool (tui-component-wants-key-release component))))

(defun tas-component-fields (screen component)
  (unless (tui-component-p component) (error "~S is not a tui-component" component))
  (list "key" (tas-key-of screen component) "caps" (tas-caps component)))

;;; ---------------------------------------------------------------- construction

(defparameter +tas-overlay-option-keys+ '("width" "minWidth" "maxHeight" "anchor" "offsetX" "offsetY" "row" "col" "margin" "nonCapturing"))

(defun tas-wheel-lines (lines)
  (cond ((realp lines) lines)
        ((or (eq lines :auto) (equal lines "auto")) "auto")
        (t (error "wheel-scroll-lines is a number or :auto"))))

(defun make-tui-alt-screen (ctx terminal &key (show-hardware-cursor nil show-p) log-directory wheel-scroll-lines (mouse nil mouse-p)
                                              search-match-style search-current-match-style search-navigation-button-style scroll-to-end-indicator
                                              open-url on-right-click-paste (copy-on-select nil copy-p) copy-selection)
  "Pi's new TuiAltScreen(terminal, showHardwareCursor, logDirectory, options): create the screen in the host, drawing on TERMINAL (a TUI-TERMINAL).
CTX is the context of the code that creates it; the screen's later host calls are the extension's own and do not end with that request.
The options: WHEEL-SCROLL-LINES is the logical lines a wheel event moves (a number or :auto); MOUSE captures mouse events for viewport scrolling and
selection; COPY-ON-SELECT copies a selection on mouse release (Pi's default is on); SEARCH-MATCH-STYLE and SEARCH-CURRENT-MATCH-STYLE are
(lambda (text)) returning the styled text; SEARCH-NAVIGATION-BUTTON-STYLE is (lambda (text hovered-p)); SCROLL-TO-END-INDICATOR is (lambda ())
returning the label; OPEN-URL is (lambda (url)); ON-RIGHT-CLICK-PASTE is (lambda ()); COPY-SELECTION is (lambda (text)) returning true on success,
an error message string to display, or NIL for a generic error. An omitted flag is Pi's undefined."
  (unless (tui-terminal-p terminal) (error "~S is not a tui-terminal" terminal))
  (let* ((ext (ctx-extension ctx))
         (registry (tas-registry-of ext))
         (screen (%make-tui-alt-screen ext (sb-thread:with-mutex ((tas-registry-lock registry)) (format nil "t~D" (incf (tas-registry-next registry)))) terminal))
         (callbacks (loop for (name function) in `(("copySelection" ,copy-selection) ("openUrl" ,open-url) ("searchMatchStyle" ,search-match-style)
                                                   ("searchCurrentMatchStyle" ,search-current-match-style)
                                                   ("searchNavigationButtonStyle" ,search-navigation-button-style)
                                                   ("scrollToEndIndicator" ,scroll-to-end-indicator) ("onRightClickPaste" ,on-right-click-paste))
                          when function collect (progn (unless (functionp function) (error "the ~A option must be a function" name)) (cons name function))))
         (options (obj)))
    (setf (tas-options screen) callbacks)
    (when wheel-scroll-lines (setf (gethash "wheelScrollLines" options) (tas-wheel-lines wheel-scroll-lines)))
    (when mouse-p (setf (gethash "mouse" options) (tas-bool mouse)))
    (when copy-p (setf (gethash "copyOnSelect" options) (tas-bool copy-on-select)))
    (when callbacks (setf (gethash "callbacks" options) (map 'simple-vector #'car callbacks)))
    ;; The host may call back from the moment it knows the screen, which can be before the call returns.
    (sb-thread:with-mutex ((tas-registry-lock registry))
      (when (tas-registry-closed registry) (error 'host-call-error :message "Extension connection is closed"))
      (setf (gethash (tas-id screen) (tas-registry-by-id registry)) screen))
    (tas-on-failure ((sb-thread:with-mutex ((tas-registry-lock registry)) (remhash (tas-id screen) (tas-registry-by-id registry))))
      (tas-invoke screen ctx "create"
                  (append (when show-p (list "showHardwareCursor" (tas-bool show-hardware-cursor)))
                          (when log-directory (list "logDirectory" log-directory))
                          (when (plusp (hash-table-count options)) (list "options" options)))))
    screen))

;;; ---------------------------------------------------------------- Pi's reads

(defun tui-alt-screen-mode (screen) (declare (ignore screen)) "fullscreen")
(defun tui-alt-screen-viewport-tui-p (screen) (declare (ignore screen)) t)
(defun tui-alt-screen-terminal (screen) (tas-terminal screen))

(defun tui-alt-screen-children (screen)
  "The children added with TUI-ALT-SCREEN-ADD-CHILD, in order. A copy: change the screen with add-child, remove-child and clear."
  ;; pig divergence (D207): a snapshot the SDK updates after add-child, remove-child and clear return, not Pi's live array; there is no setter
  (sb-thread:with-mutex ((tas-lock screen)) (copy-list (tas-children screen))))

(defun tui-alt-screen-viewport-top (screen) (tas-int (jget (tas-state screen) "viewportTop")))
(defun tui-alt-screen-following-output-p (screen) (eq (jget (tas-state screen) "isFollowingOutput") t))
(defun tui-alt-screen-full-redraws (screen) (tas-int (jget (tas-state screen) "fullRedraws")))
(defun tui-alt-screen-has-overlay-entries-p (screen) (eq (jget (tas-state screen) "hasOverlayEntries") t))
(defun tui-alt-screen-has-overlay-p (screen) (eq (jget (tas-state screen) "hasOverlay") t))
(defun tui-alt-screen-has-active-selection-p (screen)
  "Whether the fullscreen viewport has a non-empty active text selection."
  (eq (jget (tas-state screen) "hasActiveSelection") t))
(defun tui-alt-screen-get-copy-on-select (screen) (eq (jget (tas-state screen) "copyOnSelect") t))
(defun tui-alt-screen-get-show-hardware-cursor (screen) (eq (jget (tas-state screen) "showHardwareCursor") t))
(defun tui-alt-screen-get-clear-on-shrink (screen) (eq (jget (tas-state screen) "clearOnShrink") t))

(defun tui-alt-screen-get-focused-component (screen)
  "The very object given to TUI-ALT-SCREEN-SET-FOCUS (or an overlay or child the host focused), or NIL."
  (let ((key (jget (tas-state screen) "focusedKey")))
    (and (stringp key) (sb-thread:with-mutex ((tas-lock screen)) (gethash key (tas-objects screen))))))

(defun tui-alt-screen-set-on-debug (screen callback)
  "Pi's onDebug: CALLBACK, (lambda ()), runs on the debug key (Shift+Ctrl+D) before input reaches the focused component. NIL clears it."
  (when (and callback (not (functionp callback))) (error "the onDebug callback must be a function or NIL"))
  (setf (tas-on-debug screen) callback)
  (tas-call screen "setOnDebug" "enabled" (tas-bool callback))
  nil)

;;; ---------------------------------------------------------------- children, focus, overlays

(defun tui-alt-screen-add-child (screen component)
  (apply #'tas-call screen "addChild" (tas-component-fields screen component))
  (sb-thread:with-mutex ((tas-lock screen)) (setf (tas-children screen) (append (tas-children screen) (list component))))
  nil)

(defun tui-alt-screen-remove-child (screen component)
  (let ((key (sb-thread:with-mutex ((tas-lock screen))
               (and (member component (tas-children screen) :test #'eq) (gethash component (tas-keys screen))))))
    (when key
      (tas-call screen "removeChild" "key" key)
      (sb-thread:with-mutex ((tas-lock screen)) (setf (tas-children screen) (remove component (tas-children screen) :test #'eq :count 1)))))
  nil)

(defun tui-alt-screen-clear (screen)
  (tas-call screen "clear")
  (sb-thread:with-mutex ((tas-lock screen)) (setf (tas-children screen) '()))
  nil)

(defun tui-alt-screen-set-layout-root (screen component)
  "Pi's setLayoutRoot: the component rendered instead of the children; NIL returns to the children."
  (if component
      (apply #'tas-call screen "setLayoutRoot" (tas-component-fields screen component))
      (tas-call screen "setLayoutRoot" "key" nil))
  nil)

(defun tui-alt-screen-set-focus (screen component)
  (if component
      (apply #'tas-call screen "setFocus" (tas-component-fields screen component))
      (tas-call screen "setFocus" "key" nil))
  nil)

(defstruct (tui-alt-overlay-handle (:conc-name taoh-) (:constructor make-tui-alt-overlay-handle (screen key)))
  "pi-tui's OverlayHandle of an overlay TUI-ALT-SCREEN-SHOW-OVERLAY mounted. Each control is a host call that returns after the host applied it;
TUI-ALT-OVERLAY-HIDDEN-P, TUI-ALT-OVERLAY-FOCUSED-P and TUI-ALT-OVERLAY-GET-BOUNDS read the host's current state."
  screen key)

(defun taoh-control (handle action &rest fields)
  (apply #'tas-call (taoh-screen handle) "overlay" "key" (taoh-key handle) "action" action fields))

(defun tui-alt-screen-show-overlay (screen component &optional options)
  "Pi's showOverlay: mount COMPONENT as an overlay and return its handle. OPTIONS is Pi's OverlayOptions as a hash-table (\"width\", \"minWidth\",
\"maxHeight\", \"anchor\", \"offsetX\", \"offsetY\", \"row\", \"col\", \"margin\", \"nonCapturing\"); its \"visible\" is a function
(lambda (term-width term-height)) that runs in this process each time the host composites the overlay."
  (let ((fields (tas-component-fields screen component)) (visible nil) (wire nil))
    (when options
      (unless (hash-table-p options) (error "overlay options are a hash-table"))
      (maphash (lambda (key value)
                 (cond ((equal key "visible")
                        (unless (functionp value) (error "overlay option \"visible\" must be a function (term-width term-height)"))
                        (setf visible value))
                       ((member key +tas-overlay-option-keys+ :test #'equal))
                       (t (error "unknown overlay option ~S" key))))
               options)
      (setf wire (obj))
      (maphash (lambda (key value) (unless (equal key "visible") (setf (gethash key wire) value))) options)
      (setf fields (append fields (list "options" wire)))
      (when visible (setf fields (append fields (list "visible" t)))))
    (let* ((key (second fields))
           ;; The host asks from the moment it knows the overlay, which can be before the call returns; the latest show of a component names its callback.
           (previous (sb-thread:with-mutex ((tas-lock screen))
                       (prog1 (gethash key (tas-visible screen))
                         (if visible (setf (gethash key (tas-visible screen)) visible) (remhash key (tas-visible screen)))))))
      (tas-on-failure ((sb-thread:with-mutex ((tas-lock screen))
                         (if previous (setf (gethash key (tas-visible screen)) previous) (remhash key (tas-visible screen)))))
        (apply #'tas-call screen "showOverlay" fields))
      (make-tui-alt-overlay-handle screen key))))

(defun tui-alt-screen-hide-overlay (screen)
  "Hide the topmost overlay and restore the previous focus."
  (tas-call screen "hideOverlay")
  nil)

(defun tui-alt-overlay-hide (handle) "Remove the overlay permanently." (taoh-control handle "hide") nil)
(defun tui-alt-overlay-set-hidden (handle hidden) (taoh-control handle "setHidden" "hidden" (tas-bool hidden)) nil)
(defun tui-alt-overlay-hidden-p (handle) (eq (jget (taoh-control handle "state") "hidden") t))
(defun tui-alt-overlay-focus (handle) (taoh-control handle "focus") nil)
(defun tui-alt-overlay-focused-p (handle) (eq (jget (taoh-control handle "state") "focused") t))

(defun tui-alt-overlay-unfocus (handle &key (target nil target-p))
  "Release keyboard focus. :TARGET names where it goes: NIL focuses nothing, a component the screen holds focuses it; without :TARGET the host
chooses."
  (cond ((not target-p) (taoh-control handle "unfocus"))
        ((null target) (taoh-control handle "unfocus" "target" nil))
        (t (taoh-control handle "unfocus" "target" (obj "key" (tas-held-key (taoh-screen handle) target)))))
  nil)

(defun tui-alt-overlay-get-bounds (handle)
  "The overlay's {row, col, width, height} hash-table while it is visible, else NIL."
  (let ((bounds (jget (taoh-control handle "state") "bounds")))
    (and (hash-table-p bounds) bounds)))

;;; ---------------------------------------------------------------- lifecycle and rendering

(defun tui-alt-screen-start (screen)
  "Start the terminal (its START runs), enter the alternate screen, hide the cursor and request the first render."
  (tas-call screen "start")
  nil)

(defun tui-alt-screen-stop (screen &optional preserve-screen)
  "Show the cursor, stop the terminal (its STOP runs) and leave the alternate screen. PRESERVE-SCREEN leaves the renderer's output in place for
another TUI taking over the terminal."
  (if preserve-screen (tas-call screen "stop" "preserveScreen" t) (tas-call screen "stop"))
  nil)

(defun tui-alt-screen-render-now (screen &optional force)
  (if force (tas-call screen "renderNow" "force" t) (tas-call screen "renderNow"))
  nil)

(defun tui-alt-screen-request-render (screen &optional force)
  (if force (tas-call screen "requestRender" "force" t) (tas-call screen "requestRender"))
  nil)

(defun tui-alt-screen-invalidate (screen) (tas-call screen "invalidate") nil)

(defun tas-lines (result)
  (let ((lines (jget result "lines")))
    (if (and (vectorp lines) (not (stringp lines))) (coerce lines 'list) '())))

(defun tui-alt-screen-render (screen width)
  "The lines of the root (the layout root, else the children) rendered at WIDTH."
  (tas-lines (tas-call screen "render" "width" width)))

(defun tui-alt-screen-get-screen-lines (screen)
  "The lines of the last rendered frame, one per terminal row, as written to the terminal."
  (tas-lines (tas-call screen "getScreenLines")))

(defun tui-alt-screen-scroll-by (screen lines) (tas-call screen "scrollBy" "lines" lines) nil)
(defun tui-alt-screen-scroll-to-top (screen) (tas-call screen "scrollToTop") nil)
(defun tui-alt-screen-scroll-to-bottom (screen) (tas-call screen "scrollToBottom") nil)

(defun tui-alt-screen-flash (screen message &optional duration-ms)
  "Show a transient message in the alternate-screen flash stack."
  (if duration-ms
      (tas-call screen "flash" "message" message "durationMs" duration-ms)
      (tas-call screen "flash" "message" message))
  nil)

(defun tui-alt-screen-set-wheel-scroll-lines (screen lines)
  (tas-call screen "setWheelScrollLines" "lines" (tas-wheel-lines lines))
  nil)

(defun tui-alt-screen-set-copy-on-select (screen enabled) (tas-call screen "setCopyOnSelect" "enabled" (tas-bool enabled)) nil)
(defun tui-alt-screen-set-show-hardware-cursor (screen enabled) (tas-call screen "setShowHardwareCursor" "enabled" (tas-bool enabled)) nil)
(defun tui-alt-screen-set-clear-on-shrink (screen enabled) (tas-call screen "setClearOnShrink" "enabled" (tas-bool enabled)) nil)

(defun tui-alt-screen-copy-active-selection-to-clipboard (screen)
  "Copy the active text selection, if any, through the configured selection clipboard path. Blocks until it settles; true when it copied."
  (eq (jget (tas-call screen "copyActiveSelectionToClipboard") "value") t))

(defun tui-alt-screen-reset-text-selection (screen)
  "Drop the text selection and multi-click history."
  (tas-call screen "resetTextSelection")
  nil)

;;; ---------------------------------------------------------------- mouse

(defun tas-mouse-wire (event)
  (if (hash-table-p event)
      event
      (let ((wire (obj "type" (mouse-event-type event) "button" (mouse-event-button event) "x" (mouse-event-x event) "y" (mouse-event-y event)
                       "screenX" (mouse-event-screen-x event) "screenY" (mouse-event-screen-y event)
                       "width" (mouse-event-width event) "height" (mouse-event-height event)
                       "shift" (tas-bool (mouse-event-shift event)) "alt" (tas-bool (mouse-event-alt event)) "ctrl" (tas-bool (mouse-event-ctrl event)))))
        (when (or (equal (mouse-event-type event) "wheel") (not (eql 0 (mouse-event-wheel-delta event))))
          (setf (gethash "wheelDelta" wire) (mouse-event-wheel-delta event)))
        (unless (eql 0 (mouse-event-click-count event)) (setf (gethash "clickCount" wire) (mouse-event-click-count event)))
        wire)))

(defun tas-mouse-result-plist (result)
  "The plist of a host TuiMouseEventResult: a flag the host left unset (null) is absent."
  (when (hash-table-p result)
    (loop for (key name) in '((:handled "handled") (:capture "capture") (:focus "focus") (:render "render"))
          for value = (jget result name)
          when (member value '(t :false)) append (list key (eq value t)))))

(defun tas-mouse-result-wire (plist)
  (when plist
    (let ((wire (obj)) (absent (list nil)))
      (loop for (key name) in '((:handled "handled") (:capture "capture") (:focus "focus") (:render "render"))
            for value = (getf plist key absent)
            unless (eq value absent) do (setf (gethash name wire) (tas-bool value)))
      wire)))

(defun tui-alt-screen-handle-mouse (screen event)
  "Dispatch a mouse event (a MOUSE-EVENT) to the screen's components; the plist (:handled :capture :focus :render) of what handled it, NIL when none did."
  (tas-mouse-result-plist (jget (tas-call screen "handleMouse" "event" (tas-mouse-wire event)) "result")))

;;; ---------------------------------------------------------------- input listeners and terminal colors

(defun tui-alt-screen-add-input-listener (screen listener)
  "Add LISTENER, (lambda (data)), which sees each terminal input before the focused component. It returns NIL, or the plist (:consume t) to stop the
input, or (:data \"s\") to replace it. Returns the function that removes it. Adding a listener already added changes nothing."
  (unless (functionp listener) (error "an input listener must be a function"))
  (let ((id (sb-thread:with-mutex ((tas-lock screen))
              (unless (rassoc listener (tas-listeners screen) :test #'eq)
                (let ((id (format nil "l~D" (incf (tas-listener-seq screen)))))
                  (setf (tas-listeners screen) (append (tas-listeners screen) (list (cons id listener))))
                  id)))))
    (when id
      (tas-on-failure ((sb-thread:with-mutex ((tas-lock screen))
                         (setf (tas-listeners screen) (remove id (tas-listeners screen) :key #'car :test #'equal))))
        (tas-call screen "addInputListener" "listenerId" id)))
    (lambda () (tui-alt-screen-remove-input-listener screen listener))))

(defun tui-alt-screen-remove-input-listener (screen listener)
  (let ((id (sb-thread:with-mutex ((tas-lock screen)) (car (rassoc listener (tas-listeners screen) :test #'eq)))))
    (when id
      (tas-call screen "removeInputListener" "listenerId" id)
      (sb-thread:with-mutex ((tas-lock screen))
        (setf (tas-listeners screen) (remove id (tas-listeners screen) :key #'car :test #'equal)))))
  nil)

(defun tui-alt-screen-on-terminal-color-scheme-change (screen listener)
  "Subscribe LISTENER, (lambda (scheme)), to the terminal's \"dark\" / \"light\" scheme reports (see TUI-ALT-SCREEN-SET-TERMINAL-COLOR-SCHEME-NOTIFICATIONS).
Returns the function that unsubscribes."
  (unless (functionp listener) (error "a color scheme listener must be a function"))
  (sb-thread:with-mutex ((tas-lock screen))
    (unless (member listener (tas-scheme-listeners screen) :test #'eq)
      (setf (tas-scheme-listeners screen) (append (tas-scheme-listeners screen) (list listener)))))
  (lambda () (sb-thread:with-mutex ((tas-lock screen)) (setf (tas-scheme-listeners screen) (remove listener (tas-scheme-listeners screen) :test #'eq)))
    nil))

(defun tui-alt-screen-set-terminal-color-scheme-notifications (screen enabled)
  (tas-call screen "setTerminalColorSchemeNotifications" "enabled" (tas-bool enabled))
  nil)

(defun tui-alt-screen-query-terminal-colors (screen timeout-ms &optional on-late-reply)
  "Query the terminal's theme colors. Blocks until the DA1 reply or TIMEOUT-MS; the hash-table of what it reported (\"foreground\", \"background\",
\"palette\"), absent for a color it did not report. ON-LATE-REPLY, (lambda (colors)), receives the colors when the reply arrives after the timeout."
  (let ((fields (list "timeoutMs" timeout-ms "onLateReply" (tas-bool on-late-reply))))
    (when on-late-reply
      (unless (functionp on-late-reply) (error "on-late-reply must be a function"))
      ;; The callback stays registered after the query returns: a reply after the timeout names the query (lateColors).
      (let ((id (sb-thread:with-mutex ((tas-lock screen))
                  (let ((id (format nil "q~D" (incf (tas-query-seq screen)))))
                    (setf (gethash id (tas-late-colors screen)) on-late-reply)
                    id))))
        (setf fields (append fields (list "queryId" id)))))
    (let ((colors (jget (apply #'tas-call screen "queryTerminalColors" fields) "colors")))
      (if (hash-table-p colors) colors (obj)))))

;;; ---------------------------------------------------------------- host callbacks (tui.alt.cb)

(defun tas-component (screen args)
  (let ((key (tas-text (jget args "key"))))
    (or (sb-thread:with-mutex ((tas-lock screen)) (gethash key (tas-objects screen)))
        (error "unknown component ~A" key))))

(defun tas-on-input (screen) (lambda (data) (tas-call screen "input" "data" data) nil))
(defun tas-on-resize (screen) (lambda () (tas-call screen "resize") nil))

(defun tas-terminal-callback (screen args)
  (let* ((terminal (tas-terminal screen)) (method (jget args "method")))
    (flet ((run (function &rest arguments) (when function (apply function arguments))))
      (cond
        ((equal method "start") (run (tui-terminal-start terminal) (tas-on-input screen) (tas-on-resize screen)))
        ((equal method "stop") (run (tui-terminal-stop terminal)))
        ((equal method "write") (run (tui-terminal-write terminal) (tas-text (jget args "data"))))
        ((equal method "moveBy") (run (tui-terminal-move-by terminal) (tas-int (jget args "lines"))))
        ((equal method "hideCursor") (run (tui-terminal-hide-cursor terminal)))
        ((equal method "showCursor") (run (tui-terminal-show-cursor terminal)))
        ((equal method "clearLine") (run (tui-terminal-clear-line terminal)))
        ((equal method "clearFromCursor") (run (tui-terminal-clear-from-cursor terminal)))
        ((equal method "clearScreen") (run (tui-terminal-clear-screen terminal)))
        ((equal method "setTitle") (run (tui-terminal-set-title terminal) (tas-text (jget args "title"))))
        ((equal method "setProgress") (run (tui-terminal-set-progress terminal) (eq (jget args "active") t)))
        ((equal method "setProgramStatus") (run (tui-terminal-set-program-status terminal) (jget args "status")))
        ((equal method "restoreOnExit")
         (run (tui-terminal-restore-on-exit terminal) (tas-text (jget args "before")) (tas-text (jget args "after"))))
        ((equal method "drainInput")
         (flet ((ms (key) (let ((v (jget args key))) (and (realp v) v))))
           (run (tui-terminal-drain-input terminal) (ms "maxMs") (ms "idleMs"))))
        (t (error "unknown terminal method ~S" method)))
      (obj))))

(defun tas-render-callback (screen args)
  (let* ((component (tas-component screen args))
         (lines (funcall (tui-component-render component) (tas-int (jget args "width")))))
    (when (and (vectorp lines) (not (stringp lines))) (setf lines (coerce lines 'list)))
    (unless (and (listp lines) (every #'stringp lines)) (error "render must return a list of strings"))
    (obj "lines" (coerce lines 'simple-vector))))

(defun tas-mouse-callback (screen args)
  (let* ((component (tas-component screen args)) (handler (tui-component-handle-mouse component))
         (event (uc-mouse-from-wire (jget args "event"))))
    (obj "result" (when (and handler event)
                    (let ((result (funcall handler event)))
                      (unless (listp result) (error "handle-mouse must return a plist (:handled :capture :focus :render) or NIL"))
                      (tas-mouse-result-wire result))))))

(defun tas-listener-callback (screen args)
  (let ((listener (sb-thread:with-mutex ((tas-lock screen)) (cdr (assoc (tas-text (jget args "listenerId")) (tas-listeners screen) :test #'equal)))))
    (if (null listener)
        (obj)
        (let ((result (funcall listener (tas-text (jget args "data")))) (reply (obj)) (absent (list nil)))
          (unless (listp result) (error "an input listener must return a plist (:consume :data) or NIL"))
          (let ((consume (getf result :consume absent)) (data (getf result :data absent)))
            (unless (eq consume absent) (setf (gethash "consume" reply) (tas-bool consume)))
            (unless (eq data absent) (setf (gethash "data" reply) (tas-text data))))
          reply))))

(defun tas-option-callback (screen args)
  (let* ((name (tas-text (jget args "name"))) (callback (cdr (assoc name (tas-options screen) :test #'equal)))
         (call-args (let ((a (jget args "args"))) (if (and (vectorp a) (not (stringp a))) (coerce a 'list) '()))))
    (unless callback (error "TuiAltScreen option ~S is not set" name))
    (cond ((equal name "copySelection")
           (let ((value (funcall callback (tas-text (first call-args)))))
             (obj "value" (if (stringp value) value (tas-bool value)))))
          ((equal name "openUrl") (funcall callback (tas-text (first call-args))) (obj "value" nil))
          ((equal name "onRightClickPaste") (funcall callback) (obj "value" nil))
          (t (obj "value" (tas-text (cond ((equal name "searchNavigationButtonStyle") (funcall callback (tas-text (first call-args)) (eq (second call-args) t)))
                                          ((equal name "scrollToEndIndicator") (funcall callback))
                                          (t (funcall callback (tas-text (first call-args)))))))))))

(defun tas-callback (screen args)
  (let ((kind (jget args "kind")))
    (cond
      ((equal kind "terminal") (tas-terminal-callback screen args))
      ((equal kind "render") (tas-render-callback screen args))
      ((equal kind "input")
       (let ((handler (tui-component-handle-input (tas-component screen args))))
         (when handler (funcall handler (tas-text (jget args "data"))))
         (obj)))
      ((equal kind "mouse") (tas-mouse-callback screen args))
      ((equal kind "focus")
       (let ((component (tas-component screen args)) (focused (eq (jget args "focused") t)))
         (when (tui-component-focusable component)
           (setf (tui-component-focused component) focused)
           (when (tui-component-on-focus component) (funcall (tui-component-on-focus component) focused)))
         (obj)))
      ((equal kind "invalidate")
       (let ((invalidate (tui-component-invalidate (tas-component screen args))))
         (when invalidate (funcall invalidate))
         (obj)))
      ((equal kind "visible")
       (let ((visible (sb-thread:with-mutex ((tas-lock screen)) (gethash (tas-text (jget args "key")) (tas-visible screen)))))
         (unless visible (error "no visible callback for component ~A" (jget args "key")))
         (obj "value" (tas-bool (funcall visible (tas-int (jget args "termWidth")) (tas-int (jget args "termHeight")))))))
      ((equal kind "listener") (tas-listener-callback screen args))
      ((equal kind "debug")
       (let ((callback (tas-on-debug screen))) (when callback (funcall callback)) (obj)))
      ((equal kind "colorScheme")
       (let ((listeners (sb-thread:with-mutex ((tas-lock screen)) (copy-list (tas-scheme-listeners screen)))))
         (dolist (listener listeners) (funcall listener (tas-text (jget args "scheme"))))
         (obj)))
      ((equal kind "lateColors")
       (let ((callback (sb-thread:with-mutex ((tas-lock screen))
                         (prog1 (gethash (tas-text (jget args "queryId")) (tas-late-colors screen))
                           (remhash (tas-text (jget args "queryId")) (tas-late-colors screen))))))
         (when callback (funcall callback (let ((colors (jget args "colors"))) (if (hash-table-p colors) colors (obj)))))
         (obj)))
      ((equal kind "option") (tas-option-callback screen args))
      (t (error "unknown tui.alt.cb kind ~S" kind)))))

(def-request "tui.alt.cb" (ext ctx req)
  (let* ((args (let ((a (jget req "args"))) (if (hash-table-p a) a (obj))))
         (screen (tas-find ext (tas-text (jget args "id")))))
    (unless screen (error "unknown TuiAltScreen ~A" (jget args "id")))
    (let ((*tas-callback-ctx* ctx)) (tas-callback screen args))))
