package tgbot

import (
	"context"
	"net"
	"strconv"
	"strings"
	"time"

	"github.com/mhsanaei/3x-ui/v3/internal/logger"
	"github.com/mhsanaei/3x-ui/v3/internal/web/service"

	"github.com/mymmrac/telego"
	tu "github.com/mymmrac/telego/telegoutil"
)

// AI services ban accounts whose IP keeps changing, so a trusted client can pin
// a permanent exit per country: a sole server at once, otherwise via a short quiz.
const (
	cbAIExit        = "ai"
	cbAIExitHome    = "ai_o"
	cbAIExitCountry = "ai_c"
	cbAIExitUsed    = "ai_y"
	cbAIExitNew     = "ai_n"
	cbAIExitList    = "ai_l"
	cbAIExitAuto    = "ai_a"
	cbAIExitPin     = "ai_p"
	cbAIExitWhere   = "ai_k"
	// anyCountry in cbAIExitWhere data drops the user's one AI country.
	anyCountry = "*"
	// noCountry stands in callback data for exits without a country code.
	noCountry = "-"
)

// trustedEmails are the subscriptions of tgUserID whose owner the admin trusts.
func (t *Tgbot) trustedEmails(tgUserID int64) []string {
	traffics, err := t.inboundService.GetClientTrafficTgBot(tgUserID)
	if err != nil {
		return nil
	}
	var out []string
	for _, traffic := range traffics {
		if t.billingFor(traffic.Email).Trusted {
			out = append(out, traffic.Email)
		}
	}
	return out
}

func isAIExitCallback(data string) bool {
	return data == cbAIExit || strings.HasPrefix(data, "ai_")
}

func (t *Tgbot) answerAIExit(callbackQuery *telego.CallbackQuery) {
	chatID := callbackQuery.Message.GetChat().ID
	emails := t.trustedEmails(callbackQuery.From.ID)
	if len(emails) == 0 {
		t.sendCallbackAnswerTgBot(callbackQuery.ID, t.I18nBot("tgbot.answers.aiExitTrustedOnly"))
		return
	}
	action, arg, _ := strings.Cut(callbackQuery.Data, " ")
	if action == cbAIExit {
		t.answerSilent(callbackQuery.ID)
		t.startAIExit(chatID, emails)
		return
	}
	if action == cbAIExitHome {
		t.answerSilent(callbackQuery.ID)
		t.screenAIExitOverview(chatID, emails, "")
		return
	}
	if action == cbAIExitWhere {
		t.answerSilent(callbackQuery.ID)
		t.answerAIExitWhere(chatID, emails, arg)
		return
	}
	if action == cbAIExitPin {
		nodeID, err := strconv.Atoi(arg)
		if err != nil {
			t.sendCallbackAnswerTgBot(callbackQuery.ID, t.I18nBot("tgbot.answers.errorOperation"))
			return
		}
		t.pinAIExit(callbackQuery, chatID, emails, nodeID)
		return
	}
	groups, aiCountry := t.aiExitGroups(emails)
	group, ok := findExitGroup(groups, arg)
	if !ok {
		t.sendCallbackAnswerTgBot(callbackQuery.ID, t.I18nBot("tgbot.answers.errorOperation"))
		return
	}
	switch action {
	case cbAIExitCountry:
		t.answerSilent(callbackQuery.ID)
		t.screenAIExitQuiz(chatID, group, groups, aiCountry)
	case cbAIExitUsed:
		t.answerSilent(callbackQuery.ID)
		t.screenAIExitServers(chatID, group, "tgbot.messages.aiExitWhichUsed", true)
	case cbAIExitNew:
		t.answerSilent(callbackQuery.ID)
		t.screenAIExitHow(chatID, group)
	case cbAIExitList:
		t.answerSilent(callbackQuery.ID)
		t.screenAIExitServers(chatID, group, "tgbot.messages.aiExitPick", false)
	case cbAIExitAuto:
		best, ok := group.Best()
		if !ok {
			t.sendCallbackAnswerTgBot(callbackQuery.ID, t.I18nBot("tgbot.answers.aiExitAllBusy"))
			return
		}
		t.pinAIExit(callbackQuery, chatID, emails, best.NodeId)
	default:
		t.sendCallbackAnswerTgBot(callbackQuery.ID, t.I18nBot("tgbot.answers.errorOperation"))
	}
}

