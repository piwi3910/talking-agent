import {test,expect} from '@playwright/test';
import {VoiceCues} from '../src/voice-cues';
import {Voice} from '../src/voice';
test('cues respect activity, category, cancellation and cooldown',async()=>{
 const original=globalThis.fetch;const started:string[]=[];let stopped=0;let allowed=false;
 const context={decodeAudioData:async()=>({duration:.2}),createBufferSource:()=>({buffer:null,connect(){},disconnect(){},start(){started.push('audio')},stop(){stopped++},onended:null})} as unknown as AudioContext;
 globalThis.fetch=async(url:any)=>new Response(String(url).endsWith('voice-cues')?JSON.stringify({cues:[{id:'lookup-1',category:'lookup',text:'Checking the details.',duration_ms:200},{id:'waiting-1',category:'waiting',text:'One moment.',duration_ms:200}]}):new Uint8Array([1,0]));
 const reports:string[]=[];const cues=new VoiceCues(context,{} as AudioNode,()=>allowed,t=>reports.push(t));
 try{
  await cues.load('telecom-support');cues.play('lookup');expect(started).toHaveLength(0);
  allowed=true;cues.play('lookup');expect(reports).toEqual(['Checking the details.']);
  cues.cancel();expect(stopped).toBe(1);expect(cues.playing).toBe(false);
  cues.play('waiting');expect(started).toHaveLength(1); // cooldown
  cues.close();cues.play('lookup');expect(started).toHaveLength(1);
 }finally{globalThis.fetch=original;cues.close();}
});
test('interrupting before the first token blocks late text from that turn',async()=>{
 const original=globalThis.fetch;let requests=0;
 globalThis.fetch=async()=>{requests++;return new Response(new Uint8Array([1,0]));};
 const v=new Voice('test',{status(){},transcript(){},final(){},interrupt(){},error(){},metric(){}},'telecom-support');
 try{
  v.event('turn.started','old');v.stopOutput();v.delta('old','Late answer.');v.complete();
  await new Promise(r=>setTimeout(r,10));expect(requests).toBe(0);
  v.event('turn.started','new');v.delta('new','New answer.');v.complete();
  await new Promise(r=>setTimeout(r,10));expect(requests).toBe(1);
 }finally{v.stop();globalThis.fetch=original;}
});
