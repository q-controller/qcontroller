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

func TestParseBlockInfo(t *testing.T) {
	tests := []struct {
		name     string
		input    string
		expected *settingsv1.DiskStats
	}{
		{
			name: "valid qcow2 block",
			input: `[{
				"device": "ide0-hd0",
				"type": "hd",
				"removable": false,
				"locked": false,
				"inserted": {
					"file": "/tmp/test.qcow2",
					"drv": "qcow2",
					"ro": false,
					"backing_file_depth": 0,
					"active": true,
					"encrypted": false,
					"detect_zeroes": "off",
					"bps": 0, "bps_rd": 0, "bps_wr": 0,
					"iops": 0, "iops_rd": 0, "iops_wr": 0,
					"write_threshold": 0,
					"cache": {"writeback": true, "direct": false, "no-flush": false},
					"image": {
						"filename": "/tmp/test.qcow2",
						"format": "qcow2",
						"virtual-size": 10737418240,
						"actual-size": 2147483648
					}
				}
			}]`,
			expected: &settingsv1.DiskStats{
				TotalBytes: 10737418240,
				UsedBytes:  2147483648,
			},
		},
		{
			name: "multiple blocks picks qcow2",
			input: `[
				{
					"device": "virtio0",
					"type": "hd",
					"removable": false,
					"locked": false,
					"inserted": {
						"file": "/tmp/cloud-init.iso",
						"drv": "raw",
						"ro": false,
						"backing_file_depth": 0,
						"active": true,
						"encrypted": false,
						"detect_zeroes": "off",
						"bps": 0, "bps_rd": 0, "bps_wr": 0,
						"iops": 0, "iops_rd": 0, "iops_wr": 0,
						"write_threshold": 0,
						"cache": {"writeback": true, "direct": false, "no-flush": false},
						"image": {
							"filename": "/tmp/cloud-init.iso",
							"format": "raw",
							"virtual-size": 393216,
							"actual-size": 393216
						}
					}
				},
				{
					"device": "ide0-hd0",
					"type": "hd",
					"removable": false,
					"locked": false,
					"inserted": {
						"file": "/tmp/test.qcow2",
						"drv": "qcow2",
						"ro": false,
						"backing_file_depth": 0,
						"active": true,
						"encrypted": false,
						"detect_zeroes": "off",
						"bps": 0, "bps_rd": 0, "bps_wr": 0,
						"iops": 0, "iops_rd": 0, "iops_wr": 0,
						"write_threshold": 0,
						"cache": {"writeback": true, "direct": false, "no-flush": false},
						"image": {
							"filename": "/tmp/test.qcow2",
							"format": "qcow2",
							"virtual-size": 10737418240,
							"actual-size": 3221225472
						}
					}
				}
			]`,
			expected: &settingsv1.DiskStats{
				TotalBytes: 10737418240,
				UsedBytes:  3221225472,
			},
		},
		{
			name: "no inserted block",
			input: `[{
				"device": "ide0-hd0",
				"type": "hd",
				"removable": true,
				"locked": false
			}]`,
			expected: nil,
		},
		{
			name: "missing actual-size",
			input: `[{
				"device": "ide0-hd0",
				"type": "hd",
				"removable": false,
				"locked": false,
				"inserted": {
					"file": "/tmp/test.qcow2",
					"drv": "qcow2",
					"ro": false,
					"backing_file_depth": 0,
					"active": true,
					"encrypted": false,
					"detect_zeroes": "off",
					"bps": 0, "bps_rd": 0, "bps_wr": 0,
					"iops": 0, "iops_rd": 0, "iops_wr": 0,
					"write_threshold": 0,
					"cache": {"writeback": true, "direct": false, "no-flush": false},
					"image": {
						"filename": "/tmp/test.qcow2",
						"format": "qcow2",
						"virtual-size": 10737418240
					}
				}
			}]`,
			expected: nil,
		},
		{
			name:     "malformed JSON",
			input:    `{invalid`,
			expected: nil,
		},
		{
			name:     "empty array",
			input:    `[]`,
			expected: nil,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := parseBlockInfo([]byte(tt.input))

			if tt.expected == nil {
				if result != nil {
					t.Errorf("expected nil, got %+v", result)
				}
				return
			}

			if result == nil {
				t.Fatal("expected non-nil result, got nil")
			}
			if result.TotalBytes != tt.expected.TotalBytes {
				t.Errorf("TotalBytes: got %d, want %d", result.TotalBytes, tt.expected.TotalBytes)
			}
			if result.UsedBytes != tt.expected.UsedBytes {
				t.Errorf("UsedBytes: got %d, want %d", result.UsedBytes, tt.expected.UsedBytes)
			}
		})
	}
}
