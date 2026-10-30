(in-package "lisp-sdk-fixture")
;;; the probe commands, tools and event handlers of lane S1 (see def-fixture in fixture.lisp)

(def-fixture "session_actions_unbound" (ext)
  (pig:command ext "session_actions_unbound" "Report what unbound session actions answer"
               (lambda (ctx args)
                 (declare (ignore args))
                 (flet ((report (name call)
                          (handler-case (format nil "~A=~A" name (if (eq (pig:jget (funcall call) "cancelled") t) "true" "false"))
                            (error (e) (format nil "~A=error:~A" name e)))))
                   (pig:ctx-notify
                    ctx (format nil "unbound:~{~A~^,~}"
                                (list (report "new" (lambda () (pig:ctx-new-session ctx)))
                                      (report "fork" (lambda () (pig:ctx-fork ctx "entry")))
                                      (report "navigate" (lambda () (pig:ctx-navigate-tree ctx "entry")))
                                      (report "switch" (lambda () (pig:ctx-switch-session ctx "/s.jsonl")))
                                      (handler-case (progn (pig:ctx-reload ctx) "reload=ok")
                                        (error (e) (format nil "reload=error:~A" e)))))
                    "info")))))

(def-fixture "session_actions_bound" (ext)
  (pig:command ext "session_actions_bound" "Report what the host's bound session actions answer"
               (lambda (ctx args)
                 (declare (ignore args))
                 (flet ((report (name call)
                          (handler-case (format nil "~A=~A" name (if (eq (pig:jget (funcall call) "cancelled") t) "true" "false"))
                            (error (e) (format nil "~A=error:~A" name e)))))
                   (pig:ctx-notify
                    ctx (format nil "bound:~{~A~^,~}"
                                (list (report "new" (lambda () (pig:ctx-new-session ctx :parent-session "/parent.jsonl")))
                                      (report "fork" (lambda () (pig:ctx-fork ctx "entry-1" :position "at")))
                                      (report "navigate" (lambda () (pig:ctx-navigate-tree ctx "entry-2" :summarize t :custom-instructions "brief" :replace-instructions t :label "mark")))
                                      (report "switch" (lambda () (pig:ctx-switch-session ctx "/target.jsonl")))))
                    "info")))))

;;; ---------------------------------------------------------------- the session and registry facades of the context

(defun ss-null (value) (if (null value) :null value))

(def-fixture "session-registry-facades" (ext)
  (pig:command ext "registry-session" "Read registry and session facades"
               (lambda (ctx args)
                 (declare (ignore args))
                 (let ((s (pig:ctx-session-manager ctx)) (r (pig:ctx-model-registry ctx)))
                   (pig:ctx-notify
                    ctx (pig:to-json
                         (pig:obj "cwd" (pig:session-get-cwd s) "dir" (pig:session-get-session-dir s) "id" (pig:session-get-session-id s)
                                  "name" (pig:session-get-session-name s) "leaf" (pig:session-get-leaf-id s)
                                  "entry" (pig:session-get-entry s "one") "missing" (ss-null (pig:session-get-entry s "missing"))
                                  "label" (pig:session-get-label s "one") "entries" (pig:session-get-entries s)
                                  "branch" (pig:session-get-branch s "one") "tree" (pig:session-get-tree s)
                                  "contextEntries" (pig:session-build-context-entries s) "projection" (pig:session-build-session-projection s)
                                  "persisted" (pig:session-is-persisted s) "usesDefaultDir" (pig:session-uses-default-session-dir s)
                                  "children" (pig:session-get-children s "one") "sessionContext" (pig:session-build-session-context s)
                                  "models" (pig:registry-get-all r) "available" (pig:registry-get-available r)
                                  "status" (pig:registry-get-provider-auth-status r "registry-probe")
                                  "display" (pig:registry-get-provider-display-name r "registry-probe") "error" (ss-null (pig:registry-get-error r))
                                  "config" (ss-null (pig:registry-get-registered-provider-config r "registry-probe"))
                                  "ids" (pig:registry-get-registered-provider-ids r) "auth" (pig:registry-get-provider-auth r "registry-probe")
                                  "apiKey" (ss-null (pig:registry-get-api-key-for-provider r "registry-probe"))
                                  "missingKey" (ss-null (pig:registry-get-api-key-for-provider r "missing"))
                                  "refresh" (pig:registry-refresh r (pig:obj "allowNetwork" :false))))
                    "info"))))
  (pig:command ext "registry-edges" "Read the registry answers that depend on the provider state"
               (lambda (ctx args)
                 (declare (ignore args))
                 (let* ((r (pig:ctx-model-registry ctx)) (models (concatenate 'list (pig:registry-get-all r) (list (pig:obj)))))
                   (flet ((bool (value) (if value t :false)))
                     (pig:ctx-notify
                      ctx (pig:to-json
                           (pig:obj "available" (pig:registry-get-available r)
                                    "hasAuth" (map 'vector (lambda (model) (bool (pig:registry-has-configured-auth r model))) models)
                                    "oauth" (map 'vector (lambda (model) (bool (pig:registry-is-using-oauth r model))) models)
                                    "found" (ss-null (pig:registry-find r "registry-probe" "a")) "foundOff" (ss-null (pig:registry-find r "registry-off" "d"))
                                    "missing" (ss-null (pig:registry-find r "registry-probe" "zzz")) "mismatch" (ss-null (pig:registry-find r "registry-probe" "c"))))
                      "info")))))
  (pig:command ext "registry-unbound-reads" "Report find and a session read against a host that cannot answer"
               (lambda (ctx args)
                 (declare (ignore args))
                 (flet ((report (read)
                          (handler-case (concatenate 'string "ok:" (pig:to-json (funcall read)))
                            (error (e) (format nil "error:~A" e)))))
                   (pig:ctx-notify
                    ctx (pig:to-json (pig:obj "find" (report (lambda () (ss-null (pig:registry-find (pig:ctx-model-registry ctx) "registry-probe" "a"))))
                                              "entries" (report (lambda () (pig:session-get-entries (pig:ctx-session-manager ctx))))))
                    "info"))))
  (pig:command ext "session-order" "Read the session as Pi returns it"
               (lambda (ctx args)
                 (declare (ignore args))
                 (let ((s (pig:ctx-session-manager ctx)))
                   (pig:ctx-notify
                    ctx (pig:to-json (pig:obj "getEntries" (pig:session-get-entries s) "getEntry" (pig:session-get-entry s "a4")
                                              "getLeafEntry" (ss-null (pig:session-get-leaf-entry s)) "getBranch" (pig:session-get-branch s "a4")
                                              "getChildren" (pig:session-get-children s "a1") "getTree" (pig:session-get-tree s)
                                              "buildContextEntries" (pig:session-build-context-entries s)
                                              "buildSessionProjection" (pig:session-build-session-projection s)
                                              "buildSessionContext" (pig:session-build-session-context s)))
                    "info")))))
