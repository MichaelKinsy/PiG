;;;; SPDX-License-Identifier: MIT
;;;; A dependency-free test harness: DEFTEST registers, CHECK counts.
(defpackage #:pig-tests
  (:use #:cl)
  (:export #:run-all #:deftest #:check #:check-equal #:check-error #:repo-path #:read-text-file))
(in-package #:pig-tests)

(defvar *tests* '())

(defparameter *repo-root*
  (merge-pathnames "../../../" (make-pathname :name nil :type nil :defaults (or *load-truename* *default-pathname-defaults*)))
  "The repository root: this file is extensions/sdk-lisp/tests/harness.lisp.")

(defun repo-path (relative)
  "RELATIVE (a slash-separated string such as \"test/parity/sdk-vectors/pi-ai/x.json\") under the repository root."
  (merge-pathnames relative *repo-root*))

(defun read-text-file (path)
  "The whole file as a UTF-8 string."
  (with-open-file (in path :external-format :utf-8)
    (let* ((text (make-string (file-length in))) (n (read-sequence text in))) (subseq text 0 n))))
(defvar *failures* 0)
(defvar *checks* 0)

(defmacro deftest (name &body body)
  `(progn (setf *tests* (append (remove ',name *tests* :key #'car) (list (cons ',name (lambda () ,@body)))))
          ',name))

(defun report-failure (form message)
  (incf *failures*)
  (format t "~&FAIL ~S~@[: ~A~]~%" form message))

(defmacro check (form &optional message)
  `(progn (incf *checks*)
          (unless ,form (report-failure ',form ,message))))

(defmacro check-equal (expected actual &optional message)
  `(progn (incf *checks*)
          (let ((e ,expected) (a ,actual))
            (unless (equalp e a)
              (report-failure ',actual (format nil "expected ~S, got ~S~@[ (~A)~]" e a ,message))))))

(defmacro check-error (condition-type form)
  `(progn (incf *checks*)
          (handler-case (progn ,form (report-failure ',form (format nil "expected ~S" ',condition-type)))
            (,condition-type () nil))))

(defun run-all ()
  (setf *failures* 0 *checks* 0)
  (dolist (test *tests*)
    (handler-case (funcall (cdr test))
      (error (e) (report-failure (car test) (format nil "unhandled ~A" e)))))
  (format t "~&~D tests, ~D checks, ~D failures~%" (length *tests*) *checks* *failures*)
  (unless (zerop *failures*) (sb-ext:exit :code 1 :abort t))
  t)
