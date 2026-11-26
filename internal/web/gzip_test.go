package web

import (
	"bytes"
	"compress/gzip"
	"crypto/rand"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
)

// gin-contrib/gzip passes a response through only while its writes start with
// the gzip magic, so a streamed archive came out half re-compressed (stand bug).
func TestNodeBackupStreamSurvivesPanelGzip(t *testing.T) {
	gin.SetMode(gin.TestMode)
	payload := make([]byte, 1<<20)
	if _, err := rand.Read(payload); err != nil {
		t.Fatal(err)
	}
	const basePath = "/base/"
	engine := gin.New()
	engine.Use(panelGzip(basePath))
	engine.GET(basePath+"panel/api/server/nodeBackup", func(c *gin.Context) {
		c.Header("Content-Type", "application/gzip")
		zw := gzip.NewWriter(c.Writer)
		for off := 0; off < len(payload); off += 32 << 10 {
			_, _ = zw.Write(payload[off:min(off+32<<10, len(payload))])
			_ = zw.Flush()
		}
		_ = zw.Close()
	})
	srv := httptest.NewServer(engine)
	defer srv.Close()

	// Go's transport asks for gzip on its own, exactly as the master's backup fetch does.
	res, err := http.Get(srv.URL + basePath + "panel/api/server/nodeBackup")
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()
	zr, err := gzip.NewReader(res.Body)
	if err != nil {
		t.Fatalf("archive header: %v", err)
	}
	got, err := io.ReadAll(zr)
	if err != nil {
		t.Fatalf("archive body: %v", err)
	}
	if !bytes.Equal(got, payload) {
		t.Fatalf("archive decompressed to %d bytes, want the %d sent", len(got), len(payload))
	}
}
