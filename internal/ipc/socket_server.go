package ipc

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/opencode/tmux_coder/internal/interfaces"
	"github.com/opencode/tmux_coder/internal/types"
)

// SocketServer manages Unix Domain Socket server for inter-panel communication
type SocketServer struct {
	socketPath     string
	listener       net.Listener
	connections    map[string]*ClientConnection
	connectionsMux sync.RWMutex
	eventBus       interfaces.EventBus
	stateManager   interfaces.StateManager
	control        interfaces.OrchestratorControl
	ctx            context.Context
	cancel         context.CancelFunc
	isRunning      bool
	runningMux     sync.RWMutex
}

// ClientConnection represents a connected panel client
type ClientConnection struct {
	ID           string        `json:"id"`
	PanelType    string        `json:"panel_type"`
	PanelID      string        `json:"panel_id"`
	Conn         net.Conn      `json:"-"`
	ConnectedAt  time.Time     `json:"connected_at"`
	LastSeen     time.Time     `json:"last_seen"`
	MessageCount int64         `json:"message_count"`
	encoder      *json.Encoder `json:"-"`
	decoder      *json.Decoder `json:"-"`
	sendMutex    sync.Mutex    // To synchronize writes to the connection
}

// send safely writes a message to the client connection.
func (cc *ClientConnection) send(message IPCMessage) error {
	cc.sendMutex.Lock()
	defer cc.sendMutex.Unlock()

	if cc.Conn == nil {
		return fmt.Errorf("cannot send message to nil connection for panel %s", cc.PanelID)
	}

	// Set a write deadline to prevent indefinite blocking.
	cc.Conn.SetWriteDeadline(time.Now().Add(10 * time.Second))
	defer cc.Conn.SetWriteDeadline(time.Time{})

	return cc.encoder.Encode(message)
}

// NewSocketServer creates a new Unix Domain Socket server
func NewSocketServer(socketPath string, eventBus interfaces.EventBus, stateManager interfaces.StateManager, control interfaces.OrchestratorControl) *SocketServer {
	ctx, cancel := context.WithCancel(context.Background())

	return &SocketServer{
		socketPath:   socketPath,
		connections:  make(map[string]*ClientConnection),
		eventBus:     eventBus,
		stateManager: stateManager,
		control:      control,
		ctx:          ctx,
		cancel:       cancel,
	}
}

// Start begins listening for client connections
func (server *SocketServer) Start() error {
	server.runningMux.Lock()
	defer server.runningMux.Unlock()

	if server.isRunning {
		return fmt.Errorf("server is already running")
	}

	// Remove existing socket file if it exists
	if err := server.cleanupSocket(); err != nil {
		return fmt.Errorf("failed to cleanup existing socket: %w", err)
	}

	// Create directory for socket if it doesn't exist
	socketDir := filepath.Dir(server.socketPath)
	if err := os.MkdirAll(socketDir, 0755); err != nil {
		return fmt.Errorf("failed to create socket directory: %w", err)
	}

	// Start listening on Unix Domain Socket
	listener, err := net.Listen("unix", server.socketPath)
	if err != nil {
		return fmt.Errorf("failed to listen on socket: %w", err)
	}

	server.listener = listener
	server.isRunning = true

	log.Printf("IPC server started listening on %s", server.socketPath)

	// Start accepting connections in a separate goroutine
	go server.acceptConnections()

	return nil
}

// Stop gracefully shuts down the server
func (server *SocketServer) Stop() error {
	server.runningMux.Lock()
	defer server.runningMux.Unlock()

	if !server.isRunning {
		return nil
	}

	log.Printf("Stopping IPC server")

	// Cancel context to signal shutdown
	server.cancel()

	// Close all client connections
	server.connectionsMux.Lock()
	for _, conn := range server.connections {
		conn.Conn.Close()
	}
	server.connections = make(map[string]*ClientConnection)
	server.connectionsMux.Unlock()

	// Close listener
	if server.listener != nil {
		server.listener.Close()
	}

	// Cleanup socket file
	server.cleanupSocket()

	server.isRunning = false
	log.Printf("IPC server stopped")

	return nil
}

