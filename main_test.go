package main

import (
	"strings"
	"testing"
)

func TestAppendData(t *testing.T) {
	tests := []struct {
		name     string
		data     []float64
		rtt      int64
		wantLen  int
		wantLast float64
	}{
		{
			name:     "append to empty",
			data:     []float64{},
			rtt:      10,
			wantLen:  1,
			wantLast: 10,
		},
		{
			name:     "append under max",
			data:     []float64{1, 2, 3},
			rtt:      4,
			wantLen:  4,
			wantLast: 4,
		},
		{
			name:     "append at max truncates oldest",
			data:     make([]float64, maxLen),
			rtt:      99,
			wantLen:  maxLen,
			wantLast: 99,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := appendData(tt.data, tt.rtt)
			if len(got) != tt.wantLen {
				t.Errorf("len = %d, want %d", len(got), tt.wantLen)
			}
			if got[len(got)-1] != tt.wantLast {
				t.Errorf("last = %f, want %f", got[len(got)-1], tt.wantLast)
			}
		})
	}
}

func TestRenderFrame(t *testing.T) {
	addresses := []string{"192.168.1.1", "1.1.1.1"}
	data := [][]float64{{5, 10, 15}, {8, 12, 18}}
	rtts := []int64{15, 18}
	maxRTT := int64(20)

	frame := renderFrame(addresses, data, rtts, maxRTT, 80)

	// Check header with addresses
	if !strings.Contains(frame, "Ping latency: 192.168.1.1 (gateway) vs 1.1.1.1 (CloudFlare DNS)") {
		t.Error("missing header")
	}

	// Check legends with RTT values
	if !strings.Contains(frame, "192.168.1.1: 15 ms") {
		t.Error("missing gateway RTT in legend")
	}
	if !strings.Contains(frame, "1.1.1.1: 18 ms") {
		t.Error("missing CloudFlare RTT in legend")
	}

	// Check footer
	if !strings.Contains(frame, "Press Control-C to exit") {
		t.Error("missing footer")
	}

	// Check graph has Y-axis labels (numbers)
	if !strings.Contains(frame, "20.00") && !strings.Contains(frame, "20") {
		t.Error("missing Y-axis max value")
	}
}

func TestRenderFrameScaleChanges(t *testing.T) {
	addresses := []string{"192.168.1.1", "1.1.1.1"}

	// First frame with low RTT
	data1 := [][]float64{{5, 10}, {8, 12}}
	frame1 := renderFrame(addresses, data1, []int64{10, 12}, 12, 80)

	// Second frame with higher RTT (scale change)
	data2 := [][]float64{{5, 10, 50}, {8, 12, 45}}
	frame2 := renderFrame(addresses, data2, []int64{50, 45}, 50, 80)

	// Both frames should be valid strings
	if len(frame1) == 0 || len(frame2) == 0 {
		t.Error("frames should not be empty")
	}

	// Frame2 should have higher scale
	if !strings.Contains(frame2, "50") {
		t.Error("frame2 should show scale up to 50")
	}
}
