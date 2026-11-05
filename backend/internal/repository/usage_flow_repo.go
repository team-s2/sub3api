package repository

import (
	"context"
	"database/sql"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/service"
)

type usageFlowRepository struct {
	db *sql.DB
}

// NewUsageFlowRepository creates the reader behind the bot's flow image.
func NewUsageFlowRepository(db *sql.DB) service.UsageFlowReader {
	return &usageFlowRepository{db: db}
}

const usageFlowQuery = `
SELECT ul.user_id,
       COALESCE(NULLIF(u.username, ''), u.email, ''),
       ul.model,
       ul.account_id,
       COALESCE(a.name, ''),
       COUNT(*),
       COALESCE(SUM(ul.total_cost), 0),
       COALESCE(SUM(ul.input_tokens + ul.output_tokens + ul.cache_creation_tokens + ul.cache_read_tokens), 0),
       COALESCE(SUM(ul.input_tokens + ul.cache_creation_tokens + ul.cache_read_tokens), 0),
       COALESCE(SUM(ul.cache_read_tokens), 0)
FROM usage_logs ul
LEFT JOIN users u ON u.id = ul.user_id
LEFT JOIN accounts a ON a.id = ul.account_id
WHERE ul.created_at >= $1
GROUP BY ul.user_id, u.username, u.email, ul.model, ul.account_id, a.name`

func (r *usageFlowRepository) ListUsageFlow(ctx context.Context, since time.Time) ([]service.UsageFlowRow, error) {
	rows, err := r.db.QueryContext(ctx, usageFlowQuery, since)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	var flow []service.UsageFlowRow
	for rows.Next() {
		var row service.UsageFlowRow
		if err := rows.Scan(&row.UserID, &row.UserName, &row.Model, &row.AccountID, &row.AccountName,
			&row.Requests, &row.CostUSD, &row.TotalTokens, &row.InputTokens, &row.CacheTokens); err != nil {
			return nil, err
		}
		flow = append(flow, row)
	}
	return flow, rows.Err()
}
