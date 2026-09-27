package telegram

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
)

const defaultAPIBase = "https://api.telegram.org"

type User struct {
	ID int64 `json:"id"`
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
	GetUpdates(context.Context, int64, int, time.Duration) ([]Update, error)
	SendMessage(context.Context, int64, string) error
}

type apiClient struct {
	token   string
	baseURL string
	client  *http.Client
}

func newAPIClient(token string) API {
	return &apiClient{token: strings.TrimSpace(token), baseURL: defaultAPIBase, client: &http.Client{}}
}

func (client *apiClient) GetUpdates(ctx context.Context, offset int64, limit int, timeout time.Duration) ([]Update, error) {
	if client == nil || client.token == "" {
		return nil, errors.New("telegram bot token is unavailable")
	}
	if limit <= 0 || limit > 100 {
		limit = 100
	}
	seconds := int(timeout / time.Second)
	if seconds < 0 {
		seconds = 0
	}
	values := url.Values{}
	values.Set("offset", strconv.FormatInt(offset, 10))
	values.Set("limit", strconv.Itoa(limit))
	values.Set("timeout", strconv.Itoa(seconds))
	var response struct {
		OK     bool     `json:"ok"`
		Result []Update `json:"result"`
	}
	if err := client.call(ctx, http.MethodGet, "getUpdates?"+values.Encode(), nil, &response); err != nil {
		return nil, err
	}
	if !response.OK {
		return nil, errors.New("telegram getUpdates rejected")
	}
	return response.Result, nil
}

func (client *apiClient) SendMessage(ctx context.Context, chatID int64, text string) error {
	body, err := json.Marshal(map[string]any{"chat_id": chatID, "text": text})
	if err != nil {
		return err
	}
	var response struct {
		OK bool `json:"ok"`
	}
	if err := client.call(ctx, http.MethodPost, "sendMessage", body, &response); err != nil {
		return err
	}
	if !response.OK {
		return errors.New("telegram sendMessage rejected")
	}
	return nil
}

func (client *apiClient) call(ctx context.Context, method, endpoint string, body []byte, output any) error {
	if ctx == nil {
		ctx = context.Background()
	}
	requestURL := fmt.Sprintf("%s/bot%s/%s", strings.TrimRight(client.baseURL, "/"), client.token, endpoint)
	var reader io.Reader
	if body != nil {
		reader = bytes.NewReader(body)
	}
	request, err := http.NewRequestWithContext(ctx, method, requestURL, reader)
	if err != nil {
		return err
	}
	if body != nil {
		request.Header.Set("Content-Type", "application/json")
	}
	response, err := client.client.Do(request)
	if err != nil {
		return err
	}
	defer response.Body.Close()
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		_, _ = io.Copy(io.Discard, io.LimitReader(response.Body, 4096))
		return fmt.Errorf("telegram API HTTP %d", response.StatusCode)
	}
	return json.NewDecoder(io.LimitReader(response.Body, 1<<20)).Decode(output)
}
