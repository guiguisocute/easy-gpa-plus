# 日常入口(§20.2 混合模式:依赖进容器,Go/Vite 跑宿主)
.PHONY: dev full down api api-plain web test e2e e2e-registration e2e-knowledge e2e-playwright e2e-all e2e-clean e2e-knowledge-real seed lint fmt sqlc migrate-up migrate-down garage

dev: ## 起依赖三件套(postgres/redis/garage);桶与密钥由 garage 自动创建
	docker compose up -d
	@echo "依赖已就绪。另开两个终端: 'make api' 与 'make web'"

full: ## 依赖、迁移、API 与全部非 AI Worker 一起启动
	docker compose --profile full up -d --build

down:
	docker compose down

api: ## Go API 热重载(需 go install github.com/air-verse/air@latest)
	cd backend && air

api-plain: ## 不装 air 时的等价入口:改代码后自己重启
	cd backend && go run ./cmd/zongce api

web:
	cd frontend && npm run dev

test:
	cd backend && go test ./...

e2e: ## 隔离本地容器中的真 HTTP 业务冒烟
	node e2e/run.mjs smoke

e2e-registration: ## 隔离本地容器中的名单注册专项
	node e2e/run.mjs registration

e2e-knowledge: ## 隔离本地容器中的知识 Agent 全栈 + 合成模型
	node e2e/run.mjs knowledge

e2e-playwright: ## 隔离本地容器中的真实 Chromium 用例
	node e2e/run.mjs playwright

e2e-all: ## 依次运行全部本地 E2E 层
	node e2e/run.mjs all

e2e-clean: ## 删除 easygpa-plus-e2e 缓存卷（不影响 easygpa-plus-dev 或生产）
	node e2e/run.mjs clean

e2e-knowledge-real: ## 真实材料评测:还需 ALLOW_RAW_THIRD_PARTY=1 与命令行双确认
	cd backend && go run ./cmd/zongce eval:knowledge --class-id $(KNOWLEDGE_EVAL_CLASS_ID) --allow-raw-third-party

seed: ## 建三个固定业务账号(班管 DEV001 / 小组 DEV002 / 学生 DEV003)
	node scripts/seed-dev-accounts.mjs

lint:
	cd backend && go vet ./... && golangci-lint run

fmt:
	cd backend && gofmt -w .

sqlc: ## 由 db/queries/*.sql 生成类型安全代码(需 sqlc CLI)
	cd backend && sqlc generate

migrate-up:
	cd backend && go run ./cmd/zongce migrate

migrate-down:
	cd backend && go run ./cmd/zongce migrate:down

garage: ## 进 Garage CLI 看状态(桶/密钥管理: /garage bucket|key --help)
	docker compose exec garage /garage status
