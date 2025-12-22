package tasks

import (
	"context"
	"log"
	"time"

	"wallets/internal/services/chains"
)

type RefillsTask struct {
	BaseTask
}

func NewRefillsTask() *RefillsTask {
	return &RefillsTask{
		BaseTask: BaseTask{
			maxRetries:  3,
			timeout:     time.Second * 5,
			name:        "refills",
			description: "Process refills",
		},
	}
}

func (t *RefillsTask) Execute(ctx context.Context) error {
	err := chains.ProcessInboundTransactions(ctx)
	if err != nil {
		log.Printf("Processing inbound transactions error: %v", err)
		return nil
	}

	return nil
}
