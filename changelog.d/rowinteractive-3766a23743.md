### Changed

- A typed key is matched against the keybindings without copying each action's keys. Every typed character is compared with every app action; the comparison copied the action's key list each time. The keys of one keybinding table are now kept and read. On the fake-model benchmark an interactive session of five prompts runs 1.4 % fewer user instructions.
