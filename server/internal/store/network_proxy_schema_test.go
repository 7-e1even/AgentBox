package store

import (
	"strings"
	"testing"
)

func TestSchemaAllowsSOCKSNetworkProxySchemes(t *testing.T) {
	for _, expected := range []string{
		"ALTER TABLE network_proxies DROP CONSTRAINT IF EXISTS network_proxies_scheme_check",
		"ALTER TABLE network_proxies ADD CONSTRAINT network_proxies_scheme_check",
		"CHECK (scheme IN ('http', 'https', 'socks5', 'socks5h'))",
	} {
		if !strings.Contains(schema, expected) {
			t.Fatalf("network proxy schema migration is missing %q", expected)
		}
	}
}
