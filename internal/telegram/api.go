package telegram

import (
	"context"
	"time"
)

type User struct {
	ID        int64  `json:"id"`
	Username  string `json:"username,omitempty"`
	FirstName string `json:"first_name,omitempty"`
	LastName  string `json:"last_name,omitempty"`
}

type Chat struct {
	ID   int64  `json:"id"`
	Type string `json:"type"`
}

type Message struct {
	MessageID int64  `json:"message_id"`
	From      *User  `json:"from,omitempty"`
	Chat      Chat   `json:"chat"`
	Text      string `json:"text,omitempty"`
}

type CallbackQuery struct {
	ID      string   `json:"id"`
	From    User     `json:"from"`
	Message *Message `json:"message,omitempty"`
	Data    string   `json:"data,omitempty"`
}

type Update struct {
	UpdateID      int64          `json:"update_id"`
	Message       *Message       `json:"message,omitempty"`
	CallbackQuery *CallbackQuery `json:"callback_query,omitempty"`
}

type API interface {
	GetMe(context.Context) (User, error)
	SendMessage(context.Context, int64, string) error
}

// pollingAPI keeps the deterministic batch-poll fixture used by runtime tests and
// custom transports. The production Telegram adapter implements streamingAPI.
type pollingAPI interface {
	GetUpdates(context.Context, int64, int, time.Duration) ([]Update, error)
}

type pollEvent struct {
	Success    bool
	HTTPStatus int
	Err        error
}

type streamingAPI interface {
	StartUpdates(context.Context, int64, Handler, func(pollEvent)) error
}

type injectedUpdateAPI interface {
	ProcessUpdate(context.Context, Update, Handler)
}

type ScreenAPI interface {
	SendScreen(context.Context, int64, Screen) error
	EditScreen(context.Context, int64, int64, Screen) error
	AnswerCallback(context.Context, string, string, bool) error
}
