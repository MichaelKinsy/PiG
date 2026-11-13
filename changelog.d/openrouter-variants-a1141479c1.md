### Changed

- PiG holds each OpenRouter `:nitro` and `:floor` routing variant once. A variant was kept twice once a model list had been read (at a start with `--model`, in the model selector, by `--list-models`): as an entry of its own and as a copy in the list. The list is now the one place a variant is held, and a lookup returns the list's entry: 0.32 MB less live heap after the list is read (2,531,608 to 2,212,648 bytes, 803 fewer objects). The model lists, every lookup and the `--list-models` output are unchanged.
