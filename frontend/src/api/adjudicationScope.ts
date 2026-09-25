import { createContext, useContext } from 'react'

export type AdjudicationScope = 'admin' | 'deputy'

/** 副班管复用仲裁台，接口和缓存均限定在班管本人的事项。 */
export const AdjudicationContext = createContext<AdjudicationScope>('admin')
export const useAdjudicationScope = () => useContext(AdjudicationContext)

export function adjudicationPath(scope: AdjudicationScope, path: string): string {
  return scope === 'deputy' ? '/review/deputy' + path.replace(/^\/admin/, '') : path
}

export function adjudicationKey(scope: AdjudicationScope, key: readonly unknown[]): readonly unknown[] {
  return scope === 'deputy' ? ['review', 'deputy', ...(key[0] === 'admin' ? key.slice(1) : key)] : key
}

export const useAdjudicationBase = () => useAdjudicationScope() === 'deputy' ? '/review/deputy' : '/admin'
