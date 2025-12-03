// Package heimdall provides Heimdall - the cognitive guardian for NornicDB.
//
// Heimdall enables NornicDB to run reasoning SLMs alongside embedding models
// for cognitive database capabilities including anomaly detection, runtime diagnosis,
// and memory curation.
//
// The Heimdall subsystem uses standard protocols:
//   - WebSocket (WSS) for real-time streaming chat
//   - Server-Sent Events (SSE) as fallback
//   - JSON message format (OpenAI-compatible)
//   - JWT authentication from existing auth system
package heimdall

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"time"
)

// ModelType categorizes models by their purpose.
type ModelType string

const (
	ModelTypeEmbedding      ModelType = "embedding"
	ModelTypeReasoning      ModelType = "reasoning"
	ModelTypeClassification ModelType = "classification"
)

// ModelInfo describes an available model in the registry.
type ModelInfo struct {
	Name         string    `json:"name"`
	Path         string    `json:"path"`
	Type         ModelType `json:"type"`
	SizeBytes    int64     `json:"size_bytes"`
	Quantization string    `json:"quantization,omitempty"`
	Loaded       bool      `json:"loaded"`
	LastUsed     time.Time `json:"last_used,omitempty"`
	VRAMEstimate int64     `json:"vram_estimate_bytes"`
}

// ChatMessage represents a message in the chat format (OpenAI-compatible).
type ChatMessage struct {
	Role    string `json:"role"` // "system", "user", "assistant"
	Content string `json:"content"`
}

// ChatRequest is the request format for chat completions.
// Compatible with OpenAI/Ollama API format.
type ChatRequest struct {
	Model       string        `json:"model"`
	Messages    []ChatMessage `json:"messages"`
	Stream      bool          `json:"stream,omitempty"`
	MaxTokens   int           `json:"max_tokens,omitempty"`
	Temperature float32       `json:"temperature,omitempty"`
	TopP        float32       `json:"top_p,omitempty"`
}

// ChatResponse is the response format for chat completions.
// Fully OpenAI API compatible.
type ChatResponse struct {
	ID      string       `json:"id"`
	Object  string       `json:"object"` // "chat.completion" or "chat.completion.chunk"
	Model   string       `json:"model"`
	Created int64        `json:"created"`
	Choices []ChatChoice `json:"choices"`
	Usage   *ChatUsage   `json:"usage,omitempty"`
}

// ChatChoice represents a single completion choice.
type ChatChoice struct {
	Index        int          `json:"index"`
	Message      *ChatMessage `json:"message,omitempty"`
	Delta        *ChatMessage `json:"delta,omitempty"` // For streaming
	FinishReason string       `json:"finish_reason,omitempty"`
}

// ChatUsage tracks token usage.
type ChatUsage struct {
	PromptTokens     int `json:"prompt_tokens"`
	CompletionTokens int `json:"completion_tokens"`
	TotalTokens      int `json:"total_tokens"`
}

// StreamEvent represents a Server-Sent Event for streaming.
type StreamEvent struct {
	Event string `json:"event,omitempty"` // "message", "done", "error"
	Data  string `json:"data"`
}

// GenerateParams configures text generation.
type GenerateParams struct {
	MaxTokens   int
	Temperature float32
	TopP        float32
	TopK        int
	StopTokens  []string
}

// DefaultGenerateParams returns sensible defaults for structured output.
func DefaultGenerateParams() GenerateParams {
	return GenerateParams{
		MaxTokens:   512,
		Temperature: 0.1, // Low for deterministic JSON output
		TopP:        0.9,
		TopK:        40,
		StopTokens:  []string{"<|im_end|>", "<|endoftext|>", "</s>"},
	}
}

// Generator is the interface for text generation models.
type Generator interface {
	// Generate produces a complete response.
	Generate(ctx context.Context, prompt string, params GenerateParams) (string, error)

	// GenerateStream produces tokens via callback.
	GenerateStream(ctx context.Context, prompt string, params GenerateParams, callback func(token string) error) error

	// Close releases model resources.
	Close() error

	// ModelPath returns the loaded model path.
	ModelPath() string
}

// ActionOpcode represents bounded actions the SLM can recommend.
// All SLM outputs map to these predefined actions for safety.
type ActionOpcode int

