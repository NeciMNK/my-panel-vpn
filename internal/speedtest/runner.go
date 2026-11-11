package speedtest

import (
	"context"
	"sync"
	"time"
)

// State is a server's speed-test progress as the master polls it: Run numbers
// the runs since the panel started, so a restart mid-run is detectable.
type State struct {
	Run        int64   `json:"run" example:"3"`
	Running    bool    `json:"running" example:"false"`
	StartedAt  int64   `json:"startedAt" example:"1762874400000"`
	FinishedAt int64   `json:"finishedAt" example:"1762874430000"`
	Result     *Result `json:"result,omitempty"`
	Error      string  `json:"error,omitempty" example:""`
}

// runTimeout bounds one run: both services' list, ping, download and upload.
const runTimeout = 3 * time.Minute

// Runner runs at most one measurement at a time on this server.
type Runner struct {
	mu      sync.Mutex
	state   State
	measure func(context.Context) (Result, error)
}

func NewRunner(measure func(context.Context) (Result, error)) *Runner {
	return &Runner{measure: measure}
}

// Local is this server's runner, shared by the panel's own tests and the
// master's requests.
var Local = NewRunner(func(ctx context.Context) (Result, error) { return Measure(ctx, Default()...) })

// Start begins a run unless one is going, and returns the run to wait for.
func (r *Runner) Start() State {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.state.Running {
		return r.state
	}
	r.state = State{Run: r.state.Run + 1, Running: true, StartedAt: time.Now().UnixMilli()}
	go r.run()
	return r.state
}

func (r *Runner) State() State {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.state
}

func (r *Runner) run() {
	ctx, cancel := context.WithTimeout(context.Background(), runTimeout)
	defer cancel()
	res, err := r.measure(ctx)
	r.mu.Lock()
	defer r.mu.Unlock()
	r.state.Running, r.state.FinishedAt = false, time.Now().UnixMilli()
	if err != nil {
		r.state.Error = err.Error()
		return
	}
	r.state.Result = &res
}
