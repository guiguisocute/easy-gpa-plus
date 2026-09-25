# 演示站

复用前端页面与虚构班级数据，在浏览器本地保存交互，不连接实际后端，也不发送邮件。

在 `frontend/` 运行 `npm ci` 和 `npm run mock`；生产构建使用 `npm run mock:build`，包含演示站对应源码下载。部署到 Cloudflare Pages 时通过环境变量提供 `MOCK_PAGES_PROJECT`、`CLOUDFLARE_ACCOUNT_ID` 和 `CLOUDFLARE_API_TOKEN`，再运行 `npm run mock:deploy`，项目名和凭据均不写入代码。

详见文档站的[演示站](https://easygpa.guiguisocute.com/development/demo)。
