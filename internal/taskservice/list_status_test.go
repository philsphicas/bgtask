package taskservice_test

import (
	"context"
	"slices"
	"testing"

	"github.com/philsphicas/bgtask/internal/taskservice"
)

type statusCallCounter struct {
	taskservice.ProcessController
	aliveCalls  int
	verifyCalls int
}

func (c *statusCallCounter) IsAlive(pid int) bool {
	c.aliveCalls++
	return c.ProcessController.IsAlive(pid)
}

func (c *statusCallCounter) VerifyPID(pid int, createTime int64) bool {
	c.verifyCalls++
	return c.ProcessController.VerifyPID(pid, createTime)
}

func TestList_OnlyResolvesReturnedStatusesWithoutStateFiltering(t *testing.T) {
	svc, env := newTestService(t)
	for _, name := range []string{"first", "second", "third", "fourth", "fifth"} {
		created := mustRun(t, svc, name, []string{"sleep", "100"})
		if err := svc.Store.WritePID(created.Task.ID, "child.pid", created.PID+1000); err != nil {
			t.Fatal(err)
		}
		env.ports[created.PID+1000] = []uint32{8080}
	}
	calls := &statusCallCounter{ProcessController: env}
	svc.Process = calls

	for _, newestFirst := range []bool{false, true} {
		for _, states := range [][]string{nil, {}, {"running", "exited", "dead", "unknown"}} {
			var ids []string
			cursor := ""
			for _, want := range []int{2, 2, 1} {
				aliveBefore, verifyBefore, portsBefore := calls.aliveCalls, calls.verifyCalls, env.portCallCount()
				page, err := svc.List(context.Background(), taskservice.ListRequest{
					Limit: 2, Cursor: cursor, NewestFirst: newestFirst, States: states,
				})
				if err != nil {
					t.Fatal(err)
				}
				if page.Total != 5 || len(page.Tasks) != want {
					t.Fatalf("page = %+v, want total 5 and %d tasks", page, want)
				}
				if calls.aliveCalls-aliveBefore != want || calls.verifyCalls-verifyBefore != want ||
					env.portCallCount()-portsBefore != want {
					t.Fatalf("page of %d made %d liveness, %d verification and %d port calls", want,
						calls.aliveCalls-aliveBefore, calls.verifyCalls-verifyBefore, env.portCallCount()-portsBefore)
				}
				for _, task := range page.Tasks {
					if task.Status.State != "running" || task.Status.Running == nil ||
						task.Status.Running.ChildPID == 0 || !slices.Equal(task.Status.Running.Ports, []uint32{8080}) ||
						task.LogPath != svc.Store.OutputPath(task.ID) {
						t.Fatalf("incomplete returned task: %+v", task)
					}
					if slices.Contains(ids, task.ID) {
						t.Fatalf("task %s repeated across pages", task.ID)
					}
					ids = append(ids, task.ID)
				}
				cursor = page.NextCursor
				if (want == 1) != (cursor == "") {
					t.Fatalf("unexpected continuation for page of %d: %q", want, cursor)
				}
			}
			sorted := slices.Clone(ids)
			slices.Sort(sorted)
			if newestFirst {
				slices.Reverse(sorted)
			}
			if !slices.Equal(ids, sorted) {
				t.Fatalf("ordering changed for NewestFirst=%v: %v", newestFirst, ids)
			}
		}
	}

	for _, states := range [][]string{{"running"}, {"exited"}} {
		aliveBefore, verifyBefore, portsBefore := calls.aliveCalls, calls.verifyCalls, env.portCallCount()
		page, err := svc.List(context.Background(), taskservice.ListRequest{Limit: 2, States: states})
		if err != nil {
			t.Fatal(err)
		}
		wantTotal, wantReturned := 5, 2
		if states[0] == "exited" {
			wantTotal, wantReturned = 0, 0
		}
		if page.Total != wantTotal || len(page.Tasks) != wantReturned ||
			calls.aliveCalls-aliveBefore != 5 || calls.verifyCalls-verifyBefore != 5 ||
			env.portCallCount()-portsBefore != wantReturned {
			t.Fatalf("state-filtered list did not scan all candidates once and enrich only returned tasks: %+v", page)
		}
	}
}
