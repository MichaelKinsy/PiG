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
  "What the host tells a tool renderer. STATE is a hash-table that lives as long as the card; INVALIDATE is a function without arguments."
  (args (obj)) (tool-call-id "") (cwd "") execution-started args-complete is-partial expanded show-images is-error (output-pad 0)
  duration-ms (state (obj)) invalidate)

(defstruct (rd-state (:conc-name rd-))
  (message-declarations '()) (message-handlers (make-hash-table :test 'equal)) (message-views (make-hash-table :test 'equal))
  (entry-declarations '()) (entry-handlers (make-hash-table :test 'equal)) (entry-views (make-hash-table :test 'equal))
  (markdown nil)
  (call-lines (make-hash-table :test 'equal)) (result-lines (make-hash-table :test 'equal))
  (call-views (make-hash-table :test 'equal)) (result-views (make-hash-table :test 'equal))
  (resolvers '()) (resolved (make-hash-table :test 'equal))
  (cards (make-hash-table :test 'equal))      ; card -> (lock . state)
  (lock (sb-thread:make-mutex :name "pig renderers")))

(defun rd-of (ext) (extension-extra ext :renderer #'make-rd-state))

(declaim (ftype function kit-render-result))

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
  (rd-register ext :entry custom-type handler t))

(defun rd-render (ext ctx req kind member)
  (let* ((rd (rd-of ext)) (custom-type (jget req "tool" "")) (args (prov-object (jget req "args")))
         (view (gethash custom-type (if (eq kind :message) (rd-message-views rd) (rd-entry-views rd))))
         (handler (or view (gethash custom-type (if (eq kind :message) (rd-message-handlers rd) (rd-entry-handlers rd))))))
    (unless handler (error "unknown ~(~A~) renderer ~S" kind custom-type))
    (rd-result ext (funcall handler ctx (prov-object (jget args member)) (prov-object (jget args "options"))
                            (let ((w (jget args "width"))) (if (integerp w) w 0)))
               (and view t))))

(def-request "render_message" (ext ctx req) (rd-render ext ctx req :message "message"))
(def-request "render_entry" (ext ctx req) (rd-render ext ctx req :entry "entry"))

(defun markdown-transformer (ext transformer)
  "Register the extension's display-only Markdown transform: TRANSFORMER is (lambda (markdown context)) -> string; any other value, or
a signalled condition, keeps the input. A later registration replaces an earlier one."
  (setf (rd-markdown (rd-of ext)) transformer)
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
  (let ((rd (rd-of ext)))
    (sb-thread:with-mutex ((extension-reg-lock ext))
      (let ((declaration (find name (extension-tools ext) :key (lambda (d) (gethash "name" d)) :test #'equal)))
        (when declaration
          (setf (gethash "render_shell" declaration) render-shell
                (gethash "renders_call" declaration) (prov-bool (or render-call call-view))
                (gethash "renders_result" declaration) (prov-bool (or render-result result-view)))
          (loop for (table value) in (list (list (rd-call-lines rd) render-call) (list (rd-result-lines rd) render-result)
                                           (list (rd-call-views rd) call-view) (list (rd-result-views rd) result-view))
                do (if value (setf (gethash name table) value) (remhash name table)))
          (when (extension-live ext) (host-call ext "registerTool" declaration))))))
  nil)

(defun rd-card (rd card)
  (sb-thread:with-mutex ((rd-lock rd))
    (or (gethash card (rd-cards rd)) (setf (gethash card (rd-cards rd)) (cons (sb-thread:make-mutex :name "pig card") (obj))))))

(def-notify "tool_render_release" (ext args)
  (let ((rd (rd-of ext))) (sb-thread:with-mutex ((rd-lock rd)) (remhash (let ((c (jget args "card"))) (if (stringp c) c "")) (rd-cards rd)))))

(def-request "render_tool" (ext ctx req)
  (let* ((rd (rd-of ext)) (name (jget req "tool" "")) (args (prov-object (jget req "args")))
         (card-id (let ((c (jget args "card"))) (if (stringp c) c ""))) (card (rd-card rd card-id)))
    (sb-thread:with-mutex ((car card))
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
                      :state (cdr card)
                      :invalidate (lambda () (notify-host ext "tool_render_invalidate" (obj "card" card-id)))))
             (width (let ((w (jget args "width"))) (if (integerp w) w 0)))
             (resolved (let ((id (jget args "renderers"))) (and (stringp id) (plusp (length id)) (gethash id (rd-resolved rd)))))
             (result-phase (equal (jget args "phase") "result")))
        (multiple-value-bind (view lines)
            (cond (resolved (if result-phase
                                (values (tool-renderers-result-view resolved) (tool-renderers-render-result resolved))
                                (values (tool-renderers-call-view resolved) (tool-renderers-render-call resolved))))
                  (result-phase (values (gethash name (rd-result-views rd)) (gethash name (rd-result-lines rd))))
                  (t (values (gethash name (rd-call-views rd)) (gethash name (rd-call-lines rd)))))
          (let ((handler (or view lines)))
            (unless handler (error "tool ~A has no ~A renderer" name (if result-phase "result" "call")))
            (rd-result ext
                       (if result-phase
                           (funcall handler ctx (let ((r (jget args "result"))) (if (hash-table-p r) r (obj "content" (vector))))
                                    (prov-object (jget args "options")) render width)
                           (funcall handler ctx (prov-object (jget args "args")) render width))
                       (and view t))))))))

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
    (when (rd-markdown rd) (setf (gethash "markdown_transformer" payload) t))
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
