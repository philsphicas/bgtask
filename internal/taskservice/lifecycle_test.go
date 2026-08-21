package taskservice_test

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/philsphicas/bgtask/internal/taskservice"
)

func TestStop_AlreadyStoppedIsNoOp(t *testing.T) {
	svc, env := newTestService(t)
	created := mustRun(t, svc, "stopme", []string{"sleep", "100"})
	markDead(env, created.PID)

	result, err := svc.Stop(context.Background(), taskservice.StopRequest{
		Selection: taskservice.Selection{Names: []string{"stopme"}},
	})
	if err != nil {
		t.Fatalf("Stop: %v", err)
	}
	if len(result.Items) != 1 || !result.Items[0].Result.NoOp || result.Items[0].Result.Changed {
		t.Fatalf("expected a single NoOp item, got %+v", result.Items)
	}
}

func TestStop_RunningTaskChanges(t *testing.T) {
	svc, env := newTestService(t)
	created := mustRun(t, svc, "stopme2", []string{"sleep", "100"})

	result, err := svc.Stop(context.Background(), taskservice.StopRequest{
		Selection: taskservice.Selection{Names: []string{"stopme2"}},
	})
	if err != nil {
		t.Fatalf("Stop: %v", err)
	}
	if len(result.Items) != 1 || !result.Items[0].Result.Changed {
		t.Fatalf("expected Changed=true, got %+v", result.Items)
	}
	if env.IsAlive(created.PID) {
		t.Error("expected the process to be stopped")
	}
}

func TestStop_ExplicitNamesFailFast(t *testing.T) {
	svc, _ := newTestService(t)
	mustRun(t, svc, "exists", []string{"sleep", "100"})

	result, err := svc.Stop(context.Background(), taskservice.StopRequest{
		Selection: taskservice.Selection{Names: []string{"exists", "does-not-exist", "never-reached"}},
	})
	if !taskservice.IsNotFound(err) {
		t.Fatalf("expected CodeNotFound for the second name, got %v", err)
	}
	if len(result.Items) != 2 {
		t.Fatalf("expected processing to stop after the failing item, got %d items", len(result.Items))
	}
	if !result.Items[0].Result.Changed {
		t.Errorf("expected the first (valid) item to have changed")
	}
	if result.Items[1].Err == nil {
		t.Errorf("expected the second item to carry the NotFound error")
	}
}

func TestStop_ByLabelIsBestEffort(t *testing.T) {
	svc, _ := newTestService(t)
	mustRun(t, svc, "l1", []string{"sleep", "100"})
	if _, err := svc.SetLabels(context.Background(), taskservice.SetLabelsRequest{Ref: "l1", Labels: []string{"grp"}}); err != nil {
		t.Fatal(err)
	}
	r2 := mustRun(t, svc, "l2", []string{"sleep", "100"})
	if _, err := svc.SetLabels(context.Background(), taskservice.SetLabelsRequest{Ref: "l2", Labels: []string{"grp"}}); err != nil {
		t.Fatal(err)
	}
	mustRun(t, svc, "l3", []string{"sleep", "100"}) // unlabeled, must not be touched

	result, err := svc.Stop(context.Background(), taskservice.StopRequest{
		Selection: taskservice.Selection{Labels: []string{"grp"}},
	})
	if err != nil {
		t.Fatalf("Stop by label: %v", err)
	}
	if len(result.Items) != 2 {
		t.Fatalf("expected exactly 2 matched items, got %d", len(result.Items))
	}
	for _, item := range result.Items {
		if !item.Result.Changed {
			t.Errorf("expected item %s to have changed", item.Ref)
		}
	}

	l3, err := svc.Get(context.Background(), "l3")
	if err != nil {
		t.Fatalf("Get l3: %v", err)
	}
	if l3.Task.Status.State != "running" {
		t.Errorf("expected unlabeled task l3 to remain running, got %q", l3.Task.Status.State)
	}
	_ = r2
}

func TestStop_AllSelection(t *testing.T) {
	svc, _ := newTestService(t)
	mustRun(t, svc, "a1", []string{"sleep", "100"})
	mustRun(t, svc, "a2", []string{"sleep", "100"})

	result, err := svc.Stop(context.Background(), taskservice.StopRequest{Selection: taskservice.Selection{All: true}})
	if err != nil {
		t.Fatalf("Stop --all: %v", err)
	}
	if len(result.Items) != 2 {
		t.Fatalf("expected 2 items, got %d", len(result.Items))
	}
}

