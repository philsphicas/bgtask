package taskservice

import "context"

// List returns tasks matching req.Labels (OR semantics; empty means all),
// sorted by ID (chronological, given state.GenerateID's timestamp prefix).
// A task whose meta.json cannot be read is silently skipped, matching the
// historical CLI behavior.
func (s *Service) List(ctx context.Context, req ListRequest) (*ListResult, error) {
	const op = "list"
	if cerr := checkContext(op, "", ctx); cerr != nil {
		return nil, cerr
	}

	ids, err := s.snapshotIDs(op, Selection{Labels: req.Labels, All: len(req.Labels) == 0})
	if err != nil {
		return nil, err
	}

	tasks := make([]Task, 0, len(ids))
	for _, id := range ids {
		meta, err := s.Store.ReadMeta(id)
		if err != nil {
			continue
		}
		tasks = append(tasks, s.toTask(id, meta))
	}
	return &ListResult{Tasks: tasks}, nil
}

// Get resolves ref (name or ID) and returns its current Task view.
func (s *Service) Get(ctx context.Context, ref string) (*GetResult, error) {
	const op = "get"
	if cerr := checkContext(op, ref, ctx); cerr != nil {
		return nil, cerr
	}
	id, meta, err := s.resolve(op, ref)
	if err != nil {
		return nil, err
	}
	return &GetResult{Task: s.toTask(id, meta)}, nil
}
