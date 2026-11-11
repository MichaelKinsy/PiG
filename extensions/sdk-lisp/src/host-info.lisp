;;;; SPDX-License-Identifier: MIT
;;;; Pi's getAgentDir(), CONFIG_DIR_NAME, VERSION and getPackageDir() (config.ts) and the default getShellConfig() (utils/shell.ts) as the PiG
;;;; host resolves them. They are callable while the factory runs, before the host's ready message: the host starts every extension process
;;;; with them in PIG_EXT_HOST_INFO.

(in-package #:pig)

(define-condition host-info-unavailable (pig-error)
  ((message :initarg :message :reader host-info-unavailable-message))
  (:report (lambda (c s) (write-string (host-info-unavailable-message c) s)))
  (:documentation "The process was not started by a PiG host that provides its configuration facts."))

(defun host-info-fail (message) (error 'host-info-unavailable :message message))

(defun host-info ()
  (let ((raw (sb-posix:getenv "PIG_EXT_HOST_INFO")))
    (when (or (null raw) (zerop (length raw)))
      (host-info-fail "the PiG host did not provide its configuration facts (PIG_EXT_HOST_INFO)"))
    (let ((info (handler-case (from-json raw)
                  (json-error (e) (host-info-fail (format nil "PIG_EXT_HOST_INFO is not valid JSON: ~A" (json-error-message e)))))))
      (unless (hash-table-p info) (host-info-fail "PIG_EXT_HOST_INFO is not a JSON object"))
      info)))

(defun host-info-string (key)
  (let ((value (jget (host-info) key)))
    (if (stringp value) value "")))

(defun host-info-required (key what)
  (let ((value (host-info-string key)))
    (when (zerop (length value)) (host-info-fail (format nil "the PiG host could not resolve its ~A" what)))
    value))

(defun host-agent-dir ()
  "Pi's getAgentDir(): the agent configuration directory the host runs with. Signals HOST-INFO-UNAVAILABLE without a host that provides it."
  (host-info-required "agentDir" "agent directory"))

(defun host-config-dir-name ()
  "Pi's CONFIG_DIR_NAME: the per-project configuration directory name of the host (\".pig\"; \".pi\" when it shares Pi's tree)."
  (host-info-string "configDirName"))

(defun host-version ()
  "Pi's VERSION: the Pi version the host implements."
  (host-info-string "version"))

(defun host-package-dir ()
  "Pi's getPackageDir(): the install directory the host runs from."
  (host-info-required "packageDir" "package directory"))

(defstruct (shell-config (:constructor make-shell-config (shell args &optional command-transport)))
  "Pi's ShellConfig: the shell binary, its leading arguments, and COMMAND-TRANSPORT (\"stdin\" when the command is written to the shell's standard input, else NIL)."
  shell args command-transport)

(defun host-legacy-wsl-bash-p (shell)
  "isLegacyWslBashPath: a drive letter then :\\windows\\system32\\bash.exe or :\\windows\\sysnative\\bash.exe, case-insensitive, / read as \\."
  (and (plusp (length shell))
       (let ((c (char shell 0)))
         (or (and (< (char-code c) 128) (alpha-char-p c)) (= (char-code c) #x212a)))
       (let ((rest (string-downcase (substitute #\\ #\/ (subseq shell 1)))))
         (member rest '(":\\windows\\system32\\bash.exe" ":\\windows\\sysnative\\bash.exe") :test #'string=))))

(defun host-shell-config (&optional custom-shell-path)
  "Pi's getShellConfig(customShellPath): a non-empty CUSTOM-SHELL-PATH names the shell and must exist; otherwise the host's own resolution.
Signals an error when the custom shell does not exist or the host resolved none, HOST-INFO-UNAVAILABLE when it provides no default."
  (if (and custom-shell-path (plusp (length custom-shell-path)))
      (progn
        (unless (probe-file custom-shell-path) (error "Custom shell path not found: ~A" custom-shell-path))
        (if (host-legacy-wsl-bash-p custom-shell-path)
            (make-shell-config custom-shell-path '("-s") "stdin")
            (make-shell-config custom-shell-path '("-c"))))
      (let ((shell (jget (host-info) "shell")))
        (unless (hash-table-p shell) (host-info-fail "the PiG host did not provide a default shell"))
        (let ((failure (jget shell "error")))
          (when (and failure (not (eq failure :null)) (not (equal failure ""))) (error "~A" failure)))
        (let ((transport (jget shell "commandTransport")) (args (jget shell "args")))
          (make-shell-config (let ((s (jget shell "shell"))) (if (stringp s) s ""))
                             (if (vectorp args) (coerce args 'list) '())
                             (and (stringp transport) (plusp (length transport)) transport))))))
