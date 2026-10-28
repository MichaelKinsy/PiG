;;;; SPDX-License-Identifier: MIT
;;;; Upstream Theme (ctx.ui.theme) over the host's active palette, and the subscriptions to the replicated theme and tool expansion.
;;;; The host replicates its palette with the state snapshot and pushes theme_change when it changes; one THEME per extension follows
;;;; those updates, so a held reference stays current. Its semantics are the Node runtime's ThemeShim: a token the palette lacks signals
;;;; "Unknown theme color: <token>" from THEME-FG/THEME-BG once a palette has arrived, and the modifiers draw only when the host's styles do.

(in-package #:pig)

(defstruct (theme (:constructor %make-theme ()))
  "The host's active theme: NAME, SOURCE-PATH and SOURCE-INFO (NIL when the palette has none), APPEARANCE (\"light\", \"dark\" or NIL)."
  name source-path source-info (fgs (obj)) (bgs (obj)) (modifiers t) (mode "truecolor") appearance (palette-colors (obj)))

(defun theme-sgr (code) (concatenate 'string (string (code-char 27)) "[" code "m"))

(defun theme-text (text)
  (cond ((null text) "") ((stringp text) text) (t (princ-to-string text))))

(defun theme-finite-number-p (value)
  (and (realp value) (not (and (floatp value) (or (/= value value) (> (abs value) most-positive-double-float))))))

(defun theme-parse-color (value)
  "A copy of a wire color ({\"kind\": \"indexed\"|\"rgb\"|\"oklch\", ...}), or NIL when VALUE is not a well-formed pi-tui Color."
  (when (hash-table-p value)
    (let ((kind (jget value "kind")))
      (flet ((numbers (&rest keys) (every (lambda (k) (theme-finite-number-p (jget value k))) keys)))
        (cond ((equal kind "indexed")
               (let ((index (jget value "index")))
                 (and (theme-finite-number-p index) (= index (truncate index)) (<= 0 index 255)
                      (obj "kind" "indexed" "index" (truncate index)))))
              ((equal kind "rgb") (and (numbers "r" "g" "b") (obj "kind" "rgb" "r" (jget value "r") "g" (jget value "g") "b" (jget value "b"))))
              ((equal kind "oklch") (and (numbers "l" "c" "h") (obj "kind" "oklch" "l" (jget value "l") "c" (jget value "c") "h" (jget value "h")))))))))

(defun theme-copy-table (value)
  (let ((copy (make-hash-table :test 'equal)))
    (when (hash-table-p value) (maphash (lambda (k v) (setf (gethash k copy) v)) value))
    copy))

(defun theme-set-palette (theme palette)
  (let ((palette (if (hash-table-p palette) palette (obj))))
    (setf (theme-fgs theme) (theme-copy-table (jget palette "foregrounds"))
          (theme-bgs theme) (theme-copy-table (jget palette "backgrounds"))
          (theme-modifiers theme) (not (eq (jget palette "modifiers") :false))
          (theme-name theme) (let ((n (jget palette "name"))) (and (stringp n) n))
          (theme-source-path theme) (let ((p (jget palette "sourcePath"))) (and (stringp p) (plusp (length p)) p))
          (theme-source-info theme) (let ((i (jget palette "sourceInfo"))) (and (hash-table-p i) (theme-copy-table i)))
          (theme-mode theme) (if (equal (jget palette "mode") "256color") "256color" "truecolor")
          (theme-appearance theme) (let ((a (jget palette "appearance"))) (and (member a '("light" "dark") :test #'equal) a))
          (theme-palette-colors theme)
          (let ((parsed (obj)) (colors (jget palette "colors")))
            (when (hash-table-p colors)
              (maphash (lambda (token value) (let ((color (theme-parse-color value))) (when color (setf (gethash token parsed) color)))) colors))
            parsed))
    theme))

(defun make-theme-from-palette (palette)
  "A THEME over the host's palette object (the state snapshot's \"theme\"); NIL or a non-object is the empty palette (modifiers only)."
  (theme-set-palette (%make-theme) palette))

(defun theme-replace-all (string old new)
  (with-output-to-string (out)
    (let ((start 0))
      (loop for hit = (search old string :start2 start)
            while hit do (write-string string out :start start :end hit) (write-string new out) (setf start (+ hit (length old))))
      (write-string string out :start start))))

(defun theme-encase-line-breaks (value prefix postfix)
  "chalk's stringEncaseCRLFWithFirstIndex: PREFIX before and POSTFIX after every newline, a preceding CR staying with it."
  (with-output-to-string (out)
    (let ((end 0))
      (loop for index = (position #\Newline value :start end)
            while index
            do (if (and (> index 0) (char= (char value (1- index)) #\Return))
                   (progn (write-string value out :start end :end (1- index)) (write-string prefix out)
                          (write-char #\Return out) (write-char #\Newline out) (write-string postfix out))
                   (progn (write-string value out :start end :end index) (write-string prefix out)
                          (write-char #\Newline out) (write-string postfix out)))
               (setf end (1+ index)))
      (write-string value out :start end))))

(defun theme-modifier (theme open close text)
  ;; chalk's applyStyle (chalk 6.0.0 source/index.js): empty text stays empty, an inner close re-opens the style, each line break is
  ;; closed before and re-opened after
  (let ((value (theme-text text)))
    (if (or (not (theme-modifiers theme)) (string= value ""))
        value
        (progn
          (when (find (code-char 27) value) (setf value (theme-replace-all value close (concatenate 'string close open))))
          (when (find #\Newline value) (setf value (theme-encase-line-breaks value close open)))
          (concatenate 'string open value close)))))

(defun theme-unknown (token) (error "Unknown theme color: ~A" token))

(defun theme-unknown-or-unstyled (theme token value)
  ;; a token the palette lacks is upstream's `Unknown theme color` throw; a theme the host sent no palette to has no tokens to lack
  (if (and (zerop (hash-table-count (theme-fgs theme))) (zerop (hash-table-count (theme-bgs theme))))
      value
      (theme-unknown token)))

(defun theme-ansi (table token)
  (let ((ansi (gethash token table))) (and (stringp ansi) (plusp (length ansi)) ansi)))

(defun theme-ends-with (string suffix)
  (and (>= (length string) (length suffix)) (string= suffix string :start2 (- (length string) (length suffix)))))

(defun theme-fg (theme token text)
  "TEXT in the foreground token TOKEN."
  (let ((value (theme-text text)) (open (theme-ansi (theme-fgs theme) token)))
    (if open
        ;; the host opens a faint token with SGR 2 (theme.ts:399-402); Pi's fg closes it with SGR 22;39 (theme.ts:363)
        (concatenate 'string open value (if (theme-ends-with open (theme-sgr "2")) (theme-sgr "22;39") (theme-sgr "39")))
        (theme-unknown-or-unstyled theme token value))))

(defun theme-bg (theme token text)
  "TEXT on the background token TOKEN."
  (let ((value (theme-text text)) (open (theme-ansi (theme-bgs theme) token)))
    (if open (concatenate 'string open value (theme-sgr "49")) (theme-unknown-or-unstyled theme token value))))

(defun theme-bold (theme text) (theme-modifier theme (theme-sgr "1") (theme-sgr "22") text))
(defun theme-italic (theme text) (theme-modifier theme (theme-sgr "3") (theme-sgr "23") text))
(defun theme-underline (theme text) (theme-modifier theme (theme-sgr "4") (theme-sgr "24") text))
(defun theme-inverse (theme text) (theme-modifier theme (theme-sgr "7") (theme-sgr "27") text))
(defun theme-strikethrough (theme text) (theme-modifier theme (theme-sgr "9") (theme-sgr "29") text))

(defun theme-get-fg-ansi (theme token)
  (or (theme-ansi (theme-fgs theme) token) (theme-unknown token)))
(defun theme-get-bg-ansi (theme token)
  (or (theme-ansi (theme-bgs theme) token) (theme-unknown token)))

(defun theme-get-color-mode (theme) "\"truecolor\" or \"256color\"." (theme-mode theme))

(defun theme-colors (theme)
  "A concrete color for every theme token (upstream Theme.colors): {\"kind\": \"indexed\", \"index\"}, {\"kind\": \"rgb\", \"r\", \"g\", \"b\"} or
{\"kind\": \"oklch\", \"l\", \"c\", \"h\"}. A copy."
  (let ((copy (obj)))
    (maphash (lambda (token color) (setf (gethash token copy) (theme-copy-table color))) (theme-palette-colors theme))
    copy))

(defun theme-slot-ansi (theme value tokens background)
  (cond ((or (null value) (eq value :null)) "")
        ((stringp value) (or (theme-ansi tokens value) (theme-unknown value)))
        (t (let ((color (theme-parse-color value)))
             (unless color (error "Invalid theme color: ~S" value))
             (if background (tui-background-ansi color (theme-mode theme)) (tui-foreground-ansi color (theme-mode theme)))))))

(defun theme-style (theme text options)
  "Render TEXT in OPTIONS (upstream Theme.style), an object: \"fg\" and \"bg\" are a theme token, accepted only in its own slot, or a color from
THEME-COLORS; \"bold\" \"dim\" \"italic\" \"underline\" \"inverse\" \"strikethrough\" are attributes, drawn whatever the host's chalk level is.
An unknown token signals \"Unknown theme color: <token>\"."
  (let* ((fg-option (jget options "fg"))
         (fg-ansi (theme-slot-ansi theme fg-option (theme-fgs theme) nil))
         (dim (eq (jget options "dim") t)))
    ;; the palette's foreground appends SGR 2 to a faint token's color; a color never ends in it
    (when (and (stringp fg-option) (theme-ends-with fg-ansi (theme-sgr "2")))
      (setf fg-ansi (subseq fg-ansi 0 (- (length fg-ansi) (length (theme-sgr "2")))) dim t))
    (let ((bg-ansi (theme-slot-ansi theme (jget options "bg") (theme-bgs theme) t))
          (attributes (obj "dim" (if dim t :false))))
      (dolist (key '("bold" "italic" "underline" "inverse" "strikethrough"))
        (setf (gethash key attributes) (if (eq (jget options key) t) t :false)))
      (tui-style-text-with-ansi (theme-text text) fg-ansi bg-ansi attributes))))

(defparameter *theme-thinking-tokens*
  '(("off" . "thinkingOff") ("minimal" . "thinkingMinimal") ("low" . "thinkingLow") ("medium" . "thinkingMedium")
    ("high" . "thinkingHigh") ("xhigh" . "thinkingXhigh") ("max" . "thinkingMax")))

(defun theme-get-thinking-border-color (theme level)
  "A function of a string that colors it with the thinking border color of LEVEL."
  (let ((token (or (cdr (assoc level *theme-thinking-tokens* :test #'equal)) "thinkingOff")))
    (lambda (text) (theme-fg theme token text))))

(defun theme-get-bash-mode-border-color (theme)
  (lambda (text) (theme-fg theme "bashMode" text)))

;;; ---------------------------------------------------------------- the extension's theme and its subscriptions

(defun theme-of (ext)
  "The extension's theme object, which follows the host's updates."
  (let ((kit (kit-state ext)))
    (sb-thread:with-mutex ((kit-state-lock kit))
      (or (kit-state-theme kit) (setf (kit-state-theme kit) (make-theme-from-palette nil))))))

(defun ctx-theme (ctx)
  "Upstream ctx.ui.theme: the host's active theme, kept current."
  (theme-of (ctx-extension ctx)))

(defun theme-apply-palette (ext palette)
  "Install the host's palette and deliver the theme to the ON-THEME-CHANGE handlers when it differs from the one before."
  (let* ((kit (kit-state ext)) (json (to-json palette)) (theme (theme-of ext)) changed)
    (sb-thread:with-mutex ((kit-state-lock kit))
      (setf changed (and (kit-state-palette-json kit) (not (equal json (kit-state-palette-json kit)))))
      (theme-set-palette theme palette)
      (setf (kit-state-palette-json kit) json))
    (when changed (kit-feed-publish ext (kit-state-theme-feed kit) theme))))

(defun theme-apply-state (ext state)
  (when (hash-table-p state)
    (let ((palette (jget state "theme")) (expanded (jget state "toolsExpanded")) (kit (kit-state ext)))
      (when (hash-table-p palette) (theme-apply-palette ext palette))
      (when (member expanded '(t :false))
        (let ((value (eq expanded t)) changed)
          (sb-thread:with-mutex ((kit-state-lock kit))
            (setf changed (and (kit-state-tools-known kit) (not (eq (kit-state-tools-expanded kit) value)))
                  (kit-state-tools-expanded kit) value
                  (kit-state-tools-known kit) t))
          (when changed (kit-feed-publish ext (kit-state-tools-feed kit) value)))))))

(def-ready-hook "theme" (ext ready) (theme-apply-state ext (jget ready "state")))
(def-state-hook "theme" (ext state) (theme-apply-state ext state))

(def-notify "theme_change" (ext args)
  (theme-apply-palette ext (if (plusp (hash-table-count args)) args (obj))))

(defun ctx-on-theme-change (ctx handler)
  "Subscribe to changes of the host's theme: the user switching theme, a custom theme's file reloading, the terminal reporting new colors.
HANDLER, (lambda (theme)), receives CTX-THEME after it took the new palette. Handlers run on a worker thread, one at a time in the order the
changes arrived, never on the thread that reads the host's replies, so a handler can make a blocking host call; one that signals is
reported on stderr and does not stop the others. Returns the idempotent unsubscribe function. A palette equal to the one before is not a change."
  (unless (functionp handler) (error 'type-error :datum handler :expected-type 'function))
  (kit-feed-subscribe (kit-state-theme-feed (kit-state (ctx-extension ctx))) handler))

(defun ctx-on-tools-expanded-change (ctx handler)
  "Subscribe to the host toggling tool-output expansion (Pi's expand key, or CTX-SET-TOOLS-EXPANDED from any extension). HANDLER,
(lambda (expanded-p)), receives the new state after it changed. It follows the contract of CTX-ON-THEME-CHANGE."
  (unless (functionp handler) (error 'type-error :datum handler :expected-type 'function))
  (kit-feed-subscribe (kit-state-tools-feed (kit-state (ctx-extension ctx))) handler))
