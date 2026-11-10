package shaper

import (
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func TestNormalizeKeepsTheStrictestLimitPerIP(t *testing.T) {
	p := Plan{Limits: []Limit{{IP: "10.0.0.2", Mbps: 50}, {IP: " ::ffff:10.0.0.2", Mbps: 20}, {IP: "2001:db8::1", Mbps: 5}}}
	if err := p.Normalize(); err != nil {
		t.Fatal(err)
	}
	want := []Limit{{IP: "10.0.0.2", Mbps: 20}, {IP: "2001:db8::1", Mbps: 5}}
	if !reflect.DeepEqual(p.Limits, want) {
		t.Fatalf("limits = %+v", p.Limits)
	}
	for _, bad := range []Limit{{IP: "host", Mbps: 5}, {IP: "10.0.0.3", Mbps: 0}} {
		p := Plan{Limits: []Limit{bad}}
		if err := p.Normalize(); err == nil || !strings.HasPrefix(err.Error(), "limit") {
			t.Fatalf("%+v: err = %v", bad, err)
		}
	}
}

func TestCommandsShapeDownloadAndPoliceUpload(t *testing.T) {
	got := Commands("eth0", Plan{Limits: []Limit{{IP: "10.0.0.2", Mbps: 20}, {IP: "2001:db8::1", Mbps: 400}}})
	lines := make([]string, len(got))
	for i, c := range got {
		lines[i] = strings.Join(c, " ")
	}
	want := []string{
		"qdisc del dev eth0 root",
		"qdisc del dev eth0 ingress",
		"qdisc add dev eth0 root handle 1: htb default ffff",
		"class add dev eth0 parent 1: classid 1:ffff htb rate 100gbit",
		"qdisc add dev eth0 handle ffff: ingress",
		"class add dev eth0 parent 1: classid 1:1 htb rate 20mbit ceil 20mbit",
		"filter add dev eth0 parent 1: protocol ip prio 1 u32 match ip dst 10.0.0.2/32 flowid 1:1",
		"filter add dev eth0 parent ffff: protocol ip prio 1 u32 match ip src 10.0.0.2/32 police rate 20mbit burst 64k drop flowid :1",
		"class add dev eth0 parent 1: classid 1:2 htb rate 400mbit ceil 400mbit",
		"filter add dev eth0 parent 1: protocol ipv6 prio 1 u32 match ip6 dst 2001:db8::1/128 flowid 1:2",
		"filter add dev eth0 parent ffff: protocol ipv6 prio 1 u32 match ip6 src 2001:db8::1/128 police rate 400mbit burst 500k drop flowid :1",
	}
	if !reflect.DeepEqual(lines, want) {
		t.Fatalf("commands:\n%s", strings.Join(lines, "\n"))
	}
}

func TestApplyOnlyTouchesTcWhenThePlanChanges(t *testing.T) {
	stateFile = filepath.Join(t.TempDir(), "shaper.state")
	var calls []string
	tc = func(args ...string) error {
		calls = append(calls, strings.Join(args, " "))
		return nil
	}
	defaultDev = func() (string, error) { return "eth0", nil }

	if _, err := Apply(Plan{}, OwnerLocal); err != nil || len(calls) != 0 {
		t.Fatalf("an empty plan on an untouched server must not run tc: %v %v", calls, err)
	}
	plan := Plan{Limits: []Limit{{IP: "10.0.0.2", Mbps: 20}}}
	st, err := Apply(plan, OwnerLocal)
	if err != nil || st != (Status{Interface: "eth0", Limited: 1}) || len(calls) != 8 {
		t.Fatalf("first apply: status %+v, err %v, %d calls", st, err, len(calls))
	}
	calls = nil
	if _, err := Apply(Plan{Limits: []Limit{{IP: "10.0.0.2", Mbps: 20}}}, OwnerLocal); err != nil || len(calls) != 0 {
		t.Fatalf("same plan again ran %v", calls)
	}
	if _, err := Apply(Plan{}, OwnerLocal); err != nil {
		t.Fatal(err)
	}
	if want := []string{"qdisc del dev eth0 root", "qdisc del dev eth0 ingress"}; !reflect.DeepEqual(calls, want) {
		t.Fatalf("lifting the last limit ran %v", calls)
	}
}

func TestApplyKeepsCapsAnotherOwnerPushed(t *testing.T) {
	stateFile = filepath.Join(t.TempDir(), "shaper.state")
	var calls []string
	tc = func(args ...string) error {
		calls = append(calls, strings.Join(args, " "))
		return nil
	}
	defaultDev = func() (string, error) { return "eth0", nil }
	if _, err := Apply(Plan{Limits: []Limit{{IP: "10.0.0.2", Mbps: 20}}}, OwnerMaster); err != nil {
		t.Fatal(err)
	}
	calls = nil
	if _, err := Apply(Plan{}, OwnerLocal); err != nil || len(calls) != 0 {
		t.Fatalf("a node with no caps of its own lifted the master's caps: %v %v", calls, err)
	}
}
