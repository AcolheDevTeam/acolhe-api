// Package queue concentra a conexão com o Redis/asynq usada para enfileirar jobs
// assíncronos (exportação LGPD, lembretes, geração de PDF — spec §7).
//
// Os workers que consomem esses jobs serão implementados na Fase 6; aqui fica
// apenas o cliente produtor, injetado nos services que enfileiram tarefas.
package queue

import (
	"context"

	"github.com/hibiken/asynq"
)

// Enqueuer is the narrow producer boundary shared by services. *asynq.Client
// satisfies it; tests can inject a deterministic fake without a Redis process.
type Enqueuer interface {
	EnqueueContext(
		ctx context.Context,
		task *asynq.Task,
		opts ...asynq.Option,
	) (*asynq.TaskInfo, error)
}

// Connect monta o cliente asynq a partir da URL do Redis (ex.: "127.0.0.1:6379").
func Connect(redisAddr string) *asynq.Client {
	return asynq.NewClient(asynq.RedisClientOpt{Addr: redisAddr})
}
