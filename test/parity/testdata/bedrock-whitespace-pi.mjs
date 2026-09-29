import {createServer} from 'node:http';
import {readFileSync} from 'node:fs';
import {getModel} from '../../../extensions/sdk-ts/node_modules/@earendil-works/pi-coding-agent/node_modules/@earendil-works/pi-ai/dist/compat.js';
import {stream} from '../../../extensions/sdk-ts/node_modules/@earendil-works/pi-coding-agent/node_modules/@earendil-works/pi-ai/dist/api/bedrock-converse-stream.js';
import {normalizeContext} from '../../../extensions/sdk-ts/node_modules/@earendil-works/pi-coding-agent/node_modules/@earendil-works/pi-ai/dist/utils/transcript.js';
for (const key of ['AWS_PROFILE','AWS_DEFAULT_PROFILE','AWS_CONFIG_FILE','AWS_SHARED_CREDENTIALS_FILE','AWS_BEARER_TOKEN_BEDROCK','AWS_ACCESS_KEY_ID','AWS_SECRET_ACCESS_KEY','AWS_SESSION_TOKEN']) delete process.env[key];
if (process.env.BEDROCK_PROBE_HOME) process.env.HOME = process.env.BEDROCK_PROBE_HOME;
const cases = JSON.parse(readFileSync(new URL('./bedrock-whitespace-cases.json', import.meta.url), 'utf8'));
for (const id of ['anthropic.claude-sonnet-4-5-20250929-v1:0', 'amazon.nova-lite-v1:0']) {
  for (const row of cases) {
    let messages;
    const server = createServer(async (request, response) => {
      const chunks = [];
      for await (const chunk of request) chunks.push(chunk);
      messages = JSON.parse(Buffer.concat(chunks).toString('utf8')).messages;
      response.writeHead(400, {'Content-Type':'application/json'});
      response.end(JSON.stringify({message:'captured request'}));
    });
    await new Promise(resolve => server.listen(0, '127.0.0.1', resolve));
    try {
      const model = {...getModel('amazon-bedrock', id), baseUrl:`http://127.0.0.1:${server.address().port}`};
      const text = row.text;
      const request = normalizeContext({messages:[
        {role:'user', content:text, timestamp:1},
        {role:'user', content:[{type:'text',text}], timestamp:2},
        {role:'assistant', api:model.api, provider:model.provider, model:id, stopReason:'stop', timestamp:3, content:[
          {type:'text',text}, {type:'thinking',thinking:'reason',thinkingSignature:text}, {type:'toolCall',id:'call',name:'tool',arguments:{}},
        ]},
        {role:'toolResult', toolCallId:'call', toolName:'tool', content:[{type:'text',text}], isError:false, timestamp:4},
      ]});
      const result = await stream(model, request, {cacheRetention:'none', env:{AWS_BEDROCK_SKIP_AUTH:'1',AWS_REGION:'us-east-1',AWS_BEDROCK_FORCE_HTTP1:'1',HTTP_PROXY:'',HTTPS_PROXY:'',ALL_PROXY:'',NO_PROXY:'*'}}).result();
      if (!messages) throw new Error('request did not reach HTTP serializer: ' + JSON.stringify(result));
      console.log(JSON.stringify({model:id,case:row.name,messages}));
    } finally {
      await new Promise((resolve,reject) => server.close(error => error ? reject(error) : resolve()));
    }
  }
}
