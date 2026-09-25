/* 提交表单的本地暂存。

   和数据库草稿是两层，分工不同：
   - localStorage 保住"手感"——还没落库的键入。切去看别的小项再切回来，字还在。
     它写在本机、不走网络，所以可以每次按键都存，不用担心把接口打满。
   - 数据库草稿保住"数据"——佐证必须挂在一条真实记录上，跨设备也只有它算数。

   两层冲突时按时间新的那份为准（见 Submit.tsx 的 restoreFor）。

   按 sid 分桶：机房共用浏览器是常态，不能让下一个人看到上一个人写了一半的材料。 */

const KEY_PREFIX = 'zc.draft.'
/** 超过这个天数的暂存直接丢掉：窗口早翻篇了，留着只会在某天突然弹出一段旧文字。 */
const MAX_AGE_DAYS = 14

export interface LocalDraft {
  itemKey: string
  /** 已经落库的那条草稿 id；还没建就是 null。 */
  draftId: string | null
  title: string
  qty: string
  level: number
  free: string
  md: string
  savedAt: number
}

type Bucket = Record<string, LocalDraft>

const bucketKey = (sid: string) => `${KEY_PREFIX}${sid}`

/* localStorage 在隐私模式下会直接抛异常。暂存丢了不影响任何正确性，
   所以一律吞掉——不能让"存不了草稿"变成"页面打不开"。 */
function readBucket(sid: string): Bucket {
  if (!sid) return {}
  try {
    const raw = localStorage.getItem(bucketKey(sid))
    if (!raw) return {}
    const parsed: unknown = JSON.parse(raw)
    if (!parsed || typeof parsed !== 'object') return {}
    const cutoff = Date.now() - MAX_AGE_DAYS * 24 * 60 * 60 * 1000
    const bucket: Bucket = {}
    for (const [key, value] of Object.entries(parsed as Bucket)) {
      if (value && typeof value.savedAt === 'number' && value.savedAt >= cutoff) bucket[key] = value
    }
    return bucket
  } catch {
    return {}
  }
}

function writeBucket(sid: string, bucket: Bucket) {
  try {
    if (Object.keys(bucket).length === 0) localStorage.removeItem(bucketKey(sid))
    else localStorage.setItem(bucketKey(sid), JSON.stringify(bucket))
  } catch {
    /* 配额满或隐私模式：这一次存不下就算了，下一次按键还会再试。 */
  }
}

/** 一张什么都没填、也还没落库的表单不值得占一条记录。 */
function isBlank(draft: LocalDraft) {
  return !draft.draftId && !draft.title.trim() && !draft.qty.trim() && !draft.free.trim() && !draft.md.trim() && draft.level === 0
}

export function readLocalDraft(sid: string, itemKey: string): LocalDraft | null {
  return readBucket(sid)[itemKey] ?? null
}

export function writeLocalDraft(sid: string, draft: LocalDraft) {
  if (!sid) return
  const bucket = readBucket(sid)
  if (isBlank(draft)) delete bucket[draft.itemKey]
  else bucket[draft.itemKey] = draft
  writeBucket(sid, bucket)
}

export function clearLocalDraft(sid: string, itemKey: string) {
  if (!sid) return
  const bucket = readBucket(sid)
  if (!(itemKey in bucket)) return
  delete bucket[itemKey]
  writeBucket(sid, bucket)
}

/** 左侧树上"这个小项有没有没写完的东西"的小圆点用它。 */
export function localDraftItemKeys(sid: string): Set<string> {
  return new Set(Object.keys(readBucket(sid)))
}
