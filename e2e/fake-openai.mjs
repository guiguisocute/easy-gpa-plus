#!/usr/bin/env node
/* Deterministic OpenAI-compatible server for knowledge-agent E2E.
   It receives only synthetic fixtures and never reads repository or tenant data. */

import http from 'node:http'
import { pathToFileURL } from 'node:url'

const json = (value) => JSON.stringify(value)

export function syntheticScheme() {
  return {
    schemeName: '知识 Agent E2E 方案',
    weights: { major: 0.6, moral: 0.15, practice: 0.15, health: 0.1 },
    categories: [
      { key: 'major', name: '专业素质', maxTotal: 100, baseItems: [], penaltyItems: [], items: [] },
      {
        key: 'moral', name: '思想品德', maxTotal: 100, baseItems: [], penaltyItems: [],
        items: [{ key: 'moral_service', name: '志愿服务', scoreRule: { type: 'per_unit', unit: '次', per: 1, cap: 10 }, evidence: { required: false, types: ['pdf'], maxMb: 10 } }],
      },
      { key: 'practice', name: '实践创新', maxTotal: 100, baseItems: [], penaltyItems: [], items: [] },
      { key: 'health', name: '身心素质', maxTotal: 100, baseItems: [], penaltyItems: [], items: [] },
    ],
  }
}

function textOfMessages(messages) {
  return messages.map((message) => typeof message.content === 'string' ? message.content : json(message.content)).join('\n')
}

/* 空白重试探针的调用计数。第一次故意吐空白，第二次才正常作答。 */
const blankProbeCalls = new Map()

