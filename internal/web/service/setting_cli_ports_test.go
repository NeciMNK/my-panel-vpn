package service

import (
	"strings"
	"testing"
)

// The CLI writes ports straight to the DB, bypassing the web form's CheckValid,
// so a web/sub collision here leaves a panel that cannot bind on next start.
func TestSetSubPortAndSetPortRefuseCollision(t *testing.T) {
	tests := []struct {
		name      string
		webListen string
		subListen string
		set       func(s *SettingService) error
		wantErr   string
	}{
		{"sub port onto web port", "", "", func(s *SettingService) error { return s.SetSubPort(2053) }, "same ip:port"},
		{"web port onto sub port", "", "", func(s *SettingService) error { return s.SetPort(2096) }, "same ip:port"},
		{"sub port out of range", "", "", func(s *SettingService) error { return s.SetSubPort(70000) }, "not a valid port"},
		{"web port out of range", "", "", func(s *SettingService) error { return s.SetPort(70000) }, "not a valid port"},
		{"same port, distinct listens", "127.0.0.1", "192.168.1.1", func(s *SettingService) error { return s.SetSubPort(2053) }, ""},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			setupSettingTestDB(t)
			s := &SettingService{}
			if err := s.setString("webPort", "2053"); err != nil {
				t.Fatal(err)
			}
			if err := s.setString("subPort", "2096"); err != nil {
				t.Fatal(err)
			}
			if err := s.SetListen(tc.webListen); err != nil {
				t.Fatal(err)
			}
			if err := s.setString("subListen", tc.subListen); err != nil {
				t.Fatal(err)
			}

			err := tc.set(s)
			if tc.wantErr == "" {
				if err != nil {
					t.Fatalf("unexpected error: %v", err)
				}
				if got, _ := s.GetSubPort(); got != 2053 {
					t.Fatalf("subPort = %d, want 2053", got)
				}
				return
			}
			if err == nil || !strings.Contains(err.Error(), tc.wantErr) {
				t.Fatalf("err = %v, want containing %q", err, tc.wantErr)
			}
			web, _ := s.GetPort()
			sub, _ := s.GetSubPort()
			if web != 2053 || sub != 2096 {
				t.Fatalf("ports changed on refusal: web=%d sub=%d", web, sub)
			}
		})
	}
}