func (t *Tgbot) allAIExitGroups(emails []string) []service.UserExitGroup {
	groups, err := (&service.CascadeService{}).UserExitGroups(emails)
	if err != nil {
		logger.Warning("AI exit: list exits:", err)
	}
	return groups
}

// aiExitGroups are the countries the user may pin in: only their AI country
// when they named one that is still among their exits.
func (t *Tgbot) aiExitGroups(emails []string) ([]service.UserExitGroup, string) {
	groups := t.allAIExitGroups(emails)
	country := (&service.CascadeService{}).AICountry(emails)
	if g, ok := findExitGroup(groups, country); ok && country != "" {
		return []service.UserExitGroup{g}, country
	}
	return groups, ""
}

func findExitGroup(groups []service.UserExitGroup, country string) (service.UserExitGroup, bool) {
	for _, g := range groups {
		if countryToken(g.Country) == country {
			return g, true
		}
	}
	return service.UserExitGroup{}, false
}

// startAIExit keeps the user's AI country pinned, or asks for one when their
// exits span several countries.
func (t *Tgbot) startAIExit(chatID int64, emails []string) {
	if country := (&service.CascadeService{}).AICountry(emails); country != "" {
		t.useAIIn(chatID, emails, country, false)
		return
	}
	if groups := t.allAIExitGroups(emails); len(groups) > 1 {
		t.screenAIExitWhere(chatID, groups)
		return
	}
	t.startAIExitAnywhere(chatID, emails)
}

func (t *Tgbot) answerAIExitWhere(chatID int64, emails []string, arg string) {
	switch arg {
	case "":
		t.screenAIExitWhere(chatID, t.allAIExitGroups(emails))
	case anyCountry:
		if _, _, err := (&service.CascadeService{}).SetAICountry(context.Background(), emails, ""); err != nil {
			logger.Warning("AI exit: clear AI country:", err)
		}
		t.startAIExitAnywhere(chatID, emails)
	default:
		t.useAIIn(chatID, emails, arg, true)
	}
}

// useAIIn makes country the user's only AI country, which pins its best server
// at once; a fresh choice among several servers still goes through the quiz.
func (t *Tgbot) useAIIn(chatID int64, emails []string, country string, chosen bool) {
	_, pinned, err := (&service.CascadeService{}).SetAICountry(context.Background(), emails, country)
	if err != nil {
		logger.Warning("AI exit: use AI in", country, ":", err)
		t.screenAIExitWhere(chatID, t.allAIExitGroups(emails))
		return
	}
	groups, aiCountry := t.aiExitGroups(emails)
	if !pinned {
		t.screenAIExitOverview(chatID, emails, t.I18nBot("tgbot.answers.aiExitAllBusy"))
		return
	}
	if chosen && len(groups) == 1 && len(groups[0].Exits) > 1 {
		t.screenAIExitQuiz(chatID, groups[0], groups, aiCountry)
		return
	}
	t.screenAIExitOverview(chatID, emails, "")
}

// screenAIExitWhere asks which country the user uses AI from.
func (t *Tgbot) screenAIExitWhere(chatID int64, groups []service.UserExitGroup) {
	if len(groups) == 0 {
		t.renderScreen(chatID, t.newScreen("main", t.I18nBot("tgbot.messages.aiExitNoServers")))
		return
	}
	var rows [][]telego.InlineKeyboardButton
	for _, g := range groups {
		if g.Country == "" {
			continue
		}
		rows = append(rows, tu.InlineKeyboardRow(t.btn2(
			t.I18nBot("tgbot.buttons.aiExitOnlyIn", "Country=="+countryLabel(g.Country)),
			cbAIExitWhere+" "+countryToken(g.Country))))
	}
	rows = append(rows, tu.InlineKeyboardRow(t.btn("tgbot.buttons.aiExitAnyCountry", cbAIExitWhere+" "+anyCountry)), t.backRow())
	body := t.aiExitStatus(groups, "") + t.I18nBot("tgbot.messages.aiExitWhere")
	t.renderScreen(chatID, t.newScreen("main", body, rows...))
}

