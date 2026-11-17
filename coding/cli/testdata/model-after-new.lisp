;;;; model-after-new.lisp is the Lisp SDK realization of model-after-new.mjs: it registers an extension provider whose models carry metadata no default produces (a window, an output limit, costs, reasoning and an image input) over the server PIG_TEST_MODEL_AFTER_NEW_URL names, and records the model each model_select event carries and the thinking level the extension reads then, as lines of PIG_TEST_REPLACE_LOG. Every Session start runs the factory again in a fresh process.
(defpackage "model-after-new" (:use "COMMON-LISP"))
(in-package "model-after-new")

(defun env (name) (sb-posix:getenv name))

(defun log-record (record)
  (with-open-file (out (env "PIG_TEST_REPLACE_LOG") :direction :output :if-exists :append :if-does-not-exist :create :external-format :utf-8)
    (write-line (pig:to-json record) out)))

;; A provider reaches the extension as its id or as an object carrying it.
(defun describe-model (model)
  (let ((provider (pig:jget model "provider")))
    (pig:obj "id" (format nil "~A/~A" (if (hash-table-p provider) (pig:jget provider "id") provider) (pig:jget model "id"))
             "name" (pig:jget model "name") "contextWindow" (pig:jget model "contextWindow") "maxTokens" (pig:jget model "maxTokens")
             "reasoning" (pig:jget model "reasoning") "input" (pig:jget model "input") "cost" (pig:jget model "cost"))))

(defun cost () (pig:obj "input" 3.25d0 "output" 16.5d0 "cacheRead" 0.325d0 "cacheWrite" 4.0625d0))

(defun new-extension ()
  (let ((ext (pig:make-extension "model-after-new")))
    (pig:register-provider
     ext "metaprov"
     (pig:obj "api" "openai-completions" "baseUrl" (or (env "PIG_TEST_MODEL_AFTER_NEW_URL") "") "apiKey" "probe-key"
              "models" (pig:arr (pig:obj "id" "model-a" "name" "Model A" "reasoning" t "input" (pig:arr "text" "image") "cost" (cost) "contextWindow" 777000 "maxTokens" 55555)
                                (pig:obj "id" "model-b" "name" "Model B" "reasoning" t "input" (pig:arr "text") "cost" (cost) "contextWindow" 555000 "maxTokens" 44444))))
    (pig:on ext "session_start"
            (lambda (ctx data) (declare (ignore ctx))
              (log-record (pig:obj "event" "session_start" "reason" (pig:jget data "reason")))))
    (pig:on ext "model_select"
            (lambda (ctx data)
              (log-record (pig:obj "event" "model_select" "source" (pig:jget data "source") "model" (describe-model (pig:jget data "model"))
                                   "thinking" (pig:ctx-get-thinking-level ctx)))))
    ext))
