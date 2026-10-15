package main

import "testing"

func TestReachable(t *testing.T) {
	for _, c := range []struct {
		addr  string
		hosts []string
		want  bool
	}{
		{"127.0.0.1:7070", nil, false},
		{"localhost:7070", nil, false},
		{"[::1]:7070", nil, false},
		{"127.0.0.1:7070", []string{"tunnel.example.com"}, true},
		{"0.0.0.0:7070", nil, true},
		{":7070", nil, true}, // every interface
	} {
		if got := reachable(c.addr, c.hosts); got != c.want {
			t.Errorf("reachable(%q, %q) = %v", c.addr, c.hosts, got)
		}
	}
}

func TestPlatformDefaults(t *testing.T) {
	t.Setenv("PORT", "")
	t.Setenv("BEETHOVEN_URL", "")
	t.Setenv("RAILWAY_PUBLIC_DOMAIN", "")
	if a, u := defaultAddr(), publicURL(); a != "127.0.0.1:7070" || u != "" {
		t.Errorf("without a platform: %q, %q", a, u)
	}
	t.Setenv("PORT", "8080")
	t.Setenv("RAILWAY_PUBLIC_DOMAIN", "beethoven-production.up.railway.app")
	if a, u := defaultAddr(), publicURL(); a != "0.0.0.0:8080" || u != "https://beethoven-production.up.railway.app" {
		t.Errorf("on Railway: %q, %q", a, u)
	}
	t.Setenv("BEETHOVEN_URL", "https://agents.example.com/")
	if u := publicURL(); u != "https://agents.example.com" {
		t.Errorf("with BEETHOVEN_URL: %q", u)
	}
}
