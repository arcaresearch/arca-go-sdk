package arca

// This file mirrors sdk/typescript/src/types.ts. Money/amount fields are
// decimal strings; market-data timestamps are Unix epoch milliseconds; all
// other timestamps are RFC3339 UTC strings. Coin identifiers are canonical
// "{exchange}:{id}" values (e.g. "hl:BTC", "hl:1:TSLA").

// ---- Realm ----

// RealmType is the legacy single-axis realm type, retained as a derived alias
// of RealmAsset (live↔production, paper↔development). Prefer Asset + Lifecycle.
type RealmType string

const (
	RealmDevelopment RealmType = "development"
	RealmProduction  RealmType = "production"
)

// RealmAsset is the value tier — the real-money risk axis. paper realms
// transact simulated funds; live realms transact real money.
type RealmAsset string

const (
	RealmAssetPaper RealmAsset = "paper"
	RealmAssetLive  RealmAsset = "live"
)

// RealmLifecycle is the durability tier, orthogonal to RealmAsset. permanent
// realms are never auto-reaped; temporary realms are disposable.
type RealmLifecycle string

const (
	RealmLifecyclePermanent RealmLifecycle = "permanent"
	RealmLifecycleTemporary RealmLifecycle = "temporary"
)

// RealmBacking is the custody backing tier. chain realms are backed by an
// on-chain custody pool (every realm today); ledger-only is a future tier.
type RealmBacking string

const (
	RealmBackingChain      RealmBacking = "chain"
	RealmBackingLedgerOnly RealmBacking = "ledger-only"
)

type RealmSettings struct {
	// DefaultApplicationFeeBps is the realm's default application fee in tenths
	// of a basis point.
	DefaultApplicationFeeBps *int `json:"defaultApplicationFeeBps,omitempty"`
	// Deprecated: use DefaultApplicationFeeBps. Kept as a read alias for one
	// release; responses still echo this field.
	DefaultBuilderFeeBps *int `json:"defaultBuilderFeeBps,omitempty"`
}

type Realm struct {
	ID    string `json:"id"`
	OrgID string `json:"orgId"`
	Name  string `json:"name"`
	Slug  string `json:"slug"`
	// Type is the legacy alias of Asset (live→production, paper→development).
	Type        RealmType      `json:"type"`
	Asset       RealmAsset     `json:"asset"`
	Lifecycle   RealmLifecycle `json:"lifecycle"`
	Backing     RealmBacking   `json:"backing"`
	Description *string        `json:"description"`
	Settings    *RealmSettings `json:"settings,omitempty"`
	ArchivedAt  *string        `json:"archivedAt,omitempty"`
	CreatedBy   *string        `json:"createdBy,omitempty"`
	CreatedAt   string         `json:"createdAt"`
	UpdatedAt   string         `json:"updatedAt"`
}

type RealmListResponse struct {
	Realms []Realm `json:"realms"`
	Total  int     `json:"total"`
}

// ---- Arca Objects ----

type ArcaObjectType string

const (
	ObjectDenominated ArcaObjectType = "denominated"
	ObjectExchange    ArcaObjectType = "exchange"
	ObjectDeposit     ArcaObjectType = "deposit"
	ObjectWithdrawal  ArcaObjectType = "withdrawal"
	ObjectEscrow      ArcaObjectType = "escrow"
	ObjectInfo        ArcaObjectType = "info"
)

type ArcaObjectStatus string

const (
	StatusActive    ArcaObjectStatus = "active"
	StatusDeleting  ArcaObjectStatus = "deleting"
	StatusDeleted   ArcaObjectStatus = "deleted"
	StatusWithdrawn ArcaObjectStatus = "withdrawn"
)

type BrowsePathKind string

const (
	BrowseFolder        BrowsePathKind = "folder"
	BrowseIsolationZone BrowsePathKind = "isolation_zone"
)

type IsolationInfo struct {
	IsBoundaryRoot   bool    `json:"isBoundaryRoot"`
	IsInsideBoundary bool    `json:"isInsideBoundary"`
	BoundaryRootPath *string `json:"boundaryRootPath,omitempty"`
	BoundaryID       *string `json:"boundaryId,omitempty"`
}

