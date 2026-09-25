/* 方案里的计分公式。只画一件事：一条分数线。

   之前这里挂的是 KaTeX。换掉的理由不是体积，是它买回来的东西用不上——综测里的公式
   全都是「加权和 ÷ 总权重」这一个形状，为它引入一整套表达式语法，就得连带承受
   解析失败、trust 开关、CJK 回退字体、和一份自带的 CSS 重置。

   现在公式是三段纯文本（lhs / numerator / denominator），拿 flex 列加一条 border 画出来：
   跟着主题走，跟着正文字体走，没有需要转义的标记，也没有"渲染失败"这个状态。
   分子分母留空时退化成一行普通等式。 */

import type { SchemeFormula } from '@/lib/types'

export function Formula({ formula }: { formula: SchemeFormula }) {
  const lhs = formula.lhs?.trim() ?? ''
  const numerator = formula.numerator?.trim() ?? ''
  const denominator = formula.denominator?.trim() ?? ''
  if (!lhs) return null

  const fraction = numerator !== '' && denominator !== ''

  return (
    /* 公式比正文宽是常态，窄屏让它自己横向滚，不要把整页撑出横向滚动条。 */
    <div data-r="scroll" style={{ overflowX: 'auto', overflowY: 'hidden', padding: '4px 0' }}>
      <div style={{
        display: 'flex', alignItems: 'center', justifyContent: 'center',
        gap: 12, minWidth: 'min-content', fontSize: 14.5, lineHeight: 1.6, color: 'var(--fg)',
      }}>
        <span style={{ flex: 'none', fontWeight: 600 }}>{lhs}</span>
        {fraction && (
          <>
            <span style={{ flex: 'none', color: 'var(--fg2)' }}>=</span>
            <div style={{ display: 'flex', flexDirection: 'column', alignItems: 'center', flex: 'none', textAlign: 'center' }}>
              <span style={{ padding: '0 12px 7px', whiteSpace: 'nowrap' }}>{numerator}</span>
              <span style={{ borderTop: '1px solid var(--fg2)', padding: '7px 12px 0', whiteSpace: 'nowrap' }}>{denominator}</span>
            </div>
          </>
        )}
      </div>
    </div>
  )
}
