/* 把一次拖放展开成文件列表。

   顶层文件一律取 `dataTransfer.files`，只有目录才走 entry API。这不是偷懒：用 CDP
   合成一次真实拖放实测，`FileSystemFileEntry.file()` 会走错误回调、目录的
   `readEntries` 会回 EncodingError，而同一次拖放的 `dataTransfer.files` 是齐的。
   先前的写法只要拿到 entry 就整个改走 entry API，于是"拖进去毫无反应，连提示都没有"。

   （用 Playwright 合成的 `new DataTransfer()` 测不出这件事：它的 `webkitGetAsEntry()`
   一律返回 null，永远只走得到回退分支。） */

// 目录可能很深也可能很大，扫描量封顶；"一批最多几张"由调用方按后端限额另判。
export const SCAN_LIMIT = 500
// entry API 的回调可能一个都不回来（既不成功也不失败），不设超时就会永远停在这。
const ENTRY_TIMEOUT_MS = 3000

export async function filesFromDrop(transfer: DataTransfer, timeoutMs = ENTRY_TIMEOUT_MS) {
  // webkitGetAsEntry 必须在 drop 处理器的同步阶段取完：一 await，items 就失效了。
  const entries = Array.from(transfer.items, (item) => item.webkitGetAsEntry?.() ?? null)
  const directories: FileSystemDirectoryEntry[] = []
  for (const entry of entries) {
    if (entry?.isDirectory) directories.push(entry as FileSystemDirectoryEntry)
  }
  // 目录在 files 里是个 0 字节、无类型的占位，按名字剔掉，内容由下面走一遍拿。
  const folderNames = new Set(directories.map((entry) => entry.name))
  const files = Array.from(transfer.files).filter(
    (file) => !(file.size === 0 && file.type === '' && folderNames.has(file.name)),
  )
  for (const directory of directories) await walkDirectory(directory, files, timeoutMs)
  return files
}

async function walkDirectory(directory: FileSystemDirectoryEntry, out: File[], timeoutMs: number): Promise<void> {
  const reader = directory.createReader()
  /* readEntries 一次最多回 100 条，要一直读到空批为止。第一批就空也可能是浏览器还
     没把目录准备好，所以连着读到两个空批才收工。 */
  let emptyBatches = 0
  while (out.length < SCAN_LIMIT) {
    const batch = await guard<FileSystemEntry[]>([], timeoutMs, (resolve) => reader.readEntries(resolve, () => resolve([])))
    if (!batch.length) {
      if (++emptyBatches >= 2) return
      continue
    }
    emptyBatches = 0
    for (const entry of batch) {
      if (out.length >= SCAN_LIMIT) return
      if (entry.isDirectory) {
        await walkDirectory(entry as FileSystemDirectoryEntry, out, timeoutMs)
        continue
      }
      const file = await guard<File | null>(null, timeoutMs, (resolve) => (entry as FileSystemFileEntry).file(resolve, () => resolve(null)))
      if (file) out.push(file)
    }
  }
}

function guard<T>(fallback: T, timeoutMs: number, run: (resolve: (value: T) => void) => void) {
  return new Promise<T>((resolve) => {
    const timer = setTimeout(() => resolve(fallback), timeoutMs)
    run((value) => {
      clearTimeout(timer)
      resolve(value)
    })
  })
}
