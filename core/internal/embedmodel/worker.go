package embedmodel

import (
	"bufio"
	"context"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os/exec"
	"strings"
	"sync"
	"time"
)

// Kind tells the model whether a text is a search query or a passage to be
// found; some models prefix the two differently.
type Kind string

const (
	Query   Kind = "query"
	Passage Kind = "passage"
)

const (
	protocolVersion = 1
	maxFrameBytes   = 64 << 20
	// Peak memory grows with the batch. Granite in fp32 holds about 0.75 GB
	// once loaded; eight 512-token passages took it to 1.08 GB, four keep the
	// peak near 0.9 GB.
	batchSize        = 4
	readyTimeout     = 2 * time.Minute
	defaultIdleAfter = 10 * time.Minute
)

// ErrWorkerFailed reports a worker that could not load the model or answer.
var ErrWorkerFailed = errors.New("the embedding worker failed")

// Worker runs the inference worker in embedding mode for one installation at
// a time. Requests are served one after another; after IdleAfter without one
// the process stops, which frees the model's memory.
type Worker struct {
	Executable string
	IdleAfter  time.Duration

	mu      sync.Mutex
	process *workerProcess
	idle    *time.Timer
	nextID  uint64
}

type workerProcess struct {
	directory string
	command   *exec.Cmd
	stdin     io.WriteCloser
	reader    *bufio.Reader
	stderr    *tailWriter
	done      chan struct{}
}

type embedRequest struct {
	Version int      `json:"version"`
	ID      uint64   `json:"id"`
	Kind    Kind     `json:"kind"`
	Texts   []string `json:"texts"`
}

type embedResponse struct {
	Version int         `json:"version"`
	ID      uint64      `json:"id"`
	Vectors [][]float32 `json:"vectors"`
	Error   *struct {
		Code string `json:"code"`
	} `json:"error"`
}

// Embed returns one vector per text, in order.
func (worker *Worker) Embed(ctx context.Context, installation Installation, kind Kind, texts []string) ([][]float32, error) {
	if len(texts) == 0 {
		return nil, nil
	}
	worker.mu.Lock()
	defer worker.mu.Unlock()
	if worker.idle != nil {
		worker.idle.Stop()
	}
	defer worker.armIdleLocked()
	process, err := worker.ensureLocked(ctx, installation)
	if err != nil {
		return nil, err
	}
	vectors := make([][]float32, 0, len(texts))
	for start := 0; start < len(texts); start += batchSize {
		end := min(start+batchSize, len(texts))
		batch, err := worker.exchangeLocked(ctx, process, kind, texts[start:end])
		if err != nil {
			worker.stopLocked()
			return nil, err
		}
		if len(batch) != end-start {
			worker.stopLocked()
			return nil, fmt.Errorf("%w: %d vectors for %d texts", ErrWorkerFailed, len(batch), end-start)
		}
		for _, vector := range batch {
			if len(vector) != installation.Model.Dimensions {
				worker.stopLocked()
				return nil, fmt.Errorf("%w: a %d-dimensional vector", ErrWorkerFailed, len(vector))
			}
		}
		vectors = append(vectors, batch...)
	}
	return vectors, nil
}

// Running reports whether the worker process is up.
func (worker *Worker) Running() bool {
	worker.mu.Lock()
	defer worker.mu.Unlock()
	return worker.process != nil && worker.process.running()
}

// Close stops the worker process.
func (worker *Worker) Close() {
	worker.mu.Lock()
	defer worker.mu.Unlock()
	if worker.idle != nil {
		worker.idle.Stop()
	}
	worker.stopLocked()
}

func (worker *Worker) armIdleLocked() {
	after := worker.IdleAfter
	if after <= 0 {
		after = defaultIdleAfter
	}
	worker.idle = time.AfterFunc(after, func() {
		worker.mu.Lock()
		defer worker.mu.Unlock()
		worker.stopLocked()
	})
}

func (worker *Worker) ensureLocked(ctx context.Context, installation Installation) (*workerProcess, error) {
	if worker.process != nil && worker.process.directory == installation.Directory && worker.process.running() {
		return worker.process, nil
	}
	worker.stopLocked()
	command := exec.Command(worker.Executable, "--embedding-model-dir", installation.Directory)
	stderr := &tailWriter{max: 4 << 10}
	command.Stderr = stderr
	stdin, err := command.StdinPipe()
	if err != nil {
		return nil, err
	}
	stdout, err := command.StdoutPipe()
	if err != nil {
		return nil, err
	}
	if err := command.Start(); err != nil {
		return nil, fmt.Errorf("start the embedding worker: %w", err)
	}
	process := &workerProcess{
		directory: installation.Directory,
		command:   command,
		stdin:     stdin,
		reader:    bufio.NewReader(stdout),
		stderr:    stderr,
		done:      make(chan struct{}),
	}
	go func() {
		_ = command.Wait()
		close(process.done)
	}()
	worker.process = process
	if err := process.readReady(ctx); err != nil {
		worker.stopLocked()
		return nil, err
	}
	return process, nil
}

