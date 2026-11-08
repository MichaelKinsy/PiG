### Changed

- A write call that streams in, and a TypeScript or JavaScript code block that streams into an answer, are highlighted from where the last update left off instead of from their first line on every update. Writing a 50 KB TypeScript file takes the terminal 2.9 s of CPU instead of 4.9 s, and the write card's own share falls from 2.6 s to 0.4 s: after every update the first 50 lines were highlighted again as one block, and the last line once more to count the lines. The highlighted lines are the ones a whole run returns, and the card is drawn as before.
