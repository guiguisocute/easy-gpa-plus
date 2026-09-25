/* 「这一大项怎么算」。用在专业素质这种不由申报累加、而是外部导入的大项上。

   学生对专业素质唯一能做的事就是核对：分数是教务给的，系统只负责乘权重。
   所以这一页要能回答三个问题——公式长什么样、哪些课算进去、等次怎么折成百分制。
   三样都来自方案数据（category.formula / category.note），不写死在前端：
   换一个学院、换一版细则，改方案就行。 */

import { RichText } from '@/components/Markdown'
import { Formula } from '@/components/Formula'
import { plainNoteToMarkdown } from '@/lib/itemGuide'
import type { SchemeCategory } from '@/lib/types'

export function CategoryBrief({ category, weight }: { category: SchemeCategory; weight: number | undefined }) {
  const note = category.note?.trim() ? plainNoteToMarkdown(category.note.trim()) : ''

  return (
    <div style={{ border: '1px solid var(--line)', display: 'flex', flexDirection: 'column' }}>
      <div style={{ background: 'var(--sub)', borderBottom: '1px solid var(--line)', padding: '18px 22px', display: 'flex', alignItems: 'flex-start', gap: 18, flexWrap: 'wrap' }}>
        <div style={{ minWidth: 0, flex: 1 }}>
          <div style={{ fontSize: 19, fontWeight: 600, letterSpacing: '-.03em' }}>{category.name}</div>
          <div style={{ marginTop: 6, fontSize: 12.5, color: 'var(--fg3)' }}>成绩由教务系统导入，不开放申报</div>
        </div>
        <div style={{ display: 'flex', flexDirection: 'column', alignItems: 'flex-end', gap: 5, flex: 'none' }}>
          <span style={{ fontSize: 12.5, color: 'var(--fg3)' }}>占总评</span>
          <span style={{ fontSize: 17, fontWeight: 600 }}>{weight ?? 0}</span>
        </div>
      </div>

      {category.formula?.lhs?.trim() && (
        <div style={{ borderBottom: '1px solid var(--line)', padding: '22px 22px 18px' }}>
          <div style={{ fontSize: 12, fontWeight: 600, letterSpacing: '.01em', color: 'var(--fg2)', paddingBottom: 14 }}>计分公式</div>
          <Formula formula={category.formula} />
        </div>
      )}

      {note && (
        <div style={{ padding: '20px 22px' }}>
          <div style={{ fontSize: 12, fontWeight: 600, letterSpacing: '.01em', color: 'var(--fg2)', paddingBottom: 10 }}>认定说明</div>
          <RichText value={note} />
        </div>
      )}

      <div style={{ borderTop: '1px solid var(--line2)', padding: '14px 22px', display: 'flex', alignItems: 'baseline', gap: 14 }}>
        <span style={{ fontSize: 12.5, color: 'var(--fg3)' }}>分数来源</span>
        <span style={{ marginLeft: 'auto', textAlign: 'right', fontSize: 13, color: 'var(--fg2)' }}>
          由班级管理员从教务系统导入。异议请通过申诉提出。
        </span>
      </div>
    </div>
  )
}
