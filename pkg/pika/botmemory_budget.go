// PIKA-V3: BotMemory budget helper for Telemetry.

package pika

import (
	"context"
	"fmt"
)

// QueryTodayCostUSD returns the total cost_usd spent today
// from the request_log table.
// PIKA-V3: Used by Telemetry for daily budget tracking.
func (bm *BotMemory) QueryTodayCostUSD(
	ctx context.Context,
) (float64, error) {
	var total float64
	err := bm.db.QueryRowContext(ctx,
		`SELECT COALESCE(SUM(cost_usd), 0)
		 FROM request_log
		 WHERE date(ts) = date('now')`,
	).Scan(&total)
	if err != nil {
		return 0, fmt.Errorf(
			"pika/botmemory: query today cost: %w", err,
		)
	}
	return total, nil
}
