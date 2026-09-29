import {StdinBuffer} from '../../../extensions/sdk-ts/node_modules/@earendil-works/pi-coding-agent/node_modules/@earendil-works/pi-tui/dist/stdin-buffer.js';
for (const operation of ['empty flush', 'nonempty flush', 'clear']) {
  const buffer = new StdinBuffer();
  let events = [];
  buffer.on('data', value => events.push(value));
  buffer.process('\x1b[64u');
  let flushed = [];
  switch (operation) {
    case 'empty flush': flushed = buffer.flush(); break;
    case 'nonempty flush': buffer.process('\x1b['); flushed = buffer.flush(); break;
    case 'clear': buffer.clear(); break;
  }
  const before = events;
  events = [];
  buffer.process('@');
  console.log(JSON.stringify({operation,before,flushed,after:events}));
  buffer.destroy();
}
