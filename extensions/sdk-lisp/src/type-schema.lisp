;;;; SPDX-License-Identifier: MIT
;;;; pi-ai's Type (TypeBox 1.3.27's builder namespace). A schema is the hash-table of JSON Schema members in the order TypeBox wrote them, so
;;;; (to-json schema) is what JSON.stringify of Pi's value writes. TypeBox's hidden members (~kind, ~optional, ~readonly, ~immutable, ~unsafe,
;;;; ~refine) live in a weak side table keyed by the schema: JSON never sees them, the constructors and guards read them. A schema is never
;;;; changed after it is built; every constructor returns a new one. A JSON array can be a schema too (Type.unsafe, Type.optional of one).
;;;; The Codec family of Type is not served (divergence D198).

(in-package #:pig)

(define-condition type-schema-error (pig-error)
  ((message :initarg :message :reader type-schema-error-message))
  (:report (lambda (c s) (write-string (type-schema-error-message c) s)))
  (:documentation "What TypeBox throws (the engine's message, \"Invalid Arguments\", ...), or a schema that cannot be written as JSON."))

(defun ts-fail (control &rest arguments)
  (error 'type-schema-error :message (apply #'format nil control arguments)))

(defstruct (big-int (:constructor %make-big-int (value)))
  "A JavaScript bigint a schema holds as a member (the const of Type.literal of a bigint, of Type.script(\"1n\")): only writing the schema as
JSON fails, with Pi's message. A Lisp integer is a JavaScript number."
  (value 0 :type integer))

(defun make-big-int (value)
  (unless (integerp value) (ts-fail "Cannot convert ~S to a BigInt" value))
  (%make-big-int value))

(defmethod print-object ((b big-int) stream)
  (format stream "BigInt(~D)" (big-int-value b)))

;;; ---------------------------------------------------------------- hidden members

