package httpapi

import (
	"testing"
	"time"
)

func TestRuntimeLLMResolutionAdmissionEnforcesConcurrencyAndReleases(t *testing.T) {
	limits := runtimeLLMAdmissionLimits{
		resolutionConcurrency: 2, globalConcurrency: 10, sandboxConcurrency: 10,
		globalRate: 10, sandboxRate: 10, maxSandboxes: 4, window: time.Minute,
	}
	admission := newRuntimeLLMAdmission(limits)
	now := time.Date(2026, time.September, 3, 0, 0, 0, 0, time.UTC)

	releaseOne, ok := admission.acquireResolution("sandbox-one", now)
	if !ok {
		t.Fatal("first resolution was rejected")
	}
	releaseTwo, ok := admission.acquireResolution("sandbox-two", now)
	if !ok {
		t.Fatal("second resolution was rejected before the limit")
	}
	if _, ok := admission.acquireResolution("sandbox-three", now); ok {
		t.Fatal("resolution concurrency limit was not enforced")
	}
	releaseOne()
	if releaseThree, allowed := admission.acquireResolution("sandbox-three", now); !allowed {
		t.Fatal("released capacity was not reusable")
	} else {
		releaseThree()
	}
	releaseTwo()
}

func TestRuntimeLLMResolutionAdmissionEnforcesRatesAndWindow(t *testing.T) {
	limits := runtimeLLMAdmissionLimits{
		resolutionConcurrency: 10, globalConcurrency: 10, sandboxConcurrency: 10,
		globalRate: 2, sandboxRate: 1, maxSandboxes: 4, window: time.Minute,
	}
	admission := newRuntimeLLMAdmission(limits)
	now := time.Date(2026, time.September, 3, 0, 0, 0, 0, time.UTC)

	releaseOne, ok := admission.acquireResolution("sandbox-one", now)
	if !ok {
		t.Fatal("first request was rejected")
	}
	releaseOne()
	if _, ok := admission.acquireResolution("sandbox-one", now); ok {
		t.Fatal("per-sandbox rate limit was not enforced")
	}
	releaseTwo, ok := admission.acquireResolution("sandbox-two", now)
	if !ok {
		t.Fatal("second sandbox was rejected before the global rate limit")
	}
	releaseTwo()
	if _, ok := admission.acquireResolution("sandbox-three", now); ok {
		t.Fatal("global rate limit was not enforced")
	}
	if release, ok := admission.acquireResolution("sandbox-one", now.Add(time.Minute)); !ok {
		t.Fatal("rate capacity did not reset at the window boundary")
	} else {
		release()
	}
}

func TestRuntimeLLMTargetAdmissionEnforcesConcurrency(t *testing.T) {
	limits := runtimeLLMAdmissionLimits{
		resolutionConcurrency: 10, globalConcurrency: 2, sandboxConcurrency: 1,
		globalRate: 10, sandboxRate: 10, maxSandboxes: 4, window: time.Minute,
	}
	admission := newRuntimeLLMAdmission(limits)
	now := time.Date(2026, time.September, 3, 0, 0, 0, 0, time.UTC)

	releaseOne, ok := admission.acquireTarget("sandbox-one", now)
	if !ok {
		t.Fatal("first sandbox request was rejected")
	}
	if _, ok := admission.acquireTarget("sandbox-one", now); ok {
		t.Fatal("per-sandbox concurrency limit was not enforced")
	}
	releaseTwo, ok := admission.acquireTarget("sandbox-two", now)
	if !ok {
		t.Fatal("second sandbox request was rejected before the global limit")
	}
	if _, ok := admission.acquireTarget("sandbox-three", now); ok {
		t.Fatal("global target concurrency limit was not enforced")
	}
	releaseOne()
	if releaseThree, ok := admission.acquireTarget("sandbox-three", now); !ok {
		t.Fatal("released target capacity was not reusable")
	} else {
		releaseThree()
	}
	releaseTwo()
}

func TestRuntimeLLMResolutionAdmissionBoundsSandboxKeys(t *testing.T) {
	limits := runtimeLLMAdmissionLimits{
		resolutionConcurrency: 10, globalConcurrency: 10, sandboxConcurrency: 10,
		globalRate: 10, sandboxRate: 10, maxSandboxes: 1, window: time.Minute,
	}
	admission := newRuntimeLLMAdmission(limits)
	now := time.Date(2026, time.September, 3, 0, 0, 0, 0, time.UTC)
	release, ok := admission.acquireResolution("sandbox-one", now)
	if !ok {
		t.Fatal("first sandbox was rejected")
	}
	if _, ok := admission.acquireResolution("sandbox-two", now); ok {
		t.Fatal("admission accepted a sandbox beyond its key cap")
	}
	release()
	if releaseTwo, ok := admission.acquireResolution("sandbox-two", now.Add(time.Minute)); !ok {
		t.Fatal("expired sandbox state was not reclaimed")
	} else {
		releaseTwo()
	}
}