const (
	ActionNone ActionOpcode = iota
	ActionLogInfo
	ActionLogWarning
	ActionLogError
	ActionThrottleQuery
	ActionSuggestIndex
	ActionMergeNodes
	ActionRestartWorkerPool
	ActionClearQueue
	ActionTriggerGC
	ActionReduceConcurrency
)

// ActionResponse is the structured output format for SLM recommendations.
type ActionResponse struct {
	Action     ActionOpcode   `json:"action"`
	Confidence float64        `json:"confidence"`
	Reasoning  string         `json:"reasoning"`
	Params     map[string]any `json:"params,omitempty"`
}

// Config holds SLM subsystem configuration.
type Config struct {
	// Enabled controls whether Heimdall (the cognitive guardian) is active.
	// When enabled, Bifrost (the chat interface) is automatically enabled.
	// Default: false (opt-in feature)
	Enabled bool `json:"enabled"`

	// BifrostEnabled controls the Bifrost chat interface.
	// Automatically set to true when Heimdall is enabled.
	// Cannot be enabled independently - Bifrost requires Heimdall.
	BifrostEnabled bool `json:"bifrost_enabled"`

	ModelsDir   string  `json:"models_dir"`
	Model       string  `json:"model"`
	MaxTokens   int     `json:"max_tokens"`
	Temperature float32 `json:"temperature"`
	GPULayers   int     `json:"gpu_layers"`

	// Feature toggles
	AnomalyDetection bool          `json:"anomaly_detection"`
	AnomalyInterval  time.Duration `json:"anomaly_interval"`
	RuntimeDiagnosis bool          `json:"runtime_diagnosis"`
	RuntimeInterval  time.Duration `json:"runtime_interval"`
	MemoryCuration   bool          `json:"memory_curation"`
	CurationInterval time.Duration `json:"curation_interval"`
}

// DefaultConfig returns sensible defaults.
// Heimdall is disabled by default (opt-in feature).
// When Heimdall is enabled, Bifrost is automatically enabled.
func DefaultConfig() Config {
	return Config{
		Enabled:          false, // Heimdall disabled by default (opt-in)
		BifrostEnabled:   false, // Bifrost follows Heimdall state
		ModelsDir:        "",    // Empty = use NORNICDB_MODELS_DIR env var
		Model:            "qwen2.5-0.5b-instruct",
		MaxTokens:        512,
		Temperature:      0.1,
		GPULayers:        -1, // Auto
		AnomalyDetection: true,
		AnomalyInterval:  5 * time.Minute,
		RuntimeDiagnosis: true,
		RuntimeInterval:  1 * time.Minute,
		MemoryCuration:   false, // Experimental
		CurationInterval: 1 * time.Hour,
	}
}

// FeatureFlagsSource is the interface for getting Heimdall config from feature flags.
// This avoids import cycles with the config package.
type FeatureFlagsSource interface {
	GetHeimdallEnabled() bool
	GetHeimdallModel() string
	GetHeimdallGPULayers() int
	GetHeimdallMaxTokens() int
	GetHeimdallTemperature() float32
	GetHeimdallAnomalyDetection() bool
	GetHeimdallRuntimeDiagnosis() bool
	GetHeimdallMemoryCuration() bool
}

// ConfigFromFeatureFlags creates Heimdall config from feature flags.
// This is the preferred way to create Config - respects BYOM settings.
//
// Key behavior:
//   - When Heimdall is enabled, Bifrost is automatically enabled
//   - Bifrost cannot be enabled independently (requires Heimdall)
//   - Heimdall is disabled by default (opt-in feature)
//   - Uses NORNICDB_MODELS_DIR for model location (same as embedder)
func ConfigFromFeatureFlags(flags FeatureFlagsSource) Config {
	cfg := DefaultConfig()
	cfg.Enabled = flags.GetHeimdallEnabled()
	// Bifrost is automatically enabled when Heimdall is enabled
	// Bifrost (the chat interface) requires Heimdall (the SLM) to function
	cfg.BifrostEnabled = cfg.Enabled
	cfg.Model = flags.GetHeimdallModel()
	cfg.GPULayers = flags.GetHeimdallGPULayers()
	cfg.MaxTokens = flags.GetHeimdallMaxTokens()
	cfg.Temperature = flags.GetHeimdallTemperature()
	cfg.AnomalyDetection = flags.GetHeimdallAnomalyDetection()
	cfg.RuntimeDiagnosis = flags.GetHeimdallRuntimeDiagnosis()
	cfg.MemoryCuration = flags.GetHeimdallMemoryCuration()
	// ModelsDir stays empty - scheduler reads NORNICDB_MODELS_DIR directly
	// This ensures ONE model directory for both embedder and Heimdall
	return cfg
}

