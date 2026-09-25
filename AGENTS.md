# EasyGPA Plus 开发约定

- 这是独立的开源仓库。只操作本目录和本项目的开发容器，不访问上游生产环境。
- 使用、部署与开发文档在独立仓库 easy-gpa-plus-doc（https://easygpa.guiguisocute.com）；本仓库 README 只保留入口。新增或删除环境变量、后端子命令时同步更新文档站参考页。
- 使用 npm 锁文件和现有 Go 版本；不随任务升级依赖。
- 学生实时查看成绩及单项问题进度；确认只是对当前版本的核对记录，不阻塞结算、不取消申诉权。不存在整表终审、整表问题或自动确认。
- 结算仍检查封存/截止、单项定分、待处理事项和专业成绩完整性；保持租户隔离和本人回避。
- 不提交密钥、个人名单、运行数据或第三方学校素材。示例使用虚构数据和 example.org 域名。
- 前端改动运行 npm run lint、npm test、npm run build；后端运行 gofmt、go test ./...、go vet ./...、go build ./...。交互改动用真实浏览器验证。
- 本地开发使用 scripts/dev.ps1 或 compose.dev.yaml；不自动发布、推送或联系外部服务。

- 可选班级共治为测试版，尚未经过生产验证；模式选择与文档都推荐普通模式，保留中心化默认。主动加入的注册成员经过 24 小时生效，不要求提交材料。冻结选民、重大事项全班保护门槛、随机评审与独立申诉不得被旧角色或 MCP 绕过。迁移 64，完整规则见 https://easygpa.guiguisocute.com/guide/governance。
- 普通与共治工作台独立：首次班管选择随机派单的普通模式或循环共治模式；共治不显示身份切换，普通侧栏不混入共治入口。侧栏分组用稳定唯一 ID；提案、评审、共同事务、参与规则分别展示。演示站顶部常驻模式切换入口；切换另一模式重新载入示例，真实班级仍仅首次选择。

- 共治不允许为同一件事另写一页。判的东西一样，就直接用普通模式那一页，用 `collective` 开关切接口前缀与措辞（`group/Objections.tsx`、`admin/Gpa.tsx`、`admin/Bonus.tsx`）；只剩摆放方式不同时，把共用的那几块提成组件（`ReviewDesk.tsx`、`SettlementGate.tsx`、`CollegeExportGuide.tsx`、`ExportProducts.tsx`、`TimeField`、`GpaTextInput`）两边共用。共享组件只接收数据与回调，不内置班管或共治写接口；模式导航、审核与表决权限仍由各自页面和后端控制。禁止另建 gov 专用表格、筛选、状态、输入框样式或限宽。
- 共治复用的接口挂在 router.go 的 `collective` 组：前缀换成 `/governance`，处理函数与 `/review`、`/admin` 那份相同，由 `requireGovernanceMember` 代替角色放行；前端只换前缀（`queries.ts` 的 `deskBase`）。独立评审台与初审工作台共用 `components/ReviewDesk.tsx`；`GET /governance/cases/:id` 下发当事人、申报内容、期望分和本轮已交份数，不下发其他评审的身份或意见内容。异议提交即抽签，共治不提供撤回。
