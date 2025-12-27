package scheduler

import (
	"context"
	"errors"
	"fmt"
	"log"
	"sync"
	"time"

	"wallets/internal/scheduler/tasks"

	"github.com/go-co-op/gocron/v2"
	"github.com/google/uuid"
)

// Scheduler instance
type Scheduler struct {
	scheduler  gocron.Scheduler
	tasks      map[string]*tasks.TaskDefinition
	jobs       map[string]gocron.Job
	callbacks  []tasks.TaskCallback
	mu         sync.RWMutex
	ctx        context.Context
	cancel     context.CancelFunc
	location   *time.Location
	running    bool
	callbackWg sync.WaitGroup
}

// SchedulerOptions Options for create scheduler instance
type SchedulerOptions struct {
	Location         *time.Location
	GlobalJobOptions []gocron.JobOption
	SchedulerOptions []gocron.SchedulerOption
}

// Init scheduler
func Init() (*Scheduler, error) {
	// Define scheduler options
	location, err := time.LoadLocation("Europe/Moscow")
	if err != nil {
		log.Printf("Failed to load timezone: %v, using UTC", err)
		location = time.UTC
	}

	schedulerOpts := &SchedulerOptions{
		Location: location,
		SchedulerOptions: []gocron.SchedulerOption{
			gocron.WithLimitConcurrentJobs(5, gocron.LimitModeReschedule),
			gocron.WithGlobalJobOptions(
				gocron.WithEventListeners(
					gocron.BeforeJobRuns(func(jobId uuid.UUID, jobName string) {
						log.Printf("Starting job: %s [ID: %s]", jobName, jobId.String())
					}),
					gocron.AfterJobRuns(func(jobId uuid.UUID, jobName string) {
						log.Printf("Finished job: %s [ID: %s]", jobName, jobId.String())
					}),
					gocron.AfterJobRunsWithError(func(jobId uuid.UUID, jobName string, err error) {
						log.Printf("Job failed: %s [ID: %s] - %v", jobName, jobId.String(), err)
					}),
				),
			),
		},
	}

	// Create scheduler instance
	schedulerInstance, err := NewScheduler(schedulerOpts)
	if err != nil {
		return nil, fmt.Errorf("failed to create scheduler: %w", err)
	}

	// Scheduler level callback (after every task execution)
	schedulerInstance.AddCallback(func(ctx context.Context, execution *tasks.TaskExecution) {
		// Deferred panic protection
		defer func() {
			if r := recover(); r != nil {
				log.Printf("[ERROR] Scheduler logger callback failed: %v", r)
			}
		}()

		// Check context cancellation
		select {
		case <-ctx.Done():
			return
		default:
		}

		status := "✓"
		if !execution.Success {
			status = "✗"
		}
		log.Printf("%s Task: %s | Duration: %v | Attempts: %d",
			status, execution.TaskName, execution.Duration, execution.RetryCount+1)
	})

	// Register all enabled tasks
	taskDefs := tasks.GetRegistry().GetAllTaskDefinitions()
	for _, def := range taskDefs {
		if err := schedulerInstance.AddTaskDefinition(def); err != nil {
			return nil, fmt.Errorf("failed to add task %s: %w", def.Task.GetName(), err)
		}
	}

	// Start scheduler
	if err := schedulerInstance.Start(); err != nil {
		return nil, fmt.Errorf("failed to start scheduler: %w", err)
	}
	log.Printf("Scheduler is running in %s timezone with %d tasks",
		location.String(), len(taskDefs))

	return schedulerInstance, nil
}

// NewScheduler Create new Scheduler instance
func NewScheduler(opts *SchedulerOptions) (*Scheduler, error) {
	// Basic options
	if opts == nil {
		opts = &SchedulerOptions{}
	}
	if opts.Location == nil {
		opts.Location = time.UTC
	}

	// Define context
	ctx, cancel := context.WithCancel(context.Background())

	// Define scheduler options
	schedulerOpts := append([]gocron.SchedulerOption{
		gocron.WithLocation(opts.Location),
		gocron.WithLogger(gocron.NewLogger(gocron.LogLevelInfo)),
	}, opts.SchedulerOptions...)

	// Create new gocron instance
	gocronInstance, err := gocron.NewScheduler(schedulerOpts...)
	if err != nil {
		cancel()
		return nil, fmt.Errorf("failed to create gocron scheduler: %w", err)
	}

	return &Scheduler{
		scheduler: gocronInstance,
		tasks:     make(map[string]*tasks.TaskDefinition),
		jobs:      make(map[string]gocron.Job),
		ctx:       ctx,
		cancel:    cancel,
		location:  opts.Location,
	}, nil
}