func TestStop_ForceKillsImmediately(t *testing.T) {
	svc, env := newTestService(t)
	created := mustRun(t, svc, "forceme", []string{"sleep", "100"})

	result, err := svc.Stop(context.Background(), taskservice.StopRequest{
		Selection: taskservice.Selection{Names: []string{"forceme"}},
		Force:     true,
	})
	if err != nil {
		t.Fatalf("Stop --force: %v", err)
	}
	if !result.Items[0].Result.Forced {
		t.Error("expected Forced = true")
	}
	if env.killCount(created.PID) == 0 {
		t.Error("expected SignalKill to have been called")
	}
}

func TestStop_SurvivesContextCancellationAfterSignal(t *testing.T) {
	svc, env := newTestService(t)
	created := mustRun(t, svc, "slow-stop", []string{"sleep", "100"})

	// The fake process takes 150ms to actually die after being signaled --
	// long enough that the request's context will have already expired by
	// the time gracefulStop's internal wait loop observes it, proving the
	// destructive cleanup is not aborted by request cancellation.
	env.stopDelay = 150 * time.Millisecond

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Millisecond)
	defer cancel()

	result, err := svc.Stop(ctx, taskservice.StopRequest{
		Selection: taskservice.Selection{Names: []string{"slow-stop"}},
	})
	if err != nil {
		t.Fatalf("Stop: %v", err)
	}
	if !result.Items[0].Result.Changed {
		t.Fatalf("expected the stop to complete despite context expiry, got %+v", result.Items[0])
	}
	if env.IsAlive(created.PID) {
		t.Error("expected the process to be fully stopped")
	}
}

func TestRestart_NotRunningIsFailedPrecondition(t *testing.T) {
	svc, env := newTestService(t)
	created := mustRun(t, svc, "notrunning", []string{"sleep", "100"})
	markDead(env, created.PID)

	_, err := svc.Restart(context.Background(), taskservice.RestartRequest{
		Selection: taskservice.Selection{Names: []string{"notrunning"}},
	})
	if !taskservice.IsFailedPrecondition(err) {
		t.Fatalf("expected CodeFailedPrecondition, got %v (code=%s)", err, taskservice.CodeOf(err))
	}
}

func TestRestart_RunningSignalsRestart(t *testing.T) {
	svc, env := newTestService(t)
	mustRun(t, svc, "restartme", []string{"sleep", "100"})

	result, err := svc.Restart(context.Background(), taskservice.RestartRequest{
		Selection: taskservice.Selection{Names: []string{"restartme"}},
	})
	if err != nil {
		t.Fatalf("Restart: %v", err)
	}
	if !result.Items[0].Result.Changed {
		t.Error("expected Changed = true")
	}
	if env.restartCount() != 1 {
		t.Errorf("restartCount = %d, want 1", env.restartCount())
	}
}

func TestRestart_ByLabelSkipsNonRunningSilently(t *testing.T) {
	svc, env := newTestService(t)
	mustRun(t, svc, "r1", []string{"sleep", "100"})
	if _, err := svc.SetLabels(context.Background(), taskservice.SetLabelsRequest{Ref: "r1", Labels: []string{"grp"}}); err != nil {
		t.Fatal(err)
	}
	stopped := mustRun(t, svc, "r2", []string{"sleep", "100"})
	if _, err := svc.SetLabels(context.Background(), taskservice.SetLabelsRequest{Ref: "r2", Labels: []string{"grp"}}); err != nil {
		t.Fatal(err)
	}
	markDead(env, stopped.PID)

	result, err := svc.Restart(context.Background(), taskservice.RestartRequest{Selection: taskservice.Selection{Labels: []string{"grp"}}})
	if err != nil {
		t.Fatalf("Restart by label: %v", err)
	}
	if len(result.Items) != 2 {
		t.Fatalf("expected 2 matched items, got %d", len(result.Items))
	}
	changed := 0
	failedPrecondition := 0
	for _, item := range result.Items {
		switch {
		case item.Result.Changed:
			changed++
		case taskservice.IsFailedPrecondition(item.Err):
			failedPrecondition++
		}
	}
	if changed != 1 || failedPrecondition != 1 {
		t.Errorf("expected 1 changed + 1 failed-precondition, got changed=%d failedPrecondition=%d", changed, failedPrecondition)
	}
}

func TestStart_AlreadyRunningIsFailedPrecondition(t *testing.T) {
	svc, _ := newTestService(t)
	mustRun(t, svc, "already", []string{"sleep", "100"})

	_, err := svc.Start(context.Background(), taskservice.StartRequest{
		Selection: taskservice.Selection{Names: []string{"already"}},
	})
	if !taskservice.IsFailedPrecondition(err) {
		t.Fatalf("expected CodeFailedPrecondition, got %v (code=%s)", err, taskservice.CodeOf(err))
	}
}

