package pluginhost

import (
	"context"
	"sync"
	"time"

	"github.com/QuantumNous/astrlink/core/internal/plugin"
)

// The observeTurn queue: 256 captures waiting at most, two running at once,
// each with 30 s of network. A capture that finds the queue full is dropped
// and counted; a failed one never touches its request.
const (
	observeQueueSize = 256
	observeWorkers   = 2
	observeTimeout   = 30 * time.Second
)

type observeJob struct {
	plugin *plugin.Plugin
	view   TurnView
	info   plugin.RequestInfo
}

type observeQueue struct {
	jobs    chan observeJob
	stop    chan struct{}
	once    sync.Once
	workers sync.WaitGroup
	logf    func(string, ...any)

	mutex   sync.Mutex
	dropped map[string]int
}

func newObserveQueue(logf func(string, ...any)) *observeQueue {
	queue := &observeQueue{
		jobs: make(chan observeJob, observeQueueSize), stop: make(chan struct{}),
		logf: logf, dropped: map[string]int{},
	}
	queue.workers.Add(observeWorkers)
	for range observeWorkers {
		go queue.work()
	}
	return queue
}

// push queues a capture; false when the queue is full or stopped.
func (queue *observeQueue) push(job observeJob) bool {
	select {
	case <-queue.stop:
		return false
	default:
	}
	select {
	case queue.jobs <- job:
		return true
	default:
		queue.mutex.Lock()
		queue.dropped[job.plugin.Key]++
		queue.mutex.Unlock()
		return false
	}
}

// Dropped counts, by plugin, the captures a full queue dropped since Core
// started.
func (host *Host) Dropped() map[string]int {
	if host == nil {
		return nil
	}
	queue := host.observe
	queue.mutex.Lock()
	defer queue.mutex.Unlock()
	counts := make(map[string]int, len(queue.dropped))
	for key, count := range queue.dropped {
		counts[key] = count
	}
	return counts
}

func (queue *observeQueue) work() {
	defer queue.workers.Done()
	for {
		select {
		case <-queue.stop:
			return
		case job := <-queue.jobs:
			ctx, cancel := context.WithTimeout(context.Background(), observeTimeout)
			_, _, err := job.plugin.Call(ctx, plugin.HookObserveTurn, job.view, plugin.CallOptions{
				Request: &job.info, NetworkBudget: observeTimeout,
			})
			cancel()
			if err != nil {
				queue.logf("plugin %s observeTurn: %v", job.plugin.Key, err)
			}
		}
	}
}

func (queue *observeQueue) close() {
	queue.once.Do(func() { close(queue.stop) })
	queue.workers.Wait()
}
