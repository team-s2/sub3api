# ZCode OAuth 认证 + ultra 端点路由

日期：2026-11-05
范围：`team-s2/sub3api`（上游 `Wei-Shaw/sub2api` 的 fork）

## 目标

管理员可以用 Z.ai / BigModel 账号通过 **ZCode OAuth** 登录并直接生成账号；该账号的推理请求固定走
**ultra 端点**（`https://zcode.z.ai/api/v1/ultra/anthropic`），官方按 1.5x 配额口径计量。

按需求：**账号正常计费，只改路由**；relay（`zcode-plan` 中转）套餐本次不实现。

## 上游事实（来自 kc1 的 CPA 部署实测）

- kc1 `cli-proxy-api` 的 zcode 插件配置：`upstream_mode: "ultra"` + `bigmodel_api_key`，
  注释为「长期 CodingPlan key + ultra 路由 = 官方 1.5x 额度（扣减≈0.67x 直连）」。
- 面板 `static/management.html` 两档：`relay`（OAuth JWT → `/api/v1/zcode-plan/anthropic`）、
  `ultra`（CodingPlan key → `/api/v1/ultra/anthropic`）。
- 直接探测结论：
  - `POST /api/v1/ultra/anthropic/v1/messages` + CodingPlan key（`Authorization: Bearer`）→ **200**，返回 Anthropic Messages
  - 同一 key 打 `/api/v1/zcode-plan/anthropic` → **401**（该路由只认 OAuth JWT）
  - 不校验 `User-Agent`；`x-api-key` 单独使用曾返回异常响应，故统一走 Bearer
- OAuth 协议（对照 `omc-zcode-oauth` 源码 + 插件符号）：
  1. `POST https://zcode.z.ai/api/v1/oauth/cli/init`，`Authorization: Bearer <32B 随机 hex>`，
     头 `X-Device-Mid` 为该 state 派生的 UUIDv4，body `{"provider":"zai|bigmodel"}`
     → `data.flow_id / authorize_url / poll_interval_sec / expires_at`
  2. 浏览器完成授权（回调落在 zcode.z.ai，本地无需回调端口）
  3. `GET /api/v1/oauth/cli/poll/{flow_id}`（同 Bearer/设备号）
     → `data.status=ready` + `zai.access_token` / `bigmodel.access_token` + `token`(ZCode JWT) + `user`
  4. 推理凭据是 CodingPlan **项目 API key**（`id.secret`，长期有效），业务接口可复用/开通：
     `GET /api/biz/customer/getCustomerInfo` → `GET|POST /api/biz/v1/organization/{org}/projects/{proj}/api_keys`
     → `GET .../api_keys/copy/{id}` 取 `secretKey`；key 名复用 `zcode-api-key`（与 CPA/OMC 插件同名，避免重复生成）

## 实现

### 后端

| 文件 | 内容 |
| --- | --- |
| `backend/internal/service/zcode_oauth_client.go` | 协议客户端：`BeginLogin` / `PollLogin` / `ProvisionAPIKey`，含 state→device 派生、`{code,msg,data}` 信封解析、按需代理 |
| `backend/internal/service/zcode_oauth_service.go` | 会话存储（30min TTL）、站点列表、轮询编排（首次 ready 时开通 key 并缓存结果）、`BuildAccountCredentials` |
| `backend/internal/handler/admin/zcode_oauth_handler.go` | 管理端四个接口 + 创建账号（`platform=zhipu`、`type=apikey`） |
| `backend/internal/server/routes/admin.go` | `/admin/zcode/*` 路由注册 |
| `backend/internal/handler/handler.go`、`handler/wire.go`、`service/wire.go`、`cmd/server/wire_gen.go` | 依赖注入接线 |

账号形态（沿用现有国产供应商机制，无需改网关）：

