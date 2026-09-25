import { useEffect, useState } from 'react'
import type { PublicityWindow } from '@/api/types'
import { publicityIsOpen } from '@/lib/publicity'

export function usePublicityWindow(window: PublicityWindow | null | undefined, serverNow: string | undefined, receivedAt: number) {
  const [tick, setTick] = useState(Date.now)
  useEffect(() => {
    if (!window) return
    const timer = setInterval(() => setTick(Date.now()), 1000)
    return () => clearInterval(timer)
  }, [window])
  const now = serverNow ? Date.parse(serverNow) + Math.max(0, tick - receivedAt) : tick
  return publicityIsOpen(window, now)
}
