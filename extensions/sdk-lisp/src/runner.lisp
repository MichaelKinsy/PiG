;;;; SPDX-License-Identifier: MIT
;;;; Entry points: a standalone extension and the generated runner image.

(in-package #:pig)

(defun run-extension (extension &optional (socket (sb-posix:getenv "PIG_EXT_SOCKET")))
  "Connect EXTENSION to the host socket (PIG_EXT_SOCKET) and serve it until the host shuts it down.
The standalone entry point: (pig:run-extension (new-extension)) in an executable main.lisp."
  (unless (and socket (plusp (length socket)))
    (error "PIG_EXT_SOCKET not set: the extension must be launched by pig"))
  (serve extension (connect-unix socket)))

;;; ---------------------------------------------------------------- the generated runner

(defvar *members* '()
  "The extensions baked into this image: a list of (name . factory), filled by the image build.")

(defun register-member (name factory)
  (setf *members* (append *members* (list (cons name factory)))))

(defun socket-env-name (name)
  "The environment variable that carries NAME's socket (runtimecell.SocketEnvName)."
  (concatenate 'string "PIG_EXT_SOCKET_"
               (map 'string (lambda (c) (if (or (alphanumericp c) (char= c #\_)) (char-upcase c) #\_))
                    (remove-if (lambda (c) (> (char-code c) 127)) name))))

(defun unescape-field (field)
  "Decode the four escapes the host puts on a control line field (encodeAdmissionField); the percent sign last."
  (flet ((replace-all (string from to)
           (with-output-to-string (out)
             (loop with start = 0
                   for hit = (search from string :start2 start)
                   do (write-string string out :start start :end (or hit (length string)))
                      (if hit (progn (write-string to out) (setf start (+ hit (length from)))) (return))))))
    (replace-all (replace-all (replace-all (replace-all field "%09" (string #\Tab)) "%0A" (string #\Newline))
                              "%0D" (string #\Return))
                 "%25" "%")))

(defun split-tabs (line)
  (loop with start = 0
        for tab = (position #\Tab line :start start)
        collect (subseq line start tab)
        while tab
        do (setf start (1+ tab))))

(defvar *factory-turn* (sb-thread:make-mutex :name "pig factory turn"))

(defun build-member (member socket)
  "Run MEMBER's factory with PIG_EXT_SOCKET set to its socket, one factory at a time as Pi loads extensions."
  (sb-thread:with-mutex (*factory-turn*)
    (let ((previous (sb-posix:getenv "PIG_EXT_SOCKET")))
      (when socket (sb-posix:setenv "PIG_EXT_SOCKET" socket 1))
      (unwind-protect (funcall (cdr member))
        (if previous (sb-posix:setenv "PIG_EXT_SOCKET" previous 1) (sb-posix:unsetenv "PIG_EXT_SOCKET"))))))

(defun report-member-failure (member condition socket)
  (format *error-output* "~A: ~A~%" (car member) (condition-message condition))
  (finish-output *error-output*)
  (when (and socket (plusp (length socket)))
    (ignore-errors (conn-close (connect-unix socket)))))

(defun runner-main ()
  "The toplevel of a generated image: serve every baked member on its own socket and thread, admit further
generations on the host's control lines, and exit once none is live (unless the host parked the process)."
  (setf (sb-ext:bytes-consed-between-gcs) (* 32 1024 1024))
  (let ((lock (sb-thread:make-mutex :name "pig runner"))
        (queue (sb-thread:make-waitqueue))
        (live 0) (parked nil) (closed nil) (failed nil)
        (single (= (length *members*) 1)))
    (labels ((note-done ()
               (sb-thread:with-mutex (lock) (decf live) (sb-thread:condition-broadcast queue)))
             (start (member socket)
               (let ((built (make-latch)))
                 (sb-thread:with-mutex (lock) (incf live))
                 (sb-thread:make-thread
                  (lambda ()
                    (unwind-protect
                         (handler-case
                             (progn
                               (unless (and socket (plusp (length socket)))
                                 (error "~A not set for ~A" (socket-env-name (car member)) (car member)))
                               (let ((ext (build-member member socket)))
                                 (latch-set built)
                                 (unless (serve ext (connect-unix socket))
                                   (setf failed t)
                                   (format *error-output* "~A: extension handlers did not stop before the shutdown deadline~%" (car member)))))
                           (serious-condition (c)
                             (setf failed t)
                             (report-member-failure member c socket)))
                      (latch-set built)
                      (note-done)))
                  :name (format nil "pig-member-~A" (car member)))
                 built))
             (control ()
               (loop for line = (read-line *standard-input* nil nil)
                     while line
                     do (let ((fields (mapcar #'unescape-field (split-tabs (string-right-trim '(#\Return #\Newline) line)))))
                          (cond
                            ((equal (first fields) "park")
                             (sb-thread:with-mutex (lock) (setf parked t) (sb-thread:condition-broadcast queue))
                             (when (and (> (length fields) 2) (plusp (length (third fields))))
                               (ignore-errors (conn-close (connect-unix (third fields))))))
                            ((and (equal (first fields) "admit") (>= (length fields) 3))
                             (let ((member (assoc (second fields) *members* :test #'string=)))
                               (when member
                                 (sb-thread:with-mutex (lock) (setf parked nil))
                                 (start member (third fields))))))))
               (sb-thread:with-mutex (lock) (setf closed t) (sb-thread:condition-broadcast queue))))
      (let* ((active-value (sb-posix:getenv "PIG_EXT_ACTIVE_MEMBERS"))
             (active (and active-value (plusp (length active-value))
                          (loop with start = 0
                                for comma = (position #\, active-value :start start)
                                collect (subseq active-value start comma)
                                while comma do (setf start (1+ comma))))))
        (dolist (member *members*)
          (when (or (null active) (member (car member) active :test #'string=))
            (latch-wait (start member (or (let ((s (sb-posix:getenv (socket-env-name (car member))))) (and s (plusp (length s)) s))
                                          (and single (sb-posix:getenv "PIG_EXT_SOCKET"))))))))
      (sb-thread:make-thread #'control :name "pig-control")
      (sb-thread:with-mutex (lock)
        (loop while (or (plusp live) (and parked (not closed)))
              do (sb-thread:condition-wait queue lock)))
      (finish-output *error-output*)
      (sb-ext:exit :code (if failed 1 0) :abort t))))

(defun image-main ()
  "The toplevel of every generated image: baked members when there are any, else the standalone loader
(`image main.lisp`, which loads the file and exits when the extension stops)."
  (cond (*members* (runner-main))
        (t (let ((file (second sb-ext:*posix-argv*)))
             (unless file (format *error-output* "pig image: no extension file given~%") (sb-ext:exit :code 2 :abort t))
             (handler-case (progn (load file) (finish-output) (sb-ext:exit :code 0 :abort t))
               (serious-condition (c)
                 (format *error-output* "~A: ~A~%" file (condition-message c))
                 (finish-output *error-output*)
                 (sb-ext:exit :code 1 :abort t)))))))
