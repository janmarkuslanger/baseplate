package plugin

import (
	"net/http/httptest"
	"testing"
)

func TestProxyIP(t *testing.T) {
	cases := []struct {
		name   string
		header []string
		want   string
	}{
		{"no header", nil, "192.0.2.1"},
		{"single address", []string{"203.0.113.9"}, "203.0.113.9"},
		{"spoofed entries before the proxy's", []string{"1.2.3.4, 5.6.7.8, 203.0.113.9"}, "203.0.113.9"},
		{"several header lines", []string{"1.2.3.4", "203.0.113.9"}, "203.0.113.9"},
		{"spaces", []string{"1.2.3.4 ,  203.0.113.9 "}, "203.0.113.9"},
		{"ipv6", []string{"2001:db8::1"}, "2001:db8::1"},
		{"ipv4-mapped ipv6", []string{"::ffff:203.0.113.9"}, "203.0.113.9"},
		{"invalid last entry", []string{"203.0.113.9, not-an-ip"}, "192.0.2.1"},
		{"empty last entry", []string{"203.0.113.9,"}, "192.0.2.1"},
		{"empty header", []string{""}, "192.0.2.1"},
	}
	ip := ProxyIP("X-Forwarded-For")
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			r := httptest.NewRequest("GET", "/", nil)
			r.RemoteAddr = "192.0.2.1:4711"
			for _, v := range c.header {
				r.Header.Add("X-Forwarded-For", v)
			}
			if got := ip(r); got != c.want {
				t.Errorf("ProxyIP = %q, want %q", got, c.want)
			}
		})
	}
}