// BuildPrompt converts chat messages to a prompt string.
// Uses ChatML format for instruction-tuned models.
func BuildPrompt(messages []ChatMessage) string {
	var prompt string
	for _, msg := range messages {
		switch msg.Role {
		case "system":
			prompt += "<|im_start|>system\n" + msg.Content + "<|im_end|>\n"
		case "user":
			prompt += "<|im_start|>user\n" + msg.Content + "<|im_end|>\n"
		case "assistant":
			prompt += "<|im_start|>assistant\n" + msg.Content + "<|im_end|>\n"
		}
	}
	// Prompt for assistant response
	prompt += "<|im_start|>assistant\n"
	return prompt
}

// =============================================================================
// Plugin Lifecycle Hook Types
// =============================================================================

// PromptContext contains the prompt being built for Heimdall.
// ActionPrompt is immutable (always injected first).
// Plugins can modify the mutable fields to add context.
//
// Cancellation: Any lifecycle hook can cancel the request by calling
// ctx.Cancel("reason"). The request will be aborted and the reason
// sent to the user via Bifrost.
//
// Notifications: Plugins can send non-blocking SSE messages to the UI
// via ctx.Notify() - these are fire-and-forget and won't block the request.
type PromptContext struct {
	// RequestID for tracking through the lifecycle
	RequestID string

	// RequestTime when the request started
	RequestTime time.Time

	// === IMMUTABLE (set before PrePrompt, read-only for plugins) ===

	// ActionPrompt contains all registered actions formatted for the SLM.
	// This is always injected at the start of the system prompt.
	// Plugins CANNOT modify this field.
	ActionPrompt string

	// === MUTABLE (plugins can modify these in PrePrompt) ===

	// UserMessage is the current user input
	UserMessage string

	// Messages is the full conversation history
	Messages []ChatMessage

	// AdditionalInstructions are appended after ActionPrompt.
	// Plugins add context, constraints, or guidance here.
	AdditionalInstructions string

	// Examples help Heimdall understand user intent.
	// Plugins can add domain-specific examples.
	Examples []PromptExample

	// PluginData persists through the request lifecycle.
	// Plugins can store state here for use in PreExecute/PostExecute.
	PluginData map[string]interface{}

	// === INTERNAL (set by handler, used by methods) ===
	bifrost BifrostBridge // For sending notifications

	// === NOTIFICATION QUEUE (for inline streaming) ===
	// Notifications are queued and sent at the start of the streaming response
	// to maintain proper ordering with the chat content
	notificationQueue []QueuedNotification
	notificationMu    sync.Mutex

	// === CANCELLATION STATE (internal, use Cancel() method) ===
	cancelled    bool
	cancelReason string
	cancelledBy  string // Which hook/plugin cancelled
}

// QueuedNotification represents a notification waiting to be sent inline.
type QueuedNotification struct {
	Type    string `json:"type"` // "info", "warning", "error", "success", "progress"
	Title   string `json:"title"`
	Message string `json:"message"`
}

// Cancel aborts the request with a reason.
// The reason will be logged and sent to the user via Bifrost.
// cancelledBy should identify which plugin/hook is cancelling (e.g., "PrePrompt:myplugin").
func (p *PromptContext) Cancel(reason string, cancelledBy string) {
	p.cancelled = true
	p.cancelReason = reason
	p.cancelledBy = cancelledBy
}

// Cancelled returns true if the request has been cancelled.
func (p *PromptContext) Cancelled() bool {
	return p.cancelled
}

// CancelReason returns the reason for cancellation (empty if not cancelled).
func (p *PromptContext) CancelReason() string {
	return p.cancelReason
}

// CancelledBy returns which hook/plugin cancelled the request.
func (p *PromptContext) CancelledBy() string {
	return p.cancelledBy
}

