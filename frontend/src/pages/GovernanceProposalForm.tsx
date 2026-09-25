import { useRef, useState } from 'react'
import { useGovernanceAction } from '@/api/governance'
import { userErrorMessage } from '@/api/errorMessages'
import { TimeField } from '@/components/TimeField'
import { fieldStyle } from '@/lib/style'
import { Btn, Field, Note } from '@/components/ui'
import { useApp } from '@/stores/app'

export default function GovernanceProposalForm({ timeline = false }: { timeline?: boolean }) {
  const action = useGovernanceAction(),
    say = useApp((s) => s.say),
    go = useApp((s) => s.go)
  const [title, setTitle] = useState(''),
    [body, setBody] = useState('')
  const [open, setOpen] = useState(''),
    [close, setClose] = useState(''),
    [lockdown, setLockdown] = useState(''),
    [changeLockdown, setChangeLockdown] = useState(false)
  const request = useRef(crypto.randomUUID())
  const changed = () => {
    request.current = crypto.randomUUID()
  }
  return (
    <form
      className="gov-form"
      onChange={changed}
      onSubmit={async (e) => {
        e.preventDefault()
        try {
          await action.mutateAsync({
            path: '/proposals',
            body: {
              requestId: request.current,
              kind: timeline ? 'protected' : 'ordinary',
              action: timeline ? 'timeline' : 'motion',
              title,
              body,
              payload: timeline
                ? {
                    open: new Date(open).toISOString(),
                    close: new Date(close).toISOString(),
                    ...(changeLockdown
                      ? {
                          lockdown: lockdown ? new Date(lockdown).toISOString() : null,
                        }
                      : {}),
                  }
                : {},
            },
          })
          say('提案已发布，内容与投票名单已冻结')
          go('govProposals')
        } catch (error) {
          say(userErrorMessage(error))
        }
      }}
    >
      <Field label="提案标题" required>
        <input
          style={fieldStyle}
          required
          maxLength={120}
          value={title}
          onChange={(e) => setTitle(e.target.value)}
        />
      </Field>
      <Field label="依据与拟执行的内容" required>
        <textarea
          style={{ ...fieldStyle, resize: 'vertical' }}
          required
          minLength={4}
          maxLength={10000}
          rows={5}
          value={body}
          onChange={(e) => setBody(e.target.value)}
        />
      </Field>
      {timeline && (
        <>
          <TimeField
            label="材料开放时间"
            required
            value={open}
            onChange={setOpen}
            hint="开始接收本周期材料。"
          />
          <TimeField
            label="材料截止时间"
            required
            value={close}
            onChange={setClose}
            min={open || undefined}
            hint="停止新提交并自动封存，审核与申诉仍可继续。"
          />
          <label className="gov-check">
            <input
              type="checkbox"
              checked={changeLockdown}
              onChange={(e) => setChangeLockdown(e.target.checked)}
            />
            同时调整全系统封锁时间
          </label>
          {changeLockdown && (
            <TimeField
              label="全系统封锁时间"
              value={lockdown}
              onChange={setLockdown}
              min={close || undefined}
              hint="留空表示解除封锁；设置后到期全班只读。"
            />
          )}
        </>
      )}
      <Note>
        {timeline ? '重大事项须达到全班保护门槛。' : '普通共同提案只记录约定，不授予额外权限。'}
        发布后先讨论公示，再开始表决。
      </Note>
      <div>
        <Btn primary type="submit" disabled={action.isPending}>
          发布提案
        </Btn>
      </div>
    </form>
  )
}
