import { useState } from 'react'
import { BrandMark, Btn, Pill } from '@/components/ui'
import { useGovernanceAction } from '@/api/governance'
import { userErrorMessage } from '@/api/errorMessages'
import { useApp } from '@/stores/app'
import './mode-choice.css'

export type ClassMode = 'centralized' | 'collective'

/* 两张图的 viewBox 与卡片内图区同为约 460×250，桌面端按 1:1 绘制，文字不再被整体缩小。 */
function Person({ x, y, size = 1 }: { x: number; y: number; size?: number }) {
  return (
    <g className="mode-person" transform={`translate(${x} ${y}) scale(${size})`}>
      <circle cx="0" cy="-4.5" r="4.5" />
      <path d="M-8 9c0-5 3.6-8 8-8s8 3 8 8" />
    </g>
  )
}

function AssignmentDiagram() {
  const reviewers = [100, 230, 360]
  const materials = [150, 177, 204, 231, 258, 285, 312]
  return (
    <svg
      viewBox="0 0 460 250"
      role="img"
      aria-label="班管统筹综测小组；全班材料由系统随机派给两位小组成员独立审核，学生不固定隶属某个小组"
    >
      <defs>
        <marker id="mode-arrow-red" viewBox="0 0 8 8" refX="7" refY="4" markerWidth="7" markerHeight="7" orient="auto">
          <path d="M0 0 8 4 0 8z" className="mode-arrowhead" />
        </marker>
      </defs>
      <path className="mode-links" d="M230 52v20M100 92V72h260v20" />
      <path className="mode-random-links" d="M150 214 222 186M177 214l49-28M204 214l26-28M258 214l-26-28M285 214l-49-28M312 214l-74-28" />
      <path className="mode-pick" d="M231 214v-28" />
      <path className="mode-pick" d="M206 156C176 150 110 150 100 134" markerEnd="url(#mode-arrow-red)" />
      <path className="mode-pick" d="M254 156C284 150 350 150 360 134" markerEnd="url(#mode-arrow-red)" />
      <path className="mode-random-links" d="M230 156v-22" />

      <rect className="mode-lead" x="160" y="16" width="140" height="36" rx="18" />
      <text className="mode-lead-text" x="230" y="39">班级管理员</text>

      {reviewers.map((x, i) => (
        <g key={x}>
          <rect className="mode-node" x={x - 56} y="92" width="112" height="40" rx="20" />
          <Person x={x - 30} y={112} />
          <text className="mode-label" x={x + 10} y="117">小组 {'ABC'[i]}</text>
        </g>
      ))}

      <rect className="mode-assignment" x="146" y="156" width="168" height="30" rx="15" />
      <text className="mode-assignment-text" x="230" y="176">随机派单 · 每条两人</text>

      {materials.map((x) => (
        <path key={x} className={x === 231 ? 'mode-doc mode-doc-active' : 'mode-doc'} d={`M${x - 8} 214h11l5 5v17h-16z`} />
      ))}
      <text className="mode-caption" x="130" y="230" style={{ textAnchor: 'end' }}>全班材料</text>
      <text className="mode-caption" x="332" y="230" style={{ textAnchor: 'start' }}>不固定归属小组</text>
    </svg>
  )
}

function CircleDiagram() {
  const cx = 230, cy = 125, rx = 150, ry = 90
  const angles = [-90, -30, 30, 90, 150, 210]
  const at = (deg: number) => [cx + rx * Math.cos((deg * Math.PI) / 180), cy + ry * Math.sin((deg * Math.PI) / 180)]
  const arcs = angles.map((deg) => {
    const [x1, y1] = at(deg + 17)
    const [x2, y2] = at(deg + 43)
    return `M${x1.toFixed(1)} ${y1.toFixed(1)}A${rx} ${ry} 0 0 1 ${x2.toFixed(1)} ${y2.toFixed(1)}`
  })
  return (
    <svg
      viewBox="0 0 460 250"
      role="img"
      aria-label="班级成员在循环中平等相连，共同提案、表决，个人案件随机评审"
    >
      <defs>
        <marker id="mode-arrow-ring" viewBox="0 0 8 8" refX="7" refY="4" markerWidth="7" markerHeight="7" orient="auto">
          <path d="M0 0 8 4 0 8z" className="mode-arrowhead" />
        </marker>
      </defs>
      <ellipse className="mode-ring" cx={cx} cy={cy} rx={rx} ry={ry} />
      {arcs.map((d) => (
        <path key={d} className="mode-flow" d={d} markerEnd="url(#mode-arrow-ring)" />
      ))}
      {angles.map((deg) => {
        const [x, y] = at(deg)
        return (
          <g key={deg}>
            <circle className="mode-peer" cx={x} cy={y} r="22" />
            <Person x={x} y={y + 1} size={1.15} />
          </g>
        )
      })}
      <text className="mode-center" x={cx} y={cy - 4}>共同决定</text>
      <text className="mode-center-sub" x={cx} y={cy + 20}>提案 · 表决 · 随机评审</text>
    </svg>
  )
}

