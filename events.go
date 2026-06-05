package arca

// EventType enumerates every event type emitted by the Arca WebSocket stream.
type EventType = string

const (
	EventOperationCreated EventType = "operation.created"
	EventOperationUpdated EventType = "operation.updated"
	EventEventCreated     EventType = "event.created"
	EventObjectCreated    EventType = "object.created"
	EventObjectUpdated    EventType = "object.updated"
	EventObjectDeleted    EventType = "object.deleted"
	EventBalanceUpdated   EventType = "balance.updated"
	EventExchangeUpdated  EventType = "exchange.updated"
	// EventFillPreviewed is Phase 1 of two-phase fill delivery: the instant,
	// incomplete venue-level fill echo. EventFillRecorded (Phase 2) follows with
	// the authoritative record; the SDK merges the pair by correlationId.
	EventFillPreviewed EventType = "fill.previewed"
	// Deprecated: renamed to EventFillPreviewed. The wire value changed from
	// "exchange.fill" to "fill.previewed" pre-launch; this alias resolves to the
	// new value and will be removed in a future release.
	EventExchangeFill         EventType = "fill.previewed"
	EventFillRecorded         EventType = "fill.recorded"
	EventExchangeFunding      EventType = "exchange.funding"
	EventAggregationUpdated   EventType = "aggregation.updated"
	EventMidsUpdated          EventType = "mids.updated"
	EventCandleClosed         EventType = "candle.closed"
	EventCandleUpdated        EventType = "candle.updated"
	EventTradeExecuted        EventType = "trade.executed"
	EventTradesBatch          EventType = "trades.batch"
	EventObjectValuation      EventType = "object.valuation"
	EventTwapStarted          EventType = "twap.started"
	EventTwapProgress         EventType = "twap.progress"
	EventTwapCompleted        EventType = "twap.completed"
	EventTwapCancelled        EventType = "twap.cancelled"
	EventTwapFailed           EventType = "twap.failed"
	EventRealmCreated         EventType = "realm.created"
	EventAgentText            EventType = "agent.text"
	EventAgentToolUse         EventType = "agent.tool_use"
	EventAgentPlan            EventType = "agent.plan"
	EventAgentConversationLog EventType = "agent.conversation_log"
	EventAgentDone            EventType = "agent.done"
	EventAgentStepUpdated     EventType = "agent.step_updated"
	EventAgentExecutionDone   EventType = "agent.execution_done"
)

// Channel groups for WebSocket subscriptions.
const (
	ChannelOperations  = "operations"
	ChannelBalances    = "balances"
	ChannelExchange    = "exchange"
	ChannelObjects     = "objects"
	ChannelEvents      = "events"
	ChannelAggregation = "aggregation"
	ChannelAgent       = "agent"
)

// RealmEvent is a realm event delivered over the WebSocket stream. Fields are
// populated depending on Type; consult the field documentation on EventType.
type RealmEvent struct {
	RealmID    string `json:"realmId,omitempty"`
	Type       string `json:"type"`
	EntityID   string `json:"entityId,omitempty"`
	EntityPath string `json:"entityPath,omitempty"`

	Aggregation *PathAggregation `json:"aggregation,omitempty"`
	Summary     *ExplorerSummary `json:"summary,omitempty"`
	Operation   *Operation       `json:"operation,omitempty"`
	Event       *ArcaEvent       `json:"event,omitempty"`
	Object      *ArcaObject      `json:"object,omitempty"`

	Mids           map[string]string `json:"mids,omitempty"`
	MarketDataAsOf string            `json:"marketDataAsOf,omitempty"`

	ExchangeState *ExchangeState   `json:"exchangeState,omitempty"`
	Valuation     *ObjectValuation `json:"valuation,omitempty"`

	Balances     []ArcaBalance     `json:"balances,omitempty"`
	HeldOutbound []ReservedBalance `json:"heldOutbound,omitempty"`
	HeldInbound  []ReservedBalance `json:"heldInbound,omitempty"`

	Path    string `json:"path,omitempty"`
	WatchID string `json:"watchId,omitempty"`

	Fill    *SimFill        `json:"fill,omitempty"`
	Funding *FundingPayment `json:"funding,omitempty"`
	Realm   *Realm          `json:"realm,omitempty"`

	Market   string         `json:"market,omitempty"`
	Interval CandleInterval `json:"interval,omitempty"`
	Candle   *Candle        `json:"candle,omitempty"`
	Trade    *MarketTrade   `json:"trade,omitempty"`

	DriftCorrected bool `json:"driftCorrected,omitempty"`

	// TWAP fields
	Twap             *Twap  `json:"twap,omitempty"`
	TwapID           string `json:"twapId,omitempty"`
	ExecutedSize     string `json:"executedSize,omitempty"`
	ExecutedNotional string `json:"executedNotional,omitempty"`
	SliceCount       int    `json:"sliceCount,omitempty"`
	FilledSlices     int    `json:"filledSlices,omitempty"`
	FailedSlices     int    `json:"failedSlices,omitempty"`
	LastSliceStatus  string `json:"lastSliceStatus,omitempty"`

	// Envelope (Convergent Event Spine)
	EventID       string `json:"eventId,omitempty"`
	CorrelationID string `json:"correlationId,omitempty"`
	Sequence      int64  `json:"sequence,omitempty"`
	Timestamp     string `json:"timestamp,omitempty"`
	DeliverySeq   int64  `json:"deliverySeq,omitempty"`
}
