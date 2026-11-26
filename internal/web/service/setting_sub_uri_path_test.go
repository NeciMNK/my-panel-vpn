package service

import "testing"

// A subURI that ends in the old subPath kept pointing there after subPath
// changed, so the UI and the bot handed out dead links (#10).
func TestSubURIFollowsSubPathChange(t *testing.T) {
	cases := []struct {
		name, uri, newURI, wantURI string
	}{
		{"uri on the old path follows", "https://sub.example.com/old/", "", "https://sub.example.com/new/"},
		{"uri without trailing slash follows", "https://sub.example.com:8443/old", "", "https://sub.example.com:8443/new/"},
		{"proxy path that differs is kept", "https://sub.example.com/proxied/", "", "https://sub.example.com/proxied/"},
		{"uri edited in the same save wins", "https://sub.example.com/old/", "https://cdn.example.com/x/", "https://cdn.example.com/x/"},
		{"empty uri stays empty", "", "", ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			setupSettingTestDB(t)
			s := &SettingService{}
			settings, err := s.GetAllSetting()
			if err != nil {
				t.Fatal(err)
			}
			settings.SubPath, settings.SubURI = "/old/", tc.uri
			if err := s.UpdateAllSetting(settings, SecretClears{}); err != nil {
				t.Fatal(err)
			}

			settings.SubPath = "/new/"
			if tc.newURI != "" {
				settings.SubURI = tc.newURI
			}
			if err := s.UpdateAllSetting(settings, SecretClears{}); err != nil {
				t.Fatal(err)
			}
			if got, _ := s.GetSubURI(); got != tc.wantURI {
				t.Fatalf("subURI = %q, want %q", got, tc.wantURI)
			}
		})
	}
}

// The JSON and Clash URIs follow their own paths the same way.
func TestSubJsonAndClashURIFollowTheirPaths(t *testing.T) {
	setupSettingTestDB(t)
	s := &SettingService{}
	settings, err := s.GetAllSetting()
	if err != nil {
		t.Fatal(err)
	}
	settings.SubJsonPath, settings.SubJsonURI = "/json/", "https://sub.example.com/json/"
	settings.SubClashPath, settings.SubClashURI = "/clash/", "https://sub.example.com/clash/"
	if err := s.UpdateAllSetting(settings, SecretClears{}); err != nil {
		t.Fatal(err)
	}
	settings.SubJsonPath, settings.SubClashPath = "/j2/", "/c2/"
	if err := s.UpdateAllSetting(settings, SecretClears{}); err != nil {
		t.Fatal(err)
	}
	if got, _ := s.GetSubJsonURI(); got != "https://sub.example.com/j2/" {
		t.Fatalf("subJsonURI = %q, want https://sub.example.com/j2/", got)
	}
	if got, _ := s.GetSubClashURI(); got != "https://sub.example.com/c2/" {
		t.Fatalf("subClashURI = %q, want https://sub.example.com/c2/", got)
	}
}