/** 同一份首次配置界面用于真实班级与独立演示初始化。 */
export function ModeChoice({
  onChoose,
  demo = false,
  demoCurrentMode,
  onCancel,
  pending = false,
  error = '',
}: {
  onChoose: (mode: ClassMode) => void
  demo?: boolean
  demoCurrentMode?: ClassMode
  onCancel?: () => void
  pending?: boolean
  error?: string
}) {
  return (
    <main className="mode-choice">
      <header className="mode-choice-brand">
        <BrandMark />
        <span>{demo ? '演示班级 · 模式选择' : '班级 · 首次配置'}</span>
        {onCancel && <button type="button" className="mode-choice-back" disabled={pending} onClick={onCancel}>返回当前演示</button>}
      </header>
      <div className="mode-choice-intro">
        <span>START TOGETHER</span>
        <h1>这个班级，如何一起完成综测？</h1>
        <p>
          {demo ? '选择要体验的工作方式。演示站顶部随时可重新选择模式。' : '选择本周期的工作方式。确认后进入对应工作台，日常使用不再切换模式。'}
        </p>
      </div>
      <div className="mode-choice-grid">
        <article className="mode-choice-card">
          <div className="mode-choice-art">
            <AssignmentDiagram />
          </div>
          <span className="mode-choice-index">01 / 分工协作</span>
          <div className="mode-choice-title">
            <h2>普通模式</h2>
            <Pill tone="ok">推荐</Pill>
          </div>
          <p>
            班管统筹，全班材料随机派给综测小组成员独立审核；学生不固定隶属某个小组。
          </p>
          <ul>
            <li>班管设置规则、统筹审核与处理仲裁</li>
            <li>每条材料双人审核，均衡工作量并回避本人</li>
            <li>流程稳定，推荐首次使用和正式计分</li>
          </ul>
          <Btn
            primary
            disabled={pending}
            onClick={() => onChoose('centralized')}
          >
            {demoCurrentMode === 'centralized' ? '继续普通模式' : '选择普通模式'}
          </Btn>
        </article>
        <article className="mode-choice-card">
          <div className="mode-choice-art">
            <CircleDiagram />
          </div>
          <span className="mode-choice-index">02 / 平等共治</span>
          <div className="mode-choice-title">
            <h2>共治模式</h2>
            <Pill tone="warn">测试版</Pill>
          </div>
          <p>
            成员使用同一个工作台。共同事项提案表决，个人案件随机评审，没有身份切换。
          </p>
          <ul>
            <li>主动加入，与是否提交材料无关</li>
            <li>先招募、再启动；至少需要 7 名评审成员</li>
          </ul>
          <p className="mode-choice-beta">
            测试版：尚未经过真实班级的生产验证，规则与界面可能调整。正式计分建议选择普通模式。
          </p>
          <Btn
            disabled={pending}
            onClick={() => onChoose('collective')}
          >
            {demoCurrentMode === 'collective' ? '继续共治模式' : '选择共治模式'}
          </Btn>
        </article>
      </div>
      {error && (
        <p className="mode-choice-error" role="alert">
          {error}
        </p>
      )}
      <p className="mode-choice-foot">
        {demo
          ? '共治演示包含预置的招募结果与虚构提案。切换到另一种模式会重新载入该模式的示例数据，当前演示操作会重置；返回或继续当前模式可保留操作。真实班级仍只在首次配置时选择模式。'
          : '本次选择由首次配置的班管完成，推荐普通模式。共治模式为测试版，尚未经过生产验证；正式启动前请准备好名单、评分方案和班级资料，启动后本周期按章程共同决策，不能再切回普通模式。'}
      </p>
      {demo && <p><a href="/source.tar.gz" download>AGPL-3.0 · 演示站源码</a></p>}
    </main>
  )
}

export default function InitialModeChoice() {
  const action = useGovernanceAction(),
    go = useApp((s) => s.go)
  const [error, setError] = useState('')
  async function choose(mode: ClassMode) {
    setError('')
    try {
      await action.mutateAsync({
        path: '/config',
        method: 'put',
        body: { mode },
      })
      go(mode === 'collective' ? 'govHome' : 'admBoard')
    } catch (e) {
      setError(userErrorMessage(e))
    }
  }
  return (
    <ModeChoice
      onChoose={(mode) => void choose(mode)}
      pending={action.isPending}
      error={error}
    />
  )
}
