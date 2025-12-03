// Package heimdall provides the Heimdall SLM Management plugin.
//
// Heimdall is the all-seeing guardian of the SLM subsystem, named after the
// Norse god who watches over Bifröst. Like its namesake, Heimdall monitors
// all activity, maintains vigilance over system health, and controls access
// to the cognitive capabilities of NornicDB.
//
// # Plugin Type
//
// This is an SLM plugin (Type() returns "slm"), which means it provides
// subsystem management capabilities that the SLM can use.
//
// # Actions Provided
//
//   - heimdall.heimdall.status - Get SLM status (Heimdall's vigilant watch)
//   - heimdall.heimdall.health - Check SLM health (Heimdall's keen sight)
//   - heimdall.heimdall.config - Get/set SLM configuration
//   - heimdall.heimdall.metrics - Get SLM metrics (Heimdall's awareness)
//   - heimdall.heimdall.events - Get recent events (Heimdall's memory)
//
// # Example Usage
//
// User: "What's the status of the SLM?"
// SLM maps to: heimdall.heimdall.status
// Result: Returns current model, memory usage, request counts
//
// # Building as Plugin
//
// To build as a standalone .so plugin:
//
//	go build -buildmode=plugin -o heimdall.so ./plugins/heimdall
//
// # Built-in Registration
//
// This plugin is also registered as a built-in plugin, so no .so file is needed.
package heimdall

import (
	"fmt"
	"log"
	"runtime"
	"sync"
	"time"

	"github.com/orneryd/nornicdb/pkg/heimdall"
)

// Plugin is the exported plugin variable.
// For .so plugins, export as: var Plugin heimdall.HeimdallPlugin = &WatcherPlugin{}
var Plugin heimdall.HeimdallPlugin = &WatcherPlugin{}

// WatcherPlugin implements heimdall.HeimdallPlugin for SLM management.
// The Watcher is Heimdall's core guardian - the all-seeing eye of the system.
//
// This plugin also demonstrates autonomous action invocation:
// - Implements DatabaseEventHook to monitor database events
// - Accumulates events and triggers analysis when thresholds are exceeded
// - Uses HeimdallInvoker to autonomously invoke SLM actions
type WatcherPlugin struct {
	mu       sync.RWMutex
	ctx      heimdall.SubsystemContext
	status   heimdall.SubsystemStatus
	events   []heimdall.SubsystemEvent
	config   map[string]interface{}
	started  time.Time
	requests int64
	errors   int64

	// === Event Accumulation for Autonomous Actions ===
	// These track database events for autonomous action triggering
	queryFailures     int64     // Count of failed queries
	lastFailureReset  time.Time // When failure count was last reset
	nodeCreations     int64     // Track node creation rate
	lastCreationReset time.Time
}

// === Identity Methods ===

func (p *WatcherPlugin) Name() string {
	return "watcher"
}

func (p *WatcherPlugin) Version() string {
	return "1.0.0"
}

func (p *WatcherPlugin) Type() string {
	return heimdall.PluginTypeHeimdall // Must return "heimdall"
}

func (p *WatcherPlugin) Description() string {
	return "Watcher - Heimdall's core guardian, the all-seeing eye of NornicDB's SLM subsystem"
}

// === Lifecycle Methods ===

func (p *WatcherPlugin) Initialize(ctx heimdall.SubsystemContext) error {
	p.mu.Lock()
	defer p.mu.Unlock()

	p.ctx = ctx
	p.status = heimdall.StatusReady
	p.events = make([]heimdall.SubsystemEvent, 0, 100)
	p.config = map[string]interface{}{
		"max_tokens":  ctx.Config.MaxTokens,
		"temperature": ctx.Config.Temperature,
		"model":       ctx.Config.Model,
	}

	p.addEvent("info", "Heimdall awakens - SLM guardian initialized", nil)
	return nil
}

func (p *WatcherPlugin) Start() error {
	p.mu.Lock()
	defer p.mu.Unlock()

	p.status = heimdall.StatusRunning
	p.started = time.Now()
	p.addEvent("info", "Heimdall stands watch - SLM guardian active", nil)
	return nil
}

func (p *WatcherPlugin) Stop() error {
	p.mu.Lock()
	defer p.mu.Unlock()

	p.status = heimdall.StatusStopped
	p.addEvent("info", "Heimdall rests - SLM guardian paused", nil)
	return nil
}

