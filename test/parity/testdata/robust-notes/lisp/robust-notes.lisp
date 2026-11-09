;;;; The Lisp SDK version of the robust-notes fixture (../node/index.mjs).
(defpackage "robust-notes" (:use "COMMON-LISP"))
(in-package "robust-notes")

;;; POSIX-style word splitting with quotes (the vendored dependency of the other realizations: ../python/robust_words).
(defun split-words (line)
  (let ((words '()) (word (make-string-output-stream)) (in-word nil) (quote-char nil))
    (flet ((finish ()
             (when in-word (push (get-output-stream-string word) words))
             (get-output-stream-string word)
             (setf in-word nil)))
      (loop for ch across line
            do (cond (quote-char (if (char= ch quote-char) (setf quote-char nil) (write-char ch word)))
                     ((member ch '(#\' #\")) (setf quote-char ch in-word t))
                     ((member ch '(#\Space #\Tab)) (finish))
                     (t (write-char ch word) (setf in-word t))))
      (when quote-char (error "unterminated quote"))
      (finish))
    (nreverse words)))


(defclass picker () ((items :initform (list "alpha" "beta" "gamma") :reader items) (selected :initform 0 :accessor selected)))

(defun picker-render (picker width)
  (cons (format nil "robust pick (~D)" width)
        (loop for item in (items picker) for index from 0
              collect (format nil "~A ~A" (if (= index (selected picker)) ">" " ") item))))

(defun picker-input (picker data)
  (cond ((string= data "j") (setf (selected picker) (mod (1+ (selected picker)) (length (items picker)))) nil)
        ((string= data "s") (pig:make-custom-result :done t :value (nth (selected picker) (items picker))))
        ((string= data "q") (pig:make-custom-result :done t))))

(defun new-extension ()
  (let ((ext (pig:make-extension "lisp")) (tool-runs 0))
    (pig:flag ext "robust-prefix" :description "Prefix for robust_words output" :type "string" :default "rw")
    (pig:tool ext "robust_words" "Split a command line into words."
              (pig:obj "type" "object" "properties" (pig:obj "line" (pig:obj "type" "string")) "required" (vector "line"))
              (lambda (ctx params)
                (format nil "~A: ~{~A~^|~}" (pig:ctx-get-flag ctx "robust-prefix") (split-words (pig:jget params "line")))))
    (pig:on ext "session_start" (lambda (ctx data) (declare (ignore data)) (pig:ctx-set-status ctx "robust" "robust: ready")))
    (pig:on ext "tool_result"
            (lambda (ctx data)
              (when (equal (pig:jget data "toolName") "robust_words")
                (incf tool-runs)
                (pig:ctx-set-status ctx "robust" (format nil "robust: tools=~D" tool-runs)))
              nil))
    (pig:command ext "robust-settings" "Show the robust-notes settings"
                 (lambda (ctx args)
                   (declare (ignore args))
                   (let ((greeting (pig:jget (pig:jget (pig:ctx-get-settings ctx) "robustNotes") "greeting" "none")))
                     (pig:ctx-notify ctx (format nil "robust settings: greeting=~A prefix=~A" greeting (pig:ctx-get-flag ctx "robust-prefix")) "info"))))
    (pig:command ext "robust-note" "Save a note"
                 (lambda (ctx args)
                   (pig:ctx-append-entry ctx "robust-note" (pig:obj "text" args))
                   (let ((notes (count-if (lambda (entry) (and (equal (pig:jget entry "type") "custom") (equal (pig:jget entry "customType") "robust-note")))
                                          (pig:ctx-get-entries ctx))))
                     (pig:ctx-notify ctx (format nil "robust note ~D: ~A" notes args) "info"))))
    (pig:command ext "robust-widgets" "Set, repeat, clear and reset a widget around notifications"
                 (lambda (ctx args)
                   (declare (ignore args))
                   (pig:ctx-set-widget ctx "robust-w" (list "w-one"))
                   (pig:ctx-notify ctx "w-n1" "info")
                   (pig:ctx-set-widget ctx "robust-w" (list "w-one"))
                   (pig:ctx-set-widget ctx "robust-w" nil)
                   (pig:ctx-set-widget ctx "robust-w" (list "w-two"))
                   (pig:ctx-notify ctx "w-n2" "info")))
    (pig:command ext "robust-pick" "Pick an item in an overlay"
                 (lambda (ctx args)
                   (declare (ignore args))
                   (let* ((picker (make-instance 'picker))
                          (choice (pig:ctx-custom ctx (pig:make-custom-component
                                                       :render (lambda (width) (picker-render picker width))
                                                       :handle-input (lambda (data) (picker-input picker data)))
                                                  :options (pig:obj "overlay" t))))
                     (pig:ctx-notify ctx (format nil "robust picked: ~A" (or choice "nothing")) "info"))))
    ext))
