package app

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"strconv"
	"strings"
	"time"

	"go.mewis.me/codemcp/internal/config"
	"go.mewis.me/codemcp/internal/integrations/semantic"
	typesafeintegration "go.mewis.me/codemcp/internal/integrations/typesafe"
	"go.mewis.me/codemcp/internal/secretstore"
)

const typeSafeSemanticProvider = "typesafe"

type typeSafeRuntimeCandidate struct {
	fingerprint  string
	registration semantic.ProviderRegistration
	unavailable  semantic.ErrorCategory
}

func (a *App) prepareTypeSafe(cfg config.Config) (typeSafeRuntimeCandidate, error) {
	value := cfg.Integrations.TypeSafe
	base := strings.Join([]string{
		strconv.FormatBool(value.Enabled),
		strings.TrimSpace(value.Model),
		strconv.Itoa(value.TimeoutMS),
	}, "\x00")
	if !value.Enabled {
		return typeSafeRuntimeCandidate{
			fingerprint: digestTypeSafeRuntime(base),
			unavailable: semantic.ErrorDisabled,
		}, nil
	}

	apiKey, err := typesafeintegration.LoadAPIKey(config.RootPath())
	if errors.Is(err, secretstore.ErrNotFound) {
		return typeSafeRuntimeCandidate{
			fingerprint: digestTypeSafeRuntime(base + "\x00missing"),
			unavailable: semantic.ErrorMisconfigured,
		}, nil
	}
	if err != nil {
		return typeSafeRuntimeCandidate{}, err
	}

	options := typesafeintegration.ClientOptions{}
	if a != nil {
		options.HTTPClient = a.typeSafeHTTPClient
		options.BaseURL = a.typeSafeBaseURL
	}
	client, err := typesafeintegration.NewClientWithOptions(
		apiKey,
		value.Model,
		time.Duration(value.TimeoutMS)*time.Millisecond,
		options,
	)
	if err != nil {
		return typeSafeRuntimeCandidate{}, err
	}
	registration := semantic.ProviderRegistration{
		Provider:       client,
		RiskClassifier: client,
		Model:          value.Model,
	}
	if registration.Provider == nil || registration.RiskClassifier == nil {
		return typeSafeRuntimeCandidate{}, semantic.NewError(semantic.ErrorUnavailable, "TypeSafe runtime requires semantic and risk capabilities")
	}
	return typeSafeRuntimeCandidate{
		fingerprint:  digestTypeSafeRuntime(base + "\x00" + digestTypeSafeRuntime(apiKey)),
		registration: registration,
	}, nil
}

func (a *App) commitTypeSafe(candidate typeSafeRuntimeCandidate) error {
	if a == nil || a.Tools == nil || a.Tools.Semantic == nil {
		return errors.New("semantic runtime is unavailable")
	}
	a.typeSafeMu.Lock()
	defer a.typeSafeMu.Unlock()
	if candidate.fingerprint != "" && candidate.fingerprint == a.typeSafeFingerprint {
		return nil
	}
	if candidate.registration.Provider != nil || candidate.registration.RiskClassifier != nil {
		if candidate.registration.Provider == nil || candidate.registration.RiskClassifier == nil {
			return semantic.NewError(semantic.ErrorUnavailable, "TypeSafe runtime requires semantic and risk capabilities")
		}
		if err := a.Tools.Semantic.ConfigureProvider(typeSafeSemanticProvider, candidate.registration); err != nil {
			return err
		}
	} else {
		category := candidate.unavailable
		if category == "" {
			category = semantic.ErrorMisconfigured
		}
		a.Tools.Semantic.SetUnavailable(category)
	}
	a.typeSafeFingerprint = candidate.fingerprint
	return nil
}

func digestTypeSafeRuntime(value string) string {
	sum := sha256.Sum256([]byte(value))
	return hex.EncodeToString(sum[:])
}
