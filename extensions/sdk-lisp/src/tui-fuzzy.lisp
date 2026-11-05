;;;; SPDX-License-Identifier: MIT
;;;; pi-tui's fuzzy.ts: ordered-subsequence matching with Pi's scores (lower is better), over UTF-16 code units as JavaScript strings are, and
;;;; String.prototype.toLowerCase of Node's ICU. Python SDK: tui/_fuzzy.py.

(in-package #:pig)

(defvar *tf-lower-table* nil)

(defun tf-lower-table ()
  (or *tf-lower-table*
      (let ((table (make-hash-table)) (runs *tui-lower-runs*))
        (loop for k from 0 below (length runs) by 4
              do (let ((lo (svref runs k)) (hi (svref runs (+ k 1))) (stride (svref runs (+ k 2))) (delta (svref runs (+ k 3))))
                   (loop for cp from lo to hi by stride do (setf (gethash cp table) (string (code-char (+ cp delta)))))))
        (loop for (cp . codes) in *tui-lower-multi*
              do (setf (gethash cp table) (map 'string #'code-char codes)))
        (setf *tf-lower-table* table))))

(defun tui-js-lower (text)
  "String.prototype.toLowerCase: the Unicode of Node's ICU for every code point, Final_Sigma by SBCL's own rule."
  (if (every (lambda (c) (< (char-code c) 128)) text)
      (string-downcase text)
      (let* ((table (tf-lower-table)) (sigma (code-char #x3A3))
             (out (with-output-to-string (s)
                    (loop for c across text
                          do (let ((mapped (and (char/= c sigma) (gethash (char-code c) table))))
                               (if mapped (write-string mapped s) (write-char c s)))))))
        (if (not (find sigma text))
            out
            ;; Final_Sigma needs the cased/case-ignorable context: lowercase a copy whose other mapped characters are 1:1.
            (let* ((table (tf-lower-table))
                   (probe (sb-unicode:lowercase (map 'string (lambda (c) (if (and (char/= c sigma) (gethash (char-code c) table)) #\A c)) text))))
              (with-output-to-string (s)
                (loop for c across text for i from 0
                      do (cond ((char= c sigma) (write-char (char probe i) s))
                               ((gethash (char-code c) table) (write-string (gethash (char-code c) table) s))
                               (t (write-char c s))))))))))

(defun tf-units (text)
  (let ((units (make-array (tui-u16-length text) :element-type '(unsigned-byte 16))) (k 0))
    (loop for c across text
          do (let ((code (char-code c)))
               (cond ((> code #xFFFF)
                      (decf code #x10000)
                      (setf (aref units k) (+ #xD800 (ash code -10)) (aref units (1+ k)) (+ #xDC00 (logand code #x3FF)))
                      (incf k 2))
                     (t (setf (aref units k) code) (incf k)))))
    units))

(defstruct (tui-fuzzy-match (:constructor tf-result (matches score)))
  "pi-tui's FuzzyMatch: whether every query character appears in order, and the score (lower is better)."
  matches score)

(defun tf-boundary-p (unit)
  (or (find unit '(45 95 46 47 58))
      (and (not (<= #xD800 unit #xDFFF)) (tui-js-space-p (code-char unit)))))

(defstruct (tf-alignment (:conc-name tfa-)) (score 0d0) (at -1) (consecutive 0))

(defun tfa-add (alignment text i)
  "Pi's scorePositions, one matched text index at a time."
  (with-slots (score at consecutive) alignment
    (if (= at (1- i))
        (progn (incf consecutive) (decf score (* consecutive 5)))
        (progn (setf consecutive 0)
               (when (>= at 0) (incf score (* (- i at 1) 2)))))
    (when (or (= i 0) (tf-boundary-p (aref text (1- i)))) (decf score 10))
    (incf score (* i 0.1d0))
    (setf at i)))

(defun tf-match-query (query text)
  (cond
    ((zerop (length query)) (tf-result t 0))
    ((> (length query) (length text)) (tf-result nil 0))
    (t
     (let ((greedy (make-tf-alignment)) (start 0))
       (loop for unit across query
             do (let ((i (position unit text :start start)))
                  (unless i (return-from tf-match-query (tf-result nil 0)))
                  (tfa-add greedy text i)
                  (setf start (1+ i))))
       (let ((score (tfa-score greedy)))
         ;; The greedy alignment can scatter the query over an earlier, unrelated part of the text: also score every contiguous occurrence.
         (loop for found = (search query text) then (search query text :start2 (1+ found))
               while found
               do (let ((run (make-tf-alignment)))
                    (dotimes (k (length query)) (tfa-add run text (+ found k)))
                    (setf score (min score (tfa-score run)))))
         (when (equalp query text) (decf score 100))
         (tf-result t score))))))

(defun tf-swapped (query)
  "QUERY of letters then digits (or digits then letters) with the two runs swapped, or NIL."
  (let* ((split (position-if-not (lambda (c) (char<= #\a c #\z)) query)) (alt (position-if-not (lambda (c) (char<= #\0 c #\9)) query)))
    (cond ((and split (plusp split) (every (lambda (c) (char<= #\0 c #\9)) (subseq query split)))
           (tui-cat (subseq query split) (subseq query 0 split)))
          ((and alt (plusp alt) (every (lambda (c) (char<= #\a c #\z)) (subseq query alt)))
           (tui-cat (subseq query alt) (subseq query 0 alt))))))

(defun tui-fuzzy-match (query text)
  "Whether every character of QUERY appears in TEXT in order, and Pi's score, as a tui-fuzzy-match (pi-tui's fuzzyMatch)."
  (let* ((query-lower (tui-js-lower query)) (text-units (tf-units (tui-js-lower text)))
         (primary (tf-match-query (tf-units query-lower) text-units)))
    (if (tui-fuzzy-match-matches primary)
        primary
        (let* ((swapped (tf-swapped query-lower))
               (second (and swapped (tf-match-query (tf-units swapped) text-units))))
          (if (and second (tui-fuzzy-match-matches second))
              (tf-result t (+ (tui-fuzzy-match-score second) 5))
              primary)))))

(defun tui-fuzzy-filter (items query get-text)
  "The items (as a list) whose text, by GET-TEXT, matches every whitespace- or slash-separated token of QUERY, best first
(pi-tui's fuzzyFilter). A blank query returns the items unchanged."
  (let ((trimmed (tui-trim-start (tui-trim-end query))))
    (if (zerop (length trimmed))
        (coerce items 'list)
        (let ((tokens (loop with start = 0 with n = (length trimmed)
                            for end = (or (position-if (lambda (c) (or (char= c #\/) (tui-js-space-p c))) trimmed :start start) n)
                            when (> end start) collect (subseq trimmed start end)
                            do (setf start (1+ end))
                            while (< start n))))
          (if (null tokens)
              (coerce items 'list)
              (let ((results '()) (index 0))
                (map nil (lambda (item)
                           (let ((text (funcall get-text item)) (total 0) (all t))
                             (dolist (token tokens)
                               (let ((match (tui-fuzzy-match token text)))
                                 (if (tui-fuzzy-match-matches match) (incf total (tui-fuzzy-match-score match)) (progn (setf all nil) (return)))))
                             (when all (push (list total index item) results))
                             (incf index)))
                     items)
                (mapcar #'third (stable-sort (nreverse results) #'< :key #'first))))))))
