import assert from 'node:assert/strict'
import test from 'node:test'
import { mailChannelUpdate } from './mailChannel.ts'

test('blank secrets are omitted when changing sender settings', () => {
  const update = mailChannelUpdate('resend', { resendApiKey: '', sesFromName: ' Example ' }, [], {})
  assert.deepEqual(update, { provider: 'resend', sesFromName: 'Example' })
})
test('clearing a saved secret is explicit and overrides an unsubmitted replacement', () => {
  assert.deepEqual(mailChannelUpdate('resend', { resendApiKey: 'replacement' }, ['resendApiKey'], {}), { provider: 'resend', resendApiKey: '' })
})
test('provider changes cannot submit hidden credentials or connection settings', () => {
  const update = mailChannelUpdate('aliyun_dm', { sesSecretKey: 'not-for-aliyun', smtpHost: 'smtp.example.org', resendApiKey: 'not-for-aliyun', aliyunAccessKeyId: 'example-id', aliyunRegion: 'cn-hangzhou' }, ['sesSecretKey'], {})
  assert.deepEqual(update, { provider: 'aliyun_dm', aliyunRegion: 'cn-hangzhou', aliyunAccessKeyId: 'example-id' })
})
test('SMTP retains exact password bytes and validates encrypted transport and port', () => {
  assert.deepEqual(mailChannelUpdate('smtp', { smtpPassword: ' example password ' }, [], {}), { provider: 'smtp', smtpPort: 587, smtpSecurity: 'starttls', smtpPassword: ' example password ' })
  for (const smtpPort of ['', '0', '-1', '65536', '2.5', 'bad']) assert.throws(() => mailChannelUpdate('smtp', { smtpPort }, [], {}), /端口/)
  assert.throws(() => mailChannelUpdate('smtp', { smtpSecurity: 'none' }, [], {}), /加密连接/)
  assert.throws(() => mailChannelUpdate('__proto__', {}, [], {}), /发信服务/)
})
