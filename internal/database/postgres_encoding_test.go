package database

import (
	"errors"
	"fmt"
	"net/url"
	"os"
	"strings"
	"testing"
	"time"

	"gorm.io/driver/postgres"
	"gorm.io/gorm"
)

// A Debian LXC with a C locale makes `CREATE DATABASE` default to SQL_ASCII;
// the panel must name the cause instead of failing AutoMigrate on client_encoding.
func TestInitDBRejectsNonUTF8PostgresDatabase(t *testing.T) {
	dsn := strings.TrimSpace(os.Getenv("XUI_TEST_PG_DSN"))
	if dsn == "" {
		t.Skip("set XUI_TEST_PG_DSN to run the PostgreSQL encoding check test")
	}
	admin, err := gorm.Open(postgres.Open(dsn), &gorm.Config{})
	if err != nil {
		t.Fatalf("open PostgreSQL admin connection: %v", err)
	}
	adminHandle, err := admin.DB()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = adminHandle.Close() })

	name := fmt.Sprintf("xui_sql_ascii_%d", time.Now().UnixNano())
	if err := admin.Exec("CREATE DATABASE " + name + " ENCODING 'SQL_ASCII' TEMPLATE template0").Error; err != nil {
		t.Fatalf("create SQL_ASCII database: %v", err)
	}
	t.Cleanup(func() {
		_ = CloseDB()
		if err := admin.Exec("DROP DATABASE IF EXISTS " + name + " WITH (FORCE)").Error; err != nil {
			t.Error(err)
		}
	})

	t.Setenv("XUI_DB_TYPE", "postgres")
	t.Setenv("XUI_DB_DSN", withDBName(t, dsn, name))
	err = InitDB("")
	if !errors.Is(err, ErrPostgresNotUTF8) {
		t.Fatalf("InitDB on a SQL_ASCII database: err = %v, want ErrPostgresNotUTF8", err)
	}
	if !strings.Contains(err.Error(), "SQL_ASCII") {
		t.Fatalf("error %q does not name the actual encoding", err)
	}
}

func withDBName(t *testing.T, dsn, name string) string {
	t.Helper()
	if strings.HasPrefix(dsn, "postgres://") || strings.HasPrefix(dsn, "postgresql://") {
		parsed, err := url.Parse(dsn)
		if err != nil {
			t.Fatal(err)
		}
		parsed.Path = "/" + name
		return parsed.String()
	}
	return dsn + " dbname=" + name
}
