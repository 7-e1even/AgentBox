package store

import (
	"testing"
	"time"

	"agentbox/internal/platform"
)

func TestApplyNetworkProxyCheckOutput(t *testing.T) {
	t.Parallel()

	result := platform.NetworkProxyCheck{Target: "https://www.gstatic.com/generate_204"}
	applyNetworkProxyCheckOutput(&result, `{
      "ok":true,
      "latencyMs":128,
      "target":"https://www.gstatic.com/generate_204",
      "statusCode":204,
      "checkedAt":"2026-08-29T00:00:00Z"
    }`)
	if result.OK == nil || !*result.OK || result.LatencyMS != 128 || result.StatusCode != 204 {
		t.Fatalf("result = %#v", result)
	}
	wantCheckedAt := time.Date(2026, time.August, 29, 0, 0, 0, 0, time.UTC)
	if result.CheckedAt == nil || !result.CheckedAt.Equal(wantCheckedAt) {
		t.Fatalf("checkedAt = %v, want %v", result.CheckedAt, wantCheckedAt)
	}
}

func TestApplyNetworkProxyCheckOutputRejectsMalformedWorkerResult(t *testing.T) {
	t.Parallel()

	result := platform.NetworkProxyCheck{}
	applyNetworkProxyCheckOutput(&result, `not-json`)
	if result.OK == nil || *result.OK || result.Error != "Worker 未返回有效的代理检测结果" {
		t.Fatalf("result = %#v", result)
	}
}

func TestApplyNetworkProxyCheckOutputDoesNotReflectWorkerSecrets(t *testing.T) {
	t.Parallel()

	result := platform.NetworkProxyCheck{Target: "https://www.gstatic.com/generate_204"}
	applyNetworkProxyCheckOutput(&result, `{
      "ok":false,
      "latencyMs":1,
      "target":"https://attacker.invalid/?key=secret",
      "error":"http://user:secret@proxy.invalid"
    }`)
	if result.Error != "Worker 无法通过代理访问检测地址" ||
		result.Target != "https://www.gstatic.com/generate_204" {
		t.Fatalf("result = %#v", result)
	}
}