// SetBifrost sets the Bifrost bridge for notifications (called by handler).
func (p *PromptContext) SetBifrost(b BifrostBridge) {
	p.bifrost = b
}

// Notify queues a notification to be sent inline with the streaming response.
// This ensures proper ordering - notifications appear at the correct point in the chat.
// Use this to send progress updates, warnings, or informational messages.
func (p *PromptContext) Notify(notificationType, title, message string) {
	p.notificationMu.Lock()
	defer p.notificationMu.Unlock()
	p.notificationQueue = append(p.notificationQueue, QueuedNotification{
		Type:    notificationType,
		Title:   title,
		Message: message,
	})
}

// DrainNotifications returns and clears all queued notifications.
// Called by the handler to send them inline with the streaming response.
func (p *PromptContext) DrainNotifications() []QueuedNotification {
	p.notificationMu.Lock()
	defer p.notificationMu.Unlock()
	notifications := p.notificationQueue
	p.notificationQueue = nil
	return notifications
}

// NotifyInfo sends an info notification (fire-and-forget).
func (p *PromptContext) NotifyInfo(title, message string) {
	p.Notify("info", title, message)
}

// NotifyWarning sends a warning notification (fire-and-forget).
func (p *PromptContext) NotifyWarning(title, message string) {
	p.Notify("warning", title, message)
}

// NotifyError sends an error notification (fire-and-forget).
func (p *PromptContext) NotifyError(title, message string) {
	p.Notify("error", title, message)
}

// NotifyProgress sends a progress notification (fire-and-forget).
func (p *PromptContext) NotifyProgress(title, message string) {
	p.Notify("progress", title, message)
}

// SendMessage sends a raw message to the UI (fire-and-forget).
func (p *PromptContext) SendMessage(message string) {
	if p.bifrost == nil {
		return
	}
	go p.bifrost.SendMessage(message)
}

// Broadcast sends a message to all connected clients (fire-and-forget).
func (p *PromptContext) Broadcast(message string) {
	if p.bifrost == nil {
		return
	}
	go p.bifrost.Broadcast(message)
}

// PromptExample is a user→action mapping example for Heimdall.
type PromptExample struct {
	UserSays   string // What the user might say
	ActionJSON string // The JSON action Heimdall should output
}

// BuildFinalPrompt constructs the complete prompt for Heimdall.
// ActionPrompt is ALWAYS first and immutable.
func (p *PromptContext) BuildFinalPrompt() string {
	var sb strings.Builder

	// === IMMUTABLE SECTION (always first) ===
	sb.WriteString("You are Heimdall, the AI assistant for NornicDB graph database.\n\n")
	sb.WriteString("AVAILABLE ACTIONS:\n")
	sb.WriteString(p.ActionPrompt)
	sb.WriteString("\n")

	// === MUTABLE SECTION (from plugins) ===
	if p.AdditionalInstructions != "" {
		sb.WriteString("ADDITIONAL CONTEXT:\n")
		sb.WriteString(p.AdditionalInstructions)
		sb.WriteString("\n\n")
	}

	// Examples (built-in + plugin-added)
	if len(p.Examples) > 0 {
		sb.WriteString("EXAMPLES:\n")
		for _, ex := range p.Examples {
			sb.WriteString(fmt.Sprintf("User: \"%s\" → %s\n", ex.UserSays, ex.ActionJSON))
		}
		sb.WriteString("\n")
	}

	sb.WriteString("Respond with JSON action command only. No explanations.\n")

	return sb.String()
}

// PreExecuteContext contains the parsed action before execution.
//
// Cancellation: Call ctx.Cancel("reason", "hook:plugin") to abort execution.
// The reason will be logged and sent to the user via Bifrost.
//
// Notifications: Plugins can send non-blocking SSE messages to the UI
// via ctx.Notify() - these are fire-and-forget and won't block the request.
type PreExecuteContext struct {
	// RequestID for tracking
	RequestID string

	// RequestTime when the request started
	RequestTime time.Time

	// Action is the parsed action name (e.g., "heimdall.watcher.status")
	Action string

	// Params are the parsed action parameters
	Params map[string]interface{}

	// RawResponse is the raw SLM response (for inspection if needed)
	RawResponse string

	// PluginData from the PrePrompt phase
	PluginData map[string]interface{}

	// Database provides read-only graph access for async fetches
	Database DatabaseReader

	// Metrics provides runtime metrics
	Metrics MetricsReader

	// === INTERNAL (set by handler, used by methods) ===
	bifrost BifrostBridge // For sending notifications

	// === NOTIFICATION QUEUE (for inline streaming) ===
	notificationQueue []QueuedNotification
	notificationMu    sync.Mutex

	// === CANCELLATION STATE (internal, use Cancel() method) ===
	cancelled    bool
	cancelReason string
	cancelledBy  string
}

