# Homepage

自托管个人主页：分页书签网格，类似手机桌面启动器。单一 Go 二进制 + 嵌入式静态前端（无前端构建链）+ SQLite，Docker 一键部署。

## Features

- 分页书签网格：行列可配置（行 3–5，列 4–6），箭头 / 页点 / 键盘 ←→ / 滚轮 / 触屏滑动翻页
- 书签增删改、拖拽排序（含跨页拖拽），顺序持久化
- 自动抓取站点 favicon 并缓存到本地，带 SSRF 防护
- 书签与设置 JSON 导入 / 导出，方便备份与迁移
- 纯 Go SQLite（modernc.org/sqlite），`CGO_ENABLED=0`，distroless 镜像、非 root 运行

## Quick start (Docker)

```bash
docker compose up -d --build
```

打开 <http://localhost:8080>。宿主端口可用 `HOMEPAGE_PORT` 覆盖：

```bash
HOMEPAGE_PORT=9000 docker compose up -d
```

数据默认保存在 named volume `homepage-data` 中——Docker 首次挂载时会用镜像内 `/data` 的属主（uid 65532，nonroot）初始化卷，开箱即用、无权限问题。

如果更想用 bind mount（便于直接查看文件），把 compose 里的卷改成 `./data:/data`，并先创建好属主目录：

```bash
mkdir -p data && sudo chown 65532:65532 data
```

### Health check

Dockerfile 和 compose 都**没有**配置 HEALTHCHECK：distroless 镜像里没有 shell/curl 可执行探测命令。如需健康检查，请在容器外探测 `GET /api/health`（返回 `{"ok":true}`），例如反向代理或外部监控。

## Configuration

均通过环境变量配置；compose 用户可直接在 `docker-compose.yml` 或 shell env 中设置。

| 变量 | 默认值 | 说明 |
|---|---|---|
| `HOMEPAGE_ADDR` | `:8080` | HTTP 监听地址（容器内固定为 8080，改宿主端口用 `HOMEPAGE_PORT`） |
| `HOMEPAGE_DATA_DIR` | `./data`（镜像内 `/data`） | SQLite 数据库与图标缓存目录 |
| `HOMEPAGE_FAVICON_ALLOW_PRIVATE` | `false` | 允许从内网 / 回环 IP 抓取图标（内网站点需要，见安全说明） |
| `HOMEPAGE_FAVICON_FALLBACK` | 空（关闭） | 第三方图标兜底服务：`google` 或 `duckduckgo` |
| `HOMEPAGE_FAVICON_PROXY` | 空（直连） | 图标抓取的可选代理 URL（`http`/`https`/`socks5`/`socks5h`）；永不使用环境代理——走代理时代理自行解析目标，SSRF 防护只能校验代理自身 IP |
| `HOMEPAGE_FAVICON_TIMEOUT` | `8s` | 图标抓取单请求超时 |
| `HOMEPAGE_PORT` | `8080` | 仅 compose 使用：映射到宿主的端口 |

## Local development

需要 Go 1.24：

```bash
go run ./cmd/homepage   # http://localhost:8080，数据写入 ./data
go test ./...           # 全部测试（基于 httptest，无需外部服务）
go build ./... && go vet ./...
```

## Data & backup

数据目录（容器内 `/data`）内容：

- `homepage.db` — SQLite 数据库（WAL 模式）
- `icons/` — favicon 缓存文件

备份方式：

- 应用内导出：页面 Settings → Export，或 `GET /api/export` 下载 JSON；恢复用 `POST /api/import?mode=append|replace`
- 整卷备份（compose 默认卷）：

```bash
docker run --rm -v homepage-data:/data -v "$PWD":/backup \
  alpine tar czf /backup/homepage-data.tgz -C /data .
```

## API

| method | path | 说明 |
|---|---|---|
| GET | `/api/health` | `{"ok":true}` |
| GET | `/api/bookmarks` | 按 `page, position`（页号 + 页内序号）列出全部书签 |
| POST | `/api/bookmarks` | `{title,url,page?,allow_duplicate?}` → 201；放入 `page`（默认 0）起第一个空位，图标异步抓取 |
| POST | `/api/bookmarks/move` | `{"id,"page,"index"}` → 200 全量列表；满页尾部顺移溢出，`page` 可为末尾新页 |
| PUT | `/api/bookmarks/{id}` | `{title,url}`；URL 变更会重新抓图标 |
| DELETE | `/api/bookmarks/{id}` | 204；该页空位收紧、空页移除 |
| POST | `/api/bookmarks/{id}/icon/refresh` | 重新抓取图标 |
| GET | `/api/icons/{id}` | 图标字节（缓存文件），无则 404 |
| GET | `/api/settings` | `{"grid":{"rows":4,"cols":6},"spacing":"normal"}` |
| PUT | `/api/settings` | 同上结构（部分更新）；grid 变更在同一事务内顺移溢出（缩小）/保持不变（放大） |
| GET | `/api/export` | `{"version":1,"settings":{...},"bookmarks":[{title,url,page,position},...]}` 下载 |
| POST | `/api/import?mode=append\|replace` | 同导出结构；replace 全带 `page` 按页复原（缺则按文件顺序平铺，先套用文件内 grid），append 逐项从第 0 页找空位；图标异步抓取 |

校验：title 1–200 字符（trim，空则取 host）；URL 须为绝对 `http`/`https` 且 ≤ 2048 字符。错误返回 `{"error":"..."}`，状态码 400/404/500。

## Project layout

```
cmd/homepage/main.go                     入口：config + DB + HTTP server + 优雅退出
internal/config/config.go                环境变量配置
internal/db/db.go                        打开 SQLite（WAL、外键、busy_timeout）
internal/db/migrations/0001_init.sql     内嵌迁移，按序应用
internal/store/{store.go,sqlite.go}      数据访问层：接口 + SQLite 实现
internal/favicon/                        图标抓取、解析、缓存、SSRF 防护
internal/api/                            HTTP handlers（Go 1.22+ ServeMux 模式路由）
web/                                     静态前端，go:embed 嵌入，无构建步骤
Dockerfile  docker-compose.yml  .dockerignore
docs/PLAN.md                             架构与接口的事实来源
```

## Adding a DB migration

在 `internal/db/migrations/` 下新增 `NNNN_name.sql`（编号递增，如 `0002_add_tags.sql`）。迁移文件经 `go:embed` 嵌入，启动时按序应用并记录在 `schema_migrations` 表中，可安全重复启动。无需改动迁移执行器代码。

## Security notes

- **SSRF 防护**：favicon 抓取在 DNS 解析之后校验目标 IP，拒绝 loopback / private / link-local / CGNAT / multicast / unspecified 地址，重定向与 DNS rebinding 均覆盖；仅允许 http/https。
- **`HOMEPAGE_FAVICON_ALLOW_PRIVATE`**：仅当你的书签指向内网站点（NAS、路由器等）需要抓图标时才设为 `true`——它会让 SSRF 防护放行私网地址，公网部署请保持 `false`。
- **无内置认证**：服务本身没有登录机制，请只在可信网络中监听，或置于带认证的反向代理之后。
- **镜像**：distroless 静态镜像，无 shell/包管理器，以 uid 65532（nonroot）运行，容器内不可 exec。
