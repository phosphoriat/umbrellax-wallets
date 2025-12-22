package tasks

import (
	"context"
	"errors"
	"os"
	"time"
)

type BaseTask struct {
	name        string
	timeout     time.Duration
	description string
	maxRetries  int
}

// GetName Get unique task name
func (b *BaseTask) GetName() string {
	return b.name
}

// GetDescription Get task description
func (b *BaseTask) GetDescription() string {
	return b.description
}

// GetTimeout Get timeout for task execution
func (b *BaseTask) GetTimeout() time.Duration {
	return b.timeout
}

// GetMaxRetries Get maximum count of retries
func (b *BaseTask) GetMaxRetries() int {
	return b.maxRetries
}

// ShouldRetry Determines whether the task should be retried on error
func (b *BaseTask) ShouldRetry(err error) bool {
	// Retry only for temporary errors (not for access errors)
	if os.IsPermission(err) {
		return false
	}
	// Check another type of errors
	if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		return false
	}

	return true
}
