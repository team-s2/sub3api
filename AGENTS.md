本仓库是上游 `Wei-Shaw/sub2api` 的 fork。

## 当前 fork 变更

- 允许通过 GitHub OAuth 组织白名单进行登录
- 只保留 CI 与 docker build 相关 Action

## 例行维护流程

- 检查上游是否有更新的 release tag
- 将本 fork rebase 到最新的上游 tag
    - 保留本 fork 的增量变更
    - 当上游实现了本 fork 的增量变更时，直接使用上游的实现
- 运行相关的后端/前端测试或构建
- 提交并 force push

## 相关开发约束

- 注意保持代码可读性、易维护性，维持良好代码结构
- commit msg 请使用 angular 规范编写
- 不要添加新单元测试，可以在开发过程中编写单元测试来测试用，但测试完成后需要删掉
- AI 生成的文档和开发日志（如有）应当统一放置在 `docs/ai` 目录下，按日期和主题命名，形如 `YYMMDDHHmm-<主题>.md`
