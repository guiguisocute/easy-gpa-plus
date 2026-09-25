// zongce:全部后端的唯一入口,同一二进制以子命令区分进程角色(设计文档 §10)。
package main

import (
	"context"
	"fmt"
	"os"

	"easygpa/backend/db/migrations"
	"easygpa/backend/internal/aievaluation"
	"easygpa/backend/internal/api"
	"easygpa/backend/internal/config"
	"easygpa/backend/internal/knowledgeevaluation"
	"easygpa/backend/internal/worker"
)

const usage = `用法: zongce <子命令>

  api               HTTP API 服务
  worker:dispatch   分发 Worker(M2)
  worker:notify     通知 Worker(M2)
  worker:export     导出 Worker(M3)
	worker:maintenance 定时提醒、自动封存与导出清理
	worker:backup      每日数据库与对象备份、恢复演练
  worker:ai         AI 整理 Worker(M2.5,设计文档 §19)
  worker:agent      班级知识转换与对话 Agent Worker
  eval:ai           离线模型评测（不会连接业务数据库）
  eval:knowledge    真实班级知识 Agent 评测（双重外发确认）
  migrate            应用全部待执行数据库迁移
  migrate:down       回退最近一个数据库迁移
  version           版本信息
`

// 构建时经 -ldflags "-X main.version=..." 注入
var version = "dev"

func main() {
	if len(os.Args) < 2 {
		fmt.Fprint(os.Stderr, usage)
		os.Exit(2)
	}

	if os.Args[1] == "version" {
		fmt.Println("zongce", version)
		return
	}

	cfg, err := config.LoadFor(os.Args[1])
	if err != nil {
		fmt.Fprintln(os.Stderr, "配置错误:", err)
		os.Exit(1)
	}

	switch os.Args[1] {
	case "eval:ai":
		err = aievaluation.Run(context.Background(), cfg, os.Args[2:])
	case "eval:knowledge":
		err = knowledgeevaluation.Run(context.Background(), cfg, os.Args[2:])
	case "api":
		err = api.Run(cfg)
	case "migrate", "migrate:down":
		databaseURL := cfg.MigrationsDatabaseURL
		if databaseURL == "" {
			databaseURL = cfg.DatabaseURL
		}
		if databaseURL == "" {
			err = fmt.Errorf("MIGRATIONS_DATABASE_URL is required")
			break
		}
		pool, openErr := migrations.OpenPool(context.Background(), databaseURL)
		if openErr != nil {
			err = openErr
			break
		}
		defer pool.Close()
		if os.Args[1] == "migrate" {
			err = migrations.Up(context.Background(), pool)
		} else {
			err = migrations.DownOne(context.Background(), pool)
		}
	case "worker:dispatch":
		err = worker.Run(cfg, "dispatch")
	case "worker:notify":
		err = worker.Run(cfg, "notify")
	case "worker:export":
		err = worker.Run(cfg, "export")
	case "worker:maintenance":
		err = worker.Run(cfg, "maintenance")
	case "worker:backup":
		err = worker.Run(cfg, "backup")
	case "worker:ai":
		err = worker.Run(cfg, "ai")
	case "worker:agent":
		err = worker.Run(cfg, "agent")
	default:
		fmt.Fprint(os.Stderr, usage)
		os.Exit(2)
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
