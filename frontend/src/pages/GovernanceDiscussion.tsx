import { useState } from 'react'
import { useGovernanceComments, useGovernanceAction } from '@/api/governance'
import { userErrorMessage } from '@/api/errorMessages'
import { RichText } from '@/components/Markdown'
import { fieldStyle } from '@/lib/style'
import { Btn, Field, Note } from '@/components/ui'
import { useApp } from '@/stores/app'

export default function GovernanceDiscussion({
  id,
  closed = false,
  evidence = false,
}: {
  id: string
  closed?: boolean
  evidence?: boolean
}) {
  const comments = useGovernanceComments(id),
    action = useGovernanceAction(),
    say = useApp((s) => s.say)
  const [body, setBody] = useState('')
  async function submit(event: React.FormEvent<HTMLFormElement>) {
    event.preventDefault()
    try {
      const result = await action.mutateAsync({
        path: `/proposals/${id}/comments`,
        body: { body, kind: evidence ? 'evidence' : 'discussion' },
      })
      setBody('')
      say(result.notice ?? '说明已保存')
    } catch (error) {
      say(userErrorMessage(error))
    }
  }
  return (
    <details className="gov-discussion">
      <summary>
        {evidence ? '当事人补充说明' : '讨论与依据'} · {comments.data?.items.length ?? 0}
      </summary>
      {comments.error && (
        <div role="alert">
          <Note tone="bad">{userErrorMessage(comments.error)}</Note>
        </div>
      )}
      {comments.data?.items.map((item) => (
        <article className="gov-proposal" key={item.id}>
          <small>
            {item.mine ? '我的说明' : '成员说明'} ·{' '}
            {new Date(item.createdAt).toLocaleString('zh-CN')}
          </small>
          <RichText value={item.body} />
        </article>
      ))}
      {!closed && (
        <form className="gov-form" onSubmit={submit}>
          <Field label={evidence ? '新的事实或规则依据（限当事人补充一次）' : '讨论意见'} required>
            <textarea
              style={{ ...fieldStyle, resize: 'vertical' }}
              required
              minLength={evidence ? 20 : 4}
              maxLength={5000}
              value={body}
              onChange={(e) => setBody(e.target.value)}
            />
          </Field>
          <Btn type="submit" disabled={action.isPending}>
            {evidence ? '补充说明并开启新版本复核' : '发表意见'}
          </Btn>
        </form>
      )}
    </details>
  )
}
