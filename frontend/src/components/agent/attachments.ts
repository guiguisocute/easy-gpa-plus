import type { Attachment, AttachmentAdapter, CompleteAttachment, PendingAttachment } from '@assistant-ui/react'
import { removeAgentAttachment, uploadAgentAttachment } from '@/api/queries'
import { effectiveAgentPage, pageKey } from '@/stores/agentPage'
import { useApp } from '@/stores/app'

export const AGENT_IMAGE_ACCEPT = 'image/png,image/jpeg,image/webp,.png,.jpg,.jpeg,.webp'
export const AGENT_IMAGE_MAX_BYTES = 5 * 1024 * 1024
export const AGENT_IMAGE_MESSAGE_MAX_BYTES = 12 * 1024 * 1024
export const AGENT_IMAGE_MAX_COUNT = 4

export interface AgentAttachmentLimits { maxFileMb: number; maxMessageMb: number; maxCount: number }

/**
 * assistant-ui 负责选择、拖放和粘贴；这个 adapter 只负责把图片先直传到本班
 * 对象存储，并让最终 AppendMessage 携带服务端附件 ID。图片数据本身不会塞进
 * React 状态或 JSON API 请求。
 */
export class AgentImageAttachmentAdapter implements AttachmentAdapter {
  readonly accept = AGENT_IMAGE_ACCEPT
  private active = new Map<string, number>()
  private addingCount = 0
  private addingBytes = 0
  private readonly onError: (error: unknown) => void
	private readonly limits: AgentAttachmentLimits

	constructor(onError: (error: unknown) => void, limits: AgentAttachmentLimits = { maxFileMb: 5, maxMessageMb: 12, maxCount: 4 }) {
    this.onError = onError
	this.limits = limits
  }

  async add({ file }: { file: File }): Promise<PendingAttachment> {
    const scope = pageKey(effectiveAgentPage() ?? { view: useApp.getState().view })
    try {
      this.validate(file)
      this.addingCount++
      this.addingBytes += file.size
      const uploaded = await uploadAgentAttachment(file)
      if (scope !== pageKey(effectiveAgentPage() ?? { view: useApp.getState().view })) {
        await removeAgentAttachment(uploaded.id)
        throw new Error('上传期间当前事项已切换，请在需要的事项重新添加图片')
      }
      this.active.set(uploaded.id, uploaded.sizeBytes)
      return {
        id: uploaded.id,
        type: 'image',
        name: uploaded.filename,
        contentType: uploaded.mediaType,
        file,
        status: { type: 'requires-action', reason: 'composer-send' },
      }
    } catch (error) {
      this.onError(error)
      throw error
    } finally {
      this.addingCount--
      this.addingBytes -= file.size
    }
  }

  async send(attachment: PendingAttachment): Promise<CompleteAttachment> {
    return {
      ...attachment,
      status: { type: 'complete' },
      // onNew 读取 attachment.id；file part 只是满足 assistant-ui 的完整附件契约。
      // sourceType=id 可避免把这个私有服务端 ID 当作 base64 或公开 URL。
      content: [{
        type: 'file',
        filename: attachment.name,
        mimeType: attachment.contentType ?? 'application/octet-stream',
        data: attachment.id,
        sourceType: 'id',
      }],
    }
  }

  async remove(attachment: Attachment) {
    this.active.delete(attachment.id)
    await removeAgentAttachment(attachment.id)
  }

  markSent(ids: readonly string[]) {
    for (const id of ids) this.active.delete(id)
  }

  async discard(ids: readonly string[]) {
    await Promise.allSettled(ids.map(async (id) => {
      this.active.delete(id)
      await removeAgentAttachment(id)
    }))
  }

  private validate(file: File) {
    if (file.size <= 0) throw new Error('空图片不能添加')
	const maxFileBytes = this.limits.maxFileMb * 1024 * 1024
	const maxMessageBytes = this.limits.maxMessageMb * 1024 * 1024
	if (file.size > maxFileBytes) throw new Error(`单张图片不能超过 ${this.limits.maxFileMb} MB`)
	if (this.active.size + this.addingCount >= this.limits.maxCount) {
	  throw new Error(`每条消息最多添加 ${this.limits.maxCount} 张图片`)
    }
    const total = [...this.active.values()].reduce((sum, size) => sum + size, 0) + this.addingBytes + file.size
	if (total > maxMessageBytes) throw new Error(`每条消息的图片合计不能超过 ${this.limits.maxMessageMb} MB`)
  }
}
