;;;; SPDX-License-Identifier: MIT
;;;; The walks the SDK makes over values the host sends (tool arguments, event payloads, conversation values, palettes) hold on the control stack of the thread
;;;; that runs them, 2 MB in the image and in the thread that runs these tests: a value nested 10000 levels (what the host sends) or with 200000 members passes.
(in-package #:pig-tests)

(defun deep-array (depth leaf)
  "LEAF inside DEPTH one-element arrays."
  (let ((value leaf))
    (dotimes (i depth value) (setf value (vector value)))))

(defun deep-object (depth leaf)
  "LEAF inside DEPTH one-member objects."
  (let ((value leaf))
    (dotimes (i depth value) (setf value (let ((table (make-hash-table :test 'equal))) (setf (gethash "a" table) value) table)))))

(deftest event-stream-copies-take-the-nesting-the-host-sends
  (dolist (text (list (nested 10000) (nested-objects 10000)))
    (let* ((value (dec text)) (copy (pig::clone-stream-value value)))
      (check (not (eq value copy)))
      (check-equal text (enc copy)))))

(deftest tool-arguments-are-ordered-at-the-nesting-the-host-sends
  (dolist (text (list (nested 10000) (nested-objects 10000)))
    (let* ((value (dec text)) (ordered (pig::core-js-ordered value)))
      (check (not (eq value ordered)))
      (check-equal text (enc ordered))))
  (check-equal "{\"1\":3,\"2\":2,\"b\":1,\"a\":4}" (enc (pig::core-js-ordered (dec "{\"b\":1,\"2\":2,\"1\":3,\"a\":4}"))))
  (check-equal "[{\"0\":1,\"z\":2}]" (enc (pig::core-js-ordered (dec "[{\"z\":2,\"0\":1}]")))))

(deftest json-stringify-takes-the-nesting-and-the-width-the-host-sends
  (dolist (text (list (nested 10000) (nested-objects 10000)))
    (check-equal text (pig::cx-json-stringify (dec text))))
  (check-equal "{\"a\":[1,2,{\"b\":null}],\"c\":\"x\"}" (pig::cx-json-stringify (dec "{\"a\":[1,2,{\"b\":null}],\"c\":\"x\"}")))
  (let* ((numbers (make-array 200000 :initial-element 1)) (text (pig::cx-json-stringify numbers)))
    (check-equal (1- (* 2 200000)) (- (length text) 2))
    (check-equal "[1,1,1" (subseq text 0 6)))
  (let ((objects (make-array 20000)))
    (dotimes (i 20000) (setf (svref objects i) (dec "{\"k\":1,\"v\":[2]}")))
    (check-equal (+ 2 19999 (* 20000 (length "{\"k\":1,\"v\":[2]}"))) (length (pig::cx-json-stringify objects)))))

(deftest a-bigint-is-found-at-the-nesting-the-host-sends
  (let ((big (pig::make-big-int 5)))
    (dolist (build (list #'deep-array #'deep-object))
      (check (pig::ts-holds-big-int (funcall build 10000 big)))
      (check (not (pig::ts-holds-big-int (funcall build 10000 1)))))
    (check (pig::ts-holds-big-int (deep-array 100000 big)))
    (check (not (pig::ts-holds-big-int (dec "{\"a\":[1,{\"b\":2}]}"))))))

(deftest json-values-compare-at-the-nesting-the-host-sends
  (dolist (build (list #'deep-array #'deep-object))
    (check (pig::ts-json-equal (funcall build 10000 "x") (funcall build 10000 "x")))
    (check (not (pig::ts-json-equal (funcall build 10000 "x") (funcall build 10000 "y"))))
    (check (not (pig::ts-json-equal (funcall build 10000 "x") (funcall build 9999 "x"))))))

(deftest the-string-of-a-nested-array-is-taken-at-the-nesting-the-host-sends
  (check-equal "1" (pig::cx-js-value-string (dec (nested 10000))))
  (check-equal "1" (pig::ai-js-string (dec (nested 10000))))
  (check-equal "1,2,3" (pig::cx-js-value-string (dec "[1,[2,[3]]]")))
  (check-equal "1,2,3" (pig::ai-js-string (dec "[1,[2,[3]]]"))))
