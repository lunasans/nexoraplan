package server

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"regexp"
	"strconv"
	"strings"
	"time"

	"nexoraplan/internal/db"
	"nexoraplan/internal/telegram"
)

// Benachrichtigungen über einen eigenen Telegram-Bot. Einmal am Tag zur
// eingestellten Uhrzeit (Standard 07:00) gehen – je nach Einstellung – drei
// Nachrichten raus: Tagesübersicht, anstehende Abbuchungen und Warnungen
// (Engpass im Pocket, Budget überschritten). Warnungen kommen je Ereignis nur
// einmal. Alles ist standardmäßig aus.

var timeRe = regexp.MustCompile(`^([01]\d|2[0-3]):[0-5]\d$`)

type notifySettings struct {
	Payments    bool   `json:"payments"`
	PaymentDays int    `json:"payment_days"`
	Alerts      bool   `json:"alerts"`
	Digest      bool   `json:"digest"`
	Time        string `json:"time"`
}

func (s *Server) notifySettings(ctx context.Context) (notifySettings, error) {
	var firstErr error
	get := func(k string) string {
		v, err := s.repo.Setting(ctx, k)
		if err != nil && firstErr == nil {
			firstErr = err
		}
		return v
	}
	n := notifySettings{
		Payments: get(db.SettingNotifyPayments) == "1",
		Alerts:   get(db.SettingNotifyAlerts) == "1",
		Digest:   get(db.SettingNotifyDigest) == "1",
		Time:     get(db.SettingNotifyTime),
	}
	n.PaymentDays, _ = strconv.Atoi(get(db.SettingNotifyPayDays))
	if n.PaymentDays != 3 && n.PaymentDays != 7 {
		n.PaymentDays = 1
	}
	if !timeRe.MatchString(n.Time) {
		n.Time = "07:00"
	}
	return n, firstErr
}

// telegramBot liefert Bot und Chat; nil, wenn (noch) nicht verbunden.
func (s *Server) telegramBot(ctx context.Context) (*telegram.Bot, int64, error) {
	sealed, err := s.repo.Setting(ctx, db.SettingTelegramToken)
	if err != nil || sealed == "" || s.box == nil {
		return nil, 0, err
	}
	token, err := s.box.Open(sealed, db.SettingTelegramToken)
	if err != nil {
		return nil, 0, errors.New("Telegram-Token nicht lesbar (PLAN_ENCRYPTION_KEY geändert?) – bitte neu eintragen")
	}
	bot, err := telegram.New(token)
	if err != nil {
		return nil, 0, err
	}
	raw, err := s.repo.Setting(ctx, db.SettingTelegramChat)
	if err != nil {
		return nil, 0, err
	}
	chat, _ := strconv.ParseInt(raw, 10, 64)
	return bot, chat, nil
}

// RunNotifier prüft jede Minute, ob die tägliche Runde fällig ist.
func (s *Server) RunNotifier(ctx context.Context) {
	tick := time.NewTicker(time.Minute)
	defer tick.Stop()
	for {
		if err := s.notifyTick(ctx); err != nil && ctx.Err() == nil {
			slog.Warn("Benachrichtigungen", "fehler", err)
		}
		select {
		case <-ctx.Done():
			return
		case <-tick.C:
		}
	}
}

// notifyTick: ab der eingestellten Uhrzeit einmal am Tag senden. War der
// Dienst um 7:00 aus, holt er es nach dem Start am selben Tag nach.
func (s *Server) notifyTick(ctx context.Context) error {
	bot, chat, err := s.telegramBot(ctx)
	if err != nil || bot == nil || chat == 0 {
		return err
	}
	cfg, err := s.notifySettings(ctx)
	if err != nil || !(cfg.Digest || cfg.Payments || cfg.Alerts) {
		return err
	}
	now := s.now().In(s.loc)
	if now.Format("15:04") < cfg.Time {
		return nil
	}
	today := s.today()
	first, err := s.repo.MarkSent(ctx, "daily:"+today.Format(db.DateLayout))
	if err != nil || !first {
		return err
	}
	s.repo.CleanupSent(ctx, s.now().AddDate(-2, 0, 0))
	var errs []error
	if cfg.Digest {
		if text, err := s.digestText(ctx, today); err != nil {
			errs = append(errs, err)
		} else if err := bot.Send(ctx, chat, text); err != nil {
			errs = append(errs, err)
		}
	}
	if cfg.Payments {
		if text, err := s.paymentsText(ctx, today, cfg.PaymentDays); err != nil {
			errs = append(errs, err)
		} else if text != "" {
			if err := bot.Send(ctx, chat, text); err != nil {
				errs = append(errs, err)
			}
		}
	}
	if cfg.Alerts {
		if err := s.sendAlerts(ctx, bot, chat, today); err != nil {
			errs = append(errs, err)
		}
	}
	return errors.Join(errs...)
}

