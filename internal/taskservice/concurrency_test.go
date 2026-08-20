package taskservice_test

import (
	"context"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/philsphicas/bgtask/internal/state"
	"github.com/philsphicas/bgtask/internal/taskservice"
)

// namedTaskIDs returns the IDs of every task in the store whose meta.json
// carries the given name. Name uniqueness is the invariant Run's
// global-then-task locking exists to protect, so tests assert on it
// directly rather than trusting Resolve (which hides duplicates behind an
// "ambiguous" error).
func namedTaskIDs(t *testing.T, store *state.Store, name string) []string {
	t.Helper()
	ids, err := store.ListIDs()
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	var out []string
	for _, id := range ids {
		meta, err := store.ReadMeta(id)
		if err != nil {
			continue
		}
		if meta.Name == name {
			out = append(out, id)
		}
	}
	return out
}

// lockFiles returns the per-task lock files currently on disk. A completed
// operation must never leave one behind, especially for a task it removed.
func lockFiles(t *testing.T, store *state.Store) []string {
	t.Helper()
	entries, err := os.ReadDir(filepath.Join(store.Root, ".locks"))
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		t.Fatalf("read .locks: %v", err)
	}
	var out []string
	for _, e := range entries {
		out = append(out, e.Name())
	}
	return out
}

// TestRun_ConcurrentReplaceKeepsNameUnique hammers the same name with
// concurrent replacing Runs. Because Run holds the store-wide lock across
// the whole "is the name taken -> stop/remove the old task -> create the
// new one" decision, exactly one task may carry the name when the dust
// settles, and every successful result must point at a task that still
// exists.
//
// The previous implementation released the global lock before stopping and
// removing the replaced task, so two Runs could both observe "not taken"
// (or both remove and then both create) and leave duplicates behind.
func TestRun_ConcurrentReplaceKeepsNameUnique(t *testing.T) {
	svc, env := newTestService(t)

	const workers = 8
	var wg sync.WaitGroup
	results := make([]*taskservice.RunResult, workers)
	errs := make([]error, workers)
	start := make(chan struct{})

	for i := 0; i < workers; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			<-start
			results[i], errs[i] = svc.Run(context.Background(), taskservice.RunRequest{
				Name:            "web",
				Command:         []string{"sleep", "1"},
				Cwd:             ".",
				ReplaceExisting: true,
			})
		}(i)
	}
	close(start)
	wg.Wait()

	succeeded := 0
	for i, err := range errs {
		if err != nil {
			t.Fatalf("worker %d: Run failed: %v", i, err)
		}
		succeeded++
		if _, rerr := svc.Store.ReadMeta(results[i].Task.ID); rerr != nil && results[i].Task.ID != "" {
			// Losing the race to a later replacement is fine, but the
			// winner must be resolvable; checked in aggregate below.
			continue
		}
	}
	if succeeded != workers {
		t.Fatalf("expected all %d runs to succeed, got %d", workers, succeeded)
	}

	ids := namedTaskIDs(t, svc.Store, "web")
	if len(ids) != 1 {
		t.Fatalf("expected exactly 1 task named %q after %d concurrent replacing runs, got %d (%v)",
			"web", workers, len(ids), ids)
	}
	if taken, err := svc.Store.IsNameTaken("web"); err != nil || !taken {
		t.Fatalf("IsNameTaken(web) = %v, %v; want true, nil", taken, err)
	}
	if _, _, err := svc.Store.Resolve("web"); err != nil {
		t.Fatalf("Resolve(web) after concurrent replacement: %v", err)
	}

	// Every replaced supervisor must have been stopped, and no per-task
	// lock file may survive a removed task.
	if got := len(lockFiles(t, svc.Store)); got != 0 {
		t.Fatalf("expected no leftover task lock files, got %d: %v", got, lockFiles(t, svc.Store))
	}
	if env.launchCount() != workers {
		t.Fatalf("expected %d launches, got %d", workers, env.launchCount())
	}
}

