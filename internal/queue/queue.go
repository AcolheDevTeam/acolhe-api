// Package queue concentra a conexão com o Redis/asynq usada para enfileirar jobs
// assíncronos (exportação LGPD, lembretes, geração de PDF — spec §7).
//
// Os workers que consomem esses jobs serão implementados na Fase 6; aqui fica
// apenas o cliente produtor, injetado nos services que enfileiram tarefas.
package queue

import "github.com/hibiken/asynq"

// Connect monta o cliente asynq a partir da URL do Redis (ex.: "127.0.0.1:6379").
func Connect(redisAddr string) *asynq.Client {
	return asynq.NewClient(asynq.RedisClientOpt{Addr: redisAddr})
}
