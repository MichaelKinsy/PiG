;;;; SPDX-License-Identifier: MIT
;;;; A dependency-free test harness: DEFTEST registers, CHECK counts.
(defpackage #:pig-tests
  (:use #:cl)
  (:export #:run-all #:deftest #:check #:check-equal #:check-error))
(in-package #:pig-tests)

(defvar *tests* '())
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
