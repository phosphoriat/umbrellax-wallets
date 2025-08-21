package tasks

import (
	"context"
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

	return nil
}
