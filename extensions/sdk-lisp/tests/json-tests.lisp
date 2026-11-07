;;;; SPDX-License-Identifier: MIT
;;;; Vectors copied from the other SDKs' tests; each block names its source.
(in-package #:pig-tests)

(defun enc (v) (pig:to-json v))
(defun dec (s) (pig:from-json s))
(defun lone (code) (string (code-char code)))

;; extensions/sdk-rs/src/float_roundtrip_tests.rs: frame_numbers_decode_to_the_nearest_double
(deftest numbers-read-as-the-nearest-double
  (loop for (text . double) in
        `(("123456789012345680000" . ,123456789012345680000d0)
          ("1.2345678901234568e+20" . ,1.2345678901234568d20)
          ("0.1" . 0.1d0) ("0.30000000000000004" . 0.30000000000000004d0)
          ("5e-324" . 4.9406564584124654d-324)
          ("1.7976931348623157e308" . 1.7976931348623157d308)
          ("2.2250738585072011e-308" . 2.2250738585072011d-308)
          ("9007199254740993" . 9007199254740992d0)
          ("1e23" . 1d23) ("8.41e21" . 8.41d21) ("4.35" . 4.35d0)
          ("0.000001234567890123456789" . 1.234567890123456789d-6)
          ("123456789.12345678912345" . 123456789.12345678912345d0))
        do (let ((v (dec text)))
             (check-equal double (coerce v 'double-float) text))))

;; extensions/sdk-rs/src/float_roundtrip_tests.rs: a_number_outside_the_double_range_is_null...
(deftest numbers-outside-the-double-range
  (check (eq :null (dec "1e400")))
  (check (eq :null (dec "-1e999")))
  (check (eql 0d0 (dec "1e-400")))
  (check (equal "1e400" (dec "\"1e400\"")))
  (let ((v (dec "{\"v\":1e400,\"w\":[-1e999,1e-400,2.5]}")))
    (check (eq :null (gethash "v" v)))
    (check (eq :null (aref (gethash "w" v) 0)))
    (check (eql 0d0 (aref (gethash "w" v) 1)))))

(deftest number-text-is-ecmascript
  (loop for (value . text) in
        '((1 . "1") (-5 . "-5") (0 . "0") (1d21 . "1e+21") (1d20 . "100000000000000000000")
          (1.5d0 . "1.5") (0.1d0 . "0.1") (1d-6 . "0.000001") (1d-7 . "1e-7")
          (4.9406564584124654d-324 . "5e-324") (1.7976931348623157d308 . "1.7976931348623157e+308")
          (123456789012345680000d0 . "123456789012345680000") (1.2345678901234568d21 . "1.2345678901234568e+21")
          (-0d0 . "0") (0d0 . "0") (100d0 . "100") (-1.5d-7 . "-1.5e-7") (0.30000000000000004d0 . "0.30000000000000004")
          (9007199254740993 . "9007199254740992") (1/2 . "0.5"))
        do (check-equal text (enc value) (format nil "~S" value)))
  (check-equal "null" (enc sb-ext:double-float-positive-infinity))
  (check-equal "null" (enc (sb-kernel:make-double-float -524288 0))))

(deftest integers-and-negative-zero-read
  (check (eql 5 (dec "5")))
  (check (eql -7 (dec "-7")))
  (check (eql -0d0 (dec "-0")))
  (check (eql 2d0 (dec "2.0")))
  (check (eql 9007199254740992 (dec "9007199254740992")))
  (check (eql 1d22 (dec "10000000000000000000000"))))

;; extensions/sdk-py/tests/test_surrogate_transport.py: test_wire_preserves_pi_utf16_units_and_literal_escapes
(deftest lone-surrogates-travel-as-escapes
  (loop for text in (list "" "A" (lone #xD83D) (lone #xDE00) (string (code-char #x1F600))
                          (concatenate 'string "A" (lone #xD83D) "Z" (lone #xDE00))
                          (concatenate 'string "\\" "ud83d"))
        do (let* ((octets (pig:encode-json text))
                  (back (pig:decode-json octets)))
             (check-equal text back)
             (check (every (lambda (b) (or (< b 128) t)) octets))
             ;; the frame is valid UTF-8 whatever the string holds
             (check (stringp (sb-ext:octets-to-string octets :external-format :utf-8)))))
  (check-equal "\"\\ud83d\"" (enc (lone #xD83D)))
  (check-equal "\"\\\\ud83d\"" (enc (concatenate 'string "\\" "ud83d")))
  (check-equal "\"😀\"" (enc (string (code-char #x1F600))))
  ;; a valid pair written as two escapes is one astral character; a lone escape stays lone
  (check-equal (string (code-char #x1F600)) (dec "\"\\ud83d\\ude00\""))
  (check-equal (lone #xD83D) (dec "\"\\ud83d\""))
  (check-equal (concatenate 'string (lone #xD83D) "x") (dec "\"\\ud83dx\"")))

(deftest string-escapes
  (check-equal "\"a\\\"b\\\\c\\n\\t\\u0001\\u001f\"" (enc (format nil "a\"b\\c~C~C~C~C" #\Newline #\Tab (code-char 1) (code-char 31))))
  (check-equal (format nil "~C" (code-char 127)) (dec (format nil "\"~C\"" (code-char 127))))
  (check-equal (format nil "é€~C" (code-char #x1F600)) (dec "\"é€😀\"")))

(deftest object-order-is-kept
  (let ((v (dec "{\"b\":1,\"a\":2,\"1\":3,\"b\":9}")))
    (check-equal "{\"b\":9,\"a\":2,\"1\":3}" (enc v)))
  (check-equal "{\"z\":1,\"y\":[true,false,null,\"s\"]}"
               (enc (pig:obj "z" 1 "y" (vector t :false :null "s")))))

(deftest values-round-trip
  (let ((text "{\"a\":[1,2.5,{\"b\":null}],\"c\":\"d\",\"e\":true,\"f\":false,\"g\":{}}"))
    (check-equal text (enc (dec text))))
  (check-equal "[]" (enc #()))
  (check-equal "null" (enc nil))
  (check-equal "[1,2]" (enc '(1 2))))

(deftest malformed-json-is-an-error
  (dolist (bad '("" "{" "[1,]" "{\"a\"}" "01" "1." "-" "tru" "\"\\x\"" "[1] 2" "{\"a\":1,}" "\"a" "nul"))
    (check-error pig:json-error (dec bad))))

;; A value nested past the reader's bound (10016 levels) is Pi's RangeError, not the end of the connection (wire.lisp read-frame): the request it came in is answered with
;; that error and the connection goes on. A frame up to the bound (past the 10000 levels of the host's own marshal) reads.
(defun nested (depth)
  (concatenate 'string (make-string depth :initial-element #\[) "1" (make-string depth :initial-element #\])))

(deftest nesting-up-to-the-bound-reads-and-past-it-is-refused
  (check (vectorp (dec (nested 4000))))
  (check (vectorp (dec (nested 9996))))
  (check (vectorp (dec (nested pig::*max-json-depth*))))
  (check-error pig:json-error (dec (nested (1+ pig::*max-json-depth*)))))

;; The reader and the writer keep the containers on a stack of their own: a value nested to the bound round-trips on the image's default control stack.
(defun nested-objects (depth)
  (concatenate 'string (apply #'concatenate 'string (loop repeat depth collect "{\"a\":")) "1" (make-string depth :initial-element #\})))

(deftest deep-values-round-trip-on-the-default-control-stack
  (check-equal (nested 10000) (enc (dec (nested 10000))))
  (check-equal (nested-objects 10000) (enc (dec (nested-objects 10000))))
  (check-equal "[[],{},[1,[2]]]" (enc (dec "[[],{},[1,[2]]]")))
  ;; Around the depth where the recursive reader and writer hand over to the walk with a stack of its own.
  (dolist (depth '(254 255 256 257 258 600))
    (check-equal (nested depth) (enc (dec (nested depth))))
    (check-equal (nested-objects depth) (enc (dec (nested-objects depth))))
    (let ((mixed (format nil "{\"k\":[~A,{\"z\":~A}],\"n\":2}" (nested depth) (nested-objects depth))))
      (check-equal mixed (enc (dec mixed))))))

(defun frame-body (text) (sb-ext:string-to-octets text :external-format :utf-8))

(deftest a-frame-nested-too-deep-stands-for-an-error-answer
  (let ((deep (nested 30000)))
    (check-equal '("request" "c-7") (multiple-value-list (pig::frame-head (frame-body (format nil "{\"type\":\"request\",\"id\":\"c-7\",\"request\":{\"args\":~A,\"s\":\"]}\"}}" deep)))))
    (check-equal '("call_result" "a\"b") (multiple-value-list (pig::frame-head (frame-body (format nil "{\"id\":\"a\\\"b\",\"pad\":[{\"type\":\"x\"}],\"type\":\"call_result\",\"v\":~A}" deep)))))
    (let ((request (pig::too-deep-envelope (frame-body (format nil "{\"type\":\"request\",\"id\":\"c-1\",\"request\":{\"args\":~A}}" deep)))))
      (check-equal "request" (pig:jget request "type"))
      (check-equal "c-1" (pig:jget request "id"))
      (check-equal "Maximum call stack size exceeded" (pig:jget request "refused")))
    (let ((result (pig::too-deep-envelope (frame-body (format nil "{\"type\":\"call_result\",\"id\":\"h-2\",\"call_result\":{\"result\":~A}}" deep)))))
      (check-equal "Maximum call stack size exceeded" (pig:jget (pig:jget (pig:jget result "call_result") "error") "message")))
    (check-equal "refused" (pig:jget (pig::too-deep-envelope (frame-body (format nil "{\"type\":\"notify\",\"notify\":{\"args\":~A}}" deep))) "type"))))

;; A string with an escape or a non-ASCII character was decoded into a buffer as long as the rest of the frame, so a frame of N bytes holding K such strings
;; consed K x N characters: a 16 MiB frame took 20 s. The buffer is as long as the string.
(deftest decoding-strings-with-escapes-conses-in-proportion-to-the-frame
  (let* ((text (format nil "~{~A~^,~}"
                       (loop for i below 2000 collect (format nil "\"line ~D\\nsecond \\\"line\\\" \\u00e9 ~A\"" i (make-string 400 :initial-element #\x)))))
         (body (frame-body (format nil "[~A]" text)))
         (before (sb-ext:get-bytes-consed))
         (value (pig:decode-json body))
         (consed (- (sb-ext:get-bytes-consed) before)))
    (check (= 2000 (length value)))
    (check-equal (format nil "line 7~%second \"line\" ~C ~A" (code-char 233) (make-string 400 :initial-element #\x)) (aref value 7))
    ;; 2000 strings of about 450 characters (4 bytes each) in a frame of about 1 MB: well under 100 bytes consed per frame byte; the buffer sized by the rest of the
    ;; frame conses about 4 MB per string, 8 GB in all.
    (check (< consed (* 100 (length body))))))
