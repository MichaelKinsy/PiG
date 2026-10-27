(in-package "lisp-sdk-fixture")
;;; the probe commands, tools and event handlers of lane S2 (see def-fixture in fixture.lisp)

(defun graph-a-shell-report (&rest custom)
  (handler-case
      (let* ((config (apply #'pig:host-shell-config custom)) (report (pig:obj "shell" (pig:shell-config-shell config) "args" (coerce (pig:shell-config-args config) 'vector))))
        (when (pig:shell-config-command-transport config) (setf (gethash "commandTransport" report) (pig:shell-config-command-transport config)))
        report)
    (error (e) (pig:obj "error" (princ-to-string e)))))

;;; host_info_probe: getAgentDir, CONFIG_DIR_NAME and VERSION as this SDK answers them, and getAgentDir as the factory saw it
;;; (TestConformance_HostConfigurationAgreesAcrossSDKs).
(def-fixture "host_info_probe" (ext)
  (let ((factory-agent-dir (handler-case (pig:host-agent-dir)
                             (pig:host-info-unavailable (e) (format nil "error: ~A" e)))))
    (pig:command ext "host_info_probe" "Report getAgentDir, CONFIG_DIR_NAME and VERSION"
                 (lambda (ctx args) (declare (ignore args))
                   (pig:ctx-notify ctx (format nil "host_info:~A:end"
                                               (pig:to-json (pig:obj "agentDir" (pig:host-agent-dir) "configDirName" (pig:host-config-dir-name)
                                                                     "version" (pig:host-version) "factoryAgentDir" factory-agent-dir)))
                                   "info")))))

;;; host_paths_probe: getPackageDir and getShellConfig for the default shell and for the existing and the missing custom shell path in
;;; args ("<existing>|<missing>") (TestConformance_HostPathsAgreeAcrossSDKs).
(def-fixture "host_paths_probe" (ext)
  (pig:command ext "host_paths_probe" "Report getPackageDir and getShellConfig"
               (lambda (ctx args)
                 (let ((bar (position #\| args)))
                   (pig:ctx-notify ctx (format nil "host_paths:~A:end"
                                               (pig:to-json (pig:obj "packageDir" (pig:host-package-dir) "default" (graph-a-shell-report)
                                                                     "custom" (graph-a-shell-report (subseq args 0 bar))
                                                                     "missing" (graph-a-shell-report (subseq args (1+ bar))))))
                                   "info")))))
