package telegram

import (
	"context"
	"errors"
	"strings"
	"time"
)

type BotIdentity struct {
	ID       int64  `json:"id"`
	Username string `json:"username,omitempty"`
	Name     string `json:"name,omitempty"`
}

func (runtime *Runtime) ValidateStoredToken(ctx context.Context) (BotIdentity, error) {
	if runtime == nil {
		return BotIdentity{}, errors.New("telegram runtime is unavailable")
	}
	token, err := loadToken(runtime.root)
	if err != nil {
		return BotIdentity{}, err
	}
	api := runtime.factory(strings.TrimSpace(token))
	if ctx == nil {
		ctx = context.Background()
	}
	validateCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	user, err := api.GetMe(validateCtx)
	if err != nil || user.ID <= 0 {
		return BotIdentity{}, errors.New("telegram bot token validation failed")
	}
	return BotIdentity{
		ID: user.ID, Username: strings.TrimPrefix(strings.TrimSpace(user.Username), "@"),
		Name: strings.TrimSpace(strings.TrimSpace(user.FirstName) + " " + strings.TrimSpace(user.LastName)),
	}, nil
}

func (runtime *Runtime) SetValidatedToken(ctx context.Context, token string) (BotIdentity, error) {
	if runtime == nil {
		return BotIdentity{}, errors.New("telegram runtime is unavailable")
	}
	token = strings.TrimSpace(token)
	if token == "" {
		return BotIdentity{}, errors.New("telegram bot token is required")
	}
	api := runtime.factory(token)
	if ctx == nil {
		ctx = context.Background()
	}
	validateCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	user, err := api.GetMe(validateCtx)
	if err != nil || user.ID <= 0 {
		return BotIdentity{}, errors.New("telegram bot token validation failed")
	}
	if err := SetToken(runtime.root, token); err != nil {
		return BotIdentity{}, err
	}
	return BotIdentity{
		ID: user.ID, Username: strings.TrimPrefix(strings.TrimSpace(user.Username), "@"),
		Name: strings.TrimSpace(strings.TrimSpace(user.FirstName) + " " + strings.TrimSpace(user.LastName)),
	}, nil
}