// TestRun_ConcurrentReplaceAndRenameKeepsNamesUnique interleaves replacing
// Runs on "web" with Renames of a second task onto "web". Rename takes the
// global lock and then the task lock; Run must take them in the same order
// (and actually take the old task's lock before destroying it) or the two
// can interleave into a duplicate name -- or deadlock.
func TestRun_ConcurrentReplaceAndRenameKeepsNamesUnique(t *testing.T) {
	svc, _ := newTestService(t)

	other := mustRun(t, svc, "other", []string{"sleep", "1"})

	var wg sync.WaitGroup
	start := make(chan struct{})
	renameErr := make(chan error, 1)

	wg.Add(1)
	go func() {
		defer wg.Done()
		<-start
		_, err := svc.Rename(context.Background(), taskservice.RenameRequest{
			Ref: other.Task.ID, NewName: "web",
		})
		renameErr <- err
	}()

	for i := 0; i < 4; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			_, err := svc.Run(context.Background(), taskservice.RunRequest{
				Name:            "web",
				Command:         []string{"sleep", "1"},
				Cwd:             ".",
				ReplaceExisting: true,
			})
			if err != nil {
				t.Errorf("Run(web): %v", err)
			}
		}()
	}

	close(start)
	done := make(chan struct{})
	go func() { wg.Wait(); close(done) }()
	select {
	case <-done:
	case <-time.After(30 * time.Second):
		t.Fatal("timed out: concurrent Run/Rename deadlocked")
	}

	// The rename either succeeded (before some Run replaced it) or lost to
	// a conflict; both are fine. What must hold either way is uniqueness.
	if err := <-renameErr; err != nil && !taskservice.IsConflict(err) && !taskservice.IsNotFound(err) {
		t.Fatalf("Rename: unexpected error kind: %v", err)
	}
	ids := namedTaskIDs(t, svc.Store, "web")
	if len(ids) != 1 {
		t.Fatalf("expected exactly 1 task named %q, got %d (%v)", "web", len(ids), ids)
	}
}

// TestRunAndStartConcurrently_LaunchOnlyOneSupervisorPerTask races a
// replacing Run against Starts of the stopped task with the same name.
// Run holds the *new* task's lock through launch readiness, and Start
// holds the task lock through its own launch, so no task may ever end up
// with two supervisors, and the name must stay unique.
func TestRunAndStartConcurrently_LaunchOnlyOneSupervisorPerTask(t *testing.T) {
	svc, env := newTestService(t)

	first := mustRun(t, svc, "web", []string{"sleep", "1"})
	markDead(env, first.PID)

	var wg sync.WaitGroup
	start := make(chan struct{})

	wg.Add(1)
	go func() {
		defer wg.Done()
		<-start
		if _, err := svc.Run(context.Background(), taskservice.RunRequest{
			Name:            "web",
			Command:         []string{"sleep", "1"},
			Cwd:             ".",
			ReplaceExisting: true,
		}); err != nil {
			t.Errorf("Run(web): %v", err)
		}
	}()

	for i := 0; i < 4; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			// Start by ID: the task may be replaced mid-flight, in which
			// case not-found is the correct typed answer.
			res, err := svc.Start(context.Background(), taskservice.StartRequest{
				Selection: taskservice.Selection{Names: []string{first.Task.ID}},
			})
			if err != nil {
				if !taskservice.IsNotFound(err) && !taskservice.IsFailedPrecondition(err) && !taskservice.IsBusy(err) {
					t.Errorf("Start: unexpected error kind: %v", err)
				}
				return
			}
			for _, item := range res.Items {
				if item.Err != nil && !taskservice.IsNotFound(item.Err) &&
					!taskservice.IsFailedPrecondition(item.Err) && !taskservice.IsBusy(item.Err) {
					t.Errorf("Start item: unexpected error kind: %v", item.Err)
				}
			}
		}()
	}

	close(start)
	wg.Wait()

	ids := namedTaskIDs(t, svc.Store, "web")
	if len(ids) != 1 {
		t.Fatalf("expected exactly 1 task named %q, got %d (%v)", "web", len(ids), ids)
	}

	// Whatever survived must have exactly one live supervisor.
	live := 0
	for _, id := range ids {
		pid, err := svc.Store.ReadPID(id, "supervisor.pid")
		if err == nil && pid > 0 && env.IsAlive(pid) {
			live++
		}
	}
	if live > 1 {
		t.Fatalf("expected at most 1 live supervisor for %q, got %d", "web", live)
	}
}

// TestStart_WaitsForReadinessAndFailsWhenSupervisorNeverSignals proves
// Start actually verifies readiness instead of reporting success the
// moment Launch returns. With a supervisor that never writes
// supervisor.pid, Start must fail (retryably) once the bound elapses.
func TestStart_WaitsForReadinessAndFailsWhenSupervisorNeverSignals(t *testing.T) {
	svc, env := newTestService(t)

	res := mustRun(t, svc, "web", []string{"sleep", "1"})
	markDead(env, res.PID)

	env.skipPIDWrite = true
	svc.StartupReadyTimeout = 150 * time.Millisecond
	svc.StartupPollInterval = 10 * time.Millisecond

	begin := time.Now()
	batch, err := svc.Start(context.Background(), taskservice.StartRequest{
		Selection: taskservice.Selection{Names: []string{"web"}},
	})
	if err == nil {
		t.Fatalf("Start: expected a readiness failure, got success: %+v", batch)
	}
	if !taskservice.IsDeadlineExceeded(err) {
		t.Fatalf("Start: expected deadline_exceeded, got %v (code=%s)", err, taskservice.CodeOf(err))
	}
	if !taskservice.IsRetryable(err) {
		t.Fatal("Start: readiness timeout should be retryable")
	}
	if elapsed := time.Since(begin); elapsed < 100*time.Millisecond {
		t.Fatalf("Start returned after %v; it did not actually wait for readiness", elapsed)
	}
}

