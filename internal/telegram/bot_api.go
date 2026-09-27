package telegram

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync"
	"time"

	telegrambot "github.com/go-telegram/bot"
	"github.com/go-telegram/bot/models"
)

const (
	telegramUpdatesChannelCapacity = 64
	telegramUpdateWorkers          = 1

	// MaxFileTransferBytes is the transport boundary for future Telegram file
	// operations. go-telegram/bot materializes multipart request bodies in memory,
	// so callers must reject oversized files before constructing an upload.
	MaxFileTransferBytes int64 = 20 << 20
)

var telegramAllowedUpdates = telegrambot.AllowedUpdates{
	models.AllowedUpdateMessage,
	models.AllowedUpdateCallbackQuery,
}

type transportErrorClass string

const (
	transportErrorRateLimited  transportErrorClass = "rate_limited"
	transportErrorForbidden    transportErrorClass = "forbidden"
	transportErrorBadRequest   transportErrorClass = "bad_request"
	transportErrorUnauthorized transportErrorClass = "unauthorized"
	transportErrorConflict     transportErrorClass = "conflict"
	transportErrorNotFound     transportErrorClass = "not_found"
	transportErrorTransport    transportErrorClass = "transport"
)

type transportError struct {
	Class      transportErrorClass
	RetryAfter time.Duration
}

func (err *transportError) Error() string {
	if err == nil {
		return "telegram transport failed"
	}
	if err.RetryAfter > 0 {
		return fmt.Sprintf("telegram transport %s; retry after %s", err.Class, err.RetryAfter)
	}
	return "telegram transport " + string(err.Class)
}

func classifyTransportError(err error) error {
	if err == nil {
		return nil
	}
	var rateLimit *telegrambot.TooManyRequestsError
	if errors.As(err, &rateLimit) {
		retryAfter := time.Duration(rateLimit.RetryAfter) * time.Second
		return &transportError{Class: transportErrorRateLimited, RetryAfter: retryAfter}
	}
	class := transportErrorTransport
	switch {
	case errors.Is(err, telegrambot.ErrorForbidden):
		class = transportErrorForbidden
	case errors.Is(err, telegrambot.ErrorBadRequest):
		class = transportErrorBadRequest
	case errors.Is(err, telegrambot.ErrorUnauthorized):
		class = transportErrorUnauthorized
	case errors.Is(err, telegrambot.ErrorConflict):
		class = transportErrorConflict
	case errors.Is(err, telegrambot.ErrorNotFound):
		class = transportErrorNotFound
	}
	return &transportError{Class: class}
}

func transportErrorKind(err error) transportErrorClass {
	var typed *transportError
	if errors.As(err, &typed) && typed != nil {
		return typed.Class
	}
	return transportErrorTransport
}

type apiClient struct {
	token       string
	pollTimeout time.Duration
	serverURL   string
	httpClient  *http.Client
	bot         *telegrambot.Bot
	initErr     error
}

func newAPIClientWithPollTimeout(token string, pollTimeout time.Duration) API {
	return newAPIClientWithOptions(token, pollTimeout, "", nil)
}

func newAPIClientWithOptions(token string, pollTimeout time.Duration, serverURL string, httpClient *http.Client) *apiClient {
	token = strings.TrimSpace(token)
	if pollTimeout <= 0 {
		pollTimeout = defaultPollTimeout
	}
	if httpClient == nil {
		timeout := pollTimeout + 5*time.Second
		if timeout < 10*time.Second {
			timeout = 10 * time.Second
		}
		httpClient = &http.Client{Timeout: timeout}
	}
	client := &apiClient{token: token, pollTimeout: pollTimeout, serverURL: strings.TrimRight(strings.TrimSpace(serverURL), "/"), httpClient: httpClient}
	client.bot, client.initErr = client.newBot(nil, nil, nil, nil)
	return client
}

func (client *apiClient) newBot(initialOffset *int64, handler telegrambot.HandlerFunc, errorsHandler telegrambot.ErrorsHandler, onPoll func(pollEvent)) (*telegrambot.Bot, error) {
	if client == nil || strings.TrimSpace(client.token) == "" {
		return nil, errors.New("telegram bot token is unavailable")
	}
	httpClient := telegrambot.HttpClient(client.httpClient)
	if onPoll != nil {
		httpClient = &pollObservingHTTPClient{client: client.httpClient, onSuccess: func(status int) {
			onPoll(pollEvent{Success: true, HTTPStatus: status})
		}}
	}
	options := []telegrambot.Option{
		telegrambot.WithSkipGetMe(),
		telegrambot.WithHTTPClient(client.pollTimeout, httpClient),
	}
	if client.serverURL != "" {
		options = append(options, telegrambot.WithServerURL(client.serverURL))
	}
	if handler != nil {
		options = append(options,
			telegrambot.WithDefaultHandler(handler),
			telegrambot.WithAllowedUpdates(append(telegrambot.AllowedUpdates(nil), telegramAllowedUpdates...)),
			telegrambot.WithUpdatesChannelCap(telegramUpdatesChannelCapacity),
			telegrambot.WithWorkers(telegramUpdateWorkers),
			telegrambot.WithNotAsyncHandlers(),
		)
	}
	if errorsHandler != nil {
		options = append(options, telegrambot.WithErrorsHandler(errorsHandler))
	}
	if initialOffset != nil {
		options = append(options, telegrambot.WithInitialOffset(*initialOffset-1))
	}
	return telegrambot.New(client.token, options...)
}