function agentResponse(messages) {
  const transcript = textOfMessages(messages)
  const toolResult = [...messages].reverse().find((message) => typeof message.content === 'string' && message.content.startsWith('TOOL_RESULT'))
  const question = messages.findLast?.((message) => message.role === 'user' && !String(message.content).startsWith('TOOL_RESULT'))?.content ?? transcript
  /* 真实供应商在 response_format=json_object 下遇到散文历史会整轮只吐空白：实测
     deepseek-v4-flash 五次里空三次，而一条空白 assistant 消息又会被 llm 侧判成
     非法请求，最终以「Agent 模型调用失败」收场。这里如实复现——历史里的助手轮
     只要不再是 JSON 对象，就还回一串空白。 */
  if (messages.some((message) => message.role === 'assistant' && !String(message.content).trim().startsWith('{'))) {
    return '   '
  }
  /* 扩展名探针：线上出现过「按文件名 + extension 检索不到明明存在的 PDF」，
     而同样的 SQL 手动跑却能命中。这一支把那次调用原样打出去。 */
  /* 原件下载探针：「把这份文件给我」。只 find_files 一步就直接引用那个文件
     handle——文件级引用不需要先读过，界面据此渲染出可下载的来源链接。检索词只用
     两套 E2E 语料都有的「学院」，所以浏览器用例和全栈用例共用这一支。 */
  if (String(question).includes('原件下载探针')) {
    if (!toolResult) return { type: 'tool', thought: '先找那份文件', tool: 'find_files', arguments: { query: '学院', limit: 5 } }
    const found = toolResult.content.match(/"Handle"\s*:\s*"([^"]+)"/)?.[1] ?? ''
    return {
      type: 'final', thought: '把文件本身作为来源交出去',
      answer: '这份文件我作为来源附在下面，点开即可下载原件。',
      citations: found ? [found] : [], proposedActions: [],
    }
  }
  if (String(question).includes('扩展名探针')) {
    const results = messages.filter((message) => typeof message.content === 'string' && message.content.startsWith('TOOL_RESULT'))
    if (results.length === 0) return { type: 'tool', thought: '先只按文件名找', tool: 'find_files', arguments: { query: '学院细则', limit: 10 } }
    if (results.length === 1) return { type: 'tool', thought: '再加上扩展名', tool: 'find_files', arguments: { query: '学院细则', extension: 'pdf', limit: 10 } }
    const counts = results.map((item) => Number(item.content.match(/"count"\s*:\s*(\d+)/)?.[1] ?? -1))
    return {
      type: 'final', thought: '报回两次命中数',
      answer: `扩展名探针：只按文件名命中 ${counts[0]} 个，加扩展名命中 ${counts[1]} 个。`,
      citations: [], proposedActions: [],
    }
  }
  /* 分词探针：模型写检索词爱用空格断句（「示例学院 学生综合素质考评
     实施细则」），而文件名里没有那个空格。整串当一个子串去 LIKE 必定零命中。 */
  if (String(question).includes('分词探针')) {
    const results = messages.filter((message) => typeof message.content === 'string' && message.content.startsWith('TOOL_RESULT'))
    if (results.length === 0) return { type: 'tool', thought: '空格断句找一次', tool: 'find_files', arguments: { query: '学院 细则', limit: 10 } }
    if (results.length === 1) return { type: 'tool', thought: '再掺一个不存在的词', tool: 'find_files', arguments: { query: '学院 细则 不存在的词', limit: 10 } }
    const counts = results.map((item) => Number(item.content.match(/"count"\s*:\s*(\d+)/)?.[1] ?? -1))
    return {
      type: 'final', thought: '报回两次命中数',
      answer: `分词探针：空格断句命中 ${counts[0]} 个，掺一个不存在的词命中 ${counts[1]} 个。`,
      citations: [], proposedActions: [],
    }
  }
  if (String(question).includes('空白重试探针')) {
    const seen = (blankProbeCalls.get(question) ?? 0) + 1
    blankProbeCalls.set(question, seen)
    if (seen === 1) return '   '
    return { type: 'final', thought: '上一轮空了，这次好好答', answer: '重试后我给出了完整回答。', citations: [], proposedActions: [] }
  }
  /* 闲聊不调工具、也没有引用。放宽引用硬拦截之后这条路径必须仍然完成，
     所以留一个固定分支让 E2E 覆盖它。 */
  if (/^(你好|您好|hi|hello)/i.test(String(question).trim())) {
    return { type: 'final', thought: '打个招呼就行', answer: '你好，我可以帮你查本班综测规则和材料。', citations: [], proposedActions: [] }
  }
  /* 命名奖项归类链探针。先故意只查方案并尝试作答，服务端必须拦住这个过早结论，
     把模型送回 find_files → grep；否则这条 E2E 会只留下 current_scheme 一步。 */
  if (transcript.includes('归类链探针')) {
    const results = messages.filter((message) => typeof message.content === 'string' && message.content.startsWith('TOOL_RESULT'))
    const repaired = transcript.includes('EVIDENCE_REQUIREMENT')
    if (results.length === 0) {
      return { type: 'tool', thought: '先看当前方案有哪些申报项', tool: 'current_scheme', arguments: {} }
    }
    if (results.length === 1 && !repaired) {
      const scheme = results[0].content.match(/"source"\s*:\s*"([^"]+)"/)?.[1] ?? ''
      return {
        type: 'final', thought: '只按方案名称猜测归类',
        answer: '过早结论：只看方案就把五个一归入其他技能类比赛。',
        citations: scheme ? [scheme] : [], proposedActions: [],
      }
    }
    if (results.length === 1) {
      return { type: 'tool', thought: '方案只是候选项，再找学院细则原文', tool: 'find_files', arguments: { query: '学院细则', limit: 5 } }
    }
    const latest = results.at(-1)?.content ?? ''
    if (latest.includes('"files"')) {
      const file = latest.match(/"Handle"\s*:\s*"([^"]+)"/)?.[1] ?? ''
      return { type: 'tool', thought: '在学院细则中核对五个一的明确归类', tool: 'grep', arguments: { query: 'Five-One project', mode: 'literal', sources: [file], contextLines: 1, limit: 5 } }
    }
    const scheme = results[0].content.match(/"source"\s*:\s*"([^"]+)"/)?.[1] ?? ''
    const passage = latest.match(/"Source"\s*:\s*"([^"]+)"/)?.[1] ?? ''
    return {
      type: 'final', thought: '方案申报项和学院细则原文已经相互核对',
      answer: '“五个一”获奖应填在实践创新素质的“其他技能类比赛获奖”项；归类依据来自学院细则，方案用于确认当前可选的申报入口。',
      citations: [scheme, passage].filter(Boolean), proposedActions: [],
    }
  }
  if (String(question).includes('学院规则文件')) {
    if (!toolResult) {
      return { type: 'tool', thought: '先找学院细则在哪', tool: 'find_files', arguments: { query: '学院细则.pdf', limit: 5 } }
    }
    if (toolResult.content.includes('"files"')) {
      const handle = toolResult.content.match(/"Handle"\s*:\s*"([^"]+)"/)?.[1] ?? ''
      return { type: 'tool', thought: '在细则里搜权重', tool: 'grep', arguments: { query: 'Official weights', mode: 'literal', sources: [handle], contextLines: 1, limit: 5 } }
    }
    const handle = toolResult.content.match(/"Source"\s*:\s*"([^"]+)"/)?.[1] ?? ''
    return {
      type: 'final',
      thought: '细则里写清楚了，可以回答',
      answer: '学院规则文件记录的四项权重为 60% / 15% / 15% / 10%。',
      citations: handle ? [handle] : [],
      proposedActions: [],
    }
  }
  /* 流式探针。真实模型写一段话要几秒，合成模型 60ms 就写完了，快到轮询根本
     抓不到中间态。这一支不调工具、答案够长，并让 SSE 慢速滴出，好让 E2E 真的
     观察到「答案在生成过程中一段段长出来」。 */
  if (String(question).includes('逐字流式探针')) {
    return {
      type: 'final',
      thought: '按要求慢慢写一段长文',
      answer: '这是一段用于验证逐字输出的长回答。' + '综测的四项权重分别是德育 60%、智育 15%、体育 15%、美育与劳育 10%。'.repeat(6),
      citations: [],
      proposedActions: [],
    }
  }
  if (!toolResult) return { type: 'tool', thought: '先看当前已发布方案', tool: 'current_scheme', arguments: {} }

  const handle = toolResult.content.match(/"source"\s*:\s*"([^"]+)"/)?.[1] ?? ''
  const actions = []
  if (String(question).includes('申报草稿')) {
    actions.push({
      kind: 'submission_draft', title: '志愿服务申报草稿', summary: '只创建普通草稿，不提交。',
      payload: { category: 'moral', itemKey: 'moral_service', title: 'E2E 志愿服务', claim: { quantity: 2 }, note: '由合成 E2E Agent 建议' },
      diff: [], citations: [handle], targetView: 'stuSubmit',
    })
  }
  const taskId = String(question).match(/任务\s*#?(\d+)/)?.[1]
  if (String(question).includes('审核草稿') && taskId) {
    actions.push({
      kind: 'review_draft', title: '审核预填草稿', summary: '只预填，不提交审核决定。',
      payload: { taskId, decision: 'accepted', score: null, reason: '合成 E2E 预填' },
      diff: [], citations: [handle], targetView: 'revDesk',
    })
  }
  if (String(question).includes('方案草稿')) {
    actions.push({
      kind: 'scheme_draft', title: '独立方案草稿', summary: '新建草稿，不改变已发布方案。',
      payload: { name: '知识 Agent 建议方案', config: syntheticScheme() }, diff: [], citations: [handle], targetView: 'admScheme',
    })
  }
  if (String(question).includes('导出草稿')) {
    actions.push({
      kind: 'export_draft', title: '汇总表导出预填', summary: '只预填导出选项，不创建任务。',
      payload: { kind: 'summary', options: { includeRanks: true } }, diff: [], citations: [handle], targetView: 'admExport',
    })
  }
  return {
    type: 'final',
    thought: '方案读到了，整理成回答',
    answer: '当前已发布方案的四项权重为 60% / 15% / 15% / 10%。这是合成 E2E 回答。',
    citations: handle ? [handle] : [],
    proposedActions: actions,
  }
}

