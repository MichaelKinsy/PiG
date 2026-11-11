;;;; SPDX-License-Identifier: MIT
;;;; JSON with the semantics of the wire's JavaScript peers (JSON.parse/stringify):
;;;;   number text is ECMAScript Number::toString, -0 writes 0, NaN/Infinity write null,
;;;;   lone UTF-16 surrogates travel as \udXXX escapes, object member order is kept.
;;;;
;;;; Lisp <-> JSON:  object = EQUAL hash-table (string keys, insertion order),
;;;;   array = simple-vector, string, number (integer text within +-2^53 reads as an
;;;;   integer, all other numbers as double-float), true = T, false = :FALSE, null = :NULL.
;;;;   When writing, NIL is null, a list is an array, a non-keyword symbol is an error.

(in-package #:pig)

(deftype octets () '(simple-array (unsigned-byte 8) (*)))

(defconstant +max-safe-integer+ 9007199254740992)
(defparameter *max-json-depth* 2000)

(defun json-fail (message &optional offset)
  (error 'json-error :message message :offset offset))

;;; ------------------------------------------------------------------ builders

(defun obj (&rest members)
  "A JSON object from alternating string keys and values, in that order."
  (let ((table (make-hash-table :test 'equal)))
    (loop for (key value) on members by #'cddr
          do (unless (stringp key) (error "obj: key ~S is not a string" key))
             (setf (gethash key table) value))
    table))

(defun arr (&rest items)
  "A JSON array."
  (coerce items 'simple-vector))

(defstruct (lazy-value (:constructor make-lazy-value (thunk)))
  "A member whose value is computed when read through JGET, JREF or the JSON writer (a before_agent_start event's systemPrompt)."
  thunk)

(declaim (inline force-lazy))
(defun force-lazy (value)
  (if (lazy-value-p value) (funcall (lazy-value-thunk value)) value))

(defun jget (object key &optional default)
  "Member KEY of a JSON object, or DEFAULT when absent. A JSON null reads as :NULL."
  (if (hash-table-p object)
      (force-lazy (gethash key object default))
      default))

(defun jref (value &rest path)
  "Follow PATH (string keys and integer indexes) into VALUE; NIL when a step is missing."
  (dolist (step path value)
    (setf value
          (cond ((and (stringp step) (hash-table-p value)) (force-lazy (gethash step value)))
                ((and (integerp step) (vectorp value) (not (stringp value)) (< -1 step (length value)))
                 (aref value step))
                (t (return nil))))))

;;; ------------------------------------------------------------------ reader

(defstruct (jreader (:conc-name jr-))
  (buf (make-array 0 :element-type '(unsigned-byte 8)) :type octets)
  (pos 0 :type fixnum)
  (end 0 :type fixnum)
  (depth 0 :type fixnum))

(declaim (inline jr-peek))
(defun jr-peek (r)
  (if (< (jr-pos r) (jr-end r)) (aref (jr-buf r) (jr-pos r)) -1))

(defun jr-skip-ws (r)
  (let ((buf (jr-buf r)) (end (jr-end r)))
    (loop while (and (< (jr-pos r) end)
                     (let ((b (aref buf (jr-pos r))))
                       (or (= b 32) (= b 9) (= b 10) (= b 13))))
          do (incf (jr-pos r)))))

(defun jr-expect-literal (r text value)
  (let ((buf (jr-buf r)) (pos (jr-pos r)))
    (when (> (+ pos (length text)) (jr-end r)) (json-fail "unexpected end of input" pos))
    (dotimes (i (length text))
      (unless (= (aref buf (+ pos i)) (char-code (char text i)))
        (json-fail (format nil "invalid literal, expected ~A" text) pos)))
    (incf (jr-pos r) (length text))
    value))

(defun jr-hex4 (r)
  (let ((buf (jr-buf r)) (pos (jr-pos r)) (code 0))
    (when (> (+ pos 4) (jr-end r)) (json-fail "truncated \\u escape" pos))
    (dotimes (i 4)
      (let ((digit (digit-char-p (code-char (aref buf (+ pos i))) 16)))
        (unless digit (json-fail "invalid \\u escape" pos))
        (setf code (+ (* code 16) digit))))
    (incf (jr-pos r) 4)
    code))

(defun jr-string (r)
  ;; The opening quote is at pos.
  (let* ((buf (jr-buf r)) (end (jr-end r)) (start (1+ (jr-pos r))) (i start) (simple t))
    (declare (type fixnum i start end))
    ;; pass 1: find the closing quote and whether the fast path applies
    (loop
      (when (>= i end) (json-fail "unterminated string" (jr-pos r)))
      (let ((b (aref buf i)))
        (cond ((= b 34) (return))
              ((or (= b 92) (>= b 128)) (setf simple nil) (return))
              ((< b 32) (json-fail "control character in string" i))))
      (incf i))
    (when simple
      (let ((s (make-string (- i start))))
        (loop for j from start below i
              do (setf (schar s (- j start)) (code-char (aref buf j))))
        (setf (jr-pos r) (1+ i))
        (return-from jr-string s)))
    ;; slow path
    (let ((out (make-array (max 16 (- end start)) :element-type 'character :fill-pointer 0 :adjustable t)))
      (setf i start)
      (loop
        (when (>= i end) (json-fail "unterminated string" (jr-pos r)))
        (let ((b (aref buf i)))
          (cond
            ((= b 34) (incf i) (return))
            ((< b 32) (json-fail "control character in string" i))
            ((= b 92)
             (incf i)
             (when (>= i end) (json-fail "unterminated escape" i))
             (let ((e (aref buf i)))
               (incf i)
               (case e
                 (34 (vector-push-extend #\" out))
                 (92 (vector-push-extend #\\ out))
                 (47 (vector-push-extend #\/ out))
                 (98 (vector-push-extend (code-char 8) out))
                 (102 (vector-push-extend (code-char 12) out))
                 (110 (vector-push-extend (code-char 10) out))
                 (114 (vector-push-extend (code-char 13) out))
                 (116 (vector-push-extend (code-char 9) out))
                 (117
                  (setf (jr-pos r) i)
                  (let ((code (jr-hex4 r)))
                    (setf i (jr-pos r))
                    ;; a high surrogate directly followed by \uDC00-\uDFFF is one astral character
                    (when (and (<= #xD800 code #xDBFF)
                               (< (+ i 5) end)
                               (= (aref buf i) 92) (= (aref buf (1+ i)) 117))
                      (setf (jr-pos r) (+ i 2))
                      (let ((low (handler-case (jr-hex4 r) (json-error () nil))))
                        (cond ((and low (<= #xDC00 low #xDFFF))
                               (setf code (+ #x10000 (ash (- code #xD800) 10) (- low #xDC00))
                                     i (jr-pos r)))
                              (t (setf (jr-pos r) i)))))
                    (vector-push-extend (code-char code) out)))
                 (t (json-fail "invalid escape" (1- i))))))
            ((< b 128) (vector-push-extend (code-char b) out) (incf i))
            (t
             (multiple-value-bind (code len) (utf8-decode buf i end)
               (vector-push-extend (code-char code) out)
               (incf i len))))))
      (setf (jr-pos r) i)
      (coerce out 'simple-string))))

(defun utf8-decode (buf i end)
  "Decode the UTF-8 sequence at I; return the code point and the byte count."
  (declare (type octets buf) (type fixnum i end))
  (let* ((b (aref buf i))
         (len (cond ((= (logand b #xE0) #xC0) 2)
                    ((= (logand b #xF0) #xE0) 3)
                    ((= (logand b #xF8) #xF0) 4)
                    (t (json-fail "invalid UTF-8 lead byte" i)))))
    (when (> (+ i len) end) (json-fail "truncated UTF-8 sequence" i))
    (let ((code (logand b (ecase len (2 #x1F) (3 #x0F) (4 #x07)))))
      (loop for k from 1 below len
            do (let ((c (aref buf (+ i k))))
                 (unless (= (logand c #xC0) #x80) (json-fail "invalid UTF-8 continuation byte" (+ i k)))
                 (setf code (logior (ash code 6) (logand c #x3F)))))
      (when (or (< code (ecase len (2 #x80) (3 #x800) (4 #x10000)))
                (> code #x10FFFF)
                (<= #xD800 code #xDFFF))
        (json-fail "invalid UTF-8 sequence" i))
      (values code len))))

(defun jr-number (r)
  (let* ((buf (jr-buf r)) (end (jr-end r)) (start (jr-pos r)) (i start)
         (negative nil) (mantissa 0) (digits 0) (frac 0) (exp 0) (real nil))
    (declare (type fixnum i start end))
    (flet ((digit-at (k) (and (< k end) (<= 48 (aref buf k) 57))))
      (when (and (< i end) (= (aref buf i) 45)) (setf negative t) (incf i))
      (unless (digit-at i) (json-fail "invalid number" start))
      (if (= (aref buf i) 48)
          (progn (incf i) (incf digits))
          (loop while (digit-at i)
                do (setf mantissa (+ (* mantissa 10) (- (aref buf i) 48))) (incf digits) (incf i)))
      (when (and (< i end) (= (aref buf i) 46))
        (setf real t)
        (incf i)
        (unless (digit-at i) (json-fail "invalid number" start))
        (loop while (digit-at i)
              do (setf mantissa (+ (* mantissa 10) (- (aref buf i) 48))) (incf frac) (incf i)))
      (when (and (< i end) (or (= (aref buf i) 101) (= (aref buf i) 69)))
        (setf real t)
        (incf i)
        (let ((exp-negative nil))
          (when (and (< i end) (or (= (aref buf i) 43) (= (aref buf i) 45)))
            (setf exp-negative (= (aref buf i) 45)) (incf i))
          (unless (digit-at i) (json-fail "invalid number" start))
          (loop while (digit-at i)
                do (when (< exp 100000) (setf exp (+ (* exp 10) (- (aref buf i) 48)))) (incf i))
          (when exp-negative (setf exp (- exp)))))
      (setf (jr-pos r) i)
      (cond
        ((and (not real) (not negative) (<= mantissa +max-safe-integer+)) mantissa)
        ((and (not real) negative (zerop mantissa)) -0d0)
        ((and (not real) (<= mantissa +max-safe-integer+)) (- mantissa))
        (t (let ((value (decimal-to-double mantissa (- exp frac))))
             (cond ((eq value :null) :null)
                   (negative (- value))
                   (t value))))))))

(defun decimal-to-double (mantissa exponent10)
  "The double nearest MANTISSA x 10^EXPONENT10, 0 on underflow, :NULL on overflow (JS reads it as Infinity,
which JSON.stringify writes as null)."
  (cond
    ((zerop mantissa) 0d0)
    (t
     (let ((magnitude (+ (integer-length mantissa) (* exponent10 3.3219280948873626d0))))
       (cond ((> magnitude 1100) :null)
             ((< magnitude -1200) 0d0)
             (t (let ((ratio (if (>= exponent10 0)
                                 (* mantissa (expt 10 exponent10))
                                 (/ mantissa (expt 10 (- exponent10))))))
                  (handler-case (coerce ratio 'double-float)
                    (floating-point-overflow () :null)
                    (floating-point-underflow () 0d0)))))))))

(defun jr-array (r)
  (when (> (incf (jr-depth r)) *max-json-depth*) (json-fail "nesting too deep" (jr-pos r)))
  (incf (jr-pos r))
  (jr-skip-ws r)
  (let ((items '()) (count 0))
    (declare (type fixnum count))
    (if (= (jr-peek r) 93)
        (incf (jr-pos r))
        (loop
          (push (jr-value r) items) (incf count)
          (jr-skip-ws r)
          (case (jr-peek r)
            (44 (incf (jr-pos r)))
            (93 (incf (jr-pos r)) (return))
            (t (json-fail "expected , or ] in array" (jr-pos r))))))
    (decf (jr-depth r))
    (let ((vector (make-array count)))
      (loop for i from (1- count) downto 0 for item in items do (setf (svref vector i) item))
      vector)))

(defun jr-object (r)
  (when (> (incf (jr-depth r)) *max-json-depth*) (json-fail "nesting too deep" (jr-pos r)))
  (incf (jr-pos r))
  (jr-skip-ws r)
  (let ((table (make-hash-table :test 'equal)))
    (if (= (jr-peek r) 125)
        (incf (jr-pos r))
        (loop
          (jr-skip-ws r)
          (unless (= (jr-peek r) 34) (json-fail "expected a string key" (jr-pos r)))
          (let ((key (jr-string r)))
            (jr-skip-ws r)
            (unless (= (jr-peek r) 58) (json-fail "expected : after key" (jr-pos r)))
            (incf (jr-pos r))
            (setf (gethash key table) (jr-value r)))
          (jr-skip-ws r)
          (case (jr-peek r)
            (44 (incf (jr-pos r)))
            (125 (incf (jr-pos r)) (return))
            (t (json-fail "expected , or } in object" (jr-pos r))))))
    (decf (jr-depth r))
    table))

(defun jr-value (r)
  (jr-skip-ws r)
  (let ((b (jr-peek r)))
    (case b
      (-1 (json-fail "unexpected end of input" (jr-pos r)))
      (123 (jr-object r))
      (91 (jr-array r))
      (34 (jr-string r))
      (116 (jr-expect-literal r "true" t))
      (102 (jr-expect-literal r "false" :false))
      (110 (jr-expect-literal r "null" :null))
      (t (if (or (= b 45) (<= 48 b 57))
             (jr-number r)
             (json-fail "unexpected character" (jr-pos r)))))))

(defun decode-json (octets &key (start 0) (end (length octets)))
  "Read one JSON value from the UTF-8 OCTETS."
  (let* ((buf (if (typep octets 'octets) octets (coerce octets 'octets)))
         (r (make-jreader :buf buf :pos start :end end)))
    (prog1 (jr-value r)
      (jr-skip-ws r)
      (when (< (jr-pos r) end) (json-fail "trailing characters after the value" (jr-pos r))))))

(defun from-json (string)
  "Read one JSON value from STRING."
  (decode-json (sb-ext:string-to-octets string :external-format :utf-8)))

;;; ------------------------------------------------------------------ writer

(defvar *encode-limit* nil "When an integer, the most octets ENCODE-JSON may write: a larger value signals frame-too-large without allocating it.")

(defstruct (obuf (:constructor make-obuf ()))
  (data (make-array 512 :element-type '(unsigned-byte 8)) :type octets)
  (fill 0 :type fixnum))

(declaim (inline obuf-byte))
(defun obuf-byte (b byte)
  (declare (type obuf b) (type (unsigned-byte 8) byte))
  (let ((data (obuf-data b)) (fill (obuf-fill b)))
    (when (= fill (length data))
      (let ((bigger (make-array (let ((size (* 2 (length data))))
                                  (if *encode-limit*
                                      (let ((capped (min size (1+ *encode-limit*))))
                                        (when (<= capped (length data))
                                          (error 'host-call-error :message (format nil "frame too large: exceeds ~D bytes" (1- *encode-limit*))))
                                        capped)
                                      size))
                                :element-type '(unsigned-byte 8))))
        (replace bigger data)
        (setf (obuf-data b) bigger data bigger)))
    (setf (aref data fill) byte)
    (setf (obuf-fill b) (1+ fill))))

(defun obuf-ascii (b string)
  (loop for c across string do (obuf-byte b (char-code c))))

(defun obuf-char (b code)
  "Append CODE as UTF-8."
  (declare (type (integer 0 #x10FFFF) code))
  (cond ((< code #x80) (obuf-byte b code))
        ((< code #x800)
         (obuf-byte b (logior #xC0 (ash code -6)))
         (obuf-byte b (logior #x80 (logand code #x3F))))
        ((< code #x10000)
         (obuf-byte b (logior #xE0 (ash code -12)))
         (obuf-byte b (logior #x80 (logand (ash code -6) #x3F)))
         (obuf-byte b (logior #x80 (logand code #x3F))))
        (t
         (obuf-byte b (logior #xF0 (ash code -18)))
         (obuf-byte b (logior #x80 (logand (ash code -12) #x3F)))
         (obuf-byte b (logior #x80 (logand (ash code -6) #x3F)))
         (obuf-byte b (logior #x80 (logand code #x3F))))))

(defun obuf-hex4 (b code)
  (obuf-ascii b "\\u")
  (loop for shift from 12 downto 0 by 4
        do (obuf-byte b (char-code (char "0123456789abcdef" (logand (ash code (- shift)) 15))))))

(defun write-json-string (b string)
  (obuf-byte b 34)
  (loop for c across string
        for code = (char-code c)
        do (cond ((= code 34) (obuf-ascii b "\\\""))
                 ((= code 92) (obuf-ascii b "\\\\"))
                 ((< code 32)
                  (case code
                    (8 (obuf-ascii b "\\b")) (9 (obuf-ascii b "\\t")) (10 (obuf-ascii b "\\n"))
                    (12 (obuf-ascii b "\\f")) (13 (obuf-ascii b "\\r"))
                    (t (obuf-hex4 b code))))
                 ((<= #xD800 code #xDFFF) (obuf-hex4 b code))
                 (t (obuf-char b code))))
  (obuf-byte b 34))

(defun shortest-digits (x)
  "Position N and the shortest digit string D with 0.D x 10^N reading back as the positive double X.
SBCL's FLONUM-TO-DIGITS is shortest for normal doubles; for denormals it is not, so search by precision."
  (if (>= x 2.2250738585072014d-308)
      (sb-impl::flonum-to-digits x)
      (let ((exact (rational x)))
        (loop for precision from 1 to 17
              do (let* ((magnitude (floor (log x 10d0)))
                        (n (+ magnitude 1))
                        (scaled (round (* exact (expt 10 (- precision n)))))
                        (candidate (* scaled (expt 10 (- n precision)))))
                   (when (and (plusp scaled) (= (coerce candidate 'double-float) x))
                     (let ((text (string-right-trim "0" (princ-to-string scaled))))
                       (return-from shortest-digits
                         (values (+ n (- (length (princ-to-string scaled)) precision)) text))))))
        (sb-impl::flonum-to-digits x))))

(defun js-number-string (x)
  "ECMAScript Number::toString of the double X (finite, non-zero)."
  (multiple-value-bind (n digits) (shortest-digits (abs x))
    (let* ((k (length digits))
           (sign (if (minusp x) "-" "")))
      (cond
        ((<= k n 21)
         (format nil "~A~A~A" sign digits (make-string (- n k) :initial-element #\0)))
        ((< 0 n 22)
         (format nil "~A~A.~A" sign (subseq digits 0 n) (subseq digits n)))
        ((< -6 n 1)
         (format nil "~A0.~A~A" sign (make-string (- n) :initial-element #\0) digits))
        (t
         (let ((e (1- n)))
           (format nil "~A~A~:[.~A~;~*~]e~A~D" sign (char digits 0) (= k 1) (subseq digits 1)
                   (if (minusp e) "-" "+") (abs e))))))))

(defun write-json-number (b x)
  (cond
    ((integerp x)
     (if (< (- +max-safe-integer+) x +max-safe-integer+)
         (obuf-ascii b (let ((*print-base* 10) (*print-radix* nil)) (princ-to-string x)))
         (write-json-number b (coerce x 'double-float))))
    ((floatp x)
     (let ((d (coerce x 'double-float)))
       (cond ((or (sb-ext:float-nan-p d) (sb-ext:float-infinity-p d)) (obuf-ascii b "null"))
             ((zerop d) (obuf-byte b 48))
             (t (obuf-ascii b (js-number-string d))))))
    (t (write-json-number b (coerce x 'double-float)))))

(defun write-json-value (b value)
  (cond
    ((lazy-value-p value) (write-json-value b (force-lazy value)))
    ((eq value t) (obuf-ascii b "true"))
    ((eq value :false) (obuf-ascii b "false"))
    ((or (null value) (eq value :null)) (obuf-ascii b "null"))
    ((stringp value) (write-json-string b value))
    ((realp value) (write-json-number b value))
    ((hash-table-p value)
     (obuf-byte b 123)
     (let ((first t))
       (maphash (lambda (key member)
                  (unless (stringp key) (json-fail (format nil "object key ~S is not a string" key)))
                  (if first (setf first nil) (obuf-byte b 44))
                  (write-json-string b key)
                  (obuf-byte b 58)
                  (write-json-value b member))
                value))
     (obuf-byte b 125))
    ((vectorp value)
     (obuf-byte b 91)
     (dotimes (i (length value))
       (when (plusp i) (obuf-byte b 44))
       (write-json-value b (aref value i)))
     (obuf-byte b 93))
    ((consp value)
     (obuf-byte b 91)
     (loop for rest on value for first = t then nil
           do (unless first (obuf-byte b 44))
              (write-json-value b (car rest))
              (unless (listp (cdr rest)) (json-fail "improper list cannot be written as an array")))
     (obuf-byte b 93))
    (t (json-fail (format nil "cannot write ~S as JSON" value)))))

(defun encode-json (value &key (reserve 0))
  "UTF-8 octets of VALUE as JSON, preceded by RESERVE unset bytes (the frame header)."
  (let ((b (make-obuf)))
    (setf (obuf-fill b) reserve)
    (write-json-value b value)
    (subseq (obuf-data b) 0 (obuf-fill b))))

(defun to-json (value)
  "VALUE as a JSON string."
  (sb-ext:octets-to-string (encode-json value) :external-format :utf-8))
