package sub

import (
	"bytes"
	"net/http"
	"strings"

	"github.com/gin-gonic/gin"

	"github.com/mhsanaei/3x-ui/v3/internal/web/service"
)

const maxCapturedBody = 4 << 20

// captureForEdges hands each subscription body a client app got to the edge
// recorder, so edge nodes can still answer that app while no master is up.
func captureForEdges(c *gin.Context) {
	recorder := service.SubEdgeRecorder()
	if c.Request.Method != http.MethodGet || !recorder.Active() {
		c.Next()
		return
	}
	w := &teeWriter{ResponseWriter: c.Writer}
	c.Writer = w
	c.Next()
	if w.Status() != http.StatusOK || w.overflow || strings.HasPrefix(w.Header().Get("Content-Type"), "text/html") {
		return
	}
	recorder.Record(c.Request, c.Param("subid"), w.Header(), w.body.Bytes())
}

type teeWriter struct {
	gin.ResponseWriter
	body     bytes.Buffer
	overflow bool
}

func (w *teeWriter) Write(p []byte) (int, error) {
	w.keep(p)
	return w.ResponseWriter.Write(p)
}

func (w *teeWriter) WriteString(s string) (int, error) {
	w.keep([]byte(s))
	return w.ResponseWriter.WriteString(s)
}

func (w *teeWriter) keep(p []byte) {
	if w.overflow || w.body.Len()+len(p) > maxCapturedBody {
		w.overflow = true
		return
	}
	w.body.Write(p)
}
