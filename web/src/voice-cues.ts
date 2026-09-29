type Cue={id:string;category:string;text:string;duration_ms:number};
// Cues are UI feedback, never conversation history or evidence of tool success.
export class VoiceCues {
 private clips=new Map<string,AudioBuffer>();
 private cues:Cue[]=[];
 private source?:AudioBufferSourceNode;
 private timer?:ReturnType<typeof setTimeout>;
 private category='waiting';
 private previous='';
 private last=-Infinity;
 private count=0;
 private closed=false;
 private loadAbort=new AbortController();
 constructor(private ctx:AudioContext,private destination:AudioNode,private allowed:()=>boolean,private report:(text:string)=>void){}
 get playing(){return !!this.source;}
 async load(agent:string){
  try{
   const base=`/api/agents/${agent}/voice-cues`;
   const res=await fetch(base,{signal:this.loadAbort.signal});if(!res.ok)return;
   this.cues=(await res.json()).cues;
   // Bound concurrent downloads; all assets are local, prerecorded WAVs.
   const pending=[...this.cues];
   await Promise.all(Array.from({length:3},async()=>{while(pending.length&&!this.closed){const cue=pending.shift()!;const r=await fetch(`${base}/${cue.id}`,{signal:this.loadAbort.signal});if(r.ok){const buffer=await this.ctx.decodeAudioData(await r.arrayBuffer());if(!this.closed)this.clips.set(cue.id,buffer);}}}));
  }catch{/* Voice replies work even if optional cues are unavailable. */}
 }
 begin(){this.cancel();this.count=0;this.schedule('waiting',1200);}
 schedule(category:string,delay=1200){
  this.category=category;
  if(this.timer||this.closed||this.count>=2)return;
  this.timer=setTimeout(()=>{this.timer=undefined;this.play(this.category);},delay);
 }
 play(category:string){
  if(this.closed||!this.allowed()||this.source||this.count>=2||performance.now()-this.last<8000)return;
  let options=this.cues.filter(c=>(c.category===category||(category==='waiting'&&c.category==='acknowledge'))&&this.clips.has(c.id)&&c.id!==this.previous);
  if(!options.length)options=this.cues.filter(c=>c.category==='lookup'&&this.clips.has(c.id)&&c.id!==this.previous);
  if(!options.length)return;
  const cue=options[Math.floor(Math.random()*options.length)];const source=this.ctx.createBufferSource();
  source.buffer=this.clips.get(cue.id)!;source.connect(this.destination);this.source=source;
  this.previous=cue.id;this.last=performance.now();this.count++;
  source.onended=()=>{if(this.source===source)this.source=undefined;source.disconnect();};
  source.start();this.report(cue.text);
 }
 cancel(){if(this.timer)clearTimeout(this.timer);this.timer=undefined;if(this.source){this.source.onended=null;try{this.source.stop();this.source.disconnect();}catch{}this.source=undefined;}}
 close(){this.closed=true;this.cancel();this.loadAbort.abort();this.clips.clear();}
}
