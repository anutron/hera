package events

import (
	"context"
	"log/slog"
	"time"
)

// DefaultReconcileInterval is the fallback tick period used when a
// PeriodicReconciler is constructed with a non-positive interval. Keeps
// time.NewTicker from panicking on misconfiguration.
const DefaultReconcileInterval = 60 * time.Second

// PeriodicReconciler runs ResyncHandler.Reconcile on a timer in addition
// to the SSE-driven path. It exists as defensive belt-and-suspenders so
// a silently-missed archive event still gets caught within one tick
// without depending on substrate event-emit completeness.
type PeriodicReconciler struct {
	handler  *ResyncHandler
	interval time.Duration
	log      *slog.Logger
}

// NewPeriodicReconciler constructs a PeriodicReconciler. A non-positive
// interval is replaced with DefaultReconcileInterval.
func NewPeriodicReconciler(h *ResyncHandler, interval time.Duration, log *slog.Logger) *PeriodicReconciler {
	if log == nil {
		log = slog.Default()
	}
	if interval <= 0 {
		interval = DefaultReconcileInterval
	}
	return &PeriodicReconciler{handler: h, interval: interval, log: log}
}

// Run blocks ticking on the configured interval, calling Reconcile on
// every tick. Returns when ctx is canceled.
func (p *PeriodicReconciler) Run(ctx context.Context) {
	ticker := time.NewTicker(p.interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			if err := p.handler.Reconcile(ctx); err != nil {
				if ctx.Err() != nil {
					return
				}
				p.log.Warn("periodic reconcile failed", "err", err)
			}
		}
	}
}
