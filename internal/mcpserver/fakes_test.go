package mcpserver_test

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/philsphicas/bgtask/internal/state"
	"github.com/philsphicas/bgtask/internal/taskservice"
)

// fakeEnv is a single fake implementing both taskservice.Launcher and
// taskservice.ProcessController, so tests can drive a full task lifecycle
// (launch -> alive -> signaled -> dead) without spawning any real OS
// processes. It mirrors the fake used by internal/server's own route
// tests and by taskservice's own tests.
type fakeEnv struct {
	mu sync.Mutex

	nextPID    int
	alive      map[int]bool
	createTime map[int]int64
	ports      map[int][]uint32

	launchErr error
}

func newFakeEnv() *fakeEnv {
	return &fakeEnv{
		alive:      map[int]bool{},
		createTime: map[int]int64{},
		ports:      map[int][]uint32{},
	}
}

func (f *fakeEnv) Launch(_ context.Context, storeRoot, taskID string) (int, bool, error) {
	f.mu.Lock()
	if f.launchErr != nil {
		err := f.launchErr
		f.mu.Unlock()
		return 0, false, err
	}
	f.nextPID++
	pid := f.nextPID
	f.alive[pid] = true
	f.createTime[pid] = int64(pid)
	f.mu.Unlock()

	store := &state.Store{Root: storeRoot}
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
	defer f.mu.Unlock()
	f.alive[pid] = false
	return nil
}

func (f *fakeEnv) SignalRestart(_ string, _ int) error { return nil }

func (f *fakeEnv) SignalKill(pid int) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.alive[pid] = false
	return nil
}

func (f *fakeEnv) ListeningPorts(pid int) []uint32 {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.ports[pid]
}

// newTestService builds a taskservice.Service around a temp-dir store and
// a fresh fakeEnv wired as both Launcher and ProcessController, with delays
// tuned down so tests run fast.
func newTestService(t *testing.T) (*taskservice.Service, *fakeEnv) {
	t.Helper()
	store := &state.Store{Root: t.TempDir()}
	env := newFakeEnv()
	svc := &taskservice.Service{
		Store:               store,
		Launcher:            env,
		Process:             env,
		StopTimeout:         2 * time.Second,
		StartupCheckDelay:   0,
		StartupReadyTimeout: 2 * time.Second,
		StartupPollInterval: time.Millisecond,
	}
	return svc, env
}
