import type { WindowState } from '../api/types'

/** Unsealing removes the personal seal; the class intake window still applies. */
export function submissionBlockReason(state: WindowState | undefined, now: number): string | null {
  if (!state || !Number.isFinite(now)) return '正在核对提交状态，请稍候。'
  const { window, capabilities, sealed } = state
  if (window.lockdown && now >= Date.parse(window.lockdown)) return '本学期已全系统封锁，不能上传或提交材料。'
  if (now < Date.parse(window.open)) return '材料提交尚未开放，请查看班级时间线。'
  if (now >= Date.parse(window.close)) return '材料提交已截止。补交须由班级管理员延长提交窗口，并解除个人封存。'
  if (sealed) return '材料已封存，不可再提交或修改。补交须由班级管理员解封；解封后本页会自动恢复上传。'
  if (!capabilities.submit) return '班级尚未开启材料提交，请联系班级管理员。'
  return null
}