```json
{
  "platform": "zhipu",
  "type": "apikey",
  "credentials": {
    "api_key": "<CodingPlan id.secret>",
    "base_url": "https://zcode.z.ai/api/v1/ultra/anthropic",
    "api_protocol": "anthropic",
    "account_mode": "coding",
    "api_base_urls": { "anthropic": "https://zcode.z.ai/api/v1/ultra/anthropic" },
    "upstream_mode": "ultra",
    "zcode_site": "bigmodel|zai",
    "zcode_jwt": "...", "zcode_device": "...", "zcode_user_id": "...",
    "zcode_email": "...", "zcode_plan": "...", "zcode_org": "...", "zcode_project": "..."
  },
  "extra": { "anthropic_apikey_auth_scheme": "authorization_bearer" }
}
```

`extra.anthropic_apikey_auth_scheme=authorization_bearer` 是既有开关（`anthropic_apikey_auth.go`），
让出站鉴权头从 `x-api-key` 切换为 `Authorization: Bearer`；计费仍按账号 `rate_multiplier`（默认 1.0）。

### 管理端接口

| 方法 | 路径 | 说明 |
| --- | --- | --- |
| GET | `/api/v1/admin/zcode/oauth/sites` | 返回 `[{id,name,description}]`（zai / bigmodel） |
| POST | `/api/v1/admin/zcode/oauth/auth-url` | 入参 `{site, proxy_id?}`；返回 `{auth_url,state,site,expires_at}` |
| POST | `/api/v1/admin/zcode/oauth/poll` | 入参 `{state}`；返回 `{status: pending|ready|declined, email, name, user_id, plan, team, has_api_key, masked_api_key, warning, expires_at}` |
| POST | `/api/v1/admin/zcode/oauth/accounts` | 入参 `{state, name?, api_key?, proxy_id?, concurrency?, priority?, group_ids?}` → 创建账号 |

`api_key` 留空则使用 OAuth 期间自动开通的 key；自动开通失败时 `poll` 返回 `warning`，
弹窗提示手工粘贴 key。

### 前端

| 文件 | 内容 |
| --- | --- |
| `frontend/src/api/admin/zcode.ts` | 四个接口封装，注册进 `src/api/admin/index.ts` |
| `frontend/src/components/account/ZCodeOAuthModal.vue` | 登录弹窗：站点选择 → 打开授权页 + 自动轮询 → 名称/并发/Key/代理/分组 → 创建；复用 `BaseDialog` + 既有 `btn/input/input-label` 样式与 `Icon` |
| `frontend/src/views/admin/AccountsView.vue` | 「更多」菜单新增「ZCode OAuth 登录」入口，创建成功后刷新列表并提示 |
| `frontend/src/i18n/locales/{zh,en}/admin/accounts.ts` | `accounts.zcode.*` 文案（中英双语，i18n 完整性检查通过） |

## 验证

kc1 上以 docker 起了一套隔离测试实例（`/home/colemak/sub3api-test`，postgres:15 + redis:7 + `sub2api:zcode-test`，端口 48080）：

1. `go build ./...` 通过；`gofmt` 干净。
2. 前端 `pnpm typecheck`、`pnpm check:i18n`、`pnpm build` 通过（dist 内嵌进二进制）。
3. 接口实测：
   - `GET /admin/zcode/oauth/sites` → 200，返回两个门户
   - `POST /admin/zcode/oauth/auth-url` → 200，返回真实授权链接
     `https://bigmodel.cn/login?appId=zcode&redirect=.../cli/callback/bigmodel&state=...`
   - `POST /admin/zcode/oauth/poll` → 200 `status=pending`（无浏览器授权属预期）
   - 手工建 ultra 账号 + `POST /admin/accounts/1/test` → 流式返回真实内容，末尾
     `{"type":"test_complete","success":true}`，证明 base_url、Bearer 鉴权、协议转换全链路可用。
4. 测试账号与实例为一次性验证，账号已删除（测试实例容器仍在 kc1，可按需 `docker compose down -v`）。

## 遗留 / 后续

- relay（`zcode-plan` 中转，JWT 鉴权）与套餐切换未实现；`zcode_jwt`/`zcode_device` 已落盘，后续接入无需再改流程。
- OAuth 会话存在进程内存（单实例可用）；多实例部署需换 Redis 共享。
- 自动开通 key 走的是上游业务接口（`api_keys` + `copy`），若上游改版会失败，此时弹窗可手工填 key。