type BoundarySnapshot struct {
	BoundaryID       string  `json:"boundaryId"`
	Status           string  `json:"status"`
	LockedAt         *string `json:"lockedAt,omitempty"`
	FrozenAt         *string `json:"frozenAt,omitempty"`
	RecoveryActor    *string `json:"recoveryActor,omitempty"`
	RecoveryTxHash   *string `json:"recoveryTxHash,omitempty"`
	RecoveryArcaPath *string `json:"recoveryArcaPath,omitempty"`
}

type ArcaObject struct {
	ID           string            `json:"id"`
	RealmID      string            `json:"realmId"`
	Path         string            `json:"path"`
	DisplayName  *string           `json:"displayName,omitempty"`
	Type         ArcaObjectType    `json:"type"`
	Denomination *string           `json:"denomination"`
	Status       ArcaObjectStatus  `json:"status"`
	Metadata     *string           `json:"metadata"`
	Labels       map[string]string `json:"labels"`
	DeletedAt    *string           `json:"deletedAt"`
	WithdrawnAt  *string           `json:"withdrawnAt,omitempty"`
	SystemOwned  bool              `json:"systemOwned"`
	Isolation    *IsolationInfo    `json:"isolation,omitempty"`
	Boundary     *BoundarySnapshot `json:"boundary,omitempty"`
	CreatedAt    string            `json:"createdAt"`
	UpdatedAt    string            `json:"updatedAt"`
}

type ArcaObjectListResponse struct {
	Objects    []ArcaObject `json:"objects"`
	Total      int          `json:"total"`
	NextCursor string       `json:"nextCursor,omitempty"`
}

type BrowsePathEntry struct {
	Path       string            `json:"path"`
	Kind       BrowsePathKind    `json:"kind"`
	HasObjects bool              `json:"hasObjects"`
	IsEmpty    bool              `json:"isEmpty"`
	Isolation  *IsolationInfo    `json:"isolation,omitempty"`
	CreatedAt  *string           `json:"createdAt,omitempty"`
	Labels     map[string]string `json:"labels,omitempty"`
}

type ArcaObjectBrowseResponse struct {
	Folders          []string          `json:"folders"`
	Paths            []BrowsePathEntry `json:"paths,omitempty"`
	Objects          []ArcaObject      `json:"objects"`
	CurrentIsolation *IsolationInfo    `json:"currentIsolation,omitempty"`
	Total            int               `json:"total,omitempty"`
	NextCursor       string            `json:"nextCursor,omitempty"`
}

type FolderLabelsResponse struct {
	RealmID   string            `json:"realmId"`
	Path      string            `json:"path"`
	Labels    map[string]string `json:"labels"`
	CreatedAt *string           `json:"createdAt,omitempty"`
	UpdatedAt *string           `json:"updatedAt,omitempty"`
}

type IsolationZone struct {
	ID         string         `json:"id"`
	RealmID    string         `json:"realmId"`
	Path       string         `json:"path"`
	BoundaryID string         `json:"boundaryId"`
	CreatedAt  string         `json:"createdAt"`
	UpdatedAt  string         `json:"updatedAt"`
	Isolation  *IsolationInfo `json:"isolation,omitempty"`
}

type EnsureArcaObjectResponse struct {
	Object    ArcaObject `json:"object"`
	Operation *Operation `json:"operation"`
}

func (r EnsureArcaObjectResponse) op() *Operation      { return r.Operation }
func (r *EnsureArcaObjectResponse) setOp(o *Operation) { r.Operation = o }

type DeleteArcaObjectResponse struct {
	Object    ArcaObject `json:"object"`
	Operation *Operation `json:"operation"`
}

func (r DeleteArcaObjectResponse) op() *Operation      { return r.Operation }
func (r *DeleteArcaObjectResponse) setOp(o *Operation) { r.Operation = o }

