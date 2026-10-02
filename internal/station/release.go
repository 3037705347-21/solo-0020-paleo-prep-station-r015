package station

import (
	"context"

	"example.com/paleo-prep-station/internal/contracts"
	"example.com/paleo-prep-station/internal/domain"
	"example.com/paleo-prep-station/internal/repository"
)

func (s *Service) ReleaseSpecimen(
	ctx context.Context,
	specimenID string,
	request contracts.ReleaseRequest,
) (ReleaseResult, error) {
	if err := ctx.Err(); err != nil {
		return ReleaseResult{}, err
	}

	var created ReleaseResult
	err := s.repo.Mutate(func(m *repository.Mutation) error {
		specimen, ok := m.GetSpecimen(specimenID)
		if !ok {
			return notFoundSpecimen(specimenID)
		}
		// 放行唯一性、方案闭合与标本稳定都在最新状态上判定；版本条件先于
		// 这些业务条件生效，陈旧读者即使撞上其他冲突也会先得到明确的冲突。
		if err := requireVersion("标本", request.ExpectedVersion, specimen.Version); err != nil {
			return err
		}
		if m.HasOpenPlan(specimenID) {
			return contracts.Conflict("open_plan_blocks_release", "存在未结束方案，不能办理研究放行")
		}
		if _, exists := m.ReleaseForSpecimen(specimenID); exists {
			return contracts.Conflict("release_exists", "该标本已经存在放行记录")
		}
		record, err := domain.NewReleaseRecord(
			s.nextID("release"),
			specimen,
			request.Purpose,
			request.Recipient,
			request.ApprovedBy,
			s.now(),
		)
		if err != nil {
			return err
		}
		if err := specimen.MarkAvailable(s.now()); err != nil {
			return err
		}
		m.PutRelease(record)
		m.PutSpecimen(specimen)
		created = ReleaseResult{
			ReleaseRecord:   record,
			ExpectedVersion: request.ExpectedVersion,
			SpecimenVersion: specimen.Version,
		}
		return nil
	})
	return created, err
}
