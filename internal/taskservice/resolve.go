package taskservice

import (
	"errors"
	"sort"

	"github.com/philsphicas/bgtask/internal/state"
)

// resolve looks up a task by name or ID, translating state.Store's
// sentinel errors into typed Service errors.
func (s *Service) resolve(op, ref string) (string, *state.Meta, error) {
	id, meta, err := s.Store.Resolve(ref)
	if err != nil {
		switch {
		case errors.Is(err, state.ErrAmbiguousName):
			return "", nil, Conflict(op, ref, "", err.Error())
		case errors.Is(err, state.ErrTaskNotFound):
			return "", nil, NotFound(op, ref, err.Error())
		default:
			return "", nil, Internal(op, ref, "", err)
		}
	}
	return id, meta, nil
}

// hasAnyLabel reports whether labels contains any of filterLabels.
func hasAnyLabel(labels []string, filterLabels []string) bool {
	for _, fl := range filterLabels {
		for _, l := range labels {
			if l == fl {
				return true
			}
		}
	}
	return false
}

// snapshotIDs takes a sorted, task-at-a-time snapshot of the task IDs
// matched by sel (which must not be an explicit Selection.Names -- callers
// handle that case separately with fail-fast, one-at-a-time resolution).
// IDs are sorted so bulk operations process tasks in a deterministic order;
// given state.GenerateID's timestamp prefix, this is also chronological.
//
// A task whose meta.json can no longer be read (e.g. removed concurrently,
// or an auto-removed ephemeral task that has since disappeared) is silently
// skipped rather than treated as an error: bulk disappearance is tolerated.
func (s *Service) snapshotIDs(op string, sel Selection) ([]string, error) {
	ids, err := s.Store.ListIDs()
	if err != nil {
		return nil, Internal(op, "", "", err)
	}
	sort.Strings(ids)

	if sel.All {
		return ids, nil
	}

	var out []string
	for _, id := range ids {
		meta, err := s.Store.ReadMeta(id)
		if err != nil {
			continue // tolerated: disappeared between ListIDs and ReadMeta
		}
		if hasAnyLabel(meta.Labels, sel.Labels) {
			out = append(out, id)
		}
	}
	return out, nil
}
