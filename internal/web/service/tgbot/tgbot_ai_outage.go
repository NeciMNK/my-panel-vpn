package tgbot

import (
	"context"
	"html"
	"slices"
	"strconv"
	"strings"

	"github.com/mhsanaei/3x-ui/v3/internal/database/model"
	"github.com/mhsanaei/3x-ui/v3/internal/eventbus"
	"github.com/mhsanaei/3x-ui/v3/internal/logger"
	"github.com/mhsanaei/3x-ui/v3/internal/web/service"

	"github.com/mymmrac/telego"
	tu "github.com/mymmrac/telego/telegoutil"
)

// When a server a client is pinned to goes down they choose, once and for
// every later outage, what replaces it; the prefix must not start with "ai_".
const (
	cbAIOutage        = "aio"
	cbAIOutageOther   = "aio_o"
	cbAIOutageCountry = "aio_c"
	cbAIOutageNoUse   = "aio_n"
	cbAIOutageOff     = "aio_f"
	cbAIOutageBlock   = "aio_b"
	cbAIOutageToggle  = "aio_t"
	cbAIOutageAll     = "aio_a"
)

func isAIOutageCallback(data string) bool {
	return data == cbAIOutage || strings.HasPrefix(data, "aio_")
}

// notifyAIPinOutage tells every client pinned to a server that went down what
// happens now and asks what they want, and tells them when it is back.
func (t *Tgbot) notifyAIPinOutage(e eventbus.Event) {
	if e.Type != eventbus.EventNodeDown && e.Type != eventbus.EventNodeUp {
		return
	}
	data, ok := e.Data.(*eventbus.NodeHealthData)
	if !ok {
		return
	}
	holders, err := (&service.CascadeService{}).AIPinHolders(data.NodeId)
	if err != nil {
		logger.Warning("AI outage: list the users pinned to node", data.NodeId, ":", err)
		return
	}
	server := html.EscapeString(nodeName(data.NodeId))
	told := map[int64]bool{}
	for _, email := range holders {
		tgID := t.clientTgID(email)
		if tgID == 0 || told[tgID] {
			continue
		}
		told[tgID] = true
		if e.Type == eventbus.EventNodeUp {
			t.sendNotice(tgID, t.I18nBot("tgbot.messages.aiOutageBack", "Server=="+server))
			continue
		}
		emails := t.ownEmails(tgID)
		if len(emails) == 0 {
			continue
		}
		head := t.I18nBot("tgbot.messages.aiOutageNotice", "Server=="+server)
		t.renderScreen(tgID, t.aiOutageScreen(tgID, emails, head), screenSendNew)
	}
}

func (t *Tgbot) ownEmails(tgUserID int64) []string {
	traffics, err := t.inboundService.GetClientTrafficTgBot(tgUserID)
	if err != nil {
		return nil
	}
	out := make([]string, 0, len(traffics))
	for _, traffic := range traffics {
		out = append(out, traffic.Email)
	}
	return out
}

func nodeName(nodeID int) string {
	if nodeID == 0 {
		return hostname
	}
	if n, err := (&service.NodeService{}).GetById(nodeID); err == nil && n.Name != "" {
		return n.Name
	}
	return "#" + strconv.Itoa(nodeID)
}

