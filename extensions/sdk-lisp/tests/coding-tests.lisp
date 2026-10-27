;;;; SPDX-License-Identifier: MIT
;;;; Lane C: the pi-coding-agent helpers. The Pi vectors (test/parity/sdk-vectors/pi-coding-agent/<export>.json) are what the pinned package's own functions
;;;; return; every case of every file is run. Host-served helpers run against a fake host that answers the call the SDK makes.
(in-package #:pig-tests)

(defparameter *ct-vectors* "test/parity/sdk-vectors/pi-coding-agent/")

(defun ct-cases (export)
  (let ((cases (pig:jget (pig:from-json (read-text-file (repo-path (format nil "~A~A.json" *ct-vectors* export)))) "cases")))
    (check (plusp (length cases)) export)
    cases))

(defun ct-undefined-p (value) (and (hash-table-p value) (equal (pig:jget value "$") "undefined")))

(defun ct-arg (case index)
  "Argument INDEX of the vector CASE, NIL when absent or Pi's undefined."
  (let* ((args (pig:jget case "args")) (value (and (< index (length args)) (aref args index))))
    (if (ct-undefined-p value) nil value)))

(defun ct-equal (a b)
  "JSON equality: strings by case, numbers by value, objects by members."
  (cond ((and (hash-table-p a) (hash-table-p b))
         (and (= (hash-table-count a) (hash-table-count b))
              (loop for k being the hash-keys of a using (hash-value v)
                    always (multiple-value-bind (w found) (gethash k b) (and found (ct-equal v w))))))
        ((and (stringp a) (stringp b)) (string= a b))
        ((and (realp a) (realp b)) (= a b))
        ((and (vectorp a) (vectorp b) (not (stringp a)) (not (stringp b)))
         (and (= (length a) (length b)) (every #'ct-equal a b)))
        (t (eql a b))))

(defmacro check-json (expected actual &optional message)
  `(let ((e ,expected) (a ,actual))
     (check (ct-equal e a) (format nil "expected ~A, got ~A~@[ (~A)~]" (pig:to-json e) (pig:to-json a) ,message))))

(defun json-of (text) (pig:from-json text))

;;; ---------------------------------------------------------------- a fake host that answers the calls helpers make

(defvar *ct-calls* '())

(defun ct-answer-call (host frame function)
  (let* ((call (pig:jget frame "call")) (method (pig:jget call "method")) (args (pig:jget call "args")))
    (push (cons method args) *ct-calls*)
    (hsend host "type" "call_result" "id" (pig:jget frame "id")
                "call_result" (handler-case (pig:obj "result" (funcall function method args))
                                (error (e) (pig:obj "error" (pig:obj "message" (princ-to-string e))))))))

(defun ct-answer-calls (host function)
  (sb-thread:make-thread
   (lambda ()
     (loop
       (let ((frame (handler-case (hread host) (error () :eof))))
         (when (eq frame :eof) (return))
         (when (equal (pig:jget frame "type") "call") (ct-answer-call host frame function)))))
   :name "coding-test-host"))

(defmacro with-answering-host ((function) &body body)
  "BODY with an extension running against a fake host whose answer to each call is (FUNCTION method args); *CT-CALLS* is the calls, newest first."
  `(progn
     (setf *ct-calls* '())
     (with-host (host (pig:make-extension "coding"))
       (ct-answer-calls host ,function)
       ,@body)))

(defun ct-calls (method) (reverse (loop for (m . args) in *ct-calls* when (string= m method) collect args)))

;;; ---------------------------------------------------------------- truncation

(deftest truncate-limits-and-sizes
  (check-equal 2000 pig:+default-max-lines+)
  (check-equal 51200 pig:+default-max-bytes+)
  (check-equal 500 pig:+grep-max-line-length+)
  (check-equal '("0B" "1023B" "1.0KB" "1.5KB" "50.0KB" "1.0MB" "2.5MB")
               (mapcar #'pig:format-size '(0 1023 1024 1536 51200 1048576 2621440)))
  ;; toFixed rounds an exact tie up
  (check-equal "1.1KB" (pig:format-size 1126))
  (check-equal "1.0KB" (pig:format-size 1075)))

(deftest truncate-head-keeps-whole-lines
  (let ((r (pig:truncate-head (format nil "a~%b~%c~%d") :max-lines 2)))
    (check-equal (format nil "a~%b") (pig:truncation-result-content r))
    (check (pig:truncation-result-truncated r))
    (check-equal "lines" (pig:truncation-result-truncated-by r))
    (check-equal '(4 7 2 3) (list (pig:truncation-result-total-lines r) (pig:truncation-result-total-bytes r)
                                  (pig:truncation-result-output-lines r) (pig:truncation-result-output-bytes r))))
  (let ((r (pig:truncate-head "no newline at all" :max-bytes 4)))
    (check-equal "" (pig:truncation-result-content r))
    (check (pig:truncation-result-first-line-exceeds-limit r))
    (check-equal "bytes" (pig:truncation-result-truncated-by r)))
  (let ((r (pig:truncate-head (format nil "one~%two~%"))))
    (check (not (pig:truncation-result-truncated r)))
    (check-equal nil (pig:truncation-result-truncated-by r))
    (check-equal '(2 2000 51200) (list (pig:truncation-result-total-lines r) (pig:truncation-result-max-lines r) (pig:truncation-result-max-bytes r)))))

(deftest truncate-tail-keeps-the-end
  (let ((r (pig:truncate-tail (format nil "a~%b~%c~%d") :max-lines 2)))
    (check-equal (format nil "c~%d") (pig:truncation-result-content r))
    (check-equal "lines" (pig:truncation-result-truncated-by r)))
  ;; the last line alone exceeds the limit: its end is kept, from a character boundary
  (let ((r (pig:truncate-tail (format nil "short~%~A" (make-string 10 :initial-element (code-char 233))) :max-bytes 7)))
    (check-equal (make-string 3 :initial-element (code-char 233)) (pig:truncation-result-content r))
    (check (pig:truncation-result-last-line-partial r))
    (check-equal "bytes" (pig:truncation-result-truncated-by r))))

(deftest truncate-line-counts-utf16-units
  (multiple-value-bind (text was) (pig:truncate-line (make-string 501 :initial-element #\x))
    (check was)
    (check-equal (concatenate 'string (make-string 500 :initial-element #\x) "... [truncated]") text))
  (multiple-value-bind (text was) (pig:truncate-line "short")
    (check-equal "short" text) (check (not was)))
  (let ((emoji (make-string 10 :initial-element (code-char #x1F600))))
    ;; 10 emoji are 20 UTF-16 units: a limit of 16 keeps 8 of them, a limit of 7 cuts a pair and keeps the high surrogate
    (check-equal (concatenate 'string (subseq emoji 0 8) "... [truncated]") (pig:truncate-line emoji 16))
    (check-equal (concatenate 'string (subseq emoji 0 3) (string (code-char #xD83D)) "... [truncated]") (pig:truncate-line emoji 7))))

(deftest truncate-surrogates-and-negative-limits-match-pi
  ;; Pi: truncateTail("ab\ud800cd", {maxBytes: 6}) keeps "b\ufffdcd" (6 bytes): Buffer.from turns the lone surrogate into one U+FFFD
  (let ((r (pig:truncate-tail (json-of "\"ab\\ud800cd\"") :max-bytes 6)))
    (check-equal (list (json-of "\"b\\ufffdcd\"") 6 7 t)
                 (list (pig:truncation-result-content r) (pig:truncation-result-output-bytes r) (pig:truncation-result-total-bytes r)
                       (pig:truncation-result-last-line-partial r))))
  ;; a surrogate pair held as two characters is one four-byte character
  (let ((r (pig:truncate-head (concatenate 'string "a" (string (code-char #xD83D)) (string (code-char #xDE00)) "b") :max-bytes 100)))
    (check-equal (list 6 nil) (list (pig:truncation-result-total-bytes r) (pig:truncation-result-truncated r))))
  (let ((r (pig:truncate-tail (format nil "abc~%def") :max-bytes -1)))
    (check-equal (list "" 1 0 t "bytes")
                 (list (pig:truncation-result-content r) (pig:truncation-result-output-lines r) (pig:truncation-result-output-bytes r)
                       (pig:truncation-result-last-line-partial r) (pig:truncation-result-truncated-by r))))
  (check-equal "abcd... [truncated]" (pig:truncate-line "abcdef" -2))
  (check-equal "... [truncated]" (pig:truncate-line "ab" -5)))

(deftest truncate-to-visual-lines-matches-pi
  (dolist (case (coerce (ct-cases "truncateToVisualLines") 'list))
    (let* ((padding (ct-arg case 3)) (keep (ct-arg case 4))
           (got (pig:truncate-to-visual-lines (ct-arg case 0) (ct-arg case 1) (ct-arg case 2)
                                              :padding-x (or padding 0) :keep (or keep "end")))
           (want (pig:jget case "result")))
      (check-json (pig:jget want "visualLines") (pig:visual-truncate-result-visual-lines got) (pig:jget case "name"))
      (check-equal (pig:jget want "skippedCount") (pig:visual-truncate-result-skipped-count got) (pig:jget case "name")))))

;;; ---------------------------------------------------------------- compaction

(deftest compaction-defaults-and-usage
  (let ((d pig:*default-compaction-settings*))
    (check-equal '(t 16384 20000) (list (pig:compaction-settings-enabled d) (pig:compaction-settings-reserve-tokens d) (pig:compaction-settings-keep-recent-tokens d))))
  (check-equal 10 (pig:calculate-context-tokens (json-of "{\"totalTokens\":10,\"input\":1}")))
  (check-equal 6 (pig:calculate-context-tokens (json-of "{\"totalTokens\":0,\"input\":1,\"output\":2,\"cacheRead\":null,\"cacheWrite\":3}")))
  ;; a missing count is NaN, a null one is 0
  (check (sb-ext:float-nan-p (pig:calculate-context-tokens (json-of "{\"input\":1}"))))
  (check (pig:should-compact 96 100 (pig:make-compaction-settings :reserve-tokens 5)))
  (check (not (pig:should-compact 95 100 (pig:make-compaction-settings :reserve-tokens 5))))
  (check (not (pig:should-compact 1000 100 (pig:make-compaction-settings :enabled nil))))
  (let* ((entries (json-of "[{\"type\":\"message\",\"message\":{\"role\":\"assistant\",\"usage\":{\"input\":5,\"output\":1,\"cacheRead\":0,\"cacheWrite\":0}}},
                             {\"type\":\"message\",\"message\":{\"role\":\"assistant\",\"stopReason\":\"error\",\"usage\":{\"input\":9}}},
                             {\"type\":\"message\",\"message\":{\"role\":\"assistant\",\"usage\":{\"input\":0,\"output\":0,\"cacheRead\":0,\"cacheWrite\":0}}},
                             {\"type\":\"message\",\"message\":{\"role\":\"user\"}}]"))
         (usage (pig:get-last-assistant-usage entries)))
    (check-equal 5 (pig:jget usage "input"))
    (check-equal nil (pig:get-last-assistant-usage (vector)))))

(deftest estimate-tokens-counts-utf16-and-json-lengths
  (flet ((tokens (text) (pig:estimate-tokens (json-of text))))
    (check-equal 1 (tokens "{\"role\":\"user\",\"content\":\"abcd\"}"))
    (check-equal 2 (tokens "{\"role\":\"user\",\"content\":\"abcde\"}"))
    ;; an emoji is two UTF-16 units
    (check-equal 1 (tokens "{\"role\":\"user\",\"content\":\"\\ud83d\\ude00\\ud83d\\ude00\"}"))
    (check-equal 1200 (tokens "{\"role\":\"toolResult\",\"content\":[{\"type\":\"image\"}]}"))
    ;; name (4) + {"a":1} (7) = 11
    (check-equal 3 (tokens "{\"role\":\"assistant\",\"content\":[{\"type\":\"toolCall\",\"name\":\"read\",\"arguments\":{\"a\":1}}]}"))
    (check-equal 3 (tokens "{\"role\":\"bashExecution\",\"command\":\"ls\",\"output\":\"abcdefghij\"}"))
    (check-equal 0 (tokens "{\"role\":\"nobody\",\"content\":\"abcd\"}"))))

(deftest find-cut-point-and-turn-start
  (let* ((entries (json-of "[{\"type\":\"message\",\"message\":{\"role\":\"user\",\"content\":\"aaaaaaaa\"}},
                             {\"type\":\"message\",\"message\":{\"role\":\"assistant\",\"content\":[{\"type\":\"text\",\"text\":\"bbbbbbbb\"}]}},
                             {\"type\":\"label\"},
                             {\"type\":\"message\",\"message\":{\"role\":\"toolResult\",\"content\":\"cccccccc\"}},
                             {\"type\":\"message\",\"message\":{\"role\":\"user\",\"content\":\"dddddddd\"}}]"))
         (cut (pig:find-cut-point entries 0 5 3)))
    (check-equal (list 4 -1 nil) (list (pig:cut-point-result-first-kept-entry-index cut) (pig:cut-point-result-turn-start-index cut)
                                       (pig:cut-point-result-split-turn-p cut)))
    (let ((cut (pig:find-cut-point entries 0 5 5)))
      (check-equal (list 1 0 t) (list (pig:cut-point-result-first-kept-entry-index cut) (pig:cut-point-result-turn-start-index cut)
                                      (pig:cut-point-result-split-turn-p cut))))
    (check-equal 0 (pig:find-turn-start-index entries 3 0))
    (check-equal -1 (pig:find-turn-start-index entries 3 1))
    (check-equal 4 (pig:cut-point-result-first-kept-entry-index (pig:find-cut-point entries 4 4 1)))))

;;; ---------------------------------------------------------------- conversation

(deftest convert-to-llm-maps-custom-roles
  (let ((out (pig:convert-to-llm
              (json-of "[{\"role\":\"user\",\"content\":\"hi\"},
                         {\"role\":\"bashExecution\",\"command\":\"ls\",\"output\":\"a\",\"exitCode\":2,\"timestamp\":5},
                         {\"role\":\"bashExecution\",\"command\":\"x\",\"excludeFromContext\":true},
                         {\"role\":\"custom\",\"content\":\"note\",\"timestamp\":6},
                         {\"role\":\"branchSummary\",\"summary\":\"S\"},
                         {\"role\":\"compactionSummary\",\"summary\":\"C\"},
                         {\"role\":\"weird\"}, 5]"))))
    (check-equal 5 (length out))
    (check-json (json-of "{\"role\":\"user\",\"content\":[{\"type\":\"text\",\"text\":\"Ran `ls`\\n```\\na\\n```\\n\\nCommand exited with code 2\"}],\"timestamp\":5}") (aref out 1))
    (check-json (json-of "{\"role\":\"user\",\"content\":[{\"type\":\"text\",\"text\":\"note\"}],\"timestamp\":6}") (aref out 2))
    (check-equal "The following is a summary of a branch that this conversation came back from:

<summary>
S</summary>" (pig:jref (aref out 3) "content" 0 "text"))
    (check-equal "The conversation history before this point was compacted into the following summary:

<summary>
C
</summary>" (pig:jref (aref out 4) "content" 0 "text"))))

(deftest serialize-conversation-matches-pi
  (let ((messages (json-of "[{\"role\":\"user\",\"content\":\"hello\"},
                             {\"role\":\"assistant\",\"content\":[{\"type\":\"thinking\",\"thinking\":\"t\"},{\"type\":\"text\",\"text\":\"a\"},{\"type\":\"text\",\"text\":\"b\"},
                               {\"type\":\"toolCall\",\"name\":\"read\",\"arguments\":{\"z\":1,\"10\":\"x\",\"2\":[1,2]}}]},
                             {\"role\":\"toolResult\",\"content\":[{\"type\":\"text\",\"text\":\"ok\"}]},
                             {\"role\":\"system\",\"content\":\"skipped\"}]")))
    (check-equal (format nil "[User]: hello~%~%[Assistant]: a~%b~%~%[Assistant tool calls]: read(2=[1,2], 10=\"x\", z=1)~%~%[Tool result]: ok")
                 (pig:serialize-conversation messages)))
  (check-error pig:js-type-error (pig:serialize-conversation (json-of "[{\"role\":\"user\",\"content\":5}]")))
  (check-error pig:js-type-error (pig:serialize-conversation (json-of "[null]")))
  ;; Pi's output on unpaired surrogates (Node): JSON.stringify writes them as lowercase \uXXXX escapes, and the truncation slice keeps the high half of a cut pair
  (loop for (message want)
          in (list (list (concatenate 'string "{\"role\":\"toolResult\",\"content\":\"" (make-string 1999 :initial-element #\a) "\\ud83d\\ude00\"}")
                         (concatenate 'string "\"[Tool result]: " (make-string 1999 :initial-element #\a) "\\ud83d\\n\\n[... 1 more characters truncated]\""))
                   (list "{\"role\":\"assistant\",\"content\":[{\"type\":\"toolCall\",\"name\":\"t\",\"arguments\":{\"s\":\"\\udc00\",\"k\\ud83d\":1}}]}"
                         "\"[Assistant tool calls]: t(s=\\\"\\\\udc00\\\", k\\ud83d=1)\"")
                   (list "{\"role\":\"assistant\",\"content\":[{\"type\":\"toolCall\",\"name\":\"t\",\"arguments\":\"\\ud83d\\ud83d\\ude00\"}]}"
                         "\"[Assistant tool calls]: t(0=\\\"\\\\ud83d\\\", 1=\\\"\\\\ud83d\\\", 2=\\\"\\\\ude00\\\")\""))
        do (check-equal (json-of want) (pig:serialize-conversation (vector (json-of message))))))

(deftest session-entries-become-context-messages
  (with-answering-host ((lambda (method args)
                          (check-equal "date.parse" method)
                          (pig:obj "ms" (let ((text (pig:jget args "text")))
                                          (cond ((string= text "2026-01-02") 1767312000000)
                                                ((string= text "Jan 2, 2026 10:00:00 GMT+0200") 1767340800000)
                                                (t :null))))))
    (dolist (case (coerce (ct-cases "sessionEntryToContextMessages") 'list))
      (let ((name (pig:jget case "name")))
        (check-json (pig:jget case "result") (pig:session-entry-to-context-messages (aref (pig:jget case "args") 0)) name)))
    ;; the ISO form toISOString writes is parsed here: only the three other forms reached the host
    (check-equal '("not a date" "2026-01-02" "Jan 2, 2026 10:00:00 GMT+0200")
                 (mapcar (lambda (args) (pig:jget args "text")) (ct-calls "date.parse")))))

(deftest build-session-context-matches-pi
  (dolist (case (coerce (ct-cases "buildSessionContext") 'list))
    (let* ((name (pig:jget case "name")) (args (pig:jget case "args"))
           (leaf (if (> (length args) 1) (let ((v (aref args 1))) (if (eq v :null) nil v)) pig:+session-leaf-last+))
           (by-id (and (> (length args) 2)
                       (let ((index (make-hash-table :test 'equal)))
                         (loop for pair across (aref args 2) do (setf (gethash (aref pair 0) index) (aref pair 1)))
                         index)))
           (throws (pig:jget case "throws")))
      (if throws
          (check-equal throws (handler-case (progn (pig:build-session-context (aref args 0) :leaf-id leaf :by-id by-id) "no error")
                                (pig:js-type-error (e) (pig:js-type-error-message e)))
                       name)
          (check-json (pig:jget case "result") (pig:build-session-context (aref args 0) :leaf-id leaf :by-id by-id) name)))))

(deftest branch-summary-helpers
  (let* ((entries (json-of "[{\"type\":\"message\",\"id\":\"a\",\"parentId\":null,\"message\":{\"role\":\"user\",\"content\":\"hello\"}},
                             {\"type\":\"message\",\"id\":\"b\",\"parentId\":\"a\",\"message\":{\"role\":\"assistant\",\"content\":[
                                {\"type\":\"toolCall\",\"name\":\"read\",\"arguments\":{\"path\":\"/x\"}},
                                {\"type\":\"toolCall\",\"name\":\"edit\",\"arguments\":{\"path\":\"/y\"}},
                                {\"type\":\"toolCall\",\"name\":\"read\",\"arguments\":{\"path\":\"/x\"}},
                                {\"type\":\"toolCall\",\"name\":\"write\",\"arguments\":{\"path\":\"/z\"}}]}},
                             {\"type\":\"message\",\"id\":\"c\",\"parentId\":\"b\",\"message\":{\"role\":\"toolResult\",\"content\":\"r\"}},
                             {\"type\":\"custom_message\",\"id\":\"d\",\"parentId\":\"c\",\"customType\":\"n\",\"content\":\"c\",\"display\":true,\"timestamp\":\"2026-01-02T03:04:05.678Z\"}]"))
         (by-id (make-hash-table :test 'equal)))
    (loop for e across entries do (setf (gethash (pig:jget e "id") by-id) e))
    (let ((session (cons (lambda (id) (gethash id by-id))
                         (lambda (from)
                           (loop with path = '() for e = (gethash from by-id) then (gethash (pig:jget e "parentId") by-id)
                                 while e do (push e path) finally (return (coerce path 'vector)))))))
      (let ((r (pig:collect-entries-for-branch-summary session "d" "b")))
        (check-equal "b" (pig:collect-entries-result-common-ancestor-id r))
        (check-equal '("c" "d") (map 'list (lambda (e) (pig:jget e "id")) (pig:collect-entries-result-entries r))))
      (check-equal 0 (length (pig:collect-entries-result-entries (pig:collect-entries-for-branch-summary session nil "b")))))
    (let ((r (pig:prepare-branch-entries entries)))
      (check-equal '("user" "assistant" "custom") (map 'list (lambda (m) (pig:jget m "role")) (pig:branch-preparation-messages r)))
      (check-equal #("/x") (pig:file-operations-read (pig:branch-preparation-file-ops r)))
      (check-equal #("/z") (pig:file-operations-written (pig:branch-preparation-file-ops r)))
      (check-equal #("/y") (pig:file-operations-edited (pig:branch-preparation-file-ops r)))
      (check-equal 1767323045678 (pig:jget (aref (pig:branch-preparation-messages r) 2) "timestamp"))
      (check-equal (reduce #'+ (map 'list #'pig:estimate-tokens (pig:branch-preparation-messages r))) (pig:branch-preparation-total-tokens r)))
    ;; a budget of one token fits the newest message and stops at the assistant message
    (let ((r (pig:prepare-branch-entries entries 1)))
      (check-equal '("custom") (map 'list (lambda (m) (pig:jget m "role")) (pig:branch-preparation-messages r))))))

;;; ---------------------------------------------------------------- small helpers

(deftest event-guards-and-message-helpers
  (let ((event (json-of "{\"toolName\":\"bash\"}")))
    (check (pig:tool-call-event-type-p "bash" event))
    (check (not (pig:tool-call-event-type-p "read" event)))
    (check (not (pig:tool-call-event-type-p "bash" (json-of "{\"toolName\":5}"))))
    (check (pig:bash-tool-result-p event))
    (check (not (pig:read-tool-result-p event)))
    (loop for (fn name) in (list (list #'pig:powershell-tool-result-p "powershell") (list #'pig:read-tool-result-p "read") (list #'pig:edit-tool-result-p "edit")
                                 (list #'pig:write-tool-result-p "write") (list #'pig:grep-tool-result-p "grep") (list #'pig:find-tool-result-p "find")
                                 (list #'pig:ls-tool-result-p "ls"))
          do (check (funcall fn (pig:obj "toolName" name)) name)
             (check (not (funcall fn (pig:obj "toolName" "other"))) name)
             (check (not (funcall fn (pig:obj))) name)))
  (let ((data (json-of "{\"message\":{\"role\":\"assistant\",\"content\":[{\"type\":\"text\",\"text\":\"a\"},{\"type\":\"image\"},{\"type\":\"text\",\"text\":\"b\"}]}}")))
    (check-equal "assistant" (pig:message-role data))
    (check-equal "ab" (pig:message-text data)))
  (check-equal "hi" (pig:message-text (json-of "{\"message\":{\"role\":\"user\",\"content\":\"hi\"}}")))
  (check-equal "" (pig:message-role (json-of "{\"message\":{\"role\":5}}")))
  (check-equal "" (pig:message-text (json-of "{}"))))

(deftest language-from-path
  (check-equal '("typescript" "python" "dockerfile" nil "hcl" "typescript" nil nil)
               (mapcar #'pig:get-language-from-path '("file.ts" "dir/FILE.PY" "Dockerfile" "dir.d/Makefile" "x.tf" "a.b/c.ts" "x." "x.ts "))))

(deftest session-and-skill-helpers
  (check-equal 3 pig:+current-session-version+)
  (check-equal "pi.virtual-model-state" pig:*virtual-model-state-entry*)
  (let ((entries (json-of "[{\"type\":\"compaction\",\"id\":1},{\"type\":\"message\"},{\"type\":\"compaction\",\"id\":2},{\"type\":\"label\"}]")))
    (check-equal 2 (pig:jget (pig:get-latest-compaction-entry entries) "id"))
    (check-equal nil (pig:get-latest-compaction-entry (vector))))
  (let ((block (pig:parse-skill-block (format nil "<skill name=\"n\" location=\"/l\">~%body~%</skill>~%~%  user text  "))))
    (check-equal '("n" "/l" "body" "user text")
                 (list (pig:parsed-skill-block-name block) (pig:parsed-skill-block-location block) (pig:parsed-skill-block-content block)
                       (pig:parsed-skill-block-user-message block))))
  (check-equal nil (pig:parsed-skill-block-user-message (pig:parse-skill-block (format nil "<skill name=\"n\" location=\"/l\">~%body~%</skill>"))))
  ;; the first closing tag that leaves a blank line and more text ends the block
  (check-equal (format nil "a~%</skill>~%x") (pig:parsed-skill-block-content (pig:parse-skill-block (format nil "<skill name=\"n\" location=\"/l\">~%a~%</skill>~%x~%</skill>~%~%u"))))
  (check-equal nil (pig:parse-skill-block "<skill name=\"\" location=\"/l\">"))
  (check-equal nil (pig:parse-skill-block "plain"))
  (let ((skills (json-of "[{\"name\":\"a&b\",\"description\":\"<d>\",\"filePath\":\"/p\"},{\"name\":\"hidden\",\"disableModelInvocation\":true}]")))
    (check-equal (format nil "~%~%The following skills provide specialized instructions for specific tasks.~%Use the read tool to load a skill's file when the task matches its description.~%When a skill file references a relative path, resolve it against the skill directory (parent of SKILL.md / dirname of the path) and use that absolute path in tool commands.~%~%<available_skills>~%  <skill>~%    <name>a&amp;b</name>~%    <description>&lt;d&gt;</description>~%    <location>/p</location>~%  </skill>~%</available_skills>")
                 (pig:format-skills-for-prompt skills))
    (check (search "Use bash to load a skill's file when the task matches its description." (pig:format-skills-for-prompt skills "bash")))
    (check (search "Load a skill's file when the task matches" (pig:format-skills-for-prompt skills "other")))
    (check-equal "" (pig:format-skills-for-prompt (json-of "[{\"disableModelInvocation\":1}]")))))

(deftest format-dimension-note-matches-pi
  (flet ((note (ow oh w h resized) (pig:format-dimension-note (pig:make-resized-image :original-width ow :original-height oh :width w :height h :was-resized resized))))
    (check-equal nil (note 100 100 100 100 nil))
    (check-equal "[Image: original 4000x3000, displayed at 2000x1500. Multiply coordinates by 2.00 to map to original image.]" (note 4000 3000 2000 1500 t))
    ;; toFixed rounds the exact tie 0.125 up where round-half-even does not
    (check-equal "[Image: original 1x1, displayed at 8x1. Multiply coordinates by 0.13 to map to original image.]" (note 1 1 8 1 t))
    (check-equal "[Image: original 1x1, displayed at 0x1. Multiply coordinates by Infinity to map to original image.]" (note 1 1 0 1 t))
    (check-equal "[Image: original 0x1, displayed at 0x1. Multiply coordinates by NaN to map to original image.]" (note 0 1 0 1 t))))

(deftest stored-credential-matches-pi
  (let ((dir (format nil "/tmp/pig-coding-~D-~D/" (sb-posix:getpid) (random 1000000))))
    (ensure-directories-exist dir)
    (dolist (case (coerce (ct-cases "readStoredCredential") 'list))
      (let ((path (format nil "~Aauth-~A.json" dir (random 1000000))) (content (ct-arg case 1)))
        (when content
          (with-open-file (out path :direction :output :external-format :utf-8 :if-exists :supersede) (write-string content out)))
        (let ((got (pig:read-stored-credential (ct-arg case 0) path)))
          (check-json (pig:jget (pig:jget case "result") "credential") (or got :null) (pig:jget case "name")))
        (ignore-errors (delete-file path))))
    (ignore-errors (sb-ext:delete-directory dir :recursive t))))

;;; ---------------------------------------------------------------- helpers the host answers

(deftest parse-date-answers-the-iso-form-without-the-host
  (with-answering-host ((lambda (method args) (declare (ignore method args)) (pig:obj "ms" 1)))
    (check-equal 1577934245678 (pig:parse-date "2020-01-02T03:04:05.678Z"))
    (check-equal 0 (pig:parse-date "1970-01-01T00:00:00.000Z"))
    (check-equal 951782400000 (pig:parse-date "2000-02-29T00:00:00.000Z"))
    (check-equal nil *ct-calls*)
    ;; a field out of range or another form is the host's to read
    (check-equal 1 (pig:parse-date "2026-02-30T00:00:00.000Z"))
    (check-equal 1 (pig:parse-date "2026-01-02"))
    (check-equal 2 (length *ct-calls*))))

(deftest frontmatter-helpers-ask-the-host
  (with-answering-host ((lambda (method args)
                          (declare (ignore args))
                          (check-equal "frontmatter.parse" method)
                          (pig:obj "frontmatter" (pig:obj "z" 1) "body" "text")))
    (let ((parsed (pig:parse-frontmatter "---\nz: 1\n---\ntext")))
      (check-json (pig:obj "z" 1) (pig:parsed-frontmatter-frontmatter parsed))
      (check-equal "text" (pig:parsed-frontmatter-body parsed)))
    (check-equal "text" (pig:strip-frontmatter "x"))
    (check-equal "---\nz: 1\n---\ntext" (pig:jget (first (ct-calls "frontmatter.parse")) "content"))))

(deftest diff-helpers-pass-their-arguments-and-decode-the-answer
  (let ((patches (ct-cases "generateUnifiedPatch")) (diffs (ct-cases "generateDiffString")))
    (with-answering-host ((lambda (method args)
                            (cond ((string= method "diff.patch") (pig:obj "patch" (format nil "patch:~A:~A:~A:~A" (pig:jget args "path") (pig:jget args "oldContent") (pig:jget args "newContent") (pig:jget args "contextLines"))))
                                  (t (pig:obj "diff" (format nil "diff:~A:~A:~A" (pig:jget args "oldContent") (pig:jget args "newContent") (pig:jget args "contextLines"))
                                              "firstChangedLine" 7)))))
      (loop for case across patches
            do (let ((args (pig:jget case "args")))
                 (check-equal (format nil "patch:~A:~A:~A:~A" (aref args 0) (aref args 1) (aref args 2) (if (> (length args) 3) (aref args 3) nil))
                              (pig:generate-unified-patch (aref args 0) (aref args 1) (aref args 2) (and (> (length args) 3) (aref args 3))) (pig:jget case "name"))))
      (loop for case across diffs
            do (let* ((args (pig:jget case "args"))
                      (got (pig:generate-diff-string (aref args 0) (aref args 1) (and (> (length args) 2) (aref args 2)))))
                 (check-equal (format nil "diff:~A:~A:~A" (aref args 0) (aref args 1) (if (> (length args) 2) (aref args 2) nil)) (pig:diff-string-diff got) (pig:jget case "name"))
                 (check-equal 7 (pig:diff-string-first-changed-line got))))
      ;; contextLines is omitted when not given
      (pig:generate-diff-string "a" "b")
      (check (not (nth-value 1 (gethash "contextLines" (car (last (ct-calls "diff.string"))))))))
    (with-answering-host ((lambda (method args) (declare (ignore method args)) (pig:obj "diff" "" )))
      (check-equal nil (pig:diff-string-first-changed-line (pig:generate-diff-string "a" "a"))))))

(deftest clipboard-and-images-reach-the-host
  (with-answering-host ((lambda (method args)
                          (cond ((string= method "clipboard.copy") (pig:obj))
                                ((string= method "image.convertToPng")
                                 (if (string= (pig:jget args "data") "bad") (pig:obj) (pig:obj "image" (pig:obj "data" "PNG" "mimeType" "image/png"))))
                                (t (pig:obj "image" (pig:obj "originalWidth" 4000 "originalHeight" 3000 "width" 2000 "height" 1500 "wasResized" t "data" "RES" "mimeType" "image/jpeg"))))))
    (check-equal nil (pig:copy-to-clipboard "copied text"))
    (check-equal "copied text" (pig:jget (first (ct-calls "clipboard.copy")) "text"))
    (let ((png (pig:convert-to-png "AAAA" "image/jpeg")))
      (check-equal '("PNG" "image/png") (list (pig:converted-image-data png) (pig:converted-image-mime-type png))))
    (check-equal nil (pig:convert-to-png "bad" "image/jpeg"))
    (let* ((options (pig:make-image-resize-options :max-width 100 :jpeg-quality 80))
           (resized (pig:resize-image (coerce '(0 1 2 250 251 252 253) '(simple-array (unsigned-byte 8) (*))) "image/png" options))
           (args (first (ct-calls "image.resize"))))
      (check-equal "AAEC+vv8/Q==" (pig:jget args "data"))
      (check-equal "image/png" (pig:jget args "mimeType"))
      (check-json (pig:obj "maxWidth" 100 "jpegQuality" 80) (pig:jget args "options"))
      (check-equal '(4000 3000 2000 1500 t "RES" "image/jpeg")
                   (list (pig:resized-image-original-width resized) (pig:resized-image-original-height resized) (pig:resized-image-width resized)
                         (pig:resized-image-height resized) (pig:resized-image-was-resized resized) (pig:resized-image-data resized)
                         (pig:resized-image-mime-type resized))))
    (pig:resize-image (coerce '(1) '(simple-array (unsigned-byte 8) (*))) "image/png")
    (check (not (nth-value 1 (gethash "options" (car (last (ct-calls "image.resize")))))))
    ;; the converted and resized vectors' inputs reach the host as given
    (loop for case across (ct-cases "convertToPng")
          do (pig:convert-to-png (aref (pig:jget case "args") 0) (aref (pig:jget case "args") 1))
             (check-equal (aref (pig:jget case "args") 0) (pig:jget (car (last (ct-calls "image.convertToPng"))) "data") (pig:jget case "name")))))

(deftest file-mutation-queue-takes-and-returns-its-turn
  (let ((order '()))
    (with-answering-host ((lambda (method args)
                            (push method order)
                            (if (string= method "fileMutation.enter") (pig:obj "ticket" "t1") (progn (check-equal "t1" (pig:jget args "ticket")) (pig:obj)))))
      (check-equal :ran (pig:with-file-mutation-queue "/a/b/../c//d.txt" (lambda () (push :body order) :ran)))
      (check-equal "/a/c/d.txt" (pig:jget (first (ct-calls "fileMutation.enter")) "path"))
      (check-equal '("fileMutation.enter" :body "fileMutation.exit") (reverse order))
      ;; the turn is given back when the body fails
      (check-error simple-error (pig:with-file-mutation-queue "/x" (lambda () (error "boom"))))
      (check-equal 2 (length (ct-calls "fileMutation.exit"))))
    (with-answering-host ((lambda (method args) (declare (ignore method args)) (pig:obj)))
      (let ((ran nil))
        (check-error error (pig:with-file-mutation-queue "/x" (lambda () (setf ran t))))
        (check (not ran))))))
