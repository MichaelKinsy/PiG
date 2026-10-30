;;;; SPDX-License-Identifier: MIT
;;;; pi-coding-agent's keybinding hints (keybinding-hints.ts) and theme helpers (theme.ts): the component themes of pi-tui built from the
;;;; process-global theme, highlight-code and init-theme. The keys come from the global keybindings the host published and the colors from the
;;;; global theme: the one the host last published, or the one init-theme installed since. Python SDK: keybinding_hints.py, theme_helpers.py.

(in-package #:pig)

(defun th-format-key-part (darwin part capitalize)
  (let ((display (if (and darwin (every (lambda (c) (< (char-code c) 128)) part) (string-equal part "alt")) "option" part)))
    (if (or (not capitalize) (zerop (length display)) (> (char-code (char display 0)) #xFFFF))
        display
        (tui-cat (sb-unicode:uppercase (string (char display 0))) (subseq display 1)))))

(defun th-format-key-text (darwin key capitalize)
  (format nil "~{~A~^/~}"
          (mapcar (lambda (alternative)
                    (format nil "~{~A~^+~}" (mapcar (lambda (part) (th-format-key-part darwin part capitalize)) (tui-split alternative #\+))))
                  (tui-split key #\/))))

(defun th-darwin-p () (and (member :darwin *features*) t))

(defun format-key-text (key &optional capitalize)
  "pi-coding-agent's formatKeyText: a key identifier as the interface shows it, \"alt\" as \"option\" on macOS."
  (th-format-key-text (th-darwin-p) key capitalize))

(defun th-keys-text (keybinding capitalize)
  (let ((keys (tui-keybindings-get-keys (tui-get-keybindings) keybinding)))
    (if (zerop (length keys)) "" (th-format-key-text (th-darwin-p) (format nil "~{~A~^/~}" (coerce keys 'list)) capitalize))))

(defun key-text (keybinding)
  "pi-coding-agent's keyText: the keys of a keybinding as the interface shows them."
  (th-keys-text keybinding nil))

(defun key-display-text (keybinding)
  "pi-coding-agent's keyDisplayText: key-text with each part capitalized."
  (th-keys-text keybinding t))

(defun key-hint (keybinding description)
  "pi-coding-agent's keyHint: the keys of a keybinding in the dim color and DESCRIPTION in the muted color of the global theme."
  (let ((theme (ts-current-theme)))
    (tui-cat (theme-fg theme "dim" (key-text keybinding)) (theme-fg theme "muted" (format nil " ~A" description)))))

(defun raw-key-hint (key description)
  "pi-coding-agent's rawKeyHint: a hint for a key identifier that is not a keybinding."
  (let ((theme (ts-current-theme)))
    (tui-cat (theme-fg theme "dim" (format-key-text key)) (theme-fg theme "muted" (format nil " ~A" description)))))

;;; ------------------------------------------------------------------ component themes

(defstruct (markdown-theme (:constructor th-markdown-theme))
  "pi-tui's MarkdownTheme as get-markdown-theme returns it: every style is a function of the string to style, drawn with the global theme;
HIGHLIGHT-CODE is (lambda (code &optional lang)) giving a vector of lines."
  heading link link-url code code-block code-block-border quote quote-border hr list-bullet bold italic underline strikethrough highlight-code)

(defstruct (select-list-theme (:constructor th-select-list-theme))
  "pi-tui's SelectListTheme as get-select-list-theme returns it."
  selected-prefix selected-text description scroll-info no-match)

(defstruct (settings-list-theme (:constructor th-settings-list-theme))
  "pi-tui's SettingsListTheme as get-settings-list-theme returns it: LABEL and VALUE are (lambda (text selected)); CURSOR is the drawn string."
  label value description cursor hint)

(defstruct (editor-theme (:constructor th-editor-theme))
  "pi-tui's EditorTheme as get-editor-theme returns it."
  border-color select-list)

(defun th-foreground-of (token) (lambda (text) (theme-fg (ts-current-theme) token text)))

(defun get-markdown-theme ()
  "pi-coding-agent's getMarkdownTheme."
  (th-markdown-theme
   :heading (th-foreground-of "mdHeading") :link (th-foreground-of "mdLink") :link-url (th-foreground-of "mdLinkUrl")
   :code (th-foreground-of "mdCode") :code-block (th-foreground-of "mdCodeBlock") :code-block-border (th-foreground-of "mdCodeBlockBorder")
   :quote (th-foreground-of "mdQuote") :quote-border (th-foreground-of "mdQuoteBorder") :hr (th-foreground-of "mdHr")
   :list-bullet (th-foreground-of "mdListBullet")
   :bold (lambda (text) (theme-bold (ts-current-theme) text)) :italic (lambda (text) (theme-italic (ts-current-theme) text))
   :underline (lambda (text) (theme-underline (ts-current-theme) text)) :strikethrough (lambda (text) (theme-strikethrough (ts-current-theme) text))
   :highlight-code (lambda (code &optional lang) (th-highlight code lang t))))

(defun get-select-list-theme ()
  "pi-coding-agent's getSelectListTheme."
  (th-select-list-theme :selected-prefix (th-foreground-of "accent") :selected-text (th-foreground-of "accent") :description (th-foreground-of "muted")
                        :scroll-info (th-foreground-of "muted") :no-match (th-foreground-of "muted")))

(defun get-editor-theme ()
  "pi-coding-agent's getEditorTheme, which the interactive mode builds its editor with."
  (th-editor-theme :border-color (th-foreground-of "borderMuted") :select-list (get-select-list-theme)))

(defun get-settings-list-theme ()
  "pi-coding-agent's getSettingsListTheme."
  (let ((accent (th-foreground-of "accent")) (muted (th-foreground-of "muted")) (dim (th-foreground-of "dim")))
    (th-settings-list-theme :label (lambda (text selected) (if selected (funcall accent text) text))
                            :value (lambda (text selected) (if selected (funcall accent text) (funcall muted text)))
                            :description dim :cursor (funcall accent (tui-cat (string (code-char #x2192)) " ")) :hint dim)))

(defun th-code-block-lines (code)
  (let ((theme (ts-current-theme)))
    (coerce (mapcar (lambda (line) (theme-fg theme "mdCodeBlock" line)) (tui-split code #\Newline)) 'simple-vector)))

(defun th-highlight (code lang markdown)
  (if (or (null lang) (eq lang :null) (zerop (length lang)))
      (th-code-block-lines code)
      (handler-case
          (let* ((palette (ts-installed-theme-palette))
                 (args (apply #'obj "code" code "lang" lang (append (and markdown (list "markdown" t)) (and palette (list "theme" palette)))))
                 (lines (jget (helper-call "highlight.code" args) "lines")))
            (unless (and (vectorp lines) (not (stringp lines))) (tc-fail "decode highlightCode: the host answered no lines"))
            (coerce (remove-if-not #'stringp lines) 'simple-vector))
        (error () (if markdown (th-code-block-lines code) (coerce (tui-split code #\Newline) 'simple-vector))))))

(defun highlight-code (code &optional lang)
  "pi-coding-agent's highlightCode: CODE highlighted for LANG with the global theme, as a vector with one string per line. Code in a language the
host's highlight.js does not support, or with no language, takes the mdCodeBlock color; code highlight.js fails on stays unstyled. The host's
highlighter runs it; a host that cannot be reached leaves the code unstyled, as a failed highlight does."
  (th-highlight code lang nil))

(defun init-theme (&optional name)
  "pi-coding-agent's initTheme: make the host's theme of that name, or the system theme for NIL, an empty name or one the host has no theme of,
the global theme that every helper above reads, until the host's own theme changes. An error means the host could not be reached and the global
theme is unchanged."
  (let ((theme (jget (helper-call "theme.load" (obj "name" (if (and name (plusp (length name))) name :null))) "theme")))
    (unless (hash-table-p theme) (tc-fail "initTheme: the host answered a theme that is not a palette"))
    (ts-install-theme theme)
    nil))
