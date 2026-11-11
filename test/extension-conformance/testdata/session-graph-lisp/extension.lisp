(in-package "session-graph")

(defun new-extension ()
  (let ((ext (pig:make-extension "session-graph")))
    (loop for (name . script) in *scripts*
          do (let ((script script))
               (pig:tool ext name (format nil "Runs the ~A script." name) (pig:obj "type" "object")
                         (lambda (ctx params) (pig:to-json (funcall script ctx params))))))
    ext))
