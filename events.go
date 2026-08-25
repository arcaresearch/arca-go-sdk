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
	// EventDepositDetected is money seen arriving at a watched deposit
	// address, before it has been swept into the boundary and become
	// balance. It is the honest early signal for "funds are on their way";
	// EventBalanceUpdated is still what says they landed.
	EventDepositDetected EventType = "deposit.detected"
	EventExchangeUpdated EventType = "exchange.updated"
	// EventExchangeProvisioned fires when an exchange arca's venue account
	// exists and its metadata is stamped — the point at which the object stops
	// answering 503 EXCHANGE_PROVISIONING. It does NOT always mean the account
	// can trade: on a cosign-armed boundary the trading agent still needs the
	// user's co-signature, which ExchangeProvisioning.CosignRequired reports and
	// EventExchangeReady marks.
	EventExchangeProvisioned EventType = "exchange.provisioned"
	// EventExchangeReady fires when the trading agent is registered on chain and
	// the account can actually trade. On an unarmed boundary it follows
	// EventExchangeProvisioned immediately; on an armed one it waits for the
	// user's co-signed agent grant, which may be minutes or days. Treat the gap
	// as waiting on the user, not as a stall.
	EventExchangeReady EventType = "exchange.ready"
	// EventFillPreviewed is Phase 1 of two-phase fill delivery: the instant,
	// incomplete venue-level fill echo. EventFillRecorded (Phase 2) follows with
	// the authoritative record; the SDK merges the pair by correlationId.
	EventFillPreviewed        EventType = "fill.previewed"
	EventFillRecorded         EventType = "fill.recorded"
	EventExchangeFunding      EventType = "exchange.funding"
	EventAggregationUpdated   EventType = "aggregation.updated"
	EventMidsUpdated          EventType = "mids.updated"
	EventCandleClosed         EventType = "candle.closed"
	EventCandleUpdated        EventType = "candle.updated"
	EventOIUpdated            EventType = "oi.updated"
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

	// Exchange is present on EventExchangeProvisioned and EventExchangeReady.
	Exchange *ExchangeProvisioning `json:"exchange,omitempty"`

	Balances     []ArcaBalance     `json:"balances,omitempty"`
	HeldOutbound []ReservedBalance `json:"heldOutbound,omitempty"`
	HeldInbound  []ReservedBalance `json:"heldInbound,omitempty"`

	Deposit *DetectedDeposit `json:"deposit,omitempty"`

	Path    string `json:"path,omitempty"`
	WatchID string `json:"watchId,omitempty"`

	Fill    *SimFill        `json:"fill,omitempty"`
	Funding *FundingPayment `json:"funding,omitempty"`
	Realm   *Realm          `json:"realm,omitempty"`

	Market   string         `json:"market,omitempty"`
	Interval CandleInterval `json:"interval,omitempty"`
	Candle   *Candle        `json:"candle,omitempty"`
	Bar      *OIBar         `json:"bar,omitempty"`
	IsClosed bool           `json:"isClosed,omitempty"`
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

// ExchangeProvisioning is what an exchange arca reports about its own
// provisioning, carried on EventExchangeProvisioned and EventExchangeReady.
//
// Creating an exchange arca returns as soon as the object exists, which is
// before its venue account does; until then, trading calls answer 503
// EXCHANGE_PROVISIONING. These two events are how you learn that changed
// without polling for it.
type ExchangeProvisioning struct {
	ObjectID string `json:"objectId,omitempty"`
	Path     string `json:"path,omitempty"`
	// CosignRequired reports that the boundary is cosign-armed, so the account
	// exists but cannot trade until the user co-signs the agent grant. Expect
	// EventExchangeReady once they do.
	CosignRequired bool `json:"cosignRequired,omitempty"`
	// Tradable is true when the account can actually trade. Always true on
	// EventExchangeReady.
	Tradable bool `json:"tradable,omitempty"`
	// AccountAddress is the venue account address, once stamped.
	AccountAddress string `json:"accountAddress,omitempty"`
	AgentWalletID  string `json:"agentWalletId,omitempty"`
}

// DetectedDeposit is money observed arriving at a watched deposit address.
//
// It is chain truth, not ledger truth: nothing here has been credited yet.
// Amount is the transfer's value in whole units, and Sweeping says whether
// the platform is already moving it into the boundary without further
// action from the user.
type DetectedDeposit struct {
	Address    string `json:"address,omitempty"`
	From       string `json:"from,omitempty"`
	Amount     string `json:"amount,omitempty"`
	TxHash     string `json:"txHash,omitempty"`
	Block      uint64 `json:"block,omitempty"`
	BoundaryID string `json:"boundaryId,omitempty"`
	Sweeping   bool   `json:"sweeping,omitempty"`
}