func (worker *Worker) exchangeLocked(ctx context.Context, process *workerProcess, kind Kind, texts []string) ([][]float32, error) {
	worker.nextID++
	request := embedRequest{Version: protocolVersion, ID: worker.nextID, Kind: kind, Texts: texts}
	payload, err := json.Marshal(request)
	if err != nil {
		return nil, err
	}
	type result struct {
		vectors [][]float32
		err     error
	}
	answer := make(chan result, 1)
	go func() {
		if err := writeFrame(process.stdin, payload); err != nil {
			answer <- result{err: err}
			return
		}
		frame, err := readFrame(process.reader)
		if err != nil {
			answer <- result{err: err}
			return
		}
		var response embedResponse
		if err := json.Unmarshal(frame, &response); err != nil {
			answer <- result{err: err}
			return
		}
		if response.ID != request.ID {
			answer <- result{err: fmt.Errorf("%w: answer for request %d", ErrWorkerFailed, response.ID)}
			return
		}
		if response.Error != nil {
			answer <- result{err: fmt.Errorf("%w: %s", ErrWorkerFailed, response.Error.Code)}
			return
		}
		answer <- result{vectors: response.Vectors}
	}()
	select {
	case got := <-answer:
		if got.err != nil && !errors.Is(got.err, ErrWorkerFailed) {
			return nil, fmt.Errorf("%w: %v%s", ErrWorkerFailed, got.err, process.stderr.describe())
		}
		return got.vectors, got.err
	case <-process.done:
		return nil, fmt.Errorf("%w: the worker exited%s", ErrWorkerFailed, process.stderr.describe())
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}

func (worker *Worker) stopLocked() {
	if worker.process == nil {
		return
	}
	worker.process.stop()
	worker.process = nil
}

func (process *workerProcess) running() bool {
	select {
	case <-process.done:
		return false
	default:
		return true
	}
}

func (process *workerProcess) readReady(ctx context.Context) error {
	ready := make(chan error, 1)
	go func() {
		frame, err := readFrame(process.reader)
		if err != nil {
			ready <- err
			return
		}
		var message struct {
			Version int  `json:"version"`
			Ready   bool `json:"ready"`
		}
		if err := json.Unmarshal(frame, &message); err != nil || message.Version != protocolVersion || !message.Ready {
			ready <- fmt.Errorf("unexpected ready frame")
			return
		}
		ready <- nil
	}()
	timer := time.NewTimer(readyTimeout)
	defer timer.Stop()
	select {
	case err := <-ready:
		if err != nil {
			return fmt.Errorf("%w: %v%s", ErrWorkerFailed, err, process.stderr.describe())
		}
		return nil
	case <-process.done:
		return fmt.Errorf("%w: the worker exited while loading the model%s", ErrWorkerFailed, process.stderr.describe())
	case <-timer.C:
		return fmt.Errorf("%w: the model did not load within %s", ErrWorkerFailed, readyTimeout)
	case <-ctx.Done():
		return ctx.Err()
	}
}

// stop closes stdin, which ends the worker's request loop, and kills a
// worker that does not exit promptly.
func (process *workerProcess) stop() {
	_ = process.stdin.Close()
	select {
	case <-process.done:
	case <-time.After(3 * time.Second):
		_ = process.command.Process.Kill()
		<-process.done
	}
}

func writeFrame(writer io.Writer, payload []byte) error {
	if len(payload) == 0 || len(payload) > maxFrameBytes {
		return fmt.Errorf("invalid frame length %d", len(payload))
	}
	var header [4]byte
	binary.BigEndian.PutUint32(header[:], uint32(len(payload)))
	if _, err := writer.Write(header[:]); err != nil {
		return err
	}
	_, err := writer.Write(payload)
	return err
}

func readFrame(reader io.Reader) ([]byte, error) {
	var header [4]byte
	if _, err := io.ReadFull(reader, header[:]); err != nil {
		return nil, err
	}
	length := binary.BigEndian.Uint32(header[:])
	if length == 0 || length > maxFrameBytes {
		return nil, fmt.Errorf("invalid frame length %d", length)
	}
	payload := make([]byte, length)
	if _, err := io.ReadFull(reader, payload); err != nil {
		return nil, err
	}
	return payload, nil
}

type tailWriter struct {
	mu   sync.Mutex
	data []byte
	max  int
}

func (writer *tailWriter) Write(data []byte) (int, error) {
	writer.mu.Lock()
	defer writer.mu.Unlock()
	writer.data = append(writer.data, data...)
	if len(writer.data) > writer.max {
		writer.data = append([]byte(nil), writer.data[len(writer.data)-writer.max:]...)
	}
	return len(data), nil
}

func (writer *tailWriter) describe() string {
	writer.mu.Lock()
	defer writer.mu.Unlock()
	text := strings.TrimSpace(string(writer.data))
	if text == "" {
		return ""
	}
	return ": " + text
}
