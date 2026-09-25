/* 顶层：会话判定 + 视图分发。

   视图用 store 里的 view 字段而不是 react-router：原型就是这个模型（一个外壳 + 一块内容区），
   URL 同步只需要 ?v= 一个参数，为此引一整个路由库不划算。
   代价是没有嵌套路由与路由级 code splitting——前者用不上，后者用 lazy 按角色分组补上。 */

import { lazy, Suspense, useEffect } from 'react'
import Shell from './components/Shell'
import Toast from './components/Toast'
import AuthOverlay from './components/AuthOverlay'
import { Empty } from './components/ui'
import { workspaceNav, workspaceView, type View } from './lib/nav'
import { useApp, useEffectiveRole } from './stores/app'
import { useGovernance } from './api/governance'

import StuHome from './pages/student/Home'
import StuSubmit from './pages/student/Submit'
import StuList from './pages/student/List'
import StuFileAppeal from './pages/student/FileAppeal'
import StuAppeals from './pages/student/Appeals'
import StuClassPenalties from './pages/student/ClassPenalties'
import StuResult from './pages/student/Result'
import StuResources from './pages/student/Resources'
/* 学生、小组、班管共用一页，所以不在 pages/student 下面。 */
import Account from './pages/Account'
import MailOptOut from './pages/MailOptOut'
import RevTasks from './pages/group/Tasks'
import RevDesk from './pages/group/Desk'
import RevAppeals from './pages/group/Appeals'
import RevObjections from './pages/group/Objections'
import RevReports from './pages/group/Reports'
import RevCat from './pages/group/Category'
import RevHist from './pages/group/History'

/* 管理端与运维台按角色分包：学生占绝大多数登录，没必要让他们下载这 19 个页面。 */
const AdmBoard = lazy(() => import('./pages/admin/Board'))
const Governance = lazy(() => import('./pages/Governance'))
const InitialModeChoice = lazy(() => import('./pages/ModeChoice'))
const AdmReviewProgress = lazy(() => import('./pages/admin/ReviewProgress'))
const AdmScheme = lazy(() => import('./pages/admin/Scheme'))
const AdmTimeline = lazy(() => import('./pages/admin/Timeline'))
const AdmFeatures = lazy(() => import('./pages/admin/Features'))
const AdmRoster = lazy(() => import('./pages/admin/Roster'))
const AdmDispatch = lazy(() => import('./pages/admin/Dispatch'))
const AdmSubs = lazy(() => import('./pages/admin/Arbitration'))
const AdmHist = lazy(() => import('./pages/admin/History'))
const RevDeputy = lazy(() => import('./pages/group/DeputyArbitration'))
const AdmGpa = lazy(() => import('./pages/admin/Gpa'))
const AdmBonus = lazy(() => import('./pages/admin/Bonus'))
const AdmKnowledge = lazy(() => import('./pages/admin/Knowledge'))
const AdmExport = lazy(() => import('./pages/admin/Export'))
const AdmAudit = lazy(() => import('./pages/admin/Audit'))

const OpsInstances = lazy(() => import('./pages/ops/Tenants'))
const OpsTemplates = lazy(() => import('./pages/ops/Templates'))
const OpsAgent = lazy(() => import('./pages/ops/Agent'))
const OpsMail = lazy(() => import('./pages/ops/Mail'))
const OpsJobs = lazy(() => import('./pages/ops/Jobs'))
const OpsBackup = lazy(() => import('./pages/ops/Backup'))
const OpsFlags = lazy(() => import('./pages/ops/Flags'))
const OpsHealth = lazy(() => import('./pages/ops/Health'))
const OpsDeploy = lazy(() => import('./pages/ops/Deploy'))
const OpsAudit = lazy(() => import('./pages/ops/Audit'))

