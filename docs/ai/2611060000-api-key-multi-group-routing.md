# API Key 多分组按模型路由

Key 保留主分组 `group_id`，新增有序的 `extra_group_ids`（JSONB，最多 16 个）。普通单分组 Key 的请求路径不变，也不读请求体。

## 路由

`apiKeyAuth` / Gemini 鉴权在分组校验和订阅加载之前调用 `routeAPIKeyByRequestModel`，从 URL 参数、请求体或 `?model=` 取模型，再由 `APIKeyService.RouteAPIKeyByModel` 选组，并生成绑定该组的 Key 浅拷贝。下游（平台分发、白名单、订阅、计费、调度、用量）看到的都是普通单分组 Key，无需感知多分组。

候选分组：启用中，且用户可用（专属授权或有效订阅）。匹配度：

1. 已配置：分组开了模型白名单且放行；Composite 显式路由或账号归属命中；账号 `model_mapping` 显式声明该模型。
2. 名称识别：`DetectModelPlatform` 的结果等于分组平台（antigravity 同时接受 Claude、Gemini）。

已配置优先于名称识别，同级按 Key 内顺序（主分组在最前）。没有匹配时留在主分组，由其返回原有错误。开了白名单但不放行的分组直接排除。

## 边界

- 无模型的请求（`/v1/usage`、Responses WebSocket 升级等）走主分组。
- `GET /v1/models` 返回各分组模型的并集；带 `client_version` 的 Codex 清单走主分组。
- 只选一次组：选中分组内账号全部失败时不跨 Key 的分组重试，分组自身的 fallback 配置仍生效。
- 附加分组数据有 30 秒进程内缓存（`routeGroupCache`），分组变更时本实例立即失效，其他实例等待过期。
- 删除分组不清理 `extra_group_ids`，运行时跳过不存在的分组，前端保存时只提交仍可选的分组。

与上游 Composite 分组的区别：Composite 要把账号直接挂到一个分组上，由管理员配置；这里用户直接在 Key 上组合已有分组，各分组的订阅、倍率、账号池保持独立。
