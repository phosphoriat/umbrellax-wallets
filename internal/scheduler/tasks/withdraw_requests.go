package tasks

import (
	"context"
)

type WithdrawRequestTask struct {
	BaseTask
}

func NewWithdrawRequestTask() *WithdrawRequestTask {
	return &WithdrawRequestTask{
		BaseTask: BaseTask{
			maxRetries:  3,
			name:        "refills",
			description: "Process refills",
		},
	}
}

func (t *WithdrawRequestTask) Execute(ctx context.Context) error {

	return nil
}
