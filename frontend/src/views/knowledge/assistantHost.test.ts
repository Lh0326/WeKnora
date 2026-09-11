import { test } from 'node:test'
import assert from 'node:assert/strict'
import { createAssistantHost } from './assistantHost'

test('assistant page context belongs to the foreground tab and account', () => {
  const host = createAssistantHost()
  const currentSlug = () => host.context.value?.current_page_slug
  host.setScope('alice:tenant-a:kb-a')
  host.setScene('learning')
  const reader = host.publisher(['learning'])
  const wiki = host.publisher(['wiki', 'graph'])
  reader({scene:'wiki-page',current_page_slug:'concept/a'})
  assert.equal(currentSlug(), 'concept/a')
  host.setScene('wiki')
  reader({scene:'wiki-page',current_page_slug:'concept/late'})
  assert.deepEqual(host.context.value, {scene:'wiki'})
  wiki({scene:'wiki-page',current_page_slug:'concept/b'})
  assert.equal(currentSlug(), 'concept/b')
  host.setScope('bob:tenant-a:kb-a')
  wiki({scene:'wiki-page',current_page_slug:'concept/alice-private'})
  assert.deepEqual(host.context.value, {scene:'wiki'})
  const bob = host.publisher(['wiki', 'graph'])
  bob({scene:'wiki-page',current_page_slug:'concept/bob'})
  assert.equal(currentSlug(), 'concept/bob')
})

test('independent KB screens and cleared hosts never share page text', () => {
  const a = createAssistantHost(), b = createAssistantHost()
  a.setScope('alice:kb-a'); b.setScope('alice:kb-b')
  a.setScene('learning'); b.setScene('learning')
  const publish = a.publisher(['learning'])
  const content = {scene:'wiki-page',current_page_excerpt:'source content'}
  publish(content)
  content.current_page_excerpt = 'mutated later'
  assert.equal(a.context.value?.current_page_excerpt, 'source content')
  assert.deepEqual(b.context.value, {scene:'learning'})
  a.clear()
  publish({scene:'wiki-page',current_page_excerpt:'late content'})
  assert.equal(a.context.value, null)
})
