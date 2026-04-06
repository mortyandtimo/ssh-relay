package api

import (
	"context"
	"sync"

	"github.com/25743/cloud-relay-platform/packages/protocol/types"
)

// BlockingControlExecutionFixture is a test-only hook that pauses the first
// matching execute path after duplicate-detection has already marked the
// request active. It lets external integration tests exercise duplicate-
// inflight responses against a real server process without changing runtime
// execute semantics for normal deployments.
type BlockingControlExecutionFixture struct {
	started chan struct{}
	release chan struct{}
	once    sync.Once
}

type fixtureBlockingControlExecutor struct {
	next       controlExecutor
	actionKind types.ControlActionKind
	targetKind types.ControlTargetKind
	targetID   string
	started    chan struct{}
	release    chan struct{}
	mu         sync.Mutex
	blocked    bool
	startedNow sync.Once
}

func (s *Server) UseBlockingControlExecutionFixture(actionKind types.ControlActionKind, targetKind types.ControlTargetKind, targetID string) *BlockingControlExecutionFixture {
	fixture := &BlockingControlExecutionFixture{
		started: make(chan struct{}),
		release: make(chan struct{}),
	}
	s.controlExecutor = &fixtureBlockingControlExecutor{
		next:       s.controlExecutor,
		actionKind: actionKind,
		targetKind: targetKind,
		targetID:   targetID,
		started:    fixture.started,
		release:    fixture.release,
	}
	return fixture
}

func (f *BlockingControlExecutionFixture) Started() bool {
	select {
	case <-f.started:
		return true
	default:
		return false
	}
}

func (f *BlockingControlExecutionFixture) Release() {
	f.once.Do(func() {
		close(f.release)
	})
}

func (e *fixtureBlockingControlExecutor) execute(ctx context.Context, plan controlExecutionPlan) controlExecutionResult {
	if e.shouldBlock(plan) {
		e.startedNow.Do(func() {
			close(e.started)
		})
		select {
		case <-e.release:
		case <-ctx.Done():
			return retryableFailureExecutionResult(plan, "测试夹具中的阻塞执行被取消。")
		}
	}
	return e.next.execute(ctx, plan)
}

func (e *fixtureBlockingControlExecutor) shouldBlock(plan controlExecutionPlan) bool {
	if plan.actionKind != e.actionKind || plan.targetKind != e.targetKind || plan.targetID != e.targetID {
		return false
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	if e.blocked {
		return false
	}
	e.blocked = true
	return true
}