func (t *Tgbot) answerAIOutage(callbackQuery *telego.CallbackQuery) {
	chatID, userID := callbackQuery.Message.GetChat().ID, callbackQuery.From.ID
	emails := t.ownEmails(userID)
	if len(emails) == 0 {
		t.sendCallbackAnswerTgBot(callbackQuery.ID, t.I18nBot("tgbot.answers.errorOperation"))
		return
	}
	cascades := &service.CascadeService{}
	current := cascades.AIOutageChoiceOf(emails)
	action, arg, _ := strings.Cut(callbackQuery.Data, " ")
	set := func(choice model.AIOutageChoice) bool {
		if err := cascades.SetAIOutageChoice(context.Background(), emails, choice); err != nil {
			logger.Warning("AI outage: save the choice of", userID, ":", err)
			t.sendCallbackAnswerTgBot(callbackQuery.ID, t.I18nBot("tgbot.answers.errorOperation"))
			return false
		}
		return true
	}
	switch action {
	case cbAIOutage:
	case cbAIOutageNoUse:
		t.answerSilent(callbackQuery.ID)
		t.renderScreen(chatID, t.newScreen("main", t.I18nBot("tgbot.messages.aiOutageNoUse"),
			tu.InlineKeyboardRow(t.btn("tgbot.buttons.aiOutageOff", cbAIOutageOff)),
			tu.InlineKeyboardRow(t.btn("tgbot.buttons.aiOutageBlock", cbAIOutageBlock)),
			t.backTo(cbAIOutage)))
		return
	case cbAIOutageOff:
		if !set(model.AIOutageChoice{Action: service.AIOutageOff}) {
			return
		}
	case cbAIOutageOther, cbAIOutageCountry:
		country := current.Country
		if action == cbAIOutageCountry {
			country = arg
		}
		if !set(model.AIOutageChoice{Action: service.AIOutageOther, Country: country}) {
			return
		}
		if action == cbAIOutageOther && t.hasUnswappedOutage(emails) {
			t.answerSilent(callbackQuery.ID)
			t.screenAIOutageCountries(chatID, emails)
			return
		}
	case cbAIOutageBlock, cbAIOutageAll, cbAIOutageToggle:
		services := current.Services
		if current.Action != service.AIOutageBlock || action == cbAIOutageAll {
			services = nil
		}
		if action == cbAIOutageToggle {
			var ok bool
			if services, ok = toggleAIService(services, arg); !ok {
				t.sendCallbackAnswerTgBot(callbackQuery.ID, t.I18nBot("tgbot.answers.aiOutageKeepOne"))
				return
			}
		}
		if !set(model.AIOutageChoice{Action: service.AIOutageBlock, Services: services}) {
			return
		}
		t.answerSilent(callbackQuery.ID)
		t.screenAIOutageServices(chatID, services)
		return
	default:
		t.sendCallbackAnswerTgBot(callbackQuery.ID, t.I18nBot("tgbot.answers.errorOperation"))
		return
	}
	t.answerSilent(callbackQuery.ID)
	t.renderScreen(chatID, t.aiOutageScreen(userID, emails, ""))
}

// toggleAIService flips name in the blocked set, where empty means all of
// them; it refuses to leave none blocked, which would mean all again.
func toggleAIService(blocked []string, name string) ([]string, bool) {
	all := service.AIServiceNames()
	if len(blocked) == 0 {
		blocked = slices.Clone(all)
	}
	if i := slices.Index(blocked, name); i >= 0 {
		blocked = slices.Delete(slices.Clone(blocked), i, i+1)
	} else if slices.Contains(all, name) {
		blocked = append(slices.Clone(blocked), name)
	}
	if len(blocked) == 0 {
		return nil, false
	}
	if len(blocked) == len(all) {
		return nil, true
	}
	return blocked, true
}

func (t *Tgbot) hasUnswappedOutage(emails []string) bool {
	outages, err := (&service.CascadeService{}).AIPinOutages(emails)
	if err != nil {
		return false
	}
	return slices.ContainsFunc(outages, func(o service.AIPinOutage) bool { return !o.Swapped })
}

