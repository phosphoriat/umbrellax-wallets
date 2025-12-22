package tasks

import (
	"context"
	"time"

	"github.com/google/uuid"
)

type ScheduledTask interface {
	// Execute task
	Execute(ctx context.Context) error

	// GetName Get unique task name
	GetName() string

	// GetDescription Get task description
	GetDescription() string

	// GetMaxRetries Get maximum count of retries
	GetMaxRetries() int

	// GetTimeout Get timeout for task execution
	GetTimeout() time.Duration

	// ShouldRetry Determines whether the task should be retried on error
	ShouldRetry(err error) bool
}

// TaskSchedule Task schedule description
type TaskSchedule struct {
	Type               ScheduleType
	Interval           string
	At                 string
	Cron               string
	Days               []time.Weekday
	DaysOfMonth        []int
	StartAt            *time.Time
	StopAt             *time.Time
	RunOnce            bool
	SkipIfStillRunning bool
}

// ScheduleType Schedule types list
type ScheduleType string

const (
	ScheduleTypeInterval ScheduleType = "interval"
	ScheduleTypeDaily    ScheduleType = "daily"
	ScheduleTypeWeekly   ScheduleType = "weekly"
	ScheduleTypeCron     ScheduleType = "cron"
	ScheduleTypeOnce     ScheduleType = "once"
	ScheduleTypeMonthly  ScheduleType = "monthly"
)

// TaskExecution Info about task execution
type TaskExecution struct {
	ID         uuid.UUID
	TaskName   string
	StartTime  time.Time
	EndTime    *time.Time
	Duration   time.Duration
	Success    bool
	Error      error
	RetryCount int
	Context    map[string]interface{}
}

// TaskCallback Callback function
type TaskCallback func(ctx context.Context, execution *TaskExecution)

// TaskDefinition Definition task with schedule
type TaskDefinition struct {
	ID       uuid.UUID
	Task     ScheduledTask
	Schedule TaskSchedule
	Enabled  bool
}

// NewTaskDefinition Create new TaskDefinition instance
func NewTaskDefinition(task ScheduledTask, schedule TaskSchedule, enabled bool) *TaskDefinition {
	return &TaskDefinition{
		ID:       uuid.New(),
		Task:     task,
		Schedule: schedule,
		Enabled:  enabled,
	}
}
