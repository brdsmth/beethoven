package main

import "testing"

func TestNeedToken(t *testing.T) {
	for _, c := range []struct {
		addr, token string
		ok          bool
	}{
		{"127.0.0.1:7070", "", true},
		{"localhost:7070", "", true},
		{"[::1]:7070", "", true},
		{"0.0.0.0:7070", "", false},
		{":7070", "", false}, // every interface
		{"0.0.0.0:7070", "s3cret", true},
	} {
		if err := needToken(c.addr, c.token); (err == nil) != c.ok {
			t.Errorf("needToken(%q, %q) = %v", c.addr, c.token, err)
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