type ArcaPositionCurrent struct {
	ID            string  `json:"id"`
	RealmID       string  `json:"realmId"`
	ArcaID        string  `json:"arcaId"`
	Market        string  `json:"market"`
	Side          string  `json:"side"`
	Size          string  `json:"size"`
	Leverage      int     `json:"leverage"`
	EntryPx       *string `json:"entryPx,omitempty"`
	MarginUsed    *string `json:"marginUsed,omitempty"`
	UnrealizedPnl *string `json:"unrealizedPnl,omitempty"`
	UpdatedAt     string  `json:"updatedAt"`
}

type ArcaObjectDetailResponse struct {
	Object           ArcaObject            `json:"object"`
	Operations       []Operation           `json:"operations"`
	Events           []ArcaEvent           `json:"events"`
	Deltas           []StateDelta          `json:"deltas"`
	Balances         []ArcaBalance         `json:"balances"`
	ReservedBalances []ReservedBalance     `json:"reservedBalances,omitempty"`
	Positions        []ArcaPositionCurrent `json:"positions,omitempty"`
}

type ReservedBalanceStatus string

const (
	ReservedHeld      ReservedBalanceStatus = "held"
	ReservedReleased  ReservedBalanceStatus = "released"
	ReservedCancelled ReservedBalanceStatus = "cancelled"
)

type ReservedBalance struct {
	ID                  string                `json:"id"`
	ArcaID              string                `json:"arcaId"`
	OperationID         string                `json:"operationId"`
	Denomination        string                `json:"denomination"`
	Amount              string                `json:"amount"`
	Status              ReservedBalanceStatus `json:"status"`
	Direction           string                `json:"direction"`
	SourceArcaPath      *string               `json:"sourceArcaPath,omitempty"`
	DestinationArcaPath *string               `json:"destinationArcaPath,omitempty"`
	CreatedAt           string                `json:"createdAt"`
	UpdatedAt           string                `json:"updatedAt"`
}

type ArcaObjectVersionsResponse struct {
	Versions []ArcaObject `json:"versions"`
}

// ---- Operations ----

type OperationType string

const (
	OpTransfer        OperationType = "transfer"
	OpCreate          OperationType = "create"
	OpDelete          OperationType = "delete"
	OpDeposit         OperationType = "deposit"
	OpWithdrawal      OperationType = "withdrawal"
	OpSwap            OperationType = "swap"
	OpOrder           OperationType = "order"
	OpFill            OperationType = "fill"
	OpCancel          OperationType = "cancel"
	OpFeeDistribution OperationType = "fee_distribution"
	OpAdjustment      OperationType = "adjustment"
	OpFunding         OperationType = "funding"
	OpVenueClose      OperationType = "venue_close"
	OpTwap            OperationType = "twap"
)

type OperationState string

const (
	OpPending   OperationState = "pending"
	OpCompleted OperationState = "completed"
	OpFailed    OperationState = "failed"
	OpExpired   OperationState = "expired"
)

type Operation struct {
	ID             string         `json:"id"`
	RealmID        string         `json:"realmId"`
	Path           string         `json:"path"`
	Type           OperationType  `json:"type"`
	State          OperationState `json:"state"`
	SourceArcaPath *string        `json:"sourceArcaPath"`
	TargetArcaPath *string        `json:"targetArcaPath"`
	Input          *string        `json:"input"`
	Outcome        *string        `json:"outcome"`
	ParsedOutcome  map[string]any `json:"parsedOutcome"`
	FailureMessage *string        `json:"failureMessage"`
	ActorType      *string        `json:"actorType"`
	ActorID        *string        `json:"actorId"`
	TokenJti       *string        `json:"tokenJti"`
	WorkflowIDs    []string       `json:"workflowIds"`
	CreatedAt      string         `json:"createdAt"`
	UpdatedAt      string         `json:"updatedAt"`
}

func (o *Operation) snapshot() OperationSnapshot {
	return OperationSnapshot{ID: o.ID, State: string(o.State), Outcome: o.Outcome, FailureMessage: o.FailureMessage}
}

type OperationListResponse struct {
	Operations []Operation `json:"operations"`
	Total      int         `json:"total"`
	NextCursor string      `json:"nextCursor,omitempty"`
}

