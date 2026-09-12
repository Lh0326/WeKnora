import test from 'node:test'
import assert from 'node:assert/strict'
import { classifyChatError, createChatFailure, getMessageChatFailure, presentChatError } from './chatErrorPresentation'

const quota = 'API request failed with status 403: {"code":"AllocationQuota.FreeTierOnly","message":"Free quota exhausted.","request_id":"provider-request"}'

test('an explicit free-tier quota code takes precedence over HTTP 403', () => {
  const view = presentChatError(createChatFailure(quota))
  assert.equal(view.kind, 'free_quota')
  assert.equal(view.title, '免费额度已用完')
  assert.match(view.summary, /免费额度/)
  assert.doesNotMatch(view.summary, /\{|request_id|充值|付费|qwen/)
  assert.equal(view.details, quota)
})

test('ordinary access-denied 403 is not described as exhausted quota', () => {
  const view = presentChatError(createChatFailure('API request failed with status 403: {"code":"AccessDenied"}'))
  assert.equal(view.kind, 'forbidden')
  assert.match(view.summary, /权限/)
  assert.doesNotMatch(view.summary, /额度/)
  assert.equal(classifyChatError('HTTP 429'), 'rate_limit')
  assert.equal(classifyChatError({ error: { code: 'insufficient_quota' } }), 'quota')
})

test('failed model is never inferred from the requested chat model or a provider quota code', () => {
  const failure = createChatFailure(quota, { model_id: 'requested-chat', model_name: 'requested-chat-name' })
  assert.equal(failure.modelName, undefined)
  assert.equal(failure.providerName, undefined)
  assert.equal(failure.modelType, undefined)
  const actual = createChatFailure(quota, { error_context: { model_name: 'retrieval-model', provider: 'configured-provider', model_type: 'embedding' } })
  assert.equal(actual.modelName, 'retrieval-model')
  assert.equal(actual.modelType, 'embedding')
})

test('historical service envelopes render as failures, explanatory answers do not', () => {
  assert.equal(getMessageChatFailure({ role: 'assistant', is_completed: true, content: quota })?.raw, quota)
  assert.equal(getMessageChatFailure({ role: 'assistant', is_completed: true, content: '这个错误码 AllocationQuota.FreeTierOnly 表示免费额度限制。' }), null)
  assert.equal(getMessageChatFailure({ role: 'user', is_completed: true, content: quota }), null)
  assert.equal(getMessageChatFailure({ role: 'assistant', is_completed: false, content: quota }), null)
})

test('expanded diagnostics keep request IDs but redact upstream-echoed credentials', () => {
  const raw = 'HTTP 403 {"Authorization":"Bearer example-secret","api_key":"example-key","request_id":"keep-id"}'
  const view = presentChatError(createChatFailure(raw), 'en-US')
  assert.doesNotMatch(view.details, /example-secret|example-key/)
  assert.match(view.details, /keep-id/)
  assert.equal(view.title, 'Request not authorized')
})

test('each supported locale has an actionable summary without exposing the JSON body', () => {
  for (const locale of ['zh-CN', 'en-US', 'ru-RU', 'ko-KR']) {
    const view = presentChatError(createChatFailure(quota), locale)
    assert.ok(view.title && view.summary && view.labels.details && view.labels.retry)
    assert.doesNotMatch(view.summary, /request_id/)
  }
})
