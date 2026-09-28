package telegram

import (
	"context"
	"errors"
	"time"
)

func (runtime *Runtime) deliver(ctx context.Context, send func() error) error {
	if runtime == nil || send == nil {
		return errors.New("telegram runtime is unavailable")
	}
	if err := runtime.waitDeliverySlot(ctx); err != nil {
		return err
	}
	err := send()
	if retry := transportRetryAfter(err); retry > 0 && retry <= maxDeliveryRetryAfter {
		runtime.markDeliveryFailure(err)
		if waitErr := waitContext(ctx, retry); waitErr != nil {
			return waitErr
		}
		if waitErr := runtime.waitDeliverySlot(ctx); waitErr != nil {
			return waitErr
		}
		err = send()
	}
	if err != nil {
		runtime.markDeliveryFailure(err)
		return err
	}
	runtime.markDeliverySuccess()
	return nil
}

func (runtime *Runtime) deliverValue(ctx context.Context, send func() (int64, error)) (int64, error) {
	if runtime == nil || send == nil {
		return 0, errors.New("telegram runtime is unavailable")
	}
	if err := runtime.waitDeliverySlot(ctx); err != nil {
		return 0, err
	}
	value, err := send()
	if retry := transportRetryAfter(err); retry > 0 && retry <= maxDeliveryRetryAfter {
		runtime.markDeliveryFailure(err)
		if waitErr := waitContext(ctx, retry); waitErr != nil {
			return 0, waitErr
		}
		if waitErr := runtime.waitDeliverySlot(ctx); waitErr != nil {
			return 0, waitErr
		}
		value, err = send()
	}
	if err != nil {
		runtime.markDeliveryFailure(err)
		return 0, err
	}
	runtime.markDeliverySuccess()
	return value, nil
}

func (runtime *Runtime) waitDeliverySlot(ctx context.Context) error {
	if runtime == nil || runtime.deliveryInterval <= 0 {
		return nil
	}
	if ctx == nil {
		ctx = context.Background()
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	runtime.deliveryMu.Lock()
	now := time.Now()
	wait := time.Duration(0)
	if runtime.deliveryNext.After(now) {
		wait = runtime.deliveryNext.Sub(now)
	}
	base := now.Add(wait)
	runtime.deliveryNext = base.Add(runtime.deliveryInterval)
	runtime.deliveryMu.Unlock()
	return waitContext(ctx, wait)
}

func waitContext(ctx context.Context, delay time.Duration) error {
	if delay <= 0 {
		return nil
	}
	if ctx == nil {
		ctx = context.Background()
	}
	timer := time.NewTimer(delay)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}

func transportRetryAfter(err error) time.Duration {
	var typed *transportError
	if errors.As(err, &typed) && typed != nil && typed.Class == transportErrorRateLimited {
		return typed.RetryAfter
	}
	return 0
}

func (runtime *Runtime) markDeliveryFailure(err error) {
	if runtime == nil || err == nil {
		return
	}
	kind := transportErrorKind(err)
	retry := transportRetryAfter(err)
	runtime.mu.Lock()
	runtime.health.DeliveryDegraded = true
	runtime.health.DeliveryFailures++
	runtime.health.LastTransportErrorClass = string(kind)
	runtime.health.DeliveryRateLimited = kind == transportErrorRateLimited
	runtime.health.DeliveryRetryAfterMS = retry.Milliseconds()
	runtime.mu.Unlock()
}

func (runtime *Runtime) markDeliverySuccess() {
	if runtime == nil {
		return
	}
	runtime.mu.Lock()
	runtime.health.DeliveryDegraded = false
	runtime.health.DeliveryRateLimited = false
	runtime.health.DeliveryRetryAfterMS = 0
	runtime.mu.Unlock()
}
