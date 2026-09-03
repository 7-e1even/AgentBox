package platform

import "testing"

func TestNormalizeWorkerOriginRequiresHTTPSOutsideLoopback(t *testing.T) {
	for _, test := range []struct {
		input string
		want  string
	}{
		{input: "https://agentbox.example/", want: "https://agentbox.example"},
		{input: "http://localhost:3000/", want: "http://localhost:3000"},
		{input: "http://127.0.0.2:3000", want: "http://127.0.0.2:3000"},
		{input: "http://[::1]:3000", want: "http://[::1]:3000"},
	} {
		got, err := NormalizeWorkerOrigin(test.input)
		if err != nil || got != test.want {
			t.Errorf("NormalizeWorkerOrigin(%q) = %q, %v; want %q", test.input, got, err, test.want)
		}
	}
	for _, input := range []string{
		"http://192.168.1.10:3000",
		"http://localhost.example:3000",
		"http://127.0.0.1.example:3000",
		"https://user:secret@agentbox.example",
		"https://agentbox.example/api",
		"ssh://agentbox.example",
	} {
		if _, err := NormalizeWorkerOrigin(input); err == nil {
			t.Errorf("NormalizeWorkerOrigin(%q) unexpectedly succeeded", input)
		}
	}
}

func TestValidateWorkerDownloadURLAllowsSecureReleasePath(t *testing.T) {
	if err := ValidateWorkerDownloadURL("https://github.com/example/project/releases/download"); err != nil {
		t.Fatalf("ValidateWorkerDownloadURL() rejected HTTPS release path: %v", err)
	}
	if err := ValidateWorkerDownloadURL("http://releases.example/download"); err == nil {
		t.Fatal("ValidateWorkerDownloadURL() accepted non-loopback HTTP")
	}
}
