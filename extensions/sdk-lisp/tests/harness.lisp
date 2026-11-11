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

(defparameter *host-read-timeout* 10
  "Seconds a fake host waits for the extension's next frame before the test fails. Every other SDK's fake host bounds its reads the same way
(sdk/event_bus_factory_test.go: SetDeadline 5 s; sdk-rs/src/event_bus_tests.rs: set_read_timeout 5 s; sdk-py/tests/test_kit.py: settimeout 10): a frame that
never comes fails the test that waits for it instead of blocking the run, and the extension thread that is itself waiting for the host's answer
cannot keep a mismatched exchange alive.")

(defun accept-host-conn (listener)
  "The extension's connection to the fake host listening on LISTENER, with reads bounded by *HOST-READ-TIMEOUT* (a read past it signals SB-SYS:IO-TIMEOUT)."
  (let* ((socket (sb-bsd-sockets:socket-accept listener))
         (stream (sb-bsd-sockets:socket-make-stream socket :input t :output t :buffering :full :element-type '(unsigned-byte 8)
                                                           :timeout *host-read-timeout*)))
    (pig::%make-conn :impl socket :in stream :out stream)))
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
      (sb-sys:io-timeout ()
        (report-failure (car test) (format nil "blocked: no frame arrived from the extension within ~D s (the extension and the test host were both waiting; the exchange before this point does not match)" *host-read-timeout*)))
      (error (e) (report-failure (car test) (format nil "unhandled ~A" e)))))
  (format t "~&~D tests, ~D checks, ~D failures~%" (length *tests*) *checks* *failures*)
  (unless (zerop *failures*) (sb-ext:exit :code 1 :abort t))
  t)
