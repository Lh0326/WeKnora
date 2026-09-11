import test from 'node:test'
import assert from 'node:assert/strict'
import {readFileSync} from 'node:fs'
import {parse,compileScript} from '@vue/compiler-sfc'

test('JavaScript chat setup cannot evaluate TypeScript generics as runtime comparisons',()=>{
 const {descriptor}=parse(readFileSync(new URL('./index.vue',import.meta.url),'utf8'))
 if(descriptor.scriptSetup?.lang==='ts')return
 const compiled=compileScript(descriptor,{id:'chat-runtime-check'})
 const hazards:string[]=[]
 function visit(value:any){if(!value||typeof value!=='object')return;if(value.type==='BinaryExpression'&&value.operator==='>'&&value.left?.type==='BinaryExpression'&&value.left.operator==='<'&&['ref','shallowRef','computed','reactive'].includes(value.left.left?.name))hazards.push(compiled.content.slice(value.start,value.end));for(const child of Object.values(value)){if(Array.isArray(child))child.forEach(visit);else if(child&&typeof child==='object')visit(child)}}
 visit(compiled.scriptSetupAst)
 assert.deepEqual(hazards,[],'A generic in an untyped script compiles but crashes chat setup in the browser')
})