// Start scheduler
func (s *Scheduler) Start() error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.running {
		return fmt.Errorf("scheduler is already running")
	}

	log.Println("Starting scheduler...")
	s.scheduler.Start()
	s.running = true

	log.Printf("Scheduler started with %d tasks", len(s.tasks))
	return nil
}

// Shutdown Gracefully scheduler shutdown
func (s *Scheduler) Shutdown() error {
	s.mu.Lock()
	if !s.running {
		s.mu.Unlock()
		return nil
	}
	s.running = false
	s.mu.Unlock()

	log.Println("Shutting down scheduler...")

	// Cancel our internal context, that all tasks receive a stop signal
	s.cancel()

	// Graceful stop the scheduler
	err := s.scheduler.Shutdown()

	// Wait for completion of all callback goroutines
	log.Println("Waiting for callbacks to complete...")
	s.callbackWg.Wait()

	log.Println("Scheduler stopped")
	return err
}

// AddTaskDefinition Add task with schedule
func (s *Scheduler) AddTaskDefinition(taskDefinition *tasks.TaskDefinition) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	task := taskDefinition.Task

	if !taskDefinition.Enabled {
		log.Printf("Task %s is disabled, skipping", task.GetName())
		return nil
	}

	if _, exists := s.tasks[task.GetName()]; exists {
		return fmt.Errorf("task %s already exists", task.GetName())
	}

	job, err := s.createJob(taskDefinition)
	if err != nil {
		return fmt.Errorf("failed to create job for task %s: %w", task.GetName(), err)
	}

	s.tasks[task.GetName()] = taskDefinition
	s.jobs[task.GetName()] = job

	log.Printf("Added task: %s", task.GetName())
	return nil
}

// AddCallback Add callback for results
func (s *Scheduler) AddCallback(callback tasks.TaskCallback) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.callbacks = append(s.callbacks, callback)
}