// startAIExitAnywhere pins sole servers, then opens the quiz when exactly one
// country still needs a choice, or the overview of every country otherwise.
func (t *Tgbot) startAIExitAnywhere(chatID int64, emails []string) {
	if _, err := (&service.CascadeService{}).PinSoleExits(context.Background(), emails); err != nil {
		logger.Warning("AI exit: pin sole exits:", err)
	}
	groups := t.allAIExitGroups(emails)
	var undecided []service.UserExitGroup
	for _, g := range groups {
		if _, pinned := g.Pinned(); !pinned && len(g.Exits) > 1 {
			undecided = append(undecided, g)
		}
	}
	if len(undecided) == 1 {
		t.screenAIExitQuiz(chatID, undecided[0], groups, "")
		return
	}
	t.screenAIExitOverview(chatID, emails, "")
}

// screenAIExitOverview shows the pins and, below them, an optional note.
func (t *Tgbot) screenAIExitOverview(chatID int64, emails []string, note string) {
	groups, aiCountry := t.aiExitGroups(emails)
	if len(groups) == 0 {
		t.renderScreen(chatID, t.newScreen("main", t.I18nBot("tgbot.messages.aiExitNoServers")))
		return
	}
	var rows [][]telego.InlineKeyboardButton
	for _, g := range groups {
		if len(g.Exits) > 1 {
			rows = append(rows, tu.InlineKeyboardRow(t.btn2(
				t.I18nBot("tgbot.buttons.aiExitChoose", "Country=="+countryLabel(g.Country)),
				cbAIExitCountry+" "+countryToken(g.Country))))
		}
	}
	if aiCountry != "" || len(groups) > 1 {
		rows = append(rows, tu.InlineKeyboardRow(t.btn("tgbot.buttons.aiExitCountry", cbAIExitWhere)))
	}
	rows = append(rows, t.backRow())
	body := t.aiExitStatus(groups, aiCountry)
	if note != "" {
		body += "\r\n" + note
	}
	t.renderScreen(chatID, t.newScreen("main", body, rows...))
}

// aiExitStatus is the intro, the user's AI country if any and, per country,
// the exit the user is pinned to.
func (t *Tgbot) aiExitStatus(groups []service.UserExitGroup, aiCountry string) string {
	var body strings.Builder
	body.WriteString(t.I18nBot("tgbot.messages.aiExitIntro"))
	if aiCountry != "" {
		body.WriteString(t.I18nBot("tgbot.messages.aiExitOnlyIn", "Country=="+countryLabel(aiCountry)))
	}
	for _, g := range groups {
		if exit, ok := g.Pinned(); ok {
			body.WriteString(t.I18nBot("tgbot.messages.aiExitPinnedLine", "Country=="+countryLabel(g.Country), "Server=="+exitLabel(exit)))
		} else {
			body.WriteString(t.I18nBot("tgbot.messages.aiExitUnpinnedLine", "Country=="+countryLabel(g.Country)))
		}
	}
	return body.String()
}

func (t *Tgbot) screenAIExitQuiz(chatID int64, g service.UserExitGroup, groups []service.UserExitGroup, aiCountry string) {
	token := countryToken(g.Country)
	body := t.aiExitStatus(groups, aiCountry) + t.I18nBot("tgbot.messages.aiExitUsedBefore", "Country=="+countryLabel(g.Country))
	t.renderScreen(chatID, t.newScreen("main", body,
		tu.InlineKeyboardRow(
			t.btn("tgbot.buttons.aiExitYes", cbAIExitUsed+" "+token),
			t.btn("tgbot.buttons.aiExitNo", cbAIExitNew+" "+token),
		),
		t.backTo(cbAIExitHome),
	))
}