type OperationDetailResponse struct {
	Operation Operation          `json:"operation"`
	Events    []ArcaEvent        `json:"events"`
	Deltas    []StateDelta       `json:"deltas"`
	Evidence  *OperationEvidence `json:"evidence,omitempty"`
}

type OperationEvidenceExportResponse struct {
	Version    string                    `json:"version"`
	RealmID    string                    `json:"realmId"`
	From       string                    `json:"from"`
	To         string                    `json:"to"`
	ExportedAt string                    `json:"exportedAt"`
	NextCursor *string                   `json:"nextCursor,omitempty"`
	Operations []OperationDetailResponse `json:"operations"`
}

type OperationEvidence struct {
	Version   string                     `json:"version"`
	Journal   OperationEvidenceJournal   `json:"journal"`
	Integrity OperationEvidenceIntegrity `json:"integrity"`
}

type OperationEvidenceJournal struct {
	Entries           []JournalEntryEvidence     `json:"entries"`
	Legs              []JournalLegEvidence       `json:"legs"`
	Proofs            []JournalProofEvidence     `json:"proofs"`
	ChainTransactions []ChainTransactionEvidence `json:"chainTransactions"`
}

type OperationEvidenceIntegrity struct {
	AppendOnlyHistory     bool                       `json:"appendOnlyHistory"`
	ProofUniqueness       ProofUniquenessEvidence    `json:"proofUniqueness"`
	RuntimeChecks         []IntegrityAnnotation      `json:"runtimeChecks"`
	CryptographicSequence CryptographicSequenceState `json:"cryptographicSequence"`
}

type ProofUniquenessEvidence struct {
	Enforced  bool   `json:"enforced"`
	Mechanism string `json:"mechanism"`
}

type CryptographicSequenceState struct {
	Present bool   `json:"present"`
	Note    string `json:"note"`
}

type IntegrityAnnotation struct {
	Key         string `json:"key"`
	Label       string `json:"label"`
	Description string `json:"description"`
}

type JournalEntryEvidence struct {
	ID              string  `json:"id"`
	RealmID         string  `json:"realmId"`
	OperationID     *string `json:"operationId,omitempty"`
	OpType          string  `json:"opType"`
	Authority       string  `json:"authority"`
	Status          string  `json:"status"`
	IdempotencyKey  *string `json:"idempotencyKey,omitempty"`
	RejectionReason *string `json:"rejectionReason,omitempty"`
	CreatedAt       string  `json:"createdAt"`
	CommittedAt     *string `json:"committedAt,omitempty"`
}

type JournalLegEvidence struct {
	EntryID      string  `json:"entryId"`
	ID           string  `json:"id"`
	AccountID    string  `json:"accountId"`
	Denomination string  `json:"denomination"`
	Amount       string  `json:"amount"`
	Kind         string  `json:"kind"`
	Reason       *string `json:"reason,omitempty"`
}

type JournalProofEvidence struct {
	EntryID     string `json:"entryId"`
	ID          string `json:"id"`
	ProofType   string `json:"proofType"`
	ExternalRef string `json:"externalRef"`
}

type ChainTransactionEvidence struct {
	ID            string  `json:"id"`
	RealmID       string  `json:"realmId"`
	OperationID   string  `json:"operationId"`
	TxHash        *string `json:"txHash,omitempty"`
	Chain         string  `json:"chain"`
	Direction     string  `json:"direction"`
	Status        string  `json:"status"`
	Confirmations int     `json:"confirmations"`
	BlockNumber   *int64  `json:"blockNumber,omitempty"`
	Amount        *string `json:"amount,omitempty"`
	FromAddress   *string `json:"fromAddress,omitempty"`
	GasUsed       *int64  `json:"gasUsed,omitempty"`
	GasPrice      *string `json:"gasPrice,omitempty"`
	GasCostWei    *string `json:"gasCostWei,omitempty"`
	UserOpHash    *string `json:"userOpHash,omitempty"`
	Nonce         *int64  `json:"nonce,omitempty"`
	CreatedAt     string  `json:"createdAt"`
	UpdatedAt     string  `json:"updatedAt"`
}

// ---- Events ----

