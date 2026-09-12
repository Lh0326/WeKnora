import test from 'node:test'
import assert from 'node:assert/strict'
import type {LearningComponent} from '@/api/learning/components'
import {componentModules,componentLearningLinks} from './componentLearningGuide'

const entry=(key:string,topic:string,prerequisites:string[]=[],related:string[]=[])=>({
 id:`id-${key}`,material:{key,title:`目标 ${key}`,topic,prerequisites,related},
}) as LearningComponent

test('scope options group targets by their actual module, not by node title',()=>{
 const modules=componentModules([entry('a','基础'),entry('b','基础'),entry('c','应用')])
 assert.deepEqual(modules,[{topic:'基础',count:2},{topic:'应用',count:1}])
 assert.deepEqual(componentModules([]),[])
})

test('learning context uses explicit cross-module prerequisites and never treats related as a prerequisite',()=>{
 const components=[entry('a','基础'),entry('b','基础',[],['a']),entry('c','应用',['a','a','missing'])]
 const a=componentLearningLinks(components,'id-a')
 assert.deepEqual(a.before,[])
 assert.deepEqual(a.after.map(c=>c.id),['id-c'])
 const c=componentLearningLinks(components,'id-c')
 assert.deepEqual(c.before.map(c=>c.id),['id-a'])
 assert.equal(c.missing,1)
 assert.deepEqual(componentLearningLinks(components,'absent'),{before:[],after:[],missing:0})
})