func (p *WatcherPlugin) Shutdown() error {
	p.mu.Lock()
	defer p.mu.Unlock()

	p.status = heimdall.StatusUninitialized
	p.addEvent("info", "Heimdall departs - SLM guardian shutdown", nil)
	return nil
}

// === State & Health Methods ===

func (p *WatcherPlugin) Status() heimdall.SubsystemStatus {
	p.mu.RLock()
	defer p.mu.RUnlock()
	return p.status
}

func (p *WatcherPlugin) Health() heimdall.SubsystemHealth {
	p.mu.RLock()
	defer p.mu.RUnlock()

	healthy := p.status == heimdall.StatusRunning || p.status == heimdall.StatusReady

	return heimdall.SubsystemHealth{
		Status:    p.status,
		Healthy:   healthy,
		Message:   fmt.Sprintf("Heimdall reports: SLM is %s", p.status),
		LastCheck: time.Now(),
		Details: map[string]interface{}{
			"uptime_seconds": time.Since(p.started).Seconds(),
			"requests":       p.requests,
			"errors":         p.errors,
		},
	}
}

func (p *WatcherPlugin) Metrics() map[string]interface{} {
	p.mu.RLock()
	defer p.mu.RUnlock()

	var memStats runtime.MemStats
	runtime.ReadMemStats(&memStats)

	return map[string]interface{}{
		"status":         string(p.status),
		"uptime_seconds": time.Since(p.started).Seconds(),
		"requests":       p.requests,
		"errors":         p.errors,
		"error_rate":     float64(p.errors) / float64(max(p.requests, 1)),
		"memory_mb":      memStats.Alloc / 1024 / 1024,
		"goroutines":     runtime.NumGoroutine(),
	}
}

// === Configuration Methods ===

func (p *WatcherPlugin) Config() map[string]interface{} {
	p.mu.RLock()
	defer p.mu.RUnlock()

	// Return copy
	result := make(map[string]interface{})
	for k, v := range p.config {
		result[k] = v
	}
	return result
}

func (p *WatcherPlugin) Configure(settings map[string]interface{}) error {
	p.mu.Lock()
	defer p.mu.Unlock()

	// Validate and apply settings
	for key, value := range settings {
		switch key {
		case "max_tokens":
			if v, ok := value.(int); ok && v > 0 && v <= 4096 {
				p.config[key] = v
			} else {
				return fmt.Errorf("invalid max_tokens: must be 1-4096")
			}
		case "temperature":
			if v, ok := value.(float64); ok && v >= 0 && v <= 2 {
				p.config[key] = v
			} else {
				return fmt.Errorf("invalid temperature: must be 0-2")
			}
		default:
			return fmt.Errorf("unknown config key: %s", key)
		}
	}

	p.addEvent("info", "Heimdall configuration updated", settings)
	return nil
}

func (p *WatcherPlugin) ConfigSchema() map[string]interface{} {
	return map[string]interface{}{
		"type": "object",
		"properties": map[string]interface{}{
			"max_tokens": map[string]interface{}{
				"type":        "integer",
				"description": "Maximum tokens to generate",
				"minimum":     1,
				"maximum":     4096,
				"default":     512,
			},
			"temperature": map[string]interface{}{
				"type":        "number",
				"description": "Generation temperature (0=deterministic, 2=creative)",
				"minimum":     0,
				"maximum":     2,
				"default":     0.1,
			},
		},
	}
}

// === Actions ===

