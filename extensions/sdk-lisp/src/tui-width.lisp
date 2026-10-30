;;;; SPDX-License-Identifier: MIT
;;;; pi-tui's utils.ts width half: Unicode properties, grapheme clusters (UAX #29 as ICU's Intl.Segmenter), terminal width, ANSI stripping.
;;;; Strings are code point vectors; the data is tui-tables.lisp (generated from Pi's width tables).

(in-package #:pig)

(defconstant +tui-esc+ (code-char 27))

(eval-when (:compile-toplevel :load-toplevel :execute)
  (defun tui-esc (rest)
    "ESC followed by REST."
    (concatenate 'string (string (code-char 27)) rest)))

(defmacro tesc (rest)
  "The literal string ESC followed by the string REST."
  (tui-esc rest))

(defmacro tescc (code)
  "The literal string ESC followed by the character with code CODE."
  (tui-esc (string (code-char code))))

(defmacro tstr (&rest codes)
  "The literal string of the characters with the codes CODES."
  (map 'string #'code-char codes))

(defconstant +tui-w2+ 1)
(defconstant +tui-zw+ 2)
(defconstant +tui-np+ 4)
(defconstant +tui-mark+ 8)
(defconstant +tui-tsm+ 16)
(defconstant +tui-rgi1+ 32)
(defconstant +tui-ext-pict+ 64)
(defconstant +tui-cjk+ 128)
(defconstant +tui-js-space+ 16384)

(defun tui-props (code)
  "The property bits of code point CODE."
  (declare (fixnum code))
  (let ((highs *tui-range-highs*) (lo 0) (hi (1- (length *tui-range-highs*))))
    (declare (simple-vector highs) (fixnum lo hi))
    (loop while (< lo hi)
          do (let ((mid (ash (+ lo hi) -1)))
               (if (< (the fixnum (svref highs mid)) code) (setf lo (1+ mid)) (setf hi mid))))
    (the fixnum (svref *tui-range-props* lo))))

(declaim (inline tui-gcb tui-incb tui-prop-at))
(defun tui-gcb (props) (logand (ash props -8) 15))
(defun tui-incb (props) (logand (ash props -12) 3))
(defun tui-prop-at (text i) (tui-props (char-code (char text i))))

(defun tui-js-space-p (char) (logtest (tui-props (char-code char)) +tui-js-space+))

(defun tui-trim-end (text)
  (let ((end (length text)))
    (loop while (and (> end 0) (tui-js-space-p (char text (1- end)))) do (decf end))
    (if (= end (length text)) text (subseq text 0 end))))

(defun tui-trim-start (text)
  (let ((start 0))
    (loop while (and (< start (length text)) (tui-js-space-p (char text start))) do (incf start))
    (if (= start 0) text (subseq text start))))

(defun tui-u16-length (text &optional (start 0) (end (length text)))
  (loop for i from start below end count t into n
        count (> (char-code (char text i)) #xFFFF) into extra
        finally (return (+ n extra))))

(defun tui-map-graphemes (function text start end)
  "Call FUNCTION with the bounds of each extended grapheme cluster of TEXT between START and END."
  (declare (simple-string text) (fixnum start end) (function function))
  (let ((i start))
    (declare (fixnum i))
    (loop while (< i end)
          do (let* ((s i) (p (tui-prop-at text i)) (prev (tui-gcb p))
                    (incb-state (if (= (tui-incb p) 1) 1 0))
                    (emoji-state (if (logtest p +tui-ext-pict+) 1 0))
                    (ri-count (if (= prev 6) 1 0)))
               (declare (fixnum s prev incb-state emoji-state ri-count))
               (incf i)
               (loop while (< i end)
                     do (let* ((q (tui-prop-at text i)) (cur (tui-gcb q)))
                          (when (cond ((and (= prev 1) (= cur 2)) nil)
                                      ((member prev '(1 2 3)) t)
                                      ((member cur '(1 2 3)) t)
                                      ((and (= prev 9) (member cur '(9 10 12 13))) nil)
                                      ((and (member prev '(12 10)) (member cur '(10 11))) nil)
                                      ((and (member prev '(13 11)) (= cur 11)) nil)
                                      ((member cur '(4 5)) nil)
                                      ((= cur 8) nil)
                                      ((= prev 7) nil)
                                      ((and (= incb-state 2) (= (tui-incb q) 1)) nil)
                                      ((and (= emoji-state 2) (logtest q +tui-ext-pict+)) nil)
                                      ((and (= prev 6) (= cur 6) (oddp ri-count)) nil)
                                      (t t))
                            (return))
                          (case (tui-incb q)
                            (1 (setf incb-state 1))
                            (3 (when (>= incb-state 1) (setf incb-state 2)))
                            (2 nil)
                            (t (setf incb-state 0)))
                          (cond ((logtest q +tui-ext-pict+) (setf emoji-state 1))
                                ((and (= cur 4) (= emoji-state 1)))
                                ((and (= cur 5) (= emoji-state 1)) (setf emoji-state 2))
                                (t (setf emoji-state 0)))
                          (setf ri-count (if (= cur 6) (1+ ri-count) 0) prev cur)
                          (incf i)))
               (funcall function s i)))))

(defmacro do-tui-graphemes ((start end text &optional (from 0) to) &body body)
  "Run BODY with START and END bound to each grapheme cluster of TEXT (between FROM and TO); RETURN leaves the loop."
  (let ((text-var (gensym "TEXT")))
    `(let ((,text-var ,text))
       (block nil
         (tui-map-graphemes (lambda (,start ,end) (declare (fixnum ,start ,end)) ,@body)
                            ,text-var ,from (or ,to (length ,text-var)))))))

(defvar *tui-rgi-table* nil)

(defun tui-rgi-table ()
  (or *tui-rgi-table*
      (setf *tui-rgi-table*
            (let ((table (make-hash-table :test 'equal)) (text *tui-rgi-text*) (start 0))
              (loop
                (let* ((end (or (position #\; text :start start) (length text))) (chars '()) (code 0) (any nil))
                  (loop for k from start to end
                        do (let ((c (if (< k end) (char text k) #\Space)))
                             (if (char= c #\Space)
                                 (when any (push (code-char code) chars) (setf code 0 any nil))
                                 (setf code (+ (* code 16) (digit-char-p c 16)) any t))))
                  (setf (gethash (coerce (nreverse chars) 'simple-string) table) t)
                  (when (>= end (length text)) (return))
                  (setf start (1+ end))))
              table))))

(defun tui-could-be-emoji-p (text start end)
  (let ((cp (char-code (char text start))))
    (or (<= #x1F000 cp #x1FBFF) (<= #x2300 cp #x23FF) (<= #x2600 cp #x27BF) (<= #x2B50 cp #x2B55)
        (find (code-char #xFE0F) text :start start :end end)
        (> (tui-u16-length text start end) 2))))

(defun tui-rgi-emoji-p (text start end)
  (if (= (- end start) 1)
      (logtest (tui-prop-at text start) +tui-rgi1+)
      (gethash (subseq text start end) (tui-rgi-table))))

(defun tui-east-asian-width (code) (if (logtest (tui-props code) +tui-w2+) 2 1))

(defun tui-grapheme-width (text start end)
  "The terminal columns of the grapheme cluster TEXT[START, END)."
  (declare (simple-string text) (fixnum start end))
  (let ((n (- end start)))
    (when (= n 1)
      (let ((code (char-code (char text start))))
        (cond ((<= #x20 code #x7E) (return-from tui-grapheme-width 1))
              ((= code 9) (return-from tui-grapheme-width 3)))))
    (flet ((flags (k) (tui-prop-at text k)))
      (when (loop for k from start below end always (logtest (flags k) +tui-tsm+))
        (return-from tui-grapheme-width n))
      (when (loop for k from start below end always (logtest (flags k) +tui-zw+))
        (return-from tui-grapheme-width 0))
      (when (and (tui-could-be-emoji-p text start end) (tui-rgi-emoji-p text start end))
        (return-from tui-grapheme-width 2))
      (let ((first start))
        (loop while (and (< first end) (logtest (flags first) +tui-np+)) do (incf first))
        (when (= first end) (return-from tui-grapheme-width 0))
        (let ((cp (char-code (char text first))))
          (when (<= #x1F1E6 cp #x1F1FF) (return-from tui-grapheme-width 2))
          (let ((width (tui-east-asian-width cp)) (follows-mark nil))
            (loop for k from (1+ first) below end
                  do (let ((c (char-code (char text k))) (f (flags k)))
                       (cond ((logtest f +tui-tsm+) (incf width) (setf follows-mark nil))
                             ((logtest f +tui-mark+) (setf follows-mark t))
                             ((not (logtest f +tui-np+))
                              (cond ((or follows-mark (<= #xFF00 c #xFFEF)) (incf width (tui-east-asian-width c)))
                                    ((or (= c #x0E33) (= c #x0EB3)) (incf width)))
                              (setf follows-mark nil)))))
            width))))))

(defun tui-ansi-length (text pos)
  "The length of the ANSI, OSC or APC escape sequence of TEXT starting at POS, or 0 when there is none."
  (declare (simple-string text) (fixnum pos))
  (let ((n (length text)))
    (if (or (>= pos n) (char/= (char text pos) +tui-esc+) (>= (1+ pos) n))
        0
        (let ((next (char text (1+ pos))))
          (cond ((char= next #\[)
                 (let ((end (position-if (lambda (c) (find c "mGKHJ")) text :start (+ pos 2))))
                   (if end (- (1+ end) pos) 0)))
                ((or (char= next #\]) (char= next #\_))
                 (loop for i from (+ pos 2) below n
                       do (let ((c (char text i)))
                            (cond ((= (char-code c) 7) (return (- (1+ i) pos)))
                                  ((and (char= c +tui-esc+) (< (1+ i) n) (char= (char text (1+ i)) #\\))
                                   (return (- (+ i 2) pos)))))
                       finally (return 0)))
                (t 0))))))

(defun tui-text-end (line i)
  "The index of the next ANSI sequence of LINE at or after I, or its length."
  (let ((end i) (n (length line)))
    (loop while (and (< end n) (zerop (tui-ansi-length line end))) do (incf end))
    end))

(defun tui-printable-ascii-p (text)
  (every (lambda (c) (<= #x20 (char-code c) #x7E)) text))

(defun tui-ascii-visible-width (text)
  (declare (simple-string text))
  (let ((width 0) (i 0) (n (length text)))
    (declare (fixnum width i n))
    (loop while (< i n)
          do (let ((code (char-code (char text i))))
               (cond ((<= #x20 code #x7E) (incf width) (incf i))
                     ((= code 9) (incf width 3) (incf i))
                     ((= code 27) (let ((len (tui-ansi-length text i)))
                                    (when (zerop len) (return-from tui-ascii-visible-width -1))
                                    (incf i len)))
                     (t (return-from tui-ascii-visible-width -1)))))
    width))

(defun tui-simple (text) (if (simple-string-p text) text (coerce text 'simple-string)))

(defun tui-strip-ansi-codes (text)
  (with-output-to-string (out)
    (let ((i 0) (n (length text)))
      (loop while (< i n)
            do (let ((len (tui-ansi-length text i)))
                 (if (plusp len) (incf i len) (progn (write-char (char text i) out) (incf i))))))))

(defun tui-visible-width (text)
  "The terminal columns of TEXT, ignoring ANSI, OSC and APC sequences (pi-tui's visibleWidth)."
  (let ((text (tui-simple text)))
    (if (zerop (length text))
        0
        (let ((ascii (tui-ascii-visible-width text)))
          (if (/= ascii -1)
              ascii
              (let ((clean (if (find #\Tab text)
                               (with-output-to-string (out)
                                 (loop for c across text do (if (char= c #\Tab) (write-string "   " out) (write-char c out))))
                               text)))
                (when (find +tui-esc+ clean) (setf clean (tui-strip-ansi-codes clean)))
                (let ((clean (tui-simple clean)) (total 0))
                  (do-tui-graphemes (s e clean) (incf total (tui-grapheme-width clean s e)))
                  total)))))))

(defun tui-strip-terminal-sequences (text)
  "TEXT without its ANSI, OSC and APC control sequences (pi-tui's stripTerminalSequences)."
  (if (find +tui-esc+ text) (tui-strip-ansi-codes (tui-simple text)) text))
