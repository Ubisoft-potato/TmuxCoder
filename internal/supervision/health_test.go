package supervision

import "testing"

func TestPaneHealthString(t *testing.T) {
	tests := []struct {
		name     string
		health   PaneHealth
		expected string
	}{
		{
			name:     "Healthy pane",
			health:   PaneHealthy,
			expected: "Healthy",
		},
		{
			name:     "Dead pane",
			health:   PaneDead,
			expected: "Dead",
		},
		{
			name:     "Zombie pane",
			health:   PaneZombie,
			expected: "Zombie",
		},
		{
			name:     "Missing pane",
			health:   PaneMissing,
			expected: "Missing",
		},
		{
			name:     "Unknown status",
			health:   PaneHealth(99),
			expected: "Unknown",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := tt.health.String()
			if got != tt.expected {
				t.Errorf("PaneHealth.String() = %v, want %v", got, tt.expected)
			}
		})
	}
}

func TestNewPaneHealthChecker(t *testing.T) {
	tmuxCmd := "tmux"
	sessionName := "test-session"

	checker := NewPaneHealthChecker(tmuxCmd, sessionName)

	if checker == nil {
		t.Fatal("NewPaneHealthChecker() returned nil")
	}

	if checker.tmuxCommand != tmuxCmd {
		t.Errorf("tmuxCommand = %v, want %v", checker.tmuxCommand, tmuxCmd)
	}

	if checker.sessionName != sessionName {
		t.Errorf("sessionName = %v, want %v", checker.sessionName, sessionName)
	}
}

func TestCheckAllPanesHealth(t *testing.T) {
	// This test verifies the structure works correctly
	// Actual tmux interaction tests would require integration testing
	checker := NewPaneHealthChecker("tmux", "test-session")

	// Empty input should return empty map
	result := checker.CheckAllPanesHealth([]string{})
	if len(result) != 0 {
		t.Errorf("CheckAllPanesHealth([]) returned non-empty map: %v", result)
	}
}
