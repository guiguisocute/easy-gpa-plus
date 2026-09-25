/* 「本学年有哪些活动能报到这一项下」。

   活动汇总表整张有上百行，摊在任何一个页面上都读不完，塞进小项说明又会把规则埋掉。
   所以按小项拆开：填志愿服务时只看志愿服务那二十条，填观众分时只看观众那四十几条。

   两个尺寸上的约束，都是量出来的，不是猜的：
   1. 「受委派…观众」有 45 条，全部展开是 2848px，而视口只有 1249px——不封顶的话
      学生要往下滚两屏半才看得见表单。所以列表自己带最大高度和内滚动。
   2. 45 条平铺没有抓手，于是按承办组织分组：团委组织部的归团委组织部，
      青协的归青协，学生找「我参加的那个部门办的活动」时有个落点。

   加分口径一列原样照抄加分条：那一列每行的措辞本来就不统一（「1h/0.25 分」
   「组织者 2 分」「参考具体加分条」），改写成统一句式就是替学院发明规则。 */

import { useMemo, useState } from 'react'
import { mono } from '@/lib/style'
import type { SchemeActivity } from '@/lib/types'
import '@/styles/mobile-pages.css'

/** 超过这个条数就分组显示——少于它时分组只会平添几行标题。 */
const GROUP_THRESHOLD = 8
/** 展开后的最大高度。再长就自己内部滚，不推走下面的表单。 */
const MAX_HEIGHT = 420

function LevelTag({ level }: { level?: string }) {
  if (!level) return null
  return (
    <span style={{
      flex: 'none', fontSize: 11, lineHeight: 1.6, padding: '1px 6px',
      border: '1px solid var(--line)', color: 'var(--fg3)', whiteSpace: 'nowrap',
    }}>
      {level}
    </span>
  )
}

function Row({ activity, first }: { activity: SchemeActivity; first: boolean }) {
  return (
    <div className="activity-table-row" style={{
      display: 'grid', gridTemplateColumns: 'minmax(0,1fr) auto',
      gap: '2px 12px', alignItems: 'baseline', padding: '9px 14px',
      borderTop: first ? 0 : '1px solid var(--line2)',
    }}>
      <span style={{ fontSize: 13, color: 'var(--fg)', minWidth: 0, textWrap: 'pretty' }}>{activity.name}</span>
      <span style={{ fontSize: 12.5, color: 'var(--fg2)', textAlign: 'right', whiteSpace: 'nowrap' }}>
        {activity.score || '—'}
      </span>
      <span style={{ display: 'flex', alignItems: 'center', gap: 7, flexWrap: 'wrap', minWidth: 0 }}>
        <LevelTag level={activity.level} />
        {activity.date && <span style={{ fontSize: 11.5, color: 'var(--fg3)', ...mono('11.5px', '.02em') }}>{activity.date}</span>}
      </span>
      <span />
    </div>
  )
}

export function ActivityTable({
  activities,
  defaultOpen = false,
}: {
  activities: SchemeActivity[]
  defaultOpen?: boolean
}) {
  const [open, setOpen] = useState(defaultOpen)

  /* 按承办组织分组，保持原有顺序：加分条本来就是按部门归档的，
     打乱成字母序反而不像学生手上那份表。 */
  const groups = useMemo(() => {
    const map = new Map<string, SchemeActivity[]>()
    for (const activity of activities) {
      const key = activity.org?.trim() || '其他'
      const list = map.get(key)
      if (list) list.push(activity)
      else map.set(key, [activity])
    }
    return [...map.entries()]
  }, [activities])

  if (!activities?.length) return null
  const grouped = activities.length >= GROUP_THRESHOLD && groups.length > 1

  return (
    <div style={{ border: '1px solid var(--line)', background: 'var(--bg)' }}>
      <button
        type="button"
        className="activity-table-toggle"
        aria-expanded={open}
        onClick={() => setOpen((current) => !current)}
        style={{
          display: 'flex', alignItems: 'center', gap: 9, width: '100%',
          border: 0, background: 'none', padding: '11px 14px', font: 'inherit',
          textAlign: 'left', cursor: 'pointer', color: 'var(--fg2)',
        }}
      >
        <span style={{ width: 12, textAlign: 'center', fontSize: 11, color: 'var(--fg3)' }}>{open ? '▾' : '▸'}</span>
        <span style={{ fontSize: 12.5, fontWeight: 600 }}>本学年活动</span>
        <span style={{ fontSize: 12, color: 'var(--fg3)', ...mono('11.5px', '.02em') }}>{activities.length}</span>
        <span style={{ marginLeft: 'auto', fontSize: 11.5, color: 'var(--fg3)' }}>
          {open ? '收起' : grouped ? `按学院加分条列出 · ${groups.length} 个承办组织` : '按学院加分条列出'}
        </span>
      </button>

      {open && (
        <div
          data-r="scroll"
          style={{ borderTop: '1px solid var(--line2)', maxHeight: MAX_HEIGHT, overflowY: 'auto', overflowX: 'auto' }}
        >
          <div className="activity-table-list" style={{ minWidth: 420 }}>
            {grouped
              ? groups.map(([org, rows], gi) => (
                  <div key={org}>
                    {/* 组标题吸顶：滚到第三十行时还知道自己在看哪个部门。 */}
                    <div style={{
                      position: 'sticky', top: 0, zIndex: 1,
                      display: 'flex', alignItems: 'baseline', gap: 8,
                      background: 'var(--sub)', borderTop: gi === 0 ? 0 : '1px solid var(--line)',
                      borderBottom: '1px solid var(--line2)', padding: '7px 14px',
                    }}>
                      <span style={{ fontSize: 11.5, fontWeight: 600, color: 'var(--fg2)' }}>{org}</span>
                      <span style={{ marginLeft: 'auto', fontSize: 11, color: 'var(--fg3)', ...mono('11px', '.02em') }}>{rows.length}</span>
                    </div>
                    {rows.map((activity, index) => (
                      <Row key={`${activity.name}-${activity.date ?? ''}-${index}`} activity={activity} first={index === 0} />
                    ))}
                  </div>
                ))
              : activities.map((activity, index) => (
                  <Row key={`${activity.name}-${activity.date ?? ''}-${index}`} activity={activity} first={index === 0} />
                ))}
          </div>
        </div>
      )}
    </div>
  )
}