// @private createJob Create job from schedule
func (s *Scheduler) createJob(taskDefinition *tasks.TaskDefinition) (gocron.Job, error) {
	// Get task and schedule instances
	task := taskDefinition.Task
	schedule := taskDefinition.Schedule

	// Basic job options
	jobOpts := []gocron.JobOption{
		gocron.WithName(task.GetName()),
	}

	// Add SkipIfStillRunning option, if exists in schedule
	if schedule.SkipIfStillRunning {
		jobOpts = append(jobOpts, gocron.WithSingletonMode(gocron.LimitModeReschedule))
	}
	// Add start and stop options, if exists in schedule
	if schedule.StartAt != nil {
		jobOpts = append(jobOpts, gocron.WithStartAt(gocron.WithStartDateTime(*schedule.StartAt)))
	}
	if schedule.StopAt != nil {
		jobOpts = append(jobOpts, gocron.WithStopAt(gocron.WithStopDateTime(*schedule.StopAt)))
	}

	// Wrapper for task execution
	jobFunc := gocron.NewTask(s.wrapTask(task))

	// Get job definition
	var jobDefinition gocron.JobDefinition
	switch schedule.Type {
	case tasks.ScheduleTypeInterval:
		duration, parseErr := time.ParseDuration(schedule.Interval)
		if parseErr != nil {
			return nil, fmt.Errorf("invalid interval: %w", parseErr)
		}
		jobDefinition = gocron.DurationJob(duration)

	case tasks.ScheduleTypeDaily:
		if schedule.At != "" {
			hour, minute, second, err := parseTimeString(schedule.At)
			if err != nil {
				return nil, fmt.Errorf("invalid time format %s: %w", schedule.At, err)
			}

			atTime := gocron.NewAtTime(hour, minute, second)
			jobDefinition = gocron.DailyJob(1, gocron.NewAtTimes(atTime))

		} else {
			return nil, fmt.Errorf("invalid time format: %s", schedule.At)
		}

	case tasks.ScheduleTypeWeekly:
		var atTimes gocron.AtTimes
		if schedule.At != "" {
			hour, minute, second, err := parseTimeString(schedule.At)
			if err != nil {
				return nil, fmt.Errorf("invalid time format %s: %w", schedule.At, err)
			}

			atTime := gocron.NewAtTime(hour, minute, second)
			atTimes = gocron.NewAtTimes(atTime)

		} else {
			return nil, fmt.Errorf("invalid time format: %s", schedule.At)
		}

		// Use week days from schedule or Monday by default
		var weekdays gocron.Weekdays
		if len(schedule.Days) > 0 {
			weekdays = gocron.NewWeekdays(schedule.Days[0], schedule.Days[1:]...)
		} else {
			weekdays = gocron.NewWeekdays(time.Monday)
		}
		jobDefinition = gocron.WeeklyJob(1, weekdays, atTimes)

	case tasks.ScheduleTypeMonthly:
		var atTimes gocron.AtTimes
		if schedule.At != "" {
			hour, minute, second, err := parseTimeString(schedule.At)
			if err != nil {
				return nil, fmt.Errorf("invalid time format %s: %w", schedule.At, err)
			}

			atTime := gocron.NewAtTime(hour, minute, second)
			atTimes = gocron.NewAtTimes(atTime)

		} else {
			return nil, fmt.Errorf("invalid time format: %s", schedule.At)
		}

		// Use month days from schedule or 1st day of the month by default
		var monthDays gocron.DaysOfTheMonth
		if len(schedule.DaysOfMonth) > 0 {
			monthDays = gocron.NewDaysOfTheMonth(schedule.DaysOfMonth[0], schedule.DaysOfMonth[1:]...)
		} else {
			monthDays = gocron.NewDaysOfTheMonth(1)
		}
		jobDefinition = gocron.MonthlyJob(1, monthDays, atTimes)

	case tasks.ScheduleTypeCron:
		if schedule.Cron == "" {
			return nil, fmt.Errorf("cron expression is required for cron schedule")
		}
		jobDefinition = gocron.CronJob(schedule.Cron, false)

	case tasks.ScheduleTypeOnce:
		if schedule.StartAt == nil {
			return nil, fmt.Errorf("start_at is required for once schedule")
		}
		if schedule.StartAt.Before(time.Now()) {
			return nil, fmt.Errorf("start_at must be in the future")
		}
		jobDefinition = gocron.OneTimeJob(gocron.OneTimeJobStartDateTime(*schedule.StartAt))

	default:
		return nil, fmt.Errorf("unknown schedule type: %s", schedule.Type)
	}

	return s.scheduler.NewJob(jobDefinition, jobFunc, jobOpts...)
}

// @private wrapTask Wrap task for execution
func (s *Scheduler) wrapTask(task tasks.ScheduledTask) func(context.Context) error {
	return func(ctx context.Context) error {
		return s.executeTask(ctx, task)
	}
}

// @private executeTask Execute task with error handling and retry logic
func (s *Scheduler) executeTask(ctx context.Context, task tasks.ScheduledTask) error {
	execution := &tasks.TaskExecution{
		ID:         uuid.New(),
		TaskName:   task.GetName(),
		StartTime:  time.Now(),
		Context:    make(map[string]any),
		RetryCount: 0,
	}
	log.Printf("Executing task: %s [ID: %s]", task.GetName(), execution.ID.String())

	var lastErr error
	maxRetries := task.GetMaxRetries()

	for attempt := 0; attempt <= maxRetries; attempt++ {
		// Increase retries count
		execution.RetryCount = attempt

		// Create context with timeout for each attempt
		timeout := task.GetTimeout()
		taskCtx, cancel := context.WithTimeout(ctx, timeout)

		// Execute task with timeout
		err := s.runTask(taskCtx, task)
		cancel()

		// Execution successful
		if err == nil {
			// Save execution data
			now := time.Now()
			execution.Success = true
			execution.EndTime = &now
			execution.Duration = execution.EndTime.Sub(execution.StartTime)

			// Run scheduler level callbacks
			s.runSchedulerCallbacks(execution)
			// Run task level callback
			s.runTaskCallback(task.GetName(), execution)

			return nil
		}

		// Process timeout error
		if errors.Is(err, context.DeadlineExceeded) {
			log.Printf("Task %s timed out after %v on attempt %d", task.GetName(), timeout, attempt+1)
		}

		// Execution failed
		lastErr = err
		// Check, if it should retry
		if attempt < maxRetries && task.ShouldRetry(err) {
			log.Printf("Task %s failed on attempt %d, retrying", task.GetName(), attempt+1)

			// Linear delay
			backoff := time.Second
			select {
			case <-time.After(backoff):
				continue
			case <-ctx.Done():
				return ctx.Err()
			}
		} else {
			break
		}
	}

	// All retries are failed
	now := time.Now()
	execution.Success = false
	execution.Error = lastErr
	execution.EndTime = &now
	execution.Duration = execution.EndTime.Sub(execution.StartTime)

	log.Printf("Task %s failed after %d attempts: %v (duration: %v)",
		task.GetName(), maxRetries+1, lastErr, execution.Duration)

	// Run scheduler level callbacks
	s.runSchedulerCallbacks(execution)
	return lastErr
}

