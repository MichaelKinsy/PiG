;;;; SPDX-License-Identifier: MIT
;;;; Widgets, footer, header and the working-indicator members of Pi's ctx.ui. A surface takes rows (a list of strings), a renderer that
;;;; lays rows out at the host's terminal width (the subprocess form of Pi's component factory), or a kit view the host renders itself.

(in-package #:pig)

(defun ctx-width (ctx)
  "The terminal width in columns the host last reported (0 before one arrives)."
  (sb-thread:with-mutex ((extension-state-lock (ctx-extension ctx))) (extension-width (ctx-extension ctx))))

(defun ctx-height (ctx)
  "The terminal height in rows, or 0 when the host has not reported one."
  (sb-thread:with-mutex ((extension-state-lock (ctx-extension ctx))) (extension-height (ctx-extension ctx))))

(defun widget-rpc-p (ctx)
  (equal (sb-thread:with-mutex ((extension-state-lock (ctx-extension ctx))) (extension-mode (ctx-extension ctx))) "rpc"))

;;; ---------------------------------------------------------------- working indicator and other one-call members

(defun ctx-set-working-message (ctx &optional message)
  "Set the working label. NIL (or no argument) restores the default label; an empty string is a message and shows an empty label."
  (ctx-call-host ctx "ui.setWorkingMessage" (if message (obj "message" message) (obj))) nil)

(defun ctx-set-working-visible (ctx visible)
  (ctx-call-host ctx "ui.setWorkingVisible" (obj "visible" (if visible t :false))) nil)

(defun ctx-set-working-indicator (ctx options)
  "OPTIONS is the indicator object (obj \"frames\" #(\"-\" \"+\") \"intervalMs\" 120)."
  (ctx-call-host ctx "ui.setWorkingIndicator" options) nil)

(defun ctx-set-hidden-thinking-label (ctx &optional label)
  "Set the hidden-thinking label. NIL restores the default label; an empty string is a label and shows an empty one."
  (ctx-call-host ctx "ui.setHiddenThinkingLabel" (if label (obj "label" label) (obj))) nil)

(defun ctx-set-tools-expanded (ctx expanded)
  (ctx-call-host ctx "ui.setToolsExpanded" (obj "expanded" (if expanded t :false))) nil)

(defun ctx-bell (ctx)
  "Ring the terminal bell of the interactive terminal between frames. Nothing happens where there is no terminal UI."
  (ctx-call-host ctx "ui.bell" (obj)) nil)

;;; ---------------------------------------------------------------- renderers

(defstruct (widget-surface (:constructor widget-make-surface (method render key options)))
  "A footer, header or widget renderer. The host paints its rows only at the width they were laid out for."
  method render key options (lock (sb-thread:make-mutex :name "pig surface")) (stopped nil))

(defun widget-surface-args (lines width)
  (let ((args (obj "lines" lines))) (when (plusp width) (setf (gethash "width" args) width)) args))

(defun widget-surface-kind (surface)
  (let ((method (widget-surface-method surface)))
    (cond ((equal method "ui.setWidget") "widget") ((equal method "ui.setHeader") "header") (t "footer"))))

(defun widget-push (surface ctx)
  "Render at the host's width and send the rows with it. A render that signals is reported to the user and leaves the previous rows."
  (sb-thread:with-mutex ((widget-surface-lock surface))
    (unless (widget-surface-stopped surface)
      (let* ((width (ctx-width ctx)) (lines nil) (failed nil))
        (handler-case
            (let ((rendered (funcall (widget-surface-render surface) width)))
              (setf lines (if (or (listp rendered) (and (vectorp rendered) (not (stringp rendered))))
                              (map 'simple-vector #'princ-to-string rendered)
                              (vector))))
          (error (e)
            (setf failed t)
            (ctx-notify ctx (format nil "~A render failed: ~A" (widget-surface-kind surface) e) "error")))
        (unless failed
          (let ((args (widget-surface-args lines width)))
            (when (equal (widget-surface-method surface) "ui.setWidget")
              (setf (gethash "key" args) (widget-surface-key surface))
              (when (widget-surface-options surface) (setf (gethash "options" args) (widget-surface-options surface))))
            (ctx-call-host ctx (widget-surface-method surface) args)))))))

(defun widget-replace-renderer (ext id surface)
  "Retire the renderer installed under ID and install SURFACE (NIL for none)."
  (let* ((kit (kit-state ext)) previous)
    (sb-thread:with-mutex ((kit-state-renderers-lock kit))
      (setf previous (gethash id (kit-state-renderers kit)))
      (if surface (setf (gethash id (kit-state-renderers kit)) surface) (remhash id (kit-state-renderers kit))))
    (when previous (sb-thread:with-mutex ((widget-surface-lock previous)) (setf (widget-surface-stopped previous) t)))))

(defun widget-replace-surface (ext method surface)
  "Retire the renderer or view installed for METHOD and install SURFACE (NIL for none)."
  (let ((kit (kit-state ext)))
    (sb-thread:with-mutex ((kit-state-surfaces-lock kit)) (remhash method (kit-state-surfaces kit))))
  (widget-replace-renderer ext method surface))

(defun widget-refresh-all (ext width)
  (when (eql width (sb-thread:with-mutex ((extension-state-lock ext)) (extension-width ext)))
    (let ((kit (kit-state ext)))
      (dolist (surface (sb-thread:with-mutex ((kit-state-renderers-lock kit))
                         (loop for s being the hash-values of (kit-state-renderers kit) collect s)))
        (handler-case (widget-push surface (make-ctx ext)) (error () nil))))))

(defun kit-ensure-width-subscription (ext)
  "Render the extension's renderers and overlays again after every terminal resize (subscribed once)."
  (let ((kit (kit-state ext)) subscribe)
    (sb-thread:with-mutex ((kit-state-lock kit))
      (unless (kit-state-width-subscribed kit) (setf (kit-state-width-subscribed kit) t subscribe t)))
    (when subscribe
      (ctx-on-width-change (make-ctx ext) (lambda (width) (widget-refresh-all ext width) (uc-render-all ext))))))

;;; ---------------------------------------------------------------- widgets

(defun widget-string-list-p (content)
  (and (or (consp content) (and (vectorp content) (not (stringp content)))) (every #'stringp content)))

(defun widget-send-view (ctx key view options)
  (if (null options)
      (send-frame (extension-conn (ctx-extension ctx)) (obj "type" "widget_push" "widget_push" (obj "key" key "view" view)))
      (ctx-call-host ctx "ui.setWidget" (obj "key" key "view" view "options" options))))

(defun widget-set-view (ctx key view options)
  (let* ((ext (ctx-extension ctx)) (kit (kit-state ext)) (encoded (kit-encode ext view)))
    (widget-replace-renderer ext (format nil "ui.setWidget:~A" key) nil)
    ;; held across the send, so an eviction's resend and this frame keep their order
    (sb-thread:with-mutex ((kit-state-widgets-lock kit))
      (setf (gethash key (kit-state-widgets kit)) (cons encoded options))
      (widget-send-view ctx key (kit-wire ext encoded) options))))

(defun ctx-set-widget (ctx key content &optional options)
  "Set or clear a widget. CONTENT is a list or vector of strings (each becomes Text(line, 1, 0) at the host's width, the first ten shown),
a kit view the host renders at the widget's width, or NIL to clear. A string list or a view with no OPTIONS goes out as a widget_push
frame the host applies in arrival order and never answers, so it may be called from a width handler; in RPC mode a list waits for the
host like every other shape. OPTIONS is the placement object (obj \"placement\" \"belowEditor\")."
  (if (kit-view-p content)
      (widget-set-view ctx key content options)
      (let* ((ext (ctx-extension ctx)) (kit (kit-state ext)))
        (sb-thread:with-mutex ((kit-state-widgets-lock kit)) (remhash key (kit-state-widgets kit)))
        (widget-replace-renderer ext (format nil "ui.setWidget:~A" key) nil)
        (if (and (widget-string-list-p content) (null options) (not (widget-rpc-p ctx)))
            (send-frame (extension-conn ext) (obj "type" "widget_push" "widget_push" (obj "key" key "lines" (coerce content 'simple-vector))))
            (ctx-call-host ctx "ui.setWidget" (obj "key" key "content" (if (null content) :null content) "options" (or options (obj)))))))
  nil)

(defun ctx-set-widget-renderer (ctx key render &optional options)
  "Install the widget KEY with RENDER, (lambda (width)) -> rows, laid out at the host's terminal width and again after every resize. NIL
clears the widget. OPTIONS is the placement. A render that signals is reported as \"widget render failed: ...\" and leaves the previous rows."
  (if (null render)
      (ctx-set-widget ctx key nil options)
      (let* ((ext (ctx-extension ctx)) (kit (kit-state ext))
             (surface (widget-make-surface "ui.setWidget" render key options)))
        (kit-ensure-width-subscription ext)
        (sb-thread:with-mutex ((kit-state-widgets-lock kit)) (remhash key (kit-state-widgets kit)))
        (widget-replace-renderer ext (format nil "ui.setWidget:~A" key) surface)
        (widget-push surface ctx)))
  nil)

;;; ---------------------------------------------------------------- footer and header

(defun widget-set-rows (ctx method lines)
  (widget-replace-surface (ctx-extension ctx) method nil)
  (if (null lines)
      (ctx-call-host ctx method (obj "clear" t))
      (ctx-call-host ctx method (widget-surface-args (map 'simple-vector #'princ-to-string lines) (ctx-width ctx))))
  nil)

(defun widget-set-view-surface (ctx method view)
  (let* ((ext (ctx-extension ctx)) (kit (kit-state ext)) (encoded (kit-encode ext view)))
    (widget-replace-surface ext method nil)
    (sb-thread:with-mutex ((kit-state-surfaces-lock kit))
      (setf (gethash method (kit-state-surfaces kit)) encoded)
      (ctx-call-host ctx method (obj "view" (kit-wire ext encoded)))))
  nil)

(defun widget-set-renderer-surface (ctx method render)
  (cond ((null render) (widget-set-rows ctx method nil))
        ;; Pi calls a footer or header factory only in the TUI (rpc-mode.ts setFooter/setHeader ignore it, as does runner.ts noOpUIContext)
        ((or (not (ctx-has-ui-p ctx)) (widget-rpc-p ctx)) nil)
        (t (let ((surface (widget-make-surface method render nil nil)))
             (kit-ensure-width-subscription (ctx-extension ctx))
             (widget-replace-surface (ctx-extension ctx) method surface)
             (widget-push surface ctx)
             nil))))

(defun ctx-set-footer (ctx lines)
  "Replace the footer with pre-rendered rows (a list or vector of strings), or clear it for NIL. The rows carry the width the SDK holds
when they are sent and the host paints them only at that width; a footer that must never be wrong uses CTX-SET-FOOTER-RENDERER. Setting
rows replaces any footer renderer."
  (widget-set-rows ctx "ui.setFooter" lines))
(defun ctx-clear-footer (ctx) (widget-set-rows ctx "ui.setFooter" nil))
(defun ctx-set-footer-renderer (ctx render)
  "Install a footer that RENDER, (lambda (width)) -> rows, lays out at the host's terminal width, again after every resize and when the
host's footer data changes. NIL restores the built-in footer. A render that signals is reported as \"footer render failed: ...\"."
  (widget-set-renderer-surface ctx "ui.setFooter" render))
(defun ctx-set-footer-view (ctx view)
  "Replace the footer with a kit view the host renders at its width every frame; CTX-CLEAR-FOOTER restores the default."
  (widget-set-view-surface ctx "ui.setFooter" view))

(defun ctx-set-header (ctx lines) "As CTX-SET-FOOTER, for the header." (widget-set-rows ctx "ui.setHeader" lines))
(defun ctx-clear-header (ctx) (widget-set-rows ctx "ui.setHeader" nil))
(defun ctx-set-header-renderer (ctx render) "As CTX-SET-FOOTER-RENDERER, for the header." (widget-set-renderer-surface ctx "ui.setHeader" render))
(defun ctx-set-header-view (ctx view) "As CTX-SET-FOOTER-VIEW, for the header." (widget-set-view-surface ctx "ui.setHeader" view))

;;; ---------------------------------------------------------------- eviction and footer data

(defun kit-resend-surfaces (ext refs)
  "Send again, with the image data, the latest frame of every widget, header and footer view that names an evicted image."
  (let* ((kit (kit-state ext)) (ctx (make-ctx ext)))
    (handler-case
        (progn
          (sb-thread:with-mutex ((kit-state-widgets-lock kit))
            (loop for key being the hash-keys of (kit-state-widgets kit) using (hash-value entry)
                  do (when (intersection refs (kit-encoded-refs (car entry)) :test #'string=)
                       (widget-send-view ctx key (kit-wire ext (car entry)) (cdr entry)))))
          (sb-thread:with-mutex ((kit-state-surfaces-lock kit))
            (loop for method being the hash-keys of (kit-state-surfaces kit) using (hash-value encoded)
                  do (when (intersection refs (kit-encoded-refs encoded) :test #'string=)
                       (ctx-call-host ctx method (obj "view" (kit-wire ext encoded)))))))
      (error (e) (unless (extension-shutdown ext) (format *error-output* "pig: resend evicted view images: ~A~%" e))))))

(defun widget-footer-key (data)
  (to-json (obj "b" (let ((b (jget data "gitBranch"))) (if (and (stringp b) (plusp (length b))) b :null))
                "s" (let ((s (jget data "extensionStatuses"))) (if (hash-table-p s) s (obj)))
                "c" (let ((c (jget data "availableProviderCount"))) (if (integerp c) c 0)))))

(defun widget-note-footer (ext data refresh)
  "Record the footer data a state carries; when it changed and REFRESH, render the footer renderer again."
  (when (hash-table-p data)
    (let* ((kit (kit-state ext)) (key (widget-footer-key data)) changed)
      (sb-thread:with-mutex ((kit-state-lock kit))
        (setf changed (not (equal key (or (kit-state-footer-json kit) (widget-footer-key (obj)))))
              (kit-state-footer-json kit) key))
      (when (and refresh changed)
        (let ((footer (sb-thread:with-mutex ((kit-state-renderers-lock kit)) (gethash "ui.setFooter" (kit-state-renderers kit)))))
          (when footer (kit-run-job ext (lambda () (widget-push footer (make-ctx ext))))))))))

(def-ready-hook "widgets" (ext ready) (widget-note-footer ext (jget (jget ready "state") "footerData") nil))
(def-state-hook "widgets" (ext state) (widget-note-footer ext (jget state "footerData") t))
