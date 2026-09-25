import { readableErrorMessage } from '@/api/errorMessages'
/* 班级管理员 · 方案编辑器。

   GUI 与高级 JSON 只编辑评分结构：GUI 每次改动立即同步 JSON，学生端预览读取
   评分结构与本班当前运行参数合成的草稿。时间与开关只由时间线页面管理。

   学生端预览对齐原型 admScheme 的第三态：预览的是"正在编辑的这份草稿"，
   而不是已发布版本——改完 JSON 立刻能看到学生会看到什么、算出多少，这才是它存在的理由。
   已发布的版本不可改，改动一律产生新版本——提交条目里冻结的是发布时刻的快照。

   校验结果直接用后端 /admin/scheme/:id/publish 返回的 422 detail：
   前端再实现一遍 scheme.Validate 的 20 条规则，只会得到两份迟早对不上的校验。 */

import { useEffect, useState } from 'react'
import { Btn, Empty, Note, PageHead, Pill, Row, Seg, Sub } from '@/components/ui'
import { fieldStyle, mono } from '@/lib/style'
import { useApp } from '@/stores/app'
import { ApiError } from '@/api/client'
import { useAdminScheme, useAdminSchemes, useAdminTemplateShareRequests, useAvailableTemplates, useScheme, useSchemeActions } from '@/api/queries'
import { COMMON_EVIDENCE_TYPES, DEFAULT_EVIDENCE_MAX_MB } from '@/lib/evidence'
import type { SchemeConfig } from '@/lib/types'
import SchemeGuiEditor from './SchemeGuiEditor'
import SchemePreview, { type PreviewTrial } from './SchemePreview'

/* 方案只描述评分规则。窗口、业务开关和评优比例是班级的运行设置，住在
   「时间窗口」页里，新建草稿时不该也无法带上它们。 */
