import fs from 'node:fs'
import path from 'node:path'
import ts from 'typescript'

/* nav.ts 里的 desc 会渲染在顶栏，和页面里的文案一样面向用户，却长期在扫描范围外——
   「配置树驱动的动态表单」「两人背靠背复核」就是这么漏进去的。 */
const roots = ['src/pages', 'src/components', 'src/App.tsx', 'src/lib/nav.ts']
const blocked = [
  '旧版单端点回退',
  '旧配置回退',
  '旧版回退',
  '一套部署供多个班级共用',
  '行级安全隔离',
  'JWT claim',
  '环境变量单独签发',
  '视角只改渲染不改鉴权',
  '不靠前端自觉',
  '开发环境没起',
  '开发环境通常是空',
  '后端 /',
  'WHERE class_id',
  'outbox 表解耦',
  '手写 XADD',
  '同一个二进制',
  '后端规则引擎',
  '接口根本不存在',
  '拿到的都是 404',
  '唯一能给出的可验证承诺',
  '通知策略：',
  '结算器一次性产出',
  '一行一个审核任务',
  '叠在各段',
  '凭据来自 environment',
  '收件人只留哈希',
  /* 行话。学生和班委不说这些词，写进界面只会让人多猜一层。
     「闸门」「整表」是本项目自己的固定叫法，用的人已经习惯了，不在此列。 */
  '落定',
  '签发',
  '背靠背',
  '套打',
  '闭环',
  '终态',
  '口径',
  '颗粒度',
  '抓手',
  '赋能',
  '沉淀',
  '打通',
  '对齐',
  '心智',
  '链路',
]

const files = roots.flatMap((root) => {
  const full = path.resolve(root)
  if (fs.statSync(full).isFile()) return [full]
  const walk = (dir) => fs.readdirSync(dir, { withFileTypes: true }).flatMap((entry) => {
    const target = path.join(dir, entry.name)
    return entry.isDirectory() ? walk(target) : /\.tsx?$/.test(entry.name) ? [target] : []
  })
  return walk(full)
})

const problems = []
for (const file of files) {
  const sourceText = fs.readFileSync(file, 'utf8')
  const source = ts.createSourceFile(file, sourceText, ts.ScriptTarget.Latest, true, ts.ScriptKind.TSX)
  const seen = new Set()
  const inspect = (node, raw, checkLength) => {
    const text = raw.replace(/\s+/g, ' ').trim()
    if (!/[\p{Script=Han}]/u.test(text)) return
    const reasons = blocked.filter((fragment) => text.includes(fragment)).map((fragment) => `包含「${fragment}」`)
    if (checkLength && text.length > 160) reasons.push(`静态文案长达 ${text.length} 字符`)
    if (reasons.length === 0) return
    const line = source.getLineAndCharacterOfPosition(node.getStart(source)).line + 1
    const key = `${line}:${text}`
    if (seen.has(key)) return
    seen.add(key)
    problems.push(`${path.relative(process.cwd(), file)}:${line} ${reasons.join('、')}`)
  }
  const visit = (node) => {
    if (ts.isJsxText(node)) inspect(node, node.getText(source), true)
    else if (ts.isStringLiteralLike(node)) inspect(node, node.text, true)
    else if (ts.isTemplateExpression(node)) inspect(node, node.getText(source), false)
    ts.forEachChild(node, visit)
  }
  visit(source)
}

/* Agent 的页面上下文在后端另有一份注册表（internal/agentcontext/context.go）。
   两边对不上时，学生在漏掉的那一页问一句就会收到 422「页面上下文不正确」，
   而 label 不一致更隐蔽——系统提示词里写着「当前页面是 X」，页面改了名这里
   没跟，模型就一直按旧页面回答。ops 的页面不参与：ops 用不了 Agent。 */
const navSource = fs.readFileSync('src/lib/nav.ts', 'utf8')
const goSource = fs.readFileSync('../backend/internal/agentcontext/context.go', 'utf8')

const navViews = new Map()
for (const [, view, label] of navSource.matchAll(/\bview:\s*'([A-Za-z]+)',\s*label:\s*'([^']+)'/g)) {
  if (!view.startsWith('ops')) navViews.set(view, label)
}
const goPages = new Map()
for (const [, view, , label] of goSource.matchAll(/"([A-Za-z]+)":\s*\{"[A-Za-z]+", "([a-z_]+)", "([^"]+)"\}/g)) {
  goPages.set(view, label)
}
for (const [view, label] of navViews) {
  if (!goPages.has(view)) problems.push(`agentcontext 缺少视图 ${view}（${label}）——该页的 Agent 会报「页面上下文不正确」`)
  else if (goPages.get(view) !== label) problems.push(`视图 ${view} 名称不一致：nav.ts「${label}」 vs agentcontext「${goPages.get(view)}」`)
}
for (const view of goPages.keys()) {
  if (!navViews.has(view)) problems.push(`agentcontext 多出视图 ${view}，nav.ts 里已经没有它了`)
}

if (problems.length > 0) {
  console.error('发现面向用户的实现说明或过长静态文案：')
  for (const problem of problems) console.error(`- ${problem}`)
  process.exit(1)
}

console.log('ui_copy_check=ok')
