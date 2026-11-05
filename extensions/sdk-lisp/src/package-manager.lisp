;;;; SPDX-License-Identifier: MIT
;;;; Pi's DefaultPackageManager (core/package-manager.ts): it installs, removes, resolves and lists the packages named in the user and project
;;;; settings of the SettingsManager it was given. The host owns the manager; results are Pi's JSON shapes.

(in-package #:pig)

(defstruct (package-manager (:include graph-object) (:conc-name pkgmgr-) (:constructor %make-pkgmgr (ctx handle))))

(defun make-package-manager (ctx &key cwd agent-dir settings-manager builtin-extensions)
  "new DefaultPackageManager(options). BUILTIN-EXTENSIONS are the names of built-in extensions resolved as builtin:<name>."
  (%make-pkgmgr ctx (graph-new ctx "DefaultPackageManager" "new"
                               (obj "cwd" cwd "agentDir" agent-dir "settingsManager" (gobj-handle settings-manager)
                                    "builtinExtensions" builtin-extensions))))

(defun pkgmgr-set-progress-callback (manager callback)
  "The function every later install, remove and update reports (type action source message) events to, as an object; NIL clears it."
  (graph-call manager "setProgressCallback" (graph-callback-id (gobj-ctx manager) callback))
  nil)

(defun pkgmgr-local (local) (obj "local" (graph-bool-arg local)))

(defun pkgmgr-add-source-to-settings (manager source &optional local)
  (graph-bool (graph-call manager "addSourceToSettings" source (pkgmgr-local local))))

(defun pkgmgr-remove-source-from-settings (manager source &optional local)
  (graph-bool (graph-call manager "removeSourceFromSettings" source (pkgmgr-local local))))

(defun pkgmgr-get-installed-path (manager source scope)
  "Where the package SOURCE is installed in SCOPE (\"user\" or \"project\"), NIL when it is not."
  (graph-call manager "getInstalledPath" source scope))

(defun pkgmgr-resolve (manager &optional on-missing)
  "Resolve every configured package into an object with extensions, skills, prompts and themes. ON-MISSING takes the source of a package that is not installed and
answers \"install\", \"skip\" or \"error\" (default \"install\")."
  (graph-call-async manager "resolve" (graph-callback-id (gobj-ctx manager) (or on-missing (lambda (source) (declare (ignore source)) "install")))))

(defun pkgmgr-resolve-extension-sources (manager sources &optional local temporary)
  (graph-call-async manager "resolveExtensionSources" sources
                    (obj "local" (graph-bool-arg local) "temporary" (graph-bool-arg temporary))))

(defun pkgmgr-list-configured-packages (manager) (graph-call manager "listConfiguredPackages"))

(defun pkgmgr-install (manager source &optional local)
  (graph-call-async manager "install" source (pkgmgr-local local)) nil)

(defun pkgmgr-install-and-persist (manager source &optional local)
  (graph-call-async manager "installAndPersist" source (pkgmgr-local local)) nil)

(defun pkgmgr-remove (manager source &optional local)
  (graph-call-async manager "remove" source (pkgmgr-local local)) nil)

(defun pkgmgr-remove-and-persist (manager source &optional local)
  (graph-bool (graph-call-async manager "removeAndPersist" source (pkgmgr-local local))))

(defun pkgmgr-update (manager &optional source)
  (graph-call-async manager "update" source) nil)

(defun pkgmgr-check-for-available-updates (manager) (graph-call-async manager "checkForAvailableUpdates"))
