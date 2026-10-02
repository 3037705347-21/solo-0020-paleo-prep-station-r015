# 古生物修复制备台

古生物修复制备台是一个使用 Go 编写的后端基线，用于记录化石标本、审批修复方案、回填制备进度，并在全部工序完成后办理研究借阅放行。

该服务只使用进程内仓储，便于在本机重复启动和检查。它不依赖数据库、远端接口或其他在线资源。

## 目录

- `cmd/paleo-prep-station`：HTTP 服务入口和监听参数。
- `cmd/workflowcheck`：通过真实 HTTP 处理器执行四条业务链路的本地验证命令。
- `internal/httpapi`：路由、请求解码、状态码映射和 JSON 输出。
- `internal/station`：跨领域对象的编排、事务边界和业务流程。
- `internal/domain`：标本、方案、进度和放行记录的状态与不变量。
- `internal/repository`：互斥保护的内存仓储和原子提交。
- `internal/contracts`：稳定错误模型和公共请求结构。

## 构建与运行

```bash
go build ./...
go run ./cmd/paleo-prep-station --addr 127.0.0.1:18133
```

服务启动后，`GET /healthz` 返回就绪状态。

## 主要接口

- `GET /specimens` 与 `POST /specimens`
- `GET /specimens/{id}`
- `POST /specimens/{id}/plans`
- `GET /plans/{id}`
- `POST /plans/{id}/approval`
- `POST /plans/{id}/progress`
- `POST /specimens/{id}/release`

## 乐观版本条件

标本与方案都带有单调递增的 `version`。写入请求（创建方案、审批、进度、放行）可以在 JSON 中携带可选的 `expected_version`，值为调用方此前通过 `GET /specimens/{id}` 或 `GET /plans/{id}` 读到的版本：

- 不携带 `expected_version` 时行为与既有契约完全一致。
- 携带时必须与服务端最新版本一致，否则返回 `409` 与错误码 `version_conflict`；小于 1 的值返回 `400 invalid_expected_version`。
- 创建方案与放行校验标本版本；审批与进度校验方案版本。
- 成功响应回显 `expected_version` 以及写后的 `plan_version`、`specimen_version`，可与标本详情、方案快照（`GET /plans/{id}` 的 `specimen.version`）交叉核对，确认一次成功提交确实基于自己看到的事实。
- 开放方案互斥、进度单调、终态不可回退和放行唯一性在携带或不携带版本时都同样生效。

## 本地验证

每条业务链路都可以独立执行：

```bash
go run ./cmd/workflowcheck --workflow register-specimen
go run ./cmd/workflowcheck --workflow approve-prep-plan
go run ./cmd/workflowcheck --workflow log-preparation
go run ./cmd/workflowcheck --workflow release-for-study
go run ./cmd/workflowcheck --workflow optimistic-version-conflict
```

这些检查会启动临时 HTTP 处理器并调用真实公共路由；最后一条覆盖携带版本的成功提交、陈旧版本冲突、非法版本以及跨视图版本事实核对。

## 环境变量

项目不需要环境变量，也没有外部服务依赖。

## 测试边界

本基线按要求暂不生成单元测试、测试夹具或端到端测试文件。后续任务阶段会补充红绿验证测试，届时再替换或扩展当前的生产级链路检查。
