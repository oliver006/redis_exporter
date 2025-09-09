package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	log "github.com/sirupsen/logrus"
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

func TestParseLogLevel(t *testing.T) {
	tests := []struct {
		name        string
		level       string
		expected    log.Level
		expectError bool
	}{
		{"debug level", "debug", log.DebugLevel, false},
		{"DEBUG level", "DEBUG", log.DebugLevel, false},
		{"info level", "info", log.InfoLevel, false},
		{"INFO level", "INFO", log.InfoLevel, false},
		{"warn level", "warn", log.WarnLevel, false},
		{"WARN level", "WARN", log.WarnLevel, false},
		{"warning level", "warning", log.WarnLevel, false},
		{"WARNING level", "WARNING", log.WarnLevel, false},
		{"error level", "error", log.ErrorLevel, false},
		{"ERROR level", "ERROR", log.ErrorLevel, false},
		{"invalid level", "invalid", log.InfoLevel, true},
		{"empty level", "", log.InfoLevel, true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result, err := parseLogLevel(tt.level)
			if tt.expectError {
				if err == nil {
					t.Errorf("parseLogLevel(%s) expected error but got none", tt.level)
				}
				// For invalid levels, we still expect InfoLevel as default
				if result != tt.expected {
					t.Errorf("parseLogLevel(%s) = %v, expected %v", tt.level, result, tt.expected)
				}
			} else {
				if err != nil {
					t.Errorf("parseLogLevel(%s) unexpected error: %v", tt.level, err)
				}
				if result != tt.expected {
					t.Errorf("parseLogLevel(%s) = %v, expected %v", tt.level, result, tt.expected)
				}
			}
		})
	}
}

func TestValidateTLSClientConfig(t *testing.T) {
	tests := []struct {
		name        string
		certFile    string
		keyFile     string
		expectError bool
	}{
		{"both files provided", "/path/to/cert.pem", "/path/to/key.pem", false},
		{"both files empty", "", "", false},
		{"only cert file provided", "/path/to/cert.pem", "", true},
		{"only key file provided", "", "/path/to/key.pem", true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := validateTLSClientConfig(tt.certFile, tt.keyFile)
			if tt.expectError {
				if err == nil {
					t.Errorf("validateTLSClientConfig(%s, %s) expected error but got none", tt.certFile, tt.keyFile)
				}
			} else {
				if err != nil {
					t.Errorf("validateTLSClientConfig(%s, %s) unexpected error: %v", tt.certFile, tt.keyFile, err)
				}
			}
		})
	}
}

func TestLoadScripts(t *testing.T) {
	// Create temporary directory for test scripts
	tmpDir := t.TempDir()

	// Create test script files
	script1 := filepath.Join(tmpDir, "script1.lua")
	script1Content := "return {\"key1\", \"value1\"}"
	if err := os.WriteFile(script1, []byte(script1Content), 0644); err != nil {
		t.Fatalf("Failed to create test script1: %v", err)
	}

	script2 := filepath.Join(tmpDir, "script2.lua")
	script2Content := "return {\"key2\", \"value2\"}"
	if err := os.WriteFile(script2, []byte(script2Content), 0644); err != nil {
		t.Fatalf("Failed to create test script2: %v", err)
	}

	tests := []struct {
		name        string
		scriptPath  string
		expectError bool
		expectedLen int
	}{
		{"empty script path", "", false, 0},
		{"single script", script1, false, 1},
		{"multiple scripts", script1 + "," + script2, false, 2},
		{"nonexistent script", "/nonexistent/script.lua", true, 0},
		{"mixed valid and invalid", script1 + ",/nonexistent/script.lua", true, 0},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result, err := loadScripts(tt.scriptPath)
			if tt.expectError {
				if err == nil {
					t.Errorf("loadScripts(%s) expected error but got none", tt.scriptPath)
				}
			} else {
				if err != nil {
					t.Errorf("loadScripts(%s) unexpected error: %v", tt.scriptPath, err)
				}
				if tt.expectedLen == 0 && result != nil {
					t.Errorf("loadScripts(%s) expected nil result but got %v", tt.scriptPath, result)
				}
				if tt.expectedLen > 0 {
					if result == nil {
						t.Errorf("loadScripts(%s) expected non-nil result", tt.scriptPath)
					} else if len(result) != tt.expectedLen {
						t.Errorf("loadScripts(%s) expected %d scripts, got %d", tt.scriptPath, tt.expectedLen, len(result))
					}
				}
			}

			// Verify content for successful cases
			if !tt.expectError && tt.expectedLen > 0 {
				scripts := strings.Split(tt.scriptPath, ",")
				for _, scriptPath := range scripts {
					if content, exists := result[scriptPath]; !exists {
						t.Errorf("loadScripts(%s) missing script %s", tt.scriptPath, scriptPath)
					} else if len(content) == 0 {
						t.Errorf("loadScripts(%s) empty content for script %s", tt.scriptPath, scriptPath)
					}
				}
			}
		})
	}
}