// Cancel aborts the action execution with a reason.
func (p *PreExecuteContext) Cancel(reason string, cancelledBy string) {
	p.cancelled = true
	p.cancelReason = reason
	p.cancelledBy = cancelledBy
}

// Cancelled returns true if the request has been cancelled.
func (p *PreExecuteContext) Cancelled() bool {
	return p.cancelled
}

// CancelReason returns the reason for cancellation.
func (p *PreExecuteContext) CancelReason() string {
	return p.cancelReason
}

// CancelledBy returns which hook/plugin cancelled.
func (p *PreExecuteContext) CancelledBy() string {
	return p.cancelledBy
}

// SetBifrost sets the Bifrost bridge for notifications (called by handler).
func (p *PreExecuteContext) SetBifrost(b BifrostBridge) {
	p.bifrost = b
}

// Notify queues a notification to be sent inline after the AI response.
// This ensures proper ordering with the streaming content.
func (p *PreExecuteContext) Notify(notificationType, title, message string) {
	p.notificationMu.Lock()
	defer p.notificationMu.Unlock()
	p.notificationQueue = append(p.notificationQueue, QueuedNotification{
		Type:    notificationType,
		Title:   title,
		Message: message,
	})
}

// DrainNotifications returns and clears all queued notifications.
func (p *PreExecuteContext) DrainNotifications() []QueuedNotification {
	p.notificationMu.Lock()
	defer p.notificationMu.Unlock()
	notifications := p.notificationQueue
	p.notificationQueue = nil
	return notifications
}

// NotifyInfo queues an info notification.
func (p *PreExecuteContext) NotifyInfo(title, message string) {
	p.Notify("info", title, message)
}

// NotifyWarning queues a warning notification.
func (p *PreExecuteContext) NotifyWarning(title, message string) {
	p.Notify("warning", title, message)
}

// NotifyError queues an error notification.
func (p *PreExecuteContext) NotifyError(title, message string) {
	p.Notify("error", title, message)
}

// NotifyProgress queues a progress notification.
func (p *PreExecuteContext) NotifyProgress(title, message string) {
	p.Notify("progress", title, message)
}

// PreExecuteResult is returned via callback after async operations complete.
type PreExecuteResult struct {
	// Continue indicates whether to proceed with execution.
	// Set to false to abort the action.
	Continue bool

	// ModifiedParams replaces the original params if non-nil.
	ModifiedParams map[string]interface{}

	// AdditionalContext is merged into ActionContext.
	AdditionalContext map[string]interface{}

	// AbortMessage is returned to user if Continue=false.
	AbortMessage string

	// Error if something went wrong during pre-execute.
	Error error
}

// PostExecuteContext contains execution results for logging/state updates.
//
// Notifications from PostExecute are queued and sent inline after the action result,
// ensuring proper ordering in the streaming response.
type PostExecuteContext struct {
	// RequestID for tracking
	RequestID string

	// Action that was executed
	Action string

	// Params that were passed
	Params map[string]interface{}

	// Result from the action execution
	Result *ActionResult

	// Duration of action execution
	Duration time.Duration

	// PluginData from earlier phases
	PluginData map[string]interface{}

	// WasCancelled indicates if the request was cancelled in an earlier phase
	WasCancelled bool

	// CancellationInfo contains details if cancelled
	CancellationInfo *CancellationInfo

	// === NOTIFICATION QUEUE (for inline streaming) ===
	notificationQueue []QueuedNotification
	notificationMu    sync.Mutex
}

// Notify queues a notification to be sent inline after the action result.
func (p *PostExecuteContext) Notify(notificationType, title, message string) {
	p.notificationMu.Lock()
	defer p.notificationMu.Unlock()
	p.notificationQueue = append(p.notificationQueue, QueuedNotification{
		Type:    notificationType,
		Title:   title,
		Message: message,
	})
}

