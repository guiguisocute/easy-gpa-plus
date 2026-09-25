import { useState } from 'react'
import { Download, FileText } from 'lucide-react'
import { getClassResourceDownload, useClassResources } from '@/api/queries'
import { ApiError } from '@/api/client'
import { Btn, Empty, PageHead, Pill } from '@/components/ui'
import { formatBytes } from '@/lib/knowledgeAgent'
import { mono } from '@/lib/style'
import { useApp } from '@/stores/app'

export default function StudentResources() {
  const resources = useClassResources()
  const say = useApp((state) => state.say)
  const [downloading, setDownloading] = useState<string | null>(null)

  const download = async (id: string) => {
    setDownloading(id)
    try {
      const file = await getClassResourceDownload(id)
      const link = document.createElement('a')
      link.href = file.url
      link.download = file.filename
      document.body.appendChild(link)
      link.click()
      link.remove()
    } catch (error) {
      say(error instanceof ApiError ? error.message : '文件下载失败')
    } finally {
      setDownloading(null)
    }
  }

  if (resources.isLoading) return <div className="load-bar"><span /></div>

  const items = resources.data?.items ?? []
  return (
    <div style={{ animation: 'rise .28s ease both' }}>
      <PageHead en="CLASS RULES" title="班级操行分评定细则" desc="下载本班评定文件。" />

      {resources.isError ? (
        <Empty title="班级文件暂时无法读取" desc="请稍后刷新。" />
      ) : items.length === 0 ? (
        <Empty title="班级还没有评定文件" desc="文件上传完成后会显示在这里。" />
      ) : (
        <div style={{ borderTop: '1px solid var(--line)', marginTop: 10 }}>
          {items.map((item) => (
            <div key={item.id} style={{ display: 'flex', alignItems: 'center', gap: 14, borderBottom: '1px solid var(--line2)', padding: '17px 0', flexWrap: 'wrap' }}>
              <span style={{ width: 34, height: 34, display: 'grid', placeItems: 'center', flex: 'none', background: 'var(--sub)', color: 'var(--fg2)' }}>
                <FileText size={17} strokeWidth={1.5} />
              </span>
              <span style={{ display: 'flex', flexDirection: 'column', gap: 5, minWidth: 220, flex: 1 }}>
                <strong style={{ fontSize: 13.5, fontWeight: 600, color: 'var(--fg)' }}>{item.displayName}</strong>
                <span style={{ fontSize: 12, color: 'var(--fg3)' }}>{item.logicalPath}</span>
              </span>
              <Pill tone="idle">{formatBytes(item.sizeBytes)}</Pill>
              <span style={{ ...mono('11.5px', '0'), color: 'var(--fg3)', flex: 'none' }}>
                {new Date(item.updatedAt).toLocaleDateString('zh-CN')}
              </span>
              <Btn disabled={downloading === item.id} onClick={() => void download(item.id)}>
                <Download size={13} /> {downloading === item.id ? '获取中…' : '下载'}
              </Btn>
            </div>
          ))}
        </div>
      )}
    </div>
  )
}
