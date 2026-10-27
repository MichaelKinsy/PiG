;;;; SPDX-License-Identifier: MIT
;;;; Lane C: pi-coding-agent's small pure helpers (tool-result guards, message helpers, language, session and skill helpers, image notes) and the helpers
;;;; the host answers (frontmatter, Date.parse, diffs, images, clipboard, the file mutation queue). The host ones work before the extension runs.

(in-package #:pig)

;;; ---------------------------------------------------------------- event guards and message helpers

(defun cx-tool-name-p (data name)
  (and (hash-table-p data) (equal (gethash "toolName" data) name)))

(defun tool-call-event-type-p (tool-name event)
  "Pi's isToolCallEventType: whether a tool_call EVENT is a call of the tool TOOL-NAME, built-in or custom. An event without a string toolName is no call of any tool."
  (and (hash-table-p event) (stringp (gethash "toolName" event)) (string= (gethash "toolName" event) tool-name)))

(defun bash-tool-result-p (data) "Whether a tool_result event is the result of the bash tool (Pi's isBashToolResult)." (cx-tool-name-p data "bash"))
(defun powershell-tool-result-p (data) "Pi's isPowerShellToolResult." (cx-tool-name-p data "powershell"))
(defun read-tool-result-p (data) "Pi's isReadToolResult." (cx-tool-name-p data "read"))
(defun edit-tool-result-p (data) "Pi's isEditToolResult." (cx-tool-name-p data "edit"))
(defun write-tool-result-p (data) "Pi's isWriteToolResult." (cx-tool-name-p data "write"))
(defun grep-tool-result-p (data) "Pi's isGrepToolResult." (cx-tool-name-p data "grep"))
(defun find-tool-result-p (data) "Pi's isFindToolResult." (cx-tool-name-p data "find"))
(defun ls-tool-result-p (data) "Pi's isLsToolResult." (cx-tool-name-p data "ls"))

(defun message-role (data)
  "The role of an event's message payload, or \"\" when the event carries none or its role is not a string."
  (let ((role (cx-get (cx-get data "message") "role")))
    (if (stringp role) role "")))

(defun message-text (data)
  "The concatenated text blocks of an event's message payload; \"\" when it has none."
  (let* ((message (cx-get data "message")) (content (cx-get message "content")))
    (cond ((not (hash-table-p message)) "")
          ((stringp content) content)
          ((not (cx-array-p content)) "")
          (t (apply #'concatenate 'string
                    (loop for block across content
                          when (and (equal (cx-get block "type") "text") (stringp (cx-get block "text"))) collect (cx-get block "text")))))))

;;; ---------------------------------------------------------------- language

(defparameter *cx-languages*
  (let ((table (make-hash-table :test 'equal)))
    (loop for (language . extensions)
            in '(("typescript" "ts" "tsx") ("javascript" "js" "jsx" "mjs" "cjs") ("python" "py") ("ruby" "rb") ("rust" "rs") ("go" "go")
                 ("java" "java") ("kotlin" "kt") ("swift" "swift") ("c" "c" "h") ("cpp" "cpp" "cc" "cxx" "hpp") ("csharp" "cs") ("php" "php")
                 ("bash" "sh" "bash" "zsh") ("fish" "fish") ("powershell" "ps1") ("sql" "sql") ("html" "html" "htm") ("css" "css")
                 ("scss" "scss") ("sass" "sass") ("less" "less") ("json" "json") ("yaml" "yaml" "yml") ("toml" "toml") ("xml" "xml")
                 ("markdown" "md" "markdown") ("dockerfile" "dockerfile") ("makefile" "makefile") ("cmake" "cmake") ("lua" "lua")
                 ("perl" "perl") ("r" "r") ("scala" "scala") ("clojure" "clj") ("elixir" "ex" "exs") ("erlang" "erl") ("haskell" "hs")
                 ("ocaml" "ml") ("vim" "vim") ("graphql" "graphql") ("protobuf" "proto") ("hcl" "tf" "hcl"))
          do (dolist (extension extensions) (setf (gethash extension table) language)))
    table)
  "Pi's extension-to-language map of the interactive theme.")

(defun get-language-from-path (file-path)
  "Pi's getLanguageFromPath: the highlight language for the text after the last \".\" of FILE-PATH, lower-cased (a path without a dot is its own extension, so \"Dockerfile\" is dockerfile), or NIL."
  (let* ((dot (position #\. file-path :from-end t))
         (extension (if dot (subseq file-path (1+ dot)) file-path)))
    (and (plusp (length extension)) (values (gethash (sb-unicode:lowercase extension) *cx-languages*)))))

;;; ---------------------------------------------------------------- session and skill helpers

(defconstant +current-session-version+ 3 "Pi's CURRENT_SESSION_VERSION: the version a session file's header carries.")
(defparameter *virtual-model-state-entry* "pi.virtual-model-state"
  "Pi's VIRTUAL_MODEL_STATE_ENTRY: the custom entry type that persists the virtual model state.")

(defun get-latest-compaction-entry (entries)
  "Pi's getLatestCompactionEntry: the last \"compaction\" entry of ENTRIES, or NIL."
  (find-if #'cx-compaction-p (cx-seq entries) :from-end t))

(defstruct (parsed-skill-block (:constructor make-parsed-skill-block (name location content user-message)))
  "Pi's ParsedSkillBlock; USER-MESSAGE is the trimmed text after the block, NIL where Pi's is undefined."
  name location content user-message)

(defun cx-at-p (text prefix start)
  (let ((end (+ start (length prefix))))
    (and (<= end (length text)) (string= prefix text :start2 start :end2 end))))

(defun parse-skill-block (text)
  "Pi's parseSkillBlock: reads <skill name=\"N\" location=\"L\">, a newline, CONTENT, a newline and </skill>, optionally followed by a blank line and the user's message, where the block must span the whole text; else NIL. The content ends at the first \"\\n</skill>\" that leaves either nothing or a blank line and at least one more character, as Pi's lazy pattern does."
  (let ((prefix "<skill name=\"") (location-prefix " location=\"") (closing (format nil "~%</skill>")))
    (unless (cx-at-p text prefix 0) (return-from parse-skill-block nil))
    (let* ((name-start (length prefix))
           (name-end (position #\" text :start name-start)))
      (unless (and name-end (> name-end name-start) (cx-at-p text location-prefix (1+ name-end))) (return-from parse-skill-block nil))
      (let* ((location-start (+ name-end 1 (length location-prefix)))
             (location-end (position #\" text :start location-start)))
        (unless (and location-end (> location-end location-start) (cx-at-p text (format nil ">~%") (1+ location-end)))
          (return-from parse-skill-block nil))
        (let ((name (subseq text name-start name-end))
              (location (subseq text location-start location-end))
              (body-start (+ location-end 3))
              (offset 0))
          (loop
            (let ((at (search closing text :start2 (+ body-start offset))))
              (unless at (return nil))
              (let ((after (+ at (length closing))))
                (cond ((= after (length text)) (return (make-parsed-skill-block name location (subseq text body-start at) nil)))
                      ((and (cx-at-p text (format nil "~%~%") after) (> (- (length text) after) 2))
                       (let ((message (cx-js-trim (subseq text (+ after 2)))))
                         (return (make-parsed-skill-block name location (subseq text body-start at) (and (plusp (length message)) message)))))
                      (t (setf offset (- (1+ at) body-start))))))))))))

(defun cx-escape-xml (value)
  (let ((text (if (stringp value) value "")))
    (with-output-to-string (out)
      (loop for c across text
            do (case c (#\& (write-string "&amp;" out)) (#\< (write-string "&lt;" out)) (#\> (write-string "&gt;" out))
                 (#\" (write-string "&quot;" out)) (#\' (write-string "&apos;" out)) (t (write-char c out)))))))

(defun format-skills-for-prompt (skills &optional (file-read-tool "read"))
  "Pi's formatSkillsForPrompt over the skill objects (the skills of the system prompt options): the XML block that lists the skills a model may load, without those that disable model invocation. FILE-READ-TOOL names the tool that loads a skill's file: \"read\" (Pi's default), \"bash\", or any other value for a reader reachable only through another tool."
  (let ((visible (remove-if (lambda (skill) (cx-truthy (cx-get skill "disableModelInvocation"))) (coerce skills 'list))))
    (when (null visible) (return-from format-skills-for-prompt ""))
    (format nil "~{~A~^~%~}"
            (append
             (list (format nil "~%~%The following skills provide specialized instructions for specific tasks.")
                   (cond ((equal file-read-tool "read") "Use the read tool to load a skill's file when the task matches its description.")
                         ((equal file-read-tool "bash") "Use bash to load a skill's file when the task matches its description.")
                         (t "Load a skill's file when the task matches its description."))
                   "When a skill file references a relative path, resolve it against the skill directory (parent of SKILL.md / dirname of the path) and use that absolute path in tool commands."
                   ""
                   "<available_skills>")
             (loop for skill in visible
                   append (list "  <skill>"
                                (format nil "    <name>~A</name>" (cx-escape-xml (cx-get skill "name")))
                                (format nil "    <description>~A</description>" (cx-escape-xml (cx-get skill "description")))
                                (format nil "    <location>~A</location>" (cx-escape-xml (cx-get skill "filePath")))
                                "  </skill>"))
             (list "</available_skills>")))))

;;; ---------------------------------------------------------------- image dimension note

(defstruct (resized-image (:constructor make-resized-image (&key original-width original-height width height was-resized (data "") (mime-type ""))))
  "Pi's ResizedImage: dimensions (JavaScript numbers), and the image as base64 DATA with its MIME-TYPE. format-dimension-note reads only the dimensions."
  original-width original-height width height was-resized data mime-type)

(defun cx-to-fixed-2 (value)
  "Number.prototype.toFixed(2): the exact decimal value rounded half up in magnitude, \"-\" for any negative value, Number::toString from 1e21 on."
  (cond ((cx-nan-p value) "NaN")
        ((or (cx-infinity-p value) (>= (abs value) 1d21)) (cx-js-number-string value))
        (t (let ((scaled (floor (+ (* (abs (rational value)) 100) 1/2))))
             (format nil "~:[~;-~]~D.~2,'0D" (minusp value) (floor scaled 100) (mod scaled 100))))))

(defun format-dimension-note (result)
  "Pi's formatDimensionNote: the note that tells the model how a resized image's coordinates map to the original, or NIL when the image was not resized."
  (when (resized-image-was-resized result)
    (let ((scale (cx-js-math (/ (coerce (resized-image-original-width result) 'double-float)
                                (coerce (resized-image-width result) 'double-float)))))
      (format nil "[Image: original ~Ax~A, displayed at ~Ax~A. Multiply coordinates by ~A to map to original image.]"
              (cx-js-number-string (resized-image-original-width result)) (cx-js-number-string (resized-image-original-height result))
              (cx-js-number-string (resized-image-width result)) (cx-js-number-string (resized-image-height result))
              (cx-to-fixed-2 scale)))))

;;; ---------------------------------------------------------------- host helpers

(defun cx-host (method args)
  "The result object of the host call METHOD made outside a handler."
  (or (helper-call method args) (obj)))

(defstruct (parsed-frontmatter (:constructor make-parsed-frontmatter (frontmatter body)))
  "Pi's { frontmatter, body }: parse(yaml) ?? {} (an object for the usual header, any other YAML document as itself) and the body."
  frontmatter body)

(defun parse-frontmatter (content)
  "Pi's parseFrontmatter, answered by the host's YAML parser. A header it refuses signals host-call-error, as Pi throws. May be called before the extension runs."
  (let ((result (cx-host "frontmatter.parse" (obj "content" content))))
    (make-parsed-frontmatter (cx-get result "frontmatter") (let ((body (cx-get result "body"))) (if body body "")))))

(defun strip-frontmatter (content)
  "Pi's stripFrontmatter: the body of parse-frontmatter."
  (parsed-frontmatter-body (parse-frontmatter content)))

(defun cx-parse-iso-date (text)
  "Date.parse of the text toISOString writes for a four-digit year (YYYY-MM-DDTHH:mm:ss.sssZ, every field in range), which ECMA-262 defines: the time value, or NIL when the host must decide."
  (when (and (= (length text) 24)
             (loop for c across text for i from 0
                   always (case i ((4 7) (char= c #\-)) (10 (char= c #\T)) ((13 16) (char= c #\:)) (19 (char= c #\.)) (23 (char= c #\Z))
                            (t (char<= #\0 c #\9)))))
    (flet ((field (start end) (parse-integer text :start start :end end)))
      (let* ((year (field 0 4)) (month (field 5 7)) (day (field 8 10)) (hour (field 11 13)) (minute (field 14 16)) (second (field 17 19)) (ms (field 20 23))
             (leap (and (zerop (mod year 4)) (or (plusp (mod year 100)) (zerop (mod year 400)))))
             (days-in-month (cond ((= month 2) (if leap 29 28)) ((member month '(4 6 9 11)) 30) (t 31))))
        (when (and (<= 1 month 12) (<= 1 day days-in-month) (< hour 24) (< minute 60) (< second 60))
          (let* ((shifted (if (<= month 2) (1- year) year))
                 (era (floor shifted 400))
                 (year-of-era (- shifted (* era 400)))
                 (day-of-year (+ (floor (+ (* 153 (mod (+ month 9) 12)) 2) 5) (1- day)))
                 (days (+ (* era 146097) (* year-of-era 365) (floor year-of-era 4) (- (floor year-of-era 100)) day-of-year -719468)))
            (+ (* (+ (* (+ (* days 24) hour) 60) minute) 60000) (* second 1000) ms)))))))

(defun parse-date (text)
  "Date.parse(TEXT), V8's parser answered by the host: the time value in milliseconds, NIL when it is NaN. Text in the form toISOString writes is answered without the host."
  (or (cx-parse-iso-date text)
      (cx-get (cx-host "date.parse" (obj "text" text)) "ms")))

(defstruct (diff-string (:constructor make-diff-string (diff first-changed-line)))
  "The result of generate-diff-string: Pi's { diff, firstChangedLine }; FIRST-CHANGED-LINE is NIL when the texts do not differ."
  diff first-changed-line)

(defun cx-diff-args (old-content new-content context-lines)
  (let ((args (obj "oldContent" old-content "newContent" new-content)))
    (when context-lines (setf (gethash "contextLines" args) context-lines))
    args))

(defun generate-diff-string (old-content new-content &optional context-lines)
  "Pi's generateDiffString: the line-numbered display diff of two texts with the context around each change cut to CONTEXT-LINES (default 4) lines, and the first changed line of the new text. The host's port answers."
  (let ((result (cx-host "diff.string" (cx-diff-args old-content new-content context-lines))))
    (make-diff-string (let ((diff (cx-get result "diff"))) (if diff diff "")) (cx-get result "firstChangedLine"))))

(defun generate-unified-patch (path old-content new-content &optional context-lines)
  "Pi's generateUnifiedPatch: a standard unified patch of two texts for PATH with CONTEXT-LINES (default 4) lines of context around each change. The host's port of jsdiff answers."
  (let* ((args (cx-diff-args old-content new-content context-lines)))
    (setf (gethash "path" args) path)
    (let ((patch (cx-get (cx-host "diff.patch" args) "patch"))) (if patch patch ""))))

(defun copy-to-clipboard (text)
  "Pi's copyToClipboard: the host copies TEXT; a failed copy signals host-call-error."
  (cx-host "clipboard.copy" (obj "text" text))
  nil)

(defstruct (converted-image (:constructor make-converted-image (data mime-type)))
  "The result of convert-to-png: Pi's { data, mimeType }."
  data mime-type)

(defun cx-text-member (object key) (let ((v (cx-get object key))) (if (stringp v) v "")))

(defun convert-to-png (base64-data mime-type)
  "Pi's convertToPng: base64 image data as base64 PNG data, which a PNG already is; NIL when the data does not decode."
  (let ((image (cx-get (cx-host "image.convertToPng" (obj "data" base64-data "mimeType" mime-type)) "image")))
    (and (hash-table-p image) (make-converted-image (cx-text-member image "data") (cx-text-member image "mimeType")))))

(defstruct (image-resize-options (:constructor make-image-resize-options (&key max-width max-height max-bytes jpeg-quality)))
  "Pi's ImageResizeOptions; NIL takes Pi's default."
  max-width max-height max-bytes jpeg-quality)

(defun cx-base64 (octets)
  (let ((alphabet "ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz0123456789+/")
        (size (length octets)))
    (with-output-to-string (out)
      (loop for i from 0 below size by 3
            do (let* ((b0 (aref octets i))
                      (b1 (if (< (+ i 1) size) (aref octets (+ i 1)) 0))
                      (b2 (if (< (+ i 2) size) (aref octets (+ i 2)) 0))
                      (n (logior (ash b0 16) (ash b1 8) b2)))
                 (write-char (char alphabet (ldb (byte 6 18) n)) out)
                 (write-char (char alphabet (ldb (byte 6 12) n)) out)
                 (write-char (if (< (+ i 1) size) (char alphabet (ldb (byte 6 6) n)) #\=) out)
                 (write-char (if (< (+ i 2) size) (char alphabet (ldb (byte 6 0) n)) #\=) out))))))

(defun resize-image (input-bytes mime-type &optional options)
  "Pi's resizeImage: the image INPUT-BYTES (an octet vector) fitted within the OPTIONS limits (2000 pixels on a side, 4.5 MiB encoded by default), as the host's image code does it; NIL when it cannot be made to fit. An image already within every limit comes back as it was."
  (let ((args (obj "data" (cx-base64 input-bytes) "mimeType" mime-type)))
    (when options
      (let ((wire (obj)))
        (loop for (key value) in (list (list "maxWidth" (image-resize-options-max-width options)) (list "maxHeight" (image-resize-options-max-height options))
                                       (list "maxBytes" (image-resize-options-max-bytes options)) (list "jpegQuality" (image-resize-options-jpeg-quality options)))
              when value do (setf (gethash key wire) value))
        (setf (gethash "options" args) wire)))
    (let ((image (cx-get (cx-host "image.resize" args) "image")))
      (and (hash-table-p image)
           (flet ((dimension (key) (let ((v (cx-get image key))) (if v v *cx-nan*))))
             (make-resized-image :original-width (dimension "originalWidth") :original-height (dimension "originalHeight")
                                 :width (dimension "width") :height (dimension "height")
                                 :was-resized (cx-truthy (cx-get image "wasResized"))
                                 :data (cx-text-member image "data") :mime-type (cx-text-member image "mimeType")))))))

;;; ---------------------------------------------------------------- stored credential

(defun cx-normalize-path (path)
  "The part of Pi's normalizePath a credential path can use: a leading ~ is the home directory and a file:// URL is its path."
  (cond ((or (string= path "~") (cx-at-p path "~/" 0))
         (concatenate 'string (string-right-trim "/" (or (sb-posix:getenv "HOME") "")) "/" (if (> (length path) 2) (subseq path 2) "")))
        ((cx-at-p path "file://" 0) (subseq path 7))
        (t path)))

(defun read-stored-credential (provider-id &optional auth-path)
  "Pi's readStoredCredential: a one-off synchronous read of the credential stored for PROVIDER-ID in an auth.json file, without resolving configured key values. AUTH-PATH defaults to auth.json in the agent directory; a leading ~ in it is the home directory and a file:// URL is its path. A file that cannot be read or is not a JSON object, and a provider it has no credential for, give NIL. The credential keeps the key order of the file. Signals host-info-unavailable when there is no AUTH-PATH and the host gave no agent directory."
  (let ((path (or auth-path (concatenate 'string (string-right-trim "/" (host-agent-dir)) "/auth.json"))))
    (handler-case
        (let* ((octets (with-open-file (in (cx-normalize-path path) :element-type '(unsigned-byte 8))
                         (let ((data (make-array (file-length in) :element-type '(unsigned-byte 8))))
                           (subseq data 0 (read-sequence data in)))))
               (bom (and (>= (length octets) 3) (= (aref octets 0) #xEF) (= (aref octets 1) #xBB) (= (aref octets 2) #xBF)))
               (data (decode-json octets :start (if bom 3 0))))
          (cx-get data provider-id))
      (error () nil))))

;;; ---------------------------------------------------------------- file mutation queue

(defun cx-abspath (path)
  "os.path.abspath: PATH against the working directory, with . and .. and repeated separators resolved (no symlinks)."
  (let* ((full (if (cx-at-p path "/" 0) path (concatenate 'string (string-right-trim "/" (sb-posix:getcwd)) "/" path)))
         (initial (cond ((and (cx-at-p full "//" 0) (not (cx-at-p full "///" 0))) "//") (t "/")))
         (parts '()))
    (loop with start = 0
          for slash = (position #\/ full :start start)
          do (let ((part (subseq full start slash)))
               (cond ((or (string= part "") (string= part ".")))
                     ((string= part "..") (pop parts))
                     (t (push part parts))))
             (if slash (setf start (1+ slash)) (return)))
    (format nil "~A~{~A~^/~}" initial (reverse parts))))

(defun with-file-mutation-queue (file-path function)
  "Pi's withFileMutationQueue: calls FUNCTION (no arguments) once every mutation registered earlier for the same file has finished, then lets the next one run, and returns its value. Mutations of different files run in parallel. The file is keyed by its real path (a symlink and its target share a queue) and callers run in the order they called. The queue is the host's: it serializes this extension's mutations with the host's edit and write tools and with every other extension process. A path the host cannot resolve signals host-call-error without calling FUNCTION. May be called before the extension runs."
  (let ((ticket (cx-get (cx-host "fileMutation.enter" (obj "path" (cx-abspath file-path))) "ticket")))
    (unless (cx-truthy ticket) (error "fileMutation.enter returned no ticket"))
    (unwind-protect (funcall function)
      (handler-case (cx-host "fileMutation.exit" (obj "ticket" ticket))
        (host-call-error () (ignore-errors (cx-host "fileMutation.exit" (obj "ticket" ticket))))))))
