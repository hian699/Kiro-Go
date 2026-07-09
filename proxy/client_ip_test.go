package proxy

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestClientIPPrecedence(t *testing.T) {
	cases := []struct {
		name    string
		headers map[string]string
		remote  string
		want    string
	}{
		{"cf wins", map[string]string{"CF-Connecting-IP": "1.1.1.1", "X-Forwarded-For": "2.2.2.2", "X-Real-IP": "3.3.3.3"}, "9.9.9.9:1234", "1.1.1.1"},
		{"xff first element", map[string]string{"X-Forwarded-For": "2.2.2.2, 4.4.4.4", "X-Real-IP": "3.3.3.3"}, "9.9.9.9:1234", "2.2.2.2"},
		{"x-real-ip", map[string]string{"X-Real-IP": "3.3.3.3"}, "9.9.9.9:1234", "3.3.3.3"},
		{"remoteaddr host only", nil, "9.9.9.9:1234", "9.9.9.9"},
		{"remoteaddr no port", nil, "9.9.9.9", "9.9.9.9"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			r := httptest.NewRequest(http.MethodPost, "/v1/messages", strings.NewReader("{}"))
			r.RemoteAddr = c.remote
			for k, v := range c.headers {
				r.Header.Set(k, v)
			}
			if got := clientIP(r); got != c.want {
				t.Fatalf("clientIP=%q want %q", got, c.want)
			}
		})
	}
}
