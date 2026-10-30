;;;; SPDX-License-Identifier: MIT
;;;; Overlays: the width a focused overlay component renders at (Pi's TUI.resolveOverlayLayout) and the OverlayHandle ctx-custom hands
;;;; to its ON-HANDLE callback.

(in-package #:pig)

(defun overlay-number-p (value) (realp value))

(defun overlay-parse-size (value reference)
  "Pi's parseSizeValue (tui.ts:228-237): a number is cells, \"N%\" a floored share of REFERENCE, anything else is unset (NIL)."
  (cond ((overlay-number-p value) (float value 1d0))
        ((and (stringp value) (> (length value) 1) (char= (char value (1- (length value))) #\%))
         (let* ((body (subseq value 0 (1- (length value)))) (dot (position #\. body)))
           (flet ((digits-p (s) (and (plusp (length s)) (every #'digit-char-p s))))
             (when (if dot (and (digits-p (subseq body 0 dot)) (digits-p (subseq body (1+ dot)))) (digits-p body))
               (let ((percent (if dot
                                  (+ (parse-integer body :end dot)
                                     (/ (parse-integer body :start (1+ dot)) (expt 10 (- (length body) dot 1))))
                                  (parse-integer body))))
                 (float (floor (/ (* (float reference 1d0) (float percent 1d0)) 100)) 1d0))))))))

(defun overlay-resolve-width (layout terminal-width)
  "The width rule of upstream TUI.resolveOverlayLayout (tui.ts:1212-1233)."
  (let* ((margin (jget layout "margin"))
         (left (cond ((overlay-number-p margin) (float margin 1d0))
                     ((hash-table-p margin) (let ((v (jget margin "left"))) (if (overlay-number-p v) (float v 1d0) 0d0)))
                     (t 0d0)))
         (right (cond ((overlay-number-p margin) (float margin 1d0))
                      ((hash-table-p margin) (let ((v (jget margin "right"))) (if (overlay-number-p v) (float v 1d0) 0d0)))
                      (t 0d0)))
         (available (max 1d0 (- terminal-width (max 0d0 left) (max 0d0 right))))
         (width (or (overlay-parse-size (jget layout "width") terminal-width) (min 80d0 available))))
    (when (overlay-number-p (jget layout "minWidth")) (setf width (max width (float (jget layout "minWidth") 1d0))))
    (values (truncate (max 1d0 (min width available))))))

(defun overlay-render-width (options)
  "Pi renders an overlay component at the width TUI.resolveOverlayLayout resolves, and an inline component at the terminal width. The
host composites an overlay with the same layout unless it opens the legacy titled modal (no overlayOptions, with title, widthFraction or
heightFraction), which renders at the terminal width. A function of the terminal width."
  (if (not (eq (jget options "overlay") t))
      #'identity
      (let ((layout (jget options "overlayOptions"))
            (legacy (or (let ((title (jget options "title"))) (and (stringp title) (plusp (length title))))
                        (let ((f (jget options "widthFraction"))) (and (overlay-number-p f) (plusp f)))
                        (let ((f (jget options "heightFraction"))) (and (overlay-number-p f) (plusp f))))))
        (cond ((hash-table-p layout) (lambda (width) (overlay-resolve-width layout width)))
              (legacy #'identity)
              (t (let ((empty (obj))) (lambda (width) (overlay-resolve-width empty width))))))))

;;; ---------------------------------------------------------------- the handle

(defstruct (overlay-handle (:constructor make-overlay-handle (ext key ov)))
  "Pi's OverlayHandle of a mounted overlay. Each control is a host call that returns after the host applied it, so OVERLAY-HIDDEN-P,
OVERLAY-FOCUSED-P and OVERLAY-GET-BOUNDS read the state it returned. After the overlay closed they read the last state and the controls
do nothing, except OVERLAY-SET-HIDDEN, which records its value."
  ext key ov)

(defvar *overlay-editor-target-p* nil
  "A function (ext component) true when COMPONENT is the editor component the extension installed, for OVERLAY-UNFOCUS's :target.")

(defun overlay-control (handle action &key (hidden nil hidden-p) target)
  (let ((ov (overlay-handle-ov handle)))
    (unless (uc-ov-active ov)
      (when (equal action "setHidden")
        (sb-thread:with-mutex ((uc-ov-lock ov)) (setf (uc-ov-hidden ov) hidden)))
      (return-from overlay-control nil))
    (let* ((args (obj "key" (overlay-handle-key handle) "action" action))
           (state (progn (when hidden-p (setf (gethash "hidden" args) (if hidden t :false)))
                         (when target (setf (gethash "target" args) target))
                         (ctx-call-host (make-ctx (overlay-handle-ext handle)) "ui.custom.control" args))))
      (when (hash-table-p state) (uc-apply-handle-state ov state))
      nil)))

(defun overlay-hide (handle) "Remove the overlay permanently." (overlay-control handle "hide"))
(defun overlay-set-hidden (handle hidden) (overlay-control handle "setHidden" :hidden (and hidden t)))
(defun overlay-hidden-p (handle)
  (let ((ov (overlay-handle-ov handle))) (sb-thread:with-mutex ((uc-ov-lock ov)) (uc-ov-hidden ov))))
(defun overlay-focus (handle) (overlay-control handle "focus"))
(defun overlay-focused-p (handle)
  (let ((ov (overlay-handle-ov handle))) (sb-thread:with-mutex ((uc-ov-lock ov)) (uc-ov-focused ov))))

(defun overlay-get-bounds (handle)
  "The overlay's {row, col, width, height} object while it is visible, else NIL."
  (let ((ov (overlay-handle-ov handle)))
    (sb-thread:with-mutex ((uc-ov-lock ov))
      (and (uc-ov-visible ov) (hash-table-p (uc-ov-bounds ov)) (uc-ov-bounds ov)))))

(defun overlay-focus-target (handle target)
  (cond ((null target) (obj "kind" "null"))
        (t (let* ((ext (overlay-handle-ext handle)) (kit (kit-state ext)))
             (or (sb-thread:with-mutex ((kit-state-lock kit))
                   (loop for key being the hash-keys of (kit-state-overlays kit) using (hash-value ov)
                         when (and (uc-ov-active ov) (eq (uc-ov-component ov) target)) return (obj "kind" "overlay" "key" key)))
                 (and *overlay-editor-target-p* (funcall *overlay-editor-target-p* ext target) (obj "kind" "editor"))
                 ;; pig divergence (D73): a component that is not mounted by this extension has no main-process identity to focus
                 (error "Overlay unfocus(target) requires nil, the editor component, or a mounted overlay of this extension (D73)"))))))

(defun overlay-unfocus (handle &key (target nil target-p))
  "Release keyboard focus. :TARGET names where it goes: NIL focuses nothing, the editor component the extension installed, or a mounted
overlay's CUSTOM-COMPONENT of this extension; without :TARGET the host chooses."
  (if target-p
      (overlay-control handle "unfocus" :target (overlay-focus-target handle target))
      (overlay-control handle "unfocus")))
