package notification

import "context"

type TelegramSender interface {
	SendNotification(context.Context, Message) error
}

type TelegramProvider struct {
	Sender TelegramSender
}

func NewTelegramProvider(sender TelegramSender) *TelegramProvider {
	return &TelegramProvider{Sender: sender}
}

func (p *TelegramProvider) Name() string { return ProviderTelegram }

func (p *TelegramProvider) Available() bool {
	return p != nil && p.Sender != nil
}

func (p *TelegramProvider) Notify(ctx context.Context, message Message) error {
	if p == nil || p.Sender == nil {
		return ErrProviderUnavailable
	}
	if ctx == nil {
		ctx = context.Background()
	}
	return p.Sender.SendNotification(ctx, message)
}
