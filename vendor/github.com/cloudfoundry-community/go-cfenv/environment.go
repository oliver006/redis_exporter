package cfenv

import (
	"os"
)

// CurrentEnv translates the current environment to a map[string]string.
func CurrentEnv() map[string]string {
	return Env(os.Environ())
}

// Env translates the provided environment to a map[string]string.
func Env(env []string) map[string]string {
	vars := mapEnv(env, splitEnv)
	return vars
}