// @private runTask Run task with panic handling
func (s *Scheduler) runTask(ctx context.Context, task tasks.ScheduledTask) (err error) {
	// Deferred panic protection
	defer func() {
		if r := recover(); r != nil {
			err = fmt.Errorf("task error: %v", r)
		}
	}()

	// Check cancellation
	if ctx.Err() != nil {
		return ctx.Err()
	}

	// Execute task
	return task.Execute(ctx)
}

// @private runSchedulerCallbacks Run scheduler level callbacks
func (s *Scheduler) runSchedulerCallbacks(execution *tasks.TaskExecution) {
	if len(s.callbacks) == 0 {
		return
	}

	// Do not run callbacks after shutdown
	s.mu.RLock()
	if !s.running {
		s.mu.RUnlock()
		return
	}

	// Copy callbacks slice under lock and unlock after (Protection of slice modification)
	callbacksCopy := make([]tasks.TaskCallback, len(s.callbacks))
	copy(callbacksCopy, s.callbacks)
	s.mu.RUnlock()

	// Run goroutines without lock
	for index, callback := range callbacksCopy {
		// Increase counter before starting
		s.callbackWg.Add(1)

		go func() {
			// Reduce counter at completion
			defer s.callbackWg.Done()
			// Deferred panic protection
			defer func() {
				if r := recover(); r != nil {
					log.Printf("[ERROR] Callback %d failed for task %s: %v",
						index, execution.TaskName, r)
				}
			}()

			// Set 5 second timeout for callback execution
			ctx, cancel := context.WithTimeout(s.ctx, 5*time.Second)
			defer cancel()

			callback(ctx, execution)
		}()
	}
}

// @private runTaskCallback Run task level callback
func (s *Scheduler) runTaskCallback(taskName string, execution *tasks.TaskExecution) {
	s.mu.RLock()
	taskDef, exists := s.tasks[taskName]
	s.mu.RUnlock()

	if !exists || taskDef.Callback == nil {
		return
	}

	// Increase counter before starting
	s.callbackWg.Add(1)
	go func() {
		// Reduce counter at completion
		defer s.callbackWg.Done()
		// Deferred panic protection
		defer func() {
			if r := recover(); r != nil {
				log.Printf("[ERROR] Task callback failed for %s: %v", taskName, r)
			}
		}()

		// Set 5 second timeout for callback execution
		ctx, cancel := context.WithTimeout(s.ctx, 5*time.Second)
		defer cancel()

		taskDef.Callback(ctx, execution)
	}()
}

// @private parseTimeString Parse time string to format "15:04" or "15:04:05"
func parseTimeString(timeStr string) (hour, minute, second uint, err error) {
	// Trying to parse with seconds
	if t, parseErr := time.Parse("15:04:05", timeStr); parseErr == nil {
		return uint(t.Hour()), uint(t.Minute()), uint(t.Second()), nil
	}

	// Trying to parse without seconds
	if t, parseErr := time.Parse("15:04", timeStr); parseErr == nil {
		return uint(t.Hour()), uint(t.Minute()), 0, nil
	}

	// Return error, if still not success
	return 0, 0, 0, fmt.Errorf("invalid time format %s, expected HH:MM or HH:MM:SS", timeStr)
}
