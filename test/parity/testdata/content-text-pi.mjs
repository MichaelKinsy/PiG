import { contentText } from '../../../extensions/sdk-ts/node_modules/@earendil-works/pi-coding-agent/node_modules/@earendil-works/pi-ai/dist/index.js';
const content = [{type:'thinking',thinking:'reasoning'}, {type:'text',text:'first'}, {type:'toolCall',id:'1',name:'read',arguments:{}}, {type:'text',text:'second'}];
const result = [{type:'text',text:'first'}, {type:'image',data:'...',mimeType:'image/png'}, {type:'text',text:'second'}];
console.log(JSON.stringify([contentText(content),contentText(content,''),contentText('hello'),contentText(result,'')]));
