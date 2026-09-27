package application

import (
	"errors"
	"fmt"

	"go.mewis.me/codemcp/internal/auth"
	"go.mewis.me/codemcp/internal/secretstore"
	tracepkg "go.mewis.me/codemcp/internal/trace"
)

type authSecretSnapshot struct {
	kind   string
	value  string
	exists bool
}

func replaceAuthSecrets(root string, values map[string]string) (func() error, error) {
	order := []string{"mcp", "admin"}
	snapshots := make([]authSecretSnapshot, 0, len(values))
	for _, kind := range order {
		if _, ok := values[kind]; !ok {
			continue
		}
		value, err := auth.LoadToken(root, kind)
		switch {
		case err == nil:
			snapshots = append(snapshots, authSecretSnapshot{kind: kind, value: value, exists: true})
		case errors.Is(err, secretstore.ErrNotFound):
			snapshots = append(snapshots, authSecretSnapshot{kind: kind})
		default:
			return nil, err
		}
	}
	applied := 0
	for _, snapshot := range snapshots {
		if err := auth.StoreToken(root, snapshot.kind, values[snapshot.kind]); err != nil {
			for i := applied - 1; i >= 0; i-- {
				previous := snapshots[i]
				value := ""
				if previous.exists {
					value = previous.value
				}
				_ = auth.StoreToken(root, previous.kind, value)
			}
			return nil, err
		}
		applied++
	}
	return func() error {
		var result error
		for i := len(snapshots) - 1; i >= 0; i-- {
			previous := snapshots[i]
			value := ""
			if previous.exists {
				value = previous.value
			}
			result = errors.Join(result, auth.StoreToken(root, previous.kind, value))
		}
		return result
	}, nil
}

func authSecretPreview(root, kind string, configured bool) (string, error) {
	if !configured {
		return "not configured", nil
	}
	raw, err := auth.LoadToken(root, kind)
	if err == nil {
		return tracepkg.MaskSecret(raw, true), nil
	}
	if errors.Is(err, secretstore.ErrNotFound) {
		return fmt.Sprintf("%s_********legacy", kind), nil
	}
	return "", err
}
