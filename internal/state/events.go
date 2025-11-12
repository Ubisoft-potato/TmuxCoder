package state

import (
	"fmt"
	"log"
	"sync"
	"time"

	"github.com/opencode/tmux_coder/internal/interfaces"
	"github.com/opencode/tmux_coder/internal/types"
)

// EventBus manages event distribution across panels
type EventBus struct {
	subscribers    map[string]chan types.StateEvent
	subscriberMeta map[string]interfaces.SubscriberInfo
	mutex          sync.RWMutex
	eventHistory   []types.StateEvent
	maxHistory     int
}

// NewEventBus creates a new event bus for state notifications
func NewEventBus(maxHistory int) *EventBus {
	return &EventBus{
		subscribers:    make(map[string]chan types.StateEvent),
		subscriberMeta: make(map[string]interfaces.SubscriberInfo),
		eventHistory:   make([]types.StateEvent, 0, maxHistory),
		maxHistory:     maxHistory,
	}
}

// Subscribe registers a panel for state change notifications
func (bus *EventBus) Subscribe(connectionID, panelID, panelType string, eventChan chan types.StateEvent) {
	bus.mutex.Lock()
	defer bus.mutex.Unlock()

	// Remove any existing subscriptions for this connection ID
	bus.removeSubscriberLocked(connectionID, fmt.Sprintf("connection %s re-registered", connectionID))

	// Remove stale subscriptions for the same panel ID to avoid overlap
	var staleConnections []string
	for existingConnID, meta := range bus.subscriberMeta {
		if meta.PanelID == panelID {
			staleConnections = append(staleConnections, existingConnID)
		}
	}
	for _, staleConnID := range staleConnections {
		bus.removeSubscriberLocked(staleConnID, fmt.Sprintf("panel %s replaced by connection %s", panelID, connectionID))
	}

	bus.subscribers[connectionID] = eventChan
	bus.subscriberMeta[connectionID] = interfaces.SubscriberInfo{
		ConnectionID: connectionID,
		PanelID:      panelID,
		PanelType:    panelType,
		ConnectedAt:  time.Now(),
		EventCount:   0,
	}

	log.Printf("Panel %s (%s) subscribed to events with connection %s", panelID, panelType, connectionID)

	// Notify other panels about new connection
	connectEvent := types.StateEvent{
		ID:          generateEventID(),
		Type:        types.EventPanelConnected,
		Data:        types.PanelConnectionPayload{PanelID: panelID, PanelType: panelType},
		SourcePanel: "system",
		Timestamp:   time.Now(),
	}
	bus.broadcastUnsafe(connectEvent, panelID)
}

// Unsubscribe removes a panel from event notifications
func (bus *EventBus) Unsubscribe(connectionID string) {
	bus.mutex.Lock()
	defer bus.mutex.Unlock()

	bus.removeSubscriberLocked(connectionID, "")
}

// Broadcast sends events to all registered panels except the source
func (bus *EventBus) Broadcast(event types.StateEvent) {
	bus.mutex.Lock()
	defer bus.mutex.Unlock()

	bus.broadcastUnsafe(event, event.SourcePanel)
}

// broadcastUnsafe sends events without acquiring locks (caller must hold lock)
func (bus *EventBus) broadcastUnsafe(event types.StateEvent, excludePanel string) {
	// Add to event history
	bus.addToHistoryUnsafe(event)

	type pendingRemoval struct {
		connectionID string
		reason       string
	}

	var toRemove []pendingRemoval

	// Send to all subscribers except the source panel
	for connectionID, eventChan := range bus.subscribers {
		meta, hasMeta := bus.subscriberMeta[connectionID]
		if hasMeta && meta.PanelID == excludePanel {
			continue
		}

		if hasMeta {
			meta.LastEventAt = time.Now()
			meta.EventCount++
			meta.ConnectionID = connectionID
			bus.subscriberMeta[connectionID] = meta
		}

		// Try to send event (non-blocking)
		select {
		case eventChan <- event:
			// Event delivered successfully
		default:
			// Channel full, log warning but continue
			panelLabel := fmt.Sprintf("connection:%s", connectionID)
			if hasMeta && meta.PanelID != "" {
				panelLabel = meta.PanelID
			}
			log.Printf("Warning: Event channel full for %s (connection %s), dropping event %s and disconnecting subscriber",
				panelLabel, connectionID, event.Type)

			reason := fmt.Sprintf("event channel overflow while delivering %s", event.Type)
			toRemove = append(toRemove, pendingRemoval{
				connectionID: connectionID,
				reason:       reason,
			})
		}
	}

	for _, removal := range toRemove {
		bus.removeSubscriberLocked(removal.connectionID, removal.reason)
	}
}

// BroadcastToPanel sends an event specifically to one panel
func (bus *EventBus) BroadcastToPanel(event types.StateEvent, targetPanel string) {
	bus.mutex.Lock()
	defer bus.mutex.Unlock()

	for connectionID, eventChan := range bus.subscribers {
		meta, exists := bus.subscriberMeta[connectionID]
		if !exists || meta.PanelID != targetPanel {
			continue
		}

		meta.LastEventAt = time.Now()
		meta.EventCount++
		meta.ConnectionID = connectionID
		bus.subscriberMeta[connectionID] = meta

		select {
		case eventChan <- event:
			// Event delivered successfully
		default:
			log.Printf("Warning: Event channel full for panel %s (connection %s), dropping targeted event %s",
				targetPanel, connectionID, event.Type)
		}
	}
}

