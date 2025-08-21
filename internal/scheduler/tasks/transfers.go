package tasks

import (
	"context"
)

type TransfersTask struct {
	BaseTask
}

func NewTransfersTask() *TransfersTask {
	return &TransfersTask{
		BaseTask: BaseTask{
			maxRetries:  3,
			name:        "transfers",
			description: "Process auto transfers",
		},
	}
}

func (t *TransfersTask) Execute(ctx context.Context) error {

	return nil
}