function defaultSchemeConfig(): SchemeConfig {
  return {
    schemeName: '综合测评方案',
    version: 'draft',
    weights: { major: 0.6, moral: 0.15, practice: 0.15, health: 0.1 },
    categories: [
      { key: 'major', name: '专业素质', maxTotal: 100, baseItems: [], penaltyItems: [], items: [] },
      {
        key: 'moral', name: '思想道德素质', maxTotal: 100,
        baseItems: [
          { key: 'moral_base_politics', name: '坚持四项基本原则，践行社会主义核心价值体系', full: 15 },
          { key: 'moral_base_law', name: '遵纪守法，遵守社会公德，爱国爱民爱劳动爱科学爱社会主义', full: 15 },
          { key: 'moral_base_campus', name: '遵守《高等学校学生行为准则》与校纪校规，爱校荣校', full: 15 },
          { key: 'moral_base_collective', name: '热爱集体、团结同学、尊敬师长、助人为乐，参加集体与公益活动', full: 15 },
          { key: 'moral_base_edu', name: '参加形势政策、安全法规、校纪校规、心理健康、廉洁等教育活动', full: 10 },
        ],
        penaltyItems: [
          { key: 'moral_pen_zero', name: '违反四项基本原则等严重行为（思想道德素质直接归零）', per: -100 },
          { key: 'moral_pen_probation', name: '留校察看处分', per: -30 },
          { key: 'moral_pen_demerit', name: '记过处分', per: -25 },
          { key: 'moral_pen_serious_warning', name: '严重警告处分', per: -20 },
          { key: 'moral_pen_warning', name: '警告处分', per: -15 },
          { key: 'moral_pen_notice_school', name: '校级通报批评', per: -10 },
          { key: 'moral_pen_notice_college', name: '院级通报批评', per: -5 },
          { key: 'moral_pen_credit', name: '恶意拖欠学费等不良诚信记录', per: -15 },
          { key: 'moral_pen_absence', name: '考勤缺勤（每次；迟到早退累计 2 次记 1 次）', per: -1 },
          { key: 'moral_pen_freshman_exam', name: '新生校纪校规／安全法治／团员教育考试不及格（每项次）', per: -5 },
          { key: 'moral_pen_wire', name: '违章用电私拉电线', per: -5 },
          { key: 'moral_pen_stove', name: '使用煤油炉（酒精炉）', per: -3 },
          { key: 'moral_pen_candle', name: '点蜡烛', per: -2 },
          { key: 'moral_pen_facility', name: '故意污损公共设施、攀折花木', per: -4 },
          { key: 'moral_pen_disorder', name: '酗酒、起哄、摔瓶子等扰乱校园秩序', per: -5 },
          { key: 'moral_pen_appliance', name: '违规使用大功率用电器（未受处分）', per: -3 },
        ],
        items: [],
      },
      {
        key: 'practice', name: '实践创新素质', maxTotal: 100,
        baseItems: [{
          key: 'practice_base', name: '参加课外学术科技活动、专业学术竞赛、专业社会实践', full: 40,
          studentClaim: { minimum: 2, unit: '项', evidence: { required: true, types: [...COMMON_EVIDENCE_TYPES], maxMb: DEFAULT_EVIDENCE_MAX_MB } },
          note: [
            '- 完成 1 项计 20 分，完成 2 项及以上计 40 分；一学年内参加具有竞技性质的专业比赛并获院级三等奖及以上，也可按 40 分认定。',
            '- 可计项目包括教育部认可的全国大学生学科竞赛、学校竞赛管理办法所列赛事，以及学院正式通知的专业赛事、课外学术科技活动和专业社会实践。',
            '- 常见示例有网页设计、校级及以上 ACM 程序设计、计算机作品、软件服务外包、电子制作和学院“新风杯”等；只报名未实际参加不计分，专业竞赛须有有效参赛材料。',
          ].join('\n'),
        }],
        penaltyItems: [], items: [],
      },
      {
        key: 'health', name: '身体心理素质', maxTotal: 100,
        baseItems: [
          { key: 'health_base_psych', name: '具备良好的心理品质', full: 15 },
          { key: 'health_base_aesthetic', name: '具有正确的审美观和高雅的审美情趣', full: 15 },
          { key: 'health_base_fitness', name: '积极参加体育锻炼，身体素质良好，卫生习惯良好', full: 10 },
          { key: 'health_base_second_class', name: '按第二课堂学分要求参加各项课外活动', full: 30 },
        ],
        penaltyItems: [{ key: 'health_pen_fitness_fail', name: '体育测试不达标或体育课成绩不合格（需补考）', per: -30 }],
        items: [],
      },
    ],
  }
}

type ScoringConfig = Pick<SchemeConfig, 'schemeName' | 'weights' | 'categories'>

function scoringConfig(config: SchemeConfig): ScoringConfig {
  return { schemeName: config.schemeName, weights: config.weights, categories: config.categories }
}

function isScoringConfig(value: unknown): value is ScoringConfig {
  const config = value as Partial<ScoringConfig> | null
  return !!config && typeof config.schemeName === 'string' && Array.isArray(config.categories) && !!config.weights
}

function withClassRuntime(scoring: ScoringConfig, runtime: SchemeConfig): SchemeConfig {
  return { ...runtime, ...scoring }
}

