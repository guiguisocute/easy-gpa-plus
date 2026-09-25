export const AGENT_FLOAT_MIN_WIDTH = 320
export const AGENT_FLOAT_MIN_HEIGHT = 380

export type FloatFrame = { left: number; top: number; width: number; height: number }
export type FloatEdge = 'n' | 's' | 'e' | 'w' | 'ne' | 'nw' | 'se' | 'sw'

export const FLOAT_EDGES: FloatEdge[] = ['n', 's', 'e', 'w', 'ne', 'nw', 'se', 'sw']

export function clampAxis(value: number, max: number) {
  return Math.min(Math.max(value, 0), Math.max(max, 0))
}

export function clampFrame(frame: FloatFrame, viewW: number, viewH: number): FloatFrame {
  const minW = Math.min(AGENT_FLOAT_MIN_WIDTH, viewW)
  const minH = Math.min(AGENT_FLOAT_MIN_HEIGHT, viewH)
  const width = Math.min(Math.max(frame.width, minW), viewW)
  const height = Math.min(Math.max(frame.height, minH), viewH)
  return {
    width,
    height,
    left: clampAxis(frame.left, viewW - width),
    top: clampAxis(frame.top, viewH - height),
  }
}

export function moveFrame(start: FloatFrame, dx: number, dy: number, viewW: number, viewH: number): FloatFrame {
  return clampFrame({
    ...start,
    left: start.left + dx,
    top: start.top + dy,
  }, viewW, viewH)
}

export function resizeFrame(start: FloatFrame, edge: FloatEdge, dx: number, dy: number, viewW: number, viewH: number): FloatFrame {
  const minW = Math.min(AGENT_FLOAT_MIN_WIDTH, viewW)
  const minH = Math.min(AGENT_FLOAT_MIN_HEIGHT, viewH)
  let width = start.width
  let height = start.height
  if (edge.includes('e')) width = start.width + dx
  if (edge.includes('w')) width = start.width - dx
  if (edge.includes('s')) height = start.height + dy
  if (edge.includes('n')) height = start.height - dy
  width = Math.min(Math.max(width, minW), viewW)
  height = Math.min(Math.max(height, minH), viewH)
  const left = edge.includes('w') ? start.left + start.width - width : start.left
  const top = edge.includes('n') ? start.top + start.height - height : start.top
  return clampFrame({ left, top, width, height }, viewW, viewH)
}