// --- Texte -----------------------------------------------------------------

// euro: 123456 → „1.234,56 €“
func euro(cents int64) string {
	neg := cents < 0
	if neg {
		cents = -cents
	}
	whole := strconv.FormatInt(cents/100, 10)
	for i := len(whole) - 3; i > 0; i -= 3 {
		whole = whole[:i] + "." + whole[i:]
	}
	s := fmt.Sprintf("%s,%02d €", whole, cents%100)
	if neg {
		s = "−" + s
	}
	return s
}

var weekdays = []string{"Sonntag", "Montag", "Dienstag", "Mittwoch", "Donnerstag", "Freitag", "Samstag"}

func germanDate(t time.Time) string { return t.Format("02.01.2006") }

func paymentLabel(method string) string {
	return map[string]string{"sepa": "SEPA-Lastschrift", "standing_order": "Dauerauftrag", "transfer": "Überweisung",
		"card": "Kreditkarte", "paypal": "PayPal", "cash": "Bar", "other": "Sonstige"}[method]
}

// dueOn: Abos/Verträge (Ausgaben), die am Tag day abgebucht werden.
func dueOn(recs []db.Recurring, day time.Time) []db.Recurring {
	var out []db.Recurring
	for _, x := range recs {
		if x.Kind == "expense" && len(db.DueDays(x, day.AddDate(0, 0, -1), day)) > 0 {
			out = append(out, x)
		}
	}
	return out
}

func (s *Server) contractLine(x db.Recurring, pockets map[uint64]string) string {
	parts := []string{x.Title + " " + euro(x.AmountCents)}
	if x.PocketID != nil && pockets[*x.PocketID] != "" {
		parts = append(parts, "Pocket "+pockets[*x.PocketID])
	}
	if l := paymentLabel(x.PaymentMethod); l != "" {
		parts = append(parts, l)
	}
	return "• " + strings.Join(parts, " · ")
}

func (s *Server) pocketNames(ctx context.Context) (map[uint64]string, error) {
	list, err := s.repo.Pockets(ctx)
	out := map[uint64]string{}
	for _, p := range list {
		out[p.ID] = p.Name
	}
	return out, err
}

// digestText: Termine, Aufgaben und Abbuchungen des Tages, dazu der Rest im Budget.
func (s *Server) digestText(ctx context.Context, today time.Time) (string, error) {
	local := time.Date(today.Year(), today.Month(), today.Day(), 0, 0, 0, 0, s.loc)
	_, kw := local.ISOWeek()
	var b strings.Builder
	fmt.Fprintf(&b, "Guten Morgen! %s, %s (KW %d)\n", weekdays[local.Weekday()], germanDate(local), kw)

	events, remoteErr, err := s.calendar.Occurrences(ctx, local, local.AddDate(0, 0, 1))
	if err != nil {
		return "", err
	}
	b.WriteString("\nTermine:\n")
	if len(events) == 0 {
		b.WriteString("• keine\n")
	}
	for _, e := range events {
		when := "ganztägig"
		if !e.AllDay {
			when = e.Start.Format("15:04")
			if !e.End.Equal(e.Start) && e.End.Format(db.DateLayout) == e.Start.Format(db.DateLayout) {
				when += "–" + e.End.Format("15:04")
			}
		}
		line := "• " + when + " " + e.Title
		if e.Location != "" {
			line += " (" + e.Location + ")"
		}
		b.WriteString(line + "\n")
	}
	if remoteErr != nil {
		b.WriteString("(Kalenderserver nicht erreichbar)\n")
	}

	tasks, err := s.repo.Tasks(ctx, monday(local).Format(db.DateLayout))
	if err != nil {
		return "", err
	}
	todayKey := today.Format(db.DateLayout)
	var dayTasks []string
	weekOpen := 0
	for _, t := range tasks {
		if t.Done {
			continue
		}
		if t.Day != nil && *t.Day == todayKey {
			dayTasks = append(dayTasks, "• "+t.Title)
		} else if t.Day == nil {
			weekOpen++
		}
	}
	if len(dayTasks) > 0 || weekOpen > 0 {
		b.WriteString("\nAufgaben:\n")
		for _, t := range dayTasks {
			b.WriteString(t + "\n")
		}
		if weekOpen > 0 {
			fmt.Fprintf(&b, "• dazu %d offen für die Woche\n", weekOpen)
		}
	}

	recs, err := s.repo.RecurringList(ctx)
	if err != nil {
		return "", err
	}
	pockets, err := s.pocketNames(ctx)
	if err != nil {
		return "", err
	}
	if due := dueOn(recs, today); len(due) > 0 {
		b.WriteString("\nHeute abgebucht:\n")
		for _, x := range due {
			b.WriteString(s.contractLine(x, pockets) + "\n")
		}
	}

	v, err := s.computeBudget(ctx, time.Date(today.Year(), today.Month(), 1, 0, 0, 0, 0, time.UTC))
	if err != nil {
		return "", err
	}
	if v.Plan.FreeCents > 0 {
		fmt.Fprintf(&b, "\nBudget %s: %s übrig von %s frei\n", monthName(today), euro(v.RemainingCents), euro(v.Plan.FreeCents))
	}
	return strings.TrimRight(b.String(), "\n"), nil
}

