package taskservice

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"slices"
	"sort"
)

const listCursorVersion = 2

type listCursor struct {
	Version     int      `json:"v"`
	LastID      string   `json:"last_id"`
	NewestFirst bool     `json:"newest_first"`
	Labels      []string `json:"labels"`
	States      []string `json:"states"`
}

// List returns tasks matching both req.Labels and req.States (OR within each
// filter; empty means all), ordered by ascending ID unless NewestFirst is set.
// Limit and Cursor select a page; Total counts all matches before pagination.
// Cursors are bound to the filters and ordering, not to a frozen snapshot.
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
	filterByState := len(stateFilter) > 0 && len(stateFilter) < 4
	cursor, err := decodeListCursor(req.Cursor)
	if err != nil {
		return nil, InvalidArgument(op, "", "", err.Error())
	}
	if cursor != nil && cursor.NewestFirst != req.NewestFirst {
		return nil, InvalidArgument(op, "", "", "cursor ordering does not match this request")
	}
	labels := normalizedListFilter(req.Labels)
	states := normalizedListFilter(req.States)
	if len(states) == 0 {
		states = []string{"dead", "exited", "running", "unknown"}
	}
	if cursor != nil && (!slices.Equal(cursor.Labels, labels) || !slices.Equal(cursor.States, states)) {
		return nil, InvalidArgument(op, "", "", "cursor filters do not match this request")
	}

	ids, err := s.snapshotIDs(op, Selection{Labels: req.Labels, All: len(req.Labels) == 0})
	if err != nil {
		return nil, err
	}
	if req.NewestFirst {
		sort.Sort(sort.Reverse(sort.StringSlice(ids)))
	}

	taskCapacity := len(ids)
	if req.Limit > 0 && req.Limit < taskCapacity {
		taskCapacity = req.Limit
	}
	tasks := make([]Task, 0, taskCapacity)
	total := 0
	hasMore := false
	for _, id := range ids {
		meta, err := s.Store.ReadMeta(id)
		if err != nil {
			continue
		}
		task := Task{
			ID:   id,
			Meta: meta,
		}
		if filterByState {
			task.Status = s.resolveStatusWithPorts(id, false)
			if !stateFilter[task.Status.State] {
				continue
			}
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
		if req.Limit > 0 && len(tasks) >= req.Limit {
			hasMore = true
			continue
		}
		tasks = append(tasks, task)
	}
	nextCursor := ""
	if hasMore {
		nextCursor, err = encodeListCursor(tasks[len(tasks)-1].ID, req.NewestFirst, labels, states)
		if err != nil {
			return nil, Internal(op, "", "", err)
		}
	}
	for i := range tasks {
		if !filterByState {
			tasks[i].Status = s.resolveStatusWithPorts(tasks[i].ID, false)
		}
		tasks[i].LogPath = s.Store.OutputPath(tasks[i].ID)
		if tasks[i].Status.Running != nil && tasks[i].Status.Running.ChildPID > 0 {
			tasks[i].Status.Running.Ports = s.Process.ListeningPorts(tasks[i].Status.Running.ChildPID)
		}
	}
	return &ListResult{Tasks: tasks, Total: total, NextCursor: nextCursor}, nil
}

func normalizedListFilter(values []string) []string {
	normalized := slices.Clone(values)
	slices.Sort(normalized)
	return slices.Compact(normalized)
}

func encodeListCursor(lastID string, newestFirst bool, labels, states []string) (string, error) {
	raw, err := json.Marshal(listCursor{Version: listCursorVersion, LastID: lastID, NewestFirst: newestFirst, Labels: labels, States: states})
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
