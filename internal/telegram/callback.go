package telegram

import (
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"fmt"
	"strings"
)

const (
	callbackVersion      = "ct1"
	MaxCallbackDataBytes = 64
	callbackMACBytes     = 8
)

type CallbackAction string

const (
	CallbackOpen    CallbackAction = "o"
	CallbackBack    CallbackAction = "b"
	CallbackConfirm CallbackAction = "c"
)

type CallbackRef struct {
	Action CallbackAction
	Token  string
}

type CallbackCodec struct{ key []byte }

func NewCallbackCodec(key []byte) (*CallbackCodec, error) {
	if len(key) == 0 {
		key = make([]byte, 32)
		if _, err := rand.Read(key); err != nil {
			return nil, err
		}
	}
	if len(key) < 16 {
		return nil, errors.New("telegram callback signing key is too short")
	}
	return &CallbackCodec{key: append([]byte(nil), key...)}, nil
}

func (codec *CallbackCodec) Encode(action CallbackAction, token string) (string, error) {
	if codec == nil || !validCallbackPart(string(action)) || !validCallbackPart(token) {
		return "", errors.New("invalid telegram callback state")
	}
	payload := callbackVersion + "." + string(action) + "." + token
	mac := codec.sign(payload)
	value := payload + "." + mac
	if len(value) > MaxCallbackDataBytes {
		return "", fmt.Errorf("telegram callback exceeds %d bytes", MaxCallbackDataBytes)
	}
	return value, nil
}

func (codec *CallbackCodec) Decode(value string) (CallbackRef, error) {
	if codec == nil || len(value) > MaxCallbackDataBytes {
		return CallbackRef{}, errors.New("invalid telegram callback")
	}
	parts := strings.Split(value, ".")
	if len(parts) != 4 || parts[0] != callbackVersion || !validCallbackPart(parts[1]) || !validCallbackPart(parts[2]) {
		return CallbackRef{}, errors.New("invalid telegram callback")
	}
	payload := strings.Join(parts[:3], ".")
	expected := codec.sign(payload)
	if !hmac.Equal([]byte(expected), []byte(parts[3])) {
		return CallbackRef{}, errors.New("invalid telegram callback signature")
	}
	action := CallbackAction(parts[1])
	switch action {
	case CallbackOpen, CallbackBack, CallbackConfirm:
	default:
		return CallbackRef{}, errors.New("unsupported telegram callback action")
	}
	return CallbackRef{Action: action, Token: parts[2]}, nil
}

func (codec *CallbackCodec) sign(payload string) string {
	mac := hmac.New(sha256.New, codec.key)
	_, _ = mac.Write([]byte(payload))
	return base64.RawURLEncoding.EncodeToString(mac.Sum(nil)[:callbackMACBytes])
}

func validCallbackPart(value string) bool {
	if value == "" {
		return false
	}
	for _, char := range value {
		if char >= 'a' && char <= 'z' || char >= 'A' && char <= 'Z' || char >= '0' && char <= '9' || char == '_' || char == '-' {
			continue
		}
		return false
	}
	return true
}
