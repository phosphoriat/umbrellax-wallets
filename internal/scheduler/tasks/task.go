package tasks

import (
	"context"
	"errors"
	"time"
)

type BaseTask struct {
	name        string
	description string
	timeout     time.Duration
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
	// Do not retry when context canceled or timeout exceeded
	if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		return false
	}

	return true
}
