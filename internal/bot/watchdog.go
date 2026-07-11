package bot

// watchdog.go —— 异步流控工具集（对齐 TS bot/watchdog）。
// Semaphore（全局并发上限 FIFO 信号量）+ GracefulInterrupt（⏹ 优雅中断）+ WithIdleTimeout（idle watchdog）。

import (
	"sync"
	"time"
)

// ── Semaphore（M-3 排队可见可取消）─────────────────────────────────

// Semaphore FIFO 信号量：全局并发上限。
type Semaphore struct {
	mu      sync.Mutex
	active  int
	max     int
	waiters []*semWaiter
}

type semWaiter struct {
	grant     chan struct{}
	onAdvance func(pos int)
}

// NewSemaphore 构造。
func NewSemaphore(max int) *Semaphore { return &Semaphore{max: max} }

// SetLimit 运行时调整并发上限（设置页改并发后生效）。
func (s *Semaphore) SetLimit(n int) {
	if n < 1 {
		n = 1
	}
	s.mu.Lock()
	s.max = n
	s.mu.Unlock()
}

// HasFree 是否有空闲槽（不排队即授予）。
func (s *Semaphore) HasFree() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.active < s.max
}

// QueuedAcquire 排队获取句柄（位置可见 + 可取消）。
type QueuedAcquire struct {
	sem       *Semaphore
	waiter    *semWaiter
	grantCh   chan struct{}
	cancelCh  chan struct{}
	mu        sync.Mutex
	cancelled bool
}

// Wait 等待槽位授予；返回 release 函数，或 nil（被取消）。
func (q *QueuedAcquire) Wait() (release func(), ok bool) {
	select {
	case <-q.grantCh:
		return q.sem.release(), true
	case <-q.cancelCh:
		return nil, false
	}
}

// Position 当前排队位置（1-based，0=已授予/已取消）。
func (q *QueuedAcquire) Position() int {
	q.sem.mu.Lock()
	defer q.sem.mu.Unlock()
	for i, w := range q.sem.waiters {
		if w == q.waiter {
			return i + 1
		}
	}
	return 0
}

// Cancel 排队中取消。true=取消成功；false=已授予/已取消（需路由到运行中 turn）。
func (q *QueuedAcquire) Cancel() bool {
	q.mu.Lock()
	defer q.mu.Unlock()
	if q.cancelled {
		return false
	}
	q.sem.mu.Lock()
	for i, w := range q.sem.waiters {
		if w == q.waiter {
			q.sem.waiters = append(q.sem.waiters[:i], q.sem.waiters[i+1:]...)
			q.cancelled = true
			close(q.cancelCh)
			q.sem.mu.Unlock()
			q.sem.notifyAdvance(i)
			return true
		}
	}
	q.sem.mu.Unlock()
	return false
}

// Enqueue 入队获取（onAdvance 位置变化通知）。
func (s *Semaphore) Enqueue(onAdvance func(pos int)) *QueuedAcquire {
	w := &semWaiter{grant: make(chan struct{}), onAdvance: onAdvance}
	q := &QueuedAcquire{sem: s, waiter: w, grantCh: w.grant, cancelCh: make(chan struct{})}
	s.mu.Lock()
	if s.active < s.max {
		s.active++
		close(w.grant)
	} else {
		s.waiters = append(s.waiters, w)
	}
	s.mu.Unlock()
	return q
}

// Acquire 简单获取（不可取消）。
func (s *Semaphore) Acquire() func() {
	q := s.Enqueue(nil)
	release, _ := q.Wait()
	return release
}

func (s *Semaphore) release() func() {
	return func() {
		s.mu.Lock()
		s.active--
		if len(s.waiters) > 0 {
			next := s.waiters[0]
			s.waiters = s.waiters[1:]
			s.active++
			close(next.grant)
			s.mu.Unlock()
			s.notifyAdvance(0)
		} else {
			s.mu.Unlock()
		}
	}
}

func (s *Semaphore) notifyAdvance(from int) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for i := from; i < len(s.waiters); i++ {
		if s.waiters[i].onAdvance != nil {
			s.waiters[i].onAdvance(i + 1)
		}
	}
}

// ── GracefulInterrupt（⏹ 优雅中断）──────────────────────────────

const InterruptDrainTimeoutMS = 5000

// GracefulInterrupt ⏹ 优雅中断控制器。
type GracefulInterrupt struct {
	mu          sync.Mutex
	interrupted bool
	forced      bool
	timer       *time.Timer
	disposed    bool
	turnID      func() string
	abortFn     func(turnID string)
	forceStop   func()
	timeoutMS   int
}

// NewGracefulInterrupt 构造。
func NewGracefulInterrupt(turnID func() string, abort func(string), forceStop func(), timeoutMS int) *GracefulInterrupt {
	if timeoutMS <= 0 {
		timeoutMS = InterruptDrainTimeoutMS
	}
	return &GracefulInterrupt{turnID: turnID, abortFn: abort, forceStop: forceStop, timeoutMS: timeoutMS}
}

// Interrupt ⏹ 入口（幂等：重复点击只 abort 一次）。
func (g *GracefulInterrupt) Interrupt() {
	g.mu.Lock()
	defer g.mu.Unlock()
	if g.interrupted || g.disposed {
		return
	}
	g.interrupted = true
	tid := g.turnID()
	if tid == "" {
		g.forced = true
		g.forceStop()
		return
	}
	g.abortFn(tid)
	g.timer = time.AfterFunc(time.Duration(g.timeoutMS)*time.Millisecond, func() {
		g.mu.Lock()
		g.forced = true
		g.mu.Unlock()
		g.forceStop()
	})
}

// Interrupted ⏹ 是否被点过。
func (g *GracefulInterrupt) Interrupted() bool {
	g.mu.Lock()
	defer g.mu.Unlock()
	return g.interrupted
}

// Forced 是否走了强停（杀进程恢复锤）。
func (g *GracefulInterrupt) Forced() bool {
	g.mu.Lock()
	defer g.mu.Unlock()
	return g.forced
}

// Dispose 事件流收尾后清掉兜底定时器（幂等）。
func (g *GracefulInterrupt) Dispose() {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.disposed = true
	if g.timer != nil {
		g.timer.Stop()
		g.timer = nil
	}
}
