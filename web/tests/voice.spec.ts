import { test, expect } from '@playwright/test';
 test.use({permissions:['microphone'],launchOptions:{args:['--use-fake-ui-for-media-stream','--use-fake-device-for-media-stream',`--use-file-for-fake-audio-capture=${process.env.VOICE_WAV||'/tmp/enterprise-voice-mic.wav'}`]}});
// Opt-in hardware integration test: real DGX STT/TTS, deterministic fake microphone.
test.describe('Voice pipeline',()=>{
 test.skip(process.env.VOICE_TESTS!=='true','Requires reachable speech services and WAV fixture');
 test('live microphone, partial recognition, agent response and streamed playback',async({page})=>{
  test.setTimeout(90000);
  const errors:string[]=[];page.on('pageerror',e=>errors.push(e.message));
  await page.goto('/');await page.getByRole('button',{name:'Start chat',exact:true}).click();
  await expect(page.getByRole('button',{name:'Start voice',exact:true})).toBeEnabled();

  await page.getByRole('button',{name:'Start voice',exact:true}).click();
  await expect(page.locator('.live-transcript')).toContainText('appointment',{timeout:30000});
  await expect(page.locator('.bubble.user')).toContainText('Tuesday',{timeout:15000});
  await expect(page.locator('.voice-controls')).toContainText('Speaking',{timeout:45000});
  await page.getByRole('tab',{name:'activity',exact:true}).click();
  await expect(page.locator('.trace')).toContainText('stt.final');
  await expect(page.locator('.trace')).toContainText('tts.first_audio');
  if(process.env.REFERENCE_VOICE_TESTS==='true')await expect(page.locator('.trace')).toContainText('\"reference_voice\": true');
  await expect(page.getByLabel('Live latency metrics')).toContainText(/\d+ ms/);
  await expect(page.locator('.trace')).toContainText('tts.completed',{timeout:20000});
  await page.getByRole('button',{name:'End voice',exact:true}).click();
  await expect(page.getByRole('button',{name:'Start voice',exact:true})).toBeVisible();
  expect(errors).toEqual([]);
 });
});
test('text appears incrementally before generation completes',async({page})=>{
 // A controlled SSE source separates UI rendering from model speed.
 await page.route('**/api/sessions',route=>route.fulfill({json:{id:'stream-test'}}));
 await page.route('**/api/sessions/stream-test/messages',route=>route.fulfill({status:202,json:{turn_id:'t'}}));
 await page.route('**/api/sessions/stream-test/events',route=>route.fulfill({contentType:'text/event-stream',body:': connected\n\n'}));
 await page.addInitScript(()=>{
  class Events {
   static instance:Events;onopen:((e:unknown)=>void)|null=null;onmessage:((e:unknown)=>void)|null=null;onerror=null;
   constructor(){Events.instance=this;setTimeout(()=>this.onopen?.({}),0);}
   close(){}
  }
  (window as any).EventSource=Events;
  (window as any).deliver=(e:unknown)=>Events.instance.onmessage?.({data:JSON.stringify(e)});
 });
 await page.goto('/');await page.getByRole('button',{name:'Start chat',exact:true}).click();
 await page.getByLabel('Message',{exact:true}).fill('Hello');await page.getByRole('button',{name:'Send ↗'}).click();
 const emit=async(id:number,type:string,data:unknown)=>page.evaluate(({id,type,data})=>(window as any).deliver({id,type,data,turn_id:'t',time:new Date().toISOString()}),{id,type,data});
 await emit(1,'agent.response.delta',{text:'Let me check'});
 await expect(page.locator('.bubble.assistant')).toHaveText('SaraLet me check');
 await expect(page.getByRole('button',{name:'Stop',exact:true})).toBeVisible();
 await emit(2,'agent.response.delta',{text:' your connection.'});
 await expect(page.locator('.bubble.assistant')).toContainText('Let me check your connection.');
 await emit(3,'turn.completed',{duration_ms:200});
 await expect(page.getByRole('button',{name:'Stop',exact:true})).toHaveCount(0);
});

test('live model renders text before the turn finishes',async({page})=>{
 test.skip(process.env.LIVE_MODEL_TESTS!=='true','Requires a live model');
 test.setTimeout(90000);
 await page.goto('/');await page.getByRole('button',{name:'Start chat',exact:true}).click();
 await page.getByLabel('Message',{exact:true}).fill('Explain why Wi-Fi is weaker upstairs in about 100 words. Do not run diagnostics or change anything.');
 await page.getByRole('button',{name:'Send ↗'}).click();
 const bubble=page.locator('.bubble.assistant').last();
 await expect(bubble).toBeVisible({timeout:60000});
 const first=(await bubble.textContent())||'';
 await expect(page.getByRole('button',{name:'Stop',exact:true})).toBeVisible();
 await expect.poll(async()=>((await bubble.textContent())||'').length).toBeGreaterThan(first.length);
 await expect(page.getByRole('button',{name:'Send ↗'})).toBeVisible({timeout:60000});
 await expect(page.getByRole('alert')).toHaveCount(0);
});
