package plugin

import (
	"context"
	"errors"
	"math"
	"sync"
	"time"

	"github.com/Calcium-Ion/moejs"
)

// errNeverSettles ends a call whose hook waits on a promise that nothing is
// left to settle: no host work and no timer is still to come back.
var errNeverSettles = errors.New("the hook waits on a promise that never settles")

// maxTimers bounds the timers one call may have set at once.
const maxTimers = 64

// asyncLoop runs the continuations of one call of an apiVersion 2 hook: the
// promises host functions settle once their work is done, and the timers the
// call set. Only the goroutine that called the hook runs them, as it alone
// uses the runtime; host work on other goroutines posts tasks to it. Once the
// hook's promise settles the loop closes, and whatever is still to come back
// is dropped.
type asyncLoop struct {
	mutex sync.Mutex
	tasks []func(*moejs.Runtime) error
	wake  chan struct{}
	// pending counts the host work and timers whose task is still to come.
	pending int
	closed  bool
	timers  map[int64]*loopTimer
	lastID  int64
}

// loopTimer is one setTimeout or setInterval of a call.
type loopTimer struct {
	id       int64
	callback moejs.Value
	args     []moejs.Value
	delay    time.Duration
	repeat   bool
	timer    *time.Timer
	// fired is set once a timeout posted its task; cleared once
	// clearTimeout or clearInterval took it back.
	fired, cleared bool
}

func newAsyncLoop() *asyncLoop {
	return &asyncLoop{wake: make(chan struct{}, 1), timers: make(map[int64]*loopTimer)}
}

// begin counts host work in progress and returns the function that posts
// the task settling it. The first call counts; a loop that closed drops it.
func (loop *asyncLoop) begin() func(task func(*moejs.Runtime) error) {
	loop.mutex.Lock()
	loop.pending++
	loop.mutex.Unlock()
	var once sync.Once
	return func(task func(*moejs.Runtime) error) {
		once.Do(func() {
			loop.mutex.Lock()
			if loop.closed {
				loop.mutex.Unlock()
				return
			}
			loop.pending--
			loop.tasks = append(loop.tasks, task)
			loop.mutex.Unlock()
			loop.signal()
		})
	}
}

func (loop *asyncLoop) signal() {
	select {
	case loop.wake <- struct{}{}:
	default:
	}
}

// take returns the next task when one is queued. It reports
// errNeverSettles when none is queued and none is still to come.
func (loop *asyncLoop) take() (func(*moejs.Runtime) error, error) {
	loop.mutex.Lock()
	defer loop.mutex.Unlock()
	if len(loop.tasks) > 0 {
		task := loop.tasks[0]
		loop.tasks[0] = nil
		loop.tasks = loop.tasks[1:]
		return task, nil
	}
	if loop.pending == 0 {
		return nil, errNeverSettles
	}
	return nil, nil
}

// wait returns the next task, waiting for one while host work or a timer is
// still to come.
func (loop *asyncLoop) wait(ctx context.Context) (func(*moejs.Runtime) error, error) {
	for {
		task, err := loop.take()
		if task != nil || err != nil {
			return task, err
		}
		select {
		case <-loop.wake:
		case <-ctx.Done():
			return nil, context.Cause(ctx)
		}
	}
}

// close stops the call's timers and drops what is still to come.
func (loop *asyncLoop) close() {
	loop.mutex.Lock()
	defer loop.mutex.Unlock()
	loop.closed = true
	for _, timer := range loop.timers {
		timer.timer.Stop()
	}
	clear(loop.timers)
	clear(loop.tasks)
	loop.tasks = nil
}

// setTimer schedules callback after delay, again and again when repeat.
func (loop *asyncLoop) setTimer(callback moejs.Value, args []moejs.Value, delay time.Duration, repeat bool) (int64, bool) {
	loop.mutex.Lock()
	defer loop.mutex.Unlock()
	if loop.closed || len(loop.timers) >= maxTimers {
		return 0, false
	}
	loop.lastID++
	timer := &loopTimer{id: loop.lastID, callback: callback, args: args, delay: delay, repeat: repeat}
	loop.timers[timer.id] = timer
	loop.pending++
	timer.timer = time.AfterFunc(delay, func() { loop.fire(timer) })
	return timer.id, true
}

