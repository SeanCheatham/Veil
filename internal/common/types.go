// Package common provides shared types and utilities used across Veil services.
package common

// Message represents an encrypted message in the Veil network.
type Message struct {
	// ID is a unique identifier for the message.
	ID string `json:"id"`

	// Payload is the encrypted message content.
	Payload []byte `json:"payload"`

	// Timestamp is when the message was received by the pool.
	Timestamp int64 `json:"timestamp"`

	// Sequence is the consensus-ordered position in the pool.
	Sequence uint64 `json:"sequence"`
}

// HealthResponse is the standard health check response.
type HealthResponse struct {
	Status  string `json:"status"`
	Service string `json:"service"`
}

// ServiceConfig holds common configuration for all services.
type ServiceConfig struct {
	// Port is the port to listen on.
	Port string

	// LogLevel controls logging verbosity.
	LogLevel string
}
