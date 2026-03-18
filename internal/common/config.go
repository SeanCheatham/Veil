// Package common provides shared types and utilities used across Veil services.
package common

import "os"

// GetEnvOrDefault returns the value of an environment variable or a default value.
func GetEnvOrDefault(key, defaultValue string) string {
	if value := os.Getenv(key); value != "" {
		return value
	}
	return defaultValue
}

// LoadServiceConfig loads common service configuration from environment variables.
func LoadServiceConfig(defaultPort string) ServiceConfig {
	return ServiceConfig{
		Port:     GetEnvOrDefault("PORT", defaultPort),
		LogLevel: GetEnvOrDefault("LOG_LEVEL", "info"),
	}
}