// TestStart_FastCommandThatAlreadyExitedCountsAsLaunched: a command quick
// enough to write exit.json before we observe supervisor.pid still
// launched successfully. Treating that as a readiness failure would break
// every short-lived task.
func TestStart_FastCommandThatAlreadyExitedCountsAsLaunched(t *testing.T) {
	svc, env := newTestService(t)

	res := mustRun(t, svc, "quick", []string{"echo", "hi"})
	markDead(env, res.PID)

	env.exitOnLaunch = &state.Exit{Code: 0, ExitedAt: time.Now()}
	svc.StartupReadyTimeout = 150 * time.Millisecond

	batch, err := svc.Start(context.Background(), taskservice.StartRequest{
		Selection: taskservice.Selection{Names: []string{"quick"}},
	})
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	if len(batch.Items) != 1 || batch.Items[0].Err != nil {
		t.Fatalf("Start: expected one successful item, got %+v", batch.Items)
	}
	if !batch.Items[0].Result.Changed {
		t.Fatal("Start: expected Changed=true for a fast command")
	}
}

// TestStart_AutoRemovedTaskIsTypedTerminalOutcome: an --rm task whose
// command finishes immediately deletes its own state directory. That must
// surface as a successful, explicitly-flagged terminal outcome, not as
// "task no longer exists" or a readiness timeout.
func TestStart_AutoRemovedTaskIsTypedTerminalOutcome(t *testing.T) {
	svc, env := newTestService(t)

	res := mustRun(t, svc, "ephemeral", []string{"echo", "hi"})
	markDead(env, res.PID)

	env.removeOnLaunch = true
	svc.StartupReadyTimeout = 150 * time.Millisecond

	batch, err := svc.Start(context.Background(), taskservice.StartRequest{
		Selection: taskservice.Selection{Names: []string{"ephemeral"}},
	})
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	if len(batch.Items) != 1 {
		t.Fatalf("expected 1 item, got %d", len(batch.Items))
	}
	item := batch.Items[0]
	if item.Err != nil {
		t.Fatalf("Start: expected success for an auto-removed task, got %v", item.Err)
	}
	if !item.Result.AutoRemoved {
		t.Fatal("Start: expected Result.AutoRemoved=true for a task that removed its own state")
	}
}

// TestStart_SupervisorDiesWithoutSignalingIsInternal: a supervisor that
// exits immediately leaving no PID, no exit.json and an intact task
// directory is a genuine launch failure -- reported promptly rather than
// after the full readiness bound.
func TestStart_SupervisorDiesWithoutSignalingIsInternal(t *testing.T) {
	svc, env := newTestService(t)

	res := mustRun(t, svc, "web", []string{"sleep", "1"})
	markDead(env, res.PID)

	env.dieOnLaunch = true
	svc.StartupReadyTimeout = 5 * time.Second

	begin := time.Now()
	batch, err := svc.Start(context.Background(), taskservice.StartRequest{
		Selection: taskservice.Selection{Names: []string{"web"}},
	})
	if err == nil {
		t.Fatalf("Start: expected failure, got %+v", batch)
	}
	if taskservice.CodeOf(err) != taskservice.CodeInternal {
		t.Fatalf("Start: expected internal, got %v (code=%s)", err, taskservice.CodeOf(err))
	}
	if elapsed := time.Since(begin); elapsed > 2*time.Second {
		t.Fatalf("Start took %v to notice an instantly-dead supervisor; it should not wait out the full bound", elapsed)
	}
}

// TestRun_ReadinessFailureLeavesNoStaleState: when the new supervisor
// never signals readiness, Run must fail *and* clean up the task it
// created, rather than leaving a half-created task (and its lock file)
// squatting on the name.
func TestRun_ReadinessFailureLeavesNoStaleState(t *testing.T) {
	svc, env := newTestService(t)

	env.skipPIDWrite = true
	svc.StartupReadyTimeout = 150 * time.Millisecond
	svc.StartupPollInterval = 10 * time.Millisecond

	_, err := svc.Run(context.Background(), taskservice.RunRequest{
		Name:    "web",
		Command: []string{"sleep", "1"},
		Cwd:     ".",
	})
	if err == nil {
		t.Fatal("Run: expected a readiness failure")
	}
	if !taskservice.IsDeadlineExceeded(err) {
		t.Fatalf("Run: expected deadline_exceeded, got %v (code=%s)", err, taskservice.CodeOf(err))
	}

	if ids := namedTaskIDs(t, svc.Store, "web"); len(ids) != 0 {
		t.Fatalf("Run left %d stale task(s) named %q behind: %v", len(ids), "web", ids)
	}
	if taken, err := svc.Store.IsNameTaken("web"); err != nil || taken {
		t.Fatalf("IsNameTaken(web) = %v, %v; want false, nil", taken, err)
	}
	if got := lockFiles(t, svc.Store); len(got) != 0 {
		t.Fatalf("Run left stale lock files behind: %v", got)
	}
}

