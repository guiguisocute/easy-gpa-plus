import type { PublicityWindow } from '../api/types.ts'

export function publicityIsOpen(window: PublicityWindow | null | undefined, now: number): boolean {
  return !!window && Date.parse(window.open) <= now && now < Date.parse(window.close)
}
