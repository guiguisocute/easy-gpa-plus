import assert from 'node:assert/strict'
import test from 'node:test'
import { SCAN_LIMIT, filesFromDrop } from './dropFiles.ts'

/* 伪造 entry API。readEntries 的分页语义（一次最多 100 条、读到空批为止）和"回调
   一个都不回来"是这段代码仅有的两处易错点，所以假条目按 batches 一批批吐，另外留
   一种什么都不回调的条目。 */
function fileEntry(name: string, broken = false) {
  return {
    isFile: true,
    isDirectory: false,
    name,
    file: (resolve: (file: File) => void, reject: (error: Error) => void) =>
      broken ? reject(new Error('读不出来')) : resolve({ name } as File),
  } as unknown as FileSystemEntry
}

function dirEntry(name: string, batches: FileSystemEntry[][]) {
  let cursor = 0
  return {
    isFile: false,
    isDirectory: true,
    name,
    createReader: () => ({
      readEntries: (resolve: (entries: FileSystemEntry[]) => void) => resolve(cursor < batches.length ? batches[cursor++] : []),
    }),
  } as unknown as FileSystemEntry
}

const silentDir = {
  isFile: false,
  isDirectory: true,
  name: '闷葫芦',
  createReader: () => ({ readEntries: () => {} }),
} as unknown as FileSystemEntry

function drop(entries: (FileSystemEntry | null)[], files: { name: string; size?: number; type?: string }[]) {
  return {
    items: entries.map((entry) => ({ webkitGetAsEntry: () => entry })),
    files: files.map((file) => ({ size: 1, type: 'image/png', ...file })),
  } as unknown as DataTransfer
}

/* 真实拖放里 entry API 常常一个文件都给不出来（实测 file() 直接走错误回调），而
   dataTransfer.files 是齐的。顶层文件因此不能依赖 entry。 */
test('顶层文件取自 dataTransfer.files，entry 读不出来也照样进来', async () => {
  const files = await filesFromDrop(drop([fileEntry('一.png', true), fileEntry('二.png', true)], [{ name: '一.png' }, { name: '二.png' }]), 20)
  assert.deepEqual(files.map((file) => file.name), ['一.png', '二.png'])
})

test('目录连同子目录一起展开，目录本身的占位不会被当成文件', async () => {
  const tree = dirEntry('材料', [[
    fileEntry('封面.png'),
    dirEntry('志愿服务', [[fileEntry('证书.pdf'), fileEntry('合影.jpg')]]),
  ]])
  const files = await filesFromDrop(drop([tree], [{ name: '材料', size: 0, type: '' }]), 20)
  assert.deepEqual(files.map((file) => file.name), ['封面.png', '证书.pdf', '合影.jpg'])
})

test('readEntries 分批返回时要一直读到空批，而不是只收第一批', async () => {
  const paged = dirEntry('大目录', [[fileEntry('a.png'), fileEntry('b.png')], [fileEntry('c.png')]])
  const files = await filesFromDrop(drop([paged], [{ name: '大目录', size: 0, type: '' }]), 20)
  assert.deepEqual(files.map((file) => file.name), ['a.png', 'b.png', 'c.png'])
})

test('回调不回来时按超时收工，不会把整次拖放卡死', async () => {
  const files = await filesFromDrop(drop([silentDir], [{ name: '闷葫芦', size: 0, type: '' }]), 20)
  assert.deepEqual(files, [])
})

test('扫描量封顶，避免拖进一个巨大目录把页面卡死', async () => {
  const huge = dirEntry('全盘', [Array.from({ length: SCAN_LIMIT + 50 }, (_, index) => fileEntry(`${index}.png`))])
  const files = await filesFromDrop(drop([huge], [{ name: '全盘', size: 0, type: '' }]), 20)
  assert.equal(files.length, SCAN_LIMIT)
})

test('浏览器不给 entry 时退化成纯文件拖放', async () => {
  const files = await filesFromDrop(drop([null], [{ name: '只有文件.png' }]), 20)
  assert.deepEqual(files.map((file) => file.name), ['只有文件.png'])
})