// fire posts the task that runs a timer's callback.
func (loop *asyncLoop) fire(timer *loopTimer) {
	loop.mutex.Lock()
	if loop.closed || timer.cleared {
		loop.mutex.Unlock()
		return
	}
	if !timer.repeat {
		timer.fired = true
		loop.pending--
		delete(loop.timers, timer.id)
	}
	loop.tasks = append(loop.tasks, func(runtime *moejs.Runtime) error {
		loop.mutex.Lock()
		skip := timer.cleared || loop.closed
		loop.mutex.Unlock()
		if skip {
			return nil
		}
		_, err := runtime.CallFunction(timer.callback, moejs.Undefined(), timer.args...)
		if err != nil {
			return err
		}
		if timer.repeat {
			loop.mutex.Lock()
			if !timer.cleared && !loop.closed {
				timer.timer.Reset(timer.delay)
			}
			loop.mutex.Unlock()
		}
		return nil
	})
	loop.mutex.Unlock()
	loop.signal()
}

// clearTimer cancels a timer that has not run, or an interval.
func (loop *asyncLoop) clearTimer(id int64) {
	loop.mutex.Lock()
	defer loop.mutex.Unlock()
	timer, found := loop.timers[id]
	if !found || timer.cleared {
		return
	}
	timer.cleared = true
	timer.timer.Stop()
	delete(loop.timers, id)
	if !timer.fired {
		loop.pending--
	}
}

// timerDelay reads a timer's delay in milliseconds as Node does: a delay that
// is not a number, below 1 or past 2^31-1 is 1 ms.
func timerDelay(realm *moejs.Realm, value moejs.Value) (time.Duration, error) {
	if value.IsUndefined() {
		return time.Millisecond, nil
	}
	number, err := realm.ToNumber(value)
	if err != nil {
		return 0, err
	}
	if math.IsNaN(number) || number < 1 || number > math.MaxInt32 {
		return time.Millisecond, nil
	}
	return time.Duration(number) * time.Millisecond, nil
}

// settle is how a host function's work settles its promise, on the loop,
// where JavaScript values can be made: a value fulfills it, an error rejects
// it with what a host function returning that error would throw.
type settle func(runtime *moejs.Runtime) (moejs.Value, error)

// async returns a promise that work settles. work runs on a goroutine of its
// own with the call's context and returns how to settle the promise.
func (c *call) async(work func(ctx context.Context) settle) moejs.Value {
	promise, resolve, reject := c.runtime.NewPromise()
	done := c.loop.begin()
	go func() {
		result := work(c.ctx)
		done(func(runtime *moejs.Runtime) error {
			value, err := result(runtime)
			if err != nil {
				reason, ok := runtime.ThrownValue(err)
				if !ok {
					return err
				}
				return reject(reason)
			}
			return resolve(value)
		})
	}()
	return promise
}

// drive runs the call's loop until promise settles, and returns what it
// fulfilled with. A rejection is the call's exception. While no task is
// ready, the call waits without its execution slot or script time.
func (c *call) drive(runtime *moejs.Runtime, promise moejs.Value) (moejs.Value, error) {
	for {
		state, result, _ := moejs.PromiseResult(promise)
		switch state {
		case moejs.PromiseFulfilled:
			return result, nil
		case moejs.PromiseRejected:
			return moejs.Undefined(), &moejs.Exception{Value: result}
		}
		task, err := c.loop.take()
		if err != nil {
			return moejs.Undefined(), err
		}
		if task == nil {
			if task, err = c.idle(); err != nil {
				return moejs.Undefined(), err
			}
		}
		if err := task(runtime); err != nil {
			return moejs.Undefined(), err
		}
	}
}

// idle waits for the next task. The wait does not count as script time, and
// the call's execution slot goes to another call meanwhile.
func (c *call) idle() (func(*moejs.Runtime) error, error) {
	c.watchdog.pause()
	if c.slot {
		c.pool.release()
		c.slot = false
	}
	task, err := c.loop.wait(c.ctx)
	if err != nil {
		return nil, err
	}
	if err := c.pool.acquire(c.ctx); err != nil {
		return nil, err
	}
	c.slot = true
	c.watchdog.start()
	return task, nil
}

// promiseLike reports whether value is a promise the call has to wait for.
func promiseLike(value moejs.Value) bool {
	_, _, ok := moejs.PromiseResult(value)
	return ok
}
