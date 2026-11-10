package tgbot

import (
	"sort"
	"strconv"
	"sync"

	"github.com/mhsanaei/3x-ui/v3/internal/logger"

	"github.com/mymmrac/telego"
	tu "github.com/mymmrac/telego/telegoutil"
)

// clientListPageSize keeps a page of client buttons inside one phone screen.
const clientListPageSize = 8

const (
	cbClientListPage  = "clp"
	cbInboundListPage = "inbp"
	cbClientOpen      = "client_open"
)

// clientListCallback names one page of a client list; inbound 0 is every client.
func clientListCallback(inboundID, page int) string {
	return cbClientListPage + " " + strconv.Itoa(inboundID) + " " + strconv.Itoa(page)
}

func inboundListCallback(page int) string {
	return cbInboundListPage + " " + strconv.Itoa(page)
}

// clientListOrigins remembers, per chat, the list page a client card was opened
// from, so the card's Back returns there instead of to the menu.
var clientListOrigins sync.Map

func (t *Tgbot) clientListOrigin(chatID int64) string {
	if page, ok := clientListOrigins.Load(chatID); ok {
		return page.(string)
	}
	return cbClients
}

type listEntry struct {
	label string
	data  string
}

// pagedRows lays one page of entries out as buttons plus a ‹ n/m › row; the
// returned page is clamped, since a stale button may name a page now gone.
func (t *Tgbot) pagedRows(entries []listEntry, page int, pageData func(int) string) ([][]telego.InlineKeyboardButton, int) {
	pages := max((len(entries)+clientListPageSize-1)/clientListPageSize, 1)
	page = min(max(page, 0), pages-1)
	rows := [][]telego.InlineKeyboardButton{}
	for _, entry := range entries[min(page*clientListPageSize, len(entries)):min((page+1)*clientListPageSize, len(entries))] {
		rows = append(rows, tu.InlineKeyboardRow(t.btn2(entry.label, t.encodeQuery(entry.data))))
	}
	if pages > 1 {
		arrow := func(label, data string) telego.InlineKeyboardButton {
			return tu.InlineKeyboardButton(label).WithCallbackData(data)
		}
		prev, next := arrow("·", cbPageNone), arrow("·", cbPageNone)
		if page > 0 {
			prev = arrow("‹", pageData(page-1))
		}
		if page < pages-1 {
			next = arrow("›", pageData(page+1))
		}
		rows = append(rows, tu.InlineKeyboardRow(prev, t.btn2(t.pageLabel(page, pages), cbPageNone), next))
	}
	return rows, page
}

// clientEntries lists the clients of one inbound, or of all of them for 0,
// once each and sorted by email so a page holds the same clients every time.
func (t *Tgbot) clientEntries(inboundID int) []listEntry {
	inbounds, err := t.inboundService.GetAllInbounds()
	if err != nil {
		logger.Warning("Cannot list clients for the bot:", err)
		return nil
	}
	enabled := map[string]bool{}
	for _, ib := range inbounds {
		if inboundID != 0 && ib.Id != inboundID {
			continue
		}
		clients, err := t.inboundService.GetClients(ib)
		if err != nil {
			continue
		}
		for _, client := range clients {
			enabled[client.Email] = enabled[client.Email] || client.Enable
		}
	}
	emails := make([]string, 0, len(enabled))
	for email := range enabled {
		emails = append(emails, email)
	}
	sort.Strings(emails)
	entries := make([]listEntry, 0, len(emails))
	for _, email := range emails {
		mark := "❌ "
		if enabled[email] {
			mark = "✅ "
		}
		entries = append(entries, listEntry{label: mark + email, data: cbClientOpen + " " + email})
	}
	return entries
}

// screenClientList is the client browser: a page of client buttons, with the
// inline search on top only while inline mode is on.
func (t *Tgbot) screenClientList(chatID int64, inboundID, page int) {
	entries := t.clientEntries(inboundID)
	body := t.I18nBot("tgbot.messages.clientsCount", "Count=="+strconv.Itoa(len(entries)))
	parent, scope := cbCatClients, ""
	if inboundID != 0 {
		inbound, err := t.inboundService.GetInbound(inboundID)
		if err != nil {
			t.sendNotice(chatID, t.I18nBot("tgbot.answers.getInboundsFailed"))
			return
		}
		body = t.inboundSummaryText(inbound)
		parent, scope = cbInbounds, strconv.Itoa(inboundID)
	}
	listRows, page := t.pagedRows(entries, page, func(p int) string { return clientListCallback(inboundID, p) })
	clientListOrigins.Store(chatID, clientListCallback(inboundID, page))
	rows := [][]telego.InlineKeyboardButton{}
	if search := t.searchRow(scopeQuery(inlineScopeClients, scope), "tgbot.buttons.searchClients"); search != nil {
		rows = append(rows, search)
	}
	rows = append(rows, listRows...)
	rows = append(rows, t.backTo(parent))
	t.renderScreen(chatID, t.newScreen("clients", body, rows...))
}

// screenInboundList pages the inbounds; one tap opens an inbound's clients.
func (t *Tgbot) screenInboundList(chatID int64, page int) {
	inbounds, err := t.inboundService.GetAllInbounds()
	if err != nil {
		logger.Warning("Cannot list inbounds for the bot:", err)
		t.sendNotice(chatID, t.I18nBot("tgbot.answers.getInboundsFailed"))
		return
	}
	entries := make([]listEntry, 0, len(inbounds))
	for _, ib := range inbounds {
		mark := "❌ "
		if ib.Enable {
			mark = "✅ "
		}
		remark := ib.Remark
		if remark == "" {
			remark = "#" + strconv.Itoa(ib.Id)
		}
		entries = append(entries, listEntry{
			label: mark + remark + " · " + string(ib.Protocol) + " · " + strconv.Itoa(ib.Port),
			data:  "get_clients " + strconv.Itoa(ib.Id),
		})
	}
	body := t.I18nBot("tgbot.messages.inboundsCount", "Count=="+strconv.Itoa(len(entries)))
	listRows, _ := t.pagedRows(entries, page, inboundListCallback)
	rows := [][]telego.InlineKeyboardButton{}
	if search := t.searchRow(scopeQuery(inlineScopeInbounds, ""), "tgbot.buttons.searchInbounds"); search != nil {
		rows = append(rows, search)
	}
	rows = append(rows, listRows...)
	rows = append(rows, t.backTo(cbCatServer))
	t.renderScreen(chatID, t.newScreen("inbounds", body, rows...))
}