func (p *WatcherPlugin) Actions() map[string]heimdall.ActionFunc {
	return map[string]heimdall.ActionFunc{
		"hello": {
			Description: "Hello World - A simple test action to verify Heimdall is working",
			Category:    "test",
			Handler:     p.actionHello,
		},
		"status": {
			Description: "Get comprehensive NornicDB status including database, runtime, and Heimdall metrics",
			Category:    "monitoring",
			Handler:     p.actionStatus,
		},
		"health": {
			Description: "Check system health status",
			Category:    "monitoring",
			Handler:     p.actionHealth,
		},
		"config": {
			Description: "Get current SLM configuration",
			Category:    "configuration",
			Handler:     p.actionConfig,
		},
		"set_config": {
			Description: "Update SLM configuration (params: max_tokens, temperature)",
			Category:    "configuration",
			Handler:     p.actionSetConfig,
		},
		"metrics": {
			Description: "Get detailed metrics: runtime, memory, goroutines, GC, database stats",
			Category:    "monitoring",
			Handler:     p.actionMetrics,
		},
		"events": {
			Description: "Get recent system events (params: limit)",
			Category:    "monitoring",
			Handler:     p.actionEvents,
		},
		"query": {
			Description: "Execute a read-only Cypher query (params: cypher, params)",
			Category:    "database",
			Handler:     p.actionQuery,
		},
		"db_stats": {
			Description: "Get database statistics: node/edge counts, labels, indexes",
			Category:    "database",
			Handler:     p.actionDBStats,
		},
		"broadcast": {
			Description: "Broadcast a message to all connected Bifrost clients (params: message)",
			Category:    "system",
			Handler:     p.actionBroadcast,
		},
		"notify": {
			Description: "Send a notification via Bifrost (params: type, title, message)",
			Category:    "system",
			Handler:     p.actionNotify,
		},
	}
}

// Action Handlers

// actionHello is a simple test action to verify Heimdall is working.
// Prompt examples that should trigger this:
//   - "say hello"
//   - "test the system"
//   - "hello world"
//   - "run a test action"
func (p *WatcherPlugin) actionHello(ctx heimdall.ActionContext) (*heimdall.ActionResult, error) {
	p.mu.Lock()
	p.requests++
	p.mu.Unlock()

	name := "World"
	if n, ok := ctx.Params["name"].(string); ok && n != "" {
		name = n
	}

	greeting := fmt.Sprintf("Hello, %s! 👋 Heimdall is operational and ready to serve.", name)
	p.addEvent("info", greeting, nil)

	return &heimdall.ActionResult{
		Success: true,
		Message: greeting,
		Data: map[string]interface{}{
			"greeting":  greeting,
			"timestamp": time.Now().Format(time.RFC3339),
			"model":     p.config["model"],
			"status":    string(p.status),
		},
	}, nil
}

func (p *WatcherPlugin) actionStatus(ctx heimdall.ActionContext) (*heimdall.ActionResult, error) {
	p.mu.Lock()
	p.requests++
	p.mu.Unlock()

	health := p.Health()
	pluginMetrics := p.Metrics()

	// Collect comprehensive status
	status := map[string]interface{}{
		"heimdall": map[string]interface{}{
			"health":  health,
			"metrics": pluginMetrics,
			"config":  p.Config(),
		},
	}

	// Add database stats if available
	if ctx.Database != nil {
		dbStats := ctx.Database.Stats()
		status["database"] = map[string]interface{}{
			"nodes":         dbStats.NodeCount,
			"relationships": dbStats.RelationshipCount,
			"labels":        dbStats.LabelCounts,
		}
	}

	// Add runtime metrics if available
	if ctx.Metrics != nil {
		runtimeMetrics := ctx.Metrics.Runtime()
		status["runtime"] = map[string]interface{}{
			"goroutines": runtimeMetrics.GoroutineCount,
			"memory_mb":  runtimeMetrics.MemoryAllocMB,
			"gc_cycles":  runtimeMetrics.NumGC,
		}
	} else {
		// Fallback to direct runtime stats
		var m runtime.MemStats
		runtime.ReadMemStats(&m)
		status["runtime"] = map[string]interface{}{
			"goroutines": runtime.NumGoroutine(),
			"memory_mb":  m.Alloc / 1024 / 1024,
			"gc_cycles":  m.NumGC,
		}
	}

	return &heimdall.ActionResult{
		Success: true,
		Message: fmt.Sprintf("NornicDB Status: %s, Uptime: %.0fs, Goroutines: %d",
			health.Status, pluginMetrics["uptime_seconds"], runtime.NumGoroutine()),
		Data: status,
	}, nil
}

func (p *WatcherPlugin) actionHealth(ctx heimdall.ActionContext) (*heimdall.ActionResult, error) {
	p.mu.Lock()
	p.requests++
	p.mu.Unlock()

	health := p.Health()

	return &heimdall.ActionResult{
		Success: health.Healthy,
		Message: health.Message,
		Data: map[string]interface{}{
			"health": health,
		},
	}, nil
}

