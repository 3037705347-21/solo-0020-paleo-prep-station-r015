package station

import (
	"context"

	"example.com/paleo-prep-station/internal/contracts"
	"example.com/paleo-prep-station/internal/domain"
	"example.com/paleo-prep-station/internal/repository"
)

func (s *Service) LogProgress(
	ctx context.Context,
	planID string,
	request contracts.ProgressRequest,
) (ProgressResult, error) {
	if err := ctx.Err(); err != nil {
		return ProgressResult{}, err
	}

	var created ProgressResult
	err := s.repo.Mutate(func(m *repository.Mutation) error {
		plan, ok := m.GetPlan(planID)
		if !ok {
			return notFoundPlan(planID)
		}
		specimen, ok := m.GetSpecimen(plan.SpecimenID)
		if !ok {
			return notFoundSpecimen(plan.SpecimenID)
		}
		// 成功条件是调用方读到的方案版本仍为最新；方案版本随后随每次成功
		// 追加推进，因此两条并发进度只有携带最新版本的一条能够成功。
		if err := requireVersion("方案", request.ExpectedVersion, plan.Version); err != nil {
			return err
		}
		previous := m.ProgressForPlan(planID)
		entry, err := domain.NewProgressEntry(
			s.nextID("progress"),
			plan,
			previous,
			request.Stage,
			request.Percent,
			request.Note,
			request.RecordedBy,
			s.now(),
		)
		if err != nil {
			return err
		}
		transitioned := false
		if plan.Status == domain.PlanApproved {
			if err := plan.Begin(s.now()); err != nil {
				return err
			}
			transitioned = true
		}
		if request.Percent == 100 {
			if err := plan.Complete(s.now()); err != nil {
				return err
			}
			if err := specimen.MarkStabilized(s.now()); err != nil {
				return err
			}
			transitioned = true
		}
		if !transitioned {
			// 已在进行中的方案追加非收尾记录时没有状态转换，但这次写入仍然
			// 改变了方案事实，必须推进版本以支撑后续的乐观版本条件。
			plan.Touch(s.now())
		}
		m.AppendProgress(entry)
		m.PutPlan(plan)
		m.PutSpecimen(specimen)
		created = ProgressResult{
			ProgressEntry:   entry,
			ExpectedVersion: request.ExpectedVersion,
			PlanVersion:     plan.Version,
			SpecimenVersion: specimen.Version,
		}
		return nil
	})
	return created, err
}
