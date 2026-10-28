;;;; SPDX-License-Identifier: MIT
;;;; The typed model operations of the registry: typed reads, classify, generateImages, virtual models (model-registry.ts:135-188).

(in-package #:pig)

(defun registry-get-models-of-type (registry model-type &optional provider)
  "Every known model of MODEL-TYPE (\"chat\", \"image\" or \"classifier\"), optionally of one provider, in the host's order:
chat models come from the registry state's models, the others from its typedModels."
  (let* ((state (registry-state registry))
         (typed (jget state "typedModels")))
    (coerce (loop for model across (concatenate 'simple-vector (jget state "models") (if (simple-vector-p typed) typed (vector)))
                  when (and (equal (let ((type (jget model "type"))) (if (and (stringp type) (plusp (length type))) type "chat")) model-type)
                            (or (null provider) (equal (jget model "provider") provider)))
                    collect model)
            'simple-vector)))

(defun registry-get-model-of-type (registry model-type provider model-id)
  "One model of a type by provider and id, or NIL."
  (find model-id (registry-get-models-of-type registry model-type provider) :key (lambda (model) (jget model "id")) :test #'equal))

(defun registry-find-of-type (registry model-type provider model-id)
  (registry-get-model-of-type registry model-type provider model-id))

(defun model-types-operation (registry method model context options failure-members)
  "Call METHOD (classify or generateImages). It never signals: a failure, and a cancelled request, are a result that names the model."
  (let ((ctx (model-registry-ctx registry))
        (args (obj "model" model "context" context)))
    (when options (setf (gethash "options" args) options))
    (handler-case (call-result ctx method args)
      (error (e)
        (let ((result (obj "api" (jget model "api") "provider" (jget model "provider") "model" (jget model "id"))))
          (loop for (key value) on failure-members by #'cddr do (setf (gethash key result) value))
          (setf (gethash "stopReason" result) (if (ctx-cancelled-p ctx) "aborted" "error")
                (gethash "errorMessage" result) (condition-message e)
                (gethash "timestamp" result) (ms-unix-ms))
          result)))))

(defun registry-classify (registry model context &optional options)
  "Classify CONTEXT, an object with state and questions, with request-time authentication. The questions and the answers keep their order."
  (model-types-operation registry "classify" model context options (list "answers" (obj))))

(defun registry-generate-images (registry model context &optional options)
  "Generate images from CONTEXT, an object whose input holds text and image blocks; the result's output holds the generated blocks."
  (model-types-operation registry "generateImages" model context options (list "output" (vector))))

(defun registry-register-provider (registry name &optional config)
  "Register a provider as Context.register_provider does."
  (funcall 'ctx-register-provider (model-registry-ctx registry) name config))

(defun registry-unregister-provider (registry name)
  (funcall 'ctx-unregister-provider (model-registry-ctx registry) name))

(defun registry-register-virtual-model (registry model)
  (funcall 'ctx-register-virtual-model (model-registry-ctx registry) model))

(defun registry-unregister-virtual-model (registry provider model-id)
  (funcall 'ctx-unregister-virtual-model (model-registry-ctx registry) provider model-id))