type ArcaEvent struct {
	ID            string  `json:"id"`
	RealmID       string  `json:"realmId"`
	OperationID   *string `json:"operationId"`
	OperationPath *string `json:"operationPath,omitempty"`
	ArcaPath      *string `json:"arcaPath"`
	Type          string  `json:"type"`
	Path          *string `json:"path"`
	Payload       *string `json:"payload"`
	CreatedAt     string  `json:"createdAt"`
}

type EventListResponse struct {
	Events     []ArcaEvent `json:"events"`
	Total      int         `json:"total"`
	NextCursor string      `json:"nextCursor,omitempty"`
}

type EventDetailResponse struct {
	Event     ArcaEvent    `json:"event"`
	Operation *Operation   `json:"operation"`
	Deltas    []StateDelta `json:"deltas"`
}

// ---- State Deltas ----

type DeltaType string

type StateDelta struct {
	ID          string    `json:"id"`
	RealmID     string    `json:"realmId"`
	EventID     *string   `json:"eventId"`
	ArcaPath    string    `json:"arcaPath"`
	DeltaType   DeltaType `json:"deltaType"`
	BeforeValue *string   `json:"beforeValue"`
	AfterValue  *string   `json:"afterValue"`
	Internal    bool      `json:"internal,omitempty"`
	CreatedAt   string    `json:"createdAt"`
}

type StateDeltaListResponse struct {
	Deltas []StateDelta `json:"deltas"`
	Total  int          `json:"total"`
}

// ---- Balances ----

type ArcaBalance struct {
	ID           string `json:"id,omitempty"`
	ArcaID       string `json:"arcaId,omitempty"`
	Denomination string `json:"denomination"`
	Amount       string `json:"amount,omitempty"`
	Arriving     string `json:"arriving,omitempty"`
	Settled      string `json:"settled,omitempty"`
	Departing    string `json:"departing,omitempty"`
	Total        string `json:"total,omitempty"`
}

type ArcaBalanceListResponse struct {
	Balances []ArcaBalance `json:"balances"`
}

// ---- Fund / Transfer / Fees ----

type FundAccountResponse struct {
	Operation    Operation `json:"operation"`
	PoolAddress  string    `json:"poolAddress,omitempty"`
	TokenAddress string    `json:"tokenAddress,omitempty"`
	Chain        string    `json:"chain,omitempty"`
	ExpiresAt    string    `json:"expiresAt,omitempty"`
}

func (r FundAccountResponse) op() *Operation { return &r.Operation }
func (r *FundAccountResponse) setOp(o *Operation) {
	if o != nil {
		r.Operation = *o
	}
}

type DefundAccountResponse struct {
	Operation Operation `json:"operation"`
	TxHash    string    `json:"txHash,omitempty"`
}

func (r DefundAccountResponse) op() *Operation { return &r.Operation }
func (r *DefundAccountResponse) setOp(o *Operation) {
	if o != nil {
		r.Operation = *o
	}
}

type AmountDenomination struct {
	Amount       string `json:"amount"`
	Denomination string `json:"denomination"`
}

type TransferResponse struct {
	Operation Operation           `json:"operation"`
	Fee       *AmountDenomination `json:"fee,omitempty"`
}

func (r TransferResponse) op() *Operation { return &r.Operation }
func (r *TransferResponse) setOp(o *Operation) {
	if o != nil {
		r.Operation = *o
	}
}

type FeeEstimate struct {
	Fee       AmountDenomination `json:"fee"`
	NetAmount string             `json:"netAmount"`
}

// ---- Nonce / Summary ----

type NonceResponse struct {
	Nonce int64  `json:"nonce"`
	Path  string `json:"path"`
}

type ExplorerSummary struct {
	ObjectCount           int `json:"objectCount"`
	OperationCount        int `json:"operationCount"`
	EventCount            int `json:"eventCount"`
	PendingOperationCount int `json:"pendingOperationCount,omitempty"`
	ExpiredOperationCount int `json:"expiredOperationCount,omitempty"`
}

// ---- Snapshot ----

