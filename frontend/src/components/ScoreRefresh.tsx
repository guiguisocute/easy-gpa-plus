import { useEffect, useState } from 'react'
import { TextBtn } from '@/components/ui'

export function ScoreRefresh({ fetching, onRefresh, label = '刷新成绩' }: { fetching: boolean; onRefresh: () => void; label?: string }) {
  const [cooldownUntil, setCooldownUntil] = useState(0)
  const [remaining, setRemaining] = useState(0)

  useEffect(() => {
    if (!cooldownUntil) return
    // 计时器只更新按钮文字，不请求成绩；使用截止时间避免后台节流拉长冷却。
    const timer = window.setInterval(() => {
      const seconds = Math.max(0, Math.ceil((cooldownUntil - Date.now()) / 1000))
      setRemaining(seconds)
      if (seconds === 0) window.clearInterval(timer)
    }, 1000)
    return () => window.clearInterval(timer)
  }, [cooldownUntil])

  function refresh() {
    if (fetching || Date.now() < cooldownUntil) return
    setCooldownUntil(Date.now() + 15_000)
    setRemaining(15)
    onRefresh()
  }

  return <TextBtn disabled={fetching || remaining > 0} onClick={refresh}>
    {fetching ? '刷新中…' : remaining > 0 ? `${remaining} 秒后可刷新` : label}
  </TextBtn>
}
