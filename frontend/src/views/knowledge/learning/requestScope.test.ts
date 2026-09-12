import test from 'node:test'
import assert from 'node:assert/strict'
import {createRequestScope} from './requestScope'

for (const target of ['library', 'account', 'tenant']) {
  test(`a delayed ${target} response cannot overwrite another scope or a return visit`, async () => {
    let scope = 'first', resolve!: (value:string) => void, displayed = 'current'
    const guard = createRequestScope(() => scope), current = guard.capture()
    const request = new Promise<string>(yes => { resolve = yes })
    const pending = request.then(value => { if (current()) displayed = value })
    scope = 'second'; guard.invalidate(); scope = 'first'; guard.invalidate()
    resolve('obsolete'); await pending
    assert.equal(displayed, 'current')
    assert.equal(guard.capture()(), true)
  })
}
test('unmount invalidates both success and error/finally callbacks', () => {
  const guard = createRequestScope(() => 'same'), current = guard.capture()
  guard.dispose()
  assert.equal(current(), false)
  assert.equal(guard.capture()(), false)
})
