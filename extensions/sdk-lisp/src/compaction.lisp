;;;; SPDX-License-Identifier: MIT
;;;; Lane C: pi-coding-agent's compaction helpers (core/compaction/compaction.ts) and the JavaScript value rules the lane's helpers share.
;;;; Messages and entries are decoded JSON (hash-tables, vectors, strings, numbers, T :FALSE :NULL). Names of internals start with CX-.

(in-package #:pig)

;;; ---------------------------------------------------------------- JavaScript value rules shared by the lane

(define-condition js-type-error (pig-error)
  ((message :initarg :message :reader js-type-error-message))
  (:report (lambda (c s) (write-string (js-type-error-message c) s)))
  (:documentation "The TypeError Pi's own code throws for a malformed value; the message is Pi's."))

(defun cx-type-error (control &rest arguments)
  (error 'js-type-error :message (apply #'format nil control arguments)))

(defmacro cx-js-math (&body body)
  "BODY with IEEE results (NaN, infinities) instead of floating-point traps."
  `(sb-int:with-float-traps-masked (:invalid :divide-by-zero :overflow :inexact) ,@body))

(defvar *cx-nan* (sb-kernel:make-double-float -524288 0))

(defun cx-nan-p (x) (and (floatp x) (sb-ext:float-nan-p x)))
(defun cx-infinity-p (x) (and (floatp x) (sb-ext:float-infinity-p x)))

(defun cx-number (x)
  "X read as a JavaScript number: a non-number is NaN."
  (if (realp x) x *cx-nan*))

(defun cx-get (object key)
  "Member KEY of a JSON object as Python's dict.get reads it: NIL for a missing member and for null."
  (let ((value (and (hash-table-p object) (gethash key object))))
    (if (eq value :null) nil value)))

(defun cx-has (object key)
  (and (hash-table-p object) (nth-value 1 (gethash key object))))

(defun cx-with (object key value)
  "A copy of the JSON object OBJECT with KEY set to VALUE, in OBJECT's member order."
  (let ((copy (make-hash-table :test 'equal)))
    (maphash (lambda (k v) (setf (gethash k copy) v)) object)
    (setf (gethash key copy) value)
    copy))

(defun cx-truthy (value)
  "JavaScript truthiness of a decoded JSON value."
  (cond ((or (null value) (eq value :null) (eq value :false)) nil)
        ((stringp value) (plusp (length value)))
        ((realp value) (not (or (cx-nan-p value) (zerop value))))
        (t t)))

(defun cx-array-p (value) (and (vectorp value) (not (stringp value))))

(defun cx-string-in (value strings) (and (stringp value) (member value strings :test #'string=) t))

(defun cx-utf16-length (text)
  (loop for c across text sum (if (>= (char-code c) #x10000) 2 1)))

(defun cx-utf16-units (text)
  (let ((units (make-array (length text) :element-type 't :adjustable t :fill-pointer 0)))
    (loop for c across text
          for code = (char-code c)
          do (if (>= code #x10000)
                 (let ((v (- code #x10000)))
                   (vector-push-extend (+ #xD800 (ash v -10)) units)
                   (vector-push-extend (+ #xDC00 (logand v #x3FF)) units))
                 (vector-push-extend code units)))
    units))

(defun cx-units-string (units start end)
  "The string of UNITS[START, END): a high surrogate directly followed by a low one is one character, any other surrogate stays a character of its own."
  (with-output-to-string (out)
    (loop with i = start
          while (< i end)
          do (let ((u (aref units i)))
               (if (and (<= #xD800 u #xDBFF) (< (1+ i) end) (<= #xDC00 (aref units (1+ i)) #xDFFF))
                   (progn (write-char (code-char (+ #x10000 (ash (- u #xD800) 10) (- (aref units (1+ i)) #xDC00))) out) (incf i 2))
                   (progn (write-char (code-char u) out) (incf i)))))))

(defun cx-scalars (text)
  "TEXT as Buffer.from(text, \"utf-8\") reads it: a surrogate pair held as two characters is one, a lone surrogate is U+FFFD."
  (if (notany (lambda (c) (<= #xD800 (char-code c) #xDFFF)) text)
      text
      (let ((units (cx-utf16-units text)))
        (with-output-to-string (out)
          (loop with i = 0 with end = (length units)
                while (< i end)
                do (let ((u (aref units i)))
                     (cond ((and (<= #xD800 u #xDBFF) (< (1+ i) end) (<= #xDC00 (aref units (1+ i)) #xDFFF))
                            (write-char (code-char (+ #x10000 (ash (- u #xD800) 10) (- (aref units (1+ i)) #xDC00))) out)
                            (incf i 2))
                           ((<= #xD800 u #xDFFF) (write-char (code-char #xFFFD) out) (incf i))
                           (t (write-char (code-char u) out) (incf i)))))))))

(defun cx-utf8-bytes (text)
  (sb-ext:string-to-octets (cx-scalars text) :external-format :utf-8))

(defun cx-utf8-length (text)
  "Buffer.byteLength(text, \"utf-8\")."
  (let ((n 0) (i 0) (len (length text)))
    (loop while (< i len)
          do (let ((c (char-code (char text i))))
               (cond ((< c #x80) (incf n))
                     ((< c #x800) (incf n 2))
                     ((and (<= #xD800 c #xDBFF) (< (1+ i) len) (<= #xDC00 (char-code (char text (1+ i))) #xDFFF))
                      (incf n 4) (incf i))
                     ((< c #x10000) (incf n 3))
                     (t (incf n 4))))
             (incf i))
    n))

(defparameter *cx-js-space*
  (coerce (mapcar #'code-char '(9 10 11 12 13 32 #xA0 #x1680 #x2000 #x2001 #x2002 #x2003 #x2004 #x2005 #x2006 #x2007 #x2008 #x2009 #x200A
                                #x2028 #x2029 #x202F #x205F #x3000 #xFEFF))
          'string)
  "The ECMAScript white space and line terminators String.prototype.trim removes.")

(defun cx-js-trim (text) (string-trim *cx-js-space* text))

(defun cx-js-number-string (x)
  "Number::toString of a JSON number (NaN, Infinity and -Infinity spelled as JavaScript does)."
  (cond ((cx-nan-p x) "NaN")
        ((cx-infinity-p x) (if (plusp x) "Infinity" "-Infinity"))
        ((zerop x) "0")
        ((and (integerp x) (< (abs x) +max-safe-integer+)) (let ((*print-base* 10) (*print-radix* nil)) (princ-to-string x)))
        (t (js-number-string (coerce x 'double-float)))))

(defun cx-json-length (value)
  "The UTF-16 length of JSON.stringify(value)."
  (cx-utf16-length (to-json value)))

(defun cx-seq (sequence) (coerce sequence 'simple-vector))

;;; ---------------------------------------------------------------- usage

(defstruct (compaction-settings (:constructor make-compaction-settings (&key (enabled t) (reserve-tokens 16384) (keep-recent-tokens 20000))))
  "Pi's CompactionSettings."
  enabled reserve-tokens keep-recent-tokens)

(defparameter *default-compaction-settings* (make-compaction-settings)
  "Pi's DEFAULT_COMPACTION_SETTINGS.")

(defun cx-addend (usage key)
  "A token count as JavaScript's + reads it: null is 0, a missing or non-numeric count is NaN."
  (if (eq (gethash key usage) :null) 0 (cx-number (and (hash-table-p usage) (gethash key usage)))))

(defun calculate-context-tokens (usage)
  "Pi's calculateContextTokens: the native total when it is truthy, else the sum of the components."
  (cx-js-math
    (let ((total (cx-number (and (hash-table-p usage) (gethash "totalTokens" usage)))))
      (if (and (not (cx-nan-p total)) (/= total 0))
          total
          (reduce #'+ (mapcar (lambda (key) (cx-addend usage key)) '("input" "output" "cacheRead" "cacheWrite")))))))

(defun should-compact (context-tokens context-window settings)
  "Pi's shouldCompact: compaction triggers when the context exceeds the window less the reserve."
  (and (compaction-settings-enabled settings)
       (cx-js-math (> context-tokens (- context-window (compaction-settings-reserve-tokens settings))))))

(defun get-last-assistant-usage (entries)
  "Pi's getLastAssistantUsage: the usage of the last assistant message that did not abort or fail and whose usage is not all zero, or NIL."
  (loop for entry across (reverse (cx-seq entries))
        do (let ((message (and (equal (cx-get entry "type") "message") (cx-get entry "message"))))
             (when (and (hash-table-p message)
                        (equal (cx-get message "role") "assistant")
                        (not (cx-string-in (cx-get message "stopReason") '("aborted" "error"))))
               (let ((usage (cx-get message "usage")))
                 (when (and (hash-table-p usage) (cx-js-math (> (calculate-context-tokens usage) 0)))
                   (return usage)))))))

;;; ---------------------------------------------------------------- estimate

(defun cx-content-chars (content)
  (cond ((stringp content) (cx-utf16-length content))
        ((cx-array-p content)
         (loop for block across content
               for kind = (cx-get block "type")
               sum (cond ((equal kind "text") (let ((text (cx-get block "text"))) (if (stringp text) (cx-utf16-length text) 0)))
                         ((equal kind "image") 4800)
                         (t 0))))
        (t 0)))

(defun cx-string-length (value) (if (stringp value) (cx-utf16-length value) 0))

(defun estimate-tokens (message)
  "Pi's estimateTokens for one agent message (a role-discriminated object): a chars/4 heuristic over its UTF-16 length, rounded up. A role Pi does not size is 0."
  (let ((role (cx-get message "role")) (chars 0))
    (cond
      ((equal role "system")
       (setf chars (cx-content-chars (cx-get message "content")))
       (let ((sections (cx-get message "sections")))
         (when (hash-table-p sections)
           (maphash (lambda (k v) (declare (ignore k)) (when (stringp v) (incf chars (cx-utf16-length v)))) sections)))
       (let ((added (cx-get message "toolsAdded")))
         (when (cx-truthy added) (incf chars (cx-json-length added)))))
      ((cx-string-in role '("user" "custom" "toolResult")) (setf chars (cx-content-chars (cx-get message "content"))))
      ((equal role "assistant")
       (let ((blocks (cx-get message "content")))
         (when (cx-array-p blocks)
           (loop for block across blocks
                 for kind = (cx-get block "type")
                 do (cond ((equal kind "text") (incf chars (cx-string-length (cx-get block "text"))))
                          ((equal kind "thinking") (incf chars (cx-string-length (cx-get block "thinking"))))
                          ((equal kind "toolCall")
                           (incf chars (+ (cx-string-length (cx-get block "name")) (cx-json-length (cx-get block "arguments"))))))))))
      ((equal role "bashExecution") (setf chars (+ (cx-string-length (cx-get message "command")) (cx-string-length (cx-get message "output")))))
      ((cx-string-in role '("branchSummary" "compactionSummary")) (setf chars (cx-string-length (cx-get message "summary"))))
      (t (return-from estimate-tokens 0)))
    (ceiling chars 4)))

;;; ---------------------------------------------------------------- cut points

(defstruct (cut-point-result (:constructor make-cut-point-result (first-kept-entry-index turn-start-index split-turn-p)))
  "Pi's CutPointResult: the index of the first entry to keep, the index of the message that starts the turn being split (-1 when none is split), and whether the cut is not a turn start."
  first-kept-entry-index turn-start-index split-turn-p)

(defun cx-context-messages (entry)
  "Pi's sessionEntryToContextMessages for the entries the cut point logic reads, as a list: a message that is not an object is one message without a role, a null or missing message has none."
  (unless (hash-table-p entry) (return-from cx-context-messages nil))
  (let ((kind (cx-get entry "type")))
    (cond
      ((equal kind "message")
       (let ((message (cx-get entry "message")))
         (cond ((null message) nil)
               ((not (hash-table-p message)) (list (obj)))
               ((cx-get message "content") (list message))
               (t (let ((role (cx-get message "role")))
                    (cond ((equal role "system") (list (cx-with message "content" "")))
                          ((cx-string-in role '("user" "assistant" "toolResult")) (list (cx-with message "content" (vector))))
                          (t (list message))))))))
      ((equal kind "custom_message")
       (list (obj "role" "custom" "content" (or (cx-get entry "content") (vector)))))
      ((equal kind "branch_summary")
       (let ((summary (cx-get entry "summary")))
         (and (stringp summary) (plusp (length summary)) (list (obj "role" "branchSummary" "summary" summary)))))
      ((equal kind "compaction")
       (let ((summary (cx-get entry "summary")) (system (cx-get entry "systemMessage")))
         (append (and (hash-table-p system) (list system))
                 (list (obj "role" "compactionSummary" "summary" (if (stringp summary) summary ""))))))
      (t nil))))

(defun cx-compaction-p (entry) (and (hash-table-p entry) (equal (cx-get entry "type") "compaction")))

(defun cx-has-role (entry roles)
  (and (not (cx-compaction-p entry))
       (some (lambda (message) (cx-string-in (cx-get message "role") roles)) (cx-context-messages entry))))

(defparameter *cx-cut-point-roles* '("user" "assistant" "bashExecution" "custom" "branchSummary" "compactionSummary"))
(defparameter *cx-turn-start-roles* '("user" "bashExecution" "custom" "branchSummary" "compactionSummary"))

(defun find-turn-start-index (entries entry-index start-index)
  "Pi's findTurnStartIndex: the index of the context-visible message that starts the turn holding ENTRY-INDEX, or -1 when none starts it at or after START-INDEX."
  (let ((entries (cx-seq entries)))
    (loop for i from (min entry-index (1- (length entries))) downto start-index
          when (cx-has-role (svref entries i) *cx-turn-start-roles*) do (return i)
          finally (return -1))))

(defun find-cut-point (entries start-index end-index keep-recent-tokens)
  "Pi's findCutPoint: walks back from the newest entry before END-INDEX adding estimated message sizes until KEEP-RECENT-TOKENS is reached, cuts at the closest valid cut point at or after that entry (never at a tool result), then moves the cut back over adjacent entries that do not affect the context."
  (let* ((entries (cx-seq entries))
         (end-index (min end-index (length entries)))
         (cut-points (loop for i from start-index below end-index
                           when (cx-has-role (svref entries i) *cx-cut-point-roles*) collect i)))
    (when (null cut-points) (return-from find-cut-point (make-cut-point-result start-index -1 nil)))
    (let ((accumulated 0) (cut-index (first cut-points)))
      (loop for i from (1- end-index) downto start-index
            do (let ((tokens (reduce #'+ (mapcar #'estimate-tokens (cx-context-messages (svref entries i))))))
                 (unless (zerop tokens)
                   (incf accumulated tokens)
                   (when (cx-js-math (>= accumulated keep-recent-tokens))
                     (setf cut-index (or (find-if (lambda (candidate) (>= candidate i)) cut-points) (car (last cut-points))))
                     (return)))))
      (loop while (> cut-index start-index)
            do (let ((previous (svref entries (1- cut-index))))
                 (when (or (cx-compaction-p previous) (cx-context-messages previous)) (return))
                 (decf cut-index)))
      (let* ((starts-turn (cx-has-role (svref entries cut-index) *cx-turn-start-roles*))
             (turn-start (if starts-turn -1 (find-turn-start-index entries cut-index start-index))))
        (make-cut-point-result cut-index turn-start (and (not starts-turn) (/= turn-start -1)))))))
