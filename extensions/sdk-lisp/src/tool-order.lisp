;;;; SPDX-License-Identifier: MIT
;;;; The orders of one parallel tool batch, as Pi's single thread keeps them (the Python SDK's _ToolStartOrder, _SettleGate, _ToolBatches).
;;;; Every request runs on a thread of its own, so the SDK keeps three orders per batch (the host's "batch" member names it):
;;;;  - Start: a handler is invoked only after the handler of every earlier request was invoked or its request ended without one.
;;;;  - Dialog: a handler's first host call, of any kind, is written only after the first host call of every earlier request was written
;;;;    or its request ended, so the first dialogs of a batch open in the order the calls started.
;;;;  - Continuation: two dialogs answered back to back resume their handlers in the order of the answers; a handler's response is
;;;;    written only after the response of every handler that settled before it, or after that handler's next host call.

(in-package #:pig)

;;; ---------------------------------------------------------------- the settle gate

(defstruct (settle-gate (:constructor make-settle-gate ()))
  (lock (sb-thread:make-mutex :name "pig settle gate")) (queue (sb-thread:make-waitqueue))
  (settled '()))                        ; the slots that settled and have not left, in settle order

(defstruct (settle-slot (:constructor make-settle-slot (gate parked))) gate (settled nil) (finished nil) parked)

(defun settle-slot-settle (slot)
  "Give SLOT its place after every slot that settled before. The reader calls it before it hands the answer to the waiting handler."
  (let ((gate (settle-slot-gate slot)))
    (sb-thread:with-mutex ((settle-gate-lock gate))
      (unless (or (settle-slot-settled slot) (settle-slot-finished slot))
        (setf (settle-slot-settled slot) t)
        (setf (settle-gate-settled gate) (append (settle-gate-settled gate) (list slot)))))))

(defun settle-slot-leave (slot)
  "Give the place up. The caller holds the gate lock."
  (when (settle-slot-settled slot)
    (let ((gate (settle-slot-gate slot)))
      (setf (settle-gate-settled gate) (remove slot (settle-gate-settled gate))
            (settle-slot-settled slot) nil)
      (sb-thread:condition-broadcast (settle-gate-queue gate)))))

(defun settle-slot-yield (slot)
  "The handler's continuation reached its next await: the handlers that settled after it go on. The next answer settles the slot again."
  (sb-thread:with-mutex ((settle-gate-lock (settle-slot-gate slot))) (settle-slot-leave slot)))

(defun settle-slot-close (slot)
  (sb-thread:with-mutex ((settle-gate-lock (settle-slot-gate slot)))
    (settle-slot-leave slot)
    (setf (settle-slot-finished slot) t)))

(defun settle-slot-run (slot action)
  "Run ACTION after every slot that settled before this one left its place; at once for a slot that holds none. The slot closes after."
  (let ((gate (settle-slot-gate slot)))
    (sb-thread:with-mutex ((settle-gate-lock gate))
      (loop while (and (settle-slot-settled slot) (not (settle-slot-finished slot)) (not (eq (first (settle-gate-settled gate)) slot)))
            do (when (settle-slot-parked slot) (funcall (settle-slot-parked slot)))
               (sb-thread:condition-wait (settle-gate-queue gate) (settle-gate-lock gate))))
    (unwind-protect (funcall action) (settle-slot-close slot))))

;;; ---------------------------------------------------------------- one request's place in its batch

(defstruct (tool-order (:constructor make-tool-order (batches batch)))
  (lock (sb-thread:make-mutex :name "pig tool order"))
  (tail nil) (dialog-queue '()) (settle (make-settle-gate)) (parked nil) batches batch (live 0))

(defstruct (tool-start (:constructor make-tool-start (previous order)))
  previous (done (make-latch)) order (dialog-waiting nil) (dialog-passed nil) (dialog-opened nil) (ended nil) (slot nil))

(defstruct (tool-batches (:constructor make-tool-batches ()))
  (lock (sb-thread:make-mutex :name "pig tool batches")) (orders (make-hash-table :test 'equal)))

(defun tool-batches-of (ext) (extension-extra ext :tool-batches #'make-tool-batches))

(defun tool-order-reserve (order)
  (sb-thread:with-mutex ((tool-order-lock order))
    (let ((start (make-tool-start (tool-order-tail order) order)))
      (setf (tool-order-tail order) (tool-start-done start))
      (setf (tool-order-dialog-queue order) (append (tool-order-dialog-queue order) (list start)))
      (incf (tool-order-live order))
      start)))

(defun tool-batches-reserve (ext batch)
  "Take the next place in BATCH's order. The reader thread calls it, in arrival order, before it starts the request's thread."
  (let ((batches (tool-batches-of ext)))
    (sb-thread:with-mutex ((tool-batches-lock batches))
      (let ((order (or (gethash batch (tool-batches-orders batches))
                       (setf (gethash batch (tool-batches-orders batches)) (make-tool-order batches batch)))))
        (tool-order-reserve order)))))

(defun tool-order-request-ended (order)
  (let ((empty (sb-thread:with-mutex ((tool-order-lock order)) (zerop (decf (tool-order-live order)))))
        (batches (tool-order-batches order)))
    (when empty
      (sb-thread:with-mutex ((tool-batches-lock batches))
        (sb-thread:with-mutex ((tool-order-lock order))
          (when (and (zerop (tool-order-live order)) (eq (gethash (tool-order-batch order) (tool-batches-orders batches)) order))
            (remhash (tool-order-batch order) (tool-batches-orders batches))))))))

(defun tool-start-begin (start)
  "Return when every earlier request began its handler or ended without one."
  (when (tool-start-previous start) (latch-wait (tool-start-previous start)))
  (latch-set (tool-start-done start)))

(defun tool-start-pass-dialog (start)
  "Give up the request's dialog place: the first request that still holds one may write its first host call. Idempotent."
  (let ((order (tool-start-order start)))
    (sb-thread:with-mutex ((tool-order-lock order))
      (unless (tool-start-dialog-passed start)
        (setf (tool-start-dialog-passed start) t)
        (setf (tool-order-dialog-queue order) (remove start (tool-order-dialog-queue order)))
        (let ((head (first (tool-order-dialog-queue order))))
          (when (and head (tool-start-dialog-waiting head))
            (latch-set (tool-start-dialog-waiting head))
            (setf (tool-start-dialog-waiting head) nil)))))))

(defun tool-start-finish (start)
  (tool-start-pass-dialog start)
  (let ((order (tool-start-order start)) first slot)
    (sb-thread:with-mutex ((tool-order-lock order))
      (setf first (not (tool-start-ended start)) (tool-start-ended start) t slot (tool-start-slot start)))
    (when slot (settle-slot-close slot))
    (when first (tool-order-request-ended order))))

(defun tool-start-end (start)
  "Hand the places on when the request's thread ends, also for a request that failed before its handler. Idempotent."
  (when start
    (tool-start-begin start)
    (tool-start-finish start)))

(defun tool-start-release (start)
  "Hand the places on at once, for a request whose thread never started. The reader calls it, so it must not wait."
  (latch-set (tool-start-done start))
  (tool-start-finish start))

(defun tool-start-response-slot (start)
  "The request's place in the response order, made on first use; NIL once the request ended."
  (let ((order (tool-start-order start)))
    (sb-thread:with-mutex ((tool-order-lock order))
      (unless (tool-start-ended start)
        (or (tool-start-slot start)
            (setf (tool-start-slot start) (make-settle-slot (tool-order-settle order) (tool-order-parked order))))))))

(defun tool-start-before-call (start)
  "Run before the handler writes a host call, of any kind: it yields its place in the response order, and its first host call waits for
the first host calls of the earlier requests."
  (let ((slot (tool-start-slot start)) (order (tool-start-order start)) (waiting nil))
    (when slot (settle-slot-yield slot))
    (sb-thread:with-mutex ((tool-order-lock order))
      (when (tool-start-dialog-opened start) (return-from tool-start-before-call nil))
      (setf (tool-start-dialog-opened start) t)
      (let ((head (first (tool-order-dialog-queue order))))
        (when (or (null head) (eq head start) (tool-start-dialog-passed start)) (return-from tool-start-before-call nil)))
      (setf waiting (make-latch) (tool-start-dialog-waiting start) waiting))
    (when (tool-order-parked order) (funcall (tool-order-parked order)))
    (latch-wait waiting)))

(defun tool-start-after-call (start)
  "Run once the host call's frame is written: the first call of any kind gives the dialog place up."
  (tool-start-pass-dialog start))

(defun tool-start-respond (start send)
  "Write the request's response (SEND) after the response of every handler whose dialog answer was read before this handler's."
  (let ((slot (and start (tool-start-slot start))))
    (if slot (settle-slot-run slot send) (funcall send))))