func (client *apiClient) GetMe(ctx context.Context) (User, error) {
	if client == nil || client.initErr != nil || client.bot == nil {
		return User{}, errors.New("telegram bot transport is unavailable")
	}
	user, err := client.bot.GetMe(nonNilContext(ctx))
	if err != nil {
		return User{}, classifyTransportError(err)
	}
	if user == nil || user.ID <= 0 {
		return User{}, errors.New("telegram bot token validation failed")
	}
	return userFromModel(*user), nil
}

func (client *apiClient) SendMessage(ctx context.Context, chatID int64, text string) error {
	if client == nil || client.initErr != nil || client.bot == nil {
		return errors.New("telegram bot transport is unavailable")
	}
	_, err := client.bot.SendMessage(nonNilContext(ctx), &telegrambot.SendMessageParams{ChatID: chatID, Text: text})
	return classifyTransportError(err)
}

func (client *apiClient) SendScreen(ctx context.Context, chatID int64, screen Screen) error {
	if client == nil || client.initErr != nil || client.bot == nil {
		return errors.New("telegram bot transport is unavailable")
	}
	_, err := client.bot.SendMessage(nonNilContext(ctx), &telegrambot.SendMessageParams{
		ChatID: chatID, Text: screenText(screen), ParseMode: models.ParseModeHTML, ReplyMarkup: screenKeyboard(screen.Keyboard),
	})
	return classifyTransportError(err)
}

func (client *apiClient) EditScreen(ctx context.Context, chatID, messageID int64, screen Screen) error {
	if client == nil || client.initErr != nil || client.bot == nil {
		return errors.New("telegram bot transport is unavailable")
	}
	if messageID <= 0 || messageID > int64(^uint(0)>>1) {
		return errors.New("telegram message id is invalid")
	}
	_, err := client.bot.EditMessageText(nonNilContext(ctx), &telegrambot.EditMessageTextParams{
		ChatID: chatID, MessageID: int(messageID), Text: screenText(screen), ParseMode: models.ParseModeHTML, ReplyMarkup: screenKeyboard(screen.Keyboard),
	})
	return classifyTransportError(err)
}

func (client *apiClient) AnswerCallback(ctx context.Context, callbackID, text string, alert bool) error {
	if client == nil || client.initErr != nil || client.bot == nil {
		return errors.New("telegram bot transport is unavailable")
	}
	_, err := client.bot.AnswerCallbackQuery(nonNilContext(ctx), &telegrambot.AnswerCallbackQueryParams{
		CallbackQueryID: strings.TrimSpace(callbackID), Text: strings.TrimSpace(text), ShowAlert: alert,
	})
	return classifyTransportError(err)
}

func (client *apiClient) StartUpdates(ctx context.Context, initialOffset int64, handler Handler, onPoll func(pollEvent)) error {
	if client == nil || client.initErr != nil {
		return errors.New("telegram bot transport is unavailable")
	}
	botHandler := func(handlerCtx context.Context, _ *telegrambot.Bot, update *models.Update) {
		if handler == nil || update == nil {
			return
		}
		handler(handlerCtx, updateFromModel(update))
	}
	errorHandler := func(err error) {
		if onPoll == nil {
			return
		}
		onPoll(pollEvent{Err: classifyTransportError(err)})
	}
	bot, err := client.newBot(&initialOffset, botHandler, errorHandler, onPoll)
	if err != nil {
		return classifyTransportError(err)
	}
	ctx = nonNilContext(ctx)
	bot.Start(ctx)
	if ctx.Err() != nil {
		return ctx.Err()
	}
	return errors.New("telegram long polling stopped")
}

func (client *apiClient) ProcessUpdate(ctx context.Context, update Update, handler Handler) {
	if client == nil || client.initErr != nil || handler == nil {
		return
	}
	botHandler := func(handlerCtx context.Context, _ *telegrambot.Bot, modelUpdate *models.Update) {
		if modelUpdate != nil {
			handler(handlerCtx, updateFromModel(modelUpdate))
		}
	}
	bot, err := client.newBot(nil, botHandler, nil, nil)
	if err != nil {
		return
	}
	bot.ProcessUpdate(nonNilContext(ctx), updateToModel(update))
}