func monthName(t time.Time) string {
	return []string{"Januar", "Februar", "März", "April", "Mai", "Juni", "Juli", "August", "September", "Oktober", "November", "Dezember"}[t.Month()-1]
}

// paymentsText: Abbuchungen in days Tagen; "" = keine.
func (s *Server) paymentsText(ctx context.Context, today time.Time, days int) (string, error) {
	recs, err := s.repo.RecurringList(ctx)
	if err != nil {
		return "", err
	}
	day := today.AddDate(0, 0, days)
	due := dueOn(recs, day)
	if len(due) == 0 {
		return "", nil
	}
	pockets, err := s.pocketNames(ctx)
	if err != nil {
		return "", err
	}
	when := fmt.Sprintf("In %d Tagen", days)
	if days == 1 {
		when = "Morgen"
	}
	var b strings.Builder
	var sum int64
	fmt.Fprintf(&b, "%s (%s, %s) wird abgebucht:\n", when, weekdays[day.Weekday()], germanDate(day))
	for _, x := range due {
		b.WriteString(s.contractLine(x, pockets) + "\n")
		sum += x.AmountCents
	}
	if len(due) > 1 {
		fmt.Fprintf(&b, "Zusammen %s", euro(sum))
	}
	return strings.TrimRight(b.String(), "\n"), nil
}

type alert struct {
	key, text string
}

// alerts: alle Warnungen, die gerade gelten (ob schon gesendet, prüft sendAlerts).
func (s *Server) alerts(ctx context.Context, today time.Time) ([]alert, error) {
	month := time.Date(today.Year(), today.Month(), 1, 0, 0, 0, 0, time.UTC)
	v, err := s.computeBudget(ctx, month)
	if err != nil {
		return nil, err
	}
	var out []alert
	for _, p := range v.Pockets {
		if p.ID == 0 {
			continue
		}
		if f := p.Forecast; f != nil && f.ShortOn != nil {
			out = append(out, alert{fmt.Sprintf("short:%d:%s", p.ID, *f.ShortOn), fmt.Sprintf("Pocket %s: Am %s fehlen %s (%s).",
				p.Name, germanDate(mustDay(*f.ShortOn)), euro(f.ShortCents), f.ShortTitle)})
		}
		if p.ShortCents > 0 {
			out = append(out, alert{fmt.Sprintf("pmonth:%d:%s", p.ID, v.Month), fmt.Sprintf("Pocket %s: Abos & Verträge brauchen %s im Monat, es gehen %s drauf – %s zu wenig.",
				p.Name, euro(p.ContractsCents), euro(p.MonthlyCents), euro(p.ShortCents))})
		}
	}
	if v.Plan.FreeCents > 0 && v.RemainingCents < 0 {
		out = append(out, alert{"over:" + v.Month, fmt.Sprintf("Budget %s überschritten: %s mehr ausgegeben als frei war (%s).",
			monthName(today), euro(-v.RemainingCents), euro(v.Plan.FreeCents))})
	}
	for _, c := range v.Categories {
		if c.BudgetCents > 0 && c.SpentCents > c.BudgetCents {
			out = append(out, alert{fmt.Sprintf("cat:%d:%s", c.ID, v.Month), fmt.Sprintf("Kategorie %s: %s von %s ausgegeben (%s drüber).",
				c.Name, euro(c.SpentCents), euro(c.BudgetCents), euro(c.SpentCents-c.BudgetCents))})
		}
	}
	return out, nil
}

