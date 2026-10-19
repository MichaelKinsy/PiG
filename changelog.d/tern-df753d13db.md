### Fixed

- A Durable core listing of all or terminal tasks (`ScanTasks`) reads one page from storage per call instead of loading every task: a page costs the same at any task count. Listing a conversation owner's conversations reads only that owner's.
