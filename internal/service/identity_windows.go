//go:build windows

package service

import (
	"os/user"
	"path/filepath"
)

func DetectScope() Scope { return ScopeUser }

func InvokingAccount(Scope) (Account, error) {
	current, err := user.Current()
	if err != nil {
		return Account{}, err
	}
	return Account{Username: current.Username, UID: current.Uid, GID: current.Gid, HomeDir: current.HomeDir}, nil
}

func DefaultConfigRoot(account Account) string {
	return filepath.Join(account.HomeDir, ".cm")
}
