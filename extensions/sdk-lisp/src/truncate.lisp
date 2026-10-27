;;;; SPDX-License-Identifier: MIT
;;;; Lane C: output truncation helpers (core/tools/truncate.ts) and truncateToVisualLines. Two independent limits, a line count and a UTF-8 byte count; the first hit wins.

(in-package #:pig)

(defconstant +default-max-lines+ 2000)
(defconstant +default-max-bytes+ (* 50 1024))
(defconstant +grep-max-line-length+ 500)

(defstruct (truncation-result (:constructor make-truncation-result
                                  (content truncated truncated-by total-lines total-bytes output-lines output-bytes
                                   last-line-partial first-line-exceeds-limit max-lines max-bytes)))
  "Pi's TruncationResult. TRUNCATED-BY is \"lines\", \"bytes\" or NIL (Pi's null)."
  content truncated truncated-by total-lines total-bytes output-lines output-bytes
  last-line-partial first-line-exceeds-limit max-lines max-bytes)

(defun cx-count-lines (content)
  (if (string= content "")
      (vector)
      (let* ((lines (loop with start = 0
                          for newline = (position #\Newline content :start start)
                          collect (subseq content start newline)
                          while newline do (setf start (1+ newline)))))
        (when (char= (char content (1- (length content))) #\Newline) (setf lines (butlast lines)))
        (coerce lines 'simple-vector))))

(defun format-size (size)
  "Pi's formatSize: \"512B\", \"1.5KB\" or \"2.0MB\", the one decimal rounded half up like toFixed."
  (flet ((tenths (unit)
           (let ((scaled (floor (+ (* size 10) (floor unit 2)) unit)))
             (format nil "~D.~D" (floor scaled 10) (mod scaled 10)))))
    (cond ((< size 1024) (format nil "~DB" size))
          ((< size (* 1024 1024)) (concatenate 'string (tenths 1024) "KB"))
          (t (concatenate 'string (tenths (* 1024 1024)) "MB")))))

(defun cx-unchanged (content lines max-lines max-bytes total-bytes)
  (make-truncation-result content nil nil (length lines) total-bytes (length lines) total-bytes nil nil max-lines max-bytes))

(defun truncate-head (content &key max-lines max-bytes)
  "Pi's truncateHead: keeps the first lines and bytes of CONTENT, never a partial line; when the first line alone exceeds the byte limit the content is empty and first-line-exceeds-limit is set. An omitted limit takes its default; 0 is used as given."
  (let* ((max-lines (or max-lines +default-max-lines+))
         (max-bytes (or max-bytes +default-max-bytes+))
         (total-bytes (cx-utf8-length content))
         (lines (cx-count-lines content))
         (total-lines (length lines)))
    (when (and (<= total-lines max-lines) (<= total-bytes max-bytes))
      (return-from truncate-head (cx-unchanged content lines max-lines max-bytes total-bytes)))
    (when (> (cx-utf8-length (svref lines 0)) max-bytes)
      (return-from truncate-head (make-truncation-result "" t "bytes" total-lines total-bytes 0 0 nil t max-lines max-bytes)))
    (let ((kept '()) (count 0) (output-bytes 0) (truncated-by "lines"))
      (loop for i from 0 below (min total-lines (max max-lines 0))
            do (let ((line-bytes (+ (cx-utf8-length (svref lines i)) (if (> i 0) 1 0))))
                 (when (> (+ output-bytes line-bytes) max-bytes) (setf truncated-by "bytes") (return))
                 (push (svref lines i) kept) (incf count) (incf output-bytes line-bytes)))
      (when (and (>= count max-lines) (<= output-bytes max-bytes)) (setf truncated-by "lines"))
      (let ((output (format nil "~{~A~^~%~}" (reverse kept))))
        (make-truncation-result output t truncated-by total-lines total-bytes count (cx-utf8-length output) nil nil max-lines max-bytes)))))

(defun cx-tail-of-bytes (text max-bytes)
  "The last bytes of TEXT's UTF-8 that fit MAX-BYTES, from a character boundary."
  (let* ((data (cx-utf8-bytes text)) (size (length data)))
    (if (<= size max-bytes)
        text
        (let ((start (max (- size max-bytes) 0)))
          (loop while (and (< start size) (= (logand (aref data start) #xC0) #x80)) do (incf start))
          (sb-ext:octets-to-string data :start (min start size) :external-format :utf-8)))))

(defun truncate-tail (content &key max-lines max-bytes)
  "Pi's truncateTail: keeps the last lines and bytes of CONTENT (command output). When the last line alone exceeds the byte limit it keeps the end of that line and sets last-line-partial."
  (let* ((max-lines (or max-lines +default-max-lines+))
         (max-bytes (or max-bytes +default-max-bytes+))
         (total-bytes (cx-utf8-length content))
         (lines (cx-count-lines content))
         (total-lines (length lines)))
    (when (and (<= total-lines max-lines) (<= total-bytes max-bytes))
      (return-from truncate-tail (cx-unchanged content lines max-lines max-bytes total-bytes)))
    (let ((kept '()) (count 0) (output-bytes 0) (truncated-by "lines") (partial nil))
      (loop for i downfrom (1- total-lines)
            while (and (>= i 0) (< count max-lines))
            do (let ((line-bytes (+ (cx-utf8-length (svref lines i)) (if kept 1 0))))
                 (when (> (+ output-bytes line-bytes) max-bytes)
                   (setf truncated-by "bytes")
                   (unless kept
                     (let ((text (cx-tail-of-bytes (svref lines i) max-bytes)))
                       (push text kept) (incf count) (setf output-bytes (cx-utf8-length text) partial t)))
                   (return))
                 (push (svref lines i) kept) (incf count) (incf output-bytes line-bytes)))
      (when (and (>= count max-lines) (<= output-bytes max-bytes)) (setf truncated-by "lines"))
      (let ((output (format nil "~{~A~^~%~}" kept)))
        (make-truncation-result output t truncated-by total-lines total-bytes count (cx-utf8-length output) partial nil max-lines max-bytes)))))

(defun truncate-line (line &optional (max-chars +grep-max-line-length+))
  "Pi's truncateLine: (values text truncated-p). A line longer than MAX-CHARS UTF-16 units is cut to that many units plus \"... [truncated]\". A cut inside a surrogate pair keeps the lone surrogate, and a negative MAX-CHARS counts back from the end, as JavaScript's slice does."
  (let* ((units (cx-utf16-units line)) (size (length units)))
    (if (<= size max-chars)
        (values line nil)
        (let ((end (if (minusp max-chars) (max 0 (+ size max-chars)) max-chars)))
          (values (concatenate 'string (cx-units-string units 0 end) "... [truncated]") t)))))

;;; ---------------------------------------------------------------- truncateToVisualLines

(defstruct (visual-truncate-result (:constructor make-visual-truncate-result (visual-lines skipped-count)))
  "pi-coding-agent's VisualTruncateResult: the visual lines kept (a vector of strings) and how many were dropped."
  visual-lines skipped-count)

(defun cx-render-plain-text (text padding-x width)
  "pi-tui's Text(text, paddingX, 0).render(width): tabs as three spaces, the text wrapped in the width less the padding on both sides (the padding shrinks to fit a narrow width), every line padded to the width."
  (when (string= (cx-js-trim text) "") (return-from cx-render-plain-text '()))
  (let* ((normalized (with-output-to-string (out)
                       (loop for c across text do (if (char= c #\Tab) (write-string "   " out) (write-char c out)))))
         (padding-x (min padding-x (max 0 (floor (1- width) 2))))
         (content-width (max 1 (- width (* padding-x 2))))
         (margin (make-string (max 0 padding-x) :initial-element #\Space))
         (lines (loop for line across (tui-wrap-text-with-ansi normalized content-width)
                      collect (let ((with-margins (concatenate 'string margin line margin)))
                                (concatenate 'string with-margins
                                             (make-string (max 0 (- width (tui-visible-width with-margins))) :initial-element #\Space))))))
    (or lines (list ""))))

(defun cx-slice (lines start end)
  "Array.prototype.slice(START, END) of the list LINES; END NIL is the end."
  (let* ((n (length lines))
         (resolve (lambda (index) (if (minusp index) (max 0 (+ n index)) (min index n))))
         (from (funcall resolve start))
         (to (if end (funcall resolve end) n)))
    (if (>= from to) '() (subseq lines from to))))

(defun truncate-to-visual-lines (text max-visual-lines width &key (padding-x 0) (keep "end"))
  "pi-coding-agent's truncateToVisualLines: TEXT wrapped at WIDTH as a Text with horizontal padding PADDING-X renders it (Pi's default 0 suits a result placed in a Box; use 1 for a plain Container), cut to at most MAX-VISUAL-LINES visual lines, from the end unless KEEP is \"start\"."
  (when (string= text "") (return-from truncate-to-visual-lines (make-visual-truncate-result (vector) 0)))
  (let ((all (cx-render-plain-text text padding-x width)))
    (if (<= (length all) max-visual-lines)
        (make-visual-truncate-result (coerce all 'simple-vector) 0)
        (make-visual-truncate-result
         (coerce (if (equal keep "start") (cx-slice all 0 max-visual-lines) (cx-slice all (- max-visual-lines) nil)) 'simple-vector)
         (- (length all) max-visual-lines)))))