function completion(body) {
  const messages = Array.isArray(body.messages) ? body.messages : []
  const system = typeof messages[0]?.content === 'string' ? messages[0].content : ''
  const hasImage = messages.some((message) => Array.isArray(message.content) && message.content.some((part) => part?.type === 'image_url'))
  let content
  if (system.includes("class knowledge engineer")) {
    if (hasImage) {
      content = json({
        type: 'final', thought: '先看清截图里的内容',
        answer: '我看到了你上传的截图；图片消息已由识图模型处理。', citations: [], proposedActions: [],
      })
    } else {
    // agentResponse 返回字符串表示“这一轮模型什么都没说”，直接原样当正文。
      const turn = agentResponse(messages)
      content = typeof turn === 'string' ? turn : json(turn)
    }
  } else if (hasImage) content = json({ text: '合成图片 OCR：志愿服务证明 2026-05-04' })
  else content = json({ ok: true })
  return {
    id: `chatcmpl_fake_${Date.now()}`,
    object: 'chat.completion',
    created: Math.floor(Date.now() / 1000),
    model: body.model ?? 'fake-model',
    choices: [{ index: 0, message: { role: 'assistant', content }, finish_reason: 'stop' }],
    usage: { prompt_tokens: 40, completion_tokens: 20, total_tokens: 60 },
  }
}

