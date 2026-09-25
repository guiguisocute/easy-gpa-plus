import assert from 'node:assert/strict'
import test from 'node:test'
import { adjudicationKey, adjudicationPath } from './adjudicationScope.ts'

test('副班管的所有仲裁队列与详情使用独立接口和缓存，不读取班管的全班队列', () => {
  for (const resource of ['submissions', 'appeals', 'objections', 'reports', 'review-progress/issues', 'final-scorecards', 'classification-suggestions']) {
    const key = ['admin', resource, 'same-object']
    assert.equal(adjudicationPath('deputy', `/admin/${resource}/same-object`), `/review/deputy/${resource}/same-object`)
    assert.deepEqual(adjudicationKey('deputy', key), ['review', 'deputy', resource, 'same-object'])
    assert.notDeepEqual(adjudicationKey('deputy', key), adjudicationKey('admin', key))
  }
})

test('共用申诉详情在学生与班管页保留原接口，在副班管页使用专用接口与缓存', () => {
  assert.equal(adjudicationPath('admin', '/appeals/123'), '/appeals/123')
  assert.equal(adjudicationPath('deputy', '/appeals/123'), '/review/deputy/appeals/123')
  assert.deepEqual(adjudicationKey('admin', ['appeals', '123']), ['appeals', '123'])
  assert.deepEqual(adjudicationKey('deputy', ['appeals', '123']), ['review', 'deputy', 'appeals', '123'])
})
