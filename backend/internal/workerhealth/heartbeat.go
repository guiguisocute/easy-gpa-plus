// Package workerhealth defines the shared Redis heartbeat contract for workers
// and the operations console, independent of the deployment environment.
package workerhealth

import "time"

const (
	Alive   = "alive"
	Stale   = "stale"
	Missing = "missing"
	Error   = "error"
)

type State struct {
	Name            string     `json:"name"`
	LastHeartbeat   *time.Time `json:"lastHeartbeat"`
	HeartbeatAlive  bool       `json:"heartbeatAlive"`
	HeartbeatStatus string     `json:"heartbeatStatus"`
	HeartbeatError  string     `json:"heartbeatError,omitempty"`
}

// Key preserves the namespace already used by running workers.
func Key(name string) string { return "easygpa:worker:" + name + ":heartbeat" }
