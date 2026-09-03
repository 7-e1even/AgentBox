package httpapi

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"agentbox/internal/platform"
)

func TestExtensionProgressEndpointAcceptsBoundedEscapedOutput(t *testing.T) {
	for _, size := range []int{4096, 4097} {
		body, err := json.Marshal(platform.WorkerJobProgressInput{
			Stage: "extensions", ExtensionID: "selected", ExtensionStatus: "succeeded", ExtensionOutput: strings.Repeat("\x01", size),
		})
		if err != nil {
			t.Fatal(err)
		}
		request := httptest.NewRequest(http.MethodPost, "/api/servers/7b20f83b-6418-4a9f-8477-3dc7c35d6310/jobs/754f76dd-2297-44e9-8204-a688be9be4a5/progress", bytes.NewReader(body))
		request.Header.Set("Content-Type", "application/json")
		response := httptest.NewRecorder()
		testHandler().ServeHTTP(response, request)
		want := http.StatusOK
		if size > 4096 {
			want = http.StatusBadRequest
		}
		if response.Code != want {
			t.Fatalf("output size %d status = %d, want %d: %s", size, response.Code, want, response.Body.String())
		}
	}
}
