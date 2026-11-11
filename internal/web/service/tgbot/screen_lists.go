package tgbot

import (
	"sort"
	"strconv"
	"strings"
	"sync"

	"github.com/mhsanaei/3x-ui/v3/internal/logger"

	"github.com/mymmrac/telego"
	tu "github.com/mymmrac/telego/telegoutil"
)

const clientListPageSize = 10

const (
	cbClientListPage   = "clp"
	cbInboundListPage  = "inbp"
	cbClientOpen       = "client_open"
	cbClientSearch     = "cls"
	cbClientSearchDrop = "clsx"
)

const clientSearchStatePrefix = "client_search:"

func clientSearchState(inboundID int) string {
	return clientSearchStatePrefix + strconv.Itoa(inboundID)
}

func parseClientSearchState(state string) (int, bool) {
	rest, found := strings.CutPrefix(state, clientSearchStatePrefix)
	if !found {
		return 0, false
	}
	inboundID, err := strconv.Atoi(rest)
	return inboundID, err == nil
}

type clientFilter struct {
	inboundID int
	query     string
}

// clientListFilters holds, per chat, the text typed to narrow one list; paging
// and a card's Back keep it, opening the list afresh drops it.
var clientListFilters sync.Map

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

func filterEntries(entries []listEntry, query string) []listEntry {
	query = strings.ToLower(query)
	out := entries[:0:0]
	for _, entry := range entries {
		if strings.Contains(strings.ToLower(entry.label), query) {
			out = append(out, entry)
		}
	}
	return out
}

// screenClientList is the client browser: a page of client buttons with the
// search at the bottom, inline when the bot supports it, typed text otherwise.
func (t *Tgbot) screenClientList(chatID int64, inboundID, page int) {
	entries := t.clientEntries(inboundID)
	filter := ""
	if stored, ok := clientListFilters.Load(chatID); ok && stored.(clientFilter).inboundID == inboundID {
		filter = stored.(clientFilter).query
	}
	if filter != "" {
		entries = filterEntries(entries, filter)
	}
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
	if filter != "" {
		body += "\n" + t.I18nBot("tgbot.messages.clientSearchQuery", "Query=="+filter)
	}
	rows := listRows
	if filter != "" {
		rows = append(rows, tu.InlineKeyboardRow(t.btn("tgbot.buttons.clearSearch", cbClientSearchDrop+" "+strconv.Itoa(inboundID))))
	}
	search := t.searchRow(scopeQuery(inlineScopeClients, scope), "tgbot.buttons.searchClients")
	if search == nil {
		search = tu.InlineKeyboardRow(t.btn("tgbot.buttons.searchClients", cbClientSearch+" "+strconv.Itoa(inboundID)))
	}
	rows = append(rows, search, t.backTo(parent))
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
	rows := listRows
	if search := t.searchRow(scopeQuery(inlineScopeInbounds, ""), "tgbot.buttons.searchInbounds"); search != nil {
		rows = append(rows, search)
	}
	rows = append(rows, t.backTo(cbCatServer))
	t.renderScreen(chatID, t.newScreen("inbounds", body, rows...))
}

// promptClientSearch waits for the admin's typed text; Cancel returns to the list.
func (t *Tgbot) promptClientSearch(chatID int64, actor chatUser, inboundID int) {
	userStateMgr.set(actor, clientSearchState(inboundID))
	cancel := tu.InlineKeyboardRow(t.btn("tgbot.buttons.cancel", clientListCallback(inboundID, 0)))
	t.renderScreen(chatID, t.newScreen("clients", t.I18nBot("tgbot.messages.clientSearchPrompt"), cancel))
}

func (t *Tgbot) handleClientSearchInput(message *telego.Message, actor chatUser, inboundID int) {
	t.deleteIncoming(message)
	userStateMgr.clear(actor)
	if !checkAdmin(message.From.ID) {
		return
	}
	if query := strings.TrimSpace(message.Text); query != "" {
		clientListFilters.Store(actor.chatID, clientFilter{inboundID: inboundID, query: query})
	}
	t.screenClientList(actor.chatID, inboundID, 0)
}

// openClientList shows a list's first page with no search applied.
func (t *Tgbot) openClientList(chatID int64, inboundID int) {
	clientListFilters.Delete(chatID)
	t.screenClientList(chatID, inboundID, 0)
}
