# 参与开发

完整说明见文档站的[参与开发](https://easygpa.guiguisocute.com/development/contributing)、[架构](https://easygpa.guiguisocute.com/development/architecture)与[测试与 E2E](https://easygpa.guiguisocute.com/development/testing)。

使用 Docker Compose 启动本地依赖；配置只写入被忽略的 `.env.development`。

- 前端：在 `frontend/` 运行 `npm ci`、`npm run lint`、`npm test`、`npm run build`。
- 后端：在 `backend/` 运行 `gofmt`、`go test ./...`、`go vet ./...`、`go build ./...`。
- 集成测试：`node e2e/run.mjs backend` 使用独立的本地测试数据库。

提交应保持范围清晰，说明行为变化与验证结果。业务名词见 [CONTEXT.md](CONTEXT.md)。
示例仅使用虚构数据；请勿在 issue、日志或代码中放入个人资料与密钥。

文档修改请向 [easy-gpa-plus-doc](https://github.com/guiguisocute/easy-gpa-plus-doc) 提交。新增或删除环境变量、后端子命令时，请同步更新文档站的参考页。

提交贡献表示你同意按本项目 AGPL-3.0 许可证提供该贡献。
