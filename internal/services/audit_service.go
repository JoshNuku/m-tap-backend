package services

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"

	"github.com/jackc/pgx/v5/pgxpool"
)

// AuditService handles append-only compliance, security, and forensic event logging.
type AuditService interface {
	Log(ctx context.Context, entityType, entityID, action, actorType string, actorID *string, metadata map[string]interface{}) error
}

type auditService struct {
	db *pgxpool.Pool
}

func NewAuditService(db *pgxpool.Pool) AuditService {
	return &auditService{db: db}
}

func (s *auditService) Log(ctx context.Context, entityType, entityID, action, actorType string, actorID *string, metadata map[string]interface{}) error {
	metaJSON, err := json.Marshal(metadata)
	if err != nil {
		slog.Error("Failed to marshal audit metadata", "error", err)
		metaJSON = []byte("{}")
	}

	query := `
		INSERT INTO audit_logs (entity_type, entity_id, action, actor_type, actor_id, metadata, created_at)
		VALUES ($1, $2, $3, $4, $5, $6, NOW())
	`

	_, err = s.db.Exec(ctx, query, entityType, entityID, action, actorType, actorID, metaJSON)
	if err != nil {
		slog.Error("CRITICAL: Failed to write to append-only audit ledger",
			"error", err,
			"entity_type", entityType,
			"entity_id", entityID,
			"action", action,
		)
		return fmt.Errorf("failed to persist audit log: %w", err)
	}

	slog.Info("Audit log persisted",
		"entity_type", entityType,
		"entity_id", entityID,
		"action", action,
		"actor_type", actorType,
	)

	return nil
}

