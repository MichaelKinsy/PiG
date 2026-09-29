import {computeCacheWaste} from '../../../extensions/sdk-ts/node_modules/@earendil-works/pi-coding-agent/dist/core/cache-stats.js';
const zero={input:0,output:0,cacheRead:0,cacheWrite:0,total:0};
const entries=[{cacheWrite:100000,cost:{cacheWrite:.375}},{cacheRead:100000,cacheWrite:5000,cost:{cacheRead:.03,cacheWrite:.019}},{cacheWrite:110000,cost:{cacheWrite:.4125}}].map((usage,index)=>({type:'message',id:String(index),parentId:null,timestamp:'',message:{role:'assistant',api:'anthropic-messages',provider:'test',model:'test-model',content:[],stopReason:'stop',timestamp:index*60000,usage:{input:0,output:10,cacheRead:0,cacheWrite:0,totalTokens:0,...usage,cost:{...zero,...usage.cost}}}}));
const waste=computeCacheWaste(entries,{getModel:()=>({cost:{cacheRead:.3}})});
console.log(JSON.stringify([waste.missedTokens,waste.missedCost,waste.missCount]));
