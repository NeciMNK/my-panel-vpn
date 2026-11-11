package tgbot

import (
	"context"
	"strconv"
	"strings"
	"time"

	"github.com/mhsanaei/3x-ui/v3/internal/logger"

	"github.com/mymmrac/telego"
	tu "github.com/mymmrac/telego/telegoutil"
)

const tagSeparator = " · "

// telegramTag names an account the way the admin searches for it: @username,
// else the id with the display name, since a name alone is not unique.
func telegramTag(id int64, username, firstName, lastName string) string {
	if username != "" {
		return "@" + username
	}
	tag := "tg:" + strconv.FormatInt(id, 10)
	name := strings.NewReplacer("(", " ", ")", " ", "·", " ").Replace(firstName + " " + lastName)
	if name = strings.Join(strings.Fields(name), " "); name != "" {
		tag += " (" + name + ")"
	}
	return tag
}

// mergeTelegramTag puts tag in front of comment, dropping only the bot's own
// earlier tag prev; any other leading "@name" is the admin's.
func mergeTelegramTag(comment, prev, tag string) string {
	rest := comment
	for _, own := range []string{tag, prev} {
		if own == "" {
			continue
		}
		if rest == own {
			rest = ""
		} else if after, ok := strings.CutPrefix(rest, own+tagSeparator); ok {
			rest = after
		}
	}
	if rest = strings.TrimSpace(rest); rest == "" {
		return tag
	}
	return tag + tagSeparator + rest
}

// syncTelegramTag runs on every update a client sends, so a username change
// reaches the panel on the next tap; it writes only when the comment differs.
func (t *Tgbot) syncTelegramTag(user *telego.User) {
	if user == nil || user.ID <= 0 || user.IsBot {
		return
	}
	t.applyTelegramTag(user.ID, telegramTag(user.ID, user.Username, user.FirstName, user.LastName))
}

func (t *Tgbot) applyTelegramTag(tgID int64, tag string) {
	records, err := t.clientService.GetRecordsByTgID(tgID)
	if err != nil {
		logger.Warning("tgbot: load clients for tag sync failed:", err)
		return
	}
	prev, err := t.clientService.GetTelegramTag(tgID)
	if err != nil {
		logger.Warning("tgbot: load the last telegram tag failed:", err)
		return
	}
	for _, record := range records {
		comment := mergeTelegramTag(record.Comment, prev, tag)
		if comment == record.Comment {
			continue
		}
		needRestart, err := t.clientService.SetClientCommentByEmail(&t.inboundService, record.Email, comment)
		if needRestart {
			t.xrayService.SetToNeedRestart()
		}
		if err != nil {
			logger.Warning("tgbot: write telegram tag for", record.Email, "failed:", err)
		}
	}
	// Recorded after the writes: until then the old tag is still prev, and a comment
	// already rewritten carries the new tag, which merge also treats as its own.
	if prev != tag {
		if err := t.clientService.SetTelegramTag(tgID, tag); err != nil {
			logger.Warning("tgbot: record the telegram tag failed:", err)
		}
	}
}

// Spaced out so a large client list stays well under Telegram's request limits.
const (
	backfillLookupGap   = 100 * time.Millisecond
	backfillMaxFailures = 3
)

// backfillTelegramTags tags clients bound before the bot tracked usernames:
// they may never write again, so their accounts are looked up once per start.
func (t *Tgbot) backfillTelegramTags() {
	ids, err := t.clientService.GetBoundTelegramIDs()
	if err != nil {
		logger.Warning("tgbot: list bound telegram accounts failed:", err)
		return
	}
	failures := 0
	for i, id := range ids {
		if i > 0 {
			time.Sleep(backfillLookupGap)
		}
		b := liveBot()
		if b == nil {
			return
		}
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		chat, err := b.GetChat(ctx, &telego.GetChatParams{ChatID: tu.ID(id)})
		cancel()
		if err != nil {
			// A blocked Telegram fails every lookup; the next start retries them all.
			if failures++; failures >= backfillMaxFailures {
				logger.Warning("tgbot: telegram tag backfill stopped:", err)
				return
			}
			continue
		}
		if chat == nil || chat.Type != telego.ChatTypePrivate {
			continue
		}
		t.applyTelegramTag(id, telegramTag(id, chat.Username, chat.FirstName, chat.LastName))
	}
}
