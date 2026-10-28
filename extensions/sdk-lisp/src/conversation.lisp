;;;; SPDX-License-Identifier: MIT
;;;; Lane C: conversation helpers: convertToLlm, serializeConversation, branch summarization, session entry messages and buildSessionContext.
;;;; Messages and entries are decoded JSON (hash-tables, vectors); a malformed one signals js-type-error with Pi's message.

(in-package #:pig)

(defun cx-without (object key)
  (let ((copy (make-hash-table :test 'equal)))
    (maphash (lambda (k v) (unless (string= k key) (setf (gethash k copy) v))) object)
    copy))

(defun cx-copy (object)
  (let ((copy (make-hash-table :test 'equal)))
    (maphash (lambda (k v) (setf (gethash k copy) v)) object)
    copy))

(defun cx-obj-from (source names &optional (members '()))
  "An object with MEMBERS first (alternating keys and values), then each of NAMES that SOURCE has, in order."
  (let ((result (apply #'obj members)))
    (dolist (name names result)
      (when (cx-has source name) (setf (gethash name result) (gethash name source))))))

;;; ---------------------------------------------------------------- convertToLlm

(defun cx-js-value-string (value)
  "JavaScript's ToString of a value decoded from JSON."
  (cond ((or (null value) (eq value :null)) "null")
        ((eq value t) "true")
        ((eq value :false) "false")
        ((stringp value) value)
        ((realp value) (cx-js-number-string value))
        ((cx-array-p value)
         (format nil "~{~A~^,~}" (map 'list (lambda (e) (if (or (null e) (eq e :null)) "" (cx-js-value-string e))) value)))
        (t "[object Object]")))

(defun cx-js-member-string (object key)
  "The string a template literal makes of OBJECT[KEY]: \"undefined\" for an absent member."
  (if (cx-has object key) (cx-js-value-string (gethash key object)) "undefined"))

(defun cx-user-text-message (source text)
  (let ((converted (obj "role" "user" "content" (vector (obj "type" "text" "text" text)))))
    (when (cx-has source "timestamp") (setf (gethash "timestamp" converted) (gethash "timestamp" source)))
    converted))

(defun cx-bash-execution-text (message)
  (let ((exit-code (gethash "exitCode" message)))
    (concatenate 'string
                 "Ran `" (cx-js-member-string message "command") "`" (string #\Newline)
                 (if (cx-truthy (cx-get message "output"))
                     (format nil "```~%~A~%```" (cx-js-member-string message "output"))
                     "(no output)")
                 (cond ((cx-truthy (cx-get message "cancelled")) (format nil "~%~%(command cancelled)"))
                       ((and (cx-has message "exitCode") (not (eq exit-code :null))
                             (not (and (realp exit-code) (not (cx-nan-p exit-code)) (zerop exit-code))))
                        (format nil "~%~%Command exited with code ~A" (cx-js-member-string message "exitCode")))
                       (t ""))
                 (if (and (cx-truthy (cx-get message "truncated")) (cx-truthy (cx-get message "fullOutputPath")))
                     (format nil "~%~%[Output truncated. Full output: ~A]" (cx-js-member-string message "fullOutputPath"))
                     ""))))

(defun cx-convert-message (message)
  (when (hash-table-p message)
    (let ((role (cx-get message "role")))
      (cond
        ((equal role "bashExecution")
         (unless (cx-truthy (cx-get message "excludeFromContext"))
           (cx-user-text-message message (cx-bash-execution-text message))))
        ((equal role "custom")
         (let ((converted (obj "role" "user")) (content (gethash "content" message :absent)))
           (cond ((stringp content) (setf (gethash "content" converted) (vector (obj "type" "text" "text" content))))
                 ((not (eq content :absent)) (setf (gethash "content" converted) content)))
           (when (cx-has message "timestamp") (setf (gethash "timestamp" converted) (gethash "timestamp" message)))
           converted))
        ((equal role "branchSummary")
         (cx-user-text-message message (format nil "The following is a summary of a branch that this conversation came back from:~%~%<summary>~%~A</summary>"
                                               (cx-js-member-string message "summary"))))
        ((equal role "compactionSummary")
         (cx-user-text-message message (format nil "The conversation history before this point was compacted into the following summary:~%~%<summary>~%~A~%</summary>"
                                               (cx-js-member-string message "summary"))))
        ((cx-string-in role '("system" "user" "assistant" "toolResult")) message)))))

(defun convert-to-llm (messages)
  "Pi's convertToLlm: bashExecution, custom, branchSummary and compactionSummary messages become user messages, system, user, assistant and toolResult messages are returned as they are, and a bashExecution message with excludeFromContext set, or a message with any other role, is dropped. A message that is not an object has no role and is dropped. Returns a vector."
  (coerce (loop for message across (cx-seq messages)
                for converted = (cx-convert-message message)
                when converted collect converted)
          'simple-vector))

;;; ---------------------------------------------------------------- serializeConversation

(defun cx-array-index-key-p (key)
  (or (string= key "0")
      (and (<= 1 (length key) 10) (every (lambda (c) (char<= #\0 c #\9)) key) (char/= (char key 0) #\0) (<= (parse-integer key) 4294967294))))

(defun cx-ordered-keys (object)
  "The order Object.keys and JSON.stringify give an object's own string keys: the array-index keys ascending, then the others in insertion order."
  (let ((keys (loop for k being the hash-keys of object collect k)))
    (append (sort (remove-if-not #'cx-array-index-key-p keys)
                  (lambda (a b) (or (< (length a) (length b)) (and (= (length a) (length b)) (string< a b)))))
            (remove-if #'cx-array-index-key-p keys))))

(defun cx-json-stringify (value)
  "JSON.stringify for a decoded JSON value."
  (cond ((hash-table-p value)
         (format nil "{~{~A~^,~}}" (mapcar (lambda (key) (concatenate 'string (to-json key) ":" (cx-json-stringify (gethash key value)))) (cx-ordered-keys value))))
        ((cx-array-p value) (format nil "[~{~A~^,~}]" (map 'list #'cx-json-stringify value)))
        (t (to-json value))))

(defun cx-unit-json (unit)
  (if (<= #xD800 unit #xDFFF) (format nil "\"\\u~(~4,'0X~)\"" unit) (to-json (string (code-char unit)))))

(defun cx-entries-string (arguments)
  "Object.entries(args).map(([k, v]) => `${k}=${JSON.stringify(v)}`).join(\", \"): null and undefined throw, a string or array is read by index, a number or boolean has no entries."
  (when (null arguments) (cx-type-error "Cannot convert undefined or null to object"))
  (format nil "~{~A~^, ~}"
          (cond ((hash-table-p arguments)
                 (mapcar (lambda (key) (concatenate 'string key "=" (cx-json-stringify (gethash key arguments)))) (cx-ordered-keys arguments)))
                ((cx-array-p arguments) (loop for element across arguments for i from 0 collect (format nil "~D=~A" i (cx-json-stringify element))))
                ((stringp arguments) (loop for unit across (cx-utf16-units arguments) for i from 0 collect (format nil "~D=~A" i (cx-unit-json unit))))
                (t '()))))

(defun cx-content-text (content separator)
  "pi-ai's contentText: a string is its own text, an array is the text of its text blocks joined with SEPARATOR."
  (cond ((stringp content) content)
        ((cx-array-p content)
         (let ((texts (loop for block across content
                            do (when (or (null block) (eq block :null)) (cx-type-error "Cannot read properties of null (reading 'type')"))
                            when (and (hash-table-p block) (equal (gethash "type" block) "text"))
                              collect (let ((text (cx-get block "text"))) (if text (cx-js-value-string text) "")))))
           (with-output-to-string (out)
             (loop for (text . more) on texts do (write-string text out) (when more (write-string separator out))))))
        (t (cx-type-error "content.filter is not a function"))))

(defun cx-truncate-for-summary (text max-chars)
  (let* ((units (cx-utf16-units text)) (size (length units)))
    (if (<= size max-chars)
        text
        (format nil "~A~%~%[... ~D more characters truncated]" (cx-units-string units 0 max-chars) (- size max-chars)))))

(defun cx-serialize-assistant (message parts)
  "Add the parts of the assistant MESSAGE to PARTS (a cons cell holding the reversed list)."
  (let ((blocks (cx-get message "content")) (calls '()) (has-text nil))
    (unless (cx-array-p blocks) (cx-type-error "msg.content is not iterable"))
    (loop for block across blocks
          do (when (or (null block) (eq block :null)) (cx-type-error "Cannot read properties of null (reading 'type')"))
             (let ((kind (cx-get block "type")))
               (cond ((equal kind "toolCall")
                      (push (concatenate 'string (cx-js-member-string block "name") "(" (cx-entries-string (cx-get block "arguments")) ")") calls))
                     ((equal kind "text") (setf has-text t)))))
    (when has-text (push (concatenate 'string "[Assistant]: " (cx-content-text blocks (string #\Newline))) (car parts)))
    (when calls (push (format nil "[Assistant tool calls]: ~{~A~^; ~}" (reverse calls)) (car parts)))))

(defun serialize-conversation (messages)
  "Pi's serializeConversation: the text a summarization request carries for MESSAGES, LLM messages as JSON objects (the result of convert-to-llm). A tool call prints its arguments in property order. A message with another role than user, assistant or toolResult adds nothing, and thinking blocks are omitted. Pi throws a TypeError when a message it reads has content that is not a string or an array; this signals js-type-error."
  (let ((parts (list '())))
    (loop for message across (cx-seq messages)
          do (when (or (null message) (eq message :null)) (cx-type-error "Cannot read properties of null (reading 'role')"))
             (let ((role (cx-get message "role")))
               (cond ((equal role "user")
                      (let ((content (cx-content-text (gethash "content" message :absent) "")))
                        (when (plusp (length content)) (push (concatenate 'string "[User]: " content) (car parts)))))
                     ((equal role "assistant") (cx-serialize-assistant message parts))
                     ((equal role "toolResult")
                      (let ((content (cx-content-text (gethash "content" message :absent) "")))
                        (when (plusp (length content))
                          (push (concatenate 'string "[Tool result]: " (cx-truncate-for-summary content 2000)) (car parts))))))))
    (format nil "~{~A~^~%~%~}" (reverse (car parts)))))

;;; ---------------------------------------------------------------- session entries as context messages

(defun cx-timestamp (entry)
  "new Date(entry.timestamp).getTime() as JSON.stringify writes it: Date.parse of a string (the host reads it), a number's own time value clipped, null (NaN) for anything else."
  (let ((value (cx-get entry "timestamp")))
    (cond ((stringp value) (or (parse-date value) :null))
          ((realp value) (if (or (cx-nan-p value) (cx-infinity-p value) (> (abs value) 8.64d15)) :null (values (truncate value))))
          (t :null))))

(defun cx-entry-messages (entry)
  "The messages ENTRY contributes to the context, as a list."
  (let ((kind (cx-get entry "type")))
    (cond
      ((equal kind "message")
       (let ((message (cx-get entry "message")))
         (when (null message) (cx-type-error "Cannot read properties of undefined (reading 'role')"))
         (cond ((not (hash-table-p message)) (list message))
               ((cx-get message "content") (list message))
               (t (let ((role (cx-get message "role")))
                    (cond ((equal role "system") (list (cx-with message "content" "")))
                          ((cx-string-in role '("user" "assistant" "toolResult")) (list (cx-with message "content" (vector))))
                          (t (list message))))))))
      ((equal kind "custom_message")
       (let ((message (obj "role" "custom")))
         (when (cx-has entry "customType") (setf (gethash "customType" message) (gethash "customType" entry)))
         (setf (gethash "content" message) (or (cx-get entry "content") (vector)))
         (dolist (key '("display" "details")) (when (cx-has entry key) (setf (gethash key message) (gethash key entry))))
         (setf (gethash "timestamp" message) (cx-timestamp entry))
         (list message)))
      ((equal kind "branch_summary")
       (let ((summary (cx-get entry "summary")))
         (when (and (stringp summary) (plusp (length summary)))
           (let ((message (obj "role" "branchSummary" "summary" summary)))
             (when (cx-has entry "fromId") (setf (gethash "fromId" message) (gethash "fromId" entry)))
             (setf (gethash "timestamp" message) (cx-timestamp entry))
             (list message)))))
      ((equal kind "compaction")
       (let ((summary (obj "role" "compactionSummary" "summary" (let ((s (gethash "summary" entry))) (if s s :null))))
             (system (cx-get entry "systemMessage")))
         (when (cx-has entry "tokensBefore") (setf (gethash "tokensBefore" summary) (gethash "tokensBefore" entry)))
         (setf (gethash "timestamp" summary) (cx-timestamp entry))
         (if (or (cx-truthy system) (hash-table-p system) (cx-array-p system)) (list system summary) (list summary))))
      (t '()))))

(defun session-entry-to-context-messages (entry)
  "Pi's sessionEntryToContextMessages: the LLM and runtime messages a session entry contributes to the context, as a vector. A message entry is its message, where a null or missing content reads as the empty one Pi supplies (an empty string for a system message, an empty array for a user, assistant or toolResult one). A custom_message entry is a custom message, a branch_summary entry with a summary a branchSummary message, and a compaction entry a compactionSummary message after the system message it carries, if any. Every other entry is no message. The entry's timestamp becomes the epoch milliseconds of Date.parse (the host reads what is not ISO), null where Date.parse cannot read it. A message entry with a null or missing message signals js-type-error, as Pi throws."
  (coerce (cx-entry-messages entry) 'simple-vector))

;;; ---------------------------------------------------------------- buildSessionContext

(defconstant +session-leaf-last+ :last
  "An omitted leaf id of build-session-context: the context ends at the last entry. NIL or :NULL is Pi's null: no entry is on the path, so the context is empty.")

(defun cx-key (value)
  "A Map key for an entry id as JavaScript compares it: strings, numbers and booleans, told apart; anything else is no key."
  (cond ((or (stringp value) (eq value t) (eq value :false)) value)
        ((realp value) (list :n (coerce value 'double-float)))))

(defun cx-same (a a-key b b-key)
  "A[A-KEY] === B[B-KEY] for decoded entry members: undefined (a missing member) equals only undefined."
  (cond ((not (eq (and (cx-has a a-key) t) (and (cx-has b b-key) t))) nil)
        ((not (cx-has a a-key)) t)
        (t (let ((left (gethash a-key a)) (right (gethash b-key b)))
             (or (and (eq left :null) (eq right :null))
                 (let ((lk (cx-key left)) (rk (cx-key right))) (and lk (equal lk rk))))))))

(defun cx-entry-path (entries leaf-id by-id)
  (when (or (null leaf-id) (eq leaf-id :null)) (return-from cx-entry-path '()))
  (let* ((index (make-hash-table :test 'equal))
         (limit (+ (length entries) (if by-id (hash-table-count by-id) 0))))
    (if by-id
        (maphash (lambda (k v) (let ((key (cx-key k))) (when key (setf (gethash key index) v)))) by-id)
        (loop for entry across entries
              do (let ((key (cx-key (cx-get entry "id")))) (when key (setf (gethash key index) entry)))))
    (flet ((lookup (value) (let ((key (cx-key value))) (and key (values (gethash key index))))))
      (let ((current (and (not (eq leaf-id :last)) (cx-truthy leaf-id) (lookup leaf-id))) (path '()))
        (unless current
          (when (zerop (length entries)) (return-from cx-entry-path '()))
          (setf current (svref entries (1- (length entries)))))
        (loop while (and current (<= (length path) limit))
              do (push current path)
                 (let ((parent (cx-get current "parentId")))
                   (setf current (and (cx-truthy parent) (lookup parent)))))
        path))))

(defun cx-role (entry)
  "entry.message.role: Pi's TypeError for a null or missing message, no role for one that is not an object."
  (unless (cx-has entry "message") (cx-type-error "Cannot read properties of undefined (reading 'role')"))
  (let ((message (gethash "message" entry)))
    (when (eq message :null) (cx-type-error "Cannot read properties of null (reading 'role')"))
    (let ((role (and (hash-table-p message) (gethash "role" message)))) (and (stringp role) role))))

(defun cx-context-entries (path)
  (let ((compaction (find-if (lambda (entry) (equal (cx-get entry "type") "compaction")) path :from-end t)))
    (unless compaction (return-from cx-context-entries path))
    (let ((compaction-index (position-if (lambda (entry) (cx-same entry "id" compaction "id")) path)))
      (unless compaction-index (return-from cx-context-entries path))
      (let ((context (list compaction)) (found nil))
        (loop for entry in (subseq path 0 compaction-index)
              do (when (cx-same entry "id" compaction "firstKeptEntryId") (setf found t))
                 (when (and found
                            (not (and (equal (cx-get entry "type") "message")
                                      (hash-table-p (cx-get entry "message"))
                                      (equal (cx-get (cx-get entry "message") "role") "system"))))
                   (push entry context)))
        (append (nreverse context) (nthcdr (1+ compaction-index) path))))))

(defun cx-apply-edit (messages edit)
  (let ((present (cx-has edit "replacement")) (replacement (cx-get edit "replacement")))
    (when (and present (null replacement)) (return-from cx-apply-edit '()))
    (loop for message in messages
          collect (let ((role (and (hash-table-p message) (cx-get message "role"))))
                    (cond ((not (cx-string-in role '("user" "assistant" "toolResult" "custom"))) message)
                          ((not present) (cx-type-error "Cannot read properties of undefined (reading 'content')"))
                          (t (let* ((has-content (and (hash-table-p replacement) (cx-has replacement "content")))
                                    (content (and has-content (gethash "content" replacement))))
                               (cond ((and (stringp content) (cx-string-in role '("assistant" "toolResult")))
                                      (cx-with message "content" (vector (obj "type" "text" "text" content))))
                                     (has-content (cx-with message "content" content))
                                     (t (cx-without message "content"))))))))))

(defun build-session-context (entries &key (leaf-id +session-leaf-last+) by-id)
  "Pi's buildSessionContext: the model context of a session as the entries on the path from the leaf to the root, with compaction, branch summaries and context edits applied. Returns an object with messages (a vector, in order), thinkingLevel (the last thinking_level_change on the path, \"off\" without one) and model (the last model_change or assistant message's {provider, modelId}, null without one). LEAF-ID is Pi's leafId: omitted, the last entry; NIL or :NULL, no path (an empty context); an id, the entry to walk back from (an id no entry has, or the empty string, falls back to the last entry). BY-ID is an EQUAL hash-table of the entries by id that the walk reads instead of an index of ENTRIES. A message entry with a null or missing message signals js-type-error, as Pi throws."
  (let* ((entries (cx-seq entries))
         (path (cx-entry-path entries leaf-id by-id))
         (thinking "off") (thinking-defined t) (model nil))
    (dolist (entry path)
      (let ((kind (cx-get entry "type")))
        (cond ((equal kind "thinking_level_change")
               (setf thinking-defined (cx-has entry "thinkingLevel") thinking (gethash "thinkingLevel" entry :null)))
              ((equal kind "model_change") (setf model (cx-obj-from entry '("provider" "modelId"))))
              ((and (equal kind "message") (equal (cx-role entry) "assistant"))
               (let ((message (gethash "message" entry)) (result (obj)))
                 (when (cx-has message "provider") (setf (gethash "provider" result) (gethash "provider" message)))
                 (when (cx-has message "model") (setf (gethash "modelId" result) (gethash "model" message)))
                 (setf model result))))))
    (let* ((context-entries (cx-context-entries path))
           (edits (make-hash-table :test 'equal))
           (messages '()))
      (dolist (entry context-entries)
        (let ((key (cx-key (cx-get entry "targetId"))))
          (when (and (equal (cx-get entry "type") "context_edit") key) (setf (gethash key edits) entry))))
      (loop for entry in context-entries for index from 0
            do (unless (and (equal (cx-get entry "type") "compaction") (> index 0))
                 (let* ((projected (cx-entry-messages entry)) (key (cx-key (cx-get entry "id"))))
                   (when (and key (gethash key edits)) (setf projected (cx-apply-edit projected (gethash key edits))))
                   (dolist (message projected) (push message messages)))))
      (let ((result (obj "messages" (coerce (nreverse messages) 'simple-vector) "model" (or model :null))))
        (when thinking-defined (setf (gethash "thinkingLevel" result) thinking))
        result))))

;;; ---------------------------------------------------------------- branch summarization

(defstruct (file-operations (:constructor make-file-operations (read written edited)))
  "Pi's FileOperations: the files tool calls read, wrote and edited, each a Set kept as a vector in insertion order. An element is not always a string: a branch summary's recorded details can hold any JSON value."
  read written edited)

(defstruct (branch-preparation (:constructor make-branch-preparation (messages file-ops total-tokens)))
  "Pi's BranchPreparation: the messages to summarize (oldest first), their file operations and their estimated size."
  messages file-ops total-tokens)

(defstruct (collect-entries-result (:constructor make-collect-entries-result (entries common-ancestor-id)))
  "Pi's CollectEntriesResult: the entries to summarize (oldest first) and the deepest entry on both the old and the new path, or NIL."
  entries common-ancestor-id)

(defun cx-session-entry (session id) (if (consp session) (funcall (car session) id) (session-get-entry session id)))
(defun cx-session-branch (session id) (if (consp session) (funcall (cdr session) id) (session-get-branch session id)))

(defun collect-entries-for-branch-summary (session old-leaf-id target-id)
  "Pi's collectEntriesForBranchSummary: the entries from OLD-LEAF-ID back to its common ancestor with TARGET-ID, oldest first. A compaction on the way is included, not a boundary. SESSION is a session manager, or a cons of a get-entry function (id) and a get-branch function (from-id). With no old position there is nothing to summarize."
  (unless (cx-truthy old-leaf-id) (return-from collect-entries-for-branch-summary (make-collect-entries-result (vector) nil)))
  (let ((old-path (make-hash-table :test 'equal)) (common nil) (entries '()))
    (loop for entry across (cx-seq (cx-session-branch session old-leaf-id)) do (setf (gethash (cx-get entry "id") old-path) t))
    (loop for entry across (reverse (cx-seq (cx-session-branch session target-id)))
          do (when (gethash (cx-get entry "id") old-path) (setf common (cx-get entry "id")) (return)))
    (let ((current old-leaf-id))
      (loop while (and (cx-truthy current) (not (equal current common)))
            do (let ((entry (cx-session-entry session current)))
                 (unless (cx-truthy entry) (return))
                 (push entry entries)
                 (setf current (cx-get entry "parentId")))))
    (make-collect-entries-result (coerce entries 'simple-vector) common)))

(defun cx-date-time (entry)
  "new Date(entry.timestamp).getTime(): a string is parsed by the host, a number is clipped to the time range, null is the epoch, anything else (including an absent timestamp) is NaN."
  (if (not (cx-has entry "timestamp"))
      *cx-nan*
      (let ((timestamp (gethash "timestamp" entry)))
        (cond ((eq timestamp t) 1)
              ((eq timestamp :false) 0)
              ((eq timestamp :null) 0)
              ((stringp timestamp) (or (parse-date timestamp) *cx-nan*))
              ((realp timestamp) (if (or (cx-nan-p timestamp) (cx-infinity-p timestamp) (> (abs timestamp) 8.64d15)) *cx-nan* (values (truncate timestamp))))
              (t *cx-nan*)))))

(defun cx-created (role entry names)
  (let ((message (obj "role" role)))
    (dolist (name names) (when (cx-has entry name) (setf (gethash name message) (gethash name entry))))
    (setf (gethash "timestamp" message) (cx-date-time entry))
    message))

(defun cx-message-from-entry (entry)
  "Pi's getMessageFromEntry: a tool result adds nothing, nor do the entry types that carry no conversation content."
  (let ((kind (cx-get entry "type")))
    (cond ((equal kind "message")
           (let ((message (gethash "message" entry)))
             (unless (hash-table-p message)
               (cx-type-error "Cannot read properties of ~A (reading 'role')" (if (cx-has entry "message") "null" "undefined")))
             (if (equal (cx-get message "role") "toolResult") nil (cx-copy message))))
          ((equal kind "custom_message") (cx-created "custom" entry '("customType" "content" "display" "details")))
          ((equal kind "branch_summary") (cx-created "branchSummary" entry '("summary" "fromId")))
          ((equal kind "compaction") (cx-created "compactionSummary" entry '("summary" "tokensBefore"))))))

(defun cx-same-value-zero (a b)
  (cond ((or (hash-table-p a) (hash-table-p b) (cx-array-p a) (cx-array-p b)) nil)
        ((and (realp a) (realp b)) (or (and (cx-nan-p a) (cx-nan-p b)) (and (not (cx-nan-p a)) (not (cx-nan-p b)) (= a b))))
        ((and (stringp a) (stringp b)) (string= a b))
        (t (eq a b))))

(defun cx-add (set value)
  "Set.prototype.add on the list SET (insertion order): VALUE joins at the end unless an equal one is in. Objects and arrays decoded from JSON are all distinct."
  (if (member value set :test #'cx-same-value-zero) set (append set (list value))))

(defun cx-add-file-op (name arguments file-ops)
  (let ((path (and (hash-table-p arguments) (cx-get arguments "path"))))
    (when (and (stringp path) (plusp (length path)))
      (cond ((equal name "read") (setf (car file-ops) (cx-add (car file-ops) path)))
            ((equal name "write") (setf (cadr file-ops) (cx-add (cadr file-ops) path)))
            ((equal name "edit") (setf (caddr file-ops) (cx-add (caddr file-ops) path)))))))

(defun prepare-branch-entries (entries &optional (token-budget 0))
  "Pi's prepareBranchEntries: ENTRIES (oldest first) become the messages to summarize, newest first until TOKEN-BUDGET (0 is no limit) is reached, with the file operations of every entry, including those that did not fit. A branch summary Pi wrote (not one an extension did) contributes the files it recorded. An entry the budget stops at is dropped, unless it is a compaction or a branch summary and the messages so far use under 90% of the budget. Timestamps are new Date(timestamp).getTime(), which the host's date parser answers."
  (let ((entries (cx-seq entries)) (newest-first '()) (file-ops (list '() '() '())) (total 0))
    (loop for entry across entries
          do (let ((details (cx-get entry "details")))
               (when (and (equal (cx-get entry "type") "branch_summary") (not (cx-truthy (cx-get entry "fromHook"))) (cx-truthy details))
                 (let ((read (and (hash-table-p details) (cx-get details "readFiles")))
                       (modified (and (hash-table-p details) (cx-get details "modifiedFiles"))))
                   (when (cx-array-p read) (loop for file across read do (setf (car file-ops) (cx-add (car file-ops) file))))
                   (when (cx-array-p modified) (loop for file across modified do (setf (caddr file-ops) (cx-add (caddr file-ops) file))))))))
    (loop for entry across (reverse entries)
          do (let ((message (cx-message-from-entry entry)))
               (when message
                 (when (equal (cx-get message "role") "assistant")
                   (let ((blocks (cx-get message "content")))
                     (when (cx-array-p blocks)
                       (loop for block across blocks
                             do (when (and (hash-table-p block) (equal (cx-get block "type") "toolCall") (cx-has block "arguments") (cx-has block "name"))
                                  (cx-add-file-op (gethash "name" block) (gethash "arguments" block) file-ops))))))
                 (let ((tokens (estimate-tokens message)))
                   (when (and (plusp token-budget) (> (+ total tokens) token-budget))
                     (when (and (cx-string-in (cx-get entry "type") '("compaction" "branch_summary")) (< total (* token-budget 0.9d0)))
                       (push message newest-first) (incf total tokens))
                     (return))
                   (push message newest-first) (incf total tokens)))))
    (make-branch-preparation (coerce newest-first 'simple-vector)
                             (make-file-operations (coerce (first file-ops) 'simple-vector) (coerce (second file-ops) 'simple-vector)
                                                   (coerce (third file-ops) 'simple-vector))
                             total)))
