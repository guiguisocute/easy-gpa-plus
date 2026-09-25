# 部署

本目录存放 Compose 使用的配置与脚本：

| 路径 | 内容 |
| --- | --- |
| `development/` | 开发与 E2E 容器的构建、热重载和测试脚本 |
| `garage/garage.toml` | Garage 开发配置；其中的密钥只用于开发，正式部署必须替换 |
| `nginx/` | 前端容器的 Nginx 配置与资源发布脚本 |
| `postgres/init-roles.sh` | 数据库首次初始化时创建业务与运维角色 |

部署步骤、正式部署清单与升级方式见文档站：

- [自托管部署](https://easygpa.guiguisocute.com/deploy/self-hosting)
- [备份与恢复](https://easygpa.guiguisocute.com/deploy/backup)
- [按组件发布与升级](https://easygpa.guiguisocute.com/deploy/releases)
- [环境变量](https://easygpa.guiguisocute.com/reference/environment)
