package config

import "os"

func Env() string { return os.Getenv("CM_CONFIG") }