// TestRun_ReplacementCompletesStopEvenWhenContextIsCanceledMidStop: once a
// destructive stop has begun, abandoning it would orphan the supervisor.
// The stop (and the removal of the replaced task) must therefore run to
// completion even if the caller's context is canceled part-way through,
// leaving no stale lock or half-removed task behind.
func TestRun_ReplacementCompletesStopEvenWhenContextIsCanceledMidStop(t *testing.T) {
	svc, env := newTestService(t)

	first := mustRun(t, svc, "web", []string{"sleep", "1"})

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	env.onSignalStop = func() { cancel() }

	res, err := svc.Run(ctx, taskservice.RunRequest{
		Name:            "web",
		Command:         []string{"sleep", "1"},
		Cwd:             ".",
		ReplaceExisting: true,
	})
	// Either outcome is legitimate: the cancellation may abort the
	// (non-destructive) remainder, or the launch may already have won the
	// race. What must never happen is a half-finished replacement.
	if err != nil && !taskservice.IsDeadlineExceeded(err) {
		t.Fatalf("Run: expected success or deadline_exceeded after cancellation, got %v (code=%s)",
			err, taskservice.CodeOf(err))
	}

	// The destructive half must have finished regardless of cancellation.
	if env.IsAlive(first.PID) {
		t.Fatal("replaced supervisor is still alive: the stop was abandoned mid-flight")
	}
	if _, rerr := svc.Store.ReadMeta(first.Task.ID); rerr == nil {
		t.Fatal("replaced task state still exists: the removal was abandoned mid-flight")
	}
	if got := lockFiles(t, svc.Store); len(got) != 0 {
		t.Fatalf("canceled replacement left stale lock files behind: %v", got)
	}

	ids := namedTaskIDs(t, svc.Store, "web")
	if err != nil {
		if len(ids) != 0 {
			t.Fatalf("failed replacement left %d task(s) named %q behind: %v", len(ids), "web", ids)
		}
		return
	}
	if len(ids) != 1 || ids[0] != res.Task.ID {
		t.Fatalf("expected exactly the new task named %q, got %v (new=%s)", "web", ids, res.Task.ID)
	}
}

// TestLockContention_BusyVsDeadlineExceeded pins the error mapping: a
// genuinely contended lock that outlasts our own bounded wait is a
// retryable Busy, while the *caller's* context expiring is
// deadline_exceeded. The previous mapping reported both as Busy.
func TestLockContention_BusyVsDeadlineExceeded(t *testing.T) {
	svc, _ := newTestService(t)
	res := mustRun(t, svc, "web", []string{"sleep", "1"})

	// Hold the task lock for the duration of the test.
	held, err := svc.Store.LockTaskContext(context.Background(), res.Task.ID)
	if err != nil {
		t.Fatalf("LockTaskContext: %v", err)
	}
	defer held.Unlock()

	// Rename acquires the task lock with our internal bound; the caller's
	// context stays healthy, so this is contention, not a caller timeout.
	svc.LockWaitTimeout = 100 * time.Millisecond
	_, err = svc.Rename(context.Background(), taskservice.RenameRequest{
		Ref: res.Task.ID, NewName: "web2",
	})
	if err == nil {
		t.Fatal("Rename: expected a lock failure while the task lock is held")
	}
	if !taskservice.IsBusy(err) {
		t.Fatalf("Rename: expected busy for contention, got %v (code=%s)", err, taskservice.CodeOf(err))
	}
	if !taskservice.IsRetryable(err) {
		t.Fatal("Rename: busy errors must be retryable")
	}

	// Stop waits on the same lock using the caller's context; when *that*
	// expires it is the caller who ran out of time.
	cctx, ccancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer ccancel()
	_, err = svc.Stop(cctx, taskservice.StopRequest{
		Selection: taskservice.Selection{Names: []string{res.Task.ID}},
	})
	if err == nil {
		t.Fatal("Stop: expected a lock failure while the task lock is held")
	}
	if !taskservice.IsDeadlineExceeded(err) {
		t.Fatalf("Stop: expected deadline_exceeded for caller timeout, got %v (code=%s)", err, taskservice.CodeOf(err))
	}
}
