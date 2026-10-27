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

(def-fixture "session-replacement-events" (ext)
  (pig:on ext "session_start"
          (lambda (ctx data)
            (pig:ctx-notify ctx (format nil "session_start:~A" (let ((r (pig:jget data "reason"))) (if (stringp r) r "")))  "info")
            (let ((previous (pig:jget data "previousSessionFile")))
              (when (and (stringp previous) (plusp (length previous)))
                (pig:ctx-notify ctx (concatenate 'string "previous:" previous) "info")))))
  (pig:on ext "session_shutdown"
          (lambda (ctx data)
            (pig:ctx-notify ctx (format nil "session_shutdown:~A" (let ((r (pig:jget data "reason"))) (if (stringp r) r "")))  "info")
            (let ((target (pig:jget data "targetSessionFile")))
              (when (and (stringp target) (plusp (length target)))
                (pig:ctx-notify ctx (concatenate 'string "target:" target) "info"))))))

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