func validateTelegramFileSize(size int64) error {
	if size < 0 || size > MaxFileTransferBytes {
		return fmt.Errorf("telegram file size exceeds %d bytes", MaxFileTransferBytes)
	}
	return nil
}

func screenKeyboard(rows [][]Button) models.InlineKeyboardMarkup {
	keyboard := make([][]models.InlineKeyboardButton, 0, len(rows))
	for _, row := range rows {
		buttons := make([]models.InlineKeyboardButton, 0, len(row))
		for _, button := range row {
			if button.Disabled || strings.TrimSpace(button.Text) == "" || button.CallbackData == "" && button.URL == "" {
				continue
			}
			buttons = append(buttons, models.InlineKeyboardButton{Text: button.Text, CallbackData: button.CallbackData, URL: button.URL})
		}
		if len(buttons) > 0 {
			keyboard = append(keyboard, buttons)
		}
	}
	return models.InlineKeyboardMarkup{InlineKeyboard: keyboard}
}

func updateFromModel(update *models.Update) Update {
	if update == nil {
		return Update{}
	}
	result := Update{UpdateID: update.ID}
	if update.Message != nil {
		result.Message = messageFromModel(update.Message)
	}
	if callback := update.CallbackQuery; callback != nil {
		value := &CallbackQuery{ID: callback.ID, From: userFromModel(callback.From), Data: callback.Data}
		if callback.Message.Message != nil {
			value.Message = messageFromModel(callback.Message.Message)
		} else if inaccessible := callback.Message.InaccessibleMessage; inaccessible != nil {
			value.Message = &Message{MessageID: int64(inaccessible.MessageID), Chat: chatFromModel(inaccessible.Chat)}
		}
		result.CallbackQuery = value
	}
	return result
}

func updateToModel(update Update) *models.Update {
	result := &models.Update{ID: update.UpdateID}
	if update.Message != nil {
		result.Message = messageToModel(update.Message)
	}
	if callback := update.CallbackQuery; callback != nil {
		value := &models.CallbackQuery{ID: callback.ID, From: userToModel(callback.From), Data: callback.Data}
		if callback.Message != nil {
			value.Message = models.MaybeInaccessibleMessage{
				Type: models.MaybeInaccessibleMessageTypeMessage, Message: messageToModel(callback.Message),
			}
		}
		result.CallbackQuery = value
	}
	return result
}

func messageFromModel(message *models.Message) *Message {
	if message == nil {
		return nil
	}
	result := &Message{MessageID: int64(message.ID), Chat: chatFromModel(message.Chat), Text: message.Text}
	if message.From != nil {
		user := userFromModel(*message.From)
		result.From = &user
	}
	return result
}

func messageToModel(message *Message) *models.Message {
	if message == nil {
		return nil
	}
	result := &models.Message{ID: int(message.MessageID), Chat: chatToModel(message.Chat), Text: message.Text}
	if message.From != nil {
		user := userToModel(*message.From)
		result.From = &user
	}
	return result
}

func userFromModel(user models.User) User {
	return User{ID: user.ID, Username: user.Username, FirstName: user.FirstName, LastName: user.LastName}
}

func userToModel(user User) models.User {
	return models.User{ID: user.ID, Username: user.Username, FirstName: user.FirstName, LastName: user.LastName}
}

func chatFromModel(chat models.Chat) Chat { return Chat{ID: chat.ID, Type: string(chat.Type)} }

func chatToModel(chat Chat) models.Chat {
	return models.Chat{ID: chat.ID, Type: models.ChatType(chat.Type)}
}

func nonNilContext(ctx context.Context) context.Context {
	if ctx == nil {
		return context.Background()
	}
	return ctx
}

type pollObservingHTTPClient struct {
	client    *http.Client
	onSuccess func(int)
}

func (client *pollObservingHTTPClient) Do(request *http.Request) (*http.Response, error) {
	response, err := client.client.Do(request)
	if err != nil || response == nil || !isTelegramGetUpdates(request) || response.StatusCode < http.StatusOK || response.StatusCode >= http.StatusMultipleChoices {
		return response, err
	}
	response.Body = &observedPollBody{ReadCloser: response.Body, complete: func() {
		if client.onSuccess != nil {
			client.onSuccess(response.StatusCode)
		}
	}}
	return response, nil
}

func isTelegramGetUpdates(request *http.Request) bool {
	return request != nil && request.URL != nil && strings.HasSuffix(strings.ToLower(request.URL.Path), "/getupdates")
}

type observedPollBody struct {
	io.ReadCloser
	once     sync.Once
	complete func()
}

func (body *observedPollBody) Read(buffer []byte) (int, error) {
	n, err := body.ReadCloser.Read(buffer)
	if err == io.EOF && body.complete != nil {
		body.once.Do(body.complete)
	}
	return n, err
}
