package llm

import (
	"errors"
	"fmt"
	"strings"

	"go.mewis.me/codemcp/internal/secretstore"
)

func CredentialAccount(rawID string) (string, error) {
	id, err := NormalizeProviderID(rawID)
	if err != nil {
		return "", err
	}
	return secretstore.AccountName(secretstore.DomainLLM, string(id), "api-key"), nil
}

func CredentialAccounts(root string) ([]string, error) {
	catalog, err := NewStore(root).Load()
	if err != nil {
		return nil, err
	}
	result := make([]string, 0, len(catalog.Providers))
	for _, provider := range catalog.Providers {
		account, err := CredentialAccount(string(provider.ID))
		if err != nil {
			return nil, err
		}
		result = append(result, account)
	}
	return result, nil
}

func LoadCredential(root, rawID string) (string, error) {
	account, err := CredentialAccount(rawID)
	if err != nil {
		return "", err
	}
	return secretstore.New(root).Get(account)
}

func CredentialConfigured(root, rawID string) (bool, error) {
	_, err := LoadCredential(root, rawID)
	switch {
	case err == nil:
		return true, nil
	case errors.Is(err, secretstore.ErrNotFound):
		return false, nil
	default:
		return false, err
	}
}

func CredentialChange(rawID, value string) (secretstore.Change, error) {
	account, err := CredentialAccount(rawID)
	if err != nil {
		return secretstore.Change{}, err
	}
	return secretstore.Change{Name: account, Value: strings.TrimSpace(value)}, nil
}

// UpdateWithSecrets serializes provider-state mutation with its owned secret
// changes. Secret state is restored when provider persistence fails.
func (s *Store) UpdateWithSecrets(mutate func(Catalog) (Catalog, []secretstore.Change, error)) (Catalog, error) {
	if err := s.validateRoot(); err != nil {
		return Catalog{}, err
	}
	if mutate == nil {
		return Catalog{}, errors.New("llm provider store transaction requires mutation function")
	}
	lock, err := s.acquireMutationLock()
	if err != nil {
		return Catalog{}, err
	}
	defer lock.Release()

	current, err := s.loadLocked()
	if err != nil {
		return Catalog{}, err
	}
	next, changes, err := mutate(cloneCatalog(current))
	if err != nil {
		return Catalog{}, err
	}
	next, err = NormalizeCatalog(next)
	if err != nil {
		return Catalog{}, err
	}

	secrets := secretstore.New(s.root)
	restore, err := snapshotSecretChanges(secrets, changes)
	if err != nil {
		return Catalog{}, err
	}
	if err := secrets.Apply(changes); err != nil {
		return Catalog{}, err
	}
	if err := s.writeLocked(next); err != nil {
		if restoreErr := secrets.Apply(restore); restoreErr != nil {
			return Catalog{}, errors.Join(err, fmt.Errorf("restore llm credentials: %w", restoreErr))
		}
		return Catalog{}, err
	}
	return cloneCatalog(next), nil
}

func RemoveProvider(root, rawID string) error {
	store := NewStore(root)
	_, err := store.UpdateWithSecrets(func(current Catalog) (Catalog, []secretstore.Change, error) {
		if err := ValidateProviderRemoval(current, rawID); err != nil {
			return Catalog{}, nil, err
		}
		id, err := NormalizeProviderID(rawID)
		if err != nil {
			return Catalog{}, nil, err
		}
		providers := make([]Provider, 0, len(current.Providers)-1)
		for _, provider := range current.Providers {
			if provider.ID != id {
				providers = append(providers, provider)
			}
		}
		current.Providers = providers
		change, err := CredentialChange(string(id), "")
		if err != nil {
			return Catalog{}, nil, err
		}
		return current, []secretstore.Change{change}, nil
	})
	return err
}

func snapshotSecretChanges(store *secretstore.Store, changes []secretstore.Change) ([]secretstore.Change, error) {
	seen := map[string]struct{}{}
	restore := make([]secretstore.Change, 0, len(changes))
	for _, change := range changes {
		name := strings.TrimSpace(change.Name)
		if name == "" {
			return nil, errors.New("llm credential account is required")
		}
		if _, ok := seen[name]; ok {
			continue
		}
		seen[name] = struct{}{}
		value, err := store.Get(name)
		if errors.Is(err, secretstore.ErrNotFound) {
			restore = append(restore, secretstore.Change{Name: name})
			continue
		}
		if err != nil {
			return nil, err
		}
		restore = append(restore, secretstore.Change{Name: name, Value: value})
	}
	return restore, nil
}
