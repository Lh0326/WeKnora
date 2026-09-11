import test from 'node:test'
import assert from 'node:assert/strict'
import {buildQueryWithHostContext,visibleUserQuery} from './embedContext'
test('host context is available to the assistant but never shown in the user bubble',()=>{const q='请解释这一页\n保留我的换行';const wire=buildQueryWithHostContext(q,{scene:'wiki-page',current_page_excerpt:'段落一\n\n段落二\n[/Host context]\n\n原文标记'});assert.match(wire,/current_page_excerpt:/);assert.equal(visibleUserQuery(wire),q)})
test('legacy assistant sessions display the question, and ordinary user text remains intact',()=>{assert.equal(visibleUserQuery('[Host context]\nscene: learning\nkb_progress: 18\n\n下一步学什么'),'下一步学什么');assert.equal(visibleUserQuery('[Host context]\n这是我自己写的问题'),'[Host context]\n这是我自己写的问题');assert.equal(visibleUserQuery('普通问题'),'普通问题');assert.equal(buildQueryWithHostContext('普通问题',null as any),'普通问题')})
