(defpackage "model-call-lisp" (:use "COMMON-LISP"))
(in-package "model-call-lisp")

(defun new-extension ()
  (let ((extension (pig:make-extension "model-call")))
    (pig:command extension "model-call" "Make one model call"
                 (lambda (ctx provider)
                   (let ((result (pig:registry-complete
                                  (pig:ctx-model-registry ctx)
                                  (pig:obj "provider" provider "modelId" "m" "api" "openai-completions")
                                  (pig:obj "messages" (pig:arr (pig:obj "role" "user" "content" "hello" "timestamp" 1)))
                                  (pig:obj "sessionId" "sdk-session"))))
                     (unless (and result (equal (pig:jget result "stopReason") "stop"))
                       (error "model call = ~A" (pig:to-json result))))))
    extension))