func mustDay(s string) time.Time { t, _ := time.Parse(db.DateLayout, s); return t }

// sendAlerts schickt neue Warnungen gesammelt in einer Nachricht. Schlägt das
// Senden fehl, gelten sie als nicht gesendet und kommen beim nächsten Mal.
func (s *Server) sendAlerts(ctx context.Context, bot *telegram.Bot, chat int64, today time.Time) error {
	list, err := s.alerts(ctx, today)
	if err != nil {
		return err
	}
	var fresh []alert
	for _, a := range list {
		ok, err := s.repo.MarkSent(ctx, a.key)
		if err != nil {
			return err
		}
		if ok {
			fresh = append(fresh, a)
		}
	}
	if len(fresh) == 0 {
		return nil
	}
	var b strings.Builder
	b.WriteString("⚠ nexora/plan\n")
	for _, a := range fresh {
		b.WriteString("• " + a.text + "\n")
	}
	if err := bot.Send(ctx, chat, strings.TrimRight(b.String(), "\n")); err != nil {
		for _, a := range fresh {
			s.repo.ForgetSent(ctx, a.key)
		}
		return err
	}
	return nil
}

// --- API -------------------------------------------------------------------

// GET /api/notifications – Stand der Verbindung und Einstellungen (ohne Token).
func (s *Server) handleNotifyGet(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	cfg, err := s.notifySettings(ctx)
	if err != nil {
		internalError(w, r, err)
		return
	}
	token, _ := s.repo.Setting(ctx, db.SettingTelegramToken)
	bot, _ := s.repo.Setting(ctx, db.SettingTelegramBot)
	chat, _ := s.repo.Setting(ctx, db.SettingTelegramChat)
	chatName, _ := s.repo.Setting(ctx, db.SettingTelegramChatName)
	writeJSON(w, http.StatusOK, map[string]any{
		"has_token": token != "", "bot": bot, "linked": chat != "", "chat_name": chatName,
		"settings": cfg, "can_store": s.box != nil,
	})
}

// PUT /api/notifications/telegram {"token"} – prüft das Token bei Telegram und
// speichert es verschlüsselt. Danach dem Bot „/start“ schicken und verknüpfen.
func (s *Server) handleTelegramToken(w http.ResponseWriter, r *http.Request) {
	if s.box == nil {
		writeError(w, http.StatusConflict, "PLAN_ENCRYPTION_KEY fehlt – ohne Schlüssel speichert die App kein Token")
		return
	}
	var req struct {
		Token string `json:"token"`
	}
	if !decodeJSON(w, r, &req) {
		return
	}
	bot, err := telegram.New(strings.TrimSpace(req.Token))
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	name, err := bot.Me(r.Context())
	if err != nil {
		writeError(w, http.StatusBadGateway, err.Error())
		return
	}
	if err := s.repo.SetSettings(r.Context(), map[string]string{
		db.SettingTelegramToken: s.box.Seal(strings.TrimSpace(req.Token), db.SettingTelegramToken),
		db.SettingTelegramBot:   name, db.SettingTelegramChat: "", db.SettingTelegramChatName: "",
	}); err != nil {
		internalError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"bot": name})
}

