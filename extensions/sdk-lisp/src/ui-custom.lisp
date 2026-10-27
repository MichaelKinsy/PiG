;;;; SPDX-License-Identifier: MIT
;;;; ui.custom: a focused component of the extension, rendered by the extension and drawn by the host (inline or as an overlay).
;;;; A component is a CUSTOM-COMPONENT of closures: a line component has RENDER, a kit component VIEW (and optionally HANDLE-VIEW-EVENT);
;;;; both take HANDLE-INPUT and optionally HANDLE-MOUSE. Each mounted component runs on a worker thread of its own, which takes the
;;;; input, view events and mouse events in order, so they never run at once.

(in-package #:pig)

(defstruct (custom-component
            (:constructor make-custom-component (&key render view handle-input handle-view-event handle-mouse set-invalidate dispose on-focus)))
  "A focused ui.custom component. RENDER is (lambda (width)) returning rows; VIEW is (lambda (width)) returning a kit view;
HANDLE-INPUT is (lambda (data)); HANDLE-VIEW-EVENT is (lambda (kit-event)); HANDLE-MOUSE is (lambda (mouse-event)). Each returns a
CUSTOM-RESULT, or NIL to stay open. SET-INVALIDATE is (lambda (callback)): the component calls CALLBACK, a function of no arguments, to
ask for a render, and is called with NIL when it unmounts. DISPOSE runs after unmounting. ON-FOCUS is (lambda (focused-p))."
  render view handle-input handle-view-event handle-mouse set-invalidate dispose on-focus)

(defstruct (custom-result (:constructor make-custom-result (&key done value)))
  "What a component's handler returns: DONE closes the component, ctx-custom returning VALUE."
  done value)

(defstruct (mouse-event
            (:constructor make-mouse-event (&key (type "") (button "") (x 0) (y 0) (screen-x 0) (screen-y 0) (width 0) (height 0)
                                              shift alt ctrl (wheel-delta 0) (click-count 0))))
  "A cell-based pointer event: X and Y are relative to the component's first rendered cell. TYPE is \"press\", \"release\", \"move\", \"drag\",
\"click\" or \"wheel\"; BUTTON \"left\", \"middle\", \"right\" or \"none\"."
  type button x y screen-x screen-y width height shift alt ctrl wheel-delta click-count)

(defun uc-int (value) (if (realp value) (truncate value) 0))

(defun uc-mouse-from-wire (raw)
  (when (hash-table-p raw)
    (flet ((text (key) (let ((v (jget raw key))) (if (stringp v) v ""))))
      (make-mouse-event :type (text "type") :button (text "button") :x (uc-int (jget raw "x")) :y (uc-int (jget raw "y"))
                        :screen-x (uc-int (jget raw "screenX")) :screen-y (uc-int (jget raw "screenY"))
                        :width (uc-int (jget raw "width")) :height (uc-int (jget raw "height"))
                        :shift (eq (jget raw "shift") t) :alt (eq (jget raw "alt") t) :ctrl (eq (jget raw "ctrl") t)
                        :wheel-delta (uc-int (jget raw "wheelDelta")) :click-count (uc-int (jget raw "clickCount"))))))

;;; ---------------------------------------------------------------- the mounted overlay

(defstruct (uc-ov (:constructor uc-make-ov (component render-width)))
  component
  render-width                          ; (lambda (terminal-width)) -> render width
  (last-lines nil) (last-view nil) (seq 0)
  (lock (sb-thread:make-mutex :name "pig overlay"))
  (queue (sb-thread:make-waitqueue))
  (events '()) (active t) (render-pending nil) (last-render nil)
  ;; the image refs the last view frame names; force-view sends the next view frame even when equal to the last, after an eviction
  (view-refs '()) (force-view nil)
  (on-handle nil) (hidden nil) (focused nil) (visible nil) (bounds nil)
  (worker nil))

(defconstant +uc-queue-size+ 64)

(defun uc-enqueue (ov kind data)
  "Queue an event behind the ones queued. False when the queue is full, which ends the overlay."
  (sb-thread:with-mutex ((uc-ov-lock ov))
    (cond ((not (uc-ov-active ov)) t)
          ((>= (length (uc-ov-events ov)) +uc-queue-size+) (setf (uc-ov-active ov) nil) nil)
          (t (setf (uc-ov-events ov) (append (uc-ov-events ov) (list (cons kind data))))
             (sb-thread:condition-notify (uc-ov-queue ov))
             t))))

(defun uc-request-render (ov)
  (sb-thread:with-mutex ((uc-ov-lock ov))
    (when (and (uc-ov-active ov) (not (uc-ov-render-pending ov)) (< (length (uc-ov-events ov)) +uc-queue-size+))
      (setf (uc-ov-render-pending ov) t
            (uc-ov-events ov) (append (uc-ov-events ov) (list (cons :render nil))))
      (sb-thread:condition-notify (uc-ov-queue ov)))))

(defun uc-stop (ov)
  (sb-thread:with-mutex ((uc-ov-lock ov))
    (setf (uc-ov-active ov) nil
          (uc-ov-events ov) (append (uc-ov-events ov) (list (cons :stop nil))))
    (sb-thread:condition-broadcast (uc-ov-queue ov))))

(defun uc-next-event (ov)
  (sb-thread:with-mutex ((uc-ov-lock ov))
    (loop until (uc-ov-events ov) do (sb-thread:condition-wait (uc-ov-queue ov) (uc-ov-lock ov)))
    (let ((event (pop (uc-ov-events ov))))
      (when (eq (car event) :render) (setf (uc-ov-render-pending ov) nil))
      (values (car event) (cdr event)))))

(defun uc-set-focus-flag (ov focused)
  (let ((on-focus (custom-component-on-focus (uc-ov-component ov))))
    (when on-focus (handler-case (funcall on-focus focused) (error () nil)))))

(defun uc-apply-handle-state (ov state)
  "Replace the mirrored handle state with the host's (ui.custom.opened and ui.custom.input carry it)."
  (sb-thread:with-mutex ((uc-ov-lock ov))
    (setf (uc-ov-hidden ov) (eq (jget state "hidden") t) (uc-ov-focused ov) (eq (jget state "focused") t)
          (uc-ov-visible ov) (eq (jget state "visible") t) (uc-ov-bounds ov) (jget state "bounds")))
  (uc-set-focus-flag ov (eq (jget state "focused") t)))

(defun uc-apply-focus (ov focused)
  (sb-thread:with-mutex ((uc-ov-lock ov)) (setf (uc-ov-focused ov) focused))
  (uc-set-focus-flag ov focused))

(defun uc-find (ext key)
  (let ((kit (kit-state ext)))
    (sb-thread:with-mutex ((kit-state-lock kit)) (gethash key (kit-state-overlays kit)))))

(defun uc-close-full (ext key)
  (ignore-errors (notify-host ext "ui.custom.close" (obj "key" key "error" "focused input queue is full"))))

;;; ---------------------------------------------------------------- rendering

(defun uc-frame-args (ov args)
  (when (custom-component-handle-mouse (uc-ov-component ov)) (setf (gethash "mouse" args) t))
  args)

(defun uc-render (ext key ov)
  (let* ((c (uc-ov-component ov))
         (width (sb-thread:with-mutex ((extension-state-lock ext)) (extension-width ext))))
    (setf (uc-ov-last-render ov) (get-internal-real-time))
    (if (custom-component-view c)
        (let* ((encoded (kit-encode ext (funcall (custom-component-view c) width)))
               (json (to-json (kit-encoded-body encoded)))
               (force (sb-thread:with-mutex ((uc-ov-lock ov)) (prog1 (uc-ov-force-view ov) (setf (uc-ov-force-view ov) nil)))))
          (unless (and (not force) (uc-ov-last-view ov) (equal (car (uc-ov-last-view ov)) json) (eql (cdr (uc-ov-last-view ov)) width)
                       (not (kit-has-unsent ext encoded)))
            (setf (uc-ov-last-view ov) (cons json width))
            (sb-thread:with-mutex ((uc-ov-lock ov)) (setf (uc-ov-view-refs ov) (kit-encoded-refs encoded)))
            (incf (uc-ov-seq ov))
            (notify-host ext "ui.custom.render"
                         (uc-frame-args ov (obj "key" key "view" (kit-wire ext encoded) "width" width "seq" (uc-ov-seq ov))))))
        (let ((lines (map 'list #'princ-to-string (funcall (custom-component-render c) (funcall (uc-ov-render-width ov) width)))))
          (unless (equal lines (uc-ov-last-lines ov))
            (setf (uc-ov-last-lines ov) lines)
            (incf (uc-ov-seq ov))
            (notify-host ext "ui.custom.render"
                         (uc-frame-args ov (obj "key" key "lines" (coerce lines 'simple-vector) "width" width "seq" (uc-ov-seq ov)))))))))

(defun kit-resend-overlays (ext refs)
  "Render again, bypassing the dedup, every view component whose last frame names an evicted image."
  (let ((overlays (let ((kit (kit-state ext)))
                    (sb-thread:with-mutex ((kit-state-lock kit))
                      (loop for ov being the hash-values of (kit-state-overlays kit) collect ov)))))
    (dolist (ov overlays)
      (when (sb-thread:with-mutex ((uc-ov-lock ov))
              (and (intersection refs (uc-ov-view-refs ov) :test #'string=) (setf (uc-ov-force-view ov) t)))
        (uc-request-render ov)))))

(defun uc-render-all (ext)
  (dolist (ov (let ((kit (kit-state ext)))
                (sb-thread:with-mutex ((kit-state-lock kit))
                  (loop for ov being the hash-values of (kit-state-overlays kit) collect ov))))
    (uc-request-render ov)))

;;; ---------------------------------------------------------------- the worker

(defun uc-handle-event (ext key ov kind data)
  "Run one event; true when the component finished."
  (let ((c (uc-ov-component ov)))
    (flet ((run (handler argument)
             (let ((result (and handler (funcall handler argument))))
               (when (and (custom-result-p result) (custom-result-done result))
                 (setf (uc-ov-active ov) nil)
                 (notify-host ext "ui.custom.close" (obj "key" key "result" (or (custom-result-value result) :null)))
                 (return-from uc-handle-event t)))))
      (case kind
        (:opened
         (when (uc-ov-on-handle ov)
           (uc-apply-handle-state ov data)
           (funcall (uc-ov-on-handle ov) (make-overlay-handle ext key ov)))
         (return-from uc-handle-event nil))
        (:focus (uc-apply-focus ov data))
        (:input (run (custom-component-handle-input c) data))
        (:mouse (run (custom-component-handle-mouse c) data)
         ;; Pi's renderer draws again after press, click, drag and wheel only
         (when (member (mouse-event-type data) '("move" "release") :test #'string=) (return-from uc-handle-event nil)))
        (:view-event
         (unless (and (custom-component-view c) (custom-component-handle-view-event c)) (return-from uc-handle-event nil))
         (run (custom-component-handle-view-event c) data))
        (t (let ((delay (- 0.016 (if (uc-ov-last-render ov)
                                     (/ (- (get-internal-real-time) (uc-ov-last-render ov)) internal-time-units-per-second)
                                     1))))
             (when (plusp delay) (sleep delay))
             (unless (uc-ov-active ov) (return-from uc-handle-event t)))))
      (uc-render ext key ov)
      nil)))

(defun uc-worker (ext key ov)
  (loop while (uc-ov-active ov)
        do (multiple-value-bind (kind data) (uc-next-event ov)
             (when (or (eq kind :stop) (not (uc-ov-active ov))) (return))
             (when (handler-case (uc-handle-event ext key ov kind data)
                     (error (e)
                       (setf (uc-ov-active ov) nil)
                       (ignore-errors (notify-host ext "ui.custom.close" (obj "key" key "error" (princ-to-string e))))
                       t))
               (return)))))

(defun uc-start (ext key ov)
  "After the ui.custom call went out: attach the invalidation, start the worker, ask for the first frame. An error message, or NIL."
  (let ((c (uc-ov-component ov)))
    (flet ((failed (message)
             (ignore-errors (notify-host ext "ui.custom.close" (obj "key" key "error" message)))
             (return-from uc-start message)))
      (when (custom-component-set-invalidate c)
        (handler-case (funcall (custom-component-set-invalidate c) (lambda () (uc-request-render ov)))
          (error (e) (failed (format nil "attach focused invalidation: ~A" e)))))
      (handler-case (setf (uc-ov-worker ov) (kit-spawn ext (format nil "pig-overlay-~A" key) (lambda () (uc-worker ext key ov))))
        (error (e) (failed (format nil "start focused component worker: ~A" e))))
      (uc-request-render ov)
      nil)))

(defun uc-dispose (component)
  (let ((invalidate (custom-component-set-invalidate component)))
    (when invalidate
      (handler-case (funcall invalidate nil)
        (error (e) (format *error-output* "extension: recovered from error in a component's set-invalidate(nil): ~A~%" e)))))
  (let ((dispose (custom-component-dispose component)))
    (when dispose (ignore-errors (funcall dispose)))))

(defun uc-run (ctx component options on-handle)
  (let* ((ext (ctx-extension ctx)) (kit (kit-state ext)) (args (make-hash-table :test 'equal)) key ov start-error response failure)
    (when (hash-table-p options) (maphash (lambda (k v) (setf (gethash k args) v)) options))
    (kit-ensure-width-subscription ext)
    (sb-thread:with-mutex ((kit-state-lock kit))
      (setf key (format nil "custom-~D" (incf (kit-state-overlay-seq kit)))
            ov (uc-make-ov component (overlay-render-width args)))
      (when (and on-handle (eq (jget args "overlay") t))
        (setf (uc-ov-on-handle ov) on-handle (gethash "hasHandle" args) t))
      (setf (gethash key (kit-state-overlays kit)) ov))
    (setf (gethash "key" args) key)
    (handler-case
        (setf response (let ((*after-call-sent* (lambda () (let ((*after-call-sent* nil)) (setf start-error (uc-start ext key ov))))))
                         (host-call ext "ui.custom" args (ctx-request ctx) (ctx-request-id ctx))))
      (error (e) (setf failure e)))
    (sb-thread:with-mutex ((kit-state-lock kit)) (remhash key (kit-state-overlays kit)))
    (uc-stop ov)
    (let* ((worker (uc-ov-worker ov))
           (timed-out (and worker (progn (sb-thread:join-thread worker :default nil :timeout 1.0) (sb-thread:thread-alive-p worker)))))
      (unless timed-out (uc-dispose component))
      (cond (start-error (error "~A" start-error))
            (failure (error failure))
            (timed-out (error "focused component did not stop before the cleanup deadline"))
            (t (and (hash-table-p response) (eq (jget response "ok") t) (jget response "result")))))))

(defun ctx-custom (ctx component &key options on-handle)
  "Open a focused component (a CUSTOM-COMPONENT) and wait for it to close: its result, or NIL when it closed without one. OPTIONS is a
JSON object (obj \"overlay\" t \"overlayOptions\" (obj \"width\" \"50%\")); ON-HANDLE, with \"overlay\" true, is called once with the
mounted overlay's OVERLAY-HANDLE on the component's worker. With no UI, or in RPC mode, nothing runs and the result is NIL. A NIL COMPONENT
sends OPTIONS alone, which the host reports as unsupported."
  (cond ((not (ctx-has-ui-p ctx)) nil)
        ((equal (ctx-mode ctx) "rpc")
         ;; rpc-mode.ts custom() resolves undefined and never runs the component; the host call keeps the dialog scope
         (let ((kit (kit-state (ctx-extension ctx))))
           (ctx-call-host ctx "ui.custom"
                          (obj "key" (sb-thread:with-mutex ((kit-state-lock kit))
                                       (format nil "custom-~D" (incf (kit-state-overlay-seq kit)))))))
         nil)
        (t (block-for-user ctx)
           (if (null component)
               (ctx-call-host ctx "ui.custom" (or options (obj)))
               (prog1 (uc-run ctx component options on-handle)
                 (when (plusp (length (ctx-request-id ctx)))
                   (send-request-state (ctx-extension ctx) (ctx-request-id ctx) "progress" nil (ctx-request ctx))))))))

;;; ---------------------------------------------------------------- host notifies

(def-notify "ui.view.event" (ext args)
  ;; events of a closed surface are dropped, as the host drops them
  (multiple-value-bind (key event) (kit-event-from-wire args)
    (when event
      (let ((ov (uc-find ext key)))
        (when (and ov (not (uc-enqueue ov :view-event event))) (uc-close-full ext key))))))

(def-notify "ui.custom.mouse" (ext args)
  (let* ((key (let ((k (jget args "key"))) (if (stringp k) k ""))) (mouse (uc-mouse-from-wire (jget args "event"))) (ov (uc-find ext key)))
    (when (and ov mouse (not (uc-enqueue ov :mouse mouse))) (uc-close-full ext key))))

(def-notify "ui.custom.opened" (ext args)
  (let* ((key (let ((k (jget args "key"))) (if (stringp k) k ""))) (ov (uc-find ext key)))
    (when (and ov (not (uc-enqueue ov :opened args))) (uc-close-full ext key))))

(def-notify "ui.custom.focus" (ext args)
  (let* ((key (let ((k (jget args "key"))) (if (stringp k) k ""))) (ov (uc-find ext key)))
    (when (and ov (not (uc-enqueue ov :focus (eq (jget args "focused") t)))) (uc-close-full ext key))))

(def-notify "ui.custom.input" (ext args)
  (let* ((key (let ((k (jget args "key"))) (if (stringp k) k ""))) (ov (uc-find ext key)))
    (when ov
      (when (hash-table-p (jget args "state")) (uc-apply-handle-state ov (jget args "state")))
      (unless (uc-enqueue ov :input (let ((d (jget args "data"))) (if (stringp d) d ""))) (uc-close-full ext key)))))

(def-stop-hook "ui-custom" (ext)
  (let ((kit (kit-state ext)))
    (dolist (ov (sb-thread:with-mutex ((kit-state-lock kit)) (loop for ov being the hash-values of (kit-state-overlays kit) collect ov)))
      (uc-stop ov))))
