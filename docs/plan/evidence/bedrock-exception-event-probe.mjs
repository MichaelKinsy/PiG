import http from "node:http";
import zlib from "node:zlib";
const root=process.env.PI_AI_DIST; // .../node_modules/@earendil-works/pi-ai/dist of the pinned Pi package
const { streamSimple } = await import(root+"/api/bedrock-converse-stream.js").then(m=>({streamSimple:m.stream}));
const { getModel } = await import(root+"/compat.js");
function str(name,val){const n=Buffer.from(name),v=Buffer.from(val);const b=Buffer.alloc(1+n.length+1+2+v.length);let o=0;b[o++]=n.length;n.copy(b,o);o+=n.length;b[o++]=7;b.writeUInt16BE(v.length,o);o+=2;v.copy(b,o);return b;}
function frame(headers,body){const h=Buffer.concat(headers);const b=Buffer.from(body);const total=16+h.length+b.length;const buf=Buffer.alloc(total);buf.writeUInt32BE(total,0);buf.writeUInt32BE(h.length,4);buf.writeUInt32BE(zlib.crc32(buf.subarray(0,8)),8);h.copy(buf,12);b.copy(buf,12+h.length);buf.writeUInt32BE(zlib.crc32(buf.subarray(0,total-4)),total-4);return buf;}
const ev=(t,b)=>frame([str(":message-type","event"),str(":event-type",t),str(":content-type","application/json")],b);
process.env.AWS_BEDROCK_SKIP_AUTH="1";process.env.AWS_BEDROCK_FORCE_HTTP1="1";process.env.AWS_REGION="us-east-1";
for (const kind of ["internalServerException","modelStreamErrorException","validationException","throttlingException","serviceUnavailableException","somethingUnknown"]) {
const server=http.createServer((req,res)=>{req.resume();req.on("end",()=>{res.writeHead(200,{"content-type":"application/vnd.amazon.eventstream"});res.write(ev("messageStart",'{"role":"assistant"}'));res.write(ev(kind,'{"message":"bedrock stream failed","originalStatusCode":500,"originalMessage":"orig"}'));res.end();});});
await new Promise(r=>server.listen(0,r));
const model={...getModel("amazon-bedrock","us.anthropic.claude-opus-4-8"),baseUrl:`http://127.0.0.1:${server.address().port}`};
const received=[];
const result=await streamSimple(model,{messages:[{role:"user",content:"hello",timestamp:1}]},{cacheRetention:"none",onProviderStreamEvent:(i)=>{received.push(JSON.stringify(i))}}).result();
console.log(kind, JSON.stringify({received,stop:result.stopReason,err:result.errorMessage}));
server.close();
}