func (p *WatcherPlugin) actionConfig(ctx heimdall.ActionContext) (*heimdall.ActionResult, error) {
	p.mu.Lock()
	p.requests++
	p.mu.Unlock()

	return &heimdall.ActionResult{
		Success: true,
		Message: "Current SLM configuration",
		Data: map[string]interface{}{
			"config": p.Config(),
			"schema": p.ConfigSchema(),
		},
	}, nil
}

func (p *WatcherPlugin) actionSetConfig(ctx heimdall.ActionContext) (*heimdall.ActionResult, error) {
	p.mu.Lock()
	p.requests++
	p.mu.Unlock()

	if err := p.Configure(ctx.Params); err != nil {
		p.mu.Lock()
		p.errors++
		p.mu.Unlock()
		return &heimdall.ActionResult{
			Success: false,
			Message: fmt.Sprintf("Configuration error: %v", err),
		}, nil
	}

	return &heimdall.ActionResult{
		Success: true,
		Message: "Configuration updated successfully",
		Data: map[string]interface{}{
			"config": p.Config(),
		},
	}, nil
}

func (p *WatcherPlugin) actionMetrics(ctx heimdall.ActionContext) (*heimdall.ActionResult, error) {
	p.mu.Lock()
	p.requests++
	p.mu.Unlock()

	// Collect comprehensive metrics
	metrics := map[string]interface{}{
		"heimdall": p.Metrics(),
	}

	// Add runtime metrics
	var m runtime.MemStats
	runtime.ReadMemStats(&m)
	metrics["runtime"] = map[string]interface{}{
		"goroutines":      runtime.NumGoroutine(),
		"memory_alloc_mb": m.Alloc / 1024 / 1024,
		"memory_sys_mb":   m.Sys / 1024 / 1024,
		"heap_alloc_mb":   m.HeapAlloc / 1024 / 1024,
		"heap_inuse_mb":   m.HeapInuse / 1024 / 1024,
		"stack_inuse_mb":  m.StackInuse / 1024 / 1024,
		"gc_cycles":       m.NumGC,
		"gc_pause_ns":     m.PauseTotalNs,
	}

	// Add database stats if available
	if ctx.Database != nil {
		dbStats := ctx.Database.Stats()
		metrics["database"] = map[string]interface{}{
			"nodes":         dbStats.NodeCount,
			"relationships": dbStats.RelationshipCount,
			"labels":        dbStats.LabelCounts,
		}
	}

	// Add metrics reader data if available
	if ctx.Metrics != nil {
		runtimeFromReader := ctx.Metrics.Runtime()
		metrics["runtime_reader"] = runtimeFromReader
	}

	return &heimdall.ActionResult{
		Success: true,
		Message: fmt.Sprintf("NornicDB Metrics: %d goroutines, %d MB memory, %d GC cycles",
			runtime.NumGoroutine(), m.Alloc/1024/1024, m.NumGC),
		Data: metrics,
	}, nil
}

func (p *WatcherPlugin) actionEvents(ctx heimdall.ActionContext) (*heimdall.ActionResult, error) {
	p.mu.Lock()
	p.requests++
	p.mu.Unlock()

	limit := 10
	if l, ok := ctx.Params["limit"].(int); ok && l > 0 {
		limit = l
	}

	events := p.RecentEvents(limit)

	return &heimdall.ActionResult{
		Success: true,
		Message: fmt.Sprintf("Heimdall recalls %d events", len(events)),
		Data: map[string]interface{}{
			"events": events,
		},
	}, nil
}

