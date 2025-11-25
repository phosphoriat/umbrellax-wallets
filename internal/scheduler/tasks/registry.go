package tasks

import "sync"

// TaskRegistry struct with list of all enabled tasks
type TaskRegistry struct {
	definitions []*TaskDefinition
	once        sync.Once
}

// NewTaskRegistry Create new TaskRegistry instance
func NewTaskRegistry() *TaskRegistry {
	return &TaskRegistry{}
}

// GetAllTaskDefinitions Returns all enabled task definitions
func (r *TaskRegistry) GetAllTaskDefinitions() []*TaskDefinition {
	r.once.Do(func() {
		for _, def := range r.initializeDefinitions() {
			if def.Enabled {
				r.definitions = append(r.definitions, def)
			}
		}
	})

	return r.definitions
}

// @private initializeDefinitions Initialize all tasks
func (r *TaskRegistry) initializeDefinitions() []*TaskDefinition {
	var definitions []*TaskDefinition

	// Process withdrawal requests (every 10 seconds)
	definitions = append(definitions, NewTaskDefinition(
		NewWithdrawRequestTask(),
		TaskSchedule{
			Type:               ScheduleTypeInterval,
			Interval:           "10s",
			SkipIfStillRunning: true,
		},
		true,
	))

	// Process withdrawals (every 10 seconds)
	definitions = append(definitions, NewTaskDefinition(
		NewWithdrawalsTask(),
		TaskSchedule{
			Type:               ScheduleTypeInterval,
			Interval:           "10s",
			SkipIfStillRunning: true,
		},
		true,
	))

	// Process refills (every 10 seconds)
	definitions = append(definitions, NewTaskDefinition(
		NewRefillsTask(),
		TaskSchedule{
			Type:               ScheduleTypeInterval,
			Interval:           "10s",
			SkipIfStillRunning: true,
		},
		true,
	))

	return definitions
}
