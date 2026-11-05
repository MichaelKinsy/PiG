;;;; SPDX-License-Identifier: MIT
;;;; Pi's DefaultResourceLoader (core/resource-loader.ts): the skills, prompt templates, themes, context files, system prompt and extensions of a
;;;; project and agent directory. The host owns the loader; one passed to a child session is the very loader the child reads. Call
;;;; LOADER-RELOAD before reading, as Pi requires. Results are Pi's JSON shapes (an extension is its path, resolvedPath, sourceInfo and tools as data).

(in-package #:pig)

(declaim (ftype function extension-factory-p extension-factory-wire))

(defstruct (resource-loader (:include graph-object) (:conc-name loader-) (:constructor %make-loader (ctx handle))))

(defvar *loaded-extension-loaders* (make-hash-table :test 'eq :weakness :key :synchronized t)
  "Result or extension object of LOADER-GET-EXTENSIONS -> the loader it was read from.")

(defun loaded-extension-loader (loaded)
  "The resource loader that LOADED (a result of LOADER-GET-EXTENSIONS or LOADER-LOAD-PROJECT-TRUST-EXTENSIONS, or one of its extensions) was read from."
  (gethash loaded *loaded-extension-loaders*))

(defun loader-extensions-result (loader data)
  (when (hash-table-p data)
    (setf (gethash data *loaded-extension-loaders*) loader)
    (let ((extensions (jget data "extensions")))
      (when (vectorp extensions) (map nil (lambda (e) (when (hash-table-p e) (setf (gethash e *loaded-extension-loaders*) loader))) extensions))))
  data)

(defun make-resource-loader (ctx &key cwd agent-dir settings-manager runtime additional-extension-paths additional-skill-paths
                                   additional-prompt-template-paths additional-theme-paths extension-factories no-extensions
                                   disabled-builtin-extensions no-skills no-prompt-templates no-themes no-context-files system-prompt
                                   append-system-prompt extensions-override skills-override prompts-override themes-override
                                   agents-files-override system-prompt-override append-system-prompt-override)
  "new DefaultResourceLoader(options); every option is Pi's under its kebab-case name and NIL is Pi's undefined. EXTENSION-FACTORIES takes only the
references create-codemode-extension, create-tool-search-extension and create-mcp-extension return; EXTENSIONS-OVERRIDE is not supported (an owner decision is pending, gap SdkSessionGraph):
use ADDITIONAL-EXTENSION-PATHS."
  (when extensions-override
    (error 'unsupported-capability :member "make-resource-loader :extensions-override" :lane "SdkSessionGraph"))
  (dolist (factory extension-factories)
    (unless (extension-factory-p factory)
      (error 'unsupported-capability :member "make-resource-loader :extension-factories (an inline factory)" :lane "SdkSessionGraph")))
  (flet ((callback (fn) (graph-callback-id ctx fn)))
    (let ((wire (obj "cwd" cwd "agentDir" agent-dir
                     "additionalExtensionPaths" additional-extension-paths "additionalSkillPaths" additional-skill-paths
                     "additionalPromptTemplatePaths" additional-prompt-template-paths "additionalThemePaths" additional-theme-paths
                     "noExtensions" (graph-bool-arg no-extensions) "noSkills" (graph-bool-arg no-skills)
                     "noPromptTemplates" (graph-bool-arg no-prompt-templates) "noThemes" (graph-bool-arg no-themes)
                     "noContextFiles" (graph-bool-arg no-context-files)
                     "disabledBuiltinExtensions" disabled-builtin-extensions "systemPrompt" system-prompt "appendSystemPrompt" append-system-prompt
                     "skillsOverride" (callback skills-override) "promptsOverride" (callback prompts-override)
                     "themesOverride" (callback themes-override) "agentsFilesOverride" (callback agents-files-override)
                     "systemPromptOverride" (callback system-prompt-override)
                     "appendSystemPromptOverride" (callback append-system-prompt-override))))
      (when extension-factories
        (setf (gethash "extensionFactories" wire) (map 'vector (lambda (f) (extension-factory-wire f ctx)) extension-factories)))
      (when settings-manager (setf (gethash "settingsManager" wire) (gobj-handle settings-manager)))
      (when runtime (setf (gethash "runtime" wire) (gobj-handle runtime)))
      (%make-loader ctx (graph-new ctx "DefaultResourceLoader" "new" wire)))))

(defun loader-get-extensions (loader)
  "The extensions of the last reload as data: an object with extensions, errors and warnings (see LOADED-EXTENSION-LOADER)."
  (loader-extensions-result loader (graph-call loader "getExtensions")))

(defun loader-load-project-trust-extensions (loader)
  "The bootstrap extension set, loaded with project settings untrusted, that project trust is decided with."
  (loader-extensions-result loader (graph-call-async loader "loadProjectTrustExtensions")))

(defun loader-reload (loader &key resolve-project-trust)
  "Rediscover every resource. RESOLVE-PROJECT-TRUST takes the bootstrap extension set and returns whether the project is trusted."
  (let ((wire (obj)))
    (when resolve-project-trust
      (setf (gethash "resolveProjectTrust" wire) (graph-callback-id (gobj-ctx loader) resolve-project-trust)))
    (graph-call-async loader "reload" wire)
    nil))

(def-graph-methods "loader"
  (get-skills ()) (get-prompts ()) (get-themes ()) (get-agents-files ()) (get-system-prompt ()) (get-system-prompt-source ())
  (get-append-system-prompt ()) (get-append-system-prompt-sources ()) (extend-resources (paths)))
