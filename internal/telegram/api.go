package telegram

import (
	"context"
	"time"
)

type User struct {
	ID                        int64  `json:"id"`
	Username                  string `json:"username,omitempty"`
	FirstName                 string `json:"first_name,omitempty"`
	LastName                  string `json:"last_name,omitempty"`
	HasTopicsEnabled          bool   `json:"has_topics_enabled,omitempty"`
	AllowsUsersToCreateTopics bool   `json:"allows_users_to_create_topics,omitempty"`
}

type Chat struct {
	ID   int64  `json:"id"`
	Type string `json:"type"`
}

type Message struct {
	MessageID      int64     `json:"message_id"`
	From           *User     `json:"from,omitempty"`
	Chat           Chat      `json:"chat"`
	Text           string    `json:"text,omitempty"`
	ReplyToMessage *Message  `json:"reply_to_message,omitempty"`
	Document       *Document `json:"document,omitempty"`
}

type Document struct {
	FileID   string `json:"file_id"`
	FileName string `json:"file_name,omitempty"`
	MimeType string `json:"mime_type,omitempty"`
	FileSize int64  `json:"file_size,omitempty"`
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

type NavigationAPI interface {
	SetCommands(context.Context, []Command) error
	SetChatMenuButton(context.Context, int64, MenuButton) error
	GetChatMenuButton(context.Context, int64) (MenuButton, error)
}

type MessageDismissAPI interface {
	DeleteMessage(context.Context, int64, int64) error
}

type RichMessageOptions struct {
	ForceReplyPlaceholder string
	ProtectContent        bool
}

type RichMessageAPI interface {
	SendRichMessage(context.Context, int64, Screen, RichMessageOptions) (int64, error)
	SendChatAction(context.Context, int64, string) error
}

type DocumentUpload struct {
	FileName       string
	ContentType    string
	Data           []byte
	Caption        string
	ProtectContent bool
}

type DocumentAPI interface {
	SendDocument(context.Context, int64, DocumentUpload) error
	DownloadDocument(context.Context, Document) ([]byte, error)
}

type TopicAPI interface {
	CreateTopic(context.Context, int64, string) (int, error)
	SendMessageThread(context.Context, int64, int, string) error
	SendRichMessageThread(context.Context, int64, int, Screen, RichMessageOptions) (int64, error)
	SendChatActionThread(context.Context, int64, int, string) error
	SendDocumentThread(context.Context, int64, int, DocumentUpload) error
}

type PendingInputValue struct {
	Text     string
	Document *Document
}
