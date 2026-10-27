;;;; SPDX-License-Identifier: MIT
;;;; Tools whose parameters are built with Type: what the host validates a tool call's arguments with (the schema with TypeBox's ~kind on every
;;;; node) and the refinements (Type.refine) it asks this process to run.

(in-package #:pig)

(defstruct (tsd-state (:constructor make-tsd-state ()))
  (lock (sb-thread:make-mutex :name "pig tool schema"))
  (sites (make-hash-table :test 'equal)))     ; tool name -> ((path . refinements) ...)

(defun tsd-state-of (ext) (extension-extra ext :tool-schema #'make-tsd-state))

(defparameter +tsd-child-keywords+
  '("additionalProperties" "additionalItems" "contains" "propertyNames" "not" "if" "then" "else" "items" "unevaluatedItems" "unevaluatedProperties"))
(defparameter +tsd-member-keywords+ '("properties" "patternProperties" "dependentSchemas" "dependencies" "$defs" "definitions"))
(defparameter +tsd-list-keywords+ '("allOf" "anyOf" "oneOf" "prefixItems" "items"))

(defun tsd-pointer-segment (key)
  (with-output-to-string (out)
    (loop for c across key
          do (case c (#\~ (write-string "~0" out)) (#\/ (write-string "~1" out)) (t (write-char c out))))))

(defun tsd-refinement-sites (root)
  "The nodes of a schema that carry refinements, in the order they are found, as (path . refinements): the path is the keywords that lead to
the node (/properties/a, /anyOf/1, /items). A node reached only through a keyword whose value is plain JSON has none."
  (let ((sites '()))
    (labels ((visit (node path)
               (let ((hidden (and (hash-table-p node) (ts-hidden node))))
                 (when hidden
                   (let ((refinements (gethash "~refine" hidden)))
                     (when refinements (push (cons path (copy-list refinements)) sites)))
                   (maphash
                    (lambda (key value)
                      (cond ((and (member key +tsd-member-keywords+ :test #'string=) (hash-table-p value))
                             (maphash (lambda (name member)
                                        (visit member (format nil "~A/~A/~A" path key (tsd-pointer-segment name))))
                                      value))
                            ((and (member key +tsd-list-keywords+ :test #'string=) (ts-array-p value))
                             (loop for member across value for index from 0
                                   do (visit member (format nil "~A/~A/~D" path key index))))
                            ((member key +tsd-child-keywords+ :test #'string=)
                             (visit value (format nil "~A/~A" path key)))))
                    node)))))
      (visit root ""))
    (nreverse sites)))

(defun tsd-validation-schema (node)
  "The schema as the host validates arguments with it: the same tree with TypeBox's ~kind appended to every node that has one."
  (cond ((ts-array-p node) (map 'simple-vector #'tsd-validation-schema node))
        ((hash-table-p node)
         (let ((out (obj)) (hidden (ts-hidden node)))
           (maphash (lambda (key member) (setf (gethash key out) (tsd-validation-schema member))) node)
           (when (and hidden (nth-value 1 (gethash "~kind" hidden)))
             (setf (gethash "~kind" out) (gethash "~kind" hidden)))
           out))
        (t node)))

(defun tsd-count-refinements (node)
  "Every refinement in a schema, wherever it hangs: more than the sites hold means one sits under a keyword whose refinements the host does not run."
  (let ((hidden (ts-hidden node)))
    (+ (if hidden (length (gethash "~refine" hidden)) 0)
       (cond ((hash-table-p node) (loop for member being the hash-values of node sum (tsd-count-refinements member)))
             ((ts-array-p node) (loop for member across node sum (tsd-count-refinements member)))
             (t 0)))))

(defun tool-schema-guard (schema)
  "Signal Pi's error for a schema that holds a bigint, which JSON.stringify cannot write."
  (when (ts-holds-big-int schema) (ts-fail "Do not know how to serialize a BigInt")))

(defun tool-schema-declare (ext name decl schema)
  "What registering the tool NAME adds to its declaration DECL for its SCHEMA: the validation schema and the refinement sites. Records the
sites the host's later requests need. Signals before anything is recorded."
  (let ((hidden (ts-hidden schema))
        (sites (tsd-refinement-sites schema)))
    (unless (= (tsd-count-refinements schema) (loop for site in sites sum (length (cdr site))))
      (error "Tool ~S registered by extension ~S has a refinement under a schema keyword whose refinements the host does not run (it runs them under properties, patternProperties, additionalProperties, items, prefixItems, additionalItems, allOf, anyOf, oneOf, not, if, then, else and $defs)."
             name (extension-name ext)))
    (when (and hidden (nth-value 1 (gethash "~kind" hidden)))
      (setf (gethash "validation_parameters" decl) (tsd-validation-schema schema)))
    (when sites
      (setf (gethash "refinements" decl)
            (map 'simple-vector (lambda (site) (obj "path" (car site) "count" (length (cdr site)))) sites)))
    (let ((state (tsd-state-of ext)))
      (sb-thread:with-mutex ((tsd-state-lock state))
        (if sites (setf (gethash name (tsd-state-sites state)) sites) (remhash name (tsd-state-sites state)))))))

(def-request "tool_refine_arguments" (ext ctx req)
  ;; TypeBox calls a refinement's check (and message) while the host validates; the host's traversal asks for each call here.
  (let* ((name (jget req "tool" ""))
         (refine (let ((r (jget req "refine"))) (if (hash-table-p r) r (obj))))
         (path (jget refine "path" ""))
         (index (jget refine "index" -1))
         (state (tsd-state-of ext))
         (refinements (sb-thread:with-mutex ((tsd-state-lock state))
                        (cdr (assoc path (gethash name (tsd-state-sites state)) :test #'equal)))))
    (unless (and (integerp index) (< -1 index (length refinements)))
      (error "tool ~S has no refinement ~S at ~S" name index path))
    (let* ((refinement (nth index refinements))
           (value (jget refine "value"))
           (passed (not (member (funcall (type-refinement-check refinement) value) '(nil :false :null))))
           (result (obj "ok" (if passed t :false))))
      (when (and (not passed) (eq (jget refine "with_message") t))
        (setf (gethash "message" result)
              (if (type-refinement-message refinement) (funcall (type-refinement-message refinement) value) "Refine Error")))
      result)))
