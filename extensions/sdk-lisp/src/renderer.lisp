;;;; SPDX-License-Identifier: MIT
;;;; Message, entry and tool renderers, tool renderer resolvers, the Markdown transformer, and the login and sprite definitions.
;;;; A renderer returns the lines of its card (a list or vector of strings); the view forms return a kit view.

(in-package #:pig)

(defstruct tool-renderers
  "How calls to a tool are drawn. RENDER-CALL is (lambda (ctx args render width)) and RENDER-RESULT (lambda (ctx result options render width)),
both returning lines; CALL-VIEW and RESULT-VIEW take the same arguments and return kit views, which win over the line form of their phase.
RENDER-SHELL is \"default\" or \"self\"."
  render-call render-result (render-shell "default") call-view result-view)

(defstruct tool-render-context
  "What the host tells a tool renderer. STATE is a hash-table that lives as long as the card; INVALIDATE is a function without arguments.
LAST-COMPONENT is what this card's renderer of the same phase (call or result) returned on its previous run, as Pi's lastComponent is the
component the renderer returned before: a renderer that returns lines or a kit view returns no component object, so those lines or that
view stand in for it. NIL on the card's first run of the phase and after a run that signalled (Pi forgets the component a throwing renderer left)."
  (args (obj)) (tool-call-id "") (cwd "") execution-started args-complete is-partial expanded show-images is-error (output-pad 0)
  duration-ms last-component (state (obj)) invalidate)

;;; Pi 9376c8d1 (#4748) hands renderers the host's keyText and keyHint on the tool render context and the message and entry
;;; options (KeybindingHints). The message and entry options are the host's JSON object, so each form takes it as its first
;;; argument, as a method takes its receiver; every form delegates to KEY-TEXT and KEY-HINT, which read the host's keybindings.

(defun tool-render-context-key-text (context keybinding)
  "KeybindingHints.keyText of the tool render context CONTEXT: KEY-TEXT."
  (declare (ignore context))
  (key-text keybinding))
(defun tool-render-context-key-hint (context keybinding description)
  "KeybindingHints.keyHint of the tool render context CONTEXT: KEY-HINT."
  (declare (ignore context))
  (key-hint keybinding description))
(defun message-render-options-key-text (options keybinding)
  "KeybindingHints.keyText of the OPTIONS a message renderer receives: KEY-TEXT."
  (declare (ignore options))
  (key-text keybinding))
(defun message-render-options-key-hint (options keybinding description)
  "KeybindingHints.keyHint of the OPTIONS a message renderer receives: KEY-HINT."
  (declare (ignore options))
  (key-hint keybinding description))
(defun entry-render-options-key-text (options keybinding)
  "KeybindingHints.keyText of the OPTIONS an entry renderer receives: KEY-TEXT."
  (declare (ignore options))
  (key-text keybinding))
(defun entry-render-options-key-hint (options keybinding description)
  "KeybindingHints.keyHint of the OPTIONS an entry renderer receives: KEY-HINT."
  (declare (ignore options))
  (key-hint keybinding description))

(defstruct (rd-state (:conc-name rd-))
  (message-declarations '()) (message-handlers (make-hash-table :test 'equal)) (message-views (make-hash-table :test 'equal))
  (entry-declarations '()) (entry-handlers (make-hash-table :test 'equal)) (entry-views (make-hash-table :test 'equal))
  (message-events (make-hash-table :test 'equal)) (entry-events (make-hash-table :test 'equal))
  (markdown nil) (markdown-message-types :default) (owns-thinking-runs nil)
  (resolvers '()) (resolved (make-hash-table :test 'equal))
  (cards (make-hash-table :test 'equal))      ; tool card -> (lock . state)
  (render-cards (make-hash-table :test 'equal)) ; message or entry key -> render-card
  (lock (sb-thread:make-mutex :name "pig renderers")))

(defstruct (render-card (:constructor make-render-card ()))
  "The renderer state of one displayed message or entry, which the host names with a key. Requests for one card run one at a time, as Pi
draws a message on one thread. SUBJECT is the message or entry as its first request decoded it: every request for the card hands the
renderer that one object, as Pi's renderer receives one message object. STATE is the hash-table the renderer and its event handler share."
  (lock (sb-thread:make-mutex :name "pig render card")) (state (obj)) (subject nil))

(defun rd-of (ext) (extension-extra ext :renderer #'make-rd-state))

(declaim (ftype function kit-render-result kit-event-from-wire))

(defun rd-result (ext value view-p)
  (if view-p
      (kit-render-result ext value)
      (obj "lines" (prov-vec value))))

;;; ---------------------------------------------------------------- message and entry renderers

(defun rd-register (ext kind custom-type handler view-p)
  "Register HANDLER for CUSTOM-TYPE as a :message or :entry renderer, a view renderer when VIEW-P."
  (unless (functionp handler) (error "~(~A~) renderer must be a function" kind))
  (let ((rd (rd-of ext)) (declaration (obj "custom_type" custom-type)))
    (multiple-value-bind (lines views key)
        (if (eq kind :message)
            (values (rd-message-handlers rd) (rd-message-views rd) "message_renderers")
            (values (rd-entry-handlers rd) (rd-entry-views rd) "entry_renderers"))
      (sb-thread:with-mutex ((extension-reg-lock ext))
        (if (eq kind :message)
            (setf (rd-message-declarations rd) (append (rd-message-declarations rd) (list declaration)))
            (setf (rd-entry-declarations rd) (append (rd-entry-declarations rd) (list declaration))))
        (remhash custom-type (if view-p lines views))
        (setf (gethash custom-type (if view-p views lines)) handler)
        (publish-declaration ext (obj key (vector declaration))))))
  nil)

(defun message-renderer (ext custom-type handler)
  "Render messages of CUSTOM-TYPE: HANDLER is (lambda (ctx message options width)) -> lines."
  (rd-register ext :message custom-type handler nil))
(defun message-view-renderer (ext custom-type handler)
  "Like MESSAGE-RENDERER, returning a kit view; it replaces a line renderer of CUSTOM-TYPE."
  (rd-register ext :message custom-type handler t))
(defun entry-renderer (ext custom-type handler)
  "Render session entries of CUSTOM-TYPE: HANDLER is (lambda (ctx entry options width)) -> lines."
  (rd-register ext :entry custom-type handler nil))
(defun entry-view-renderer (ext custom-type handler)
  "Like ENTRY-RENDERER, returning a kit view; it replaces a line renderer of CUSTOM-TYPE."
  (rd-register ext :entry custom-type handler t))

;;; A displayed message or entry is named by the host with a key. Pi hands a custom message renderer the same message object on every
;;; rebuild, so the renderer keeps per-message state in a WeakMap keyed by it, and the Component it returns toggles that state from a
;;; MouseRegion click. The renderers here are request/response, so the extension keeps a card per key: the state the renderer reads and
;;; writes through the state the options name (MESSAGE-RENDER-OPTIONS-STATE), and the message or entry as the first request for the key decoded it. The mouse events
;;; the host's view took arrive in the next request for the key and are applied, in order, before that request renders.

(defun rd-view-event (ext kind custom-type handler)
  "Register HANDLER as the event handler of CUSTOM-TYPE's :message or :entry view renderer."
  (unless (functionp handler) (error "~(~A~) view event handler must be a function" kind))
  (let ((rd (rd-of ext)))
    (sb-thread:with-mutex ((extension-reg-lock ext))
      (setf (gethash custom-type (if (eq kind :message) (rd-message-events rd) (rd-entry-events rd))) handler)))
  nil)

(defun message-view-event (ext custom-type handler)
  "Handle the events of the views of CUSTOM-TYPE's MESSAGE-VIEW-RENDERER: HANDLER is (lambda (ctx message options event)), EVENT a
kit-event (KIT-EVENT-TYPE, KIT-EVENT-NODE, KIT-EVENT-MOUSE). The click on a kit-mouse-region of the view arrives as a \"mouse\" event of
that region's id, with coordinates local to the region. HANDLER changes the message's state, (MESSAGE-RENDER-OPTIONS-STATE options), and the
host draws the message again with the next request, which runs after every event the host had taken. MESSAGE and OPTIONS are the ones of
the message's last render. An event without a handler is ignored; a handler that signals does not stop the later events or the render, and
the first condition it signalled is signalled once the render has run. A later registration replaces an earlier one. Local to the
extension: nothing is declared to the host."
  (rd-view-event ext :message custom-type handler))

(defun entry-view-event (ext custom-type handler)
  "Handle the events of the views of CUSTOM-TYPE's ENTRY-VIEW-RENDERER: HANDLER is (lambda (ctx entry options event)); see MESSAGE-VIEW-EVENT."
  (rd-view-event ext :entry custom-type handler))

;;; The options are the hash-table the wire decoded (what an author reads "expanded" from), so the state is not a member of it: it is kept
;;; beside it by identity, for as long as the options live.
(defvar *render-option-states* (make-hash-table :test 'eq :weakness :key :synchronized t))

(defun message-render-options-state (options)
  "The state of the message OPTIONS belong to: a hash-table that is the same for every request for the message (renderer and event handler
alike), empty at the first, and that lives until the host releases the message. A request for a message the host does not name (an HTML
export) gets a table of its own."
  (values (gethash options *render-option-states*)))

(defun entry-render-options-state (options)
  "The state of the entry OPTIONS belong to; see MESSAGE-RENDER-OPTIONS-STATE."
  (values (gethash options *render-option-states*)))

(defun rd-render-card (rd key)
  "The card of KEY, created on first use. An empty KEY gets a card of its own that nothing keeps."
  (if (zerop (length key))
      (make-render-card)
      (sb-thread:with-mutex ((rd-lock rd))
        (or (gethash key (rd-render-cards rd)) (setf (gethash key (rd-render-cards rd)) (make-render-card))))))

(def-notify "render_release" (ext args)
  (let ((rd (rd-of ext)))
    (sb-thread:with-mutex ((rd-lock rd)) (remhash (let ((k (jget args "key"))) (if (stringp k) k "")) (rd-render-cards rd)))))

(defun rd-render (ext ctx req kind member)
  (let* ((rd (rd-of ext)) (custom-type (jget req "tool" "")) (args (prov-object (jget req "args")))
         (view (gethash custom-type (if (eq kind :message) (rd-message-views rd) (rd-entry-views rd))))
         (handler (or view (gethash custom-type (if (eq kind :message) (rd-message-handlers rd) (rd-entry-handlers rd)))))
         (on-event (gethash custom-type (if (eq kind :message) (rd-message-events rd) (rd-entry-events rd))))
         (card (rd-render-card rd (let ((k (jget args "key"))) (if (stringp k) k ""))))
         (width (let ((w (jget args "width"))) (if (integerp w) w 0)))
         (events (let ((e (jget args "events"))) (if (vectorp e) e (vector))))
         (failure nil))
    (unless handler (error "unknown ~(~A~) renderer ~S" kind custom-type))
    (sb-thread:with-mutex ((render-card-lock card))
      (unless (render-card-subject card) (setf (render-card-subject card) (prov-object (jget args member))))
      (let ((options (prov-object (jget args "options"))) (subject (render-card-subject card)))
        (setf (gethash options *render-option-states*) (render-card-state card))
        (when on-event
          (loop for wire across events
                do (let ((event (and (hash-table-p wire) (nth-value 1 (kit-event-from-wire wire)))))
                     (when event
                       (handler-case (funcall on-event ctx subject options event)
                         (error (e) (unless failure (setf failure e))))))))
        (let ((result (rd-result ext (funcall handler ctx subject options width) (and view t))))
          (when failure (error failure))
          result)))))

(def-request "render_message" (ext ctx req) (rd-render ext ctx req :message "message"))
(def-request "render_entry" (ext ctx req) (rd-render ext ctx req :entry "entry"))

(defun markdown-transformer (ext transformer &key (message-types nil message-types-p))
  "Register the extension's display-only Markdown transform: TRANSFORMER is (lambda (markdown context)) -> string; any other value, or
a signalled condition, keeps the input. A later registration replaces an earlier one. MESSAGE-TYPES (Pi's options.messageTypes) is a list of the
surfaces it runs on: \"user\", \"assistant\", \"assistant-thinking\" (the default), \"custom\" (custom messages without a message renderer,
expanded skill blocks), \"summary\" (expanded branch and compaction summaries) and \"changelog\" (What's New, /changelog, Extension Updates,
the Update Available note); NIL means none."
  (setf (rd-markdown (rd-of ext)) transformer
        (rd-markdown-message-types (rd-of ext)) (if message-types-p (or message-types :none) :default))
  nil)

; pig additive (D214)
(defun own-thinking-runs (ext &optional (enabled t))
  "Declare that the extension owns the thinking runs of assistant messages: a hidden or zero-row thinking run then leaves no blank
spacer rows around it. Before the extension is live this records the value for the register payload (a later call wins; no argument
means on); once live it calls the host. The host ORs the flag over extensions and drops it when the extension unloads."
  (let ((live (sb-thread:with-mutex ((extension-reg-lock ext))
                (setf (rd-owns-thinking-runs (rd-of ext)) (and enabled t))
                (extension-live ext))))
    (when live
      (host-call ext "ownThinkingRuns" (obj "enabled" (if enabled t :false)))))
  nil)

; pig additive (D214)
(defun ctx-own-thinking-runs (ctx &optional (enabled t))
  "Change, while the extension runs, whether it owns the thinking runs of assistant messages (see OWN-THINKING-RUNS)."
  (ctx-call-host ctx "ownThinkingRuns" (obj "enabled" (if enabled t :false)))
  nil)

(def-request "markdown_transform" (ext ctx req)
  (let ((transformer (rd-markdown (rd-of ext))) (args (prov-object (jget req "args"))))
    (and transformer
         (handler-case (let ((result (funcall transformer (let ((m (jget args "markdown"))) (if (stringp m) m ""))
                                              (prov-object (jget args "context")))))
                         (and (stringp result) result))
           (error () nil)))))

;;; ---------------------------------------------------------------- tool renderers

(defun tool-renderers (ext name &key render-call render-result (render-shell "default") call-view result-view)
  "Set the renderers of tool NAME and publish its updated declaration to a running session (see the tool-renderers struct for the
function arguments). A tool this extension did not register is left alone."
  (unless (member render-shell '("default" "self") :test #'equal) (error "render-shell must be \"default\" or \"self\""))
  (sb-thread:with-mutex ((extension-reg-lock ext))
    (let ((declaration (find name (extension-tools ext) :key (lambda (d) (gethash "name" d)) :test #'equal)))
      (when declaration
        (setf (gethash "render_shell" declaration) render-shell
              (gethash "renders_call" declaration) (prov-bool (or render-call call-view))
              (gethash "renders_result" declaration) (prov-bool (or render-result result-view)))
        (let ((hooks (copy-list (gethash name (extension-tool-hooks ext)))))
          (loop for (key value) in (list (list :render-call render-call) (list :render-result render-result)
                                         (list :call-view call-view) (list :result-view result-view))
                do (remf hooks key)
                   (when value (setf (getf hooks key) value)))
          (if hooks (setf (gethash name (extension-tool-hooks ext)) hooks) (remhash name (extension-tool-hooks ext))))
        (when (extension-live ext) (host-call ext "registerTool" declaration)))))
  nil)

(defstruct tool-card
  "One tool card: its renderer lock, its STATE and the last output of each phase's renderer (LAST-COMPONENT)."
  (mutex (sb-thread:make-mutex :name "pig card")) (state (obj)) call result)

(defun rd-card (rd card)
  (sb-thread:with-mutex ((rd-lock rd))
    (or (gethash card (rd-cards rd)) (setf (gethash card (rd-cards rd)) (make-tool-card)))))

(defun tool-hook (ext name key)
  (getf (sb-thread:with-mutex ((extension-reg-lock ext)) (gethash name (extension-tool-hooks ext))) key))

(def-notify "tool_render_release" (ext args)
  (let ((rd (rd-of ext))) (sb-thread:with-mutex ((rd-lock rd)) (remhash (let ((c (jget args "card"))) (if (stringp c) c "")) (rd-cards rd)))))

(def-request "render_tool" (ext ctx req)
  (let* ((rd (rd-of ext)) (name (jget req "tool" "")) (args (prov-object (jget req "args")))
         (card-id (let ((c (jget args "card"))) (if (stringp c) c ""))) (card (rd-card rd card-id)))
    (sb-thread:with-mutex ((tool-card-mutex card))
      (let* ((wire (prov-object (jget args "context")))
             (render (make-tool-render-context
                      :args (prov-object (jget args "args"))
                      :tool-call-id (let ((v (jget wire "toolCallId"))) (if (stringp v) v ""))
                      :cwd (let ((v (jget wire "cwd"))) (if (stringp v) v ""))
                      :execution-started (eq (jget wire "executionStarted") t) :args-complete (eq (jget wire "argsComplete") t)
                      :is-partial (eq (jget wire "isPartial") t) :expanded (eq (jget wire "expanded") t)
                      :show-images (eq (jget wire "showImages") t) :is-error (eq (jget wire "isError") t)
                      :output-pad (let ((v (jget wire "outputPad"))) (if (integerp v) v 0))
                      :duration-ms (let ((v (jget wire "durationMs"))) (and (realp v) (round v)))
                      :last-component (if (equal (jget args "phase") "result") (shiftf (tool-card-result card) nil) (shiftf (tool-card-call card) nil))
                      :state (tool-card-state card)
                      :invalidate (lambda () (notify-host ext "tool_render_invalidate" (obj "card" card-id)))))
             (width (let ((w (jget args "width"))) (if (integerp w) w 0)))
             (resolved (let ((id (jget args "renderers"))) (and (stringp id) (plusp (length id)) (gethash id (rd-resolved rd)))))
             (result-phase (equal (jget args "phase") "result")))
        (multiple-value-bind (view lines)
            (cond (resolved (if result-phase
                                (values (tool-renderers-result-view resolved) (tool-renderers-render-result resolved))
                                (values (tool-renderers-call-view resolved) (tool-renderers-render-call resolved))))
                  (result-phase (values (tool-hook ext name :result-view) (tool-hook ext name :render-result)))
                  (t (values (tool-hook ext name :call-view) (tool-hook ext name :render-call))))
          (let ((handler (or view lines)))
            (unless handler (error "tool ~A has no ~A renderer" name (if result-phase "result" "call")))
            (let ((rendered (if result-phase
                                (funcall handler ctx (let ((r (jget args "result"))) (if (hash-table-p r) r (obj "content" (vector))))
                                         (prov-object (jget args "options")) render width)
                                (funcall handler ctx (prov-object (jget args "args")) render width))))
              ;; a handler that signalled left no component: LAST-COMPONENT was cleared above and is not restored
              (if result-phase (setf (tool-card-result card) rendered) (setf (tool-card-call card) rendered))
              (rd-result ext rendered (and view t)))))))))

(defun tool-renderer (ext resolver)
  "Register a tool renderer resolver, (lambda (tool next)) -> tool-renderers or NIL. NEXT is a function without arguments returning the
renderers the remaining resolvers, then the registered tool, would use; the host's own renderers are a tool-renderers without render
functions: returning it keeps them. Resolvers run in extension load order, an extension's in registration order."
  (unless (functionp resolver) (error "tool-renderer requires a function"))
  (let* ((rd (rd-of ext))
         (count (sb-thread:with-mutex ((rd-lock rd))
                  (setf (rd-resolvers rd) (append (rd-resolvers rd) (list resolver)))
                  (length (rd-resolvers rd)))))
    (when (sb-thread:with-mutex ((extension-reg-lock ext)) (extension-live ext))
      (notify-host ext "tool_renderers" (obj "count" count)))
    nil))

(def-request "resolve_tool_renderers" (ext ctx req)
  (let* ((rd (rd-of ext)) (args (prov-object (jget req "args"))) (wire-next (jget args "next"))
         (marker (and (hash-table-p wire-next)
                      (make-tool-renderers :render-shell (let ((s (jget wire-next "render_shell"))) (if (and (stringp s) (plusp (length s))) s "default")))))
         (resolvers (sb-thread:with-mutex ((rd-lock rd)) (copy-list (rd-resolvers rd))))
         (tool (let ((tool (jget args "tool"))) (if (stringp tool) tool ""))))
    (labels ((resolve (index)
               (if (< index (length resolvers))
                   (funcall (nth index resolvers) tool (lambda () (resolve (1+ index))))
                   marker)))
      (let ((got (resolve 0)))
        (if (null got)
            (obj "use" "none")
            (let ((renders-call (and (or (tool-renderers-render-call got) (tool-renderers-call-view got)) t))
                  (renders-result (and (or (tool-renderers-render-result got) (tool-renderers-result-view got)) t)))
              (if (and (eq got marker) (not renders-call) (not renders-result))
                  (obj "use" "next")
                  (let ((id (sb-thread:with-mutex ((rd-lock rd))
                              (let ((id (format nil "r~D" (1+ (hash-table-count (rd-resolved rd))))))
                                (setf (gethash id (rd-resolved rd)) got)
                                id)))
                        (out (obj "use" "own")))
                    (setf (gethash "renderers" out) id)
                    (when (equal (tool-renderers-render-shell got) "self") (setf (gethash "render_shell" out) "self"))
                    (when renders-call (setf (gethash "renders_call" out) t))
                    (when renders-result (setf (gethash "renders_result" out) t))
                    out))))))))

(def-register-contributor "renderers" (ext payload)
  (let ((rd (rd-of ext)))
    (setf (gethash "message_renderers" payload) (prov-vec (rd-message-declarations rd))
          (gethash "entry_renderers" payload) (prov-vec (rd-entry-declarations rd)))
    (when (rd-markdown rd)
      (setf (gethash "markdown_transformer" payload) t
            (gethash "markdown_message_types" payload)
            (let ((types (rd-markdown-message-types rd)))
              (cond ((eq types :none) (prov-vec nil))
                    ((listp types) (prov-vec types))
                    (t (prov-vec (list "user" "assistant" "assistant-thinking")))))))
    (when (rd-owns-thinking-runs rd) (setf (gethash "owns_thinking_runs" payload) t))
    (when (rd-resolvers rd) (setf (gethash "tool_renderers" payload) (length (rd-resolvers rd))))))

;;; ---------------------------------------------------------------- login and sprite definitions

(defstruct login-definition "Semantic pixel art and labels for the host's native login template." brand hero mascot palette name description tagline)
(defstruct sprite-definition "One sprite of the /sprite catalogue: MASCOT is a 16-by-14 grid of palette symbols (\".\" is transparent)." id name tagline mascot palette)

(defun ctx-set-login (ctx definition)
  "Set the shared header from a native login definition. The host validates it: a rejected definition signals host-call-error."
  (ctx-call-host ctx "ui.setLogin"
                 (obj "brand" (prov-vec (login-definition-brand definition)) "hero" (prov-vec (login-definition-hero definition))
                      "mascot" (prov-vec (login-definition-mascot definition))
                      "palette" (prov-copy-table (login-definition-palette definition))
                      "name" (login-definition-name definition) "description" (login-definition-description definition)
                      "tagline" (login-definition-tagline definition)))
  nil)

(defun ctx-register-sprite (ctx definition)
  "Add a sprite to /sprite. The host validates it: a rejected definition signals host-call-error. The same ID again replaces this
extension's sprite, which leaves /sprite when the extension unloads."
  (ctx-call-host ctx "ui.registerSprite"
                 (obj "id" (sprite-definition-id definition) "name" (sprite-definition-name definition)
                      "tagline" (sprite-definition-tagline definition) "mascot" (prov-vec (sprite-definition-mascot definition))
                      "palette" (prov-copy-table (sprite-definition-palette definition))))
  nil)
