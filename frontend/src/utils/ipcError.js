/**
 * 主进程/后端抛出的异常经过调用层后可能带一层包装：
 *   Wails: 直接是 Go 的 error 文本
 *   Electron: Error invoking remote method 'xxx': Error: 真正的信息
 * 这里统一把包装层剥掉，只保留给用户看的内容。
 */
export function ipcErrorMessage(err, fallback = '操作失败') {
  const raw = typeof err === 'string' ? err : err?.message || ''
  if (!raw) return fallback
  const matched = raw.match(/Error invoking remote method '[^']*':\s*(?:Error:\s*)?([\s\S]*)$/)
  const text = (matched ? matched[1] : raw).trim()
  return text || fallback
}
