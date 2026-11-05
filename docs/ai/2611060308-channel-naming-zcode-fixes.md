# 渠道管理：命名统一为「渠道」+ ZCode OAuth 缺陷修复

## 背景

渠道管理页（/admin/channels）卡片化后，弹窗标题仍显示「编辑账号」，且页内大量文案沿用「账号」。
另外修复三个 ZCode OAuth 相关缺陷：OAuth 建号仍要求 API Key（上一提交已修）、编辑面板切换协议/账号类型后 Base_URL 丢失 ultra、卡片徽标显示 "Key" 而非 OAuth。

## 改动

### 1. 命名统一：账号 → 渠道

- `i18n/locales/zh/admin/accounts.ts`：全部「账号」改为「渠道」（title/editAccount/deleteAccount/searchAccounts/deleteConfirm/CRS 同步/导入导出等约 220 处）。
- `i18n/locales/en/admin/accounts.ts`：title → 'Channel Management'、editAccount → 'Edit Channel'、deleteAccount → 'Delete Channel'、searchAccounts → 'Search channels...'、deleteConfirm/duplicateAccount 同步。
- `GrokModelDiagnosticsPanel.spec.ts`：中文文案断言同步。

### 2. 删除渠道入口

- 卡片右上角垃圾桶图标（已有，`account-card-delete`）。
- 「⋯」菜单底部新增红色「删除渠道」项（分隔线隔开），emit `delete` → `handleDelete`。

### 3. ZCode OAuth：徽标显示 OAuth

- `PlatformTypeBadge.vue` 新增 `zcodeOAuth` prop；为 true 时按 `oauth` 渲染（OAuth 钥匙图标 + OAuth 标签）。
- `AccountCard.vue` 对 ZCode 账号（有 `zcode_user_id`）传 `:zcode-oauth="isZCode"`。

### 4. ZCode OAuth：编辑面板 Base_URL 回落 ultra

- `EditAccountModal.vue` 新增 `isZCodeOAuthAccount`（zhipu 且 credentials 有 `zcode_user_id` 或 `upstream_mode=ultra`）、`zcodeUltraBaseUrl`（按 `zcode_site` 选 `ultra`/`ultra-zai` 端点）、`resolveDefaultBaseUrl`。
- 协议/账号类型 watcher 及初始化回填均改走 `resolveDefaultBaseUrl`：ZCode 账号 anthropic 协议下切协议/模式后 base_url 始终回落 ultra 端点，不再丢成官方通用端点。

### 5. 卡片清理

- `AccountCard.vue` 删除用量行（`AccountUsageCell`）及 usage 相关 props/emits。
- `AccountActionMenu.vue` 删除「重置卡」菜单项（入口统一收敛到卡片「额度详情」按钮）。
- `AccountsView.vue` 随之删除批量化用量拉取簇（usageBatch*/queueBatchedUsage/flushQueuedUsageBatch 等）。

## 验证

- kc1 全量：typecheck / lint:check / check:i18n / test:run（2991 passed）/ build 全绿。
