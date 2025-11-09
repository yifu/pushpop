package discovery

import (
	"net"
	"testing"

	"github.com/grandcat/zeroconf"
)

func TestGetUserName_Success(t *testing.T) {
	entry := &zeroconf.ServiceEntry{
		Text: []string{"foo=bar", "user=alice", "baz=qux"},
	}
	u, err := GetUserName(entry)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if u != "alice" {
		t.Fatalf("expected alice, got %q", u)
	}
}

func TestGetUserName_NotFound(t *testing.T) {
	entry := &zeroconf.ServiceEntry{
		Text: []string{"nope=here"},
	}
	_, err := GetUserName(entry)
	if err == nil {
		t.Fatalf("expected error when user key missing")
	}
}

func TestFindMatchingIP_Loopback(t *testing.T) {
	// Try IPv4 then IPv6 loopback; at least one should exist on host running tests.
	ips := []net.IP{net.ParseIP("127.0.0.1"), net.ParseIP("::1")}
	got, err := FindMatchingIP(ips)
	if err != nil {
		t.Fatalf("expected to find loopback address on local interfaces, got error: %v", err)
	}
	if got != "127.0.0.1" && got != "::1" {
		t.Fatalf("unexpected matching IP: %q", got)
	}
}

func TestFindMatchingIP_NoneFound(t *testing.T) {
	// Use an address from TEST-NET-3 which should not be assigned to local interfaces.
	ips := []net.IP{net.ParseIP("203.0.113.1")}
	_, err := FindMatchingIP(ips)
	if err == nil {
		t.Fatalf("expected no matching interface for test IP, but found one")
	}
}
