;;;; SPDX-License-Identifier: MIT
;;;; pi-tui's terminal-image.ts capability detection and hyperlink. Python SDK: tui/_terminal.py.

(in-package #:pig)

(defstruct (tui-capabilities (:constructor make-tui-capabilities (images true-color hyperlinks)))
  "pi-tui's TerminalCapabilities: IMAGES is NIL, \"kitty\" or \"iterm2\"; TRUE-COLOR and HYPERLINKS are booleans."
  images true-color hyperlinks)

(defun tui-hyperlink (text url)
  "TEXT wrapped in an OSC 8 link to URL (pi-tui's hyperlink)."
  (tui-cat (tui-esc "]8;;") url (tui-esc "\\") text (tui-esc "]8;;") (tui-esc "\\")))

(defun tt-probe-tmux-hyperlinks ()
  "Whether tmux reports hyperlink support (a fixed probe with a 250 ms limit; any failure is false)."
  (handler-case
      (let ((process (sb-ext:run-program "tmux" '("display-message" "-p" "#{client_termfeatures}") :search t :wait nil :input nil :output :stream :error nil)))
        (loop repeat 25 while (sb-ext:process-alive-p process) do (sleep 0.01))
        (if (sb-ext:process-alive-p process)
            (progn (sb-ext:process-kill process 9) (sb-ext:process-close process) nil)
            (let ((features (tui-split (with-output-to-string (out)
                                         (loop for line = (read-line (sb-ext:process-output process) nil) while line do (write-string line out)))
                                       #\,)))
              (prog1 (and (eql (sb-ext:process-exit-code process) 0)
                          (member "hyperlinks" features :test #'string= :key (lambda (f) (string-trim '(#\Space #\Tab #\Newline) f)))
                          t)
                (sb-ext:process-close process)))))
    (error () nil)))

(defun tt-detect-from-environment (env platform tmux-forwards-hyperlink)
  (flet ((get (key) (let ((v (gethash key env))) (if (stringp v) v ""))))
    (let* ((term-program (tui-js-lower (get "TERM_PROGRAM"))) (terminal-emulator (tui-js-lower (get "TERMINAL_EMULATOR")))
           (term (tui-js-lower (get "TERM"))) (color-term (tui-js-lower (get "COLORTERM")))
           (true-color-hint (or (member color-term '("truecolor" "24bit") :test #'string=)
                                (and (>= (length term) 7) (string= "-direct" term :start2 (- (length term) 7))))))
      (flet ((starts (prefix text) (and (>= (length text) (length prefix)) (string= prefix text :end2 (length prefix))))
             (set-p (key) (plusp (length (get key)))))
        (cond
          ((or (set-p "TMUX") (starts "tmux" term)) (make-tui-capabilities nil (and true-color-hint t) (and (funcall tmux-forwards-hyperlink) t)))
          ((starts "screen" term) (make-tui-capabilities nil (and true-color-hint t) nil))
          ((or (set-p "KITTY_WINDOW_ID") (string= term-program "kitty")) (make-tui-capabilities "kitty" t t))
          ((or (string= term-program "ghostty") (search "ghostty" term) (set-p "GHOSTTY_RESOURCES_DIR")) (make-tui-capabilities "kitty" t t))
          ((or (set-p "WEZTERM_PANE") (string= term-program "wezterm")) (make-tui-capabilities "kitty" t t))
          ((or (string= term-program "warpterminal") (set-p "WARP_SESSION_ID") (set-p "WARP_TERMINAL_SESSION_UUID")) (make-tui-capabilities "kitty" t t))
          ((or (set-p "ITERM_SESSION_ID") (string= term-program "iterm.app")) (make-tui-capabilities "iterm2" t t))
          ((set-p "WT_SESSION") (make-tui-capabilities nil t t))
          ((member term-program '("alacritty" "vscode" "zed") :test #'string=) (make-tui-capabilities nil t t))
          ((string= terminal-emulator "jetbrains-jediterm") (make-tui-capabilities nil t nil))
          ((equal platform "win32") (make-tui-capabilities nil t nil))
          (t (make-tui-capabilities nil (and true-color-hint t) nil)))))))

(defun tt-boolean-override (value) (cond ((equal value "1") t) ((equal value "0") :false)))

(defun tt-detect (env platform tmux-forwards-hyperlink)
  "Detection over the environment ENV (a hash-table of strings), PLATFORM and the tmux probe function."
  (let* ((hyperlinks (tt-boolean-override (gethash "PI_HYPERLINKS" env)))
         (detected (tt-detect-from-environment env platform (cond (hyperlinks (lambda () (eq hyperlinks t))) (tmux-forwards-hyperlink) (t #'tt-probe-tmux-hyperlinks))))
         (images-env (gethash "PI_IMAGE_PROTOCOL" env)) (true-color (tt-boolean-override (gethash "PI_TRUE_COLOR" env))))
    (let ((protocol (and (stringp images-env) (tui-js-lower images-env))))
      (cond ((member protocol '("kitty" "iterm2") :test #'equal) (setf (tui-capabilities-images detected) protocol))
            ((member protocol '("none" "0") :test #'equal) (setf (tui-capabilities-images detected) nil))))
    (when true-color (setf (tui-capabilities-true-color detected) (eq true-color t)))
    (when hyperlinks (setf (tui-capabilities-hyperlinks detected) (eq hyperlinks t)))
    detected))

(defun tt-environment ()
  (let ((env (make-hash-table :test 'equal)))
    (dolist (entry (sb-ext:posix-environ) env)
      (let ((eq (position #\= entry))) (when eq (setf (gethash (subseq entry 0 eq) env) (subseq entry (1+ eq))))))))

(defun tui-detect-capabilities (&optional tmux-forwards-hyperlink)
  "The terminal's capabilities from the environment (pi-tui's detectCapabilities). PI_HYPERLINKS, PI_IMAGE_PROTOCOL and PI_TRUE_COLOR override
detection; without the function TMUX-FORWARDS-HYPERLINK a tmux session is probed."
  (tt-detect (tt-environment) (if (member :win32 *features*) "win32" "other") tmux-forwards-hyperlink))

(defvar *tt-cached* nil)
(defvar *tt-overrides* nil "The capability overrides as a plist (:images :true-color :hyperlinks), absent members unset.")

(defun tt-apply-overrides (caps overrides)
  (let ((images (getf overrides :images)) (true-color (getf overrides :true-color)) (hyperlinks (getf overrides :hyperlinks)))
    (make-tui-capabilities (if images (if (equal images "none") nil images) (tui-capabilities-images caps))
                           (if true-color (eq true-color t) (tui-capabilities-true-color caps))
                           (if hyperlinks (eq hyperlinks t) (tui-capabilities-hyperlinks caps)))))

(defun tui-get-capabilities ()
  "The detected capabilities with the overrides on top, cached; the host terminal's published capabilities replace them (pi-tui's getCapabilities)."
  (let ((published (ts-take-capabilities)))
    (when published
      (let ((images (jget published "images")))
        (setf *tt-cached* (make-tui-capabilities (and (stringp images) (plusp (length images)) images)
                                                 (eq (jget published "trueColor") t) (eq (jget published "hyperlinks") t)))))
    (or *tt-cached*
        (setf *tt-cached*
              (let ((hyperlinks (getf *tt-overrides* :hyperlinks)))
                (tt-apply-overrides (tui-detect-capabilities (and hyperlinks (lambda () (eq hyperlinks t)))) *tt-overrides*))))))

(defun tui-get-terminal-color-mode (&optional capabilities)
  "\"truecolor\" when the capabilities report it, else \"256color\" (pi-tui's getTerminalColorMode)."
  (if (tui-capabilities-true-color (or capabilities (tui-get-capabilities))) "truecolor" "256color"))

(defun tui-reset-capabilities-cache ()
  "Drop the cached capabilities (pi-tui's resetCapabilitiesCache)."
  (setf *tt-cached* nil))

(defun tui-set-capability-overrides (&key images true-color hyperlinks)
  "Replace the capability overrides, dropping the cache only when one changed (pi-tui's setCapabilityOverrides). IMAGES is \"kitty\", \"iterm2\"
or \"none\"; TRUE-COLOR and HYPERLINKS are T or :FALSE; NIL leaves a member to detection."
  (let ((overrides (list :images images :true-color true-color :hyperlinks hyperlinks)))
    (unless (equal overrides *tt-overrides*)
      (setf *tt-overrides* overrides *tt-cached* nil))
    nil))

(defun tui-set-capabilities (capabilities)
  "Replace the cached capabilities (pi-tui's setCapabilities)."
  (setf *tt-cached* capabilities)
  nil)
