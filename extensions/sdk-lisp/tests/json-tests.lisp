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
