package service

import (
	"os"
	"slices"
	"testing"
)

// A Patroni DSN lists every database host; pg_dump must get all of them and
// the read-write requirement, or a backup can hit a replica or nothing.
func TestPgConnEnvKeepsEveryHostOfAClusterDSN(t *testing.T) {
	cases := []struct {
		dsn  string
		want []string
	}{
		{
			"postgres://xui:pw@10.0.0.1:5432,10.0.0.2:5433/xui?sslmode=disable&target_session_attrs=read-write",
			[]string{"PGHOST=10.0.0.1,10.0.0.2", "PGPORT=5432,5433", "PGDATABASE=xui", "PGUSER=xui", "PGPASSWORD=pw", "PGSSLMODE=disable", "PGTARGETSESSIONATTRS=read-write"},
		},
		{"postgres://xui@[2001:db8::1]/xui", []string{"PGHOST=2001:db8::1", "PGPORT=5432", "PGDATABASE=xui", "PGUSER=xui"}},
		{"postgres://xui@/xui", []string{"PGHOST=127.0.0.1", "PGPORT=5432", "PGDATABASE=xui", "PGUSER=xui"}},
	}
	for _, tc := range cases {
		env, _, err := pgConnEnv(tc.dsn)
		if err != nil {
			t.Fatalf("%s: %v", tc.dsn, err)
		}
		got := env[len(os.Environ()):]
		if !slices.Equal(got, tc.want) {
			t.Fatalf("%s: env = %v, want %v", tc.dsn, got, tc.want)
		}
	}
}