type CanonicalPosition struct {
	ID        string `json:"id"`
	RealmID   string `json:"realmId"`
	ArcaID    string `json:"arcaId"`
	Market    string `json:"market"`
	Side      string `json:"side"`
	Size      string `json:"size"`
	Leverage  int    `json:"leverage"`
	UpdatedAt string `json:"updatedAt"`
}

type SnapshotBalancesResponse struct {
	RealmID   string              `json:"realmId"`
	ArcaID    string              `json:"arcaId"`
	AsOf      string              `json:"asOf"`
	Balances  []ArcaBalance       `json:"balances"`
	Positions []CanonicalPosition `json:"positions"`
}

// ---- Aggregation / Valuation ----

type AssetBreakdown struct {
	Asset               string  `json:"asset"`
	Category            string  `json:"category"`
	Amount              string  `json:"amount"`
	Price               *string `json:"price"`
	ValueUsd            string  `json:"valueUsd"`
	WeightedAvgLeverage *string `json:"weightedAvgLeverage,omitempty"`
	AvgEntryPrice       *string `json:"avgEntryPrice,omitempty"`
}

type BalanceValue struct {
	Denomination string  `json:"denomination"`
	Amount       string  `json:"amount"`
	Price        *string `json:"price"`
	ValueUsd     string  `json:"valueUsd"`
}

type PositionValue struct {
	Coin          string  `json:"coin"`
	Market        string  `json:"market"`
	Side          string  `json:"side"`
	Size          string  `json:"size"`
	Leverage      int     `json:"leverage"`
	EntryPrice    string  `json:"entryPrice"`
	MarkPrice     *string `json:"markPrice,omitempty"`
	UnrealizedPnl *string `json:"unrealizedPnl,omitempty"`
	ValueUsd      *string `json:"valueUsd,omitempty"`
}

type ReservedValue struct {
	Denomination        string  `json:"denomination"`
	Amount              string  `json:"amount"`
	Price               *string `json:"price"`
	ValueUsd            string  `json:"valueUsd"`
	OperationID         string  `json:"operationId"`
	SourceArcaPath      *string `json:"sourceArcaPath,omitempty"`
	DestinationArcaPath *string `json:"destinationArcaPath,omitempty"`
	StartedAt           *string `json:"startedAt,omitempty"`
	InTransit           bool    `json:"inTransit,omitempty"`
}

type ObjectValuation struct {
	ObjectID         string          `json:"objectId"`
	Path             string          `json:"path"`
	Type             string          `json:"type"`
	Denomination     *string         `json:"denomination"`
	ValueUsd         string          `json:"valueUsd"`
	RealizedValue    *string         `json:"realizedValue,omitempty"`
	UnrealizedValue  *string         `json:"unrealizedValue,omitempty"`
	Balances         []BalanceValue  `json:"balances"`
	ReservedBalances []ReservedValue `json:"reservedBalances"`
	PendingInbound   []ReservedValue `json:"pendingInbound,omitempty"`
	Positions        []PositionValue `json:"positions"`
}

type PathAggregation struct {
	Prefix             string           `json:"prefix"`
	TotalEquityUsd     string           `json:"totalEquityUsd"`
	DepartingUsd       string           `json:"departingUsd"`
	ArrivingUsd        string           `json:"arrivingUsd,omitempty"`
	Breakdown          []AssetBreakdown `json:"breakdown"`
	AsOf               string           `json:"asOf,omitempty"`
	AvailableIntervals []string         `json:"availableIntervals,omitempty"`
	CumInflowsUsd      string           `json:"cumInflowsUsd,omitempty"`
	CumOutflowsUsd     string           `json:"cumOutflowsUsd,omitempty"`
}

type WatchID = string

type WatchSnapshotBalanceEntry struct {
	EntityID   string         `json:"entityId"`
	EntityPath string         `json:"entityPath,omitempty"`
	Balances   []BalanceValue `json:"balances"`
}

type WatchSnapshot struct {
	Path               string                      `json:"path"`
	Balances           []WatchSnapshotBalanceEntry `json:"balances"`
	Operations         []Operation                 `json:"operations"`
	BufferedOperations []Operation                 `json:"bufferedOperations,omitempty"`
	Exchange           *ExchangeState              `json:"exchange"`
	Valuation          *ObjectValuation            `json:"valuation"`
	Valuations         map[string]ObjectValuation  `json:"valuations,omitempty"`
	WatchID            *string                     `json:"watchId"`
}

