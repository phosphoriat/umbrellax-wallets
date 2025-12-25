package tasks

import (
	"context"
	"time"

	"wallets/internal/services/chains"
)

type RefillsTask struct {
	BaseTask
}

func NewRefillsTask() *RefillsTask {
	return &RefillsTask{
		BaseTask: BaseTask{
			maxRetries:  30,
			timeout:     60 * time.Second,
			name:        "refills",
			description: "Process refills",
		},
	}
}

func (t *RefillsTask) Execute(ctx context.Context) error {
	return chains.ProcessInboundTransactions(ctx)
}
