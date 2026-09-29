// Browser transport only: finalized speech uses the same message API as typing.
export type VoiceCallbacks = {
 status: (status: string) => void; transcript: (text: string) => void;
 final: (text: string) => void; interrupt: () => void;
 error: (message: string) => void; metric: (name: string, ms: number) => void;
};
export class Voice {
 private context?: AudioContext;
 private media?: MediaStream;
 private node?: AudioWorkletNode;
 private socket?: WebSocket;
 private active = false;
 private muted = false;
 private closed = false;
 private speechFrames = 0;
 private silence = 0;
 private frames = 0;
 private preRoll: ArrayBuffer[] = [];
 private output = new Set<AudioBufferSourceNode>();
 private abort?: AbortController;
 private generation = 0;
 private queue: {text: string; turn: string}[] = [];
 private producing = false;
 private text = '';
 private turn = '';
 private blockedTurn = ''; 
 private nextAudio = 0;
 private lastSpeech = 0;
 private firstPlayback = false;
 private outputEnabled = true;
 constructor(private session: string, private cb: VoiceCallbacks) {}
 async start() {
  this.cb.status('Requesting microphone…');
  try {
   this.context = new AudioContext(); await this.context.resume();
   this.media = await navigator.mediaDevices.getUserMedia({audio:{echoCancellation:true,noiseSuppression:true,autoGainControl:true,channelCount:1}});
   if(this.closed){this.media.getTracks().forEach(t=>t.stop());return;}
   await this.context.audioWorklet.addModule('/pcm-worklet.js');
   if(this.closed)return;
   const input=this.context.createMediaStreamSource(this.media);
   this.node=new AudioWorkletNode(this.context,'pcm-capture');
   this.node.port.onmessage=e=>this.frame(e.data.pcm,e.data.rms);
   input.connect(this.node); this.node.connect(this.context.destination);
   this.cb.status('Listening');
  } catch(e) { this.stop(); throw e; }
 }
 private frame(pcm:ArrayBuffer,rms:number) {
  if(this.closed||this.muted)return;
  this.preRoll.push(pcm);if(this.preRoll.length>3)this.preRoll.shift();
  const speaking=rms>(this.output.size ? 0.035 : 0.018);
  if(speaking){this.lastSpeech=performance.now();this.speechFrames++;}else{this.speechFrames=0;}
  if(!this.active && !this.socket && this.speechFrames>=2){
   this.active=true;this.silence=0;this.frames=0;this.firstPlayback=false;
   this.stopOutput();this.cb.interrupt();this.cb.transcript('');this.cb.status('Listening to you…');
   const ws=new WebSocket(`${location.protocol==='https:'?'wss:':'ws:'}//${location.host}/api/sessions/${this.session}/transcribe?utterance=${crypto.randomUUID()}`);
   this.socket=ws;const initial=[...this.preRoll];
   ws.onopen=()=>{if(this.socket!==ws)return;initial.forEach(b=>ws.send(b));};
   let partial='';let final=false;
   ws.onmessage=e=>{
    if(this.socket!==ws)return;
    const data=JSON.parse(e.data);
    if(data.type==='stt.partial'){partial+=data.text;this.cb.transcript(partial);}
    if(data.type==='stt.final'){
     final=true;this.active=false;this.socket=undefined;ws.close();
     const text=(data.text||partial).trim();this.cb.transcript(text);
     this.cb.metric('STT after speech',performance.now()-this.lastSpeech);
     if(text){this.cb.status('Waiting for reply');this.cb.final(text);}else{this.cb.status('Listening');}
    }
    if(data.type==='error'){this.cb.error(data.message);ws.close();}
   };
   ws.onerror=()=>this.cb.error('Speech connection failed. You can still type your message.');
   ws.onclose=()=>{if(this.socket===ws){this.socket=undefined;this.active=false;if(!final){this.cb.status('Listening');this.cb.error('Transcription interrupted. Please try again.');}}};
   return;
  }
  if(this.active){
   this.frames++;
   if(this.socket?.readyState===WebSocket.OPEN){
    if(this.socket.bufferedAmount>128000){this.cb.error('Speech connection is too slow.');this.resetInput();return;}
    this.socket.send(pcm);
   }
   this.silence=speaking?0:this.silence+1;
   if(this.silence>=7||this.frames>=550)this.finishInput();
  }
 }
 finishInput(){
  if(this.active&&this.socket?.readyState===WebSocket.OPEN){this.active=false;this.socket.send(JSON.stringify({type:'finish'}));this.cb.metric('Endpointing',performance.now()-this.lastSpeech);this.cb.status('Transcribing…');}
 }
 private resetInput(){this.active=false;const ws=this.socket;this.socket=undefined;ws?.close();this.preRoll=[];this.speechFrames=0;}
 mute(value:boolean){this.muted=value;this.media?.getAudioTracks().forEach(t=>t.enabled=!value);if(value)this.resetInput();this.cb.status(value?'Microphone muted':'Listening');}
 enableOutput(value:boolean){this.outputEnabled=value;if(!value)this.stopOutput();}
 delta(turn:string,text:string){
  if(this.closed||!this.outputEnabled||turn===this.blockedTurn)return;
  if(turn!==this.turn){this.text='';this.turn=turn;}
  this.text+=text;
  this.flush(false);
 }
 complete(){this.flush(true);}
 private flush(final:boolean){
  // Tool records retain their complete visual representation. Strip technical IDs
  // from their spoken version, without asking a second LLM to reinterpret facts.
  while(this.text.trim()){
   const boundary=this.text.match(/^[\s\S]*?[.!?](?:\s|$)/);
   let n=boundary?.[0].length||0;
   if((!n||n>240)&&this.text.length>240)n=this.text.lastIndexOf(' ',220)+1||220;
   if(!n&&final)n=this.text.length;
   if(!n)break;
   let phrase=this.text.slice(0,n);this.text=this.text.slice(n);
   phrase=phrase.replace(/\bID\s+[^\s·,]+/g,'').replace(/[*#`]/g,'').replace(/\s·\s/g,', ').replace(/^\s*-\s*/gm,'').trim();
   if(phrase){if(this.queue.length>=40){this.cb.error('Spoken reply is too long; the full answer is in chat.');this.text='';break;}this.queue.push({text:phrase,turn:this.turn});}
  }
  void this.produce();
 }
 private async produce(){
  if(this.producing||this.closed)return;this.producing=true;
  const gen=this.generation;
  try{
   while(this.queue.length&&gen===this.generation&&!this.closed){
    // Bound prepared audio; generation can overlap playback of the previous phrase.
    while(this.context&&this.nextAudio-this.context.currentTime>8){await new Promise(r=>setTimeout(r,80));if(gen!==this.generation||this.closed)return;}
    const phrase=this.queue.shift()!;const started=performance.now();
    this.abort=new AbortController();
    const res=await fetch(`/api/sessions/${this.session}/speech`,{method:'POST',headers:{'Content-Type':'application/json'},body:JSON.stringify({text:phrase.text,turn_id:phrase.turn}),signal:this.abort.signal});
    if(!res.ok)throw new Error(`Speech service unavailable (${res.status})`);
    const reader=res.body!.getReader();let first=true;let leftover=new Uint8Array(0);
    for(;;){
     const {value,done}=await reader.read();if(done)break;if(gen!==this.generation)return;
     if(first){first=false;this.cb.metric('TTS first bytes',performance.now()-started);}
     const joined=new Uint8Array(leftover.length+value.length);joined.set(leftover);joined.set(value,leftover.length);
     const n=joined.length-joined.length%2;leftover=joined.slice(n);
     if(n)this.play(joined.subarray(0,n),gen);
    }
    if(first)throw new Error('Speech service returned no audio.');
   }
  }catch(e){if(gen===this.generation&&!this.closed){this.cb.error((e as Error).message);this.cb.status('Listening');}}
  finally{this.producing=false;if(this.queue.length&&!this.closed)void this.produce();}
 }
 private play(bytes:Uint8Array,gen:number){
  const ctx=this.context;if(!ctx||this.closed||gen!==this.generation)return;
  const buffer=ctx.createBuffer(1,bytes.length/2,24000);const samples=buffer.getChannelData(0);const view=new DataView(bytes.buffer,bytes.byteOffset,bytes.byteLength);
  for(let i=0;i<samples.length;i++)samples[i]=view.getInt16(i*2,true)/32768;
  const source=ctx.createBufferSource();source.buffer=buffer;source.connect(ctx.destination);
  const at=Math.max(ctx.currentTime+0.12,this.nextAudio);this.nextAudio=at+buffer.duration;
  this.output.add(source);source.onended=()=>{this.output.delete(source);source.disconnect();if(!this.output.size&&!this.producing&&!this.closed)this.cb.status(this.muted?'Microphone muted':'Listening');};
  source.start(at);this.cb.status('Speaking');
  if(!this.firstPlayback){this.firstPlayback=true;this.cb.metric('Browser playback buffer',(at-ctx.currentTime)*1000);if(this.lastSpeech)this.cb.metric('Speech → reply audio (estimated)',performance.now()-this.lastSpeech+(at-ctx.currentTime)*1000);}
 }
 stopOutput(){
  this.blockedTurn=this.turn;this.generation++;this.abort?.abort();this.queue=[];this.text='';
  for(const source of this.output){source.onended=null;try{source.stop();source.disconnect();}catch{ /* Already ended. */ }}
  this.output.clear();this.nextAudio=0;this.firstPlayback=false;
 }
 stop(){this.closed=true;this.resetInput();this.stopOutput();this.media?.getTracks().forEach(t=>t.stop());this.node?.disconnect();void this.context?.close();this.cb.status('Voice off');}
}
