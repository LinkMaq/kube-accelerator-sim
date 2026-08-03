# 保持中英文档同步

在线文档直接从仓库 `docs/` 下的权威 Markdown 构建。不要另建一份网站专用产品文档。

## 文档是功能的一部分

每项面向用户的行为变更都必须在同一个 PR 更新相关文档，包括：

- CLI 命令、参数、回执、错误或目标选择行为；
- Scenario、Vendor Profile、Resource Contract 或 Fidelity Mode 语义；
- 运行时安装、权限、所有权、升级或清理；
- Kubernetes 支持版本或验证声明；
- 内置档案、型号、资源信号或示例；
- 发布的二进制、镜像、Helm Chart 和验证过程。

英文根目录文档是规范和 ADR 的权威来源。面向操作者的页面必须同时维护英文
`docs/operators/` 与中文 `docs/zh/operators/`。新增公开页面时，两种语言的导航都要
更新；若某项深层设计记录暂时只有英文，中文导航必须明确标注“英文”，并提供中文导读。

领域语言或架构决策变化时还要更新 `CONTEXT.md` 或 ADR。

## 本地工作流

```sh
npm ci
npm run docs:build
```

本地热更新：

```sh
npm run docs:dev
```

英文地址位于 `/kube-accelerator-sim/`，中文地址位于
`/kube-accelerator-sim/zh/`。生产构建会同时检查两种语言的页面、链接和本地搜索。

## PR 门禁

CI 会把 PR 与 base commit 比较。修改 `api/`、`cmd/`、`internal/`、`profiles/`、
`examples/`、`charts/` 或 `config/crd/` 等产品路径时，必须包含权威 Markdown 更新。
测试、工作流和 Agent 专用变更本身不会触发产品文档门禁。

门禁通过只表示提交包含文档；评审者仍要确认英文与中文操作说明准确、互相一致。
变更合入 `main` 后，GitHub Pages 会自动重建和部署整个双语站点。