// DrainNotifications returns and clears all queued notifications.
func (p *PostExecuteContext) DrainNotifications() []QueuedNotification {
	p.notificationMu.Lock()
	defer p.notificationMu.Unlock()
	notifications := p.notificationQueue
	p.notificationQueue = nil
	return notifications
}

// NotifyInfo queues an info notification.
func (p *PostExecuteContext) NotifyInfo(title, message string) {
	p.Notify("info", title, message)
}

// NotifyWarning queues a warning notification.
func (p *PostExecuteContext) NotifyWarning(title, message string) {
	p.Notify("warning", title, message)
}

// NotifyError queues an error notification.
func (p *PostExecuteContext) NotifyError(title, message string) {
	p.Notify("error", title, message)
}

// NotifySuccess queues a success notification.
func (p *PostExecuteContext) NotifySuccess(title, message string) {
	p.Notify("success", title, message)
}

// CancellationInfo contains details about a cancelled request.
type CancellationInfo struct {
	Reason      string `json:"reason"`
	CancelledBy string `json:"cancelled_by"`
	Phase       string `json:"phase"` // "PrePrompt" or "PreExecute"
}

// =============================================================================
// Database Events - Hook for plugins to react to database operations
// =============================================================================

// DatabaseEventType categorizes database events.
type DatabaseEventType string

const (
	// Node events
	EventNodeCreated DatabaseEventType = "node.created"
	EventNodeUpdated DatabaseEventType = "node.updated"
	EventNodeDeleted DatabaseEventType = "node.deleted"
	EventNodeRead    DatabaseEventType = "node.read"

	// Relationship events
	EventRelationshipCreated DatabaseEventType = "relationship.created"
	EventRelationshipUpdated DatabaseEventType = "relationship.updated"
	EventRelationshipDeleted DatabaseEventType = "relationship.deleted"

	// Query events
	EventQueryExecuted DatabaseEventType = "query.executed"
	EventQueryFailed   DatabaseEventType = "query.failed"

	// Index events
	EventIndexCreated DatabaseEventType = "index.created"
	EventIndexDropped DatabaseEventType = "index.dropped"

	// Transaction events
	EventTransactionCommit   DatabaseEventType = "transaction.commit"
	EventTransactionRollback DatabaseEventType = "transaction.rollback"

	// System events
	EventDatabaseStarted  DatabaseEventType = "database.started"
	EventDatabaseShutdown DatabaseEventType = "database.shutdown"
	EventBackupStarted    DatabaseEventType = "backup.started"
	EventBackupCompleted  DatabaseEventType = "backup.completed"
)

// DatabaseEvent represents a database event that plugins can react to.
// This provides a unified interface for all database operations.
type DatabaseEvent struct {
	// Type identifies what kind of event occurred
	Type DatabaseEventType `json:"type"`

	// Timestamp when the event occurred
	Timestamp time.Time `json:"timestamp"`

	// RequestID links to the originating request (if applicable)
	RequestID string `json:"request_id,omitempty"`

	// === Node/Relationship Data ===

	// NodeID for node events
	NodeID string `json:"node_id,omitempty"`

	// NodeLabels for node events
	NodeLabels []string `json:"node_labels,omitempty"`

	// RelationshipID for relationship events
	RelationshipID string `json:"relationship_id,omitempty"`

	// RelationshipType for relationship events
	RelationshipType string `json:"relationship_type,omitempty"`

	// SourceNodeID for relationship events
	SourceNodeID string `json:"source_node_id,omitempty"`

	// TargetNodeID for relationship events
	TargetNodeID string `json:"target_node_id,omitempty"`

	// Properties that were set/changed
	Properties map[string]interface{} `json:"properties,omitempty"`

	// OldProperties for update events (what was there before)
	OldProperties map[string]interface{} `json:"old_properties,omitempty"`

	// === Query Data ===

	// Query is the Cypher query that was executed
	Query string `json:"query,omitempty"`

	// QueryParams are the parameters passed to the query
	QueryParams map[string]interface{} `json:"query_params,omitempty"`

	// Duration of query execution
	Duration time.Duration `json:"duration,omitempty"`

	// RowsAffected by the query
	RowsAffected int64 `json:"rows_affected,omitempty"`

	// Error message if the query failed
	Error string `json:"error,omitempty"`

	// === Index Data ===

	// IndexName for index events
	IndexName string `json:"index_name,omitempty"`

	// IndexLabel for index events
	IndexLabel string `json:"index_label,omitempty"`

	// IndexProperty for index events
	IndexProperty string `json:"index_property,omitempty"`

	// === Context ===

	// UserID who triggered the event (if authenticated)
	UserID string `json:"user_id,omitempty"`

	// Source identifies where the event came from (e.g., "bolt", "http", "internal")
	Source string `json:"source,omitempty"`

	// Metadata for any additional event-specific data
	Metadata map[string]interface{} `json:"metadata,omitempty"`
}

