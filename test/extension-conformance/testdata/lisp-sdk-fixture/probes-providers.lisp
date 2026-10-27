(in-package "lisp-sdk-fixture")
;;; the probe commands, tools and event handlers of lane P (see def-fixture in fixture.lisp)

(defun pv-bool (value) (if (eq value t) "true" "false"))

(defun pv-append (path text)
  (with-open-file (out path :direction :output :if-exists :append :if-does-not-exist :create :external-format :utf-8)
    (write-string text out)))

(defun pv-transform-markdown (markdown context)
  ;; "trace:<dir>:<name>" records the body's entry in <dir>/trace; the body named "first" returns only once <dir>/release exists.
  (when (and (>= (length markdown) 6) (string= markdown "trace:" :end1 6))
    (let* ((rest (subseq markdown 6)) (colon (position #\: rest :from-end t))
           (directory (subseq rest 0 colon)) (name (subseq rest (1+ colon))))
      (pv-append (concatenate 'string directory "/trace") (format nil "entered ~A~%" name))
      (when (string= name "first")
        (loop until (probe-file (concatenate 'string directory "/release")) do (sleep 0.01))
        (pv-append (concatenate 'string directory "/trace") (format nil "returned first~%")))))
  (format nil "md:~A:~A:streaming=~A:width=~D" markdown (let ((type (pig:jget context "messageType"))) (if (stringp type) type ""))
          (pv-bool (pig:jget context "isStreaming")) (let ((width (pig:jget context "availableWidth"))) (if (integerp width) width 0))))

(defun pv-resolved-line (prefix tool)
  (lambda (ctx args render width)
    (declare (ignore ctx render width))
    (list (format nil "~A:~A:~A" prefix tool (pig:jget args "q")))))

(defun pv-resolve-tool (tool next)
  (cond ((equal tool "conformance_tool_renderer")
         (pig:make-tool-renderers :render-call (pv-resolved-line "resolved" tool)))
        ((equal tool "conformance_no_renderer") nil)
        ((equal tool "conformance_fill")
         (or (funcall next) (pig:make-tool-renderers :render-call (pv-resolved-line "filled" tool))))
        ((equal tool "conformance_wrap")
         (let ((renderers (copy-structure (funcall next))))
           (setf (pig:tool-renderers-render-call renderers) (pv-resolved-line "wrapped" tool))
           renderers))
        (t (funcall next))))

(def-fixture "provider-renderers" (ext)
  (pig:message-renderer
   ext "conformance-message"
   (lambda (ctx message options width)
     (declare (ignore ctx))
     (if (equal (pig:jget message "content") "padding-options")
         (list (pig:to-json options))
         (list (format nil "renderer:~A:expanded=~A:width=~D" (let ((c (pig:jget message "content"))) (if (stringp c) c "")) (pv-bool (pig:jget options "expanded")) width)))))
  (pig:entry-renderer
   ext "conformance-entry"
   (lambda (ctx entry options width)
     (declare (ignore ctx))
     (list (format nil "entryrenderer:~A:expanded=~A:width=~D" (let ((d (pig:jget entry "data"))) (if (stringp d) d "")) (pv-bool (pig:jget options "expanded")) width))))
  (pig:markdown-transformer ext #'pv-transform-markdown)
  (pig:tool-renderer ext #'pv-resolve-tool)
  (pig:command ext "late_tool_renderer" "Register a tool renderer resolver after loading"
               (lambda (ctx args)
                 (declare (ignore ctx args))
                 (pig:tool-renderer ext (lambda (tool next)
                                          (if (equal tool "conformance_late")
                                              (pig:make-tool-renderers :render-call (pv-resolved-line "late" tool))
                                              (funcall next)))))))

(def-fixture "provider-response" (ext)
  (pig:on ext "after_provider_response"
          (lambda (ctx data)
            (pig:ctx-notify ctx (format nil "provider-response=~A:~A:~A" (pig:jget data "type") (pig:jget data "status") (pig:jref data "headers" "x-probe")) "info")
            (pig:obj "cancel" t)))
  (pig:on ext "after_provider_response"
          (lambda (ctx data) (declare (ignore data)) (pig:ctx-notify ctx "provider-response=second" "info") nil)))

(def-fixture "provider-late-registrations" (ext)
  (pig:command ext "provider_probe_register" "Register a provider with one model"
               (lambda (ctx args)
                 (declare (ignore ctx args))
                 (pig:register-provider
                  ext "conformance-probe"
                  (pig:from-json "{\"baseUrl\":\"https://probe.invalid/v1\",\"api\":\"openai-completions\",\"apiKey\":\"probe-key\",\"models\":[{\"id\":\"probe-model\",\"name\":\"Probe Model\",\"reasoning\":false,\"input\":[\"text\"],\"cost\":{\"input\":0,\"output\":0,\"cacheRead\":0,\"cacheWrite\":0},\"contextWindow\":1000,\"maxTokens\":100}]}"))))
  (pig:command ext "provider_probe_unregister" "Unregister the provider"
               (lambda (ctx args) (declare (ignore ctx args)) (pig:unregister-provider ext "conformance-probe"))))

;;; the OAuth providers the cross-transport conformance suite drives (oauth_conformance_test.go)

(defun pv-oauth-store ()
  (pig:make-oauth-credential-store
   :credential-status (lambda () (pig:make-oauth-credential-status :present t :auth-type "oauth" :source "conformance"))
   :store-credentials (lambda (credentials)
                        (unless (and (equal (pig:jget credentials "accountId") "account-store") (equal (pig:jget credentials "scope") "scope-store"))
                          (error "credential metadata lost"))
                        "/conf/creds.json")
   :delete-credentials (lambda () t)))

(defun pv-copy-credentials (credentials &rest members)
  (let ((copy (make-hash-table :test 'equal)))
    (maphash (lambda (k v) (setf (gethash k copy) v)) credentials)
    (loop for (key value) on members by #'cddr do (setf (gethash key copy) value))
    copy))

(def-fixture "provider-oauth" (ext)
  (pig:register-oauth-provider
   ext "conformance-oauth-object" (pig:obj "name" "Conformance OAuth Object")
   (pig:make-oauth-provider
    :name "Conformance OAuth Object"
    :login (lambda (callbacks)
             (declare (ignore callbacks))
             (pig:oauth-credentials :access "object-access" :refresh "object-refresh" :expires 1700000000000.25d0
                                    :extra (pig:obj "meta" (pig:obj "k" (vector 1 :null "")) "projectId" "")))
    :refresh-token (lambda (credentials)
                     (pv-copy-credentials credentials "access" (concatenate 'string "refreshed-" (pig:jget credentials "refresh"))
                                                      "expires" (+ (pig:jget credentials "expires") 0.5d0)))
    :get-api-key (lambda (credentials)
                   (format nil "key:~A:~A" (if (equal (pig:jget credentials "type") "oauth") "typed" "untyped")
                           (if (nth-value 1 (gethash "meta" credentials)) "meta" "nometa")))))
  (pig:register-oauth-provider
   ext "conformance-oauth-large" (pig:obj "name" "Conformance OAuth Large")
   (pig:make-oauth-provider
    :name "Conformance OAuth Large"
    :login (lambda (callbacks) (declare (ignore callbacks)) (pig:oauth-credentials :access "large-access" :refresh "large-refresh" :expires (expt 2 60)))
    :get-api-key (lambda (credentials)
                   (let ((expires (pig:jget credentials "expires")))
                     (format nil "key:~D:~D" (round (- expires (expt 2 60))) (- (round expires) (expt 2 60)))))))
  (pig:register-oauth-provider
   ext "conformance-oauth" (pig:obj "name" "Conformance OAuth")
   (pig:make-oauth-provider
    :name "Conformance OAuth" :is-subscription t
    :login (lambda (callbacks)
             (pig:oauth-on-device-code callbacks (pig:make-oauth-device-code-info :user-code "CONF-USER-CODE" :verification-uri "https://conf.example/verify"))
             (pig:oauth-on-progress callbacks "waiting")
             (let ((value (pig:oauth-on-prompt callbacks (pig:make-oauth-prompt :message "paste the code"))))
               (pig:oauth-credentials :access (concatenate 'string "access-" value) :refresh "refresh-tok" :expires 4242
                                      :account-id "account-login" :scope "scope-login")))
    :refresh-token (lambda (credentials)
                     (pig:oauth-credentials :access (concatenate 'string "refreshed-" (pig:jget credentials "refresh")) :refresh (pig:jget credentials "refresh")
                                            :expires 9999 :account-id (pig:jget credentials "accountId") :scope (pig:jget credentials "scope")))
    :get-api-key (lambda (credentials)
                   (when (equal (pig:jget credentials "access") "boom") (error "getApiKey exploded"))
                   (concatenate 'string "key:" (pig:jget credentials "access")))
    :credential-store (pv-oauth-store))))
