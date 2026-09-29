import {test,expect} from '@playwright/test';
import {readFileSync} from 'node:fs';
test.skip(process.env.VOICE_INTERRUPT_TESTS!=='true','Requires live inference and /tmp/voice-stop.wav');
test('speech during playback immediately stops audio and cancels the old turn',async({page})=>{
 test.setTimeout(90000);
 await page.addInitScript(()=>{
  let mic:AudioContext;let destination:MediaStreamAudioDestinationNode;
  (window as any).audioStops=[];
  const stop=AudioBufferSourceNode.prototype.stop;
  AudioBufferSourceNode.prototype.stop=function(...args){(window as any).audioStops.push(performance.now());return stop.apply(this,args);};
  navigator.mediaDevices.getUserMedia=async()=>{mic=new AudioContext();await mic.resume();destination=mic.createMediaStreamDestination();return destination.stream;};
  (window as any).say=async(data:string)=>{const bytes=Uint8Array.from(atob(data),c=>c.charCodeAt(0));const audio=await mic.decodeAudioData(bytes.buffer);const source=mic.createBufferSource();source.buffer=audio;source.connect(destination);const now=performance.now();source.start();return now;};
 });
 const requests:string[]=[];page.on('request',r=>{if(r.url().endsWith('/messages'))requests.push(r.url());});
 await page.goto('/');await page.getByRole('button',{name:'Start chat',exact:true}).click();
 await page.getByRole('button',{name:'Start voice',exact:true}).click();
 await expect(page.locator('.voice-controls')).toContainText('Listening',{timeout:20000});
 await page.getByLabel('Message',{exact:true}).fill('Explain how Wi-Fi works in about 200 words. Do not use tools or change anything.');
 await page.getByRole('button',{name:'Send ↗'}).click();
 await expect(page.locator('.voice-controls')).toContainText('Speaking',{timeout:45000});
 await page.evaluate(()=>(window as any).audioStops=[]);
 const started=await page.evaluate(data=>(window as any).say(data),readFileSync('/tmp/voice-stop.wav').toString('base64'));
 await expect.poll(()=>page.evaluate(()=>(window as any).audioStops.length),{timeout:2000}).toBeGreaterThan(0);
 const stopped=await page.evaluate(()=>(window as any).audioStops[0]);
 expect(stopped-started).toBeLessThan(1200);
 await expect(page.locator('.live-transcript')).toContainText(/stop/i,{timeout:15000});
 await expect(page.getByRole('button',{name:'Stop',exact:true})).toHaveCount(0,{timeout:15000});
 await page.waitForTimeout(2000);
 expect(requests).toHaveLength(1); // "Stop please" controls playback, not a fresh LLM request.
 await expect(page.getByRole('alert')).toHaveCount(0);
 await page.getByRole('button',{name:'End voice',exact:true}).click();
 console.log(`Microphone onset to audio stop: ${Math.round(stopped-started)} ms`);
});
