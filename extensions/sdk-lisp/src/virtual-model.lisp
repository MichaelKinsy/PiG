;;;; SPDX-License-Identifier: MIT
;;;; Virtual models: models whose physical model the extension picks for each request.

(in-package #:pig)

(defstruct virtual-model
  "ROUTE is (lambda (ctx request)) -> a hash-table with the physical \"model\", \"thinkingLevel\" and optionally \"state\". REQUEST is the
host's route request (\"model\", \"thinkingLevel\", \"reason\", \"previous\", \"failed\", \"state\", \"messages\") plus \"signal\", the
request's cancellation. An absent or null \"state\" keeps the current state."
  provider id name route thinking-levels context-window max-tokens input)

(defstruct (vm-state (:conc-name vm-))
  (routes (make-hash-table :test 'equal))   ; (provider . id) -> route function
  (pending '())                             ; declarations of the register payload
  (unregistrations '()))

(defun vm-of (ext) (extension-extra ext :virtual-model #'make-vm-state))

(defun vm-register (ext model call)
  (unless (functionp (virtual-model-route model)) (error "virtual model route must be callable"))
  (let ((decl (obj "provider" (virtual-model-provider model) "id" (virtual-model-id model) "name" (virtual-model-name model))))
    (loop for (key value) in (list (list "thinkingLevels" (virtual-model-thinking-levels model))
                                   (list "contextWindow" (virtual-model-context-window model))
                                   (list "maxTokens" (virtual-model-max-tokens model))
                                   (list "input" (virtual-model-input model)))
          do (when value (setf (gethash key decl) (if (listp value) (prov-vec value) value))))
    (ensure-jsonable decl (format nil "virtual model ~A/~A" (virtual-model-provider model) (virtual-model-id model)))
    (let* ((vm (vm-of ext)) (key (cons (virtual-model-provider model) (virtual-model-id model))) previous
           (live (sb-thread:with-mutex ((extension-reg-lock ext))
                   (setf previous (gethash key (vm-routes vm)) (gethash key (vm-routes vm)) (virtual-model-route model))
                   (or (extension-live ext)
                       (progn (setf (vm-pending vm) (append (vm-pending vm) (list decl))) nil)))))
      (when live
        (handler-case (prov-call ext call "registerVirtualModel" decl)
          (error (e)
            (sb-thread:with-mutex ((extension-reg-lock ext))
              (if previous (setf (gethash key (vm-routes vm)) previous) (remhash key (vm-routes vm))))
            (error e))))
      nil)))

(defun vm-unregister (ext provider model-id call)
  (let* ((vm (vm-of ext)) (key (cons provider model-id))
         (live (sb-thread:with-mutex ((extension-reg-lock ext))
                 (or (extension-live ext)
                     (progn (setf (vm-pending vm) (remove-if (lambda (d) (and (equal (jget d "provider") provider) (equal (jget d "id") model-id)))
                                                              (vm-pending vm))
                                  (vm-unregistrations vm) (append (vm-unregistrations vm) (list (obj "provider" provider "id" model-id))))
                            (remhash key (vm-routes vm))
                            nil)))))
    (when live
      (prov-call ext call "unregisterVirtualModel" (obj "provider" provider "id" model-id))
      (sb-thread:with-mutex ((extension-reg-lock ext)) (remhash key (vm-routes vm))))
    nil))

(defun register-virtual-model (ext model)
  "Register a virtual model (Pi's registerVirtualModel): queued while the factory runs, else applied at once."
  (vm-register ext model nil))

(defun unregister-virtual-model (ext provider model-id)
  (vm-unregister ext provider model-id nil))

(defun ctx-register-virtual-model (ctx model) (vm-register (ctx-extension ctx) model (prov-ctx-call ctx)))
(defun ctx-unregister-virtual-model (ctx provider model-id) (vm-unregister (ctx-extension ctx) provider model-id (prov-ctx-call ctx)))

(def-register-contributor "virtual-models" (ext payload)
  (let ((vm (vm-of ext)))
    (when (vm-pending vm) (setf (gethash "virtual_models" payload) (prov-vec (vm-pending vm))))
    (when (vm-unregistrations vm) (setf (gethash "unregister_virtual_models" payload) (prov-vec (vm-unregistrations vm))))))

(def-request "virtual_model_route" (ext ctx req)
  (let* ((args (prov-object (jget req "args"))) (provider (jget args "provider" "")) (id (jget args "id" ""))
         (route (sb-thread:with-mutex ((extension-reg-lock ext)) (gethash (cons provider id) (vm-routes (vm-of ext))))))
    (unless route (error "unknown virtual model ~A/~A" provider id))
    (let* ((request (prov-copy-table (prov-object (jget args "request"))))
           (routed (progn (setf (gethash "signal" request) (ctx-cancel ctx)) (funcall route ctx request))))
      (unless (and (hash-table-p routed) (hash-table-p (jget routed "model")))
        (error "virtual model route must return a mapping with a model and a thinkingLevel"))
      ;; An absent or null state keeps the current state instead of storing null.
      (if (member (jget routed "state") '(nil :null))
          (let ((copy (prov-copy-table routed))) (remhash "state" copy) copy)
          routed))))