type CreateWatchResponse struct {
	WatchID     string          `json:"watchId"`
	Aggregation PathAggregation `json:"aggregation"`
}

// AggregationSource is a watch source. Use one of NewPrefixSource,
// NewPatternSource, NewPathsSource, NewWatchSource.
type AggregationSource struct {
	Type  string   `json:"type"`
	Value string   `json:"value,omitempty"`
	Paths []string `json:"paths,omitempty"`
}

func NewPrefixSource(prefix string) AggregationSource {
	return AggregationSource{Type: "prefix", Value: prefix}
}
func NewPatternSource(pattern string) AggregationSource {
	return AggregationSource{Type: "pattern", Value: pattern}
}
func NewPathsSource(paths []string) AggregationSource {
	return AggregationSource{Type: "paths", Paths: paths}
}
func NewWatchSource(watchID string) AggregationSource {
	return AggregationSource{Type: "watch", Value: watchID}
}

// ---- P&L / Equity history ----

type ExternalFlowEntry struct {
	OperationID    string  `json:"operationId"`
	Type           string  `json:"type"`
	Direction      string  `json:"direction"`
	Amount         string  `json:"amount"`
	Denomination   string  `json:"denomination"`
	ValueUsd       string  `json:"valueUsd"`
	SourceArcaPath *string `json:"sourceArcaPath"`
	TargetArcaPath *string `json:"targetArcaPath"`
	Timestamp      string  `json:"timestamp"`
}

type PnlResponse struct {
	Prefix            string              `json:"prefix"`
	From              string              `json:"from"`
	To                string              `json:"to"`
	StartingEquityUsd string              `json:"startingEquityUsd"`
	EndingEquityUsd   string              `json:"endingEquityUsd"`
	NetInflowsUsd     string              `json:"netInflowsUsd"`
	NetOutflowsUsd    string              `json:"netOutflowsUsd"`
	PnlUsd            string              `json:"pnlUsd"`
	ExternalFlows     []ExternalFlowEntry `json:"externalFlows,omitempty"`
}

type ChartPointStatus string

const (
	ChartOpen       ChartPointStatus = "open"
	ChartSealed     ChartPointStatus = "sealed"
	ChartCarried    ChartPointStatus = "carried"
	ChartIncomplete ChartPointStatus = "incomplete"
)

type PnlPoint struct {
	Timestamp      string           `json:"timestamp"`
	PnlUsd         string           `json:"pnlUsd"`
	EquityUsd      string           `json:"equityUsd"`
	Status         ChartPointStatus `json:"status,omitempty"`
	CumInflowsUsd  string           `json:"cumInflowsUsd,omitempty"`
	CumOutflowsUsd string           `json:"cumOutflowsUsd,omitempty"`
	LastEventOpID  string           `json:"lastEventOpId,omitempty"`
	MidSetID       string           `json:"midSetId,omitempty"`
	ValueUsd       string           `json:"valueUsd,omitempty"`
}

type PnlHistoryResponse struct {
	Prefix              string              `json:"prefix"`
	From                string              `json:"from"`
	To                  string              `json:"to"`
	Points              int                 `json:"points"`
	Resolution          string              `json:"resolution,omitempty"`
	ResolutionRequested string              `json:"resolutionRequested,omitempty"`
	ServerNow           string              `json:"serverNow,omitempty"`
	StartingEquityUsd   string              `json:"startingEquityUsd"`
	EffectiveFrom       string              `json:"effectiveFrom,omitempty"`
	PnlPoints           []PnlPoint          `json:"pnlPoints"`
	ExternalFlows       []ExternalFlowEntry `json:"externalFlows,omitempty"`
	MidPrices           map[string]string   `json:"midPrices,omitempty"`
}

