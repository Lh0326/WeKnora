import {defineConfig} from 'vite'
import vue from '@vitejs/plugin-vue'
import {fileURLToPath} from 'node:url'
const path=(s:string)=>fileURLToPath(new URL(s,import.meta.url))
export default defineConfig({root:path('./learning'),plugins:[vue()],resolve:{alias:[{find:'@/api/learning/objectives',replacement:path('./learning/mockApi.ts')},{find:'@',replacement:path('../src')}]},server:{host:'127.0.0.1',port:5187,strictPort:true,fs:{allow:[path('..')]}}})
