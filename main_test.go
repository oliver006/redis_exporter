package main

import (
	"os"
	"testing"
)

func TestGetEnv(t *testing.T) {
	tests := []struct {
		name       string
		key        string
		defaultVal string
		envValue   string
		setEnv     bool
		expected   string
	}{
		{
			name:       "environment variable exists",
			key:        "TEST_ENV_VAR",
			defaultVal: "default",
			envValue:   "from_env",
			setEnv:     true,
			expected:   "from_env",
		},
		{
			name:       "environment variable does not exist",
			key:        "NONEXISTENT_ENV_VAR",
			defaultVal: "default_value",
			setEnv:     false,
			expected:   "default_value",
		},
		{
			name:       "empty environment variable",
			key:        "EMPTY_ENV_VAR",
			defaultVal: "default",
			envValue:   "",
			setEnv:     true,
			expected:   "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if tt.setEnv {
				os.Setenv(tt.key, tt.envValue)
				defer os.Unsetenv(tt.key)
			}

			result := getEnv(tt.key, tt.defaultVal)
			if result != tt.expected {
				t.Errorf("getEnv() = %v, expected %v", result, tt.expected)
			}
		})
	}
}

func TestGetEnvBool(t *testing.T) {
	tests := []struct {
		name       string
		key        string
		defaultVal bool
		envValue   string
		setEnv     bool
		expected   bool
	}{
		{
			name:       "true from environment",
			key:        "TEST_BOOL_TRUE",
			defaultVal: false,
			envValue:   "true",
			setEnv:     true,
			expected:   true,
		},
		{
			name:       "false from environment",
			key:        "TEST_BOOL_FALSE",
			defaultVal: true,
			envValue:   "false",
			setEnv:     true,
			expected:   false,
		},
		{
			name:       "1 from environment (true)",
			key:        "TEST_BOOL_ONE",
			defaultVal: false,
			envValue:   "1",
			setEnv:     true,
			expected:   true,
		},
		{
			name:       "0 from environment (false)",
			key:        "TEST_BOOL_ZERO",
			defaultVal: true,
			envValue:   "0",
			setEnv:     true,
			expected:   false,
		},
		{
			name:       "invalid bool value returns default",
			key:        "TEST_BOOL_INVALID",
			defaultVal: true,
			envValue:   "invalid",
			setEnv:     true,
			expected:   true,
		},
		{
			name:       "environment variable does not exist",
			key:        "NONEXISTENT_BOOL_VAR",
			defaultVal: false,
			setEnv:     false,
			expected:   false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if tt.setEnv {
				os.Setenv(tt.key, tt.envValue)
				defer os.Unsetenv(tt.key)
			}

			result := getEnvBool(tt.key, tt.defaultVal)
			if result != tt.expected {
				t.Errorf("getEnvBool() = %v, expected %v", result, tt.expected)
			}
		})
	}
}

func TestGetEnvInt64(t *testing.T) {
	tests := []struct {
		name       string
		key        string
		defaultVal int64
		envValue   string
		setEnv     bool
		expected   int64
	}{
		{
			name:       "valid positive integer",
			key:        "TEST_INT_POSITIVE",
			defaultVal: 100,
			envValue:   "1234",
			setEnv:     true,
			expected:   1234,
		},
		{
			name:       "valid negative integer",
			key:        "TEST_INT_NEGATIVE",
			defaultVal: 100,
			envValue:   "-567",
			setEnv:     true,
			expected:   -567,
		},
		{
			name:       "zero value",
			key:        "TEST_INT_ZERO",
			defaultVal: 100,
			envValue:   "0",
			setEnv:     true,
			expected:   0,
		},
		{
			name:       "invalid integer returns default",
			key:        "TEST_INT_INVALID",
			defaultVal: 999,
			envValue:   "not_a_number",
			setEnv:     true,
			expected:   999,
		},
		{
			name:       "empty value returns default",
			key:        "TEST_INT_EMPTY",
			defaultVal: 500,
			envValue:   "",
			setEnv:     true,
			expected:   500,
		},
		{
			name:       "environment variable does not exist",
			key:        "NONEXISTENT_INT_VAR",
			defaultVal: 42,
			setEnv:     false,
			expected:   42,
		},
		{
			name:       "large integer value",
			key:        "TEST_INT_LARGE",
			defaultVal: 1,
			envValue:   "9223372036854775807", // max int64
			setEnv:     true,
			expected:   9223372036854775807,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if tt.setEnv {
				os.Setenv(tt.key, tt.envValue)
				defer os.Unsetenv(tt.key)
			}

			result := getEnvInt64(tt.key, tt.defaultVal)
			if result != tt.expected {
				t.Errorf("getEnvInt64() = %v, expected %v", result, tt.expected)
			}
		})
	}
}
