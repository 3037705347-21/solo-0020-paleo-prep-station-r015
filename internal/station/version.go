package station

import (
	"example.com/paleo-prep-station/internal/contracts"
	"example.com/paleo-prep-station/internal/domain"
)

// 写请求可以携带调用方已读取的版本作为成功条件：版本落后于服务端当前状态时
// 返回明确冲突，未携带版本的请求保持既有行为。校验必须在提交变更的同一事务内执行。
func requireSpecimenVersion(expected *int, specimen domain.Specimen) error {
	if err := validateExpectedVersion(expected); err != nil {
		return err
	}
	if expected != nil && *expected != specimen.Version {
		return contracts.Conflict("specimen_version_conflict", "标本版本已被他人推进，请刷新后重试")
	}
	return nil
}

func requirePlanVersion(expected *int, plan domain.PrepPlan) error {
	if err := validateExpectedVersion(expected); err != nil {
		return err
	}
	if expected != nil && *expected != plan.Version {
		return contracts.Conflict("plan_version_conflict", "方案版本已被他人推进，请刷新后重试")
	}
	return nil
}

func validateExpectedVersion(expected *int) error {
	if expected != nil && *expected < 1 {
		return contracts.Invalid("invalid_expected_version", "期望版本必须是正整数")
	}
	return nil
}
