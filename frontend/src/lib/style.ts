/* 三个到处复用的样式片段。单独成文件是为了让 components/ui.tsx 只导出组件——
   混着导出常量会让 Vite 的 React Fast Refresh 对整个文件失效，改一个按钮就整页重挂载。 */

import type { CSSProperties } from 'react'

/** 全站的 mono 小标签：顶栏 EN 标题、编号、时间戳。 */
export const mono = (fs = '10.5px', ls = '.14em'): CSSProperties => ({
  font: `500 ${fs}/1 'JetBrains Mono',monospace`,
  letterSpacing: ls,
  color: 'var(--fg3)',
})

/** 等宽数字。分数要能上下对齐，否则一列分数看着像在抖。 */
export const num: CSSProperties = { fontVariantNumeric: 'tabular-nums' }

/** 下划线输入框。表单里的默认形态，只有需要成块的地方才改成描边框。 */
export const fieldStyle: CSSProperties = {
  width: '100%',
  background: 'none',
  border: 0,
  borderBottom: '1px solid var(--line)',
  padding: '9px 0',
  color: 'var(--fg)',
  fontFamily: 'inherit',
  fontSize: 14,
  fontWeight: 500,
  letterSpacing: '-.01em',
}