// acceptConnections handles incoming client connections
func (server *SocketServer) acceptConnections() {
	for {
		select {
		case <-server.ctx.Done():
			return
		default:
			// Set a timeout for Accept to allow periodic context checking
			if unixListener, ok := server.listener.(*net.UnixListener); ok {
				unixListener.SetDeadline(time.Now().Add(1 * time.Second))
			}

			conn, err := server.listener.Accept()
			if err != nil {
				if netErr, ok := err.(net.Error); ok && netErr.Timeout() {
					continue // This is expected, just loop again
				}
				if server.ctx.Err() != nil {
					return // Server is stopping
				}
				log.Printf("Error accepting connection: %v", err)
				continue
			}

			// Handle new connection in a separate goroutine
			go server.handleConnection(conn)
		}
	}
}

// handleConnection processes a new client connection
func (server *SocketServer) handleConnection(conn net.Conn) {
	defer conn.Close()

	// Set initial deadline for handshake
	conn.SetReadDeadline(time.Now().Add(10 * time.Second))

	decoder := json.NewDecoder(conn)
	encoder := json.NewEncoder(conn)

	// Wait for handshake message
	var handshake HandshakeMessage
	if err := decoder.Decode(&handshake); err != nil {
		log.Printf("Failed to decode handshake: %v", err)
		return
	}

	// Validate handshake
	if handshake.Type != "handshake" || handshake.PanelID == "" || handshake.PanelType == "" {
		log.Printf("Invalid handshake: %+v", handshake)
		// Note: Cannot use the safe send method here as clientConn is not yet created
		// and this is a raw response, not an IPCMessage.
		encoder.Encode(HandshakeResponse{Success: false, Error: "Invalid handshake"})
		return
	}

	// Create client connection object
	clientConn := &ClientConnection{
		ID:          fmt.Sprintf("%s-%d", handshake.PanelID, time.Now().UnixNano()),
		PanelType:   handshake.PanelType,
		PanelID:     handshake.PanelID,
		Conn:        conn,
		ConnectedAt: time.Now(),
		LastSeen:    time.Now(),
		encoder:     encoder,
		decoder:     decoder,
	}

	// Send handshake response (raw, not wrapped in IPCMessage)
	handshakeResponse := HandshakeResponse{
		Type:         "handshake_response",
		Success:      true,
		ConnectionID: clientConn.ID,
		ServerTime:   time.Now(),
	}
	if err := encoder.Encode(handshakeResponse); err != nil {
		log.Printf("Failed to send handshake response: %v", err)
		return
	}

	// Register connection
	server.connectionsMux.Lock()
	server.connections[clientConn.ID] = clientConn
	server.connectionsMux.Unlock()
	log.Printf("Panel %s (%s) connected with ID %s", clientConn.PanelID, clientConn.PanelType, clientConn.ID)

	// Subscribe to event bus
	eventChan := make(chan types.StateEvent, 100)
	server.eventBus.Subscribe(clientConn.ID, clientConn.PanelID, clientConn.PanelType, eventChan)

	// Start event forwarding goroutine
	go server.forwardEvents(clientConn, eventChan)

	// Remove read deadline for normal operation
	conn.SetReadDeadline(time.Time{})

	// Handle messages from this client in a blocking loop
	server.handleClientMessages(clientConn)

	// Cleanup on disconnect
	server.disconnectClient(clientConn, "client message loop ended")
}

// handleClientMessages processes incoming messages from a client
func (server *SocketServer) handleClientMessages(clientConn *ClientConnection) {
	for {
		select {
		case <-server.ctx.Done():
			return
		default:
			// Set a read timeout to allow periodic context checking
			clientConn.Conn.SetReadDeadline(time.Now().Add(15 * time.Second)) // Increased timeout

			var message IPCMessage
			err := clientConn.decoder.Decode(&message)
			if err != nil {
				if netErr, ok := err.(net.Error); ok && netErr.Timeout() {
					// This is an expected timeout when client is idle, not an error.
					// We can add a verbose log here if needed for debugging.
					// log.Printf("[SERVER] Read timeout for client %s. Looping.", clientConn.ID)
					continue
				}
				log.Printf("Error reading from client %s: %v", clientConn.ID, err)
				return // Real error or EOF, close connection
			}

			clientConn.LastSeen = time.Now()
			clientConn.MessageCount++

			// Process the message in a new goroutine to avoid blocking the read loop
			go server.processClientMessage(clientConn, message)
		}
	}
}