export function startFakeOpenAI({ port = 18081, host = '127.0.0.1' } = {}) {
  const stats = { calls: 0, visionCalls: 0, agentCalls: 0, streamCalls: 0, lastImageModel: '' }
  const server = http.createServer(async (request, response) => {
    if (request.method === 'GET' && request.url === '/health') {
      response.writeHead(200, { 'content-type': 'application/json' })
      response.end(json({ ok: true, stats }))
      return
    }
    if (request.method !== 'POST' || !request.url?.endsWith('/chat/completions')) {
      response.writeHead(404)
      response.end()
      return
    }
    const chunks = []
    for await (const chunk of request) chunks.push(chunk)
    let body
    try { body = JSON.parse(Buffer.concat(chunks).toString('utf8')) } catch { body = {} }
    stats.calls++
    const raw = json(body.messages ?? [])
    if (raw.includes('image_url')) {
      stats.visionCalls++
      stats.lastImageModel = String(body.model ?? '')
    }
    if (raw.includes('class knowledge engineer')) stats.agentCalls++
    const result = completion(body)
    if (!body.stream) {
      response.writeHead(200, { 'content-type': 'application/json' })
      response.end(json(result))
      return
    }
    /* 真的按 SSE 分块吐。切片按码点而不是按字节——真实供应商每个 data: 里都是
       一个合法 JSON 字符串，不会把一个字劈到两个分块里；把切点做成 5 个字符，
       足以让 JSON 结构、转义和 answer 字段在任意位置被截断。 */
    stats.streamCalls++
    response.writeHead(200, { 'content-type': 'text/event-stream', 'cache-control': 'no-cache', connection: 'keep-alive' })
    const head = { id: result.id, object: 'chat.completion.chunk', created: result.created, model: result.model }
    const runes = Array.from(result.choices[0].message.content)
    const gap = raw.includes('逐字流式探针') ? 40 : 2
    for (let at = 0; at < runes.length; at += 5) {
      const text = runes.slice(at, at + 5).join('')
      response.write(`data: ${json({ ...head, choices: [{ index: 0, delta: { content: text }, finish_reason: null }] })}\n\n`)
      await new Promise((resolve) => setTimeout(resolve, gap))
    }
    response.write(`data: ${json({ ...head, choices: [{ index: 0, delta: {}, finish_reason: 'stop' }] })}\n\n`)
    if (body.stream_options?.include_usage) {
      response.write(`data: ${json({ ...head, choices: [], usage: result.usage })}\n\n`)
    }
    response.write('data: [DONE]\n\n')
    response.end()
  })
  return new Promise((resolve, reject) => {
    server.once('error', reject)
    server.listen(port, host, () => resolve({ server, stats, url: `http://${host}:${port}/v1` }))
  })
}

if (process.argv[1] && import.meta.url === pathToFileURL(process.argv[1]).href) {
  const port = Number(process.env.FAKE_OPENAI_PORT ?? 18081)
  const host = process.env.FAKE_OPENAI_HOST ?? '127.0.0.1'
  const running = await startFakeOpenAI({ port, host })
  console.log(`fake OpenAI listening on ${running.url}`)
}
