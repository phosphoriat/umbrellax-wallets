package tasks

import (
	"context"
	"log"

	"wallets/internal/services/chains"
)

type RefillsTask struct {
	BaseTask
}

func NewRefillsTask() *RefillsTask {
	return &RefillsTask{
		BaseTask: BaseTask{
			maxRetries:  3,
			name:        "refills",
			description: "Process refills",
		},
	}
}

func (t *RefillsTask) Execute(ctx context.Context) error {
	err := chains.ProcessInboundTransactions()
	if err != nil {
		log.Printf("Processing inbound transactions error: %v", err)
		return nil
	}

	return nil
}
