<div align="center">

<img src="frontend/public/favicon.svg" width="72" height="72" alt="EasyGPA Plus">

# EasyGPA Plus

### 让综合测评，更清楚，也更轻松。

材料提交 · 独立审核 · 班级共治 · 实时成绩

**开源 · 自托管 · 多班级 · 可配置评分规则**

[**文档站**](https://easygpa.guiguisocute.com) · [在线体验](https://easygpamock.guiguisocute.com) · [自托管](https://easygpa.guiguisocute.com/deploy/self-hosting) · [参与开发](CONTRIBUTING.md)

</div>

从材料、认定到申诉与结算，把每一分的来处说清楚。
学生随时查看成绩和处理进度，核对后一键确认；班级按业务完成情况结算。

- **学生**：提交佐证、追踪进度、查看成绩、提出申诉。
- **综测小组**：独立审核、复评与异议处理。
- **班级管理员**：评分方案、名单、分发、仲裁与报表。
- **平台运维**：班级隔离、对象存储、通知与备份；AI 辅助按需配置。
- **你的 Agent**：通过 MCP 读取资料、上传佐证、审核与统计，权限由账号掌握。

> [!NOTE]
> 班级首次配置时可选**普通模式**（推荐）或**共治模式**。共治模式为测试版，尚未经过真实班级的生产验证，正式计分请选择普通模式。

## 在本机启动

安装 Docker Compose 后：

```sh
cp .env.example .env.development
docker compose --env-file .env.development -f compose.dev.yaml up -d --build
```

打开 **http://localhost:35173**，使用 `.env.development` 中的运维账号登录，创建班级并导入名单。Windows 也可运行 `./scripts/dev.ps1 up`。

## 文档

| | |
| --- | --- |
| 使用指南 | [快速开始](https://easygpa.guiguisocute.com/guide/getting-started) · [学生](https://easygpa.guiguisocute.com/guide/student) · [综测小组](https://easygpa.guiguisocute.com/guide/reviewer) · [班级管理员](https://easygpa.guiguisocute.com/guide/class-admin) · [评分方案](https://easygpa.guiguisocute.com/guide/scoring-scheme) |
| 部署运维 | [自托管部署](https://easygpa.guiguisocute.com/deploy/self-hosting) · [邮件](https://easygpa.guiguisocute.com/deploy/mail) · [AI](https://easygpa.guiguisocute.com/deploy/ai) · [备份](https://easygpa.guiguisocute.com/deploy/backup) · [升级](https://easygpa.guiguisocute.com/deploy/releases) |
| 参考 | [环境变量](https://easygpa.guiguisocute.com/reference/environment) · [命令与服务](https://easygpa.guiguisocute.com/reference/commands) · [连接 Agent](https://easygpa.guiguisocute.com/guide/agent) |

文档源码在 [easy-gpa-plus-doc](https://github.com/guiguisocute/easy-gpa-plus-doc)，欢迎一起改进。

React · TypeScript · Go · PostgreSQL · Redis · S3

源自 EasyGPA，面向可复用的开源综合测评场景，以 [AGPL-3.0](LICENSE) 发布。