// actionBroadcast demonstrates using Bifrost to broadcast messages to all connected clients.
func (p *WatcherPlugin) actionBroadcast(ctx heimdall.ActionContext) (*heimdall.ActionResult, error) {
	p.mu.Lock()
	p.requests++
	p.mu.Unlock()

	msg, ok := ctx.Params["message"].(string)
	if !ok || msg == "" {
		return &heimdall.ActionResult{
			Success: false,
			Message: "Missing required parameter: message",
		}, nil
	}

	// Use Bifrost to broadcast the message
	if ctx.Bifrost != nil {
		if err := ctx.Bifrost.Broadcast(fmt.Sprintf("📢 Heimdall announces: %s", msg)); err != nil {
			p.mu.Lock()
			p.errors++
			p.mu.Unlock()
			return &heimdall.ActionResult{
				Success: false,
				Message: fmt.Sprintf("Failed to broadcast via Bifrost: %v", err),
			}, nil
		}
	}

	p.addEvent("info", fmt.Sprintf("Broadcast sent: %s", msg), nil)

	return &heimdall.ActionResult{
		Success: true,
		Message: fmt.Sprintf("Message broadcast to %d connected clients", ctx.Bifrost.ConnectionCount()),
		Data: map[string]interface{}{
			"message":     msg,
			"connections": ctx.Bifrost.ConnectionCount(),
		},
	}, nil
}

// actionNotify demonstrates using Bifrost to send typed notifications.
func (p *WatcherPlugin) actionNotify(ctx heimdall.ActionContext) (*heimdall.ActionResult, error) {
	p.mu.Lock()
	p.requests++
	p.mu.Unlock()

	notifType, _ := ctx.Params["type"].(string)
	title, _ := ctx.Params["title"].(string)
	message, _ := ctx.Params["message"].(string)

	if notifType == "" {
		notifType = "info"
	}
	if title == "" {
		title = "Heimdall"
	}
	if message == "" {
		return &heimdall.ActionResult{
			Success: false,
			Message: "Missing required parameter: message",
		}, nil
	}

	// Use Bifrost to send notification
	if ctx.Bifrost != nil {
		if err := ctx.Bifrost.SendNotification(notifType, title, message); err != nil {
			p.mu.Lock()
			p.errors++
			p.mu.Unlock()
			return &heimdall.ActionResult{
				Success: false,
				Message: fmt.Sprintf("Failed to send notification via Bifrost: %v", err),
			}, nil
		}
	}

	p.addEvent(notifType, fmt.Sprintf("Notification sent: [%s] %s - %s", notifType, title, message), nil)

	return &heimdall.ActionResult{
		Success: true,
		Message: fmt.Sprintf("Notification sent: %s", message),
		Data: map[string]interface{}{
			"type":    notifType,
			"title":   title,
			"message": message,
		},
	}, nil
}

// actionQuery executes a read-only Cypher query against the database.
func (p *WatcherPlugin) actionQuery(ctx heimdall.ActionContext) (*heimdall.ActionResult, error) {
	p.mu.Lock()
	p.requests++
	p.mu.Unlock()

	cypher, ok := ctx.Params["cypher"].(string)
	if !ok || cypher == "" {
		return &heimdall.ActionResult{
			Success: false,
			Message: "Missing required parameter: cypher",
		}, nil
	}

	// Check if database is available
	if ctx.Database == nil {
		return &heimdall.ActionResult{
			Success: false,
			Message: "Database access not available",
		}, nil
	}

	// Get query params
	queryParams := make(map[string]interface{})
	if params, ok := ctx.Params["params"].(map[string]interface{}); ok {
		queryParams = params
	}

	// Execute query
	results, err := ctx.Database.Query(ctx.Context, cypher, queryParams)
	if err != nil {
		p.mu.Lock()
		p.errors++
		p.mu.Unlock()
		return &heimdall.ActionResult{
			Success: false,
			Message: fmt.Sprintf("Query failed: %v", err),
		}, nil
	}

	p.addEvent("info", fmt.Sprintf("Query executed: %s", cypher), map[string]interface{}{
		"result_count": len(results),
	})

	return &heimdall.ActionResult{
		Success: true,
		Message: fmt.Sprintf("Query returned %d results", len(results)),
		Data: map[string]interface{}{
			"results": results,
			"count":   len(results),
		},
	}, nil
}

