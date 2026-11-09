### Fixed

- `pi.sendUserMessage(content, { expandPromptTemplates: true })` and the same option on a `withSession` context now reach the Session from Go, Rust, Python, Lisp and Node extensions (`Context.SendUserMessageWith`, `send_user_message_with`, `expand_prompt_templates=`, `:expand-prompt-templates`). The host used to drop the option, so a message such as `/my-command` or `/skill:name` was sent to the model as plain text. `ctx.ui.theme.sourceInfo` is now set on the Node runtime theme. <!-- fix: sdkhostgaps -->
