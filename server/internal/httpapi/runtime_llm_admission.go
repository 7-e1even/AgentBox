package httpapi

import (
	"sync"
	"time"
)

const (
	runtimeLLMResolutionConcurrency = 8
	runtimeLLMGlobalConcurrency     = 64
	runtimeLLMSandboxConcurrency    = 4
	runtimeLLMGlobalRequestsMinute  = 600
	runtimeLLMSandboxRequestsMinute = 60
	runtimeLLMAdmissionMaxSandboxes = 4096
	runtimeLLMAdmissionWindow       = time.Minute
)

type runtimeLLMAdmissionLimits struct {
	resolutionConcurrency int
	globalConcurrency     int
	sandboxConcurrency    int
	globalRate            int
	sandboxRate           int
	maxSandboxes          int
	window                time.Duration
}

var defaultRuntimeLLMAdmissionLimits = runtimeLLMAdmissionLimits{
	resolutionConcurrency: runtimeLLMResolutionConcurrency,
	globalConcurrency:     runtimeLLMGlobalConcurrency,
	sandboxConcurrency:    runtimeLLMSandboxConcurrency,
	globalRate:            runtimeLLMGlobalRequestsMinute,
	sandboxRate:           runtimeLLMSandboxRequestsMinute,
	maxSandboxes:          runtimeLLMAdmissionMaxSandboxes,
	window:                runtimeLLMAdmissionWindow,
}

type runtimeLLMSandboxAdmission struct {
	active int
	starts []time.Time
}

type runtimeLLMAdmission struct {
	mu               sync.Mutex
	limits           runtimeLLMAdmissionLimits
	resolutionActive int
	resolutionStarts []time.Time
	globalActive     int
	sandboxes        map[string]*runtimeLLMSandboxAdmission
}

func newRuntimeLLMAdmission(limits runtimeLLMAdmissionLimits) *runtimeLLMAdmission {
	return &runtimeLLMAdmission{limits: limits, sandboxes: make(map[string]*runtimeLLMSandboxAdmission)}
}

// acquireResolution is called only after the token's signed sandbox claim has
// been checked without database access. It bounds database work globally and
// applies request-rate limits before target resolution starts.
func (a *runtimeLLMAdmission) acquireResolution(sandboxID string, now time.Time) (func(), bool) {
	a.mu.Lock()
	defer a.mu.Unlock()
	cutoff := now.Add(-a.limits.window)
	a.resolutionStarts = retainRuntimeLLMStarts(a.resolutionStarts, cutoff)
	if a.resolutionActive >= a.limits.resolutionConcurrency || len(a.resolutionStarts) >= a.limits.globalRate {
		return nil, false
	}
	state := a.sandboxes[sandboxID]
	if state == nil {
		if len(a.sandboxes) >= a.limits.maxSandboxes {
			a.cleanupSandboxes(cutoff)
		}
		if len(a.sandboxes) >= a.limits.maxSandboxes {
			return nil, false
		}
		state = &runtimeLLMSandboxAdmission{}
		a.sandboxes[sandboxID] = state
	} else {
		state.starts = retainRuntimeLLMStarts(state.starts, cutoff)
	}
	if len(state.starts) >= a.limits.sandboxRate {
		return nil, false
	}
	a.resolutionActive++
	a.resolutionStarts = append(a.resolutionStarts, now)
	state.starts = append(state.starts, now)
	return sync.OnceFunc(func() {
		a.mu.Lock()
		defer a.mu.Unlock()
		a.resolutionActive--
	}), true
}

// acquireTarget is called only after the signed runtime token has resolved to
// an authoritative sandbox ID. Its lease spans the upstream request.
func (a *runtimeLLMAdmission) acquireTarget(sandboxID string, now time.Time) (func(), bool) {
	a.mu.Lock()
	defer a.mu.Unlock()
	cutoff := now.Add(-a.limits.window)
	state := a.sandboxes[sandboxID]
	if state == nil {
		a.cleanupSandboxes(cutoff)
		if len(a.sandboxes) >= a.limits.maxSandboxes {
			return nil, false
		}
		state = &runtimeLLMSandboxAdmission{}
		a.sandboxes[sandboxID] = state
	}
	state.starts = retainRuntimeLLMStarts(state.starts, cutoff)
	if a.globalActive >= a.limits.globalConcurrency || state.active >= a.limits.sandboxConcurrency {
		return nil, false
	}
	a.globalActive++
	state.active++
	return sync.OnceFunc(func() {
		a.mu.Lock()
		defer a.mu.Unlock()
		a.globalActive--
		state.active--
	}), true
}

func (a *runtimeLLMAdmission) cleanupSandboxes(cutoff time.Time) {
	for sandboxID, state := range a.sandboxes {
		state.starts = retainRuntimeLLMStarts(state.starts, cutoff)
		if state.active == 0 && len(state.starts) == 0 {
			delete(a.sandboxes, sandboxID)
		}
	}
}

func retainRuntimeLLMStarts(starts []time.Time, cutoff time.Time) []time.Time {
	kept := starts[:0]
	for _, started := range starts {
		if started.After(cutoff) {
			kept = append(kept, started)
		}
	}
	return kept
}