// actionDBStats returns comprehensive database statistics.
func (p *WatcherPlugin) actionDBStats(ctx heimdall.ActionContext) (*heimdall.ActionResult, error) {
	p.mu.Lock()
	p.requests++
	p.mu.Unlock()

	stats := map[string]interface{}{}

	// Get database stats if available
	if ctx.Database != nil {
		dbStats := ctx.Database.Stats()
		stats["database"] = map[string]interface{}{
			"nodes":         dbStats.NodeCount,
			"relationships": dbStats.RelationshipCount,
			"labels":        dbStats.LabelCounts,
		}
	}

	// Get runtime stats
	var m runtime.MemStats
	runtime.ReadMemStats(&m)
	stats["runtime"] = map[string]interface{}{
		"goroutines":      runtime.NumGoroutine(),
		"memory_alloc_mb": m.Alloc / 1024 / 1024,
		"heap_objects":    m.HeapObjects,
		"gc_cycles":       m.NumGC,
	}

	// Get metrics if available
	if ctx.Metrics != nil {
		runtimeMetrics := ctx.Metrics.Runtime()
		stats["metrics"] = runtimeMetrics
	}

	return &heimdall.ActionResult{
		Success: true,
		Message: "Database statistics",
		Data:    stats,
	}, nil
}

// === Data Access Methods ===

func (p *WatcherPlugin) Summary() string {
	p.mu.RLock()
	defer p.mu.RUnlock()

	return fmt.Sprintf("Heimdall watches: Status=%s, Model=%s, Uptime=%.0fs, Requests=%d, Errors=%d",
		p.status,
		p.config["model"],
		time.Since(p.started).Seconds(),
		p.requests,
		p.errors,
	)
}

func (p *WatcherPlugin) RecentEvents(limit int) []heimdall.SubsystemEvent {
	p.mu.RLock()
	defer p.mu.RUnlock()

	if limit <= 0 || limit > len(p.events) {
		limit = len(p.events)
	}

	// Return most recent events
	start := len(p.events) - limit
	if start < 0 {
		start = 0
	}

	result := make([]heimdall.SubsystemEvent, limit)
	copy(result, p.events[start:])
	return result
}

// === Request Lifecycle Hooks ===

// PrePrompt is called before the prompt is sent to Heimdall.
// The ActionPrompt is immutable (already set). We can add context here.
//
// This demonstrates:
// - Adding custom examples to help the SLM
// - Storing state in PluginData for later hooks
// - Sending fire-and-forget notifications to the UI
// - Cancelling requests when needed
func (p *WatcherPlugin) PrePrompt(ctx *heimdall.PromptContext) error {
	p.mu.Lock()
	defer p.mu.Unlock()

	msgPreview := ctx.UserMessage
	if len(msgPreview) > 50 {
		msgPreview = msgPreview[:50] + "..."
	}
	log.Printf("[Watcher] PrePrompt: request=%s user_msg=%q", ctx.RequestID, msgPreview)

	// === EXAMPLE: Send non-blocking notification to UI ===
	// This is fire-and-forget - it won't block the request
	ctx.NotifyInfo("Watcher", "Processing your request...")

	// Add watcher-specific context to help Heimdall understand the system
	var m runtime.MemStats
	runtime.ReadMemStats(&m)

	// Store current metrics in PluginData for later phases
	if ctx.PluginData == nil {
		ctx.PluginData = make(map[string]interface{})
	}
	ctx.PluginData["watcher_preprompt_time"] = time.Now()
	ctx.PluginData["watcher_goroutines"] = runtime.NumGoroutine()
	ctx.PluginData["watcher_memory_mb"] = m.Alloc / 1024 / 1024

	// Add status examples to help with natural language → action mapping
	ctx.Examples = append(ctx.Examples,
		heimdall.PromptExample{
			UserSays:   "check the system",
			ActionJSON: `{"action": "heimdall.watcher.status", "params": {}}`,
		},
		heimdall.PromptExample{
			UserSays:   "show database info",
			ActionJSON: `{"action": "heimdall.watcher.db_stats", "params": {}}`,
		},
	)

	// === EXAMPLE: Conditional cancellation ===
	// Uncomment to see cancellation in action:
	// if strings.Contains(ctx.UserMessage, "dangerous") {
	//     ctx.Cancel("Message contains dangerous content", "PrePrompt:watcher")
	//     return nil
	// }

	// === EXAMPLE: Send progress notification (async, non-blocking) ===
	ctx.NotifyProgress("Watcher", fmt.Sprintf("System state: %d goroutines, %d MB memory",
		runtime.NumGoroutine(), m.Alloc/1024/1024))

	p.addEvent("info", "PrePrompt hook executed", map[string]interface{}{
		"request_id":  ctx.RequestID,
		"user_msg":    ctx.UserMessage[:min(50, len(ctx.UserMessage))],
		"has_history": len(ctx.Messages) > 0,
	})

	return nil
}

