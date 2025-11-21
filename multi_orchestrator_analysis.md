# Multi-Orchestrator Architecture Analysis

## 1. Overview
The current architecture supports running multiple independent orchestrator instances simultaneously. Each instance manages a distinct Tmux session and is isolated via unique file paths and resource locks.

## 2. Isolation Mechanisms (Validated)
The following mechanisms ensure proper isolation between sessions:

*   **Session-Based Paths**:
    *   The `paths.NewPathManager(sessionName)` creates a unique directory context for each session.
    *   **Socket Path**: Derived from the session name (e.g., `.../opencode/sessions/<name>/opencode.sock`), ensuring IPC traffic doesn't cross sessions.
    *   **State Path**: State files are stored in session-specific directories, preventing state corruption between instances.

*   **Process Locking**:
    *   `session.AcquireLock(pathMgr.PIDPath())` uses a PID file specific to the session. This correctly prevents starting duplicate orchestrators for the *same* session while allowing different sessions to run in parallel.

*   **Tmux Session Isolation**:
    *   The orchestrator passes the `sessionName` to all `tmux` commands (e.g., `tmux new-session -s <name>`). This ensures that pane management and layout operations affect only the target session.

## 3. Identified Issues

### 🔴 Critical: Shared Log File Collision
**Location**: `cmd/opencode-tmux/main.go` (Lines 1867-1884)
**Issue**: The logging configuration is hardcoded to a single file:
```go
logPath := filepath.Join(logDir, "tmux.log")
```
**Impact**:
*   When multiple orchestrators run, they all write to the same `tmux.log`.
*   Logs will be interleaved, making debugging impossible.
*   Potential write contention (though `O_APPEND` mitigates data loss, readability is destroyed).

### ⚠️ Potential: Environment Variable Overrides
**Location**: `cmd/opencode-tmux/main.go`
**Issue**: The code respects `OPENCODE_SOCKET` and `OPENCODE_STATE` environment variables.
**Impact**: If a user exports these variables globally in their shell profile and then tries to run multiple sessions, all instances might try to use the same socket/state paths, breaking isolation.
**Recommendation**: This is likely intended for debugging, but it's worth noting that explicit flags or the automatic session-based path derivation is safer for multi-session usage.

## 4. Recommendations

1.  **Fix Logging (High Priority)**:
    Modify `main.go` to include the session name in the log file.
    ```go
    // Current
    logPath := filepath.Join(logDir, "tmux.log")

    // Recommended
    logPath := filepath.Join(logDir, fmt.Sprintf("tmux-%s.log", sessionName))
    ```

2.  **Theme Handling**:
    Verify that `theme.SetTheme` only modifies in-memory state or sends IPC events. If it writes to a global config file, it needs to be scoped to the session or removed.

3.  **Verification**:
    After applying the logging fix, run two instances:
    *   `opencode-tmux session1`
    *   `opencode-tmux session2`
    *   Verify `tmux-session1.log` and `tmux-session2.log` exist and contain distinct logs.
