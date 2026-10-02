# 古生物修复制备台项目说明

## 项目目标

本项目为古生物标本制备组提供一个可运行的后端基线，用于管理标本登记、修复方案审批、制备进度和研究成果放行。系统关注的是标本状态的真实流转、同一方案下的连续记录以及放行前的完整性约束。

## 使用方

- 登记员：录入标本目录号、野外编号、基质类型和临时保护信息。
- 制备负责人：为标本拟定修复目标和方法，并作出审批决定。
- 制备人员：按阶段提交基质去除比例、操作说明和记录人。
- 标本管理员：核对已完成制备的标本并办理研究借阅放行。

## 核心实体

- 标本：保存目录号、野外标签、分类提示、基质类型、夹克备注、状态和版本号。
- 修复方案：绑定一份标本，保存目标、方法、黏结材料、目标暴露比例、审批信息和状态。
- 进度记录：属于一个修复方案，按序号保存阶段名称、完成比例、说明和记录人。
- 放行记录：绑定已稳定的标本，保存借阅目的、接收方和批准人。

## 状态与规则

标本状态依次经过 `registered`、`planned`、`preparing`、`stabilized` 和 `available`。同一目录号不能重复登记。一个标本同时最多存在一个未结束的修复方案。

修复方案状态依次经过 `draft`、`approved`、`active` 和 `completed`，草稿也可以进入 `cancelled`。只有草稿能够审批；只有已审批方案能够开始记录进度。

进度比例必须位于零到一百之间，且新的比例不得低于上一条记录。比例达到一百时形成最后一条记录，方案进入 `completed`，标本进入 `stabilized`。完成后的方案不能再写入进度。

放行只允许发生在标本进入 `stabilized` 且不存在未结束方案时。放行成功后标本进入 `available`，并永久保留一条放行记录。

### 乐观版本条件

标本与方案各自维护单调递增的版本号，每次成功写入（包括向进行中方案追加进度）都会推进对应版本。制备人员先通过标本详情或方案快照读取版本，再在创建方案、审批、进度或放行请求中携带可选的 `expected_version`：

- 未携带 `expected_version` 的请求沿用既有契约，不参与版本条件，保持兼容。
- 携带时，该版本必须与仓储事务内读到的服务端最新版本完全一致，写入才允许继续；陈旧版本得到明确的 `409 version_conflict`，不会静默覆盖他人已经推进的事实。
- `expected_version` 必须是不小于 1 的正整数，否则返回 `400 invalid_expected_version`。
- 版本条件在互斥、单调、终态与唯一性等业务条件之前判定，因此陈旧读者不会把别人的推进误判成普通业务冲突。
- 每次成功写入的响应都回显提交依据的 `expected_version` 以及写后的方案、标本版本，可与 `GET /specimens/{id}` 和 `GET /plans/{id}`（快照内含标本版本）交叉核对同一次提交的版本事实。

版本条件的保护对象分别是：创建方案与放行校验标本版本，审批与进度校验方案版本。

## 业务链路

### 登记标本

登记员通过 `POST /specimens` 提交目录号、野外标签、分类提示、基质类型和夹克备注。服务校验必填字段和目录号唯一性，然后创建 `registered` 标本。`GET /specimens/{id}` 可读取完整快照。

### 审批修复方案

负责人通过 `POST /specimens/{id}/plans` 创建草稿方案，再通过 `POST /plans/{id}/approval` 写入批准人和审批时间。创建请求可携带标本的 `expected_version`，审批请求可携带方案的 `expected_version`；每一步都检查标本状态和未结束方案约束，审批成功后标本进入 `preparing`。

### 记录制备进度

制备人员通过 `POST /plans/{id}/progress` 追加阶段记录，并可携带方案的 `expected_version`。第一条有效进度会把方案从 `approved` 推进到 `active`；比例达到一百时方案和标本在同一仓储事务中进入终态，防止只更新其中一方。每次成功追加都会推进方案版本，因此先后读取同一版本的两位制备人只有先提交者能够成功，后提交者必须重新读取后再试。

### 办理研究放行

标本管理员通过 `POST /specimens/{id}/release` 提交借阅目的、接收方、批准人和可选的标本 `expected_version`。服务再次检查标本状态和方案闭合状态，成功后写入不可覆盖的放行记录并把标本置为 `available`。

## 模块边界

- `cmd/paleo-prep-station` 负责进程参数、监听和关闭。
- `cmd/workflowcheck` 负责通过公共 HTTP 路由执行独立的本地链路检查。
- `internal/httpapi` 负责传输层解码、输出和错误状态映射。
- `internal/station` 负责加载对象、执行业务条件并提交原子变更。
- `internal/domain` 负责实体构造、状态转换和不变量。
- `internal/repository` 负责互斥保护和事务副本提交。
- `internal/contracts` 负责公共请求和稳定错误类型。

## 接口约定

- `POST /specimens` 成功返回 `201` 和标本对象。
- `POST /specimens/{id}/plans` 成功返回 `201` 和方案对象，并附带 `expected_version` 与写后 `specimen_version`。
- `POST /plans/{id}/approval` 成功返回 `200` 和更新后的方案，并附带 `expected_version`、`plan_version` 与 `specimen_version`。
- `POST /plans/{id}/progress` 成功返回 `201` 和进度记录，并附带 `expected_version`、`plan_version` 与 `specimen_version`。
- `POST /specimens/{id}/release` 成功返回 `201` 和放行记录，并附带 `expected_version` 与写后 `specimen_version`。
- `GET /plans/{id}` 返回方案、带版本的标本摘要和有序进度列表。
- 输入不合法返回 `400`（包括非法的 `expected_version`），对象不存在返回 `404`，状态冲突、重复标识或携带的版本已过期返回 `409`（陈旧版本固定为 `version_conflict`）。

## 验证计划

构建命令为 `go build ./...`。当前基线不生成自动化测试文件，使用五条生产级 HTTP 链路检查覆盖主要成功路径、关键冲突边界和乐观版本条件。后续任务阶段再补充红绿验证测试，并保留这些检查作为端到端烟雾路径。