// PreExecute is called after Heimdall responds, before action execution.
// We can fetch additional data or modify params here.
//
// This demonstrates:
// - Async validation with callback
// - Sending notifications before action runs
// - Cancelling with ctx.Cancel() method
// - Modifying params before execution
func (p *WatcherPlugin) PreExecute(ctx *heimdall.PreExecuteContext, done func(heimdall.PreExecuteResult)) {
	p.mu.Lock()
	p.requests++
	p.mu.Unlock()

	log.Printf("[Watcher] PreExecute: request=%s action=%s params=%v", ctx.RequestID, ctx.Action, ctx.Params)

	// === EXAMPLE: Send notification that we're about to execute ===
	ctx.NotifyInfo("Watcher", fmt.Sprintf("Executing action: %s", ctx.Action))

	// Log the action being executed
	p.addEvent("info", fmt.Sprintf("PreExecute: %s", ctx.Action), map[string]interface{}{
		"request_id": ctx.RequestID,
		"action":     ctx.Action,
		"params":     ctx.Params,
	})

	// For certain actions, we might want to fetch additional context
	// This is async so we don't block the response
	go func() {
		// === EXAMPLE: Validation for query actions ===
		if ctx.Action == "heimdall.watcher.query" {
			if cypher, ok := ctx.Params["cypher"].(string); ok {
				// Basic safety check
				if len(cypher) > 10000 {
					// Send warning notification (async)
					ctx.NotifyWarning("Query Validation", "Query too long, aborting")

					done(heimdall.PreExecuteResult{
						Continue:     false,
						AbortMessage: "Query too long (max 10000 chars)",
					})
					return
				}

				// === EXAMPLE: Notify about query analysis ===
				ctx.NotifyProgress("Query Analysis", "Validating Cypher query...")
			}
		}

		// === EXAMPLE: Cancel via context method (alternative to callback) ===
		// This is another way to cancel - useful when you want to
		// cancel from deep in nested code
		// if someCondition {
		//     ctx.Cancel("Validation failed", "PreExecute:watcher")
		//     done(heimdall.PreExecuteResult{Continue: false})
		//     return
		// }

		// Default: continue with execution
		done(heimdall.PreExecuteResult{
			Continue: true,
		})
	}()
}

// PostExecute is called after action execution completes.
// We log metrics and update state here.
//
// This demonstrates:
// - Logging execution metrics
// - Sending completion notifications to UI
// - Tracking error counts
// - Accessing execution timing from context
func (p *WatcherPlugin) PostExecute(ctx *heimdall.PostExecuteContext) {
	p.mu.Lock()
	defer p.mu.Unlock()

	log.Printf("[Watcher] PostExecute: request=%s action=%s duration=%v", ctx.RequestID, ctx.Action, ctx.Duration)

	// === EXAMPLE: Check if request was cancelled in earlier phase ===
	if ctx.WasCancelled && ctx.CancellationInfo != nil {
		p.addEvent("warning", fmt.Sprintf("Request was cancelled: %s", ctx.CancellationInfo.Reason), map[string]interface{}{
			"request_id":   ctx.RequestID,
			"cancelled_by": ctx.CancellationInfo.CancelledBy,
			"phase":        ctx.CancellationInfo.Phase,
		})
		return
	}

	// Log execution metrics
	executionTime := float64(ctx.Duration.Microseconds()) / 1000
	p.addEvent("info", fmt.Sprintf("PostExecute: %s (%.2fms)", ctx.Action, executionTime), map[string]interface{}{
		"request_id": ctx.RequestID,
		"action":     ctx.Action,
		"duration":   ctx.Duration.String(),
		"success":    ctx.Result != nil && ctx.Result.Success,
	})

	// Track errors
	if ctx.Result != nil && !ctx.Result.Success {
		p.errors++
	}

	// === Send completion notification inline ===
	// PostExecute notifications are queued and sent after the action result
	if ctx.Result != nil && ctx.Result.Success {
		ctx.NotifySuccess("Watcher", fmt.Sprintf("Action completed in %.2fms", executionTime))
	} else if ctx.Result != nil {
		ctx.NotifyError("Watcher", fmt.Sprintf("Action failed: %s", ctx.Result.Message))
	}
}

