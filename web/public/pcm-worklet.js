// Average samples into 16kHz bins and deliver 100ms signed PCM frames.
// The output stays silent; microphone audio is never played back locally.
class PCMCapture extends AudioWorkletProcessor {
 constructor(){super();this.samples=new Int16Array(1600);this.index=0;this.phase=0;this.sum=0;this.count=0;this.energy=0;}
 process(inputs){
  const input=inputs[0]?.[0];if(!input)return true;
  for(const sample of input){
   this.sum+=sample;this.count++;this.phase+=16000;
   if(this.phase>=sampleRate){
    this.phase-=sampleRate;const value=Math.max(-1,Math.min(1,this.sum/this.count));this.sum=0;this.count=0;
    this.samples[this.index++]=value<0?value*32768:value*32767;this.energy+=value*value;
    if(this.index===1600){const pcm=this.samples.buffer;this.port.postMessage({pcm,rms:Math.sqrt(this.energy/1600)},[pcm]);this.samples=new Int16Array(1600);this.index=0;this.energy=0;}
   }
  }
  return true;
 }
}
registerProcessor('pcm-capture',PCMCapture);
