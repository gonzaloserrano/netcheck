package main

import (
	"bytes"
	"context"
	"strings"
	"testing"
	"time"
)

// mockPingSource sends predetermined RTT values.
type mockPingSource struct {
	address string
	values  []int64
}

func (m *mockPingSource) Address() string { return m.address }

func (m *mockPingSource) Start(ctx context.Context) (<-chan int64, error) {
	ch := make(chan int64)
	go func() {
		defer close(ch)
		for _, v := range m.values {
			select {
			case <-ctx.Done():
				return
			case ch <- v:
			}
		}
	}()
	return ch, nil
}

// mockTerminal captures output for testing.
type mockTerminal struct {
	buf   bytes.Buffer
	width int
}

func (t *mockTerminal) Write(p []byte) (n int, err error) { return t.buf.Write(p) }
func (t *mockTerminal) Clear()                            {}
func (t *mockTerminal) MoveCursor(x, y int)               {}
func (t *mockTerminal) Flush()                            {}
func (t *mockTerminal) Width() int                        { return t.width }

func TestAppendData(t *testing.T) {
	t.Parallel()
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
			t.Parallel()
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
	t.Parallel()
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
	if !strings.Contains(frame, "Gateway: 15 ms") {
		t.Error("missing gateway RTT in legend")
	}
	if !strings.Contains(frame, "CloudFlare: 18 ms") {
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

func TestRenderFrameStability(t *testing.T) {
	t.Parallel()
	addresses := []string{"192.168.1.1", "1.1.1.1"}
	data := [][]float64{{10, 20, 30}, {5, 15, 25}}

	// Frame 1: Gateway RTT is higher (30 vs 25)
	frame1 := renderFrame(addresses, data, []int64{30, 25}, 30, 80)

	// Frame 2: CloudFlare RTT is higher (20 vs 35)
	frame2 := renderFrame(addresses, data, []int64{20, 35}, 35, 80)

	// Both frames should contain both legends
	if !strings.Contains(frame1, "Gateway: 30 ms") || !strings.Contains(frame1, "CloudFlare: 25 ms") {
		t.Error("frame1 missing legends")
	}
	if !strings.Contains(frame2, "Gateway: 20 ms") || !strings.Contains(frame2, "CloudFlare: 35 ms") {
		t.Error("frame2 missing legends")
	}

	// Legend order should be stable (Gateway then CloudFlare)
	posGW1 := strings.Index(frame1, "Gateway:")
	posCF1 := strings.Index(frame1, "CloudFlare:")
	posGW2 := strings.Index(frame2, "Gateway:")
	posCF2 := strings.Index(frame2, "CloudFlare:")

	if posGW1 >= posCF1 {
		t.Errorf("frame1: Gateway legend should come before CloudFlare, got GW at %d and CF at %d", posGW1, posCF1)
	}
	if posGW2 >= posCF2 {
		t.Errorf("frame2: Gateway legend should come before CloudFlare, got GW at %d and CF at %d", posGW2, posCF2)
	}
}

func TestRenderFrameScaleChanges(t *testing.T) {
	t.Parallel()
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

func TestRunLoop(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name      string
		gwValues  []int64
		cfValues  []int64
		maxFrames int
		wantInOut []string
	}{
		{
			name:      "basic render",
			gwValues:  []int64{10, 20, 15},
			cfValues:  []int64{25, 30, 28},
			maxFrames: 3,
			wantInOut: []string{
				"192.168.1.1 (gateway)",
				"1.1.1.1 (CloudFlare DNS)",
				"Press Control-C to exit",
			},
		},
		{
			name:      "shows current RTT in legend",
			gwValues:  []int64{42},
			cfValues:  []int64{55},
			maxFrames: 1,
			wantInOut: []string{
				"Gateway: 42 ms",
				"CloudFlare: 55 ms",
			},
		},
		{
			name:      "graph scales with high RTT",
			gwValues:  []int64{10, 100},
			cfValues:  []int64{20, 80},
			maxFrames: 2,
			wantInOut: []string{"100"}, // Y-axis should show 100
		},
		{
			name:      "handles zero RTT",
			gwValues:  []int64{0, 5},
			cfValues:  []int64{0, 3},
			maxFrames: 2,
			wantInOut: []string{"00 ms"}, // zero formatted
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			ctx := context.Background()
			term := &mockTerminal{width: 80}

			sources := []PingSource{
				&mockPingSource{address: "192.168.1.1", values: tt.gwValues},
				&mockPingSource{address: "1.1.1.1", values: tt.cfValues},
			}

			err := runLoop(ctx, term, sources, tt.maxFrames, time.Millisecond)
			if err != nil {
				t.Fatalf("runLoop error: %v", err)
			}

			out := term.buf.String()
			for _, want := range tt.wantInOut {
				if !strings.Contains(out, want) {
					t.Errorf("output missing %q\ngot:\n%s", want, out)
				}
			}
		})
	}
}

func TestRunLoopNonBlocking(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	term := &mockTerminal{width: 80}

	// Gateway responds 3 times, CloudFlare only once.
	sources := []PingSource{
		&mockPingSource{address: "192.168.1.1", values: []int64{10, 20, 30}},
		&mockPingSource{address: "1.1.1.1", values: []int64{50}},
	}

	// We expect 4 frames total (3 from GW + 1 from CF)
	err := runLoop(ctx, term, sources, 4, time.Millisecond)
	if err != nil {
		t.Fatalf("runLoop error: %v", err)
	}

	out := term.buf.String()
	if !strings.Contains(out, "Gateway: 30 ms") {
		t.Error("should have rendered frame with latest gateway value")
	}
	if !strings.Contains(out, "CloudFlare: 50 ms") {
		t.Error("should have rendered frame with latest cloudflare value")
	}
}

func TestRunLoopContextCancel(t *testing.T) {
	t.Parallel()
	ctx, cancel := context.WithCancel(context.Background())
	term := &mockTerminal{width: 80}

	// Sources with many values, but we cancel early
	sources := []PingSource{
		&mockPingSource{address: "192.168.1.1", values: []int64{10, 20, 30, 40, 50}},
		&mockPingSource{address: "1.1.1.1", values: []int64{15, 25, 35, 45, 55}},
	}

	done := make(chan error)
	go func() {
		done <- runLoop(ctx, term, sources, 0, time.Millisecond) // unlimited frames
	}()

	// Let it render a couple frames then cancel
	cancel()

	err := <-done
	if err != nil {
		t.Fatalf("runLoop error: %v", err)
	}
}
