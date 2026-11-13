### Fixed

- On Windows, attaching a file in a terminal prompt no longer reads a different file when a parent directory is swapped for a link after the first yes: the identity of the confirmed file is read from an open handle, not looked up again by path when it is compared. <!-- fix: 17d97b46d4 -->
- A plugin install that finds no `package.json` (or another problem) names the directory it was going to install into when the plugins root is reached through a link or a Windows short name, not the staging directory. <!-- fix: 17d97b46d4 -->
