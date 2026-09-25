// Live provider tests and optional Office preview artifacts have separate,
// explicit opt-ins. Every other skipped test is a verification failure.
const optionalTests = new Set([
  'easygpa/backend/internal/aiassist/TestLiveMaterialCompetitionIntent',
  'easygpa/backend/internal/aiassist/TestLiveComposeChunksProduceCandidates',
  'easygpa/backend/internal/aiassist/TestLiveComposeBatchEndToEnd',
  'easygpa/backend/internal/llm/TestOpenCodeGoContract',
  'easygpa/backend/internal/exportjob/TestCollegeRenderFixture',
])

export function checkGoTestReport(raw) {
  const result = { passed: 0, skipped: [], failed: [], requiredSkipped: [] }
  const runningPackages = new Set()
  let completedPackages = 0
  for (const line of raw.split(/\r?\n/)) {
    if (!line.trim()) continue
    const event = JSON.parse(line)
    if (!event.Test) {
      if (event.Action === 'start') runningPackages.add(event.Package)
      if (['pass', 'fail', 'skip'].includes(event.Action)) runningPackages.delete(event.Package)
      if (event.Action === 'pass' || event.Action === 'fail') completedPackages++
      if (event.Action === 'fail') result.failed.push(event.Package)
      continue
    }
    const name = `${event.Package}/${event.Test}`
    if (event.Action === 'pass') result.passed++
    if (event.Action === 'fail') result.failed.push(name)
    if (event.Action === 'skip') {
      result.skipped.push(name)
      if (!optionalTests.has(`${event.Package}/${event.Test.split('/')[0]}`)) result.requiredSkipped.push(name)
    }
  }
  if (!completedPackages || !result.passed || runningPackages.size) throw new Error('Go 测试报告为空或未完整结束')
  return result
}