// processClientMessage handles a message from a client
func (server *SocketServer) processClientMessage(clientConn *ClientConnection, message IPCMessage) {
	log.Printf("[SERVER] Received message of type '%s' from client %s (%s)", message.Type, clientConn.PanelID, clientConn.ID)
	switch message.Type {
	case "state_update":
		server.handleStateUpdate(clientConn, message)
	case "state_request":
		server.handleStateRequest(clientConn, message)
	case "clear_session_messages":
		server.handleClearSessionMessages(clientConn, message)
	case "ping":
		server.handlePing(clientConn, message)
	case "orchestrator_command":
		server.handleOrchestratorCommand(clientConn, message)
	default:
		log.Printf("Unknown message type from client %s: %s", clientConn.ID, message.Type)
		server.sendError(clientConn, "unknown message type")
	}
}

// handleStateUpdate processes a state update from a client
func (server *SocketServer) handleStateUpdate(clientConn *ClientConnection, message IPCMessage) {
	var update types.StateUpdate
	if err := mapToStruct(message.Data, &update); err != nil {
		log.Printf("Failed to decode state update: %v", err)
		server.sendError(clientConn, "invalid state update")
		return
	}

	update.SourcePanel = clientConn.PanelID

	err := server.stateManager.UpdateWithVersionCheck(update)
	if err != nil {
		log.Printf("Failed to apply state update: %v", err)
		server.sendErrorMessage(clientConn, "state_update_error", err.Error(), message.RequestID)
		return
	}

	response := IPCMessage{
		Type:      "state_update_response",
		RequestID: message.RequestID,
		Data: map[string]interface{}{
			"success": true,
			"version": server.stateManager.GetState().GetCurrentVersion(),
		},
		Timestamp: time.Now(),
	}
	log.Printf("[SERVER] Sending state_update_response id=%s version=%d to panel=%s", message.RequestID, server.stateManager.GetState().GetCurrentVersion(), clientConn.PanelID)
	if err := clientConn.send(response); err != nil {
		log.Printf("Failed to send state update success response: %v", err)
	}
}

// handleStateRequest processes a state request from a client
func (server *SocketServer) handleStateRequest(clientConn *ClientConnection, message IPCMessage) {
	currentState := server.stateManager.GetState()
	if currentState == nil {
		server.sendError(clientConn, "state not available")
		return
	}

	// Log state details for debugging
	log.Printf("[IPC] State request from panel %s (%s): CurrentSessionID=%s, Sessions=%d, Messages=%d",
		clientConn.PanelID, clientConn.PanelType, currentState.CurrentSessionID,
		len(currentState.Sessions), len(currentState.Messages))

	response := IPCMessage{
		Type:      "state_response",
		RequestID: message.RequestID,
		Data:      currentState.Clone(),
		Timestamp: time.Now(),
	}
	if err := clientConn.send(response); err != nil {
		log.Printf("Failed to send state response: %v", err)
	}
}

