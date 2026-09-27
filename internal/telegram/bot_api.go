package telegram

import (
	"bytes"
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

func (client *apiClient) SendMessageThread(ctx context.Context, chatID int64, threadID int, text string) error {
	if client == nil || client.initErr != nil || client.bot == nil {
		return errors.New("telegram bot transport is unavailable")
	}
	_, err := client.bot.SendMessage(nonNilContext(ctx), &telegrambot.SendMessageParams{ChatID: chatID, MessageThreadID: threadID, Text: text})
	return classifyTransportError(err)
}

func (client *apiClient) CreateTopic(ctx context.Context, chatID int64, name string) (int, error) {
	if client == nil || client.initErr != nil || client.bot == nil {
		return 0, errors.New("telegram bot transport is unavailable")
	}
	name = strings.TrimSpace(name)
	if chatID <= 0 || name == "" {
		return 0, errors.New("telegram topic metadata is invalid")
	}
	topic, err := client.bot.CreateForumTopic(nonNilContext(ctx), &telegrambot.CreateForumTopicParams{ChatID: chatID, Name: name})
	if err != nil {
		return 0, classifyTransportError(err)
	}
	if topic == nil || topic.MessageThreadID <= 0 {
		return 0, errors.New("telegram topic response is invalid")
	}
	return topic.MessageThreadID, nil
}

func (client *apiClient) SendScreen(ctx context.Context, chatID int64, screen Screen) error {
	if client == nil || client.initErr != nil || client.bot == nil {
		return errors.New("telegram bot transport is unavailable")
	}
	if rich, ok := screenRichMessage(screen); ok {
		_, err := client.bot.SendRichMessage(nonNilContext(ctx), &telegrambot.SendRichMessageParams{
			ChatID: chatID, RichMessage: rich, ReplyMarkup: screenKeyboard(screen.Keyboard),
		})
		if err == nil {
			return nil
		}
		if !richMessageFallbackAllowed(err) {
			return classifyTransportError(err)
		}
	}
	_, err := client.bot.SendMessage(nonNilContext(ctx), &telegrambot.SendMessageParams{
		ChatID: chatID, Text: screenText(screen), ParseMode: models.ParseModeHTML, ReplyMarkup: screenKeyboard(screen.Keyboard),
	})
	return classifyTransportError(err)
}

func (client *apiClient) SendRichMessage(ctx context.Context, chatID int64, screen Screen, options RichMessageOptions) (int64, error) {
	if client == nil || client.initErr != nil || client.bot == nil {
		return 0, errors.New("telegram bot transport is unavailable")
	}
	if err := validateKeyboard(screen.Keyboard); err != nil {
		return 0, err
	}
	var replyMarkup models.ReplyMarkup
	if len(screen.Keyboard) > 0 {
		replyMarkup = screenKeyboard(screen.Keyboard)
	} else if placeholder := forceReplyPlaceholder(options.ForceReplyPlaceholder); placeholder != "" {
		replyMarkup = &models.ForceReply{ForceReply: true, InputFieldPlaceholder: placeholder, Selective: true}
	}
	var message *models.Message
	var err error
	if rich, ok := screenRichMessage(screen); ok {
		message, err = client.bot.SendRichMessage(nonNilContext(ctx), &telegrambot.SendRichMessageParams{
			ChatID: chatID, RichMessage: rich, ProtectContent: options.ProtectContent, ReplyMarkup: replyMarkup,
		})
		if err != nil && richMessageFallbackAllowed(err) {
			message, err = client.bot.SendMessage(nonNilContext(ctx), &telegrambot.SendMessageParams{
				ChatID: chatID, Text: screenText(screen), ParseMode: models.ParseModeHTML, ProtectContent: options.ProtectContent, ReplyMarkup: replyMarkup,
			})
		}
	} else {
		message, err = client.bot.SendMessage(nonNilContext(ctx), &telegrambot.SendMessageParams{
			ChatID: chatID, Text: screenText(screen), ParseMode: models.ParseModeHTML, ProtectContent: options.ProtectContent, ReplyMarkup: replyMarkup,
		})
	}
	if err != nil {
		return 0, classifyTransportError(err)
	}
	if message == nil {
		return 0, errors.New("telegram rich message response is empty")
	}
	return int64(message.ID), nil
}

func (client *apiClient) SendUserPicker(ctx context.Context, chatID int64, requestID int, prompt string) (int64, error) {
	if client == nil || client.initErr != nil || client.bot == nil {
		return 0, errors.New("telegram bot transport is unavailable")
	}
	if chatID <= 0 || requestID <= 0 {
		return 0, errors.New("telegram user-picker metadata is invalid")
	}
	message, err := client.bot.SendMessage(nonNilContext(ctx), &telegrambot.SendMessageParams{
		ChatID: chatID,
		Text:   strings.TrimSpace(prompt),
		ReplyMarkup: &models.ReplyKeyboardMarkup{
			Keyboard: [][]models.KeyboardButton{{{
				Text: "Select user",
				RequestUsers: &models.KeyboardButtonRequestUsers{
					RequestID: int32(requestID), MaxQuantity: 1, RequestName: true, RequestUsername: true,
				},
			}}},
			ResizeKeyboard: true, OneTimeKeyboard: true, Selective: true,
		},
	})
	if err != nil {
		return 0, classifyTransportError(err)
	}
	if message == nil {
		return 0, errors.New("telegram user-picker response is empty")
	}
	return int64(message.ID), nil
}

func (client *apiClient) SendRichMessageThread(ctx context.Context, chatID int64, threadID int, screen Screen, options RichMessageOptions) (int64, error) {
	if client == nil || client.initErr != nil || client.bot == nil {
		return 0, errors.New("telegram bot transport is unavailable")
	}
	if err := validateKeyboard(screen.Keyboard); err != nil {
		return 0, err
	}
	var replyMarkup models.ReplyMarkup
	if len(screen.Keyboard) > 0 {
		replyMarkup = screenKeyboard(screen.Keyboard)
	} else if placeholder := forceReplyPlaceholder(options.ForceReplyPlaceholder); placeholder != "" {
		replyMarkup = &models.ForceReply{ForceReply: true, InputFieldPlaceholder: placeholder, Selective: true}
	}
	var message *models.Message
	var err error
	if rich, ok := screenRichMessage(screen); ok {
		message, err = client.bot.SendRichMessage(nonNilContext(ctx), &telegrambot.SendRichMessageParams{
			ChatID: chatID, MessageThreadID: threadID, RichMessage: rich, ProtectContent: options.ProtectContent, ReplyMarkup: replyMarkup,
		})
		if err != nil && richMessageFallbackAllowed(err) {
			message, err = client.bot.SendMessage(nonNilContext(ctx), &telegrambot.SendMessageParams{
				ChatID: chatID, MessageThreadID: threadID, Text: screenText(screen), ParseMode: models.ParseModeHTML, ProtectContent: options.ProtectContent, ReplyMarkup: replyMarkup,
			})
		}
	} else {
		message, err = client.bot.SendMessage(nonNilContext(ctx), &telegrambot.SendMessageParams{
			ChatID: chatID, MessageThreadID: threadID, Text: screenText(screen), ParseMode: models.ParseModeHTML, ProtectContent: options.ProtectContent, ReplyMarkup: replyMarkup,
		})
	}
	if err != nil {
		return 0, classifyTransportError(err)
	}
	if message == nil {
		return 0, errors.New("telegram rich message response is empty")
	}
	return int64(message.ID), nil
}

func (client *apiClient) SendChatAction(ctx context.Context, chatID int64, action string) error {
	if client == nil || client.initErr != nil || client.bot == nil {
		return errors.New("telegram bot transport is unavailable")
	}
	ok, err := client.bot.SendChatAction(nonNilContext(ctx), &telegrambot.SendChatActionParams{ChatID: chatID, Action: models.ChatAction(strings.TrimSpace(action))})
	if err != nil {
		return classifyTransportError(err)
	}
	if !ok {
		return errors.New("telegram chat action was not accepted")
	}
	return nil
}

func (client *apiClient) SendChatActionThread(ctx context.Context, chatID int64, threadID int, action string) error {
	if client == nil || client.initErr != nil || client.bot == nil {
		return errors.New("telegram bot transport is unavailable")
	}
	ok, err := client.bot.SendChatAction(nonNilContext(ctx), &telegrambot.SendChatActionParams{ChatID: chatID, MessageThreadID: threadID, Action: models.ChatAction(strings.TrimSpace(action))})
	if err != nil {
		return classifyTransportError(err)
	}
	if !ok {
		return errors.New("telegram chat action was not accepted")
	}
	return nil
}

func (client *apiClient) SendDocument(ctx context.Context, chatID int64, upload DocumentUpload) error {
	if client == nil || client.initErr != nil || client.bot == nil {
		return errors.New("telegram bot transport is unavailable")
	}
	if err := ValidateDocumentUpload(upload); err != nil {
		return err
	}
	_, err := client.bot.SendDocument(nonNilContext(ctx), &telegrambot.SendDocumentParams{
		ChatID:         chatID,
		Document:       &models.InputFileUpload{Filename: upload.FileName, Data: bytes.NewReader(upload.Data)},
		Caption:        strings.TrimSpace(upload.Caption),
		ProtectContent: upload.ProtectContent,
	})
	return classifyTransportError(err)
}

func (client *apiClient) SendDocumentThread(ctx context.Context, chatID int64, threadID int, upload DocumentUpload) error {
	if client == nil || client.initErr != nil || client.bot == nil {
		return errors.New("telegram bot transport is unavailable")
	}
	if err := ValidateDocumentUpload(upload); err != nil {
		return err
	}
	_, err := client.bot.SendDocument(nonNilContext(ctx), &telegrambot.SendDocumentParams{ChatID: chatID, MessageThreadID: threadID, Document: &models.InputFileUpload{Filename: upload.FileName, Data: bytes.NewReader(upload.Data)}, Caption: strings.TrimSpace(upload.Caption), ProtectContent: upload.ProtectContent})
	return classifyTransportError(err)
}

func (client *apiClient) DownloadDocument(ctx context.Context, document Document) ([]byte, error) {
	if client == nil || client.initErr != nil || client.bot == nil {
		return nil, errors.New("telegram bot transport is unavailable")
	}
	if err := ValidateDocument(document); err != nil {
		return nil, err
	}
	file, err := client.bot.GetFile(nonNilContext(ctx), &telegrambot.GetFileParams{FileID: document.FileID})
	if err != nil {
		return nil, classifyTransportError(err)
	}
	if file == nil || strings.TrimSpace(file.FilePath) == "" {
		return nil, errors.New("telegram document file path is unavailable")
	}
	request, err := http.NewRequestWithContext(nonNilContext(ctx), http.MethodGet, client.bot.FileDownloadLink(file), nil)
	if err != nil {
		return nil, err
	}
	response, err := client.httpClient.Do(request)
	if err != nil {
		return nil, classifyTransportError(err)
	}
	defer response.Body.Close()
	if response.StatusCode < http.StatusOK || response.StatusCode >= http.StatusMultipleChoices {
		return nil, &transportError{Class: transportErrorTransport}
	}
	reader := io.LimitReader(response.Body, MaxFileTransferBytes+1)
	data, err := io.ReadAll(reader)
	if err != nil {
		return nil, err
	}
	if int64(len(data)) > MaxFileTransferBytes {
		return nil, errors.New("telegram document exceeds the allowed size")
	}
	return data, nil
}

func (client *apiClient) EditScreen(ctx context.Context, chatID, messageID int64, screen Screen) error {
	if client == nil || client.initErr != nil || client.bot == nil {
		return errors.New("telegram bot transport is unavailable")
	}
	if messageID <= 0 || messageID > int64(^uint(0)>>1) {
		return errors.New("telegram message id is invalid")
	}
	params := &telegrambot.EditMessageTextParams{ChatID: chatID, MessageID: int(messageID), ReplyMarkup: screenKeyboard(screen.Keyboard)}
	if rich, ok := screenRichMessage(screen); ok {
		params.RichMessage = &rich
	} else {
		params.Text = screenText(screen)
		params.ParseMode = models.ParseModeHTML
	}
	_, err := client.bot.EditMessageText(nonNilContext(ctx), params)
	if err != nil && params.RichMessage != nil && richMessageFallbackAllowed(err) {
		params.RichMessage = nil
		params.Text = screenText(screen)
		params.ParseMode = models.ParseModeHTML
		_, err = client.bot.EditMessageText(nonNilContext(ctx), params)
	}
	return classifyTransportError(err)
}

func screenRichMessage(screen Screen) (models.InputRichMessage, bool) {
	if screen.Rich == nil {
		return models.InputRichMessage{}, false
	}
	html := strings.TrimSpace(string(RichMessageHTML(screen.Rich)))
	if html == "" {
		return models.InputRichMessage{}, false
	}
	return models.InputRichMessage{HTML: html}, true
}

func richMessageFallbackAllowed(err error) bool {
	kind := transportErrorKind(classifyTransportError(err))
	return kind == transportErrorBadRequest || kind == transportErrorNotFound
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

func (client *apiClient) SetCommands(ctx context.Context, commands []Command) error {
	if client == nil || client.initErr != nil || client.bot == nil {
		return errors.New("telegram bot transport is unavailable")
	}
	values := make([]models.BotCommand, 0, len(commands))
	for _, command := range commands {
		name, description := strings.TrimSpace(command.Name), strings.TrimSpace(command.Description)
		if name == "" || description == "" {
			continue
		}
		values = append(values, models.BotCommand{Command: name, Description: description})
	}
	if len(values) == 0 {
		return errors.New("telegram command registry is empty")
	}
	_, err := client.bot.SetMyCommands(nonNilContext(ctx), &telegrambot.SetMyCommandsParams{Commands: values})
	return classifyTransportError(err)
}

func (client *apiClient) SetChatMenuButton(ctx context.Context, chatID int64, button MenuButton) error {
	if client == nil || client.initErr != nil || client.bot == nil || chatID <= 0 {
		return errors.New("telegram bot transport is unavailable")
	}
	var value models.InputMenuButton
	switch button.Type {
	case MenuButtonCommands:
		value = models.MenuButtonCommands{Type: models.MenuButtonTypeCommands}
	case MenuButtonDefault:
		value = models.MenuButtonDefault{Type: models.MenuButtonTypeDefault}
	default:
		return errors.New("unsupported telegram chat menu button")
	}
	_, err := client.bot.SetChatMenuButton(nonNilContext(ctx), &telegrambot.SetChatMenuButtonParams{ChatID: chatID, MenuButton: value})
	return classifyTransportError(err)
}

func (client *apiClient) GetChatMenuButton(ctx context.Context, chatID int64) (MenuButton, error) {
	if client == nil || client.initErr != nil || client.bot == nil || chatID <= 0 {
		return MenuButton{}, errors.New("telegram bot transport is unavailable")
	}
	value, err := client.bot.GetChatMenuButton(nonNilContext(ctx), &telegrambot.GetChatMenuButtonParams{ChatID: chatID})
	if err != nil {
		return MenuButton{}, classifyTransportError(err)
	}
	switch value.Type {
	case models.MenuButtonTypeCommands:
		return MenuButton{Type: MenuButtonCommands}, nil
	case models.MenuButtonTypeDefault:
		return MenuButton{Type: MenuButtonDefault}, nil
	case models.MenuButtonTypeWebApp:
		return MenuButton{}, errors.New("telegram chat menu unexpectedly uses WebApp mode")
	default:
		return MenuButton{}, errors.New("telegram chat menu type is unsupported")
	}
}

func (client *apiClient) DeleteMessage(ctx context.Context, chatID, messageID int64) error {
	if client == nil || client.initErr != nil || client.bot == nil || chatID <= 0 || messageID <= 0 || messageID > int64(^uint(0)>>1) {
		return errors.New("telegram bot transport is unavailable")
	}
	deleted, err := client.bot.DeleteMessage(nonNilContext(ctx), &telegrambot.DeleteMessageParams{ChatID: chatID, MessageID: int(messageID)})
	if err != nil {
		return classifyTransportError(err)
	}
	if !deleted {
		return errors.New("telegram message was not deleted")
	}
	return nil
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

type keyboardCapabilities struct {
	Styles   bool
	Disabled bool
	CopyText bool
	WebApp   bool
}

var nativeKeyboardCapabilities = keyboardCapabilities{Styles: true, Disabled: true, CopyText: true, WebApp: true}

func screenKeyboard(rows [][]Button) models.InlineKeyboardMarkup {
	return screenKeyboardWithCapabilities(rows, nativeKeyboardCapabilities)
}

func screenKeyboardWithCapabilities(rows [][]Button, capabilities keyboardCapabilities) models.InlineKeyboardMarkup {
	keyboard := make([][]models.InlineKeyboardButton, 0, len(rows))
	for _, row := range rows {
		buttons := make([]models.InlineKeyboardButton, 0, len(row))
		for _, button := range row {
			if strings.TrimSpace(button.Text) == "" {
				continue
			}
			if button.Disabled && !capabilities.Disabled {
				continue
			}
			if button.CopyText != "" && !capabilities.CopyText {
				continue
			}
			if button.WebAppURL != "" && !capabilities.WebApp {
				continue
			}
			item := models.InlineKeyboardButton{Text: button.Text, CallbackData: button.CallbackData, URL: button.URL}
			if capabilities.Styles {
				item.Style = string(semanticButtonStyle(button))
			}
			if button.CopyText != "" {
				item.CopyText = &models.CopyTextButton{Text: button.CopyText}
			}
			if button.WebAppURL != "" {
				item.WebApp = &models.WebAppInfo{URL: button.WebAppURL}
			}
			if button.Disabled {
				item.Disabled = &models.DisabledButton{}
			}
			buttons = append(buttons, item)
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
	if message.ReplyToMessage != nil {
		result.ReplyToMessage = messageFromModel(message.ReplyToMessage)
	}
	if message.Document != nil {
		result.Document = &Document{FileID: message.Document.FileID, FileName: message.Document.FileName, MimeType: message.Document.MimeType, FileSize: message.Document.FileSize}
	}
	if message.UsersShared != nil {
		shared := &UsersShared{RequestID: message.UsersShared.RequestID, Users: make([]SharedUser, 0, len(message.UsersShared.Users))}
		for _, user := range message.UsersShared.Users {
			shared.Users = append(shared.Users, SharedUser{UserID: user.UserID, FirstName: user.FirstName, LastName: user.LastName, Username: user.Username})
		}
		result.UsersShared = shared
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
	if message.ReplyToMessage != nil {
		result.ReplyToMessage = messageToModel(message.ReplyToMessage)
	}
	if message.Document != nil {
		result.Document = &models.Document{FileID: message.Document.FileID, FileName: message.Document.FileName, MimeType: message.Document.MimeType, FileSize: message.Document.FileSize}
	}
	if message.UsersShared != nil {
		shared := &models.UsersShared{RequestID: message.UsersShared.RequestID, Users: make([]models.SharedUser, 0, len(message.UsersShared.Users))}
		for _, user := range message.UsersShared.Users {
			shared.Users = append(shared.Users, models.SharedUser{UserID: user.UserID, FirstName: user.FirstName, LastName: user.LastName, Username: user.Username})
		}
		result.UsersShared = shared
	}
	return result
}

func userFromModel(user models.User) User {
	return User{ID: user.ID, Username: user.Username, FirstName: user.FirstName, LastName: user.LastName, HasTopicsEnabled: user.HasTopicsEnabled, AllowsUsersToCreateTopics: user.AllowsUsersToCreateTopics}
}

func userToModel(user User) models.User {
	return models.User{ID: user.ID, Username: user.Username, FirstName: user.FirstName, LastName: user.LastName, HasTopicsEnabled: user.HasTopicsEnabled, AllowsUsersToCreateTopics: user.AllowsUsersToCreateTopics}
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