// === Internal Helpers ===

func (p *WatcherPlugin) addEvent(eventType, message string, data map[string]interface{}) {
	event := heimdall.SubsystemEvent{
		Time:    time.Now(),
		Type:    eventType,
		Message: message,
		Data:    data,
	}

	p.events = append(p.events, event)

	// Keep only last 100 events
	if len(p.events) > 100 {
		p.events = p.events[len(p.events)-100:]
	}
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}

func max(a, b int64) int64 {
	if a > b {
		return a
	}
	return b
}

// =============================================================================
// DatabaseEventHook Implementation - Autonomous Action Triggering
// =============================================================================

// OnDatabaseEvent is called when database operations occur.
// This demonstrates AUTONOMOUS ACTION INVOCATION:
// - Accumulates events over time
// - When thresholds are exceeded, triggers SLM analysis automatically
// - Uses HeimdallInvoker to invoke actions without user prompting
//
// Example scenarios:
//  1. Multiple query failures → trigger anomaly detection
//  2. High node creation rate → trigger memory curation
//  3. Security-related events → trigger security analysis
func (p *WatcherPlugin) OnDatabaseEvent(event *heimdall.DatabaseEvent) {
	p.mu.Lock()
	defer p.mu.Unlock()

	// === Track Query Failures ===
	if event.Type == heimdall.EventQueryFailed {
		// Reset counter every 5 minutes
		if time.Since(p.lastFailureReset) > 5*time.Minute {
			p.queryFailures = 0
			p.lastFailureReset = time.Now()
		}
		p.queryFailures++

		// AUTONOMOUS ACTION: After 5 failures in 5 minutes, analyze
		if p.queryFailures >= 5 && p.ctx.Heimdall != nil {
			log.Printf("[Watcher] Autonomous action: %d query failures detected, triggering analysis", p.queryFailures)

			// Option 1: Directly invoke an action
			p.ctx.Heimdall.InvokeActionAsync("heimdall.watcher.status", map[string]interface{}{
				"trigger":  "autonomous",
				"reason":   "query_failures",
				"failures": p.queryFailures,
			})

			// Reset counter after triggering
			p.queryFailures = 0
			p.lastFailureReset = time.Now()

			// Log the autonomous action
			p.addEvent("info", "Autonomous analysis triggered due to query failures", map[string]interface{}{
				"failures_count": p.queryFailures,
				"time_window":    "5m",
			})
		}
	}

	// === Track High Node Creation Rate ===
	if event.Type == heimdall.EventNodeCreated {
		// Reset counter every minute
		if time.Since(p.lastCreationReset) > time.Minute {
			p.nodeCreations = 0
			p.lastCreationReset = time.Now()
		}
		p.nodeCreations++

		// AUTONOMOUS ACTION: After 1000 nodes/minute, notify about high creation rate
		if p.nodeCreations >= 1000 && p.ctx.Bifrost != nil && p.ctx.Bifrost.IsConnected() {
			log.Printf("[Watcher] Autonomous notification: High node creation rate (%d/min)", p.nodeCreations)

			// Send notification to connected clients
			p.ctx.Bifrost.SendNotification("warning", "High Activity",
				fmt.Sprintf("Detected %d node creations in the last minute", p.nodeCreations))

			// Option 2: Send a natural language prompt to the SLM
			// The SLM will interpret this and decide what action to take
			if p.ctx.Heimdall != nil {
				p.ctx.Heimdall.SendPromptAsync(
					fmt.Sprintf("Analyze high node creation rate: %d nodes created in 1 minute. Should we investigate?",
						p.nodeCreations))
			}

			// Reset counter after notification
			p.nodeCreations = 0
			p.lastCreationReset = time.Now()
		}
	}

	// === Log interesting events ===
	switch event.Type {
	case heimdall.EventNodeDeleted:
		p.addEvent("info", "Node deleted", map[string]interface{}{
			"node_id": event.NodeID,
			"labels":  event.NodeLabels,
		})
	case heimdall.EventQueryExecuted:
		if event.Duration > 5*time.Second {
			p.addEvent("warning", "Slow query detected", map[string]interface{}{
				"query":    event.Query,
				"duration": event.Duration.String(),
			})
		}
	}
}