// handleClearSessionMessages processes clear session messages request
func (server *SocketServer) handleClearSessionMessages(clientConn *ClientConnection, message IPCMessage) {
	var requestData map[string]interface{}
	if err := mapToStruct(message.Data, &requestData); err != nil {
		log.Printf("Failed to decode clear messages request: %v", err)
		server.sendError(clientConn, "invalid request")
		return
	}

	sessionID, ok := requestData["session_id"].(string)
	if !ok || sessionID == "" {
		server.sendError(clientConn, "session_id is required")
		return
	}

	panelID := clientConn.PanelID
	if pid, ok := requestData["panel_id"].(string); ok && pid != "" {
		panelID = pid
	}

	// Call syncManager to clear session messages
	if err := server.stateManager.ClearSessionMessages(sessionID, panelID); err != nil {
		log.Printf("Failed to clear session messages: %v", err)
		server.sendErrorMessage(clientConn, "error", err.Error(), message.RequestID)
		return
	}

	// Send success response
	response := IPCMessage{
		Type:      "clear_session_messages_response",
		RequestID: message.RequestID,
		Data: map[string]interface{}{
			"success":    true,
			"session_id": sessionID,
		},
		Timestamp: time.Now(),
	}

	if err := clientConn.send(response); err != nil {
		log.Printf("Failed to send clear messages response: %v", err)
	}
}

// handlePing processes a ping message from a client
func (server *SocketServer) handlePing(clientConn *ClientConnection, message IPCMessage) {
	response := IPCMessage{
		Type:      "pong",
		RequestID: message.RequestID,
		Timestamp: time.Now(),
	}
	if err := clientConn.send(response); err != nil {
		log.Printf("Failed to send pong: %v", err)
	}
}

// forwardEvents forwards state events to a client
func (server *SocketServer) forwardEvents(clientConn *ClientConnection, eventChan chan types.StateEvent) {
	for {
		event, ok := <-eventChan
		if !ok {
			log.Printf("Event channel closed for client %s; disconnecting", clientConn.ID)
			server.disconnectClient(clientConn, "event channel closed")
			return
		}

		message := IPCMessage{
			Type:      "state_event",
			Data:      event,
			Timestamp: time.Now(),
		}
		if err := clientConn.send(message); err != nil {
			log.Printf("Failed to forward event to client %s: %v", clientConn.ID, err)
			server.disconnectClient(clientConn, fmt.Sprintf("event forwarding failed: %v", err))
			return
		}
	}
}

// disconnectClient removes a client connection, unsubscribes it from the event bus,
// and closes the underlying socket. It returns true if the connection was active.
func (server *SocketServer) disconnectClient(clientConn *ClientConnection, reason string) bool {
	if clientConn == nil {
		return false
	}

	removed := false

	server.connectionsMux.Lock()
	if existing, exists := server.connections[clientConn.ID]; exists && existing == clientConn {
		delete(server.connections, clientConn.ID)
		removed = true
	}
	server.connectionsMux.Unlock()

	server.eventBus.Unsubscribe(clientConn.ID)

	clientConn.sendMutex.Lock()
	conn := clientConn.Conn
	clientConn.Conn = nil
	clientConn.sendMutex.Unlock()

	if conn != nil {
		if err := conn.Close(); err != nil && !errors.Is(err, net.ErrClosed) {
			log.Printf("Failed to close connection %s cleanly: %v", clientConn.ID, err)
		}
	}

	if removed {
		if reason != "" {
			log.Printf("Panel %s (%s) disconnected (reason: %s)", clientConn.PanelID, clientConn.PanelType, reason)
		} else {
			log.Printf("Panel %s (%s) disconnected", clientConn.PanelID, clientConn.PanelType)
		}
	}

	return removed
}

// sendError sends a generic error message to a client.
func (server *SocketServer) sendError(clientConn *ClientConnection, errorMsg string) {
	response := IPCMessage{
		Type:      "error",
		Data:      map[string]interface{}{"error": errorMsg},
		Timestamp: time.Now(),
	}
	if err := clientConn.send(response); err != nil {
		log.Printf("Failed to send error message: %v", err)
	}
}

// sendErrorMessage sends a structured error message to a client.
func (server *SocketServer) sendErrorMessage(clientConn *ClientConnection, messageType, errorMsg, requestID string) {
	response := IPCMessage{
		Type:      messageType,
		RequestID: requestID,
		Data: map[string]interface{}{
			"success": false,
			"error":   errorMsg,
		},
		Timestamp: time.Now(),
	}
	if err := clientConn.send(response); err != nil {
		log.Printf("Failed to send structured error message: %v", err)
	}
}