// POST /api/notifications/telegram/link – nimmt den Chat, der dem Bot zuletzt
// geschrieben hat, und schickt dorthin eine Bestätigung.
func (s *Server) handleTelegramLink(w http.ResponseWriter, r *http.Request) {
	var req struct{}
	if !decodeJSON(w, r, &req) { // nur echte JSON-Anfragen (wie alle Änderungen, gegen CSRF)
		return
	}
	bot, _, err := s.telegramBot(r.Context())
	if err != nil {
		writeError(w, http.StatusConflict, err.Error())
		return
	}
	if bot == nil {
		writeError(w, http.StatusConflict, "Zuerst das Bot-Token eintragen")
		return
	}
	chat, err := bot.LastChat(r.Context())
	if err != nil {
		writeError(w, http.StatusBadGateway, err.Error())
		return
	}
	if chat == nil {
		name, _ := s.repo.Setting(r.Context(), db.SettingTelegramBot)
		writeError(w, http.StatusConflict, fmt.Sprintf("Noch keine Nachricht gefunden – öffne @%s in Telegram, tippe „Starten“ (/start) und versuch es dann noch einmal", name))
		return
	}
	if err := bot.Send(r.Context(), chat.ID, "nexora/plan ist verbunden. Hier kommen ab jetzt deine Benachrichtigungen an."); err != nil {
		writeError(w, http.StatusBadGateway, err.Error())
		return
	}
	if err := s.repo.SetSettings(r.Context(), map[string]string{
		db.SettingTelegramChat: strconv.FormatInt(chat.ID, 10), db.SettingTelegramChatName: chat.Name,
	}); err != nil {
		internalError(w, r, err)
		return
	}
	slog.Info("Telegram verbunden")
	writeJSON(w, http.StatusOK, map[string]string{"chat_name": chat.Name})
}

// DELETE /api/notifications/telegram – Verbindung trennen.
func (s *Server) handleTelegramDelete(w http.ResponseWriter, r *http.Request) {
	if err := s.repo.SetSettings(r.Context(), map[string]string{
		db.SettingTelegramToken: "", db.SettingTelegramBot: "", db.SettingTelegramChat: "", db.SettingTelegramChatName: "",
	}); err != nil {
		internalError(w, r, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// PUT /api/notifications {"payments", "payment_days", "alerts", "digest", "time"}
func (s *Server) handleNotifyPut(w http.ResponseWriter, r *http.Request) {
	var req notifySettings
	if !decodeJSON(w, r, &req) {
		return
	}
	switch {
	case req.PaymentDays != 1 && req.PaymentDays != 3 && req.PaymentDays != 7:
		writeError(w, http.StatusBadRequest, "Abbuchungen: 1, 3 oder 7 Tage vorher")
		return
	case !timeRe.MatchString(req.Time):
		writeError(w, http.StatusBadRequest, "Uhrzeit bitte als HH:MM")
		return
	}
	flag := func(b bool) string {
		if b {
			return "1"
		}
		return ""
	}
	if err := s.repo.SetSettings(r.Context(), map[string]string{
		db.SettingNotifyPayments: flag(req.Payments), db.SettingNotifyPayDays: strconv.Itoa(req.PaymentDays),
		db.SettingNotifyAlerts: flag(req.Alerts), db.SettingNotifyDigest: flag(req.Digest), db.SettingNotifyTime: req.Time,
	}); err != nil {
		internalError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, req)
}

// POST /api/notifications/send {"kind": "digest" | "payments" | "alerts"} –
// jetzt senden, zum Ausprobieren (Warnungen dabei ohne Merken, damit sie später
// trotzdem regulär kommen).
func (s *Server) handleNotifySend(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Kind string `json:"kind"`
	}
	if !decodeJSON(w, r, &req) {
		return
	}
	ctx := r.Context()
	bot, chat, err := s.telegramBot(ctx)
	if err == nil && (bot == nil || chat == 0) {
		err = errors.New("Telegram ist noch nicht verbunden")
	}
	if err != nil {
		writeError(w, http.StatusConflict, err.Error())
		return
	}
	cfg, err := s.notifySettings(ctx)
	if err != nil {
		internalError(w, r, err)
		return
	}
	var text string
	switch req.Kind {
	case "digest":
		text, err = s.digestText(ctx, s.today())
	case "payments":
		text, err = s.paymentsText(ctx, s.today(), cfg.PaymentDays)
		if err == nil && text == "" {
			text = fmt.Sprintf("In %d Tag(en) wird nichts abgebucht.", cfg.PaymentDays)
		}
	case "alerts":
		var list []alert
		list, err = s.alerts(ctx, s.today())
		lines := []string{"⚠ nexora/plan (Test)"}
		for _, a := range list {
			lines = append(lines, "• "+a.text)
		}
		if len(list) == 0 {
			lines = append(lines, "Gerade gibt es keine Warnungen.")
		}
		text = strings.Join(lines, "\n")
	default:
		writeError(w, http.StatusBadRequest, "Unbekannte Nachricht")
		return
	}
	if err != nil {
		internalError(w, r, err)
		return
	}
	if err := bot.Send(ctx, chat, text); err != nil {
		writeError(w, http.StatusBadGateway, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"text": text})
}
