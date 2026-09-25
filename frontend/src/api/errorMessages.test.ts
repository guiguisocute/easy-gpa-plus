import assert from 'node:assert/strict'
import test from 'node:test'
import { ApiError } from './client.ts'
import { readableErrorMessage, userErrorMessage } from './errorMessages.ts'

test('结构化错误原因优先于服务端文案，并用参数生成可翻译的范围提示', () => {
  const detail = { reason: 'claim_score_out_of_range', params: { min: 0, max: 12 } }
  const error = new ApiError(422, 'submission_invalid', 'enum score must be between 0 and 12.000', detail)
  assert.equal(error.message, '期望分数须在 0 至 12 分之间')
  assert.equal(error.code, 'submission_invalid')
  assert.equal(error.detail, detail)
  assert.equal(new ApiError(422, 'submission_invalid', '', { reason: 'claim_score_above_maximum', params: { max: 2.5 } }).message, '期望分数不能超过 2.5 分')
})

test('旧英文校验、未知接口错误和错误格式的响应均有中文提示', () => {
  assert.equal(new ApiError(422, 'submission_invalid', 'score is required').message, '请填写期望分数')
  assert.equal(new ApiError(422, 'submission_invalid', 'quantity is required').message, '请填写已完成的数量')
  for (const message of ['source is required', 'unknown enum option "bad"', '']) {
    assert.match(new ApiError(422, 'submission_invalid', message).message, /申报内容.*检查/)
  }
  for (const status of [400, 401, 403, 404, 409, 413, 422, 429, 500, 502, 504]) {
    assert.match(new ApiError(status, 'unrecognized_code', 'technical diagnostic').message, /\p{Script=Han}/u)
  }
  assert.equal(new ApiError(422, 'constructor', '').message, '提交内容未通过校验，请检查填写的信息后重试')
})

test('保留具体的中文业务原因，解析异常中即便包含中文也不直接显示', () => {
  assert.equal(new ApiError(409, 'sealed', '账号已封存，请联系班级管理员').message, '账号已封存，请联系班级管理员')
  assert.equal(readableErrorMessage('Unexpected token: "测试方案" is not valid JSON', '请检查方案格式'), '请检查方案格式')
  assert.equal(readableErrorMessage({ message: '不可直接展示对象' }, '操作失败'), '操作失败')
  assert.equal(userErrorMessage(new Error('Failed to fetch')), '网络连接中断，请检查网络后重试')
  assert.equal(userErrorMessage(new Error('Cannot read properties of undefined'), '无法打开文件，请重试'), '无法打开文件，请重试')
})