type EquityPoint struct {
	Timestamp      string           `json:"timestamp"`
	EquityUsd      string           `json:"equityUsd"`
	Status         ChartPointStatus `json:"status,omitempty"`
	CumInflowsUsd  string           `json:"cumInflowsUsd,omitempty"`
	CumOutflowsUsd string           `json:"cumOutflowsUsd,omitempty"`
	LastEventOpID  string           `json:"lastEventOpId,omitempty"`
	MidSetID       string           `json:"midSetId,omitempty"`
}

type EquityHistoryResponse struct {
	Prefix              string        `json:"prefix"`
	From                string        `json:"from"`
	To                  string        `json:"to"`
	Points              int           `json:"points"`
	Resolution          string        `json:"resolution,omitempty"`
	ResolutionRequested string        `json:"resolutionRequested,omitempty"`
	ServerNow           string        `json:"serverNow,omitempty"`
	EquityPoints        []EquityPoint `json:"equityPoints"`
}

// ---- Reconciliation ----

type ReconciliationState struct {
	ID                    string  `json:"id"`
	RealmID               string  `json:"realmId"`
	ArcaID                string  `json:"arcaId"`
	Venue                 string  `json:"venue"`
	ExpectedBalanceUsd    string  `json:"expectedBalanceUsd"`
	VenueReportedUsd      string  `json:"venueReportedUsd"`
	ExpectedPositionsJSON *string `json:"expectedPositionsJson"`
	VenuePositionsJSON    *string `json:"venuePositionsJson"`
	LastSeenVenueHash     *string `json:"lastSeenVenueHash"`
	DriftDetectedAt       *string `json:"driftDetectedAt"`
	LastReconciledAt      *string `json:"lastReconciledAt"`
	UpdatedAt             string  `json:"updatedAt"`
}

type ReconciliationStateListResponse struct {
	Items []ReconciliationState `json:"items"`
	Total int                   `json:"total"`
}

// ---- Invariants ----

type InvariantViolation struct {
	EntityID *string `json:"entityId"`
	Message  string  `json:"message"`
}

type InvariantCheckResult struct {
	ID             string               `json:"id"`
	Name           string               `json:"name,omitempty"`
	Passed         bool                 `json:"passed"`
	ViolationCount int                  `json:"violationCount"`
	Violations     []InvariantViolation `json:"violations"`
}

type InvariantCheckResponse struct {
	AllPassed bool                   `json:"allPassed"`
	Results   []InvariantCheckResult `json:"results"`
}

// ---- Payment Links ----

type ReturnStrategy string

const (
	ReturnRedirect ReturnStrategy = "redirect"
	ReturnClose    ReturnStrategy = "close"
	ReturnNavigate ReturnStrategy = "navigate"
)

type PaymentLinkResponse struct {
	ID             string         `json:"id"`
	URL            string         `json:"url"`
	Token          string         `json:"token"`
	Type           string         `json:"type"`
	Status         string         `json:"status"`
	Amount         string         `json:"amount"`
	Denomination   string         `json:"denomination"`
	OperationID    string         `json:"operationId"`
	ExpiresAt      string         `json:"expiresAt"`
	ReturnURL      string         `json:"returnUrl,omitempty"`
	ReturnStrategy ReturnStrategy `json:"returnStrategy,omitempty"`
	CreatedAt      string         `json:"createdAt"`
}

type CreatePaymentLinkResponse struct {
	PaymentLink PaymentLinkResponse `json:"paymentLink"`
	Operation   Operation           `json:"operation"`
}

func (r CreatePaymentLinkResponse) op() *Operation { return &r.Operation }
func (r *CreatePaymentLinkResponse) setOp(o *Operation) {
	if o != nil {
		r.Operation = *o
	}
}

type PaymentLinkListResponse struct {
	PaymentLinks []PaymentLinkResponse `json:"paymentLinks"`
	Total        int                   `json:"total"`
}

// ---- Permissions / Policy ----

type PolicyEffect string

const (
	EffectAllow PolicyEffect = "Allow"
	EffectDeny  PolicyEffect = "Deny"
)

type PolicyStatement struct {
	Effect    PolicyEffect `json:"effect,omitempty"`
	Actions   []string     `json:"actions"`
	Resources []string     `json:"resources"`
}

type TokenScope struct {
	Statements []PolicyStatement `json:"statements"`
}
