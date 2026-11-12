package service

import (
	"fmt"
	"net"
	"testing"
)

// The bot dialed socks5://127.0.0.1:62790 from the running config while
// nothing listened there and failed every call (#12); a dead bridge means direct.
func TestLiveEgressBridgeURL(t *testing.T) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	livePort := listener.Addr().(*net.TCPAddr).Port
	defer listener.Close()

	closed, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	deadPort := closed.Addr().(*net.TCPAddr).Port
	closed.Close()

	if got, want := liveEgressBridgeURL("panel-egress", livePort), fmt.Sprintf("socks5://127.0.0.1:%d", livePort); got != want {
		t.Fatalf("listening bridge = %q, want %q", got, want)
	}
	if got := liveEgressBridgeURL("panel-egress", deadPort); got != "" {
		t.Fatalf("dead bridge = %q, want a direct connection", got)
	}
}
