package tasks

import (
	"context"
)

type WithdrawalsTask struct {
	BaseTask
}

func NewWithdrawalsTask() *WithdrawalsTask {
	return &WithdrawalsTask{
		BaseTask: BaseTask{
			maxRetries:  3,
			name:        "withdrawals",
			description: "Process withdrawals",
		},
	}
}

func (t *WithdrawalsTask) Execute(ctx context.Context) error {

	return nil
}