export default function AdminScheme() {
  const say = useApp((s) => s.say)
  const agentHandoff = useApp((s) => s.agentHandoff)
  const setAgentHandoff = useApp((s) => s.setAgentHandoff)
  const list = useAdminSchemes()
  const published = useScheme()
  const actions = useSchemeActions()
  const templates = useAvailableTemplates()
	const shareRequests = useAdminTemplateShareRequests()

  const [selId, setSelId] = useState<string | null>(null)
  const [tab, setTab] = useState<'tree' | 'json' | 'preview'>('tree')
  const [draftName, setDraftName] = useState('')
  const [json, setJson] = useState('')
  const [error, setError] = useState<string | null>(null)
  const [templateId, setTemplateId] = useState('')

  /* 当前聚焦的小项由页面持有，GUI 编辑与学生端预览共用一份。
     切页签是换个角度看同一个小项，不是重新开始——两边各存一份选择，一切过去就弹回第一项。
     试填的数量/档次同理留在页面上，改完规则切回预览还能看见同一条的新分数。 */
  const [focusKey, setFocusKey] = useState<string | null>(null)
  const [trial, setTrial] = useState<PreviewTrial | null>(null)

  const draft = useAdminScheme(selId)

  /* 切换草稿时把编辑器内容换成服务端的那一份。
     不做"保留本地未保存改动"——跨草稿保留会让人不知道自己在编辑哪一版。 */
  useEffect(() => {
    if (!draft.data) return
    setJson(JSON.stringify(scoringConfig(draft.data.config), null, 2))
    setDraftName(draft.data.name)
    setError(null)
  }, [draft.data])

  const items = list.data?.items ?? []
  const drafts = items.filter((s) => s.status === 'draft')
  const versions = items.filter((s) => s.status === 'published')
  const current = published.data
  const availableTemplates = templates.data?.items ?? []
	const currentShare = versions[0] ? (shareRequests.data?.items ?? []).find((request) => request.schemeId === versions[0].id) : undefined

  useEffect(() => {
    if (agentHandoff?.kind !== 'scheme_draft') return
    if (!drafts.some((item) => item.id === agentHandoff.resourceId)) return
    setSelId(agentHandoff.resourceId)
    setTab('tree')
    setAgentHandoff(null)
    say('Agent 草稿打开了。已经发布的那一版没动')
  }, [agentHandoff, drafts, say, setAgentHandoff])
  const fail = (e: unknown) => {
    if (e instanceof ApiError) {
      setError(typeof e.detail === 'string' ? `${e.message}：${readableErrorMessage(e.detail, '请检查方案格式、权重和分值范围')}` : e.message)
      say(e.message)
      return
    }
    say('操作失败')
  }

  let parsed: unknown = null
  let parseError: string | null = null
  try {
    parsed = json.trim() ? JSON.parse(json) : null
  } catch {
    parseError = '方案格式不正确，请检查括号、逗号和引号是否完整'
  }

  const weights: Record<string, number> = (parsed as { weights?: Record<string, number> } | null)?.weights ?? current?.weights ?? {}
  const weightSum = Object.values(weights).reduce<number>((a, b) => a + Number(b), 0)

  /* 预览优先用正在编辑的草稿：改完 JSON 立刻能看到效果才是预览的意义。
     JSON 正在写一半解析不了时退回已发布版本，而不是整块空掉——空掉会让人以为配置炸了。
     categories 是渲染的前提，缺了就当没有可预览的方案。 */
  const draftConfig = draft.data && isScoringConfig(parsed) ? withClassRuntime(parsed, draft.data.config) : null
  const previewConfig: SchemeConfig | null =
    (selId ? draftConfig : null) ?? current ?? null
  const dirty = !!selId && !!draft.data && (draftName !== draft.data.name || json !== JSON.stringify(scoringConfig(draft.data.config), null, 2))

  async function saveDraft(showNotice = true) {
    if (!selId || !draft.data || parseError || !draftConfig || !draftName.trim()) return false
    try {
      await actions.update.mutateAsync({ id: selId, name: draftName.trim(), config: draftConfig, lockVersion: draft.data.lockVersion })
      setError(null)
      if (showNotice) say('草稿已保存')
      return true
    } catch (e) {
      fail(e)
      return false
    }
  }

  async function publishDraft() {
    if (!selId) return
    if (!(await saveDraft(false))) return
    try {
      const result = await actions.publish.mutateAsync(selId)
      setError(null)
      setSelId(null)
      setTab('tree')
      say(`v${result.version} 已发布 · 只对之后的新提交生效`)
    } catch (e) {
      fail(e)
    }
  }

  return (
    <div style={{ animation: 'rise .28s ease both' }}>
      <PageHead
        en="SCHEME EDITOR"
        title="方案编辑器"
        desc={
          current
            ? `当前生效版本为 ${current.version}。如需修改，请另存草稿后发布。`
            : '还没有在用的方案。新建一份草稿发布出去，学生就能开始提交了。'
        }
        side={
          <>
            {current && <Pill tone="ok">当前生效 {current.version}</Pill>}
			{current && versions[0] && <Btn disabled={actions.share.isPending || currentShare?.status === 'pending' || currentShare?.status === 'approved'} onClick={() => { if (!window.confirm('把当前评分规则提交到平台模板库审核？时间、开关和评优比例不会共享出去。')) return; actions.share.mutate(versions[0].id, { onSuccess: () => say('共享申请已提交 · 等平台运维审核'), onError: fail }) }}>{actions.share.isPending ? '提交中…' : currentShare?.status === 'pending' ? '共享审核中' : currentShare?.status === 'approved' ? '已进入平台模板' : currentShare?.status === 'rejected' ? '修正后重新申请' : '申请共享评分模板'}</Btn>}
            {/* 这里只管"照着已发布那版再改一份"。空白新建挪到下面的新建区——
                原来两件事共用一个按钮，发布过之后它就固定变成「另存」，
                于是一个已经上线的班级再也没有从零开一份方案的入口。 */}
            {current && (
              <Btn
                disabled={actions.create.isPending}
                onClick={() =>
                  actions.create.mutate(
                    { name: `${current.schemeName}（副本）`, config: current },
                    {
                      onSuccess: (r) => {
                        setSelId(r.id)
                        setTab('tree')
                        say('已从当前发布版本另存为草稿')
                      },
                      onError: fail,
                    },
                  )
                }
              >
                从当前版本另存草稿
              </Btn>
            )}
          </>
        }
      />
	  {currentShare?.status === 'rejected' && <div style={{ marginBottom: 18 }}><Note tone="warn">上次共享申请未通过：{currentShare.reviewReason || '运维未填写原因'}。改完发个新版本就能重新申请，也可以直接再提交一遍。</Note></div>}

      <div style={{ border: '1px solid var(--line)', background: 'var(--sub)', marginBottom: 24 }}>
      <div style={{ padding: '14px 16px', display: 'flex', alignItems: 'center', gap: 10, flexWrap: 'wrap' }}>
        <div style={{ display: 'flex', flexDirection: 'column', gap: 4, minWidth: 190, marginRight: 'auto' }}>
          <span style={{ fontSize: 12.5, fontWeight: 600 }}>从平台模板新建草稿</span>
          <span style={{ fontSize: 11.5, color: 'var(--fg3)' }}>只复制评分规则。时间、开关和评优比例还是用本班时间线里的设置。</span>
        </div>
        <select className="hv-bfg" value={templateId} onChange={(event) => setTemplateId(event.target.value)} style={{ ...fieldStyle, minWidth: 260, maxWidth: 420, background: 'var(--bg)' }}>
          <option value="">{templates.isLoading ? '读取模板中…' : availableTemplates.length === 0 ? '暂无已上架模板' : '选择一个已上架模板'}</option>
          {availableTemplates.map((template) => <option key={template.id} value={template.id}>{template.name}</option>)}
        </select>
        <Btn
          primary
          disabled={!templateId || actions.create.isPending}
          onClick={() => {
            const template = availableTemplates.find((item) => item.id === templateId)
            if (!template) return
            actions.create.mutate(
              { name: `${template.name}（副本）`, templateId: Number(template.id) },
              {
                onSuccess: (result) => {
                  setSelId(result.id)
                  setTab('tree')
                  setTemplateId('')
                  say(`已从「${template.name}」复制为本班草稿`)
                },
                onError: fail,
              },
            )
          }}
        >
          从模板新建
        </Btn>
      </div>

      {/* 没有合适模板、或者本班规则和任何模板都不像时，直接空白开一份。
          骨架给的是四个标准大项与合计为 1 的权重——不给这些，发布校验第一关就过不去，
          而新手多半不知道 major/moral/practice/health 这四个 key 是写死的。 */}
      <div style={{ borderTop: '1px solid var(--line)', padding: '14px 16px', display: 'flex', alignItems: 'center', gap: 10, flexWrap: 'wrap' }}>
        <div style={{ display: 'flex', flexDirection: 'column', gap: 4, minWidth: 190, marginRight: 'auto' }}>
          <span style={{ fontSize: 12.5, fontWeight: 600 }}>新建默认方案</span>
          <span style={{ fontSize: 11.5, color: 'var(--fg3)' }}>
            四个标准大项和基础项已建好，申报小项自己加。
          </span>
        </div>
        <Btn
          disabled={actions.create.isPending}
          onClick={() =>
            actions.create.mutate(
              { name: '默认综合测评方案' },
              {
                onSuccess: (result) => {
                  setSelId(result.id)
                  setTab('tree')
                  say('默认草稿已建立')
                },
                onError: fail,
              },
            )
          }
        >
          新建默认方案
        </Btn>
      </div>
      </div>

      <div data-r="split" style={{ display: 'grid', gridTemplateColumns: '252px 1fr', gap: 0, borderTop: '1px solid var(--line)' }}>
        <div className="scheme-versions-column" style={{ minWidth: 0, padding: '22px 26px 22px 0', borderRight: '1px solid var(--line)' }}>
          <div style={{ fontSize: 12, fontWeight: 600, letterSpacing: '.01em', color: 'var(--fg2)', paddingBottom: 12 }}>草稿</div>
          {drafts.length === 0 ? (
            <div style={{ fontSize: 12.5, color: 'var(--fg3)', paddingBottom: 14 }}>没有草稿。</div>
          ) : (
            drafts.map((s) => {
              const on = s.id === selId
              return (
                <button
                  key={s.id}
                  type="button"
                  className="hv-fg"
                  onClick={() => {
                    setSelId(s.id)
                    setTab('tree')
                  }}
                  style={{
                    display: 'flex', alignItems: 'center', gap: 10, width: '100%',
                    background: on ? 'var(--sub)' : 'transparent', border: 0, margin: 0,
                    padding: '10px 10px 10px 0', font: 'inherit', textAlign: 'left',
                    cursor: 'pointer', color: on ? 'var(--fg)' : 'var(--fg2)',
                  }}
                >
                  <span style={{ width: 3, height: 14, flex: 'none', background: on ? 'var(--red)' : 'transparent' }} />
                  <span style={{ fontSize: 13, fontWeight: on ? 600 : 400, minWidth: 0, overflow: 'hidden', textOverflow: 'ellipsis', whiteSpace: 'nowrap' }}>{s.name}</span>
                </button>
              )
            })
          )}

          <div style={{ fontSize: 12, fontWeight: 600, letterSpacing: '.01em', color: 'var(--fg2)', padding: '18px 0 4px', borderTop: '1px solid var(--line2)', marginTop: 12 }}>
            已发布版本
          </div>
          <div style={{ fontSize: 11, color: 'var(--fg3)', lineHeight: 1.55, paddingBottom: 8 }}>点任意一个版本，会复制成一份草稿，直接进编辑器。</div>
          {versions.map((s) => (
            <button
              key={s.id}
              type="button"
              className="hv-sub"
              disabled={actions.create.isPending}
              title={`从 v${s.version} 创建修订草稿`}
              onClick={() => actions.create.mutate(
                { name: `${s.name}（修订草稿）`, sourceSchemeId: Number(s.id) },
                {
                  onSuccess: (result) => {
                    setSelId(result.id)
                    setTab('tree')
                    say(`已从 v${s.version} 创建修订草稿，可直接编辑`)
                  },
                  onError: fail,
                },
              )}
              style={{ display: 'flex', alignItems: 'center', gap: 10, width: '100%', border: 0, background: 'transparent', padding: '9px 5px', color: 'inherit', font: 'inherit', cursor: actions.create.isPending ? 'wait' : 'pointer', textAlign: 'left' }}
            >
              <span style={{ ...mono('11px', '.02em'), color: 'var(--fg2)' }}>v{s.version}</span>
              <span style={{ fontSize: 12.5, color: 'var(--fg3)', minWidth: 0, overflow: 'hidden', textOverflow: 'ellipsis', whiteSpace: 'nowrap' }}>{s.name}</span>
              <span style={{ marginLeft: 'auto', fontSize: 10.5, color: 'var(--fg3)', flex: 'none' }}>编辑</span>
            </button>
          ))}
          {versions.length === 0 && <div style={{ fontSize: 12.5, color: 'var(--fg3)' }}>还没有发布过。</div>}
        </div>

        <div className="scheme-editor-column" style={{ minWidth: 0, padding: '22px 0 22px 30px' }}>
          <Sub
            title={selId ? `编辑草稿 · ${draftName}` : current ? `当前生效方案 ${current.version}` : '方案'}
            note={selId ? '先保存再发布。发布的时候会整体检查一遍' : '只读。要改就先另存为草稿'}
            actions={
              <Seg
                items={[
                  { key: 'tree', label: 'GUI 编辑' },
                  { key: 'json', label: 'JSON' },
                  { key: 'preview', label: '学生端实时预览' },
                ]}
                value={tab}
                onChange={setTab}
                pad="4px 12px"
                fs={12}
              />
            }
          />

          {selId && (
            <div style={{ border: '1px solid var(--line)', background: 'var(--sub)', padding: '13px 15px', marginTop: 12, display: 'flex', alignItems: 'flex-end', gap: 10, flexWrap: 'wrap' }}>
              <label style={{ display: 'flex', flexDirection: 'column', gap: 6, minWidth: 240, flex: '1 1 340px' }}>
                <span style={{ fontSize: 12, fontWeight: 600, color: 'var(--fg2)' }}>方案名称</span>
                <input className="hv-bfg" value={draftName} onChange={(event) => setDraftName(event.target.value)} style={{ ...fieldStyle, background: 'var(--bg)', border: '1px solid var(--line)', padding: '9px 11px' }} />
              </label>
              {dirty && <Pill tone="warn">有未保存修改</Pill>}
              <Btn
                danger
                disabled={!draft.data || actions.remove.isPending || actions.update.isPending || actions.publish.isPending}
                onClick={() => {
                  if (!selId || !window.confirm(`确定删除草稿“${draftName}”吗？${dirty ? '未保存修改也会一并丢失。' : ''}\n\n已经发布的版本和以前的提交都不受影响。`)) return
                  actions.remove.mutate(selId, {
                    onSuccess: () => {
                      setSelId(null)
                      setDraftName('')
                      setJson('')
                      setError(null)
                      say('草稿已删除')
                    },
                    onError: fail,
                  })
                }}
              >
                {actions.remove.isPending ? '删除中…' : '删除草稿'}
              </Btn>
              <Btn disabled={!!parseError || !draft.data || actions.remove.isPending || actions.update.isPending || actions.publish.isPending} onClick={() => void saveDraft()}>
                {actions.update.isPending ? '保存中…' : '保存草稿'}
              </Btn>
              <Btn primary disabled={!!parseError || !draft.data || actions.remove.isPending || actions.update.isPending || actions.publish.isPending} onClick={() => void publishDraft()}>
                {actions.publish.isPending ? '发布中…' : actions.update.isPending ? '先保存…' : '保存并发布'}
              </Btn>
              <span style={{ width: '100%', fontSize: 11.5, color: Math.abs(weightSum - 1) < 1e-9 ? 'var(--fg3)' : 'var(--red)' }}>
                GUI、JSON 和学生端预览用的是同一份本地草稿 · 权重合计 {weightSum.toFixed(2)}（发布要求 1.00）
              </span>
            </div>
          )}

          {parseError && <div role="alert" style={{ marginTop: 12, fontSize: 12.5, color: 'var(--red)' }}>JSON 语法错误：{parseError}</div>}
          {error && (
            <div role="alert" style={{ marginTop: 12, border: '1px solid var(--red)', background: 'var(--redBg)', padding: '12px 14px', fontSize: 12.5, color: 'var(--fg2)', lineHeight: 1.75, whiteSpace: 'pre-wrap' }}>
              {error}
            </div>
          )}

          {tab === 'preview' ? (
            !previewConfig ? (
              <div style={{ paddingTop: 14 }}>
                <Empty title="没有可预览的方案" desc="先选一份草稿，或者发布一版，预览才有东西可看。" />
              </div>
            ) : (
              <>
                <div style={{ display: 'flex', alignItems: 'center', gap: 13, flexWrap: 'wrap', background: 'var(--warnBg)', border: '1px solid var(--warn)', padding: '12px 16px', marginTop: 14 }}>
                  <span style={{ fontSize: 12.5, fontWeight: 600, color: 'var(--fg)', flex: 'none' }}>学生视角预览中</span>
                  <span style={{ fontSize: 12.5, color: 'var(--fg2)', lineHeight: 1.6, minWidth: 0, textWrap: 'pretty' }}>
                    下面就是学生实际看到的提交表单，可以直接试填，顺便检查规则。
                    当前预览的是{selId ? (parseError ? '草稿最后一次可解析的内容' : '正在编辑的草稿') : `已发布的 ${current?.version ?? ''}`}。
                  </span>
                </div>
                <SchemePreview config={previewConfig} focusKey={focusKey} onFocus={setFocusKey} trial={trial} onTrial={setTrial} />
              </>
            )
          ) : tab === 'tree' ? (
            !selId ? (
              <div style={{ paddingTop: 14 }}><Empty title="先选择一份草稿" desc="已发布的版本改不了。从左边选草稿，或者新建一份。" /></div>
            ) : !draftConfig ? (
              <div style={{ paddingTop: 14 }}>
                <Empty title="当前 JSON 还不是完整方案" desc="可以转去 JSON 手动修，也可以初始化成四个标准大项。" />
                <div style={{ paddingTop: 12 }}><Btn onClick={() => setJson(JSON.stringify(scoringConfig(defaultSchemeConfig()), null, 2))}>初始化可视化方案</Btn></div>
              </div>
            ) : (
              <div style={{ paddingTop: 14 }}><SchemeGuiEditor config={draftConfig} savedConfig={draft.data?.config ?? null} focusKey={focusKey} onFocusKey={setFocusKey} onChange={(next) => setJson(JSON.stringify(scoringConfig(next), null, 2))} /></div>
            )
          ) : !selId ? (
            <div style={{ paddingTop: 14 }}>
              <Empty title="选一份草稿来编辑" desc="或者点右上角「另存草稿」，照当前发布的版本复制一份。" />
            </div>
          ) : (
            <div style={{ paddingTop: 14, display: 'flex', flexDirection: 'column', gap: 14 }}>
              <textarea
                value={json}
                onChange={(e) => setJson(e.target.value)}
                rows={26}
                spellCheck={false}
                style={{ width: '100%', background: 'var(--bg)', border: `1px solid ${parseError ? 'var(--red)' : 'var(--line)'}`, padding: '12px 14px', color: 'var(--fg)', font: "400 12.5px/1.7 'JetBrains Mono',monospace", resize: 'vertical' }}
              />

              <Note>只管评分结构。时间与评优比例去「时间窗口」改，业务开关在「功能开关」里。</Note>
            </div>
          )}
        </div>
      </div>

      {current && (
        <div style={{ borderTop: '1px solid var(--line)', marginTop: 26, paddingTop: 18 }}>
          <Sub title="当前生效版本信息" />
          <Row label="方案名称" value={current.schemeName} />
          <Row label="版本" value={current.version} />
          <Row label="小项总数" value={`${current.categories.reduce((s, c) => s + c.items.length, 0)} 个可申报小项`} />
        </div>
      )}
    </div>
  )
}
