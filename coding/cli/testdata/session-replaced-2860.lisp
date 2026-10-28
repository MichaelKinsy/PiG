;;;; session-replaced-2860.lisp runs the bodies of Pi's 2860-replaced-session-context.test.ts cases as Lisp SDK commands.
;;;; Each line goes to the file named by PIG_TEST_2860_LOG; process ids name the extension instances, because each Session's extensions run in their own process here.
(defpackage "session-replaced-2860" (:use "COMMON-LISP"))
(in-package "session-replaced-2860")

(defun env (name) (sb-posix:getenv name))

(defun log-line (line)
  (with-open-file (out (env "PIG_TEST_2860_LOG") :direction :output :if-exists :append :if-does-not-exist :create :external-format :utf-8)
    (write-line line out)))

(defun bool (value) (if value "true" "false"))

(defun content-text (content)
  (if (stringp content)
      content
      (format nil "~{~A~}" (loop for part across (if (vectorp content) content #())
                                 when (equal (pig:jget part "type") "text") collect (pig:jget part "text")))))

(defun conversation (manager)
  (format nil "~{~A~^|~}"
          (loop for entry across (or (pig:session-get-branch manager) #())
                for message = (let ((m (pig:jget entry "message"))) (if (hash-table-p m) m (pig:obj)))
                when (and (equal (pig:jget entry "type") "message") (not (equal (pig:jget message "role") "system")))
                  collect (format nil "~A:~A" (pig:jget message "role") (content-text (pig:jget message "content"))))))

(defun read-marks ()
  (handler-case (with-open-file (in (env "PIG_TEST_2860_MARKS") :external-format :utf-8)
                  (let ((text (make-string (file-length in))))
                    (pig:from-json (subseq text 0 (read-sequence text in)))))
    (error () (pig:obj))))

(defun succeeds-p (thunk) (handler-case (progn (funcall thunk) t) (error () nil)))

(defun new-extension ()
  (let ((ext (pig:make-extension "session-replaced-2860")) (pid (sb-posix:getpid)))
    (pig:on ext "session_start" (lambda (ctx data) (declare (ignore ctx data)) (log-line (format nil "start:~D" pid))))
    (pig:on ext "session_shutdown" (lambda (ctx data) (declare (ignore ctx data)) (log-line (format nil "shutdown:~D" pid))))
    (pig:command
     ext "repro" "repro"
     (lambda (old-ctx args)
       (declare (ignore args))
       (let ((old-session-file (pig:session-get-session-file (pig:ctx-session-manager old-ctx))))
         (pig:ctx-new-session
          old-ctx :parent-session old-session-file
                  :with-session
                  (lambda (replaced)
                    (log-line (format nil "with:~D" pid))
                    (let ((replacement (pig:session-get-session-file (pig:ctx-session-manager replaced))))
                      (log-line (format nil "replacement:~A" (bool (and replacement (not (equal replacement old-session-file)))))))
                    (log-line (format nil "staleCtx:~A" (bool (not (succeeds-p (lambda () (pig:session-get-session-file (pig:ctx-session-manager old-ctx))))))))
                    (log-line (format nil "stalePi:~A" (bool (not (succeeds-p (lambda () (pig:ctx-send-user-message old-ctx "stale message")))))))
                    (pig:ctx-send-user-message replaced "reply with exactly: hello reply")
                    (log-line (format nil "idle:~A" (bool (pig:ctx-is-idle-p replaced))))
                    (log-line (format nil "model:~A" (pig:jget (pig:ctx-get-model-info replaced) "id")))
                    (log-line (concatenate 'string "conversation:" (conversation (pig:ctx-session-manager replaced)))))))))
    (pig:command
     ext "throw-it" "throw-it"
     (lambda (ctx args)
       (declare (ignore args))
       (let ((thrown (make-condition 'simple-error :format-control "callback failed")))
         (handler-case (progn (pig:ctx-new-session ctx :with-session (lambda (replaced) (declare (ignore replaced)) (error thrown)))
                              (log-line "caught:none"))
           (error (e) (log-line (if (eq e thrown) (format nil "caught:~A" e) (format nil "caught:other:~A" e))))))))
    (pig:command
     ext "keep-it" "keep-it"
     (lambda (ctx args)
       (declare (ignore args))
       ;; a context kept past its callback must not block the extension: the command still returns
       (let ((kept '()))
         (pig:ctx-new-session ctx :with-session (lambda (replaced) (push replaced kept)))
         (dolist (read (list (lambda () (pig:session-get-session-file (pig:ctx-session-manager (first kept))))
                             (lambda () (pig:ctx-is-idle-p (first kept)))
                             (lambda () (pig:ctx-send-user-message (first kept) "kept message"))))
           (succeeds-p read))
         (log-line "kept:returned"))))
    (pig:command
     ext "fork-it" "fork-it"
     (lambda (ctx args)
       (declare (ignore args))
       (let ((leaf-id (pig:session-get-leaf-id (pig:ctx-session-manager ctx))))
         (unless (and (stringp leaf-id) (plusp (length leaf-id))) (error "Missing leaf id"))
         (pig:ctx-fork ctx leaf-id :position "at"
                                   :with-session (lambda (replaced)
                                                   (pig:ctx-send-user-message replaced "reply with exactly: fork reply")
                                                   (log-line (concatenate 'string "conversation:" (conversation (pig:ctx-session-manager replaced)))))))))
    (pig:command
     ext "mark" "mark"
     (lambda (ctx name)
       (let ((marks (read-marks)))
         (setf (gethash name marks) (pig:session-get-session-file (pig:ctx-session-manager ctx)))
         (with-open-file (out (env "PIG_TEST_2860_MARKS") :direction :output :if-exists :supersede :external-format :utf-8)
           (write-string (pig:to-json marks) out)))))
    (pig:command ext "new" "new" (lambda (ctx args) (declare (ignore args)) (pig:ctx-new-session ctx)))
    (pig:command ext "switch" "switch" (lambda (ctx name) (pig:ctx-switch-session ctx (pig:jget (read-marks) name))))
    (pig:command
     ext "switch-it" "switch-it"
     (lambda (ctx args)
       (declare (ignore args))
       (let ((target (pig:jget (read-marks) "target")))
         (pig:ctx-switch-session ctx target
                                 :with-session (lambda (replaced)
                                                 (pig:ctx-send-user-message replaced "reply with exactly: switch reply")
                                                 (log-line (format nil "switched:~A" (bool (equal (pig:session-get-session-file (pig:ctx-session-manager replaced)) target))))
                                                 (log-line (concatenate 'string "conversation:" (conversation (pig:ctx-session-manager replaced)))))))))
    (pig:command
     ext "seed-it" "seed-it"
     (lambda (ctx args)
       (declare (ignore args))
       ;; types.ts:411, agent-session-runtime.ts:254-257: setup seeds the replacement Session through its SessionManager before withSession runs
       (pig:ctx-new-session
        ctx :setup (lambda (manager)
                     (let ((ids (list (pig:setup-append-custom-message-entry manager "seed-msg" "from setup" t)
                                      (pig:setup-append-session-info manager "seeded-session"))))
                       (log-line (format nil "setup:~A" (bool (and (every (lambda (id) (and (stringp id) (plusp (length id)))) ids)
                                                                   (= (length (remove-duplicates ids :test #'equal)) (length ids))))))))
            :with-session (lambda (replaced)
                            (let ((manager (pig:ctx-session-manager replaced)))
                              (log-line (format nil "seedName:~A" (pig:session-get-session-name manager)))
                              (log-line (format nil "seedEntry:~{~A~^|~}"
                                                (loop for entry across (pig:session-get-entries manager)
                                                      when (equal (pig:jget entry "type") "custom_message")
                                                        collect (format nil "~A=~A" (pig:jget entry "customType") (pig:jget entry "content"))))))))))
    ext))
