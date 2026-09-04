package taskservice

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"sort"
)

const listCursorVersion = 1

type listCursor struct {
	Version     int    `json:"v"`
	LastID      string `json:"last_id"`
	NewestFirst bool   `json:"newest_first"`
}

// List returns tasks matching req.Labels (OR semantics; empty means all),
// sorted by ID (chronological, given state.GenerateID's timestamp prefix).
// A task whose meta.json cannot be read is silently skipped, matching the
// historical CLI behavior.
func (s *Service) List(ctx context.Context, req ListRequest) (*ListResult, error) {
	const op = "list"
	if cerr := checkContext(op, "", ctx); cerr != nil {
		return nil, cerr
	}
	if req.Limit < 0 {
		return nil, InvalidArgument(op, "", "", "limit must not be negative")
	}
	stateFilter := make(map[string]bool, len(req.States))
	for _, taskState := range req.States {
		switch taskState {
		case "running", "exited", "dead", "unknown":
			stateFilter[taskState] = true
		default:
			return nil, InvalidArgument(op, "", "", fmt.Sprintf("invalid state %q (expected running, exited, dead, or unknown)", taskState))
		}
	}
	cursor, err := decodeListCursor(req.Cursor)
	if err != nil {
		return nil, InvalidArgument(op, "", "", err.Error())
	}
	if cursor != nil && cursor.NewestFirst != req.NewestFirst {
		return nil, InvalidArgument(op, "", "", "cursor ordering does not match this request")
	}

	ids, err := s.snapshotIDs(op, Selection{Labels: req.Labels, All: len(req.Labels) == 0})
	if err != nil {
		return nil, err
	}
	if req.NewestFirst {
		sort.Sort(sort.Reverse(sort.StringSlice(ids)))
	}

	tasks := make([]Task, 0, len(ids))
	total := 0
	for _, id := range ids {
		meta, err := s.Store.ReadMeta(id)
		if err != nil {
			continue
		}
		task := Task{
			ID:      id,
			Meta:    meta,
			Status:  s.resolveStatusWithPorts(id, req.Limit == 0),
			LogPath: s.Store.OutputPath(id),
		}
		if len(stateFilter) > 0 && !stateFilter[task.Status.State] {
			continue
		}
		total++
		if cursor != nil {
			if req.NewestFirst && id >= cursor.LastID {
				continue
			}
			if !req.NewestFirst && id <= cursor.LastID {
				continue
			}
		}
		tasks = append(tasks, task)
	}
	nextCursor := ""
	if req.Limit > 0 && len(tasks) > req.Limit {
		tasks = tasks[:req.Limit]
		nextCursor, err = encodeListCursor(tasks[len(tasks)-1].ID, req.NewestFirst)
		if err != nil {
			return nil, Internal(op, "", "", err)
		}
	}
	if req.Limit > 0 {
		for i := range tasks {
			if tasks[i].Status.Running != nil && tasks[i].Status.Running.ChildPID > 0 {
				tasks[i].Status.Running.Ports = s.Process.ListeningPorts(tasks[i].Status.Running.ChildPID)
			}
		}
	}
	return &ListResult{Tasks: tasks, Total: total, NextCursor: nextCursor}, nil
}

func encodeListCursor(lastID string, newestFirst bool) (string, error) {
	raw, err := json.Marshal(listCursor{Version: listCursorVersion, LastID: lastID, NewestFirst: newestFirst})
	if err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(raw), nil
}

func decodeListCursor(value string) (*listCursor, error) {
	if value == "" {
		return nil, nil
	}
	raw, err := base64.RawURLEncoding.DecodeString(value)
	if err != nil {
		return nil, fmt.Errorf("invalid cursor")
	}
	var cursor listCursor
	if err := json.Unmarshal(raw, &cursor); err != nil || cursor.Version != listCursorVersion || cursor.LastID == "" {
		return nil, fmt.Errorf("invalid cursor")
	}
	return &cursor, nil
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
