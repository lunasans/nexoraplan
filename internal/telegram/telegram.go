// Package telegram schickt Nachrichten über einen eigenen Telegram-Bot
// (Bot API: getMe, getUpdates, sendMessage).
package telegram

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"regexp"
	"strconv"
	"time"
)

// API ist die Adresse der Bot API (in Tests ein httptest-Server).
var API = "https://api.telegram.org"

var tokenRe = regexp.MustCompile(`^\d{5,15}:[A-Za-z0-9_-]{30,64}$`)

var ErrToken = errors.New("Das sieht nicht wie ein Bot-Token aus (Format 123456789:ABC…, von @BotFather)")

type Bot struct {
	token string
	http  *http.Client
}

func New(token string) (*Bot, error) {
	if !tokenRe.MatchString(token) {
		return nil, ErrToken
	}
	return &Bot{token: token, http: &http.Client{Timeout: 15 * time.Second}}, nil
}

type response struct {
	OK          bool            `json:"ok"`
	Result      json.RawMessage `json:"result"`
	Description string          `json:"description"`
}

func (b *Bot) call(ctx context.Context, method string, params any, out any) error {
	body, _ := json.Marshal(params)
	req, err := http.NewRequestWithContext(ctx, "POST", API+"/bot"+b.token+"/"+method, bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	res, err := b.http.Do(req)
	if err != nil {
		// Die URL enthält das Token – nie in Fehlermeldungen weitergeben
		return errors.New("Telegram nicht erreichbar – erlaubt die Firewall ausgehende Verbindungen zu api.telegram.org?")
	}
	defer res.Body.Close()
	var r response
	if err := json.NewDecoder(res.Body).Decode(&r); err != nil {
		return fmt.Errorf("Antwort von Telegram unlesbar (%s)", res.Status)
	}
	if !r.OK {
		if res.StatusCode == http.StatusUnauthorized || res.StatusCode == http.StatusNotFound {
			return errors.New("Telegram kennt dieses Bot-Token nicht")
		}
		return fmt.Errorf("Telegram: %s", r.Description)
	}
	if out != nil {
		return json.Unmarshal(r.Result, out)
	}
	return nil
}

// Me liefert den Benutzernamen des Bots (prüft das Token).
func (b *Bot) Me(ctx context.Context) (string, error) {
	var me struct {
		Username string `json:"username"`
	}
	err := b.call(ctx, "getMe", map[string]any{}, &me)
	return me.Username, err
}

// Chat ist der Gesprächspartner, der dem Bot zuletzt geschrieben hat.
type Chat struct {
	ID   int64  `json:"id"`
	Name string `json:"name"`
}

// LastChat sucht die neueste private Nachricht an den Bot (z. B. „/start“).
func (b *Bot) LastChat(ctx context.Context) (*Chat, error) {
	var updates []struct {
		Message *struct {
			Chat struct {
				ID        int64  `json:"id"`
				Type      string `json:"type"`
				FirstName string `json:"first_name"`
				LastName  string `json:"last_name"`
				Username  string `json:"username"`
			} `json:"chat"`
		} `json:"message"`
	}
	if err := b.call(ctx, "getUpdates", map[string]any{"allowed_updates": []string{"message"}}, &updates); err != nil {
		return nil, err
	}
	for i := len(updates) - 1; i >= 0; i-- {
		m := updates[i].Message
		if m == nil || m.Chat.Type != "private" {
			continue
		}
		name := m.Chat.FirstName
		if m.Chat.LastName != "" {
			name += " " + m.Chat.LastName
		}
		if m.Chat.Username != "" {
			name += " (@" + m.Chat.Username + ")"
		}
		return &Chat{ID: m.Chat.ID, Name: name}, nil
	}
	return nil, nil
}

// Send schickt eine Textnachricht (ohne Formatierung – nichts muss maskiert werden).
func (b *Bot) Send(ctx context.Context, chatID int64, text string) error {
	return b.call(ctx, "sendMessage", map[string]any{
		"chat_id": strconv.FormatInt(chatID, 10), "text": text, "disable_web_page_preview": true,
	}, nil)
}