func (t *Tgbot) screenAIExitHow(chatID int64, g service.UserExitGroup) {
	token := countryToken(g.Country)
	t.renderScreen(chatID, t.newScreen("main", t.I18nBot("tgbot.messages.aiExitHow"),
		tu.InlineKeyboardRow(t.btn("tgbot.buttons.aiExitPickFromList", cbAIExitList+" "+token)),
		tu.InlineKeyboardRow(t.btn("tgbot.buttons.aiExitAuto", cbAIExitAuto+" "+token)),
		t.backTo(cbAIExitCountry+" "+token),
	))
}

// screenAIExitServers lists the country's servers that still have speed to
// spare; a saturated one is never offered, even as the IP the user remembers.
func (t *Tgbot) screenAIExitServers(chatID int64, g service.UserExitGroup, promptKey string, offerForgot bool) {
	token := countryToken(g.Country)
	var rows [][]telego.InlineKeyboardButton
	for _, exit := range g.Open() {
		rows = append(rows, tu.InlineKeyboardRow(t.btn2(exitLabel(exit), cbAIExitPin+" "+strconv.Itoa(exit.NodeId))))
	}
	body := t.I18nBot(promptKey, "Country=="+countryLabel(g.Country))
	if len(rows) == 0 {
		body = t.I18nBot("tgbot.answers.aiExitAllBusy")
	} else if len(rows) < len(g.Exits) {
		body += t.I18nBot("tgbot.messages.aiExitBusyHidden")
	}
	if offerForgot {
		rows = append(rows, tu.InlineKeyboardRow(t.btn("tgbot.buttons.aiExitForgot", cbAIExitNew+" "+token)))
	}
	rows = append(rows, t.backTo(cbAIExitCountry+" "+token))
	t.renderScreen(chatID, t.newScreen("main", body, rows...))
}

func (t *Tgbot) pinAIExit(callbackQuery *telego.CallbackQuery, chatID int64, emails []string, nodeID int) {
	exit, err := (&service.CascadeService{}).PinUserExit(context.Background(), emails, nodeID)
	if err != nil {
		logger.Warning("AI exit: pin", nodeID, "for", emails, ":", err)
		t.sendCallbackAnswerTgBot(callbackQuery.ID, t.I18nBot("tgbot.answers.aiExitUnavailable"))
		t.screenAIExitOverview(chatID, emails, "")
		return
	}
	t.sendCallbackAnswerTgBot(callbackQuery.ID, t.I18nBot("tgbot.answers.aiExitPinned", "Server=="+exitLabel(exit)))
	t.screenAIExitOverview(chatID, emails, "")
}

func countryToken(country string) string {
	if country == "" {
		return noCountry
	}
	return country
}

// countryLabel is the flag and code of a two-letter country, or "—" for none.
func countryLabel(country string) string {
	if len(country) != 2 {
		return "—"
	}
	flag := ""
	for _, r := range strings.ToUpper(country) {
		if r < 'A' || r > 'Z' {
			return country
		}
		flag += string(0x1F1E6 + r - 'A')
	}
	return flag + " " + strings.ToUpper(country)
}

// exitLabel names a server by the IP the user would recognise from an AI
// service or an IP checker, resolving a node addressed by domain.
func exitLabel(exit service.UserExit) string {
	name := exit.Name
	if exit.NodeId == 0 {
		name = hostname
	}
	ip := exitIP(exit.Address)
	if ip == "" {
		return name
	}
	return name + " · " + ip
}

func exitIP(address string) string {
	if address == "" || net.ParseIP(address) != nil {
		return address
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	ips, err := net.DefaultResolver.LookupIPAddr(ctx, address)
	if err != nil || len(ips) == 0 {
		return address
	}
	return ips[0].IP.String()
}
