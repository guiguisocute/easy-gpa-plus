/* 订阅一条媒体查询。用 useSyncExternalStore 而不是 useState+useEffect：
   首帧就能拿到正确结果，不会先按桌面渲染一帧再跳成窄屏。 */

import { useCallback, useSyncExternalStore } from 'react'

export function useMediaQuery(query: string): boolean {
  const subscribe = useCallback(
    (onChange: () => void) => {
      const mql = matchMedia(query)
      mql.addEventListener('change', onChange)
      return () => mql.removeEventListener('change', onChange)
    },
    [query],
  )
  return useSyncExternalStore(
    subscribe,
    () => matchMedia(query).matches,
    /* 本项目是纯客户端渲染，没有 SSR 快照可言，服务端一律按桌面算。 */
    () => false,
  )
}
