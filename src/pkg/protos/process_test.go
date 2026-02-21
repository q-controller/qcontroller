package protos

import (
	"testing"

	settingsv1 "github.com/q-controller/qcontroller/src/generated/settings/v1"
)

func TestParseGuestStats(t *testing.T) {
	tests := []struct {
		name     string
		input    string
		expected *settingsv1.MemoryStats
	}{
		{
			name: "valid stats",
			input: `{
				"stats": {
					"stat-total-memory": 1006845952,
					"stat-available-memory": 793726976,
					"stat-free-memory": 656433152,
					"stat-disk-caches": 210960384
				},
				"last-update": 1740000000
			}`,
			expected: &settingsv1.MemoryStats{
				TotalMemory:     1006845952,
				AvailableMemory: 793726976,
				FreeMemory:      656433152,
				DiskCaches:      210960384,
			},
		},
		{
			name:     "last-update zero returns nil",
			input:    `{"stats": {"stat-total-memory": 100}, "last-update": 0}`,
			expected: nil,
		},
		{
			name:     "missing last-update returns nil",
			input:    `{"stats": {"stat-total-memory": 100}}`,
			expected: nil,
		},
		{
			name:     "malformed JSON returns nil",
			input:    `{invalid`,
			expected: nil,
		},
		{
			name:     "empty object returns nil",
			input:    `{}`,
			expected: nil,
		},
		{
			name: "partial stats with valid last-update",
			input: `{
				"stats": {
					"stat-total-memory": 1006845952
				},
				"last-update": 1740000000
			}`,
			expected: &settingsv1.MemoryStats{
				TotalMemory: 1006845952,
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := parseGuestStats([]byte(tt.input))

			if tt.expected == nil {
				if result != nil {
					t.Errorf("expected nil, got %+v", result)
				}
				return
			}

			if result == nil {
				t.Fatal("expected non-nil result, got nil")
			}
			if result.TotalMemory != tt.expected.TotalMemory {
				t.Errorf("TotalMemory: got %d, want %d", result.TotalMemory, tt.expected.TotalMemory)
			}
			if result.AvailableMemory != tt.expected.AvailableMemory {
				t.Errorf("AvailableMemory: got %d, want %d", result.AvailableMemory, tt.expected.AvailableMemory)
			}
			if result.FreeMemory != tt.expected.FreeMemory {
				t.Errorf("FreeMemory: got %d, want %d", result.FreeMemory, tt.expected.FreeMemory)
			}
			if result.DiskCaches != tt.expected.DiskCaches {
				t.Errorf("DiskCaches: got %d, want %d", result.DiskCaches, tt.expected.DiskCaches)
			}
		})
	}
}