func TestSetupLogging(t *testing.T) {
	// Save original log level to restore after tests
	originalLevel := log.GetLevel()
	defer log.SetLevel(originalLevel)

	tests := []struct {
		name          string
		isDebug       bool
		logLevel      string
		logFormat     string
		expectedLevel log.Level
	}{
		{"debug enabled", true, "info", "txt", log.DebugLevel},
		{"info level", false, "info", "txt", log.InfoLevel},
		{"warn level", false, "warn", "txt", log.WarnLevel},
		{"error level", false, "error", "txt", log.ErrorLevel},
		{"invalid level defaults to info", false, "invalid", "txt", log.InfoLevel},
		{"json format", false, "info", "json", log.InfoLevel},
		{"text format", false, "info", "txt", log.InfoLevel},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := setupLogging(tt.isDebug, tt.logLevel, tt.logFormat)
			if err != nil {
				t.Errorf("setupLogging() unexpected error: %v", err)
			}

			if log.GetLevel() != tt.expectedLevel {
				t.Errorf("setupLogging() level = %v, expected %v", log.GetLevel(), tt.expectedLevel)
			}

			// Check formatter type (basic check)
			formatter := log.StandardLogger().Formatter
			if tt.logFormat == "json" {
				if _, ok := formatter.(*log.JSONFormatter); !ok {
					t.Errorf("setupLogging() expected JSONFormatter for json format")
				}
			} else {
				if _, ok := formatter.(*log.TextFormatter); !ok {
					t.Errorf("setupLogging() expected TextFormatter for txt format")
				}
			}
		})
	}
}

func TestCreatePrometheusRegistry(t *testing.T) {
	tests := []struct {
		name                 string
		redisMetricsOnly     bool
		inclGoRuntimeMetrics bool
		description          string
	}{
		{"redis metrics only", true, false, "should create registry with only Redis metrics"},
		{"redis + process metrics", false, false, "should create registry with Redis and process metrics"},
		{"all metrics", false, true, "should create registry with process and Go metrics"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			registry := createPrometheusRegistry(tt.redisMetricsOnly, tt.inclGoRuntimeMetrics)
			if registry == nil {
				t.Errorf("createPrometheusRegistry(%v, %v) returned nil registry", tt.redisMetricsOnly, tt.inclGoRuntimeMetrics)
			}

			// Verify it's a valid Prometheus registry (it's already *prometheus.Registry)

			// Basic functionality test - gather metrics
			metricFamilies, err := registry.Gather()
			if err != nil {
				t.Errorf("createPrometheusRegistry(%v, %v) registry.Gather() error: %v", tt.redisMetricsOnly, tt.inclGoRuntimeMetrics, err)
			}

			// When redisMetricsOnly=false, inclGoRuntimeMetrics=false we should have process metrics
			// When redisMetricsOnly=false, inclGoRuntimeMetrics=true we should have Go process+runtime metrics
			// When redisMetricsOnly=true, we should have fewer (or no) built-in metrics
			if !tt.redisMetricsOnly {
				if len(metricFamilies) == 0 {
					t.Errorf("createPrometheusRegistry(%v, %v) expected Go process+runtime metrics but got none", tt.redisMetricsOnly, tt.inclGoRuntimeMetrics)
				}
				if !tt.inclGoRuntimeMetrics {
					if len(metricFamilies) > 20 { // process metrics count is less than 10
						t.Errorf("createPrometheusRegistry(%v, %v) expected only process metrics but got %v metrics", tt.redisMetricsOnly, tt.inclGoRuntimeMetrics, len(metricFamilies))
					}
				}
			}
		})
	}
}

// Integration test to verify the functions work together
func TestMainFunctionsIntegration(t *testing.T) {
	// Test that the extracted functions can be used together
	tmpDir := t.TempDir()
	scriptFile := filepath.Join(tmpDir, "test.lua")
	scriptContent := "return redis.call('ping')"

	if err := os.WriteFile(scriptFile, []byte(scriptContent), 0644); err != nil {
		t.Fatalf("Failed to create test script: %v", err)
	}

	// Test logging setup
	originalLevel := log.GetLevel()
	defer log.SetLevel(originalLevel)

	err := setupLogging(false, "debug", "json")
	if err != nil {
		t.Errorf("setupLogging failed: %v", err)
	}

	if log.GetLevel() != log.DebugLevel {
		t.Errorf("Expected debug level, got %v", log.GetLevel())
	}

	// Test script loading
	scripts, err := loadScripts(scriptFile)
	if err != nil {
		t.Errorf("loadScripts failed: %v", err)
	}

	if len(scripts) != 1 {
		t.Errorf("Expected 1 script, got %d", len(scripts))
	}

	if string(scripts[scriptFile]) != scriptContent {
		t.Errorf("Script content mismatch")
	}

	// Test TLS validation
	if err := validateTLSClientConfig("/cert.pem", ""); err == nil {
		t.Error("Expected TLS validation error for mismatched cert/key")
	}

	if err := validateTLSClientConfig("/cert.pem", "/key.pem"); err != nil {
		t.Errorf("Unexpected TLS validation error: %v", err)
	}

	// Test registry creation
	registry := createPrometheusRegistry(true, false)
	if registry == nil {
		t.Error("Registry creation failed")
	}
}
