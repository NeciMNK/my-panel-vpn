package main

import (
	"net"
	"strconv"
	"strings"
	"testing"

	"github.com/mhsanaei/3x-ui/v3/internal/config"
	"github.com/mhsanaei/3x-ui/v3/internal/database"
	"github.com/mhsanaei/3x-ui/v3/internal/database/dbtest"
	"github.com/mhsanaei/3x-ui/v3/internal/database/model"
	"github.com/mhsanaei/3x-ui/v3/internal/logger"
)

// A node whose sub port is held by another process (socat on 2096) crash-looped
// under systemd because a sub-server bind error exited the whole panel.
func TestStartSubServerSurvivesTakenPort(t *testing.T) {
	t.Setenv("XUI_DB_FOLDER", t.TempDir())
	dbtest.InitDB(t, config.GetDBPath())

	squatter, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("hold a port: %v", err)
	}
	defer squatter.Close()
	port := squatter.Addr().(*net.TCPAddr).Port

	for key, value := range map[string]string{
		"subEnable": "true",
		"subListen": "127.0.0.1",
		"subPort":   strconv.Itoa(port),
	} {
		if err := database.GetDB().Create(&model.Setting{Key: key, Value: value}).Error; err != nil {
			t.Fatalf("seed %s: %v", key, err)
		}
	}

	subServer := startSubServer()
	defer func() { _ = subServer.Stop() }()

	for _, line := range logger.GetLogs(20, "ERROR") {
		if strings.Contains(line, "address already in use") {
			return
		}
	}
	t.Fatalf("bind failure on sub port %d was not logged as an error; logs: %v", port, logger.GetLogs(20, "DEBUG"))
}
