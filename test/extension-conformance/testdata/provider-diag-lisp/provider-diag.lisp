(defpackage "provider-diag" (:use "COMMON-LISP"))
(in-package "provider-diag")

(defun model (id)
  (vector (pig:obj "id" id "name" id "reasoning" :false "input" (vector "text")
                   "cost" (pig:obj "input" 0 "output" 0 "cacheRead" 0 "cacheWrite" 0) "contextWindow" 4096 "maxTokens" 1024)))

(defun new-extension ()
  (let ((ext (pig:make-extension "provider-diag")))
    (pig:register-provider ext "diag-good" (pig:obj "api" "openai-completions" "baseUrl" "https://good.invalid/v1" "apiKey" "key" "models" (model "good-model")))
    (pig:register-provider ext "diag-bad" (pig:obj "baseUrl" "https://bad.invalid/v1" "apiKey" "key" "models" (model "bad-model")))
    (pig:tool ext "diag_ping" "diag_ping" (pig:obj "type" "object") (lambda (ctx args) (declare (ignore ctx args)) "pong"))
    ext))
