# EasyGPA Plus · 前端

React 19 + Vite 8 + TypeScript。数据全部来自后端 `/api/v1`。

```bash
npm ci
npm run dev        # http://localhost:5173，/api 与 /mcp 转发到 VITE_API_PROXY
npm run lint
npm test
npm run build
npm run mock       # 演示站，http://localhost:4173
```

通常通过仓库根目录的 `compose.dev.yaml` 或 `scripts/dev.ps1 up` 连同后端一起启动。

目录结构与设计取舍见文档站的[架构](https://easygpa.guiguisocute.com/development/architecture#前端)，演示站见[演示站](https://easygpa.guiguisocute.com/development/demo)。
