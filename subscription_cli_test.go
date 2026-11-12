package main

import (
	"flag"
	"io"
	"os"
	"strings"
	"testing"

	"github.com/mhsanaei/3x-ui/v3/internal/web/service"
)

func parseSubEnable(t *testing.T, args ...string) optionalBool {
	t.Helper()
	var v optionalBool
	fs := flag.NewFlagSet("setting", flag.ContinueOnError)
	fs.Var(&v, "subEnable", "")
	if err := fs.Parse(args); err != nil {
		t.Fatalf("parse %v: %v", args, err)
	}
	return v
}

// A plain BoolVar cannot tell "-subEnable=false" from an absent flag, so the
// node installer could never switch the subscription server off.
func TestSubEnableFlagIsTriState(t *testing.T) {
	tests := []struct {
		args    []string
		wantSet bool
		wantVal bool
	}{
		{nil, false, false},
		{[]string{"-subEnable=false"}, true, false},
		{[]string{"-subEnable"}, true, true},
		{[]string{"-subEnable=true"}, true, true},
	}
	for _, tc := range tests {
		t.Run(strings.Join(tc.args, " "), func(t *testing.T) {
			got := parseSubEnable(t, tc.args...)
			if got.set != tc.wantSet || got.value != tc.wantVal {
				t.Fatalf("got set=%t value=%t, want set=%t value=%t", got.set, got.value, tc.wantSet, tc.wantVal)
			}
		})
	}
}

func TestUpdateSettingTogglesSubscriptionServer(t *testing.T) {
	newTokenCLIEnv(t)
	s := service.SettingService{}

	if err := updateSetting(settingUpdate{subEnable: optionalBool{set: true, value: false}, subPort: 2097}); err != nil {
		t.Fatal(err)
	}
	if on, err := s.GetSubEnable(); err != nil || on {
		t.Fatalf("subEnable = %t, %v; want false", on, err)
	}
	if port, err := s.GetSubPort(); err != nil || port != 2097 {
		t.Fatalf("subPort = %d, %v; want 2097", port, err)
	}

	// An absent -subEnable must leave the stored value alone.
	if err := updateSetting(settingUpdate{}); err != nil {
		t.Fatal(err)
	}
	if on, _ := s.GetSubEnable(); on {
		t.Fatal("absent -subEnable re-enabled the subscription server")
	}

	if err := updateSetting(settingUpdate{subEnable: optionalBool{set: true, value: true}}); err != nil {
		t.Fatal(err)
	}
	if on, _ := s.GetSubEnable(); !on {
		t.Fatal("-subEnable=true did not enable the subscription server")
	}
}

// install.sh reads the panel port with `grep 'port:'`; a "subPort:" line in
// -show would feed it two ports. Pin that only one line matches.
func TestShowSettingKeepsSinglePortLine(t *testing.T) {
	newTokenCLIEnv(t)
	out := captureStdout(t, func() { showSetting(true) })

	var portLines []string
	for _, line := range strings.Split(out, "\n") {
		if strings.Contains(line, "port:") {
			portLines = append(portLines, line)
		}
	}
	if len(portLines) != 1 {
		t.Fatalf("lines matching 'port:' = %q, want exactly the panel port", portLines)
	}
	for _, want := range []string{"subEnable: true", "subscriptionPort: 2096"} {
		if !strings.Contains(out, want) {
			t.Fatalf("-show output missing %q:\n%s", want, out)
		}
	}
}

func captureStdout(t *testing.T, fn func()) string {
	t.Helper()
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	orig := os.Stdout
	os.Stdout = w
	defer func() { os.Stdout = orig }()
	fn()
	_ = w.Close()
	data, err := io.ReadAll(r)
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}
