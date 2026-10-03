import {mkdtempSync,writeFileSync,rmSync,readFileSync} from 'node:fs';
import {join} from 'node:path';
import {fileURLToPath,pathToFileURL} from 'node:url';
const root=fileURLToPath(new URL('../../../extensions/sdk-ts/node_modules/@earendil-works/pi-coding-agent',import.meta.url));
if (JSON.parse(readFileSync(join(root,'package.json'),'utf8')).version!=='1.0.0') throw new Error('Expected Pi 1.0.0');
const {readClipboardImage}=await import(pathToFileURL(join(root,'dist/utils/clipboard-image.js')));
const {loadPhoton}=await import(pathToFileURL(join(root,'dist/utils/photon.js')));
const dir=mkdtempSync(join(process.env.PARITY_CLIPBOARD_DIR,'clipboard-pi-'));
try {
  writeFileSync(join(dir,'wl-paste'),`#!/usr/bin/env node
const data=Buffer.alloc(58); data.write('BM');
for(const [offset,value] of [[2,58],[10,54],[14,40],[18,1],[22,1],[34,4]]) data.writeUInt32LE(value,offset);
data.writeUInt16LE(1,26);data.writeUInt16LE(24,28);data[56]=255;
process.stdout.write(process.argv.includes('--list-types')?'image/bmp\\n':data);
`,{mode:0o700});
  process.env.PATH=dir+':'+process.env.PATH;
  const image=await readClipboardImage({platform:'linux',env:{WAYLAND_DISPLAY:'wayland-0'}});
  if (!image) throw new Error('No clipboard image');
  const photon=await loadPhoton();
  const decoded=photon.PhotonImage.new_from_byteslice(image.bytes);
  try { console.log(JSON.stringify({mimeType:image.mimeType,signature:Buffer.from(image.bytes.subarray(0,4)).toString('hex'),width:decoded.get_width(),height:decoded.get_height(),rgba:Array.from(decoded.get_raw_pixels()).slice(0,4)})); }
  finally {decoded.free();}
} finally {rmSync(dir,{recursive:true,force:true});}