func TestStart_StoppedTaskLaunches(t *testing.T) {
	svc, env := newTestService(t)
	created := mustRun(t, svc, "stopped", []string{"sleep", "100"})
	markDead(env, created.PID)

	result, err := svc.Start(context.Background(), taskservice.StartRequest{
		Selection: taskservice.Selection{Names: []string{"stopped"}},
	})
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	if !result.Items[0].Result.Changed {
		t.Fatalf("expected Changed = true, got %+v", result.Items[0])
	}
	if env.launchCount() != 2 {
		t.Fatalf("launches = %d, want 2 (initial run + restart)", env.launchCount())
	}
}

// TestStart_ConcurrentStartsLaunchOnlyOneSupervisor exercises the
// per-task-lock-held-through-launch-readiness requirement: two concurrent
// Start calls for the same stopped task must never both succeed in
// launching a supervisor.
func TestStart_ConcurrentStartsLaunchOnlyOneSupervisor(t *testing.T) {
	svc, env := newTestService(t)
	created := mustRun(t, svc, "race", []string{"sleep", "100"})
	markDead(env, created.PID)

	const n = 5
	errs := make([]error, n)
	var wg sync.WaitGroup
	wg.Add(n)
	for i := 0; i < n; i++ {
		i := i
		go func() {
			defer wg.Done()
			_, errs[i] = svc.Start(context.Background(), taskservice.StartRequest{
				Selection: taskservice.Selection{Names: []string{"race"}},
			})
		}()
	}
	wg.Wait()

	if got := env.launchCount(); got != 2 {
		// 1 from the initial mustRun + exactly 1 from whichever concurrent
		// Start call won the per-task lock race.
		t.Fatalf("launches = %d, want 2 (no duplicate supervisors)", got)
	}
	successes, failedPrecondition := 0, 0
	for _, err := range errs {
		switch {
		case err == nil:
			successes++
		case taskservice.IsFailedPrecondition(err):
			failedPrecondition++
		default:
			t.Errorf("unexpected error from concurrent Start: %v", err)
		}
	}
	if successes != 1 || failedPrecondition != n-1 {
		t.Errorf("expected exactly 1 success and %d failed-precondition, got successes=%d failedPrecondition=%d", n-1, successes, failedPrecondition)
	}
}

func TestRemove_StopsAndDeletes(t *testing.T) {
	svc, env := newTestService(t)
	created := mustRun(t, svc, "removeme", []string{"sleep", "100"})

	result, err := svc.Remove(context.Background(), taskservice.RemoveRequest{
		Selection: taskservice.Selection{Names: []string{"removeme"}},
	})
	if err != nil {
		t.Fatalf("Remove: %v", err)
	}
	if !result.Items[0].Result.Changed {
		t.Fatalf("expected Changed = true, got %+v", result.Items[0])
	}
	if env.IsAlive(created.PID) {
		t.Error("expected the process to be stopped before removal")
	}
	if _, _, err := svc.Store.Resolve(created.Task.ID); err == nil {
		t.Error("expected the task directory to be gone")
	}
}

func TestRemove_UnknownRefIsNotFound(t *testing.T) {
	svc, _ := newTestService(t)
	_, err := svc.Remove(context.Background(), taskservice.RemoveRequest{
		Selection: taskservice.Selection{Names: []string{"nope"}},
	})
	if !taskservice.IsNotFound(err) {
		t.Fatalf("expected CodeNotFound, got %v (code=%s)", err, taskservice.CodeOf(err))
	}
}

func TestCleanup_RemovesOnlyNonRunning(t *testing.T) {
	svc, env := newTestService(t)
	running := mustRun(t, svc, "keep-running", []string{"sleep", "100"})
	stopped := mustRun(t, svc, "clean-me", []string{"sleep", "100"})
	markDead(env, stopped.PID)

	result, err := svc.Cleanup(context.Background(), taskservice.CleanupRequest{})
	if err != nil {
		t.Fatalf("Cleanup: %v", err)
	}

	var changed, noop int
	for _, item := range result.Items {
		if item.Result.Changed {
			changed++
		}
		if item.Result.NoOp {
			noop++
		}
	}
	if changed != 1 || noop != 1 {
		t.Fatalf("expected 1 changed + 1 no-op, got changed=%d noop=%d", changed, noop)
	}

	if _, _, err := svc.Store.Resolve(stopped.Task.ID); err == nil {
		t.Error("expected the stopped task to be removed")
	}
	if _, _, err := svc.Store.Resolve(running.Task.ID); err != nil {
		t.Errorf("expected the running task to remain: %v", err)
	}
}
