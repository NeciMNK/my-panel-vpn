package controller

import (
	"context"
	"errors"
	"io"
	"os"
	"time"

	"github.com/gin-gonic/gin"

	"github.com/mhsanaei/3x-ui/v3/internal/web/service"
	"github.com/mhsanaei/3x-ui/v3/internal/web/service/panel"
	"github.com/mhsanaei/3x-ui/v3/internal/web/session"
	"github.com/mhsanaei/3x-ui/v3/internal/web/websocket"
)

const (
	migrationSnapshotLimit = 256 << 20
	migrationDBLimit       = 1 << 30
)

// MigrationController imports another panel (Remnawave or 3x-ui) so that its
// users keep their configs and subscription links.
type MigrationController struct {
	migrationService service.MigrationService
	xrayService      service.XrayService
	panelService     panel.PanelService
}

func NewMigrationController(g *gin.RouterGroup) *MigrationController {
	a := &MigrationController{}
	g.POST("/preview", a.preview)
	g.POST("/apply", a.apply)
	return a
}

// migrationSource is the parsed form shared by preview and apply.
type migrationSource struct {
	kind    string
	snap    *service.RemnawaveSnapshot
	xuiPath string
	targets map[string]string
}

func (a *MigrationController) readSource(c *gin.Context) (*migrationSource, func(), error) {
	src := &migrationSource{kind: c.PostForm("source")}
	cleanup := func() {}
	targets, err := service.ParseMigrationTargets(c.PostForm("targets"))
	if err != nil {
		return nil, cleanup, err
	}
	src.targets = targets
	switch src.kind {
	case service.MigrationSourceRemnawave:
		if file, _, ferr := c.Request.FormFile("file"); ferr == nil {
			defer file.Close()
			data, rerr := io.ReadAll(io.LimitReader(file, migrationSnapshotLimit))
			if rerr != nil {
				return nil, cleanup, rerr
			}
			src.snap, err = service.ParseRemnawaveSnapshot(data)
			return src, cleanup, err
		}
		ctx, cancel := context.WithTimeout(c.Request.Context(), 3*time.Minute)
		defer cancel()
		src.snap, err = service.FetchRemnawaveSnapshot(ctx, c.PostForm("url"), c.PostForm("token"))
		return src, cleanup, err
	case service.MigrationSourceXUI:
		file, _, ferr := c.Request.FormFile("file")
		if ferr != nil {
			return nil, cleanup, errors.New("upload the x-ui.db file of the old panel")
		}
		defer file.Close()
		tmp, terr := os.CreateTemp("", "x-ui-migrate-*.db")
		if terr != nil {
			return nil, cleanup, terr
		}
		cleanup = func() { _ = os.Remove(tmp.Name()) }
		if _, cerr := io.Copy(tmp, io.LimitReader(file, migrationDBLimit)); cerr != nil {
			tmp.Close()
			return nil, cleanup, cerr
		}
		if cerr := tmp.Close(); cerr != nil {
			return nil, cleanup, cerr
		}
		src.xuiPath = tmp.Name()
		return src, cleanup, nil
	}
	return nil, cleanup, errors.New("unknown source: expected remnawave or 3x-ui")
}

func (a *MigrationController) preview(c *gin.Context) {
	src, cleanup, err := a.readSource(c)
	defer cleanup()
	if err != nil {
		jsonMsg(c, I18nWeb(c, "somethingWentWrong"), err)
		return
	}
	var p *service.MigrationPreview
	if src.kind == service.MigrationSourceRemnawave {
		p, err = a.migrationService.PreviewRemnawave(src.snap, src.targets)
	} else {
		p, err = a.migrationService.PreviewXUI(src.xuiPath, src.targets)
	}
	if err != nil {
		jsonMsg(c, I18nWeb(c, "somethingWentWrong"), err)
		return
	}
	jsonObj(c, p, nil)
}

func (a *MigrationController) apply(c *gin.Context) {
	src, cleanup, err := a.readSource(c)
	defer cleanup()
	if err != nil {
		jsonMsg(c, I18nWeb(c, "somethingWentWrong"), err)
		return
	}
	user := session.GetLoginUser(c)
	applySub := c.PostForm("applySub") == "true"
	var res *service.MigrationResult
	if src.kind == service.MigrationSourceRemnawave {
		res, err = a.migrationService.ApplyRemnawave(src.snap, src.targets, applySub, user.Id)
	} else {
		res, err = a.migrationService.ApplyXUI(src.xuiPath, src.targets, applySub, user.Id)
	}
	// Whatever was created before a failure is live and must reach Xray and the UI.
	if res != nil {
		if res.NeedRestart {
			a.xrayService.SetToNeedRestart()
		}
		websocket.BroadcastInvalidate(websocket.MessageTypeInbounds)
		notifyClientsChanged()
	}
	if err != nil {
		jsonMsg(c, I18nWeb(c, "somethingWentWrong"), err)
		return
	}
	jsonObj(c, res, nil)
	if res.PanelRestart {
		// The subscription server registers its paths at startup.
		_ = a.panelService.RestartPanel(3 * time.Second)
	}
}