// aiOutageScreen states what happens during an outage, what is happening now
// if a pinned server is down, and offers the two ways to change it.
func (t *Tgbot) aiOutageScreen(tgUserID int64, emails []string, head string) *screen {
	var body strings.Builder
	body.WriteString(head)
	outages, err := (&service.CascadeService{}).AIPinOutages(emails)
	if err != nil {
		logger.Warning("AI outage: read the outages of", emails, ":", err)
	}
	for _, o := range dedupeOutages(outages) {
		body.WriteString(t.I18nBot("tgbot.messages.aiOutageDownLine", "Server=="+html.EscapeString(nodeName(o.NodeId))))
		switch {
		case o.Swapped:
			body.WriteString(t.I18nBot("tgbot.messages.aiOutageNowSwapped", "Server=="+html.EscapeString(nodeName(o.SwapNodeId))))
		case o.Off:
			body.WriteString(t.I18nBot("tgbot.messages.aiOutageNowOff"))
		default:
			body.WriteString(t.I18nBot("tgbot.messages.aiOutageNowBlocked",
				"Services=="+html.EscapeString(strings.Join(service.AIServicesIn(o.Domains), ", "))))
		}
	}
	body.WriteString(t.aiOutageChoiceLine((&service.CascadeService{}).AIOutageChoiceOf(emails)))
	back := t.backRow()
	if len(t.aiExitEmails(tgUserID)) > 0 {
		back = t.backTo(cbAIExitHome)
	}
	return t.newScreen("main", body.String(),
		tu.InlineKeyboardRow(t.btn("tgbot.buttons.aiOutageOther", cbAIOutageOther)),
		tu.InlineKeyboardRow(t.btn("tgbot.buttons.aiOutageNoUse", cbAIOutageNoUse)),
		back)
}

// dedupeOutages keeps one line per dead server: a person's subscriptions share it.
func dedupeOutages(outages []service.AIPinOutage) []service.AIPinOutage {
	var out []service.AIPinOutage
	for _, o := range outages {
		if !slices.ContainsFunc(out, func(seen service.AIPinOutage) bool { return seen.NodeId == o.NodeId }) {
			out = append(out, o)
		}
	}
	return out
}

func (t *Tgbot) aiOutageChoiceLine(choice model.AIOutageChoice) string {
	switch choice.Action {
	case service.AIOutageOther:
		if choice.Country != "" {
			return t.I18nBot("tgbot.messages.aiOutageChoiceOtherIn", "Country=="+countryLabel(choice.Country))
		}
		return t.I18nBot("tgbot.messages.aiOutageChoiceOther")
	case service.AIOutageOff:
		return t.I18nBot("tgbot.messages.aiOutageChoiceOff")
	}
	if len(choice.Services) > 0 {
		return t.I18nBot("tgbot.messages.aiOutageChoiceBlock",
			"Services=="+html.EscapeString(strings.Join(choice.Services, ", ")))
	}
	return t.I18nBot("tgbot.messages.aiOutageChoiceBlockAll")
}

// screenAIOutageCountries asks where to look when the dead server's own
// country has no live server left.
func (t *Tgbot) screenAIOutageCountries(chatID int64, emails []string) {
	var rows [][]telego.InlineKeyboardButton
	for _, g := range t.allAIExitGroups(emails) {
		if g.Country == "" {
			continue
		}
		rows = append(rows, tu.InlineKeyboardRow(t.btn2(countryLabel(g.Country), cbAIOutageCountry+" "+g.Country)))
	}
	body := t.I18nBot("tgbot.messages.aiOutagePickCountry")
	if len(rows) == 0 {
		body = t.I18nBot("tgbot.messages.aiOutageNoCountry")
	}
	rows = append(rows, t.backTo(cbAIOutage))
	t.renderScreen(chatID, t.newScreen("main", body, rows...))
}

func (t *Tgbot) screenAIOutageServices(chatID int64, blocked []string) {
	var rows [][]telego.InlineKeyboardButton
	for _, name := range service.AIServiceNames() {
		mark := "✅ "
		if len(blocked) > 0 && !slices.Contains(blocked, name) {
			mark = "▫️ "
		}
		rows = append(rows, tu.InlineKeyboardRow(t.btn2(mark+name, t.encodeQuery(cbAIOutageToggle+" "+name))))
	}
	rows = append(rows,
		tu.InlineKeyboardRow(t.btn("tgbot.buttons.aiOutageAll", cbAIOutageAll)),
		t.backTo(cbAIOutage))
	t.renderScreen(chatID, t.newScreen("main", t.I18nBot("tgbot.messages.aiOutageServices"), rows...))
}