(defvar *ts-hidden* (make-hash-table :test 'eq :weakness :key :synchronized t))

(defun ts-array-p (value) (and (vectorp value) (not (stringp value))))

(defun type-schema-p (value)
  "TypeBox's IsSchema: any object (an array counts)."
  (or (hash-table-p value) (ts-array-p value)))

(defun ts-hidden (value)
  "The hash-table of the hidden members of VALUE, NIL for a value that has none."
  (and (type-schema-p value) (values (gethash value *ts-hidden*))))

(defun ts-make (value hidden)
  (setf (gethash value *ts-hidden*) hidden)
  value)

(defun ts-schema (value)
  "A result that is an object, as a schema: a plain object has no hidden members."
  (when (and (hash-table-p value) (not (ts-hidden value)))
    (ts-make value (obj)))
  value)

(defun ts-member (value name)
  "(values member found) of an own member of VALUE, hidden or enumerable."
  (let ((hidden (ts-hidden value)))
    (multiple-value-bind (member found) (if hidden (gethash name hidden) (values nil nil))
      (cond (found (values member t))
            ((hash-table-p value) (gethash name value))
            (t (values nil nil))))))

(defun ts-has-member (value name)
  (nth-value 1 (ts-member value name)))

(defun ts-number-p (value)
  (and (realp value) (not (and (floatp value) (or (sb-ext:float-nan-p value) (sb-ext:float-infinity-p value))))))

(defun ts-js-number (value)
  "A JavaScript number as JSON writes it: an integral float is the integer."
  (if (and (floatp value) (= value (ffloor value)) (< (abs value) 1d21)) (values (truncate value)) value))

(defun ts-vec (sequence)
  (if (listp sequence) (coerce sequence 'simple-vector) (copy-seq sequence)))

(defun ts-copy-table (table)
  (let ((copy (make-hash-table :test 'equal)))
    (maphash (lambda (key value) (setf (gethash key copy) value)) table)
    copy))

(defun ts-merge (table options)
  (when (hash-table-p options)
    (maphash (lambda (key value) (setf (gethash key table) value)) options))
  table)

;;; ---------------------------------------------------------------- property order

(defun ts-index-key (key)
  "The array index a canonical array-index key stands for: a JavaScript object orders those keys before every other, ascending."
  (and (stringp key) (<= 1 (length key) 10)
       (every (lambda (c) (char<= #\0 c #\9)) key)
       (or (= (length key) 1) (char/= (char key 0) #\0))
       (let ((n (parse-integer key))) (and (< n (1- (expt 2 32))) n))))

(defun ts-js-order (table)
  "TABLE with its keys in the order a JavaScript object enumerates them."
  (let ((indexes '()) (others '()))
    (maphash (lambda (key value) (declare (ignore value)) (if (ts-index-key key) (push key indexes) (push key others))) table)
    (if (null indexes)
        table
        (let ((ordered (make-hash-table :test 'equal)))
          (dolist (key (sort indexes #'< :key #'ts-index-key)) (setf (gethash key ordered) (gethash key table)))
          (dolist (key (nreverse others)) (setf (gethash key ordered) (gethash key table)))
          ordered))))

(defun ts-keys (table)
  (let ((keys '())) (maphash (lambda (key value) (declare (ignore value)) (push key keys)) table) (nreverse keys)))

;;; ---------------------------------------------------------------- Memory.Create and Memory.Update

(defun ts-create (kind enumerable options)
  "TypeBox's Memory.Create: {...enumerable, ...options} (ENUMERABLE alternates keys and values) with the hidden ~kind."
  (let ((merged (make-hash-table :test 'equal)))
    (loop for (key value) on enumerable by #'cddr do (setf (gethash key merged) value))
    (ts-make (ts-js-order (ts-merge merged options)) (obj "~kind" kind))))

(defun ts-update (current hidden enumerable)
  "TypeBox's Memory.Update: a copy of CURRENT with the HIDDEN members set and the ENUMERABLE ones assigned."
  (cond
    ((ts-array-p current)
     ;; TypeBox's Clone of an array drops every non-index member: only the length survives an update, and ~unsafe or ~refine when set by it.
     (let ((kept (obj "length" (length current))))
       (when (and hidden (or (ts-has-member hidden "~unsafe") (ts-has-member hidden "~refine")))
         (ts-merge kept hidden))
       (ts-make (copy-seq (coerce current 'simple-vector)) kept)))
    ((hash-table-p current)
     (let ((merged (ts-copy-table current))
           (kept (let ((existing (ts-hidden current))) (if existing (ts-copy-table existing) (obj)))))
       (ts-merge kept hidden)
       (ts-merge merged enumerable)
       (ts-make (ts-js-order merged) kept)))
    (t (ts-fail "cannot update ~S, a schema is an object" current))))

;;; ---------------------------------------------------------------- JSON

(defun ts-holds-big-int (value)
  (cond ((big-int-p value) t)
        ((hash-table-p value) (loop for member being the hash-values of value thereis (ts-holds-big-int member)))
        ((ts-array-p value) (some #'ts-holds-big-int value))))

(defun type-to-json (value)
  "JSON.stringify of a schema (any value of hash-tables, vectors, strings, numbers and the JSON constants). A schema that holds a bigint
signals TYPE-SCHEMA-ERROR \"Do not know how to serialize a BigInt\", as Pi's JSON.stringify throws."
  (when (ts-holds-big-int value) (ts-fail "Do not know how to serialize a BigInt"))
  (to-json value))

;;; ---------------------------------------------------------------- guards

(defun type-kind-p (value kind)
  "Whether VALUE is a schema of the TypeBox KIND (\"String\", \"Object\", ...)."
  (and (type-schema-p value)
       (multiple-value-bind (member found) (ts-member value "~kind") (and found (equal member kind)))))

(defmacro ts-def-guards (&rest specs)
  `(progn ,@(loop for (name kind) in specs
                  collect `(defun ,name (value) ,(format nil "Whether VALUE is a ~A schema." kind) (type-kind-p value ,kind)))))

(ts-def-guards
 (type-any-p "Any") (type-array-p "Array") (type-big-int-p "BigInt") (type-boolean-p "Boolean") (type-enum-p "Enum")
 (type-integer-p "Integer") (type-intersect-p "Intersect") (type-literal-p "Literal") (type-never-p "Never") (type-null-p "Null")
 (type-number-p "Number") (type-object-p "Object") (type-record-p "Record") (type-ref-p "Ref") (type-string-p "String")
 (type-symbol-p "Symbol") (type-undefined-p "Undefined") (type-union-p "Union") (type-unknown-p "Unknown") (type-void-p "Void")
 (type-call-p "Call") (type-constructor-p "Constructor") (type-cyclic-p "Cyclic") (type-dependent-p "Dependent")
 (type-function-p "Function") (type-generic-p "Generic") (type-identifier-p "Identifier") (type-infer-p "Infer")
 (type-parameter-p "Parameter") (type-rest-p "Rest") (type-template-literal-p "TemplateLiteral") (type-this-p "This")
 (type-tuple-p "Tuple"))

(defun type-optional-p (value) (and (type-schema-p value) (ts-has-member value "~optional")))
(defun type-readonly-p (value) (and (type-schema-p value) (ts-has-member value "~readonly")))
(defun type-immutable-p (value) (and (type-schema-p value) (ts-has-member value "~immutable")))

(defun type-unsafe-p (value)
  (and (not (ts-array-p value))
       (multiple-value-bind (member found) (ts-member value "~unsafe") (and found (or (null member) (eq member :null))))))

(defun type-enum-value-p (value) (or (stringp value) (ts-number-p value)))

(defun type-refine-p (value)
  (and (hash-table-p value) (let ((hidden (ts-hidden value))) (and hidden (gethash "~refine" hidden) t))))

;;; ---------------------------------------------------------------- constructors

(defun type-any (&optional options) (ts-create "Any" '() options))
(defun type-unknown (&optional options) (ts-create "Unknown" '() options))
(defun type-string (&optional options) (ts-create "String" '("type" "string") options))
(defun type-number (&optional options) (ts-create "Number" '("type" "number") options))
(defun type-integer (&optional options) (ts-create "Integer" '("type" "integer") options))
(defun type-boolean (&optional options) (ts-create "Boolean" '("type" "boolean") options))
(defun type-null (&optional options) (ts-create "Null" '("type" "null") options))
(defun type-never (&optional options) (ts-create "Never" (list "not" (obj)) options))
(defun type-undefined (&optional options) (ts-create "Undefined" '("type" "undefined") options))
(defun type-void (&optional options) (ts-create "Void" '("type" "void") options))
(defun type-symbol (&optional options) (ts-create "Symbol" '("type" "symbol") options))
(defun type-big-int (&optional options) (ts-create "BigInt" '("type" "bigint") options))
(defun type-ref (ref &optional options) (ts-create "Ref" (list "$ref" ref) options))
(defun type-array (items &optional options) (ts-create "Array" (list "type" "array" "items" items) options))
(defun type-union (any-of &optional options) (ts-create "Union" (list "anyOf" (ts-vec any-of)) options))
(defun type-intersect (types &optional options) (ts-create "Intersect" (list "allOf" (ts-vec types)) options))

(defun type-object (&optional properties options)
  "Type.Object: PROPERTIES is a hash-table of name to schema. \"required\" lists, in key order, the properties that are not optional, and is left
out when there are none. Keys that are array indexes come first and ascending, as in a JavaScript object."
  (let* ((ordered (ts-js-order (if (hash-table-p properties) properties (obj))))
         (required (loop for key in (ts-keys ordered) unless (type-optional-p (gethash key ordered)) collect key)))
    (ts-create "Object" `("type" "object" ,@(and required (list "required" (ts-vec required))) "properties" ,ordered) options)))

(defun type-optional (schema) (ts-update schema (obj "~optional" t) nil))
(defun type-readonly (schema) (ts-update schema (obj "~readonly" t) nil))
(defun type-immutable (schema) (ts-update schema (obj "~immutable" t) nil))

(defun type-unsafe (schema)
  "Type.Unsafe: a copy of the object (or array) SCHEMA marked ~unsafe; any other value signals TYPE-SCHEMA-ERROR."
  (ts-update schema (obj "~unsafe" :null) nil))

(defstruct (type-refinement (:constructor make-type-refinement (check message)))
  "One entry of a schema's hidden ~refine list: (funcall CHECK value) decides whether a value passes and (funcall MESSAGE value) words the
failure (NIL is TypeBox's \"Refine Error\"). Pi runs them while it validates a tool call's arguments."
  check message)

(defun type-refine (&rest arguments)
  "Type.Refine (schema check) or (schema check error): a copy of the schema whose hidden ~refine list has the refinement appended; the schema's
JSON does not change. The refinements run when the host validates a tool call's arguments (see TOOL). TypeBox matches the arguments by count, so
any other count signals TYPE-SCHEMA-ERROR \"Invalid Arguments\", as Pi's does."
  (unless (<= 2 (length arguments) 3) (ts-fail "Invalid Arguments"))
  (destructuring-bind (schema check &optional message) arguments
    (let ((existing (and (hash-table-p schema) (ts-hidden schema) (gethash "~refine" (ts-hidden schema)))))
      (ts-update schema (obj "~refine" (append existing (list (make-type-refinement check message)))) nil))))

(defun ts-deferred (action parameters options)
  (ts-create "Deferred" (list "type" "deferred" "action" action "parameters" (ts-vec parameters)
                              "options" (if (hash-table-p options) (ts-copy-table options) (obj)))
             nil))

(defun type-with (schema &optional options)
  "Type.With: a copy with the options assigned (a Ref is not evaluated: the result is TypeBox's deferred With)."
  (if (type-ref-p schema)
      (ts-deferred "With" (list schema (or options :null)) nil)
      (ts-update schema nil options)))

(defun type-literal (value &optional options)
  "Type.Literal: {\"type\": string|number|boolean|bigint, \"const\": VALUE}. Anything else signals TYPE-SCHEMA-ERROR \"Invalid Literal value\"."
  (multiple-value-bind (name constant)
      (cond ((stringp value) (values "string" value))
            ((member value '(t :false)) (values "boolean" value))
            ((big-int-p value) (values "bigint" value))
            ((ts-number-p value) (values "number" (ts-js-number value)))
            (t (ts-fail "Invalid Literal value")))
    (ts-create "Literal" (list "type" name "const" constant) options)))

(defun ts-numeric-string-p (text)
  "Whether JavaScript's Number reads TEXT (trimmed of white space, non-empty) as a number: a decimal, an infinity or a 0x, 0o, 0b integer."
  (flet ((digits (start end radix) (and (< start end) (loop for i from start below end always (digit-char-p (char text i) radix)))))
    (let* ((n (length text)) (start (if (and (plusp n) (find (char text 0) "+-")) 1 0)))
      (cond
        ((string= text "Infinity" :start1 start) t)
        ((and (= start 0) (> n 2) (char= (char text 0) #\0) (find (char text 1) "xXoObB"))
         (digits 2 n (ecase (char-downcase (char text 1)) (#\x 16) (#\o 8) (#\b 2))))
        (t
         (let* ((e (position-if (lambda (c) (find c "eE")) text :start start))
                (mantissa-end (or e n))
                (dot (position #\. text :start start :end mantissa-end))
                (int-end (or dot mantissa-end))
                (int-ok (loop for i from start below int-end always (digit-char-p (char text i))))
                (frac-ok (or (null dot) (loop for i from (1+ dot) below mantissa-end always (digit-char-p (char text i)))))
                (has-digit (or (> int-end start) (and dot (> mantissa-end (1+ dot)))))
                (exp-ok (or (null e)
                            (let ((s (if (and (< (1+ e) n) (find (char text (1+ e)) "+-")) (+ e 2) (1+ e)))) (digits s n 10)))))
           (and int-ok frac-ok has-digit exp-ok)))))))

(defparameter +ts-js-space+
  (coerce (mapcar #'code-char '(9 10 11 12 13 32 #xa0 #x1680 #x2000 #x2001 #x2002 #x2003 #x2004 #x2005 #x2006 #x2007 #x2008 #x2009 #x200a
                                #x2028 #x2029 #x202f #x205f #x3000 #xfeff))
          'string))

(defun ts-nan-string-p (key)
  "isNaN(KEY): whether Number(KEY) is NaN."
  (let ((trimmed (string-trim +ts-js-space+ key)))
    (and (plusp (length trimmed)) (not (ts-numeric-string-p trimmed)))))

(defun type-enum (value &optional options)
  "Type.Enum: {\"enum\": values}. VALUE is a vector of values, or a hash-table in the manner of a TypeScript enum, whose values for the keys
that are not numbers are the enum's (a numeric TypeScript enum also maps each number back to its name, which is dropped)."
  (let ((values (cond ((hash-table-p value)
                       (let ((ordered (ts-js-order value)))
                         (ts-vec (loop for key in (ts-keys ordered) when (ts-nan-string-p key) collect (gethash key ordered)))))
                      ((or (ts-array-p value) (listp value)) (ts-vec value))
                      (t (ts-fail "Type.enum takes a vector or a hash-table")))))
    (ts-create "Enum" (list "enum" values) options)))

;;; ---------------------------------------------------------------- Record

(defparameter +ts-integer-key+ "^-?(?:0|[1-9][0-9]*)$")
(defparameter +ts-number-key+ "^-?(?:0|[1-9][0-9]*)(?:\\.[0-9]+)?$")
(defparameter +ts-string-key+ "^.*$")

(defun ts-create-record (pattern value)
  (ts-create "Record" (list "type" "object" "patternProperties" (obj pattern value)) nil))

(defun ts-flatten-union (any-of)
  (loop for member in (if (ts-array-p any-of) (coerce any-of 'list) '())
        append (if (type-union-p member) (ts-flatten-union (ts-member member "anyOf")) (list member))))

(declaim (ftype function te-call))

(defun ts-record-from-key (key value)
  (cond
    ((type-any-p key) (ts-create-record +ts-string-key+ value))
    ((type-boolean-p key) (type-object (obj "true" value "false" value)))
    ;; the key kinds TypeBox evaluates with its type engine, which the host runs
    ((or (type-enum-p key) (type-intersect-p key) (type-kind-p key "TemplateLiteral")) (te-call "Record" key value))
    ((type-integer-p key) (ts-create-record +ts-integer-key+ value))
    ((type-literal-p key)
     (let ((constant (ts-member key "const")))
       (cond ((stringp constant) (type-object (obj constant value)))
             ((member constant '(t :false)) (type-object (obj (if (eq constant t) "true" "false") value)))
             ((ts-number-p constant) (type-object (obj (to-json constant) value)))
             (t (type-object)))))
    ((type-number-p key) (ts-create-record +ts-number-key+ value))
    ((type-union-p key)
     (let ((members (ts-flatten-union (ts-member key "anyOf"))))
       (if (some (lambda (m) (or (type-string-p m) (type-number-p m) (type-integer-p m))) members)
           (ts-create-record +ts-string-key+ value)
           (let ((properties (obj)))
             (dolist (member members)
               (when (type-literal-p member)
                 (let ((constant (ts-member member "const")))
                   (cond ((stringp constant) (setf (gethash constant properties) value))
                         ((ts-number-p constant) (setf (gethash (to-json constant) properties) value))))))
             (type-object properties)))))
    ((type-string-p key)
     (multiple-value-bind (pattern found) (ts-member key "pattern")
       (ts-create-record (if (and found (stringp pattern)) pattern +ts-string-key+) value)))
    (t (type-object))))

(defun type-record (key value &optional options)
  "Type.Record: for a string, number or integer key {\"type\": \"object\", \"patternProperties\": {pattern: VALUE}}; for a literal, boolean or
union-of-literals key the object with those properties. A key TypeBox evaluates with its type engine (an enum, an intersection, a template
literal) is evaluated by the host."
  (if (type-ref-p key)
      (ts-deferred "Record" (list key value) options)
      (ts-update (ts-record-from-key key value) nil options)))
