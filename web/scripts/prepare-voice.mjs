import {mkdirSync,copyFileSync} from 'node:fs';
import {fileURLToPath} from 'node:url';
const root=fileURLToPath(new URL('../',import.meta.url));
const target=root+'public/voice-assets/';mkdirSync(target,{recursive:true});
for(const file of ['vad.worklet.bundle.min.js','silero_vad_v6.onnx'])copyFileSync(root+'node_modules/@ricky0123/vad-web/dist/'+file,target+file);
for(const file of ['ort-wasm-simd-threaded.mjs','ort-wasm-simd-threaded.wasm'])copyFileSync(root+'node_modules/onnxruntime-web/dist/'+file,target+file);
