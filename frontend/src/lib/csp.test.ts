import assert from 'node:assert/strict'
import { createHash } from 'node:crypto'
import { readFileSync } from 'node:fs'
import test from 'node:test'

/* index.html 里那段内联脚本在 React 挂载前设置 data-theme。nginx 的 CSP 用
   script-src 'self' 且不带 'unsafe-inline'，所以它只能靠 sha256 白名单放行。
   两边一旦不同步，浏览器会静默拦掉脚本——页面照常渲染，只是深色偏好每次刷新
   都退回浅色，冒烟测试跑在 Node 里永远发现不了。这个测试就是那道同步闸。 */

const html = readFileSync(new URL('../../index.html', import.meta.url), 'utf8')
const nginxConf = readFileSync(new URL('../../../deploy/nginx/default.conf', import.meta.url), 'utf8')

function documentCSP(): string {
  // 只取 `location /` 这一段：文档的 CSP 才决定内联脚本能不能跑，
  // 静态资源响应上的 CSP 管不到加载它的页面。
  const block = nginxConf.match(/location \/ \{[\s\S]*?\n {4}\}/)
  assert.ok(block, 'deploy/nginx/default.conf 里找不到 location / 块')
  const header = block[0].match(/add_header Content-Security-Policy "([^"]+)"/)
  assert.ok(header, 'location / 块里找不到 Content-Security-Policy')
  return header[1]
}

test('内联主题脚本的 sha256 与 nginx CSP 一致', () => {
  const inline = html.match(/<script>([\s\S]*?)<\/script>/)
  assert.ok(inline, 'index.html 里找不到内联脚本')
  const digest = createHash('sha256').update(inline[1], 'utf8').digest('base64')
  assert.match(
    documentCSP(),
    new RegExp(`script-src [^;]*'sha256-${digest.replace(/[+/=]/g, '\\$&')}'`),
    `内联脚本变了，请把 deploy/nginx/default.conf 的 script-src 哈希改成 'sha256-${digest}'`,
  )
})

test('文档 CSP 仍然禁止任意内联脚本', () => {
  // 加 'unsafe-inline' 能让上面的测试永远通过，但那等于把 CSP 关掉。
  assert.doesNotMatch(documentCSP(), /script-src [^;]*'unsafe-inline'/)
})
