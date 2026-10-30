(defpackage "child-session-lisp" (:use "COMMON-LISP"))
(in-package "child-session-lisp")

(defun text-of (message)
  (let ((content (pig:jget message "content")))
    (if (and (vectorp content) (not (stringp content)) (plusp (length content)))
        (let ((text (pig:jget (aref content 0) "text"))) (if (stringp text) text ""))
        "")))

(defun sorted-keys (table)
  (let ((keys '()))
    (when (hash-table-p table) (maphash (lambda (key value) (declare (ignore value)) (push key keys)) table))
    (coerce (sort keys #'string<) 'simple-vector)))

(defun event-name (event)
  (let ((type (pig:jget event "type")))
    (if (and (>= (length type) 8) (string= "message_" type :end2 8))
        (concatenate 'string type ":" (pig:jget (pig:jget event "message") "role"))
        type)))

(defun quiet-resources (&rest more)
  (apply #'pig:make-resource-options :no-extensions t :no-skills t :no-prompt-templates t :no-themes t :no-context-files t more))

(defun sentinel-tool (name description handler)
  (pig:child-tool name description (pig:from-json "{\"type\":\"object\",\"properties\":{\"x\":{\"type\":\"string\"}},\"required\":[\"x\"]}") handler :label "sentinel"))

(defun no-compaction () (pig:obj "compaction" (pig:obj "enabled" :false)))

(defun string-or-empty (value) (if (stringp value) value ""))

(defun split-lines (text)
  (loop with start = 0
        for end = (position #\Newline text :start start)
        collect (subseq text start end)
        while end do (setf start (1+ end))))

(defun norm (message)
  (if (or (null message) (equal message ""))
      :null
      (let ((lines (split-lines message)))
        (pig:obj "first" (first lines)
                 "docs" (coerce (loop for line in lines
                                      for trimmed = (string-trim (list #\Space #\Tab) line)
                                      when (and (>= (length trimmed) 3) (string= ".md" trimmed :start2 (- (length trimmed) 3)))
                                        collect (subseq trimmed (1+ (or (position-if (lambda (c) (member c '(#\/ #\\))) trimmed :from-end t) -1))))
                                'simple-vector)))))

(defun child-run (ctx params)
  (let ((events '()) (updates 0) (streamed "") (partial ""))
    (let ((session (pig:ctx-create-agent-session
                    ctx :model (pig:obj "provider" "sentinel" "id" "echo") :tools '("sentinel_tool") :in-memory t :settings (no-compaction)
                        :resources (quiet-resources :append-system-prompt '("Child appendix"))
                        :custom-tools (list (sentinel-tool "sentinel_tool" "uppercases x"
                                                           (lambda (c args) (declare (ignore c)) (concatenate 'string (string-upcase (pig:jget args "x")) "!")))))))
      (unwind-protect
           (progn
             (pig:agent-session-subscribe session
                                          (lambda (event)
                                            (push (event-name event) events)
                                            (when (equal (pig:jget event "type") "message_update")
                                              (incf updates)
                                              (setf streamed (text-of (pig:jget event "message"))
                                                    partial (text-of (pig:jref event "assistantMessageEvent" "partial"))))))
             (pig:agent-session-prompt session (pig:jget params "prompt"))
             (let* ((messages (pig:agent-session-messages session))
                    (first (if (plusp (length messages)) (aref messages 0) (pig:obj)))
                    (state (pig:agent-session-state session)))
               (pig:to-json
                (pig:obj "events" (coerce (reverse events) 'simple-vector) "updates" updates "streamed" streamed "partial" partial
                         "roles" (map 'simple-vector (lambda (m) (pig:jget m "role")) messages)
                         "system" (pig:obj "keys" (sorted-keys first) "role" (or (pig:jget first "role") :null) "content" (or (pig:jget first "content") :null)
                                           "sections" (sorted-keys (pig:jget first "sections")) "toolsAdded" (or (pig:jget first "toolsAdded") :null))
                         "final" (string-or-empty (pig:jget state "lastAssistantText")) "model" (pig:jref state "model" "id")
                         "activeTools" (pig:jget state "activeTools") "file" (string-or-empty (pig:jget state "sessionFile"))
                         "appendix" (if (search "Child appendix" (pig:jget state "systemPrompt")) t :false)))))
        (pig:agent-session-dispose session)))))

(defun hanging-tool (started saw)
  (sentinel-tool "sentinel_tool" "waits for its abort"
                 (lambda (c args)
                   (declare (ignore args))
                   (sb-thread:signal-semaphore started)
                   (loop until (pig:ctx-cancelled-p c) do (sleep 0.005))
                   (when saw (setf (car saw) t))
                   (error "aborted"))))

(defun prompt-error (session text)
  (handler-case (progn (pig:agent-session-prompt session text) "resolved")
    (error (e) (princ-to-string e))))

(defun child-lifecycle (ctx params)
  (declare (ignore params))
  (let* ((started (sb-thread:make-semaphore)) (saw (list nil))
         (session (pig:ctx-create-agent-session
                   ctx :model (pig:obj "provider" "sentinel" "id" "echo") :tools '("sentinel_tool") :in-memory t :settings (no-compaction)
                       :resources (quiet-resources) :custom-tools (list (hanging-tool started saw))))
         (events '()))
    (unwind-protect
         (progn
           (pig:agent-session-subscribe session
                                        (lambda (e)
                                          (push (concatenate 'string (pig:jget e "type")
                                                             (if (equal (pig:jget e "type") "agent_settled")
                                                                 (format nil ":aborted=~A" (if (eq (pig:jget e "aborted") t) "true" "false"))
                                                                 ""))
                                                events)))
           (let* ((running (sb-thread:make-thread (lambda () (pig:agent-session-prompt session "tool:hang")) :name "child-prompt"))
                  (concurrent (progn (sb-thread:wait-on-semaphore started :timeout 10) (prompt-error session "again"))))
             (pig:agent-session-abort session)
             (sb-thread:join-thread running :default nil :timeout 10)
             (let* ((messages (pig:agent-session-messages session))
                    (out (pig:obj "concurrentPrompt" concurrent "toolSawAbort" (if (car saw) t :false)
                                  "idleAfterAbort" (if (eq (pig:jget (pig:agent-session-state session) "isStreaming") t) :false t)
                                  "tail" (coerce (reverse (subseq events 0 (min 4 (length events)))) 'simple-vector)
                                  "lastRole" (pig:jget (aref messages (1- (length messages))) "role"))))
               (pig:agent-session-dispose session)
               (setf (gethash "afterDispose" out) (prompt-error session "after dispose"))
               (pig:to-json out))))
      (pig:agent-session-dispose session))))

(defun child-hold (ctx params)
  (declare (ignore params))
  (let* ((started (sb-thread:make-semaphore))
         (session (pig:ctx-create-agent-session
                   ctx :model (pig:obj "provider" "sentinel" "id" "echo") :tools '("sentinel_tool") :in-memory t
                       :resources (quiet-resources) :custom-tools (list (hanging-tool started nil)))))
    (sb-thread:make-thread (lambda () (ignore-errors (pig:agent-session-prompt session "tool:hang"))) :name "child-hold")
    (sb-thread:wait-on-semaphore started :timeout 10)
    (pig:to-json (pig:obj "held" t))))

(defun child-errors (ctx params)
  (declare (ignore params))
  (let ((out (pig:obj)))
    (loop for (name model) in (list (list "no-key" (pig:obj "provider" "nokey" "id" "locked")) (list "no-model" nil))
          do (let* ((session (pig:ctx-create-agent-session ctx :model model :in-memory t :settings (no-compaction) :resources (quiet-resources)))
                    (error-text (let ((text (prompt-error session "hi"))) (if (equal text "resolved") "" text)))
                    (state (pig:agent-session-state session)))
               (pig:agent-session-dispose session)
               (setf (gethash name out)
                     (pig:obj "fallback" (norm (pig:agent-session-model-fallback-message session))
                              "model" (or (pig:jref state "model" "id") :null) "promptError" (norm error-text)))))
    (pig:to-json out)))

(defun child-events (ctx params)
  (let* ((scope (list (pig:obj "model" (pig:obj "provider" "sentinel" "id" "echo") "thinkingLevel" "high")
                      (pig:obj "model" (pig:obj "provider" "nokey" "id" "locked") "thinkingLevel" "low")))
         (probe (pig:jget params "probe"))
         (loader (when (equal (pig:jget params "via") "loader")
                   (let ((loader (pig:make-resource-loader ctx :cwd (pig:jget params "cwd") :agent-dir (pig:jget params "agentDir")
                                                               :additional-extension-paths (list probe) :no-extensions t :no-skills t
                                                               :no-prompt-templates t :no-themes t :no-context-files t)))
                     (pig:loader-reload loader)
                     loader)))
         (session (apply #'pig:ctx-create-agent-session
                         ctx :model (pig:obj "provider" "sentinel" "id" "echo") :in-memory t :settings (no-compaction) :scoped-models scope
                             (if loader
                                 (list :resource-loader loader)
                                 (list :resources (quiet-resources :additional-extension-paths (list probe))))))
         (seen '()))
    (unwind-protect
         (progn
           (pig:agent-session-subscribe session
                                        (lambda (event)
                                          (let ((name (event-name event)))
                                            (when (equal name "entry_appended")
                                              (setf name (concatenate 'string name ":" (pig:jref event "entry" "customType"))))
                                            (push name seen))))
           (pig:agent-session-bind-extensions session)
           (pig:agent-session-prompt session "stream:3")
           (pig:to-json (coerce (reverse seen) 'simple-vector)))
      (pig:agent-session-dispose session))))

(defun new-extension ()
  (let ((ext (pig:make-extension "child-session")))
    (loop for (name function) in (list (list "child_run" #'child-run) (list "child_lifecycle" #'child-lifecycle) (list "child_hold" #'child-hold)
                                       (list "child_errors" #'child-errors) (list "child_events" #'child-events))
          do (pig:tool ext name name (pig:obj "type" "object") function))
    ext))
