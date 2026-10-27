;;;; SPDX-License-Identifier: MIT
;;;; The extension component kit (D107): Pi's tui components described as a view the host renders with its tui ports. Build a tree of
;;;; nodes (KIT-CONTAINER, KIT-TEXT, ...), wrap it in KIT-VIEW and hand it to a view surface (ctx-custom, ctx-set-widget,
;;;; ctx-set-header-view/ctx-set-footer-view, the view renderers). Constructors take upstream's positional arguments and defaults; upstream's
;;;; option bags are keywords. A style closure upstream takes is a theme token name here; the host validates tokens, colors and ids.
;;;; A keyword that is a tri-state (omitted, true, false) takes NIL, T or :FALSE; one of a list or string that may be empty takes NIL for
;;;; absent. This file is the Python kit.py.

(in-package #:pig)

(defconstant +kit-max-depth+ 64 "The host's depth bound: deeper trees are invalid.")

;;; ---------------------------------------------------------------- SHA-256 and base64 (image refs and image transport)

(defparameter *kit-sha-primes*
  (loop with found = '() for n from 2 while (< (length found) 64)
        when (notany (lambda (p) (zerop (mod n p))) found) do (setf found (append found (list n)))
        finally (return found)))

(defun kit-int-root (n k)
  "floor of the K-th root of N."
  (let ((lo 0) (hi (ash 1 (1+ (ceiling (integer-length n) k)))))
    (loop while (< lo hi)
          do (let ((mid (ash (+ lo hi 1) -1)))
               (if (<= (expt mid k) n) (setf lo mid) (setf hi (1- mid)))))
    lo))

(defparameter *kit-sha-k*
  (map 'simple-vector (lambda (p) (ldb (byte 32 0) (kit-int-root (ash p 96) 3))) *kit-sha-primes*))
(defparameter *kit-sha-h*
  (map 'simple-vector (lambda (p) (ldb (byte 32 0) (isqrt (ash p 64)))) (subseq *kit-sha-primes* 0 8)))

(defun kit-sha256-hex (octets)
  "The lowercase hex SHA-256 of the octet vector OCTETS."
  (let* ((len (length octets))
         (padded (* 64 (ceiling (+ len 9) 64)))
         (m (make-array padded :element-type '(unsigned-byte 8) :initial-element 0))
         (h (copy-seq *kit-sha-h*))
         (w (make-array 64)))
    (replace m octets)
    (setf (aref m len) #x80)
    (dotimes (i 8) (setf (aref m (- padded 1 i)) (ldb (byte 8 (* 8 i)) (* 8 len))))
    (flet ((rotr (x n) (logior (ash x (- n)) (ldb (byte 32 0) (ash x (- 32 n))))))
      (loop for base from 0 below padded by 64
            do (dotimes (i 16)
                 (setf (aref w i) (logior (ash (aref m (+ base (* 4 i))) 24) (ash (aref m (+ base (* 4 i) 1)) 16)
                                          (ash (aref m (+ base (* 4 i) 2)) 8) (aref m (+ base (* 4 i) 3)))))
               (loop for i from 16 below 64
                     do (let* ((w15 (aref w (- i 15))) (w2 (aref w (- i 2)))
                               (s0 (logxor (rotr w15 7) (rotr w15 18) (ash w15 -3)))
                               (s1 (logxor (rotr w2 17) (rotr w2 19) (ash w2 -10))))
                          (setf (aref w i) (ldb (byte 32 0) (+ (aref w (- i 16)) s0 (aref w (- i 7)) s1)))))
               (let ((a (aref h 0)) (b (aref h 1)) (c (aref h 2)) (d (aref h 3))
                     (e (aref h 4)) (f (aref h 5)) (g (aref h 6)) (hh (aref h 7)))
                 (dotimes (i 64)
                   (let* ((s1 (logxor (rotr e 6) (rotr e 11) (rotr e 25)))
                          (ch (logxor (logand e f) (logand (ldb (byte 32 0) (lognot e)) g)))
                          (t1 (ldb (byte 32 0) (+ hh s1 ch (aref *kit-sha-k* i) (aref w i))))
                          (s0 (logxor (rotr a 2) (rotr a 13) (rotr a 22)))
                          (maj (logxor (logand a b) (logand a c) (logand b c)))
                          (t2 (ldb (byte 32 0) (+ s0 maj))))
                     (setf hh g g f f e e (ldb (byte 32 0) (+ d t1)) d c c b b a a (ldb (byte 32 0) (+ t1 t2)))))
                 (loop for value in (list a b c d e f g hh) for i from 0
                       do (setf (aref h i) (ldb (byte 32 0) (+ (aref h i) value)))))))
    (format nil "~(~{~8,'0X~}~)" (coerce h 'list))))

(defun kit-base64 (octets)
  (let ((alphabet "ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz0123456789+/")
        (out (make-string-output-stream)))
    (loop for i from 0 below (length octets) by 3
          do (let* ((n (- (length octets) i))
                    (v (logior (ash (aref octets i) 16) (if (> n 1) (ash (aref octets (+ i 1)) 8) 0)
                               (if (> n 2) (aref octets (+ i 2)) 0))))
               (write-char (char alphabet (ldb (byte 6 18) v)) out)
               (write-char (char alphabet (ldb (byte 6 12) v)) out)
               (write-char (if (> n 1) (char alphabet (ldb (byte 6 6) v)) #\=) out)
               (write-char (if (> n 2) (char alphabet (ldb (byte 6 0) v)) #\=) out)))
    (get-output-stream-string out)))

;;; ---------------------------------------------------------------- nodes and the encoder

(defstruct (kit-img (:constructor kit-make-img (data mime-type ref))) data mime-type ref)

(defun kit-image-data (data mime-type)
  (unless (typep data '(vector (unsigned-byte 8))) (error 'type-error :datum data :expected-type '(vector (unsigned-byte 8))))
  (let ((octets (coerce data '(simple-array (unsigned-byte 8) (*)))))
    (kit-make-img octets (princ-to-string mime-type) (kit-sha256-hex octets))))

(defstruct (kit-node (:constructor %kit-node (kind state))) kind state)

(defun kit-new (kind &rest plist)
  (let ((state (make-hash-table :test 'eq)))
    (loop for (key value) on plist by #'cddr do (setf (gethash key state) value))
    (%kit-node kind state)))

(defun kit-get (node key) (gethash key (kit-node-state node)))
(defun kit-put (node key value) (setf (gethash key (kit-node-state node)) value) nil)

(defstruct (kit-enc (:constructor kit-make-enc (frontend))) frontend (images '()))

(defun kit-enc-add-image (enc image)
  (unless (find (kit-img-ref image) (kit-enc-images enc) :key #'kit-img-ref :test #'string=)
    (setf (kit-enc-images enc) (append (kit-enc-images enc) (list image)))))

(defvar *kit-encoders* (make-hash-table :test 'equal) "Node kind -> (lambda (node enc depth out)) adding the kind's fields to OUT.")

(defmacro def-kit-encoder (kind (node enc depth out) &body body)
  `(setf (gethash ,kind *kit-encoders*)
         (lambda (,node ,enc ,depth ,out) (declare (ignorable ,node ,enc ,depth ,out)) ,@body)))

(defun kit-enc-node (enc node depth)
  (when (>= depth +kit-max-depth+) (error "kit: view deeper than ~D nodes" +kit-max-depth+))
  (unless (kit-node-p node) (error "kit: ~S is not a kit node" node))
  (let ((out (obj "kind" (kit-node-kind node))))
    (funcall (gethash (kit-node-kind node) *kit-encoders*) node enc depth out)
    out))

(defun kit-out (out key value)
  "Set OUT's KEY to VALUE unless it is NIL (Python's _put)."
  (when value (setf (gethash key out) value)))

(defun kit-out-children (enc out children depth)
  (when children
    (setf (gethash "children" out) (map 'simple-vector (lambda (c) (kit-enc-node enc c (1+ depth))) children))))

(defun kit-vector (sequence) (coerce sequence 'simple-vector))

(defmacro kit-out-state (node out &rest specs)
  "Copy node state keys to OUT: each spec is (state-key \"wire\") for a value set when non-NIL."
  `(progn ,@(mapcar (lambda (spec) `(kit-out ,out ,(second spec) (kit-get ,node ,(first spec)))) specs)))

;;; leaves: plain wire objects --------------------------------------------------------------------

(defun kit-select-item (value label &optional description)
  (let ((item (obj "value" value "label" label))) (kit-out item "description" description) item))

(defun kit-select-layout (&key min-primary-column-width max-primary-column-width)
  (let ((layout (obj)))
    (kit-out layout "minPrimaryColumnWidth" min-primary-column-width)
    (kit-out layout "maxPrimaryColumnWidth" max-primary-column-width)
    layout))

(defun kit-text-style (&key color bg-color bold italic strikethrough underline)
  "Markdown's DefaultTextStyle with theme tokens for its color functions."
  (let ((style (obj)))
    (kit-out style "color" color) (kit-out style "bgColor" bg-color)
    (when bold (setf (gethash "bold" style) t)) (when italic (setf (gethash "italic" style) t))
    (when strikethrough (setf (gethash "strikethrough" style) t)) (when underline (setf (gethash "underline" style) t))
    style))

(defun kit-loader-indicator (&key frames interval-ms)
  "LoaderIndicatorOptions. FRAMES NIL leaves the frames alone; an empty vector hides the indicator."
  (let ((indicator (obj)))
    (when frames (setf (gethash "frames" indicator) (kit-vector frames)))
    (kit-out indicator "intervalMs" interval-ms)
    indicator))

(defun kit-list-item (label &optional detail columns)
  (let ((item (obj "label" label)))
    (kit-out item "detail" detail)
    (when columns (setf (gethash "columns" item) (map 'simple-vector #'princ-to-string columns)))
    item))

(defun kit-list (items &optional (selected -1))
  "The list annotation of a KIT-LINES node: one item per row; SELECTED is the selected item's index, -1 for none."
  (obj "items" (kit-vector items) "selectedIndex" selected))

(defun kit-slash-command (name &optional (description "") (argument-hint ""))
  (let ((command (obj "name" name)))
    (when (plusp (length description)) (setf (gethash "description" command) description))
    (when (plusp (length argument-hint)) (setf (gethash "argumentHint" command) argument-hint))
    command))

(defun kit-autocomplete (&optional commands (base-path ""))
  "CombinedAutocompleteProvider(commands, basePath): the host runs its completion as the editor asks."
  (let ((provider (obj)))
    (when commands (setf (gethash "commands" provider) (kit-vector commands)))
    (when (plusp (length base-path)) (setf (gethash "basePath" provider) base-path))
    provider))

(defun kit-text-block (text)
  (let ((item (obj "type" "text"))) (when (plusp (length text)) (setf (gethash "text" item) text)) item))
(defun kit-thinking-block (thinking)
  (let ((item (obj "type" "thinking"))) (when (plusp (length thinking)) (setf (gethash "thinking" item) thinking)) item))
(defun kit-tool-call-block ()
  "A tool call content block: the component draws none; it separates thinking runs."
  (obj "type" "toolCall"))

(defun kit-message (content &key (stop-reason "") (error-message ""))
  "The part of an AssistantMessage the component draws. STOP-REASON is \"stop\" (or \"\"), \"length\", \"toolUse\", \"error\" or \"aborted\"."
  (let ((message (obj "content" (kit-vector content))))
    (when (plusp (length stop-reason)) (setf (gethash "stopReason" message) stop-reason))
    (when (plusp (length error-message)) (setf (gethash "errorMessage" message) error-message))
    message))

;;; containers --------------------------------------------------------------------------------------

(defun kit-container (&optional children) (kit-new "container" :children (copy-list children)))
(def-kit-encoder "container" (node enc depth out) (kit-out-children enc out (kit-get node :children) depth))

(defun kit-container-add-child (container child)
  "addChild; returns the container."
  (kit-put container :children (append (kit-get container :children) (list child))) container)

(defun kit-box (&optional (padding-x 1) (padding-y 1) &key bg children)
  (kit-new "box" :padding-x padding-x :padding-y padding-y :bg bg :children (copy-list children)))
(def-kit-encoder "box" (node enc depth out)
  (kit-out-children enc out (kit-get node :children) depth)
  (setf (gethash "paddingX" out) (kit-get node :padding-x) (gethash "paddingY" out) (kit-get node :padding-y))
  (kit-out out "bg" (kit-get node :bg)))

(defun kit-box-add-child (box child) (kit-container-add-child box child))

(defun kit-text (&optional (text "") (padding-x 1) (padding-y 1) &key bg)
  (kit-new "text" :text text :padding-x padding-x :padding-y padding-y :bg bg))
(def-kit-encoder "text" (node enc depth out)
  (setf (gethash "text" out) (kit-get node :text) (gethash "paddingX" out) (kit-get node :padding-x)
        (gethash "paddingY" out) (kit-get node :padding-y))
  (kit-out out "bg" (kit-get node :bg)))

(defun kit-truncated-text (text &optional (padding-x 0) (padding-y 0))
  (kit-new "truncated-text" :text text :padding-x padding-x :padding-y padding-y))
(def-kit-encoder "truncated-text" (node enc depth out)
  (setf (gethash "text" out) (kit-get node :text) (gethash "paddingX" out) (kit-get node :padding-x)
        (gethash "paddingY" out) (kit-get node :padding-y)))

(defun kit-markdown (text &optional (padding-x 0) (padding-y 0) &key default-text-style render-latex)
  (kit-new "markdown" :text text :padding-x padding-x :padding-y padding-y :style default-text-style :latex render-latex))
(def-kit-encoder "markdown" (node enc depth out)
  (setf (gethash "text" out) (kit-get node :text) (gethash "paddingX" out) (kit-get node :padding-x)
        (gethash "paddingY" out) (kit-get node :padding-y))
  (kit-out out "defaultTextStyle" (kit-get node :style))
  (kit-out out "renderLatex" (kit-get node :latex)))

(defun kit-spacer (&optional (lines 1)) (kit-new "spacer" :lines lines))
(def-kit-encoder "spacer" (node enc depth out) (setf (gethash "lines" out) (kit-get node :lines)))

(defun kit-dynamic-border (&optional (color "border")) (kit-new "dynamic-border" :color color))
(def-kit-encoder "dynamic-border" (node enc depth out) (setf (gethash "color" out) (kit-get node :color)))

(defun kit-select-list (id items &optional (max-visible 5) &key layout selected-index filter)
  "SelectList; ID names it in events and the view's focus. SELECTED-INDEX and FILTER apply when they differ from the last frame's."
  (kit-new "select-list" :id id :items (copy-list items) :max-visible max-visible :layout layout :selected-index selected-index :filter filter))
(def-kit-encoder "select-list" (node enc depth out)
  (setf (gethash "id" out) (kit-get node :id) (gethash "items" out) (kit-vector (kit-get node :items))
        (gethash "maxVisible" out) (kit-get node :max-visible))
  (kit-out-state node out (:layout "layout") (:selected-index "selectedIndex") (:filter "filter")))

(defstruct (kit-setting (:constructor %kit-setting (id label current-value description values submenu)))
  id label current-value description values submenu)

(defun kit-setting-item (id label current-value &key description values submenu)
  "SettingItem; SUBMENU is a node the open setting shows in the list's place."
  (%kit-setting id label current-value description values submenu))

(defun kit-enc-setting (enc item depth)
  (let ((out (obj "id" (kit-setting-id item) "label" (kit-setting-label item))))
    (kit-out out "description" (kit-setting-description item))
    (setf (gethash "currentValue" out) (kit-setting-current-value item))
    (when (kit-setting-values item) (setf (gethash "values" out) (kit-vector (kit-setting-values item))))
    (when (kit-setting-submenu item) (setf (gethash "submenu" out) (kit-enc-node enc (kit-setting-submenu item) (1+ depth))))
    out))

(defun kit-settings-list (id items max-visible &key enable-search selected-index filter)
  (kit-new "settings-list" :id id :items (copy-list items) :max-visible max-visible :enable-search enable-search
                           :selected-index selected-index :filter filter))
(def-kit-encoder "settings-list" (node enc depth out)
  (setf (gethash "id" out) (kit-get node :id)
        (gethash "items" out) (map 'simple-vector (lambda (item) (kit-enc-setting enc item depth)) (kit-get node :items))
        (gethash "maxVisible" out) (kit-get node :max-visible))
  (when (kit-get node :enable-search) (setf (gethash "enableSearch" out) t))
  (kit-out-state node out (:selected-index "selectedIndex") (:filter "filter")))

(defun kit-image (data mime-type &key max-width-cells max-height-cells filename fallback-color)
  "Image(base64Data, mimeType): DATA is the decoded octets. A connection sends the bytes once; later frames name the ref."
  (kit-new "image" :image (kit-image-data data mime-type) :max-width-cells max-width-cells :max-height-cells max-height-cells
                   :filename filename :fallback-color fallback-color))
(defun kit-image-ref (node) "The lowercase hex SHA-256 of the image's bytes." (kit-img-ref (kit-get node :image)))
(def-kit-encoder "image" (node enc depth out)
  (let ((image (kit-get node :image)))
    (setf (gethash "ref" out) (kit-img-ref image) (gethash "mimeType" out) (kit-img-mime-type image))
    (kit-out-state node out (:max-width-cells "maxWidthCells") (:max-height-cells "maxHeightCells")
                   (:filename "filename") (:fallback-color "fallbackColor"))
    (kit-enc-add-image enc image)))

(defun kit-loader (&optional (message "Loading...") &key spinner-color message-color indicator frame)
  (kit-new "loader" :message message :spinner-color spinner-color :message-color message-color :indicator indicator :frame frame))
(defun kit-loader-fields (node out)
  (setf (gethash "message" out) (kit-get node :message))
  (kit-out-state node out (:spinner-color "spinnerColor") (:message-color "messageColor") (:indicator "indicator") (:frame "frame")))
(def-kit-encoder "loader" (node enc depth out) (kit-loader-fields node out))

(defstruct (kit-stack-entry (:constructor %kit-stack-entry (component basis grow shrink min-size max-size)))
  component basis grow shrink min-size max-size)

(defun kit-stack-entry (component &key basis grow shrink min-size max-size)
  "A stack child with its options; BASIS NIL is \"auto\"."
  (%kit-stack-entry component basis grow shrink min-size max-size))

(defun kit-hstack (&optional children &key gap align) (kit-new "hstack" :children (copy-list children) :gap gap :align align))
(defun kit-vstack (&optional children &key gap align) (kit-new "vstack" :children (copy-list children) :gap gap :align align))
(defun kit-stack-add-child (stack child) (kit-container-add-child stack child))
(flet ((encode-stack (node enc depth out)
         (when (kit-get node :children)
           (setf (gethash "children" out)
                 (map 'simple-vector
                      (lambda (child)
                        (if (kit-stack-entry-p child)
                            (let ((encoded (kit-enc-node enc (kit-stack-entry-component child) (1+ depth)))
                                  (options (obj)))
                              (kit-out options "basis" (kit-stack-entry-basis child))
                              (kit-out options "grow" (kit-stack-entry-grow child))
                              (kit-out options "shrink" (kit-stack-entry-shrink child))
                              (kit-out options "minSize" (kit-stack-entry-min-size child))
                              (kit-out options "maxSize" (kit-stack-entry-max-size child))
                              (when (plusp (hash-table-count options)) (setf (gethash "stack" encoded) options))
                              encoded)
                            (kit-enc-node enc child (1+ depth))))
                      (kit-get node :children))))
         (kit-out-state node out (:gap "gap") (:align "align"))))
  (setf (gethash "hstack" *kit-encoders*) #'encode-stack (gethash "vstack" *kit-encoders*) #'encode-stack))

(defun kit-lines (content &key list)
  "Rows a component of the extension's own drew, shown verbatim. LIST is a KIT-LIST annotation (one item per row)."
  (kit-new "lines" :content (mapcar #'princ-to-string content) :list list :image nil :progress nil))
(defun kit-lines-image (node data mime-type)
  "Frontend-only: the rows depict this image. Returns NODE."
  (kit-put node :image (kit-image-data data mime-type)) node)
(defun kit-lines-progress (node value max)
  "Frontend-only: the rows show VALUE in 0..MAX. Returns NODE."
  (kit-put node :progress (cons (float value 1d0) (float max 1d0))) node)
(def-kit-encoder "lines" (node enc depth out)
  (setf (gethash "content" out) (kit-vector (kit-get node :content)))
  (when (kit-enc-frontend enc)
    (let ((image (kit-get node :image)) (progress (kit-get node :progress)))
      (when image
        (setf (gethash "image" out) (obj "ref" (kit-img-ref image)))
        (kit-enc-add-image enc image))
      (when progress (setf (gethash "progress" out) (obj "value" (car progress) "max" (cdr progress))))
      (kit-out out "list" (kit-get node :list)))))

;;; conversation kinds ----------------------------------------------------------------------------

(defun kit-user-message (text &optional (output-pad 1)) (kit-new "user-message" :text text :output-pad output-pad))
(defun kit-user-message-set-output-pad (node padding) (kit-put node :output-pad padding))
(def-kit-encoder "user-message" (node enc depth out)
  (setf (gethash "text" out) (kit-get node :text) (gethash "outputPad" out) (kit-get node :output-pad)))

(defun kit-assistant-message (&optional message &key (id ""))
  "AssistantMessageComponent: text blocks as Markdown, thinking runs in the thinking color, a length, abort or error line. No message draws nothing."
  (kit-new "assistant-message" :id id :message message :streaming nil :hide-thinking nil :label "Thinking..." :output-pad 1))
(defun kit-assistant-message-update-content (node message is-streaming)
  (kit-put node :message message) (kit-put node :streaming (and is-streaming t)))
(defun kit-assistant-message-set-hide-thinking-block (node hide) (kit-put node :hide-thinking (and hide t)))
(defun kit-assistant-message-set-hidden-thinking-label (node label) (kit-put node :label label))
(defun kit-assistant-message-set-output-pad (node padding) (kit-put node :output-pad padding))
(def-kit-encoder "assistant-message" (node enc depth out)
  (when (plusp (length (kit-get node :id))) (setf (gethash "id" out) (kit-get node :id)))
  (kit-out out "message" (kit-get node :message))
  (setf (gethash "outputPad" out) (kit-get node :output-pad))
  (when (kit-get node :hide-thinking) (setf (gethash "hideThinkingBlock" out) t))
  (unless (equal (kit-get node :label) "Thinking...") (setf (gethash "hiddenThinkingLabel" out) (kit-get node :label)))
  (when (kit-get node :streaming) (setf (gethash "isStreaming" out) t)))

(defparameter *kit-tool-definition-builtin* "builtin" "A ToolExecution's renderers: the built-in tool's (the default).")
(defparameter *kit-tool-definition-empty* "empty" "A ToolExecution's renderers: a definition without any.")

(defstruct (kit-rc (:constructor kit-make-rc (text image))) text image)
(defun kit-text-content (text) (kit-make-rc text nil))
(defun kit-image-content (data mime-type) (kit-make-rc "" (kit-image-data data mime-type)))

(defstruct (kit-tool-result (:constructor %kit-tool-result (content is-error details duration-ms))) content is-error details duration-ms)
(defun kit-tool-result (content &key is-error details duration-ms)
  "The result updateResult receives: DETAILS any JSON value the built-in renderers read; DURATION-MS a final result's recorded time."
  (%kit-tool-result content is-error details duration-ms))

(defun kit-tool-execution (tool-name &optional (tool-call-id "") args (cwd "") &key (tool-definition *kit-tool-definition-builtin*)
                                                                                  (output-pad 1) (id ""))
  "ToolExecutionComponent: the tool card the main transcript draws. Upstream's defaults: images shown 60 cells wide, no result yet."
  (kit-new "tool-execution" :id id :tool-name tool-name :tool-call-id tool-call-id :args args :tool-definition tool-definition :cwd cwd
                            :show-images t :image-width-cells 60 :output-pad output-pad :started nil :args-complete nil :expanded nil
                            :result nil :partial t))
(defun kit-tool-execution-update-args (node args) (kit-put node :args args))
(defun kit-tool-execution-mark-execution-started (node) (kit-put node :started t))
(defun kit-tool-execution-set-args-complete (node) (kit-put node :args-complete t))
(defun kit-tool-execution-update-result (node result is-partial) (kit-put node :result result) (kit-put node :partial (and is-partial t)))
(defun kit-tool-execution-set-expanded (node expanded) (kit-put node :expanded (and expanded t)))
(defun kit-tool-execution-set-show-images (node show) (kit-put node :show-images (and show t)))
(defun kit-tool-execution-set-image-width-cells (node width) (kit-put node :image-width-cells (max 1 width)))
(defun kit-tool-execution-set-output-pad (node padding) (kit-put node :output-pad padding))
(def-kit-encoder "tool-execution" (node enc depth out)
  (when (plusp (length (kit-get node :id))) (setf (gethash "id" out) (kit-get node :id)))
  (unless (eql (kit-get node :output-pad) 1) (setf (gethash "outputPad" out) (kit-get node :output-pad)))
  (setf (gethash "toolName" out) (kit-get node :tool-name))
  (when (plusp (length (kit-get node :tool-call-id))) (setf (gethash "toolCallId" out) (kit-get node :tool-call-id)))
  (setf (gethash "args" out) (or (kit-get node :args) (obj)))
  (unless (equal (kit-get node :tool-definition) *kit-tool-definition-builtin*)
    (setf (gethash "toolDefinition" out) (kit-get node :tool-definition)))
  (when (plusp (length (kit-get node :cwd))) (setf (gethash "cwd" out) (kit-get node :cwd)))
  (unless (kit-get node :show-images) (setf (gethash "showImages" out) :false))
  (unless (eql (kit-get node :image-width-cells) 60) (setf (gethash "imageWidthCells" out) (kit-get node :image-width-cells)))
  (when (kit-get node :started) (setf (gethash "executionStarted" out) t))
  (when (kit-get node :args-complete) (setf (gethash "argsComplete" out) t))
  (when (kit-get node :expanded) (setf (gethash "expanded" out) t))
  (let ((result (kit-get node :result)))
    (when result
      (let ((result-out
              (obj "content"
                   (map 'simple-vector
                        (lambda (content-block)
                          (if (kit-rc-image content-block)
                              (let ((image (kit-rc-image content-block)))
                                (kit-enc-add-image enc image)
                                (obj "type" "image" "ref" (kit-img-ref image) "mimeType" (kit-img-mime-type image)))
                              (kit-text-block (kit-rc-text content-block))))
                        (kit-tool-result-content result)))))
        (when (kit-tool-result-is-error result) (setf (gethash "isError" result-out) t))
        (kit-out result-out "details" (kit-tool-result-details result))
        (when (kit-tool-result-duration-ms result) (setf (gethash "durationMs" result-out) (kit-tool-result-duration-ms result)))
        (setf (gethash "result" out) result-out (gethash "isPartial" out) (if (kit-get node :partial) t :false))))))

(defun kit-bash-execution (command &optional exclude-from-context &key (output-pad 1) (id ""))
  "BashExecutionComponent: a ! command between two borders, with its spinner while it runs and its status after."
  (kit-new "bash-execution" :id id :command command :exclude (and exclude-from-context t) :output-pad output-pad :output "" :expanded nil :complete nil))
(defun kit-bash-execution-append-output (node chunk)
  (kit-put node :output (concatenate 'string (kit-get node :output) chunk)))
(defun kit-bash-execution-set-complete (node exit-code &optional cancelled truncated (full-output-path ""))
  "setComplete: EXIT-CODE NIL when unknown, whether it was CANCELLED, whether its output was TRUNCATED, where the full output is (\"\" for nowhere)."
  (let ((complete (obj)))
    (when exit-code (setf (gethash "exitCode" complete) exit-code))
    (when cancelled (setf (gethash "cancelled" complete) t))
    (when truncated (setf (gethash "truncated" complete) t))
    (when (plusp (length full-output-path)) (setf (gethash "fullOutputPath" complete) full-output-path))
    (kit-put node :complete complete)))
(defun kit-bash-execution-set-expanded (node expanded) (kit-put node :expanded (and expanded t)))
(defun kit-bash-execution-set-output-pad (node padding) (kit-put node :output-pad padding))
(def-kit-encoder "bash-execution" (node enc depth out)
  (when (plusp (length (kit-get node :id))) (setf (gethash "id" out) (kit-get node :id)))
  (unless (eql (kit-get node :output-pad) 1) (setf (gethash "outputPad" out) (kit-get node :output-pad)))
  (when (kit-get node :expanded) (setf (gethash "expanded" out) t))
  (setf (gethash "command" out) (kit-get node :command))
  (when (kit-get node :exclude) (setf (gethash "excludeFromContext" out) t))
  (when (plusp (length (kit-get node :output))) (setf (gethash "output" out) (kit-get node :output)))
  (when (kit-get node :complete) (setf (gethash "complete" out) (kit-get node :complete))))

(defun kit-diff (diff &key (file-path "") (padding-x 0) (padding-y 0))
  "renderDiff(diff, {filePath}) drawn in a Text(..., paddingX, paddingY), as Pi's edit renderer draws it."
  (kit-new "diff" :diff diff :file-path file-path :padding-x padding-x :padding-y padding-y))
(def-kit-encoder "diff" (node enc depth out)
  (setf (gethash "paddingX" out) (kit-get node :padding-x) (gethash "paddingY" out) (kit-get node :padding-y)
        (gethash "diff" out) (kit-get node :diff))
  (when (plusp (length (kit-get node :file-path))) (setf (gethash "filePath" out) (kit-get node :file-path))))

(defun kit-summary-fields (node out)
  (when (plusp (length (kit-get node :id))) (setf (gethash "id" out) (kit-get node :id)))
  (unless (eql (kit-get node :output-pad) 1) (setf (gethash "outputPad" out) (kit-get node :output-pad)))
  (when (kit-get node :expanded) (setf (gethash "expanded" out) t)))

(defun kit-branch-summary (summary &key (output-pad 1) (id ""))
  "BranchSummaryMessageComponent: the marker of a summarized branch, with the summary's markdown behind a click or -set-expanded."
  (kit-new "branch-summary" :id id :summary summary :output-pad output-pad :expanded nil))
(def-kit-encoder "branch-summary" (node enc depth out)
  (kit-summary-fields node out)
  (when (plusp (length (kit-get node :summary))) (setf (gethash "summary" out) (kit-get node :summary))))
(defun kit-branch-summary-set-expanded (node expanded) (kit-put node :expanded (and expanded t)))
(defun kit-branch-summary-set-output-pad (node padding) (kit-put node :output-pad padding))

(defun kit-compaction-summary (summary tokens-before &key (output-pad 1) (id ""))
  (kit-new "compaction-summary" :id id :summary summary :tokens-before tokens-before :output-pad output-pad :expanded nil))
(def-kit-encoder "compaction-summary" (node enc depth out)
  (kit-summary-fields node out)
  (when (plusp (length (kit-get node :summary))) (setf (gethash "summary" out) (kit-get node :summary)))
  (setf (gethash "tokensBefore" out) (kit-get node :tokens-before)))
(defun kit-compaction-summary-set-expanded (node expanded) (kit-put node :expanded (and expanded t)))
(defun kit-compaction-summary-set-output-pad (node padding) (kit-put node :output-pad padding))

(defun kit-skill-invocation (name content &key (output-pad 1) (id ""))
  (kit-new "skill-invocation" :id id :name name :content content :output-pad output-pad :expanded nil))
(def-kit-encoder "skill-invocation" (node enc depth out)
  (kit-summary-fields node out)
  (let ((skill (obj)))
    (when (plusp (length (kit-get node :name))) (setf (gethash "name" skill) (kit-get node :name)))
    (when (plusp (length (kit-get node :content))) (setf (gethash "content" skill) (kit-get node :content)))
    (setf (gethash "skill" out) skill)))
(defun kit-skill-invocation-set-expanded (node expanded) (kit-put node :expanded (and expanded t)))
(defun kit-skill-invocation-set-output-pad (node padding) (kit-put node :output-pad padding))

(defun kit-custom-message (custom-type text &key (output-pad 1) (id ""))
  "CustomMessageComponent: the default rendering of an extension's message, \"[customType]\" over the content's text."
  (kit-new "custom-message" :id id :custom-type custom-type :text text :output-pad output-pad :expanded nil))
(def-kit-encoder "custom-message" (node enc depth out)
  (kit-summary-fields node out)
  (when (plusp (length (kit-get node :custom-type))) (setf (gethash "customType" out) (kit-get node :custom-type)))
  (when (plusp (length (kit-get node :text))) (setf (gethash "text" out) (kit-get node :text))))
(defun kit-custom-message-set-expanded (node expanded) (kit-put node :expanded (and expanded t)))
(defun kit-custom-message-set-output-pad (node padding) (kit-put node :output-pad padding))

;;; widget kinds ------------------------------------------------------------------------------------

(defun kit-cancellable-loader (id &optional (message "Loading...") &key spinner-color message-color indicator frame)
  "A loader the focused tui.select.cancel key aborts (an \"abort\" event of the node); the view's focus must name it."
  (kit-new "cancellable-loader" :id id :message message :spinner-color spinner-color :message-color message-color :indicator indicator :frame frame))
(def-kit-encoder "cancellable-loader" (node enc depth out)
  (setf (gethash "id" out) (kit-get node :id))
  (kit-loader-fields node out))

(defun kit-bordered-loader (id &optional (message "Loading...") &key (cancellable t) frame)
  "A loader between two borders; CANCELLABLE NIL drops the cancel key hint."
  (kit-new "bordered-loader" :id id :message message :cancellable cancellable :frame frame))
(def-kit-encoder "bordered-loader" (node enc depth out)
  (setf (gethash "id" out) (kit-get node :id) (gethash "message" out) (kit-get node :message))
  (unless (kit-get node :cancellable) (setf (gethash "cancellable" out) :false))
  (kit-out out "frame" (kit-get node :frame)))

(defun kit-input (id &optional (value "") &key prompt (placeholder "") placeholder-color)
  "A one-line text field the host owns once the view's focus names ID; submit, escape and change reach the extension as events."
  (kit-new "input" :id id :value value :prompt prompt :placeholder placeholder :placeholder-color placeholder-color))
(def-kit-encoder "input" (node enc depth out)
  (setf (gethash "id" out) (kit-get node :id))
  (when (plusp (length (kit-get node :value))) (setf (gethash "value" out) (kit-get node :value)))
  (kit-out out "prompt" (kit-get node :prompt))
  (when (plusp (length (kit-get node :placeholder))) (setf (gethash "placeholder" out) (kit-get node :placeholder)))
  (kit-out out "placeholderColor" (kit-get node :placeholder-color)))

(defun kit-editor (id &optional (value "") &key (padding-x 0) (autocomplete-max-visible 0) border-color disable-submit history autocomplete)
  "A multi-line text editor between two borders; HISTORY is the addToHistory calls, oldest first."
  (kit-new "editor" :id id :value value :padding-x padding-x :autocomplete-max-visible autocomplete-max-visible :border-color border-color
                    :disable-submit disable-submit :history (copy-list history) :autocomplete autocomplete))
(def-kit-encoder "editor" (node enc depth out)
  (setf (gethash "id" out) (kit-get node :id))
  (when (plusp (length (kit-get node :value))) (setf (gethash "value" out) (kit-get node :value)))
  (when (and (kit-get node :padding-x) (/= 0 (kit-get node :padding-x))) (setf (gethash "paddingX" out) (kit-get node :padding-x)))
  (when (and (kit-get node :autocomplete-max-visible) (/= 0 (kit-get node :autocomplete-max-visible)))
    (setf (gethash "autocompleteMaxVisible" out) (kit-get node :autocomplete-max-visible)))
  (kit-out out "borderColor" (kit-get node :border-color))
  (when (kit-get node :disable-submit) (setf (gethash "disableSubmit" out) t))
  (when (kit-get node :history) (setf (gethash "history" out) (kit-vector (kit-get node :history))))
  (kit-out out "autocomplete" (kit-get node :autocomplete)))

(defun kit-scroll-view (id child &key (follow "") primary (overscroll "") (scrollbar "") scrollbar-track-color scrollbar-thumb-color
                                   scrollbar-hide-delay-ms scroll-top)
  "One child that scrolls: FOLLOW \"none\" or \"end\", OVERSCROLL \"chain\" or \"contain\", SCROLLBAR \"hidden\", \"auto\" or \"always\"."
  (kit-new "scroll-view" :id id :child child :follow follow :primary primary :overscroll overscroll :scrollbar scrollbar
                         :track scrollbar-track-color :thumb scrollbar-thumb-color :hide-delay scrollbar-hide-delay-ms :scroll-top scroll-top))
(def-kit-encoder "scroll-view" (node enc depth out)
  (setf (gethash "id" out) (kit-get node :id) (gethash "children" out) (vector (kit-enc-node enc (kit-get node :child) (1+ depth))))
  (when (plusp (length (kit-get node :follow))) (setf (gethash "follow" out) (kit-get node :follow)))
  (when (kit-get node :primary) (setf (gethash "primary" out) t))
  (when (plusp (length (kit-get node :overscroll))) (setf (gethash "overscroll" out) (kit-get node :overscroll)))
  (when (plusp (length (kit-get node :scrollbar))) (setf (gethash "scrollbar" out) (kit-get node :scrollbar)))
  (kit-out-state node out (:track "scrollbarTrackColor") (:thumb "scrollbarThumbColor") (:hide-delay "scrollbarHideDelayMs")
                 (:scroll-top "scrollTop")))

(defun kit-mouse-region (id child &key types handled capture focus render)
  "Mouse handling added to CHILD: TYPES are the event types the handler runs for (every type when none); HANDLED and RENDER are tri-state."
  (kit-new "mouse-region" :id id :child child :types (copy-list types) :handled handled :capture capture :focus focus :render render))
(def-kit-encoder "mouse-region" (node enc depth out)
  (let ((region (obj)))
    (when (kit-get node :types) (setf (gethash "types" region) (kit-vector (kit-get node :types))))
    (kit-out region "handled" (kit-get node :handled))
    (when (kit-get node :capture) (setf (gethash "capture" region) t))
    (when (kit-get node :focus) (setf (gethash "focus" region) t))
    (kit-out region "render" (kit-get node :render))
    (setf (gethash "id" out) (kit-get node :id) (gethash "children" out) (vector (kit-enc-node enc (kit-get node :child) (1+ depth)))
          (gethash "region" out) region)))

;;; ---------------------------------------------------------------- views and events

(defstruct (kit-view (:constructor %kit-view (root focus theme))) root focus theme)

(defun kit-view (root &key focus theme)
  "A component tree with the surface's focus (the id of the node that receives keys) and THEME overrides: tokens to #rrggbb strings, a
hash-table (obj \"accent\" \"#d75f00\"). Foreground tokens: accent muted dim text border borderAccent borderMuted success warning error;
background: selectedBg customMessageBg."
  (%kit-view root focus theme))

(defstruct (kit-encoded (:constructor kit-make-encoded (body images))) body images)

(defun kit-encode-view (view frontend)
  "The wire ViewPayload without images, and the images it references."
  (unless (kit-view-p view) (error "a view surface takes a kit view"))
  (let* ((enc (kit-make-enc frontend))
         (body (obj "root" (kit-enc-node enc (kit-view-root view) 0))))
    (when (and (kit-view-focus view) (plusp (length (kit-view-focus view)))) (setf (gethash "focus" body) (kit-view-focus view)))
    (when (and (hash-table-p (kit-view-theme view)) (plusp (hash-table-count (kit-view-theme view))))
      (setf (gethash "theme" body) (kit-view-theme view)))
    (kit-make-encoded body (kit-enc-images enc))))

(defstruct (kit-event (:constructor kit-make-event (node type index item id value cursor mouse)))
  "A callback of an interactive node of a ui.custom view (wire ui.view.event). TYPE is \"select\", \"cancel\", \"selectionChange\", \"change\",
\"submit\", \"escape\", \"abort\" or \"mouse\"; INDEX is the item's index among the shown items for select and selection changes, 0 otherwise."
  node type index item id value cursor mouse)

(defun kit-event-from-wire (args)
  "(values overlay-key event) of a ui.view.event; NIL for a type this SDK does not deliver."
  (let ((type (jget args "type")) (item (jget args "item")))
    (when (member type '("select" "cancel" "selectionChange" "change" "submit" "escape" "abort" "mouse") :test #'equal)
      (flet ((text (key) (let ((v (jget args key))) (if (stringp v) v ""))))
        (values (text "key")
                (kit-make-event (text "node") type (let ((i (jget args "index"))) (if (integerp i) i 0))
                                (and (hash-table-p item) (kit-select-item (let ((v (jget item "value"))) (if (stringp v) v ""))
                                                                          (let ((v (jget item "label"))) (if (stringp v) v ""))
                                                                          (let ((v (jget item "description"))) (and (stringp v) v))))
                                (text "id") (text "value")
                                (let ((c (jget args "cursor"))) (and (hash-table-p c) c))
                                (let ((m (jget args "mouse"))) (and (hash-table-p m) m))))))))

;;; ---------------------------------------------------------------- per-extension state

(defstruct (kit-feed (:constructor kit-make-feed (what)))
  "Delivery of a replicated value's changes (and queued jobs) to subscribers on a worker thread, one delivery at a time, in order."
  what (lock (sb-thread:make-mutex :name "pig feed")) (id 0) (subscribers '()) (queue '()) (running nil))

(defstruct (kit-state (:constructor kit-make-state ()))
  (lock (sb-thread:make-mutex :name "pig kit"))
  (frontend nil)
  (sent (make-hash-table :test 'equal))
  ;; held across a send, so an eviction's resend and the author's next frame keep their order
  (widgets-lock (sb-thread:make-mutex :name "pig kit widgets"))
  (widgets (make-hash-table :test 'equal))
  (surfaces-lock (sb-thread:make-mutex :name "pig kit surfaces"))
  (surfaces (make-hash-table :test 'equal))
  ;; the renderer installed under a surface id (a method, or "ui.setWidget:<key>")
  (renderers (make-hash-table :test 'equal))
  (renderers-lock (sb-thread:make-mutex :name "pig kit renderers"))
  (overlays (make-hash-table :test 'equal))
  (overlay-seq 0)
  (jobs (kit-make-feed "view job"))
  (theme-feed (kit-make-feed "theme"))
  (tools-feed (kit-make-feed "tools expanded"))
  (theme nil) (palette-json nil)
  (tools-expanded nil) (tools-known nil)
  (footer-json nil)
  (width-subscribed nil))

(defun kit-state (ext) (extension-extra ext :kit #'kit-make-state))

(defun kit-spawn (ext name function)
  "Run FUNCTION on a new thread that the connection's shutdown waits for."
  (let ((thread (sb-thread:make-thread
                 (lambda ()
                   (unwind-protect (funcall function)
                     (sb-thread:with-mutex ((extension-state-lock ext))
                       (setf (extension-request-threads ext) (remove sb-thread:*current-thread* (extension-request-threads ext))))))
                 :name name)))
    (sb-thread:with-mutex ((extension-state-lock ext)) (push thread (extension-request-threads ext)))
    thread))

(defun kit-feed-subscribe (feed handler)
  "Subscribe HANDLER; returns the idempotent unsubscribe function."
  (let ((id (sb-thread:with-mutex ((kit-feed-lock feed))
              (let ((id (incf (kit-feed-id feed))))
                (setf (kit-feed-subscribers feed) (append (kit-feed-subscribers feed) (list (cons id handler))))
                id))))
    (lambda ()
      (sb-thread:with-mutex ((kit-feed-lock feed))
        (setf (kit-feed-subscribers feed) (remove id (kit-feed-subscribers feed) :key #'car))))))

(defun kit-feed-run (ext feed)
  (loop
    (let ((job (sb-thread:with-mutex ((kit-feed-lock feed))
                 (cond ((or (null (kit-feed-queue feed)) (extension-shutdown ext))
                        (setf (kit-feed-queue feed) '() (kit-feed-running feed) nil)
                        nil)
                       (t (pop (kit-feed-queue feed)))))))
      (unless job (return))
      (dolist (handler (cdr job))
        (handler-case (funcall handler (car job))
          (error (e) (unless (extension-shutdown ext)
                       (format *error-output* "pig: ~A change handler failed: ~A~%" (kit-feed-what feed) e))))))))

(defun kit-feed-publish (ext feed value)
  "Queue VALUE for the handlers subscribed now."
  (let ((start nil))
    (sb-thread:with-mutex ((kit-feed-lock feed))
      (let ((handlers (mapcar #'cdr (kit-feed-subscribers feed))))
        (when (and handlers (not (extension-shutdown ext)))
          (setf (kit-feed-queue feed) (append (kit-feed-queue feed) (list (cons value handlers))))
          (unless (kit-feed-running feed) (setf (kit-feed-running feed) t start t)))))
    (when start (kit-spawn ext (format nil "pig-~A-change" (kit-feed-what feed)) (lambda () (kit-feed-run ext feed))))))

(defun kit-run-job (ext function)
  "Run FUNCTION on the extension's view-job worker, after the jobs queued before it (never on the thread that reads the host)."
  (let ((feed (kit-state-jobs (kit-state ext))))
    (sb-thread:with-mutex ((kit-feed-lock feed))
      (unless (kit-feed-subscribers feed)
        (setf (kit-feed-subscribers feed) (list (cons (incf (kit-feed-id feed)) #'funcall)))))
    (kit-feed-publish ext feed function)))

;;; ---------------------------------------------------------------- image transport

(defun kit-encoded-refs (encoded) (mapcar #'kit-img-ref (kit-encoded-images encoded)))

(defun kit-has-unsent (ext encoded)
  (let ((state (kit-state ext)))
    (sb-thread:with-mutex ((kit-state-lock state))
      (some (lambda (image) (not (gethash (kit-img-ref image) (kit-state-sent state)))) (kit-encoded-images encoded)))))

(defun kit-wire (ext encoded)
  "The wire view: the body with the data of every image not sent on this connection yet, which counts as sent from here on."
  (let ((state (kit-state ext)) (unsent '()))
    (sb-thread:with-mutex ((kit-state-lock state))
      (dolist (image (kit-encoded-images encoded))
        (unless (gethash (kit-img-ref image) (kit-state-sent state))
          (setf (gethash (kit-img-ref image) (kit-state-sent state)) t)
          (push (obj "ref" (kit-img-ref image) "mimeType" (kit-img-mime-type image) "data" (kit-base64 (kit-img-data image))) unsent))))
    (if unsent
        (let ((body (make-hash-table :test 'equal)))
          (maphash (lambda (k v) (setf (gethash k body) v)) (kit-encoded-body encoded))
          (setf (gethash "images" body) (kit-vector (nreverse unsent)))
          body)
        (kit-encoded-body encoded))))

(defun kit-encode (ext view)
  (kit-encode-view view (sb-thread:with-mutex ((kit-state-lock (kit-state ext))) (kit-state-frontend (kit-state ext)))))

(defun kit-render-result (ext view)
  "A view renderer's wire RenderResult: {\"view\": the wire view}."
  (obj "view" (kit-wire ext (kit-encode ext view))))

(defun kit-apply-state (ext state)
  (when (hash-table-p state)
    (let ((kit (kit-state ext)))
      (sb-thread:with-mutex ((kit-state-lock kit)) (setf (kit-state-frontend kit) (eq (jget state "frontend") t))))))

(def-ready-hook "kit" (ext ready) (kit-apply-state ext (jget ready "state")))
(def-state-hook "kit" (ext state) (kit-apply-state ext state))

(def-notify "ui.view.evicted" (ext args)
  ;; forget the refs the host dropped and send again, with the data, the latest frame of every live surface that names one
  (let ((refs (let ((r (jget args "refs"))) (and (vectorp r) (not (stringp r)) (remove-if-not #'stringp (coerce r 'list)))))
        (kit (kit-state ext)))
    (when refs
      (sb-thread:with-mutex ((kit-state-lock kit)) (dolist (ref refs) (remhash ref (kit-state-sent kit))))
      (kit-resend-overlays ext refs)
      (kit-run-job ext (lambda () (kit-resend-surfaces ext refs))))))
