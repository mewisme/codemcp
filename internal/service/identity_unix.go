//go:build linux || darwin

package service

import (
	"os"
	"os/user"
	"path/filepath"
)

func DetectScope() Scope {
	if os.Geteuid() == 0 {
		return ScopeSystem
	}
	return ScopeUser
}

func InvokingAccount(scope Scope) (Account, error) {
	var current *user.User
	var err error
	if scope == ScopeSystem && os.Getenv("SUDO_USER") != "" {
		current, err = user.Lookup(os.Getenv("SUDO_USER"))
	} else {
		current, err = user.Current()
	}
	if err != nil {
		return Account{}, err
	}
	return Account{Username: current.Username, UID: current.Uid, GID: current.Gid, HomeDir: current.HomeDir}, nil
}

func DefaultConfigRoot(account Account) string {
	return filepath.Join(account.HomeDir, ".cm")
}
