"""Generate fixed persona cues using the deployed Breeze and checked-in reference."""
import base64, hashlib, json, os, pathlib, urllib.request, wave
ROOT=pathlib.Path(__file__).resolve().parents[1]
GROUPS={
 'waiting':['One moment.','Just a moment.','Let me check that.','Let me take a look.'],
 'lookup':["I'm checking the details.","I'm looking that up.","Let me check the information.","I'll take a look at that."],
 'network':["I'm checking your connection.","Let me check the network.","I'm checking the connection details."],
 'availability':["I'm checking availability.","Let me look at the available times.","I'm checking the appointment options."],
 'listening':["I'm listening.","Go ahead.","Take your time."],
 'acknowledge':['Okay.','All right.','Thanks for explaining.'],
 'interrupted':["Okay, I've stopped.","All right, I'll pause here."]}
for domain in os.environ.get('VOICE_DOMAINS','telecom,hospital,school').split(','):
 root=ROOT/'agents'/domain/'voice';ref=(root/'reference.wav').read_bytes();transcript=(root/'reference.txt').read_text().strip()
 manifest=[]
 for category,phrases in GROUPS.items():
  if (category=='network' and domain!='telecom') or (category=='availability' and domain=='telecom'):continue
  for i,text in enumerate(phrases):
   id=f'{category}-{i+1}';path=root/'cues'/f'{id}.wav';path.parent.mkdir(exist_ok=True)
   body={'model':'breeze','input':text,'stream':True,'stream_format':'audio','response_format':'pcm','voice_ref':{'type':'base64','data':base64.b64encode(ref).decode()},'reference_text':transcript,'options':{'seed':'42','instruction':'Preserve the reference voice. Speak naturally and conversationally.'}}
   req=urllib.request.Request(os.environ.get('TTS_URL','http://192.168.10.246:8092')+'/v1/audio/speech',json.dumps(body).encode(),{'Content-Type':'application/json'})
   with urllib.request.urlopen(req,timeout=90) as r:pcm=r.read()
   if len(pcm)<4800 or len(pcm)%2:raise ValueError('invalid PCM for '+id)
   with wave.open(str(path),'wb') as w:w.setnchannels(1);w.setsampwidth(2);w.setframerate(24000);w.writeframes(pcm)
   manifest.append({'id':id,'category':category,'text':text,'duration_ms':round(len(pcm)/48)})
   print(domain,id,flush=True)
 (root/'cues.json').write_text(json.dumps({'reference_sha256':hashlib.sha256(ref).hexdigest(),'cues':manifest},indent=2)+'\n')