// GetSubscribers returns information about all current subscribers
func (bus *EventBus) GetSubscribers() map[string]interfaces.SubscriberInfo {
	bus.mutex.RLock()
	defer bus.mutex.RUnlock()

	subscribers := make(map[string]interfaces.SubscriberInfo)
	for connectionID, info := range bus.subscriberMeta {
		subscribers[connectionID] = info
	}
	return subscribers
}

// removeSubscriberLocked removes a subscriber while holding the bus mutex.
func (bus *EventBus) removeSubscriberLocked(connectionID string, reason string) {
	eventChan, exists := bus.subscribers[connectionID]
	if !exists {
		return
	}

	meta, metaExists := bus.subscriberMeta[connectionID]
	if !metaExists {
		meta = interfaces.SubscriberInfo{
			ConnectionID: connectionID,
		}
	} else {
		meta.ConnectionID = connectionID
	}

	delete(bus.subscribers, connectionID)
	delete(bus.subscriberMeta, connectionID)

	close(eventChan)

	if meta.PanelID == "" {
		if reason != "" {
			log.Printf("Removed event subscription for unidentified connection %s (reason: %s)", connectionID, reason)
		} else {
			log.Printf("Removed event subscription for unidentified connection %s", connectionID)
		}
		return
	}

	if reason != "" {
		log.Printf("Panel %s (%s) unsubscribed from events (connection %s, reason: %s)", meta.PanelID, meta.PanelType, connectionID, reason)
	} else {
		log.Printf("Panel %s (%s) unsubscribed from events (connection %s)", meta.PanelID, meta.PanelType, connectionID)
	}

	disconnectEvent := types.StateEvent{
		ID:          generateEventID(),
		Type:        types.EventPanelDisconnected,
		Data:        types.PanelConnectionPayload{PanelID: meta.PanelID, PanelType: meta.PanelType},
		SourcePanel: "system",
		Timestamp:   time.Now(),
	}
	bus.broadcastUnsafe(disconnectEvent, meta.PanelID)
}

// GetEventHistory returns recent events from the history buffer
func (bus *EventBus) GetEventHistory(maxEvents int) []types.StateEvent {
	bus.mutex.RLock()
	defer bus.mutex.RUnlock()

	historyLen := len(bus.eventHistory)
	if maxEvents <= 0 || maxEvents > historyLen {
		maxEvents = historyLen
	}

	// Return the most recent events
	startIndex := historyLen - maxEvents
	events := make([]types.StateEvent, maxEvents)
	copy(events, bus.eventHistory[startIndex:])
	return events
}

// addToHistoryUnsafe adds an event to the history buffer (caller must hold lock)
func (bus *EventBus) addToHistoryUnsafe(event types.StateEvent) {
	bus.eventHistory = append(bus.eventHistory, event)

	// Maintain maximum history size
	if len(bus.eventHistory) > bus.maxHistory {
		// Remove oldest events
		copy(bus.eventHistory, bus.eventHistory[1:])
		bus.eventHistory = bus.eventHistory[:bus.maxHistory]
	}
}

// CreateEventFromUpdate converts a state update to a state event
func CreateEventFromUpdate(update types.StateUpdate, version int64) types.StateEvent {
	var eventType types.StateEventType

	// Map update types to event types
	switch update.Type {
	case types.SessionChanged:
		eventType = types.EventSessionChanged
	case types.SessionAdded:
		eventType = types.EventSessionAdded
	case types.SessionDeleted:
		eventType = types.EventSessionDeleted
	case types.SessionUpdated:
		eventType = types.EventSessionUpdated
	case types.MessageAdded:
		eventType = types.EventMessageAdded
	case types.MessageUpdated:
		eventType = types.EventMessageUpdated
	case types.MessageDeleted:
		eventType = types.EventMessageDeleted
	case types.MessagesCleared:
		eventType = types.EventMessagesCleared
	case types.InputUpdated:
		eventType = types.EventInputUpdated
	case types.CursorMoved:
		eventType = types.EventCursorMoved
	case types.ThemeChanged:
		eventType = types.EventThemeChanged
	case types.ModelChanged:
		eventType = types.EventModelChanged
	case types.AgentChanged:
		eventType = types.EventAgentChanged
	case types.UIActionTriggered:
		eventType = types.EventUIActionTriggered
	default:
		eventType = types.EventStateSync
	}

	return types.StateEvent{
		ID:          generateEventID(),
		Type:        eventType,
		Data:        update.Payload,
		Version:     version,
		SourcePanel: update.SourcePanel,
		Timestamp:   time.Now(),
	}
}

// generateEventID creates a unique identifier for events
func generateEventID() string {
	// Simple timestamp-based ID for now
	// In production, consider using UUID or other unique ID generation
	return time.Now().Format("20060102150405.000000")
}
