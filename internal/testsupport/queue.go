package testsupport

import (
	"context"

	"github.com/hibiken/asynq"
)

type FakeEnqueuer struct {
	Task  *asynq.Task
	Err   error
	Calls int
}

func (queue *FakeEnqueuer) EnqueueContext(
	_ context.Context,
	task *asynq.Task,
	_ ...asynq.Option,
) (*asynq.TaskInfo, error) {
	queue.Calls++
	queue.Task = task
	if queue.Err != nil {
		return nil, queue.Err
	}
	return &asynq.TaskInfo{ID: "fake-task", Type: task.Type()}, nil
}
