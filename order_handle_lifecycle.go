package arca

import "context"

// LifecycleUpdates follows this handle's original operation and batch leg.
// Lost HTTP responses recover by the already chosen placement path; this
// reader never calls placement. Stop the watch when no longer interested.
func (h *OrderHandle) LifecycleUpdates(ctx context.Context) (*OrderLifecycleWatch, error) {
	if h.deps.watchLifecycle == nil {
		return nil, newArcaError("ORDER_LIFECYCLE_UNAVAILABLE", "Original order lifecycle is unavailable", "")
	}
	ref := OriginalOrderReference{ObjectID: h.objectID, OperationPath: h.placementPath, Leg: h.deps.lifecycleLeg}
	response, err := h.Submitted(ctx)
	if ctx.Err() != nil {
		return nil, ctx.Err()
	}
	if err == nil && response.Operation.ID != "" {
		ref.OperationID, ref.OperationPath = response.Operation.ID, ""
	}
	return h.deps.watchLifecycle(ctx, ref)
}

func (h *OrderHandle) waitLifecycleReceipt(ctx context.Context, accounting bool) (OrderExecutionReceipt, error) {
	if h.deps.releaseExecution != nil {
		defer h.deps.releaseExecution()
	}
	watch, err := h.LifecycleUpdates(ctx)
	if err != nil {
		return OrderExecutionReceipt{}, err
	}
	defer watch.Stop()
	for {
		select {
		case <-ctx.Done():
			return OrderExecutionReceipt{}, ctx.Err()
		case update, ok := <-watch.Updates:
			if !ok {
				return OrderExecutionReceipt{}, newArcaError("ORDER_LIFECYCLE_UNAVAILABLE", "Original order stream ended", "")
			}
			if update.Unavailable {
				if !update.Recoverable {
					return OrderExecutionReceipt{}, newArcaError("ORDER_LIFECYCLE_UNAVAILABLE", update.Reason, "")
				}
				continue
			}
			if update.Lifecycle == nil {
				continue
			}
			if receipt := update.Lifecycle.ExecutionReceipt; receipt != nil && (!accounting || receipt.FillsComplete) {
				return *receipt, nil
			}
		}
	}
}

// streamLifecycleFills consumes only the server's canonical full snapshots.
// Stable IDs deduplicate replay; quantities and completion stay server-owned.
func (h *OrderHandle) streamLifecycleFills(ctx context.Context, callback func(SimFill)) error {
	if h.deps.releaseExecution != nil {
		defer h.deps.releaseExecution()
	}
	watch, err := h.LifecycleUpdates(ctx)
	if err != nil {
		return err
	}
	defer watch.Stop()
	seen := map[string]bool{}
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case update, ok := <-watch.Updates:
			if !ok {
				return newArcaError("ORDER_LIFECYCLE_UNAVAILABLE", "Original order stream ended", "")
			}
			if update.Unavailable {
				if !update.Recoverable {
					return newArcaError("ORDER_LIFECYCLE_UNAVAILABLE", update.Reason, "")
				}
				continue
			}
			v := update.Lifecycle
			if v == nil {
				continue
			}
			if v.CommittedFills == nil {
				return newArcaError("ORDER_FILLS_UNAVAILABLE", "Original order has no canonical fill snapshot", "")
			}
			for _, f := range v.CommittedFills {
				if ctx.Err() != nil {
					return ctx.Err()
				}
				if seen[f.ID] {
					continue
				}
				seen[f.ID] = true
				callback(f.SimFill)
			}
			if v.AccountingComplete && !v.RecoveryRequired {
				return nil
			}
		}
	}
}
