package service

import (
	"errors"
	"path/filepath"
	"testing"
	"time"

	"github.com/mhsanaei/3x-ui/v3/internal/database"
	"github.com/mhsanaei/3x-ui/v3/internal/database/dbtest"
	"github.com/mhsanaei/3x-ui/v3/internal/database/model"
)

const testCertPin = "4f2d8c1a9b7e6d5c4b3a29181716151413121110090807060504030201000a0b"

func initJoinDB(t *testing.T) {
	t.Helper()
	dbDir := t.TempDir()
	t.Setenv("XUI_DB_FOLDER", dbDir)
	dbtest.InitDB(t, filepath.Join(dbDir, "x-ui.db"))
}

func joinRequest(token string) *NodeJoinRequest {
	return &NodeJoinRequest{
		Join: token, Name: "de-fra", Port: 31337, BasePath: "/abc/",
		ApiToken: "node-secret", CertSha256: testCertPin,
	}
}

func TestJoinNodeRegistersPinnedNodeOnce(t *testing.T) {
	initJoinDB(t)
	s := &NodeService{}
	tok, err := s.CreateJoinToken("amd64")
	if err != nil {
		t.Fatal(err)
	}
	if err := s.CheckJoinToken(tok.Token); err != nil {
		t.Fatalf("fresh token rejected: %v", err)
	}

	view, err := s.JoinNode(joinRequest(tok.Token), "203.0.113.7")
	if err != nil {
		t.Fatalf("JoinNode: %v", err)
	}
	n, err := s.GetById(view.Id)
	if err != nil {
		t.Fatal(err)
	}
	if n.Address != "203.0.113.7" || n.Port != 31337 || n.TlsVerifyMode != "pin" ||
		n.PinnedCertSha256 != testCertPin || n.ApiToken != "node-secret" || !n.Enable {
		t.Fatalf("unexpected node: %+v", n)
	}

	if _, err := s.JoinNode(joinRequest(tok.Token), "203.0.113.8"); !errors.Is(err, ErrNodeJoinTokenInvalid) {
		t.Fatalf("second join with the same token: err = %v", err)
	}
	if err := s.CheckJoinToken(tok.Token); !errors.Is(err, ErrNodeJoinTokenInvalid) {
		t.Fatalf("used token still downloads the bundle: %v", err)
	}
}

func TestJoinNodeRejectsExpiredTokenAndKeepsNamesUnique(t *testing.T) {
	initJoinDB(t)
	s := &NodeService{}

	expired, err := s.CreateJoinToken("amd64")
	if err != nil {
		t.Fatal(err)
	}
	database.GetDB().Model(model.NodeJoinToken{}).Where("1 = 1").
		Update("expires_at", time.Now().Add(-time.Minute).Unix())
	if _, err := s.JoinNode(joinRequest(expired.Token), "203.0.113.7"); !errors.Is(err, ErrNodeJoinTokenInvalid) {
		t.Fatalf("expired token: err = %v", err)
	}

	first, _ := s.CreateJoinToken("amd64")
	second, _ := s.CreateJoinToken("amd64")
	a, err := s.JoinNode(joinRequest(first.Token), "203.0.113.7")
	if err != nil {
		t.Fatal(err)
	}
	b, err := s.JoinNode(joinRequest(second.Token), "203.0.113.9")
	if err != nil {
		t.Fatal(err)
	}
	if a.Name != "de-fra" || b.Name != "de-fra-2" {
		t.Fatalf("names = %q, %q; want de-fra, de-fra-2", a.Name, b.Name)
	}
}

func TestJoinNodeKeepsTokenWhenRequestIsInvalid(t *testing.T) {
	initJoinDB(t)
	s := &NodeService{}
	tok, _ := s.CreateJoinToken("amd64")
	bad := joinRequest(tok.Token)
	bad.Port = 0
	if _, err := s.JoinNode(bad, "203.0.113.7"); err == nil {
		t.Fatal("port 0 accepted")
	}
	if err := s.CheckJoinToken(tok.Token); err != nil {
		t.Fatalf("invalid request burned the token: %v", err)
	}
}