const PAGES: Record<View, React.ComponentType> = {
  classGovernance: Governance,
  govHome: Governance,
  govProposals: Governance,
  govReviews: Governance,
  govParticipation: Governance,
  govOperations: Governance,
  govBonus: Governance,
  govObjections: Governance,
  govGpa: Governance,
  govExport: Governance,
  govSettle: Governance,
  govTimeline: Governance,
  govNewProposal: Governance,
  govSetupRoster: AdmRoster,
  govSetupScheme: AdmScheme,
  govSetupFeatures: AdmFeatures,
  govSetupFiles: AdmKnowledge,
  stuHome: StuHome,
  stuSubmit: StuSubmit,
  stuList: StuList,
  stuFileAppeal: StuFileAppeal,
  stuAppeals: StuAppeals,
  stuClassPenalties: StuClassPenalties,
  stuResult: StuResult,
  stuResources: StuResources,
  stuAccount: Account,
  revTasks: RevTasks,
  revDesk: RevDesk,
  revAppeals: RevAppeals,
  revObjections: RevObjections,
  revReports: RevReports,
  revCat: RevCat,
  revHist: RevHist,
  revDeputy: RevDeputy,
  admBoard: AdmBoard,
  admReviewProgress: AdmReviewProgress,
  admScheme: AdmScheme,
  admTimeline: AdmTimeline,
  admFeatures: AdmFeatures,
  admRoster: AdmRoster,
  admDispatch: AdmDispatch,
  admSubs: AdmSubs,
  admHist: AdmHist,
  admGpa: AdmGpa,
  admBonus: AdmBonus,
  admKnowledge: AdmKnowledge,
  admExport: AdmExport,
  admAudit: AdmAudit,
  opsInstances: OpsInstances,
  opsTemplates: OpsTemplates,
  opsAgent: OpsAgent,
  opsMail: OpsMail,
  opsJobs: OpsJobs,
  opsBackup: OpsBackup,
  opsFlags: OpsFlags,
  opsHealth: OpsHealth,
  opsDeploy: OpsDeploy,
  opsAudit: OpsAudit,
}

export default function App() {
  if (new URLSearchParams(window.location.search).has('mail_opt_out')) return <MailOptOut />
  return <SignedInApp />
}

function SignedInApp() {
  const { user, view, sessionReady, restore, go } = useApp()
  const role = useEffectiveRole()
  const governance = useGovernance()
  const collective = user?.role !== 'ops' && !!governance.data && governance.data.config.mode !== 'centralized'
  const initial = user?.role === 'class_admin' && governance.data?.config.version === 0 && governance.data.canConfigure
  const modeReady = user?.role === 'ops' || governance.isSuccess
  const entries = workspaceNav(role, collective)
  const allowed = entries.filter(n => (!n.deputyOnly || (user?.role === 'group' && user.isDeputy)) && (!n.preparationOnly || (governance.data?.config.mode==='enrolling' && governance.data.canConfigure)))
  const resolvedView = workspaceView(allowed, view)
  useEffect(() => {
    if (!user || !modeReady || initial) return
    if (collective && useApp.getState().viewAs) useApp.setState({ viewAs: null })
    if (view !== resolvedView) go(resolvedView)
  }, [user, modeReady, initial, collective, view, resolvedView, go])

  /* 刷新页面后用 HttpOnly refresh cookie 换一次 access token 并读 /me。
     换不到就留在登录页——access token 只在内存里，本来就活不过一次刷新。 */
  useEffect(() => {
    void restore()
  }, [restore])

  /* 兜底守卫：URL 里带来的 view 不属于当前生效角色时踢回该角色首页。
     这只是防误操作，真正的越权拦截在后端——前端守卫从来不是安全边界。 */

  if (!sessionReady) return null
  if (!user) {
    return (
      <>
        <AuthOverlay />
        <Toast />
      </>
    )
  }

  if (!modeReady) return <><Empty title={governance.error ? '暂时无法读取班级工作方式' : '正在进入班级工作台'} desc={governance.error ? '请检查网络后重试。' : undefined} />{governance.error && <button onClick={()=>void governance.refetch()}>重试</button>}<Toast /></>
  if (initial) return <><Suspense fallback={<div className="load-bar"><span /></div>}><InitialModeChoice /></Suspense><Toast /></>
  const Page = PAGES[resolvedView]

  return (
    <>
      <Shell key={collective ? 'collective' : role} collective={collective}>
        <Suspense fallback={<div className="load-bar"><span /></div>}>
          <Page key={resolvedView} />
        </Suspense>
      </Shell>
      <Toast />
    </>
  )
}
