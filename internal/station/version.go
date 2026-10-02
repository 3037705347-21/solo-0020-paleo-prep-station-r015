package station

import (
	"fmt"

	"example.com/paleo-prep-station/internal/contracts"
)

// requireVersion 在仓储事务内基于最新状态执行乐观版本条件。
//
// expected 为 nil 表示调用方沿用既有契约、未携带已读版本，此时跳过检查，
// 保持向后兼容；非 nil 时必须是正整数，且与服务端最新版本完全一致，
// 否则分别得到明确的 400 输入错误或 409 version_conflict 冲突，
// 绝不允许陈旧版本静默覆盖他人已经推进的事实。
func requireVersion(resource string, expected *int, actual int) error {
	if expected == nil {
		return nil
	}
	if *expected < 1 {
		return contracts.Invalid("invalid_expected_version", "期望版本必须是不小于 1 的正整数")
	}
	if *expected != actual {
		return contracts.Conflict(
			"version_conflict",
			fmt.Sprintf("%s版本已被推进：提交依据的版本 %d 已过期，当前版本为 %d", resource, *expected, actual),
		)
	}
	return nil
}