// IsNodeEvent returns true if this is a node-related event.
func (e *DatabaseEvent) IsNodeEvent() bool {
	return e.Type == EventNodeCreated || e.Type == EventNodeUpdated ||
		e.Type == EventNodeDeleted || e.Type == EventNodeRead
}

// IsRelationshipEvent returns true if this is a relationship-related event.
func (e *DatabaseEvent) IsRelationshipEvent() bool {
	return e.Type == EventRelationshipCreated || e.Type == EventRelationshipUpdated ||
		e.Type == EventRelationshipDeleted
}

// IsQueryEvent returns true if this is a query-related event.
func (e *DatabaseEvent) IsQueryEvent() bool {
	return e.Type == EventQueryExecuted || e.Type == EventQueryFailed
}

// IsTransactionEvent returns true if this is a transaction-related event.
func (e *DatabaseEvent) IsTransactionEvent() bool {
	return e.Type == EventTransactionCommit || e.Type == EventTransactionRollback
}

// =============================================================================
// Optional Plugin Interfaces - Plugins implement only what they need
// =============================================================================

// PrePromptHook is an optional interface for plugins that want to modify prompts.
// If a plugin implements this, PrePrompt will be called before each SLM request.
type PrePromptHook interface {
	// PrePrompt is called BEFORE sending the prompt to Heimdall.
	// The ActionPrompt field is IMMUTABLE (already set, read-only).
	// Plugins can modify mutable fields:
	//   - AdditionalInstructions: Add context, constraints, guidance
	//   - Examples: Add domain-specific examples
	//   - PluginData: Store state for later phases
	// Return error to log warning (does not abort request).
	PrePrompt(ctx *PromptContext) error
}

// PreExecuteHook is an optional interface for plugins that want to validate/modify actions.
// If a plugin implements this, PreExecute will be called after SLM responds but before action runs.
type PreExecuteHook interface {
	// PreExecute is called AFTER Heimdall responds, BEFORE action execution.
	// Plugins can perform async operations:
	//   - Fetch additional data from external services
	//   - Validate/modify params
	//   - Abort execution by setting Continue=false
	// The done callback MUST be called when complete.
	PreExecute(ctx *PreExecuteContext, done func(PreExecuteResult))
}

// PostExecuteHook is an optional interface for plugins that want to react to action results.
// If a plugin implements this, PostExecute will be called after each action completes.
type PostExecuteHook interface {
	// PostExecute is called AFTER action execution completes.
	// Plugins can:
	//   - Log execution metrics
	//   - Update internal state
	//   - Cache results
	//   - Trigger side effects
	// This is fire-and-forget (does not block response).
	PostExecute(ctx *PostExecuteContext)
}

// DatabaseEventHook is an optional interface for plugins that want to react to database events.
// This enables plugins to monitor database activity without modifying the database layer.
type DatabaseEventHook interface {
	// OnDatabaseEvent is called when a database event occurs.
	// This is fire-and-forget (does not block database operations).
	// The plugin should handle errors internally and not panic.
	//
	// Events are delivered asynchronously - the database operation
	// has already completed by the time this is called.
	//
	// Plugins can use this to:
	//   - Build audit logs
	//   - Track usage patterns
	//   - Trigger alerts on specific events
	//   - Update caches
	//   - Collect metrics
	OnDatabaseEvent(event *DatabaseEvent)
}

// FullLifecycleHook is a convenience interface for plugins that implement all hooks.
// Plugins are NOT required to implement this - they can pick and choose.
type FullLifecycleHook interface {
	PrePromptHook
	PreExecuteHook
	PostExecuteHook
	DatabaseEventHook
}
