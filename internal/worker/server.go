package worker

import (
	"context"
	"log/slog"
	"strings"

	"github.com/hibiken/asynq"
	"github.com/boronlabs/m-tap-backend/internal/config"
	"github.com/boronlabs/m-tap-backend/internal/tasks"
)

// AsynqRedisOpt converts application config into asynq.RedisConnOpt.
func AsynqRedisOpt(cfg *config.Config) asynq.RedisConnOpt {
	if strings.HasPrefix(cfg.RedisURL, "redis://") || strings.HasPrefix(cfg.RedisURL, "rediss://") {
		opt, err := asynq.ParseRedisURI(cfg.RedisURL)
		if err == nil {
			return opt
		}
	}
	return asynq.RedisClientOpt{
		Addr:     cfg.RedisURL,
		Password: cfg.RedisPassword,
		DB:       0,
	}
}

// NewWorkerServer initializes the background queue consumer.
func NewWorkerServer(cfg *config.Config, paymentProcessor *PaymentWebhookProcessor, smsProcessor *SMSProcessor) (*asynq.Server, *asynq.ServeMux) {
	redisOpt := AsynqRedisOpt(cfg)

	srv := asynq.NewServer(
		redisOpt,
		asynq.Config{
			Concurrency: 10,
			Queues: map[string]int{
				tasks.QueuePayments:      6,
				tasks.QueueNotifications: 3,
				tasks.QueueDefault:       1,
			},
			ErrorHandler: asynq.ErrorHandlerFunc(func(ctx context.Context, task *asynq.Task, err error) {
				slog.Error("Asynq task processing failed",
					"task_type", task.Type(),
					"error", err,
				)
			}),
		},
	)

	mux := asynq.NewServeMux()
	mux.Handle(tasks.TypePaystackWebhookProcess, paymentProcessor)
	mux.Handle(tasks.TypeSendOTP, smsProcessor)

	return srv, mux
}
