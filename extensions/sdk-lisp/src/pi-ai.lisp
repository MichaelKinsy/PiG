;;;; SPDX-License-Identifier: MIT
;;;; pi-ai's pure helpers: StringEnum, contentText, getSystemMessageText and renderSystemMessageUpdate (utils/text.ts), the transcript helpers
;;;; (utils/transcript.ts), calculateCost, getSupportedThinkingLevels and clampThinkingLevel (models.ts), isContextOverflow and
;;;; isRecoverableLength (utils/overflow.ts), isRetryableAssistantError and retryDelayMs (utils/retry.ts), envApiKeyAuth (auth/helpers.ts),
;;;; getModelType and friends. A model, a message, a context and a tool are the hash-tables the wire carries, a list of them a vector. What
;;;; Pi tolerates (a missing optional field) is read as Pi reads it; what Pi throws a TypeError on (content that is neither a string nor an
;;;; array) signals an error. A function that answers undefined returns NIL.

(in-package #:pig)

(defconstant +default-max-agent-retry-delay-ms+ 60000)

;;; ---------------------------------------------------------------- JavaScript values

(defun ai-get (object key)
  "OBJECT's member KEY: :ABSENT when it has none."
  (if (hash-table-p object) (gethash key object :absent) :absent))

(defun ai-opt (object key)
  "OBJECT's member KEY as `object.key ?? undefined` reads it: NIL for an absent member and for null."
  (let ((value (ai-get object key))) (if (member value '(:absent :null)) nil value)))

(defun ai-nullish (value) (member value '(nil :null :absent)))

(defun ai-list (value)
  "The items of a JSON array as a list; none for anything else."
  (cond ((ts-array-p value) (coerce value 'list)) ((consp value) value) (t '())))

(defun ai-truthy (value)
  "JavaScript truthiness of a decoded JSON value."
  (cond ((member value '(nil :null :absent :false)) nil)
        ((stringp value) (plusp (length value)))
        ((realp value) (not (or (zerop value) (and (floatp value) (sb-ext:float-nan-p value)))))
        (t t)))

(defvar *ai-nan* (sb-kernel:make-double-float #x7ff80000 0))

(defun ai-number (value)
  "A decoded JSON number as JavaScript arithmetic reads it: null is 0, an absent or non-numeric value is NaN."
  (cond ((member value '(nil :null)) 0d0)
        ((realp value) (coerce value 'double-float))
        (t *ai-nan*)))

(defmacro ai-with-js-floats (&body body)
  "BODY with IEEE arithmetic: NaN and Infinity propagate as in JavaScript instead of trapping."
  `(sb-int:with-float-traps-masked (:invalid :overflow :divide-by-zero :inexact) ,@body))

(defun ai-number-string (value)
  "Number::toString."
  (let ((x (coerce value 'double-float)))
    (cond ((sb-ext:float-nan-p x) "NaN")
          ((sb-ext:float-infinity-p x) (if (plusp x) "Infinity" "-Infinity"))
          ((zerop x) "0")
          (t (js-number-string x)))))

(defun ai-js-string (value)
  "JavaScript's ToString of a value decoded from JSON."
  (cond ((eq value :null) "null")
        ((eq value t) "true")
        ((eq value :false) "false")
        ((stringp value) value)
        ((realp value) (ai-number-string value))
        ((ts-array-p value)
         (format nil "~{~A~^,~}" (map 'list (lambda (item) (if (member item '(nil :null)) "" (ai-js-string item))) value)))
        (t "[object Object]")))

(defun ai-string-or-empty (object key)
  "What Array.prototype.join makes of a member: absent and null are the empty string, any other value its ToString."
  (let ((value (ai-opt object key))) (if (null value) "" (ai-js-string value))))

(defun ai-equal (a b)
  (cond ((and (stringp a) (stringp b)) (string= a b))
        ((and (realp a) (realp b)) (= a b))
        (t (equal a b))))

(defun ai-ordered-keys (table)
  "The keys of a hash-table in the order Object.keys and JSON.stringify use: the array-index keys ascending, then the rest as inserted."
  (ts-keys (ts-js-order table)))

(defun ai-js-ordered (alist)
  "A hash-table of the (key . value) pairs of ALIST with its keys in Object.keys order."
  (let ((table (obj)))
    (loop for (key . value) in alist do (setf (gethash key table) value))
    (ts-js-order table)))

(defun ai-put (alist key value)
  "ALIST with KEY set: replaced in place, or appended."
  (let ((entry (assoc key alist :test #'equal)))
    (cond (entry (setf (cdr entry) value) alist)
          (t (append alist (list (cons key value)))))))

(defun ai-remove (alist key) (remove key alist :key #'car :test #'equal))

;;; ---------------------------------------------------------------- StringEnum, thinking levels, cost

(defun string-enum (values &optional options)
  "pi-ai's StringEnum: a JSON Schema string enum that Google's API and other providers that reject anyOf/const accept. The keys are type, enum,
then description and default when OPTIONS carry a non-empty one."
  (let ((schema (obj "type" "string" "enum" (ts-vec values))))
    (dolist (key '("description" "default"))
      (when (ai-truthy (ai-opt options key)) (setf (gethash key schema) (ai-opt options key))))
    schema))

(defparameter +ai-thinking-levels+ '("off" "minimal" "low" "medium" "high" "xhigh" "max"))

(defun get-supported-thinking-levels (model)
  "pi-ai's getSupportedThinkingLevels: the thinking levels MODEL accepts, in Pi's order. A model without reasoning accepts only \"off\"; a level
its thinkingLevelMap maps to null is not accepted, and xhigh and max only when the map names them."
  (if (not (ai-truthy (ai-opt model "reasoning")))
      (vector "off")
      (let ((level-map (let ((m (ai-opt model "thinkingLevelMap"))) (if (hash-table-p m) m (obj)))))
        (coerce (loop for level in +ai-thinking-levels+
                      unless (or (eq (gethash level level-map) :null)
                                 (and (member level '("xhigh" "max") :test #'string=) (not (nth-value 1 (gethash level level-map)))))
                        collect level)
                'simple-vector))))

(defun clamp-thinking-level (model level)
  "pi-ai's clampThinkingLevel: LEVEL when MODEL accepts it, else the nearest accepted level at or above it, else the nearest below it, else the
first accepted level, else \"off\". A level that is not one of Pi's is the first accepted level."
  (let* ((available (coerce (get-supported-thinking-levels model) 'list))
         (first (if available (first available) "off"))
         (requested (and (stringp level) (position level +ai-thinking-levels+ :test #'string=))))
    (cond ((and (stringp level) (member level available :test #'string=)) level)
          ((null requested) first)
          (t (or (find-if (lambda (candidate) (member candidate available :test #'string=)) (nthcdr requested +ai-thinking-levels+))
                 (find-if (lambda (candidate) (member candidate available :test #'string=)) (reverse (subseq +ai-thinking-levels+ 0 requested)))
                 first)))))

(defun calculate-cost (model usage)
  "pi-ai's calculateCost: prices USAGE at the model's per-million-token rates and stores the result in (jget usage \"cost\") (a hash-table the
caller supplies, as in Pi), which it also returns. The rates are the model's cost, or those of the tier with the highest inputTokensAbove that
the request's input (input, cacheRead and cacheWrite tokens) exceeds. Cache writes that cacheWrite1h reports are charged at twice the input rate."
  (let ((cost (ai-get usage "cost")))
    (unless (hash-table-p cost) (error "usage.cost is not an object"))
    (ai-with-js-floats
      (let* ((model-cost (let ((c (ai-opt model "cost"))) (if (hash-table-p c) c (obj))))
             (input-tokens (+ (ai-number (ai-get usage "input")) (ai-number (ai-get usage "cacheRead")) (ai-number (ai-get usage "cacheWrite"))))
             (rates model-cost)
             (matched -1d0))
        (dolist (tier (ai-list (ai-opt model-cost "tiers")))
          (let ((above (ai-number (ai-get tier "inputTokensAbove"))))
            (when (and (> input-tokens above) (> above matched)) (setf rates tier matched above))))
        (let* ((long-write (if (ai-opt usage "cacheWrite1h") (ai-number (ai-opt usage "cacheWrite1h")) 0d0))
               (short-write (- (ai-number (ai-get usage "cacheWrite")) long-write)))
          (flet ((rate (key) (ai-number (ai-get rates key))))
            (setf (gethash "input" cost) (* (/ (rate "input") 1000000) (ai-number (ai-get usage "input")))
                  (gethash "output" cost) (* (/ (rate "output") 1000000) (ai-number (ai-get usage "output")))
                  (gethash "cacheRead" cost) (* (/ (rate "cacheRead") 1000000) (ai-number (ai-get usage "cacheRead")))
                  (gethash "cacheWrite" cost) (/ (+ (* (rate "cacheWrite") short-write) (* (rate "input") 2 long-write)) 1000000)
                  (gethash "total" cost) (+ (gethash "input" cost) (gethash "output" cost) (gethash "cacheRead" cost) (gethash "cacheWrite" cost))
                  (gethash "totalFromProvider" cost) :false)))
        cost))))

;;; ---------------------------------------------------------------- text and transcripts

(defun content-text (content &optional (separator (string #\Newline)))
  "pi-ai's contentText: a string is its own text, and an array of content blocks is the text of its text blocks joined with SEPARATOR. Any other
content signals an error, as it throws in Pi."
  (cond ((stringp content) content)
        ((ts-array-p content)
         (let ((texts '()))
           (loop for block across content
                 do (when (eq block :null) (error "Cannot read properties of null (reading 'type')"))
                    (when (equal (ai-opt block "type") "text") (push (ai-string-or-empty block "text") texts)))
           (with-output-to-string (out)
             (loop for text in (nreverse texts) for first = t then nil
                   do (unless first (write-string separator out)) (write-string text out)))))
        (t (error "content.filter is not a function"))))

(defun ai-system-p (message)
  (and (hash-table-p message) (equal (gethash "role" message) "system")))

(defun ai-tool-name (tool) (and (hash-table-p tool) (ai-opt tool "name")))

(defun ai-current-tools (messages)
  "The tools after every system message's toolsRemoved and toolsAdded are applied in order, as a list in the order they were first added."
  (let ((tools '()))
    (dolist (message messages)
      (when (ai-system-p message)
        (dolist (tool (ai-list (ai-opt message "toolsRemoved"))) (setf tools (ai-remove tools (ai-tool-name tool))))
        (dolist (tool (ai-list (ai-opt message "toolsAdded"))) (setf tools (ai-put tools (ai-tool-name tool) tool)))))
    (mapcar #'cdr tools)))

(defun get-current-tools (messages)
  "pi-ai's getCurrentTools: the tools available after every system message's toolsRemoved and toolsAdded are applied in order, in the order
they were first added (a tool removed and added again moves to the end)."
  (coerce (ai-current-tools (ai-list messages)) 'simple-vector))

(defun get-current-system-message (messages)
  "pi-ai's getCurrentSystemMessage: every system message replayed into one holding the current prompt and tools. Later content is appended to
the base prompt with a blank line between, sections are patched by name (null removes one) and tools are resolved as GET-CURRENT-TOOLS does.
NIL when the transcript has no system message and no tool."
  (let ((content '()) (sections '()) (timestamp :absent) (messages (ai-list messages)))
    (dolist (message messages)
      (when (ai-system-p message)
        (when (member timestamp '(:null :absent)) (setf timestamp (ai-get message "timestamp")))
        (let ((text (content-text (ai-get message "content"))))
          (when (plusp (length text)) (push text content)))
        (let ((declared (ai-opt message "sections")))
          (when (hash-table-p declared)
            (dolist (name (ai-ordered-keys declared))
              (if (eq (gethash name declared) :null)
                  (setf sections (ai-remove sections name))
                  (setf sections (ai-put sections name (gethash name declared)))))))))
    (let ((tools (ai-current-tools messages)))
      (unless (and (eq timestamp :absent) (null tools))
        (let ((result (obj "role" "system" "content" (format nil "~{~A~^~%~%~}" (nreverse content)))))
          (when sections (setf (gethash "sections" result) (ai-js-ordered sections)))
          (when tools (setf (gethash "toolsAdded" result) (coerce tools 'simple-vector)))
          (setf (gethash "timestamp" result) (if (member timestamp '(:null :absent)) 0 timestamp))
          result)))))

(defun get-system-message-text (message)
  "pi-ai's getSystemMessageText: a system message rendered as a complete prompt, its content followed by its non-null sections, separated by
blank lines."
  (let ((parts (list (content-text (ai-get message "content"))))
        (sections (ai-opt message "sections")))
    (when (hash-table-p sections)
      (dolist (name (ai-ordered-keys sections))
        (unless (eq (gethash name sections) :null) (setf parts (append parts (list (ai-string-or-empty sections name)))))))
    (format nil "~{~A~^~%~%~}" (remove-if (lambda (part) (zerop (length part))) parts))))

(defun get-current-system-prompt (messages)
  "pi-ai's getCurrentSystemPrompt: the text of GET-CURRENT-SYSTEM-MESSAGE, or \"\" when there is none."
  (let ((message (get-current-system-message messages)))
    (if message (get-system-message-text message) "")))

(defun render-system-message-update (message)
  "pi-ai's renderSystemMessageUpdate: a later system message rendered for APIs that accept system messages mid-conversation. Section changes are
framed by name. Pi marks the framing request-time only and subject to change between versions."
  (let ((parts '()) (text (content-text (ai-get message "content"))) (sections (ai-opt message "sections")))
    (when (plusp (length text)) (push text parts))
    (when (hash-table-p sections)
      (dolist (name (ai-ordered-keys sections))
        (push (if (eq (gethash name sections) :null)
                  (format nil "Removed system prompt section \"~A\"." name)
                  (format nil "Updated system prompt section \"~A\":~%~%~A" name (ai-string-or-empty sections name)))
              parts)))
    (format nil "~{~A~^~%~%~}" (nreverse parts))))

(defun ai-initial-system-message (system-prompt tools)
  (let ((tool-list (ai-list tools)))
    (when (or (and (stringp system-prompt) (plusp (length system-prompt))) tool-list)
      (let ((message (obj "role" "system" "content" (if (stringp system-prompt) system-prompt ""))))
        (when tool-list (setf (gethash "toolsAdded" message) (coerce tool-list 'simple-vector)))
        (setf (gethash "timestamp" message) 0)
        message))))

(defun create-initial-system-message (system-prompt tools)
  "pi-ai's createInitialSystemMessage: the leading system message for a prompt and tool set, or NIL when both are empty."
  (ai-initial-system-message system-prompt tools))

(defun normalize-context (context)
  "pi-ai's normalizeContext: the context's systemPrompt and tools folded into a leading system message, as the {\"messages\": [...]} transcript
every provider-facing function expects. A context with neither keeps its messages as they are."
  (let ((messages (ai-list (ai-opt context "messages")))
        (initial (ai-initial-system-message (ai-opt context "systemPrompt") (ai-opt context "tools"))))
    (obj "messages" (coerce (if initial (cons initial messages) messages) 'simple-vector))))

(defun get-initial-system-message (messages)
  "pi-ai's getInitialSystemMessage: the leading message when it is a system message, else NIL."
  (let ((first (first (ai-list messages)))) (and (ai-system-p first) first)))

(defun without-initial-system-message (messages)
  "pi-ai's withoutInitialSystemMessage: the messages without a leading system message, for APIs that carry the prompt outside the message list."
  (let ((list (ai-list messages)))
    (coerce (if (and list (ai-system-p (first list))) (rest list) list) 'simple-vector)))

(defun collapse-system-messages (context)
  "pi-ai's collapseSystemMessages: the transcript for APIs without mid-conversation system messages. The replayed system message leads and every
later system message is dropped."
  (let* ((messages (ai-list (ai-opt context "messages")))
         (head (get-current-system-message messages))
         (rest (remove-if #'ai-system-p messages)))
    (obj "messages" (coerce (if head (cons head rest) rest) 'simple-vector))))

(defun resolve-transcript (context supports-mid-convo-system-messages)
  "pi-ai's resolveTranscript: CONTEXT itself when the model accepts later system messages, else COLLAPSE-SYSTEM-MESSAGES of it."
  (if (ai-truthy supports-mid-convo-system-messages) context (collapse-system-messages context)))

(defun get-declared-tools (messages)
  "pi-ai's getDeclaredTools: every definition the transcript's toolsAdded names, one per name, in first-declaration order with the latest
definition of each."
  (let ((declared '()))
    (dolist (message (ai-list messages))
      (when (ai-system-p message)
        (dolist (tool (ai-list (ai-opt message "toolsAdded"))) (setf declared (ai-put declared (ai-tool-name tool) tool)))))
    (coerce (mapcar #'cdr declared) 'simple-vector)))

(defun ai-last-non-additive-tool-change (system-messages)
  "The index of the last system message with a removal or a same-name redeclaration, or -1. A name declared before counts as a redeclaration
even after its removal: Kimi K3 ignores a removed tool that a later tool message declares again."
  (let ((declared '()) (last -1))
    (loop for message in system-messages for index from 0
          do (when (ai-list (ai-opt message "toolsRemoved")) (setf last index))
             (dolist (tool (ai-list (ai-opt message "toolsAdded")))
               (let ((name (ai-tool-name tool)))
                 (when (member name declared :test #'equal) (setf last index))
                 (push name declared))))
    last))

(defun has-non-additive-tool-changes-p (messages)
  "pi-ai's hasNonAdditiveToolChanges: whether the tool history holds a removal or a same-name redeclaration that an addition-only transport
cannot replay."
  (>= (ai-last-non-additive-tool-change (remove-if-not #'ai-system-p (ai-list messages))) 0))

(defun resolve-transcript-tools (messages support)
  "pi-ai's resolveTranscriptTools: {\"requestTools\": [...], \"foldedSystemMessages\": n}, the tool declarations split between the top-level
request field and in-place changes. SUPPORT is pi-ai's TranscriptToolSupport, how a transport can express tool changes after the first
request: \"none\" (only the top-level tool list exists; it always holds the current tools), \"additions\" (tools can be added at a later
message, but not removed or redefined) or \"changes\" (every tool, including the initial ones, is declared in place and a later declaration
under the same name replaces the earlier one; removals are announced as text and the removed tools stay declared). With \"changes\" the
top-level list stays empty. With \"additions\" a removal or redefinition cannot be expressed in place, so the top-level list becomes the tool
state after the last such change; later additions stay anchored. With \"none\" the top-level list is the current tool set. Any other value is
\"additions\", as Pi's comparisons treat it."
  (let* ((list (ai-list messages)) (system (remove-if-not #'ai-system-p list)))
    (cond
      ((equal support "none")
       (obj "requestTools" (coerce (ai-current-tools system) 'simple-vector) "foldedSystemMessages" (length system)))
      ((equal support "changes") (obj "requestTools" (vector) "foldedSystemMessages" 0))
      (t (let ((folded (min (length system)
                            (max (if (get-initial-system-message list) 1 0) (1+ (ai-last-non-additive-tool-change system))))))
           (obj "requestTools" (coerce (ai-current-tools (subseq system 0 folded)) 'simple-vector) "foldedSystemMessages" folded))))))

;;; ---------------------------------------------------------------- retry delay

(defun retry-delay-ms (policy attempt)
  "pi-ai's retryDelayMs: the delay before retry ATTEMPT (1-indexed): the policy's baseDelayMs doubled for each earlier attempt, a result that is
not a safe integer counting as the largest safe integer, capped by maxAgentDelayMs (60 seconds when the policy names none)."
  (ai-with-js-floats
    (let* ((exponent (max 0d0 (- (ai-number attempt) 1)))
           (delay (* (ai-number (ai-get policy "baseDelayMs")) (expt 2d0 exponent)))
           (max-safe 9007199254740991d0)
           (safe (if (and (not (sb-ext:float-nan-p delay)) (not (sb-ext:float-infinity-p delay)) (= delay (ftruncate delay)) (<= (abs delay) max-safe))
                     delay
                     max-safe))
           (limit (let ((l (ai-opt policy "maxAgentDelayMs"))) (if (null l) (coerce +default-max-agent-retry-delay-ms+ 'double-float) (ai-number l)))))
      (cond ((or (sb-ext:float-nan-p safe) (sb-ext:float-nan-p limit)) *ai-nan*)
            ((< limit safe) limit)
            (t safe)))))

;;; ---------------------------------------------------------------- JavaScript regular expressions (the subset pi-ai's patterns use)

(defstruct (rx-node (:constructor rx-node (match &optional test)))
  "MATCH is (lambda (string index continuation)); TEST, for a node that matches exactly one character, is its predicate."
  match test)

(defun rx-fold (c) (if (char<= #\A c #\Z) (char-downcase c) c))
(defun rx-word-p (c) (and (< (char-code c) 128) (or (alphanumericp c) (char= c #\_))))
(defun rx-digit-p (c) (char<= #\0 c #\9))
(defun rx-space-p (c) (find c +ts-js-space+))

(defun rx-char (test)
  (rx-node (lambda (s i k) (and (< i (length s)) (funcall test (char s i)) (funcall k (1+ i)))) test))

(defun rx-seq (nodes)
  (if (null nodes)
      (rx-node (lambda (s i k) (declare (ignore s)) (funcall k i)))
      (let ((first (rx-node-match (first nodes))) (rest (rx-node-match (rx-seq (rest nodes)))))
        (rx-node (lambda (s i k) (funcall first s i (lambda (j) (funcall rest s j k))))))))

(defun rx-alt (branches)
  (let ((matches (mapcar #'rx-node-match branches)))
    (rx-node (lambda (s i k) (some (lambda (m) (funcall m s i k)) matches)))))

(defun rx-repeat (node min max)
  (let ((match (rx-node-match node)) (test (rx-node-test node)))
    (rx-node
     (if test
         (lambda (s i k)
           (let ((end i))
             (loop while (and (< end (length s)) (or (null max) (< (- end i) max)) (funcall test (char s end))) do (incf end))
             (loop for j from end downto (+ i min) thereis (funcall k j))))
         (lambda (s i k)
           (labels ((try (count i)
                      (or (and (or (null max) (< count max))
                               (funcall match s i (lambda (j) (and (> j i) (try (1+ count) j)))))
                          (and (>= count min) (funcall k i)))))
             (try 0 i)))))))

(defun rx-compile (source)
  "A function of a string that is true when the JavaScript regular expression SOURCE with the i flag (and no u flag) matches somewhere in it.
A letter matches its own two ASCII cases only, in a class too; . is any one UTF-16 unit but a line terminator; \\s \\d \\w are JavaScript's; $ is
only the end of the text. Supported: literals, . [] [^] \\d \\w \\s \\S, (?:) () (?!) (?<!), | ? * +, ^ and $."
  (let ((pos 0) (n (length source)))
    (labels ((peek () (and (< pos n) (char source pos)))
             (next () (prog1 (char source pos) (incf pos)))
             (eat (c) (when (eql (peek) c) (incf pos) t))
             (alternation ()
               (let ((branches (list (sequence*))))
                 (loop while (eat #\|) do (push (sequence*) branches))
                 (if (rest branches) (rx-alt (nreverse branches)) (first branches))))
             (sequence* ()
               (let ((items '()))
                 (loop while (and (peek) (not (member (peek) '(#\| #\))))) do (push (quantified) items))
                 (rx-seq (nreverse items))))
             (quantified ()
               (let ((node (atom*)))
                 (cond ((eat #\?) (rx-repeat node 0 1)) ((eat #\*) (rx-repeat node 0 nil)) ((eat #\+) (rx-repeat node 1 nil)) (t node))))
             (literal (c) (let ((c (rx-fold c))) (rx-char (lambda (x) (char= (rx-fold x) c)))))
             (escape-test (c)
               (case c
                 (#\d #'rx-digit-p) (#\D (complement #'rx-digit-p)) (#\w #'rx-word-p) (#\W (complement #'rx-word-p))
                 (#\s #'rx-space-p) (#\S (complement #'rx-space-p))))
             (bracket ()
               (let ((negated (eat #\^)) (tests '()))
                 (loop until (eql (peek) #\])
                       do (let ((c (next)))
                            (cond ((and (char= c #\\) (escape-test (peek))) (push (escape-test (next)) tests))
                                  (t (let ((low (if (char= c #\\) (next) c)))
                                       (if (and (eql (peek) #\-) (< (1+ pos) n) (char/= (char source (1+ pos)) #\]))
                                           (let ((high (progn (next) (next))))
                                             (push (lambda (x) (flet ((in (y) (char<= low y high)))
                                                                 (or (in x) (and (alpha-char-p x) (< (char-code x) 128) (in (if (upper-case-p x) (char-downcase x) (char-upcase x)))))))
                                                   tests))
                                           (let ((low (rx-fold low))) (push (lambda (x) (char= (rx-fold x) low)) tests))))))))
                 (next)
                 (let ((test (lambda (x) (some (lambda (f) (funcall f x)) tests))))
                   (rx-char (if negated (complement test) test)))))
             (atom* ()
               (let ((c (next)))
                 (case c
                   (#\( (let ((kind (cond ((eat #\?) (cond ((eat #\:) :group) ((eat #\!) :ahead) ((and (eat #\<) (eat #\!)) :behind) (t (error "regexp ~S: unsupported group" source))))
                                          (t :group))))
                          (let ((inner (alternation)))
                            (eat #\))
                            (let ((match (rx-node-match inner)))
                              (ecase kind
                                (:group inner)
                                (:ahead (rx-node (lambda (s i k) (and (not (funcall match s i (lambda (j) (declare (ignore j)) t))) (funcall k i)))))
                                (:behind (rx-node (lambda (s i k)
                                                    (and (not (loop for j from i downto 0 thereis (funcall match s j (lambda (e) (= e i)))))
                                                         (funcall k i))))))))))
                   (#\. (rx-char (lambda (x) (not (find x '(#\Newline #\Return #.(code-char #x2028) #.(code-char #x2029)))))))
                   (#\^ (rx-node (lambda (s i k) (declare (ignore s)) (and (= i 0) (funcall k i)))))
                   (#\$ (rx-node (lambda (s i k) (and (= i (length s)) (funcall k i)))))
                   (#\[ (bracket))
                   (#\\ (let ((e (next))) (let ((test (escape-test e))) (if test (rx-char test) (literal e)))))
                   (t (literal c))))))
      (let ((match (rx-node-match (alternation))))
        (lambda (string)
          (let ((units (ai-js-units string)))
            (loop for i from 0 to (length units) thereis (funcall match units i (lambda (j) (declare (ignore j)) t)))))))))

(defun ai-js-units (text)
  "TEXT as one character per UTF-16 code unit, which a JavaScript regular expression without the u flag steps over."
  (if (every (lambda (c) (< (char-code c) #x10000)) text)
      text
      (with-output-to-string (out)
        (loop for c across text
              for code = (char-code c)
              do (if (< code #x10000)
                     (write-char c out)
                     (let ((v (- code #x10000)))
                       (write-char (code-char (+ #xD800 (ash v -10))) out)
                       (write-char (code-char (+ #xDC00 (logand v #x3FF))) out)))))))

(defun ai-compile-each (&rest patterns) (mapcar #'rx-compile patterns))
(defun ai-any-match (matchers text) (some (lambda (matcher) (funcall matcher text)) matchers))

(defvar *ai-overflow-patterns*
  (ai-compile-each
   "prompt (?:is )?too long" "prompt exceeds max length" "request_too_large" "input is too long for requested model"
   "exceeds the context window" "exceeds (?:the )?(?:model'?s )?maximum context length(?: of [\\d,]+ tokens?|\\s*\\([\\d,]+\\))"
   "input token count.*exceeds the maximum" "maximum prompt length is \\d+" "reduce the length of the messages"
   "maximum context length is \\d+ tokens" "exceeds (?:the )?maximum allowed input length of [\\d,]+ tokens?"
   "input \\(\\d+ tokens\\) is longer than the model'?s context length \\(\\d+ tokens\\)" "exceeds the limit of \\d+"
   "exceeds the available context size" "greater than the context length" "context window exceeds limit" "exceeded model token limit"
   "too large for model with \\d+ maximum context length" "prompt has [\\d,]+ tokens?, but the configured context size is [\\d,]+ tokens?"
   "model_context_window_exceeded" "prompt too long; exceeded (?:max )?context length" "range of input length should be"
   "context[_ ]length[_ ]exceeded" "too many tokens" "token limit exceeded"
   ;; HTTP 413 means request-size overflow regardless of provider or body text.
   "^(?:[\\w -]+ API error \\(413\\):|413(?:\\s|:|$))"))
(defvar *ai-non-overflow-patterns* (ai-compile-each "^(Throttling error|Service unavailable):" "rate limit" "too many requests"))
(defvar *ai-cerebras-bodyless-overflow* (rx-compile "^4(?:00|13)\\s*(?:status code)?\\s*\\(no body\\)"))

(defun ai-join-alternatives (&rest alternatives) (format nil "~{~A~^|~}" alternatives))

(defvar *ai-non-retryable-provider-limit-error*
  (rx-compile (ai-join-alternatives
               "GoUsageLimitError" "FreeUsageLimitError" "Monthly usage limit reached" "available balance" "insufficient_quota" "out of budget"
               "quota exceeded" "billing" "subscription_sharing_usage_limit_exceeded"
               "subscription_sharing_workplace_individual_usage_limit_exceeded")))

(defvar *ai-retryable-provider-error*
  (rx-compile (ai-join-alternatives
               "overloaded" "server_busy" "servers are currently busy" "currently experiencing high demand" "model is at capacity" "rate.?limit"
               "too many requests"
               ;; status codes must not be part of a longer word or number, e.g. a request id or byte count
               "(?<![a-z0-9])(?:429|500|502|503|504|520|524)(?![a-z0-9])"
               "service.?(?:is.?)?(?:temporarily.?)?unavailable" "server.?error" "internal.?error" "provider.?returned.?(?:an.?)?error"
               "exceeded request buffer limit while retrying upstream" "unable to forward upstream" "upstream.?request.?failed"
               "upstream_stream_read_error" "stream was interrupted" "http/?2 stream failed" "network.?error" "connection.?error"
               "connection.?refused" "connection.?lost" "other side closed" "fetch failed" "request to \\S+ failed, reason:" "getaddrinfo"
               "ENOTFOUND" "EAI_AGAIN" "upstream.?connect" "reset before headers" "socket hang up" "socket connection was closed"
               "timed? out" "timeout" "terminated" "websocket.?closed" "websocket.?error" "ended without" "stream ended before message_stop"
               "stream ended before a terminal response event" "http2 request did not get a response"
               "stream disconnected before completion"
               "pending stream has been canceled" "retry delay" "you can retry your request" "try your request again"
               "please retry your request" "ResourceExhausted" "subscription_sharing_usage_unavailable" "subscription_sharing_user_unavailable")))

(defun ai-assistant-error (message)
  "(values stop-reason error-text) of an assistant message."
  (let ((error (ai-opt message "errorMessage")))
    (values (ai-opt message "stopReason") (if (stringp error) error ""))))

(defun retryable-assistant-error-p (message)
  "pi-ai's isRetryableAssistantError: whether a failed assistant message looks like a transient provider or transport error, so the caller can
restart the turn. It is a classifier, not a retry policy: a quota, billing or subscription-limit message is not retryable. Handle
CONTEXT-OVERFLOW-P first."
  (multiple-value-bind (stop-reason error) (ai-assistant-error message)
    (and (equal stop-reason "error") (plusp (length error))
         (not (funcall *ai-non-retryable-provider-limit-error* error))
         (funcall *ai-retryable-provider-error* error)
         t)))

(defun context-overflow-p (message &optional context-window)
  "pi-ai's isContextOverflow: whether an assistant message is a context overflow. It is one when it failed with an overflow error message that
is not a rate limit or throttling message, or when it failed with a Cerebras body-less 400 or 413. With a truthy CONTEXT-WINDOW it is also one
when it stopped normally with more input (input and cacheRead tokens) than the window, or stopped at its length limit with no output and
input that fills 99% of the window."
  (multiple-value-bind (stop-reason error) (ai-assistant-error message)
    (or (and (equal stop-reason "error") (plusp (length error))
             (not (ai-any-match *ai-non-overflow-patterns* error))
             (or (ai-any-match *ai-overflow-patterns* error)
                 (and (equal (ai-opt message "provider") "cerebras") (funcall *ai-cerebras-bodyless-overflow* error)))
             t)
        (and (ai-truthy context-window)
             (ai-with-js-floats
               (let* ((usage (let ((u (ai-opt message "usage"))) (if (ai-truthy u) u (obj))))
                      (input-tokens (+ (ai-number (ai-get usage "input")) (ai-number (ai-get usage "cacheRead"))))
                      (window (ai-number context-window)))
                 (or (and (equal stop-reason "stop") (> input-tokens window))
                     (and (equal stop-reason "length") (= (ai-number (ai-get usage "output")) 0) (>= input-tokens (* window 0.99d0)))))))
        nil)))

(defun recoverable-length-p (message desired-max-output)
  "pi-ai's isRecoverableLength: whether a length stop ended below the output limit the caller or model intended (DESIRED-MAX-OUTPUT, before any
context-based clamping), so one compact-and-retry attempt may help."
  (ai-with-js-floats
    (let ((usage (let ((u (ai-opt message "usage"))) (if (ai-truthy u) u (obj)))))
      (and (equal (ai-opt message "stopReason") "length")
           (> (ai-number desired-max-output) 0)
           (< (ai-number (ai-get usage "output")) (ai-number desired-max-output))))))

;;; ---------------------------------------------------------------- envApiKeyAuth

(declaim (ftype function make-api-key-auth make-auth-result api-key-auth-input-ctx api-key-auth-input-credential api-key-auth-input-signal
                auth-context-env auth-interaction-prompt auth-interaction-signal))

(defun ai-throw-if-aborted (signal)
  (when (and signal (abort-signal-p signal) (abort-signal-set-p signal)) (error "This operation was aborted")))

(defun env-api-key-auth (name env-vars)
  "pi-ai's envApiKeyAuth: the standard API-key auth of a provider whose key comes from a stored credential or an environment variable. A stored
credential's key wins (its env travels with it); otherwise the first of ENV-VARS that the auth context finds non-empty resolves, and none
resolving is NIL. Its login prompts for the key with a secret prompt and returns an api_key credential. The auth has no check, as in Pi."
  (let ((variables (ai-list env-vars)))
    (make-api-key-auth
     :name name
     :login (lambda (interaction)
              (ai-throw-if-aborted (auth-interaction-signal interaction))
              (let ((key (funcall (auth-interaction-prompt interaction) (obj "type" "secret" "message" (concatenate 'string "Enter " name)))))
                (ai-throw-if-aborted (auth-interaction-signal interaction))
                (obj "type" "api_key" "key" key)))
     :resolve (lambda (input)
                (ai-throw-if-aborted (api-key-auth-input-signal input))
                (let ((credential (api-key-auth-input-credential input)))
                  (if (and (hash-table-p credential) (ai-truthy (ai-opt credential "key")))
                      (make-auth-result :auth (obj "apiKey" (gethash "key" credential))
                                        :env (let ((env (ai-opt credential "env"))) (and (hash-table-p env) env))
                                        :source "stored credential")
                      (dolist (variable variables)
                        (let ((value (funcall (auth-context-env (api-key-auth-input-ctx input)) variable)))
                          (ai-throw-if-aborted (api-key-auth-input-signal input))
                          (when (ai-truthy value)
                            (return (make-auth-result :auth (obj "apiKey" value) :source variable))))))))))))

;;; ---------------------------------------------------------------- model types

(defun get-model-type (model)
  "pi-ai's getModelType: the type of MODEL, \"chat\", \"image\" or \"classifier\". A model without a type is a chat model."
  (or (ai-opt model "type") "chat"))

(defun model-type-p (model model-type)
  "pi-ai's isModelType: whether MODEL is of MODEL-TYPE, a model without a type counting as a chat model."
  (ai-equal (get-model-type model) (if (ai-nullish model-type) nil model-type)))

(defun has-api-p (model api)
  "pi-ai's hasApi: whether MODEL is a chat model of API. A model of another type never matches, even when its api names the same API."
  (and (model-type-p model "chat") (ai-equal (ai-opt model "api") (if (ai-nullish api) nil api)) t))

(defun models-are-equal-p (a b)
  "pi-ai's modelsAreEqual: whether two models have the same type, id and provider. It is false when either is NIL."
  (and (not (ai-nullish a)) (not (ai-nullish b))
       (ai-equal (get-model-type a) (get-model-type b))
       (ai-equal (ai-opt a "id") (ai-opt b "id"))
       (ai-equal (ai-opt a "provider") (ai-opt b "provider"))))
