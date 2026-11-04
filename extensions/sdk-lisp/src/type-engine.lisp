;;;; SPDX-License-Identifier: MIT
;;;; The members of pi-ai's Type that compute a schema (pick, omit, partial, key-of, index, mapped, conditional, extends, evaluate, instantiate,
;;;; cyclic, module, script, ...) are served by TypeBox's type engine, which the host runs (internal/typebox/engine): one port for every SDK. A
;;;; call encodes its arguments in the engine's wire form, asks the host (the type.engine call, which the host answers before the extension
;;;; registers, so a schema built while the factory runs works) and decodes the schema it returns: the enumerable members in the order TypeBox
;;;; wrote them and the hidden ones. What TypeBox throws signals TYPE-SCHEMA-ERROR with its message.

(in-package #:pig)

(defparameter +te-envelope+ (string (code-char 0)))

(defun te-envelope (&rest members)
  (apply #'obj +te-envelope+ members))

(defun te-encode (value refs)
  "The wire form of VALUE (internal/typebox/engine/wire.go). REFS collects the refinements, which cross as indexes."
  (cond
    ((or (ts-array-p value) (consp value)) (map 'simple-vector (lambda (item) (te-encode item refs)) value))
    ((hash-table-p value)
     (let ((hidden (ts-hidden value))
           (members (obj)))
       (maphash (lambda (key member) (setf (gethash key members) (te-encode member refs))) value)
       (cond
         (hidden
          (let ((wire-hidden (obj)))
            (maphash (lambda (name member)
                       (setf (gethash name wire-hidden)
                             (if (equal name "~refine")
                                 (map 'simple-vector
                                      (lambda (refinement)
                                        (vector-push-extend refinement refs)
                                        (te-envelope "f" "i" (1- (length refs))))
                                      member)
                                 (te-encode member refs))))
                     hidden)
            (apply #'te-envelope "o"
                   (append (and (plusp (hash-table-count wire-hidden)) (list "h" wire-hidden)) (list "e" members)))))
         ((loop for key being the hash-keys of members thereis (and (plusp (length key)) (char= (char key 0) (char +te-envelope+ 0))))
          (te-envelope "o" "e" members))
         (t members))))
    ((big-int-p value) (te-envelope "n" "v" (princ-to-string (big-int-value value))))
    ((or (null value) (eq value :null)) :null)
    ((or (member value '(t :false)) (stringp value) (realp value)) value)
    (t (ts-fail "a ~A has no JavaScript counterpart a schema can hold" (type-of value)))))

(defun te-decode (value refs)
  (cond
    ((ts-array-p value) (map 'simple-vector (lambda (item) (let ((d (te-decode item refs))) (if (eq d :undefined) :null d))) value))
    ((not (hash-table-p value)) value)
    ((loop for key being the hash-keys of value do (return (equal key +te-envelope+)))
     (let ((kind (gethash +te-envelope+ value)))
       (cond
         ((equal kind "u") :undefined)
         ((equal kind "n") (make-big-int (parse-integer (gethash "v" value))))
         ((equal kind "f") (aref refs (gethash "i" value)))
         ((equal kind "o")
          (let ((hidden (obj)) (members (obj)))
            (let ((h (gethash "h" value)))
              (when (hash-table-p h) (maphash (lambda (name member) (setf (gethash name hidden) (te-decode member refs))) h)))
            (let ((e (gethash "e" value)))
              (when (hash-table-p e)
                (maphash (lambda (key member) (let ((d (te-decode member refs))) (unless (eq d :undefined) (setf (gethash key members) d)))) e)))
            (ts-make members hidden)))
         (t (ts-fail "the host answered an unknown envelope ~S" kind)))))
    (t (let ((members (obj)))
         (maphash (lambda (key member) (let ((d (te-decode member refs))) (unless (eq d :undefined) (setf (gethash key members) d)))) value)
         members))))

(defun te-call (member &rest arguments)
  "Run MEMBER of the type engine on ARGUMENTS and return the value it answers."
  (let* ((refs (make-array 0 :adjustable t :fill-pointer t))
         (reply (helper-call "type.engine" (obj "member" member "args" (map 'simple-vector (lambda (argument) (te-encode argument refs)) arguments))))
         (err (jget reply "error")))
    (when (hash-table-p err) (ts-fail "~A" (jget err "message" "")))
    (te-decode (jget reply "value") refs)))

(defun te-match-count (arguments low high)
  "TypeBox's Arguments.Match: these members choose their overload by the number of arguments, and any other count throws before anything runs."
  (unless (<= low (length arguments) high) (ts-fail "Invalid Arguments")))

(defun te-run (member arguments options)
  (ts-schema (apply #'te-call member (append arguments (and (hash-table-p options) (list options))))))

;;; ---------------------------------------------------------------- the members

(defun type-call (target arguments) (ts-schema (te-call "Call" target (ts-vec arguments))))
(defun type-capitalize (schema &optional options) (te-run "Capitalize" (list schema) options))
(defun type-lowercase (schema &optional options) (te-run "Lowercase" (list schema) options))
(defun type-uncapitalize (schema &optional options) (te-run "Uncapitalize" (list schema) options))
(defun type-uppercase (schema &optional options) (te-run "Uppercase" (list schema) options))

(defun type-conditional (left right true-type false-type &optional options)
  "Type.Conditional: TypeScript's `left extends right ? true-type : false-type`."
  (te-run "Conditional" (list left right true-type false-type) options))

(defun type-constructor (parameters instance-type &optional options)
  (te-run "Constructor" (list (ts-vec parameters) instance-type) options))

(defun type-constructor-parameters (schema &optional options) (te-run "ConstructorParameters" (list schema) options))
(defun type-parameters (schema &optional options) (te-run "Parameters" (list schema) options))
(defun type-instance-type (schema &optional options) (te-run "InstanceType" (list schema) options))
(defun type-return-type (schema &optional options) (te-run "ReturnType" (list schema) options))
(defun type-cyclic (defs ref &optional options) (te-run "Cyclic" (list defs ref) options))
(defun type-dependent (if-type then-type else-type &optional options) (te-run "Dependent" (list if-type then-type else-type) options))
(defun type-evaluate (schema &optional options) (te-run "Evaluate" (list schema) options))
(defun type-exclude (left right &optional options) (te-run "Exclude" (list left right) options))
(defun type-extract (left right &optional options) (te-run "Extract" (list left right) options))

(defun type-extends (inferred left right)
  "Type.Extends: an ExtendsTrue, ExtendsUnion or ExtendsFalse value; INFERRED holds the types an `infer` already captured (NIL is none)."
  (ts-schema (te-call "Extends" (or inferred (obj)) left right)))

(defun type-function (parameters return-type &optional options) (te-run "Function" (list (ts-vec parameters) return-type) options))
(defun type-generic (parameters expression) (ts-schema (te-call "Generic" (ts-vec parameters) expression)))
(defun type-identifier (name) (ts-schema (te-call "Identifier" name)))

(defun type-index (schema indexer &optional options)
  "Type.Index: TypeScript's `schema[indexer]`; INDEXER is a schema or a vector of keys."
  (te-run "Index" (list schema indexer) options))

(defun type-infer (&rest arguments)
  "Type.Infer (name) or (name extends); any other argument count signals TYPE-SCHEMA-ERROR \"Invalid Arguments\", as Pi's does."
  (te-match-count arguments 1 2)
  (ts-schema (apply #'te-call "Infer" arguments)))

(defun type-instantiate (context schema) (ts-schema (te-call "Instantiate" (or context (obj)) schema)))
(defun type-interface (heritage properties &optional options) (te-run "Interface" (list (ts-vec heritage) properties) options))
(defun type-key-of (schema &optional options) (te-run "KeyOf" (list schema) options))

(defun type-mapped (identifier schema as property &optional options)
  "Type.Mapped: TypeScript's `{ [identifier in schema as AS]: property }`."
  (te-run "Mapped" (list identifier schema as property) options))

(defun type-module (&optional declarations options) (te-run "Module" (list (or declarations (obj))) options))
(defun type-non-nullable (schema &optional options) (te-run "NonNullable" (list schema) options))
(defun type-omit (schema keys &optional options) (te-run "Omit" (list schema keys) options))
(defun type-pick (schema keys &optional options) (te-run "Pick" (list schema keys) options))

(defun type-parameter (&rest arguments)
  "Type.Parameter (name), (name extends) or (name extends equals); any other argument count signals TYPE-SCHEMA-ERROR \"Invalid Arguments\"."
  (te-match-count arguments 1 3)
  (ts-schema (apply #'te-call "Parameter" arguments)))

(defun type-partial (schema &optional options) (te-run "Partial" (list schema) options))
(defun type-required (schema &optional options) (te-run "Required" (list schema) options))
(defun type-readonly-object (schema &optional options) (te-run "ReadonlyObject" (list schema) options))
(defun type-readonly-type (schema &optional options) (te-run "ReadonlyType" (list schema) options))
(defun type-record-key (record) (ts-schema (te-call "RecordKey" record)))
(defun type-record-pattern (record) (te-call "RecordPattern" record))
(defun type-record-value (record) (ts-schema (te-call "RecordValue" record)))
(defun type-rest (schema) (ts-schema (te-call "Rest" schema)))

(defun type-script (&rest arguments)
  "Type.Script (script), (script options), (context script) or (context script options): TypeScript type text, parsed and instantiated. Any
other argument count signals TYPE-SCHEMA-ERROR as Pi's does."
  (te-match-count arguments 1 3)
  (ts-schema (apply #'te-call "Script" arguments)))

(defun type-template-literal (input &optional options) (te-run "TemplateLiteral" (list input) options))
(defun type-this (&optional options) (te-run "This" '() options))
(defun type-tuple (types &optional options) (te-run "Tuple" (list (ts-vec types)) options))

;;; ---------------------------------------------------------------- Type.ExtendsResult

(defun te-outcome (kind inferred)
  (ts-create kind (and inferred (list "inferred" inferred)) nil))

(defun type-extends-true (&optional inferred) (te-outcome "ExtendsTrue" (or inferred (obj))))
(defun type-extends-union (&optional inferred) (te-outcome "ExtendsUnion" (or inferred (obj))))
(defun type-extends-false () (te-outcome "ExtendsFalse" nil))

(defun te-has-inferred (value)
  (multiple-value-bind (inferred found) (ts-member value "inferred") (and found (type-schema-p inferred))))

(defun type-extends-union-p (value) (and (type-kind-p value "ExtendsUnion") (te-has-inferred value)))
(defun type-extends-true-p (value) (and (type-kind-p value "ExtendsTrue") (te-has-inferred value)))
(defun type-extends-false-p (value) (type-kind-p value "ExtendsFalse"))
(defun type-extends-true-like-p (value) (or (type-extends-union-p value) (type-extends-true-p value)))

(defun type-extends-match (result on-true on-false)
  "Type.ExtendsResult.Match: ON-TRUE called with the inferred types of a true-like RESULT, ON-FALSE with no arguments otherwise."
  (if (type-extends-true-like-p result)
      (funcall on-true (ts-member result "inferred"))
      (funcall on-false)))
