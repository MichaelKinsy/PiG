(in-package "lisp-sdk-fixture")
;;; the probe commands, tools and event handlers of lane C (see def-fixture in fixture.lisp)

(defun coding-bool (value) (if value t :false))
(defun coding-or-null (value) (if value value :null))

(defun coding-report (ctx prefix value)
  "Notify PREFIX:<value as JSON>:end, the report every probe of the lane's tests reads."
  (pig:ctx-notify ctx (format nil "~A:~A:end" prefix (pig:to-json value)) "info"))

(defun coding-member (case key) (let ((v (pig:jget case key))) (if (eq v :null) nil v)))

;;; truncate_probe runs one truncation helper on the JSON case in its argument (TestConformance_TruncationHelpersAgreeAcrossSDKs).
(def-fixture "truncate_probe" (ext)
  (pig:command ext "truncate_probe" "Run a truncation helper on a JSON case and report the result"
    (lambda (ctx args)
      (let* ((case (pig:from-json args)) (fn (pig:jget case "fn"))
             (result
               (cond
                 ((member fn '("head" "tail") :test #'equal)
                  (let ((r (funcall (if (equal fn "head") #'pig:truncate-head #'pig:truncate-tail) (pig:jget case "content")
                                    :max-lines (coding-member case "maxLines") :max-bytes (coding-member case "maxBytes"))))
                    (pig:obj "content" (pig:truncation-result-content r) "truncated" (coding-bool (pig:truncation-result-truncated r))
                             "truncatedBy" (coding-or-null (pig:truncation-result-truncated-by r))
                             "totalLines" (pig:truncation-result-total-lines r) "totalBytes" (pig:truncation-result-total-bytes r)
                             "outputLines" (pig:truncation-result-output-lines r) "outputBytes" (pig:truncation-result-output-bytes r)
                             "lastLinePartial" (coding-bool (pig:truncation-result-last-line-partial r))
                             "firstLineExceedsLimit" (coding-bool (pig:truncation-result-first-line-exceeds-limit r))
                             "maxLines" (pig:truncation-result-max-lines r) "maxBytes" (pig:truncation-result-max-bytes r))))
                 ((equal fn "line")
                  (multiple-value-bind (text was)
                      (pig:truncate-line (pig:jget case "content") (or (coding-member case "maxChars") pig:+grep-max-line-length+))
                    (pig:obj "text" text "wasTruncated" (coding-bool was))))
                 ((equal fn "limits") (pig:obj "maxLines" pig:+default-max-lines+ "maxBytes" pig:+default-max-bytes+))
                 (t (pig:obj "text" (pig:format-size (pig:jget case "bytes")))))))
        (coding-report ctx "truncate" result)))))

;;; message_probe reads an event payload with the SDK's message-role and message-text (TestConformance_MessageHelpersAgreeAcrossSDKs).
(def-fixture "message_probe" (ext)
  (pig:command ext "message_probe" "Report message-role and message-text of a JSON event payload"
    (lambda (ctx args)
      (let ((data (pig:from-json args)))
        (coding-report ctx "message" (pig:obj "role" (pig:message-role data) "text" (pig:message-text data)))))))

;;; file_queue_probe runs the tasks of its JSON argument concurrently through the file mutation queue and reports the start/end log and the tasks that failed
;;; (TestConformance_FileMutationQueueAgreesAcrossSDKs). Tasks order themselves by waiting on each other's progress, never by sleeping through a race.
(def-fixture "file_queue_probe" (ext)
  (pig:command ext "file_queue_probe" "Run tasks through the file mutation queue and report the log"
    (lambda (ctx args)
      (let* ((case (pig:from-json args)) (lock (sb-thread:make-mutex :name "file queue probe"))
             (log '()) (failed '()) (progress '()))
        (labels ((mark (kind id) (sb-thread:with-mutex (lock) (push (cons kind id) progress)))
                 (note (text) (sb-thread:with-mutex (lock) (push text log)))
                 (await-stage (kind ids who)
                   (loop for other across ids
                         do (let ((deadline (+ (get-internal-real-time) (* 20 internal-time-units-per-second))))
                              (loop
                                (when (sb-thread:with-mutex (lock) (member (cons kind other) progress :test #'equal)) (return))
                                (when (> (get-internal-real-time) deadline)
                                  (note (format nil "timeout:~D:~A:~D" who kind other))
                                  (return-from await-stage))
                                (sleep 0.001)))))
                 (settle (ids) (when (plusp (length ids)) (sleep (/ (pig:jget case "settleMs") 1000.0))))
                 (ids (task key) (or (pig:jget task key) (vector)))
                 (run (task)
                   (let ((id (pig:jget task "id")))
                     (await-stage "started" (ids task "waitStarted") id)
                     (await-stage "called" (ids task "waitCalled") id)
                     (settle (ids task "waitCalled"))
                     (mark "called" id)
                     (handler-case
                         (pig:with-file-mutation-queue
                          (pig:jget task "path")
                          (lambda ()
                            (note (format nil "start:~D" id))
                            (mark "started" id)
                            (await-stage "ended" (ids task "holdEnded") id)
                            (await-stage "called" (ids task "holdCalled") id)
                            (settle (ids task "holdCalled"))
                            (note (format nil "end:~D" id))
                            (mark "ended" id)
                            (when (eq (pig:jget task "fail") t) (error "task failed"))))
                       (error ()
                         (mark "ended" id)
                         (sb-thread:with-mutex (lock) (push id failed)))))))
          (let ((threads (map 'list (lambda (task) (sb-thread:make-thread (lambda () (run task)) :name "file-queue-task")) (pig:jget case "tasks"))))
            (mapc #'sb-thread:join-thread threads)))
        (coding-report ctx "file_queue" (pig:obj "log" (coerce (reverse log) 'simple-vector) "errors" (coerce (sort (copy-list failed) #'<) 'simple-vector)))))))

;;; host_helpers_probe runs the host-served pi helpers over the golden file's inputs (TestConformance_HostHelpersAgreeAcrossSDKs). args is "<golden>|<auth.json path>|<text to copy>".
(defun coding-base64-decode (text)
  (let ((alphabet "ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz0123456789+/") (bits 0) (count 0) (out '()))
    (loop for c across text
          for value = (position c alphabet)
          do (when value
               (setf bits (logior (ash bits 6) value)) (incf count 6)
               (when (>= count 8) (decf count 8) (push (ldb (byte 8 count) bits) out) (setf bits (ldb (byte count 0) bits)))))
    (coerce (nreverse out) '(simple-array (unsigned-byte 8) (*)))))

(defun coding-read-file (path)
  (with-open-file (in path :external-format :utf-8)
    (let* ((text (make-string (file-length in))) (n (read-sequence text in))) (subseq text 0 n))))

(defun coding-split-args (args)
  (let* ((first (position #\| args)) (second (position #\| args :start (1+ first))))
    (list (subseq args 0 first) (subseq args (1+ first) second) (subseq args (1+ second)))))

(defun coding-context-report (case)
  (let* ((args (pig:jget case "args")) (leaf (if (> (length args) 1) (let ((v (aref args 1))) (if (eq v :null) nil v)) pig:+session-leaf-last+))
         (by-id (and (> (length args) 2)
                     (let ((index (make-hash-table :test 'equal)))
                       (loop for pair across (aref args 2) do (setf (gethash (aref pair 0) index) (aref pair 1)))
                       index))))
    (handler-case (pig:obj "result" (pig:build-session-context (aref args 0) :leaf-id leaf :by-id by-id))
      (pig:js-type-error (e) (pig:obj "throws" (pig:js-type-error-message e))))))

(defun coding-image-report (case)
  (if (equal (pig:jget case "op") "convert")
      (let ((converted (pig:convert-to-png (pig:jget case "data") (pig:jget case "mimeType"))))
        (if converted
            (pig:obj "mimeType" (pig:converted-image-mime-type converted) "decodes" (coding-bool (string/= (pig:converted-image-data converted) "")))
            :null))
      (let* ((options (coding-member case "options"))
             (resized (pig:resize-image (coding-base64-decode (pig:jget case "data")) (pig:jget case "mimeType")
                                        (and options (pig:make-image-resize-options :max-width (coding-member options "maxWidth") :max-height (coding-member options "maxHeight")
                                                                                    :max-bytes (coding-member options "maxBytes") :jpeg-quality (coding-member options "jpegQuality"))))))
        (if resized
            (pig:obj "mimeType" (pig:resized-image-mime-type resized) "originalWidth" (pig:resized-image-original-width resized)
                     "originalHeight" (pig:resized-image-original-height resized) "width" (pig:resized-image-width resized)
                     "height" (pig:resized-image-height resized) "wasResized" (coding-bool (pig:resized-image-was-resized resized))
                     "decodes" (coding-bool (string/= (pig:resized-image-data resized) "")))
            :null))))

(def-fixture "host_helpers_probe" (ext)
  (pig:command ext "host_helpers_probe" "Run the host-served pi helpers over the golden inputs"
    (lambda (ctx args)
      (destructuring-bind (golden-path auth-path text) (coding-split-args args)
        (let ((golden (pig:from-json (coding-read-file golden-path))))
          (coding-report
           ctx "host_helpers"
           (pig:obj
            "diffs" (map 'simple-vector
                         (lambda (case)
                           (let* ((lines (coding-member case "contextLines"))
                                  (report (pig:obj "patch" (pig:generate-unified-patch (or (coding-member case "path") "src/a.ts") (pig:jget case "oldContent") (pig:jget case "newContent") lines))))
                             (unless (eq (pig:jget case "patchOnly") t)
                               (let ((diff (pig:generate-diff-string (pig:jget case "oldContent") (pig:jget case "newContent") lines)))
                                 (setf (gethash "diff" report) (pig:diff-string-diff diff)
                                       (gethash "firstChangedLine" report) (coding-or-null (pig:diff-string-first-changed-line diff)))))
                             report))
                         (pig:jget golden "diffs"))
            "entries" (map 'simple-vector (lambda (case) (pig:session-entry-to-context-messages (pig:jget case "entry"))) (pig:jget golden "entries"))
            "contexts" (map 'simple-vector #'coding-context-report (pig:jget golden "contexts"))
            "images" (map 'simple-vector #'coding-image-report (pig:jget golden "images"))
            "creds" (pig:obj "explicit" (coding-or-null (pig:read-stored-credential "anthropic" auth-path))
                             "default" (coding-or-null (pig:read-stored-credential "anthropic"))
                             "absentProvider" (coding-or-null (pig:read-stored-credential "nobody" auth-path))
                             "missingFile" (coding-or-null (pig:read-stored-credential "anthropic" (concatenate 'string auth-path ".missing")))
                             "notJSON" (coding-or-null (pig:read-stored-credential "anthropic" (concatenate 'string auth-path ".bad"))))))
          (pig:copy-to-clipboard text))))))

;;; tool_call_guard_probe reports which tool names the guard accepts for each tool_call event it receives (TestConformance_ToolCallGuardAgreesAcrossSDKs).
(def-fixture "tool_call_guard_probe" (ext)
  (pig:command ext "tool_call_guard_probe" "Report the tool names the tool_call guard accepts"
    (lambda (ctx args)
      (declare (ignore ctx args))
      (pig:on ext :tool-call
              (lambda (hctx data)
                (pig:ctx-notify hctx (format nil "tool_call_guard:~{~A~^,~}:end"
                                             (remove-if-not (lambda (name) (pig:tool-call-event-type-p name data)) '("bash" "read" "probe" "other")))
                                "info")
                nil)))))

;;; language_probe reports the highlight language of a path (TestConformance_LanguageFromPathAgreesAcrossSDKs).
(def-fixture "language_probe" (ext)
  (pig:command ext "language_probe" "Report the highlight language of a path"
    (lambda (ctx args)
      (coding-report ctx "language" (pig:obj "lang" (coding-or-null (pig:get-language-from-path (pig:jget (pig:from-json args) "path"))))))))

;;; helper_probe, helper_factory_probe: parseFrontmatter, stripFrontmatter and Date.parse (TestConformance_HostParserHelpersAgreeAcrossSDKs). The factory-time
;;; results are computed while the factory runs, before the connection to the host is ready.
(defun coding-key-lists (value &optional (out (list '())))
  (cond ((and (vectorp value) (not (stringp value))) (loop for element across value do (coding-key-lists element out)))
        ((hash-table-p value)
         (push (format nil "~{~A~^,~}" (loop for k being the hash-keys of value collect k)) (car out))
         (loop for element being the hash-values of value do (coding-key-lists element out))))
  out)

(defun coding-helper-report (fn text)
  (handler-case
      (cond ((equal fn "date") (pig:obj "ms" (coding-or-null (pig:parse-date text))))
            ((equal fn "strip") (pig:obj "body" (pig:strip-frontmatter text)))
            (t (let ((parsed (pig:parse-frontmatter text)))
                 (pig:obj "frontmatter" (coding-or-null (pig:parsed-frontmatter-frontmatter parsed)) "body" (pig:parsed-frontmatter-body parsed)
                          "keys" (coerce (reverse (car (coding-key-lists (pig:parsed-frontmatter-frontmatter parsed)))) 'simple-vector)))))
    (error () (pig:obj "error" t))))

(def-fixture "helper_probe" (ext)
  (let ((factory-helpers
          (pig:obj "frontmatter" (coding-helper-report "frontmatter" (format nil "---~%z: 1~%10: a~%2: b~%nested:~%  y: 1~%  3: x~%  \"q r\": [1, 2]~%---~%body text~%"))
                   "error" (coding-helper-report "frontmatter" (format nil "---~%a: [~%---~%x"))
                   "date" (coding-helper-report "date" "2020-01-02T03:04:05.678Z")
                   "strip" (coding-helper-report "strip" (format nil "---~%z: 1~%---~%body text~%")))))
    (pig:command ext "helper_probe" "Run parseFrontmatter, stripFrontmatter or Date.parse on its argument"
      (lambda (ctx args)
        (let ((c (pig:from-json args)))
          (coding-report ctx "helper" (coding-helper-report (pig:jget c "fn") (pig:jget c "input"))))))
    (pig:command ext "helper_factory_probe" "Report the helpers' results from the factory"
      (lambda (ctx args) (declare (ignore args)) (coding-report ctx "helper-factory" factory-helpers)))))

;;; convert_probe, serialize_probe (TestConformance_MessageConversionAgreesAcrossSDKs, TestConformance_SerializeConversationAgreesAcrossSDKs).
(def-fixture "convert_probe" (ext)
  (pig:command ext "convert_probe" "Run convertToLlm or formatDimensionNote on a JSON case and report the result"
    (lambda (ctx args)
      (let* ((c (pig:from-json args))
             (result (if (equal (pig:jget c "fn") "convert")
                         (pig:convert-to-llm (pig:jget c "messages"))
                         (let ((i (pig:jget c "image")))
                           (coding-or-null (pig:format-dimension-note
                                            (pig:make-resized-image :original-width (pig:jget i "originalWidth") :original-height (pig:jget i "originalHeight")
                                                                    :width (pig:jget i "width") :height (pig:jget i "height")
                                                                    :was-resized (eq (pig:jget i "wasResized") t))))))))
        (coding-report ctx "convert" (pig:obj "result" result))))))

(def-fixture "serialize_probe" (ext)
  (pig:command ext "serialize_probe" "Run serializeConversation on JSON messages and report the text"
    (lambda (ctx args)
      (coding-report ctx "serialize"
                     (handler-case (pig:obj "result" (pig:serialize-conversation (pig:jget (pig:from-json args) "messages")))
                       (pig:js-type-error () (pig:obj "error" t)))))))

;;; branch_probe runs a branch summarization helper on a JSON case (TestConformance_BranchSummaryHelpersAgreeAcrossSDKs).
(def-fixture "branch_probe" (ext)
  (pig:command ext "branch_probe" "Run a branch summarization helper on a JSON case and report the result"
    (lambda (ctx args)
      (let* ((c (pig:from-json args)) (by-id (make-hash-table :test 'equal)))
        (loop for entry across (pig:jget c "entries") do (setf (gethash (pig:jget entry "id") by-id) entry))
        (coding-report
         ctx "branch"
         (handler-case
             (if (equal (pig:jget c "fn") "prepare")
                 (let ((r (pig:prepare-branch-entries (pig:jget c "entries") (pig:jget c "budget"))))
                   (pig:obj "messages" (pig:branch-preparation-messages r)
                            "fileOps" (pig:obj "read" (pig:file-operations-read (pig:branch-preparation-file-ops r))
                                               "written" (pig:file-operations-written (pig:branch-preparation-file-ops r))
                                               "edited" (pig:file-operations-edited (pig:branch-preparation-file-ops r)))
                            "totalTokens" (pig:branch-preparation-total-tokens r)))
                 (let ((r (pig:collect-entries-for-branch-summary
                           (cons (lambda (id) (values (gethash id by-id)))
                                 (lambda (from)
                                   (let ((path '()))
                                     (loop for entry = (gethash from by-id) then (let ((parent (coding-member entry "parentId"))) (and parent (gethash parent by-id)))
                                           while entry do (push entry path))
                                     (coerce path 'simple-vector))))
                           (pig:jget c "old") (pig:jget c "target"))))
                   (pig:obj "entries" (pig:collect-entries-result-entries r) "commonAncestorId" (coding-or-null (pig:collect-entries-result-common-ancestor-id r)))))
           (pig:js-type-error () (pig:obj "error" t))))))))

;;; compact_usage_probe runs a compaction helper on a JSON case (TestConformance_CompactionUsageAgreesAcrossSDKs).
(def-fixture "compact_usage_probe" (ext)
  (pig:command ext "compact_usage_probe" "Run a compaction usage helper on a JSON case and report the result"
    (lambda (ctx args)
      (let* ((c (pig:from-json args)) (fn (pig:jget c "fn"))
             (result
               (cond
                 ((equal fn "calc") (pig:obj "tokens" (pig:calculate-context-tokens (pig:jget c "usage"))))
                 ((equal fn "should")
                  (let ((s (pig:jget c "settings")))
                    (pig:obj "result" (coding-bool (pig:should-compact (pig:jget c "tokens") (pig:jget c "window")
                                                                       (pig:make-compaction-settings :enabled (eq (pig:jget s "enabled") t)
                                                                                                     :reserve-tokens (pig:jget s "reserveTokens")
                                                                                                     :keep-recent-tokens (pig:jget s "keepRecentTokens")))))))
                 ((equal fn "last")
                  (let ((usage (pig:get-last-assistant-usage (pig:jget c "entries"))))
                    (pig:obj "usage" (if usage
                                         (let ((out (pig:obj)))
                                           (dolist (key '("input" "output" "cacheRead" "cacheWrite" "totalTokens") out)
                                             (setf (gethash key out) (or (coding-member usage key) 0))))
                                         :null))))
                 ((equal fn "cut")
                  (let ((cut (pig:find-cut-point (pig:jget c "entries") (pig:jget c "start") (pig:jget c "end") (pig:jget c "keep"))))
                    (pig:obj "firstKeptEntryIndex" (pig:cut-point-result-first-kept-entry-index cut) "turnStartIndex" (pig:cut-point-result-turn-start-index cut)
                             "isSplitTurn" (coding-bool (pig:cut-point-result-split-turn-p cut)))))
                 ((equal fn "turn") (pig:obj "index" (pig:find-turn-start-index (pig:jget c "entries") (pig:jget c "index") (pig:jget c "start"))))
                 ((equal fn "estimate") (pig:obj "tokens" (pig:estimate-tokens (pig:jget c "message"))))
                 (t (let ((d pig:*default-compaction-settings*))
                      (pig:obj "enabled" (coding-bool (pig:compaction-settings-enabled d)) "reserveTokens" (pig:compaction-settings-reserve-tokens d)
                               "keepRecentTokens" (pig:compaction-settings-keep-recent-tokens d)))))))
        (coding-report ctx "compact" result)))))

;;; skills_probe runs a session or skill helper on a JSON case (TestConformance_SessionSkillHelpersAgreeAcrossSDKs).
(def-fixture "skills_probe" (ext)
  (pig:command ext "skills_probe" "Run a session or skill helper on a JSON case and report the result"
    (lambda (ctx args)
      (let* ((c (pig:from-json args)) (fn (pig:jget c "fn"))
             (result
               (cond
                 ((equal fn "latest") (pig:obj "entry" (coding-or-null (pig:get-latest-compaction-entry (pig:jget c "entries")))))
                 ((equal fn "parse")
                  (let ((block (pig:parse-skill-block (pig:jget c "text"))))
                    (pig:obj "block" (if block
                                         (pig:obj "name" (pig:parsed-skill-block-name block) "location" (pig:parsed-skill-block-location block)
                                                  "content" (pig:parsed-skill-block-content block)
                                                  "userMessage" (coding-or-null (pig:parsed-skill-block-user-message block)))
                                         :null))))
                 ((equal fn "format")
                  (let ((tool (coding-member c "tool")))
                    (pig:obj "text" (if tool (pig:format-skills-for-prompt (pig:jget c "skills") tool) (pig:format-skills-for-prompt (pig:jget c "skills"))))))
                 (t (pig:obj "version" pig:+current-session-version+ "virtual" pig:*virtual-model-state-entry*)))))
        (coding-report ctx "skills" result)))))

;;; tool_result guards: every tool_result event is reported with the guards that accept it (TestToolResultGuardsMatchAcrossSDKs, TestConformance_ToolResultGuards...).
(defparameter *coding-result-guards*
  (list (cons "bash" #'pig:bash-tool-result-p) (cons "powershell" #'pig:powershell-tool-result-p) (cons "read" #'pig:read-tool-result-p)
        (cons "edit" #'pig:edit-tool-result-p) (cons "write" #'pig:write-tool-result-p) (cons "grep" #'pig:grep-tool-result-p)
        (cons "find" #'pig:find-tool-result-p) (cons "ls" #'pig:ls-tool-result-p)))

(defun coding-accepting-guards (data)
  (loop for (name . guard) in *coding-result-guards* when (funcall guard data) collect name))

(def-fixture "tool_result_guards" (ext)
  (pig:on ext :tool-result
          (lambda (ctx data)
            (pig:ctx-notify ctx (format nil "tool-result-guards=~{~A~^,~}" (coding-accepting-guards data)) "info")
            nil))
  (pig:command ext "tool_result_guards_probe" "Report the tool_result guards that accept each tool_result event"
    (lambda (ctx args)
      (declare (ignore ctx args))
      (pig:on ext :tool-result
              (lambda (hctx data)
                (pig:ctx-notify hctx (format nil "tool_result_guards:~{~A~^,~}:end" (sort (coding-accepting-guards data) #'string<)) "info")
                nil)))))
