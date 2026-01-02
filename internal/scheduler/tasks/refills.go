package tasks

import (
	"context"
	"time"

	"wallets/internal/repository"
	"wallets/internal/services/chains"
)

type RefillsTask struct {
	BaseTask
}

func NewRefillsTask() *RefillsTask {
	return &RefillsTask{
		BaseTask: BaseTask{
			maxRetries:  3,
			timeout:     10 * time.Minute,
			name:        "refills",
			description: "Process refills",
		},
	}
}

func (t *RefillsTask) Execute(ctx context.Context) error {
	// Init repositories
	actionRepo := repository.NewActionRepository()

	return chains.ProcessInboundTransactions(ctx, actionRepo)
}