// handleOrchestratorCommand processes control commands sent to the orchestrator via IPC.
func (server *SocketServer) handleOrchestratorCommand(clientConn *ClientConnection, message IPCMessage) {
	if server.control == nil {
		server.sendErrorMessage(clientConn, "orchestrator_command_response", "control handler not configured", message.RequestID)
		return
	}

	var payload struct {
		Command string                 `json:"command"`
		Params  map[string]interface{} `json:"params"`
	}
	if err := mapToStruct(message.Data, &payload); err != nil || strings.TrimSpace(payload.Command) == "" {
		server.sendErrorMessage(clientConn, "orchestrator_command_response", "invalid command payload", message.RequestID)
		return
	}

	switch strings.ToLower(payload.Command) {
	case "reload_layout":
		if err := server.control.ReloadLayout(); err != nil {
			log.Printf("Reload layout command failed: %v", err)
			server.sendErrorMessage(clientConn, "orchestrator_command_response", err.Error(), message.RequestID)
			return
		}

	case "shutdown", "shutdown:cleanup":
		// Extract cleanup parameter from command suffix or params
		cleanup := false
		if strings.HasSuffix(strings.ToLower(payload.Command), ":cleanup") {
			cleanup = true
		} else if payload.Params != nil {
			if c, ok := payload.Params["cleanup"].(bool); ok {
				cleanup = c
			}
		}

		log.Printf("Shutdown command received (cleanup=%v)", cleanup)

		// Send success response before shutting down
		response := IPCMessage{
			Type:      "orchestrator_command_response",
			RequestID: message.RequestID,
			Data: map[string]interface{}{
				"success": true,
				"command": "shutdown",
				"cleanup": cleanup,
			},
			Timestamp: time.Now(),
		}
		if err := clientConn.send(response); err != nil {
			log.Printf("Failed to send shutdown response: %v", err)
		}

		// Trigger shutdown asynchronously to allow response to be sent
		go func() {
			time.Sleep(100 * time.Millisecond) // Give time for response to be sent
			if err := server.control.Shutdown(cleanup); err != nil {
				log.Printf("Shutdown command failed: %v", err)
			}
		}()
		return

	default:
		server.sendErrorMessage(clientConn, "orchestrator_command_response", "unsupported command", message.RequestID)
		return
	}

	response := IPCMessage{
		Type:      "orchestrator_command_response",
		RequestID: message.RequestID,
		Data: map[string]interface{}{
			"success": true,
			"command": strings.ToLower(payload.Command),
		},
		Timestamp: time.Now(),
	}
	if err := clientConn.send(response); err != nil {
		log.Printf("Failed to send orchestrator command response: %v", err)
	}
}

// cleanupSocket removes the socket file if it exists
func (server *SocketServer) cleanupSocket() error {
	if _, err := os.Stat(server.socketPath); err == nil {
		return os.Remove(server.socketPath)
	}
	return nil
}

// GetConnections returns information about all active connections
func (server *SocketServer) GetConnections() map[string]*ClientConnection {
	server.connectionsMux.RLock()
	defer server.connectionsMux.RUnlock()

	connections := make(map[string]*ClientConnection)
	for id, conn := range server.connections {
		// Create a copy without the connection object
		connections[id] = &ClientConnection{
			ID:           conn.ID,
			PanelType:    conn.PanelType,
			PanelID:      conn.PanelID,
			ConnectedAt:  conn.ConnectedAt,
			LastSeen:     conn.LastSeen,
			MessageCount: conn.MessageCount,
		}
	}
	return connections
}

// IsRunning returns true if the server is currently running
func (server *SocketServer) IsRunning() bool {
	server.runningMux.RLock()
	defer server.runningMux.RUnlock()
	return server.isRunning
}

// ConnectionCount returns the number of active IPC connections
// This method is thread-safe and can be called concurrently
func (server *SocketServer) ConnectionCount() int {
	server.connectionsMux.RLock()
	defer server.connectionsMux.RUnlock()
	return len(server.connections)
}
