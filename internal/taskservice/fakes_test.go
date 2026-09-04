package taskservice_test

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/philsphicas/bgtask/internal/state"
	"github.com/philsphicas/bgtask/internal/taskservice"
)

// fakeEnv is a single fake implementing both taskservice.Launcher and
// taskservice.ProcessController, so tests can simulate a full task
// lifecycle (launch -> alive -> signaled -> dead) without spawning any
// real OS processes.
type fakeEnv struct {
	mu sync.Mutex

	nextPID    int
	alive      map[int]bool
	deadAfter  map[int]time.Time
	createTime map[int]int64
	ports      map[int][]uint32
	portCalls  int

	launches    int
	launchErr   error
	restarts    int
	kills       []int
	stopDelay   time.Duration // simulated delay before a SignalStop takes effect
	restartFunc func(taskDir string, pid int) error

	// Readiness-simulation knobs. By default Launch behaves like a healthy
	// supervisor: it writes supervisor.pid + createtime and stays alive.
	//
	// skipPIDWrite makes Launch return a live PID without ever writing
	// supervisor.pid, i.e. a supervisor that hangs before signaling
	// readiness.
	skipPIDWrite bool
	// dieOnLaunch makes Launch return a PID that is already dead and that
	// never wrote supervisor.pid: a supervisor that crashed instantly.
	dieOnLaunch bool
	// exitOnLaunch makes Launch write exit.json (and mark the PID dead)
	// instead of signaling readiness: a command fast enough to finish
	// before the caller could observe it running.
	exitOnLaunch *state.Exit
	// removeOnLaunch makes Launch delete the task's state directory, as an
	// --rm supervisor does when its command finishes immediately.
	removeOnLaunch bool

	// onSignalStop, if set, runs at the start of every SignalStop. Tests
	// use it to inject concurrent activity (e.g. canceling the caller's
	// context) part-way through a destructive stop.
	onSignalStop func()
}

func newFakeEnv() *fakeEnv {
	return &fakeEnv{
		alive:      map[int]bool{},
		deadAfter:  map[int]time.Time{},
		createTime: map[int]int64{},
		ports:      map[int][]uint32{},
	}
}

// Launch implements taskservice.Launcher: it fabricates a PID, writes it to
// the task's supervisor.pid (as the real supervisor would on startup), and
// marks it alive.
func (f *fakeEnv) Launch(_ context.Context, storeRoot, taskID string) (int, bool, error) {
	f.mu.Lock()
	if f.launchErr != nil {
		err := f.launchErr
		f.mu.Unlock()
		return 0, false, err
	}
	f.launches++
	f.nextPID++
	pid := f.nextPID
	f.alive[pid] = !f.dieOnLaunch && f.exitOnLaunch == nil
	f.createTime[pid] = int64(pid)
	skipPID := f.skipPIDWrite || f.dieOnLaunch
	exit := f.exitOnLaunch
	remove := f.removeOnLaunch
	f.mu.Unlock()

	store := &state.Store{Root: storeRoot}
	if remove {
		if err := store.Remove(taskID); err != nil {
			return 0, false, err
		}
		return pid, false, nil
	}
	if exit != nil {
		if err := store.WriteExit(taskID, exit); err != nil {
			return 0, false, err
		}
		return pid, false, nil
	}
	if skipPID {
		return pid, false, nil
	}
	if err := store.WritePID(taskID, "supervisor.pid", pid); err != nil {
		return 0, false, err
	}
	if err := store.WriteCreateTime(taskID, int64(pid)); err != nil {
		return 0, false, err
	}
	return pid, false, nil
}

func (f *fakeEnv) IsAlive(pid int) bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	if until, ok := f.deadAfter[pid]; ok && !time.Now().Before(until) {
		delete(f.deadAfter, pid)
		f.alive[pid] = false
	}
	return f.alive[pid]
}

func (f *fakeEnv) VerifyPID(pid int, createTime int64) bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	if createTime == 0 {
		return true
	}
	got, ok := f.createTime[pid]
	return !ok || got == createTime
}

func (f *fakeEnv) CreateTime(pid int) int64 {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.createTime[pid]
}

func (f *fakeEnv) SignalStop(_ string, pid int) error {
	f.mu.Lock()
	hook := f.onSignalStop
	f.mu.Unlock()
	if hook != nil {
		hook()
	}

	f.mu.Lock()
	defer f.mu.Unlock()
	if f.stopDelay > 0 {
		f.deadAfter[pid] = time.Now().Add(f.stopDelay)
	} else {
		f.alive[pid] = false
	}
	return nil
}

func (f *fakeEnv) SignalRestart(taskDir string, pid int) error {
	f.mu.Lock()
	f.restarts++
	fn := f.restartFunc
	f.mu.Unlock()
	if fn != nil {
		return fn(taskDir, pid)
	}
	return nil
}

func (f *fakeEnv) SignalKill(pid int) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.alive[pid] = false
	f.kills = append(f.kills, pid)
	return nil
}

func (f *fakeEnv) ListeningPorts(pid int) []uint32 {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.portCalls++
	return f.ports[pid]
}

func (f *fakeEnv) portCallCount() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.portCalls
}

func (f *fakeEnv) launchCount() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.launches
}

func (f *fakeEnv) restartCount() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.restarts
}

func (f *fakeEnv) killCount(pid int) int {
	f.mu.Lock()
	defer f.mu.Unlock()
	n := 0
	for _, k := range f.kills {
		if k == pid {
			n++
		}
	}
	return n
}

// newTestService builds a Service around a temp-dir store and a fresh
// fakeEnv wired as both Launcher and ProcessController. StartupCheckDelay
// is zero so Run's tests don't pay the real production delay.
func newTestService(t *testing.T) (*taskservice.Service, *fakeEnv) {
	t.Helper()
	store := &state.Store{Root: t.TempDir()}
	env := newFakeEnv()
	svc := &taskservice.Service{
		Store:             store,
		Launcher:          env,
		Process:           env,
		StopTimeout:       2 * time.Second,
		StartupCheckDelay: 0,
	}
	return svc, env
}

// mustRun launches a task via svc.Run and fails the test on error.
func mustRun(t *testing.T, svc *taskservice.Service, name string, cmd []string) *taskservice.RunResult {
	t.Helper()
	result, err := svc.Run(context.Background(), taskservice.RunRequest{
		Name:            name,
		Command:         cmd,
		Cwd:             ".",
		ReplaceExisting: true,
	})
	if err != nil {
		t.Fatalf("Run(%q): %v", name, err)
	}
	return result
}

// stopTaskDirectly marks a running task's supervisor PID dead in the fake
// environment and clears its supervisor.pid-derived aliveness, without
// going through Service.Stop, so tests can set up a "stopped" precondition
// cheaply. It leaves meta.json (including the task's name) intact -- the
// caller must not expect exit.json to be written.
func markDead(env *fakeEnv, pid int) {
	env.mu.Lock()
	defer env.mu.Unlock()
	env.alive[pid] = false
}
