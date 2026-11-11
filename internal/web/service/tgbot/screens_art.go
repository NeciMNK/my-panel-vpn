package tgbot

import (
	"context"
	"sync"
	"sync/atomic"
	"time"

	"github.com/mhsanaei/3x-ui/v3/internal/logger"

	"github.com/mymmrac/telego"
)

// Screen art: the bot's avatar serves every screen's picture; a bot without one
// gets text screens rather than an empty picture over the text.

var (
	screenArtMu  sync.RWMutex
	screenArtIDs = map[string]string{}
	// artOverrides is artwork the panel set for one screen kind. It outranks
	// the bot's avatar, so adopting an avatar never silently replaces it.
	artOverrides = map[string]string{}
	// artFromAvatar records whether the cached ids came from the bot's avatar: a
	// tile cached from an upload must never win over an avatar set afterwards.
	artFromAvatar    bool
	artProbedAt      time.Time
	rejectedAvatarID string
	// artProbeRunning keeps a burst of renders from starting a burst of probes.
	artProbeRunning atomic.Bool
	artFirstProbe   atomic.Bool
	// artProbes lets resetScreenArt outlast a probe still running against a previous bot.
	artProbes sync.WaitGroup
)

// Probes are cheap but not free: a known avatar is trusted for a while, a missing
// one retried sooner, so an uploaded picture shows without a restart.
const (
	artAvatarTTL = 10 * time.Minute
	artRetryTTL  = time.Minute
)

// resolveScreenArt caches the bot avatar's file_id for every screen kind.
// Telegram serves one photo per bot, so all kinds share it.
func (t *Tgbot) resolveScreenArt() {
	// One read: the panel can swap the bot under us (login/logout), and reading
	// the global per call is a nil-pointer crash in this background goroutine.
	b := liveBot()
	if b == nil {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	me, err := b.GetMe(ctx)
	if err != nil {
		logger.Warning("Failed to read the bot identity for screen art:", err)
		return
	}

	// Use getUserProfilePhotos, not getChat: getChat's ChatPhoto file_id is
	// refused in sendPhoto ("can't use file of type ChatPhoto as Photo").
	photos, err := b.GetUserProfilePhotos(ctx, &telego.GetUserProfilePhotosParams{
		UserID: me.ID, Limit: 1,
	})
	if err != nil {
		logger.Debug("Failed to read the bot's profile photos for screen art:", err)
		return
	}

	screenArtMu.Lock()
	defer screenArtMu.Unlock()
	artProbedAt = time.Now()
	if len(photos.Photos) == 0 || len(photos.Photos[0]) == 0 {
		// Remember the miss so the next render does not probe again; a removed
		// avatar must stop showing, so its cached ids go too.
		artFromAvatar = false
		rejectedAvatarID = ""
		screenArtIDs = map[string]string{}
		logger.Debug("Bot has no avatar; screens are sent as text")
		return
	}
	// The last entry is the largest size Telegram keeps.
	sizes := photos.Photos[0]
	fileID := sizes[len(sizes)-1].FileID
	if fileID == rejectedAvatarID {
		return
	}
	rejectedAvatarID = ""
	screenArtIDs = map[string]string{}
	for _, kind := range screenKinds {
		screenArtIDs[kind] = fileID
	}
	artFromAvatar = true
}

// ensureScreenArt refreshes stale cached art off the render path, at most one
// probe in flight, so a just-uploaded picture shows without a restart.
func (t *Tgbot) ensureScreenArt() {
	screenArtMu.RLock()
	fromAvatar, at := artFromAvatar, artProbedAt
	screenArtMu.RUnlock()

	ttl := artRetryTTL
	if fromAvatar {
		ttl = artAvatarTTL
	}
	if time.Since(at) <= ttl {
		return
	}
	// The first render waits for the probe once, or a bot with an avatar would
	// open as text and swap to a picture on the next tap.
	if at.IsZero() && artFirstProbe.CompareAndSwap(false, true) {
		t.resolveScreenArt()
		return
	}
	if !artProbeRunning.CompareAndSwap(false, true) {
		return
	}
	artProbes.Go(func() {
		defer artProbeRunning.Store(false)
		t.resolveScreenArt()
	})
}

// artFileID returns the cached file_id for a screen kind: the panel's own
// override for that kind first, then the bot's avatar.
func artFileID(kind string) (string, bool) {
	screenArtMu.RLock()
	defer screenArtMu.RUnlock()
	if id, ok := artOverrides[kind]; ok && id != "" {
		return id, true
	}
	id, ok := screenArtIDs[kind]
	return id, ok && id != ""
}

// Compare before eviction: an asynchronous probe may already have installed a new avatar.
func rejectArtFileID(fileID string) {
	screenArtMu.Lock()
	defer screenArtMu.Unlock()
	for kind, id := range screenArtIDs {
		if id == fileID {
			delete(screenArtIDs, kind)
			if artFromAvatar {
				rejectedAvatarID = fileID
			}
		}
	}
	if rejectedAvatarID == fileID {
		artFromAvatar = false
	}
	for kind, id := range artOverrides {
		if id == fileID {
			delete(artOverrides, kind)
		}
	}
}

// resetScreenArt drops the cached file_ids; tests use it between mock servers.
func resetScreenArt() {
	artProbes.Wait()
	screenArtMu.Lock()
	screenArtIDs = map[string]string{}
	artOverrides = map[string]string{}
	artFromAvatar = false
	rejectedAvatarID = ""
	artProbedAt = time.Time{}
	screenArtMu.Unlock()
	artFirstProbe.Store(false)
}

// screenKinds is the catalogue of screen pictures: every kind resolves to the
// same avatar, but the names keep each screen's intent visible in the code.
var screenKinds = []string{
	"main", "status", "inbounds", "clients", "client", "links", "qr",
	"wizard", "reports", "backup", "onlines", "deplete", "commands", "broadcast", "pay",
}
