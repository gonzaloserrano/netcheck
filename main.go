package main

import (
	"context"
	"flag"
	"fmt"
	"io"
	"os"
	"os/signal"
	"strings"

	"github.com/buger/goterm"
	"github.com/guptarohit/asciigraph"
	"github.com/jackpal/gateway"
	probing "github.com/prometheus-community/pro-bing"
)

const cloudFlareIP = "1.1.1.1"

// PingSource provides RTT values from a ping target.
type PingSource interface {
	Start(ctx context.Context) (<-chan int64, error)
	Address() string
}

// Terminal abstracts terminal operations for testing.
type Terminal interface {
	io.Writer
	Clear()
	MoveCursor(x, y int)
	Flush()
	Width() int
}

func main() {
	frames := flag.Int("frames", 0, "number of frames to render (0 = unlimited)")
	flag.Parse()

	gatewayIP, err := gateway.DiscoverGateway()
	if err != nil {
		fmt.Fprintf(os.Stderr, "discover gateway: %v\n", err)
		os.Exit(1)
	}

	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, os.Interrupt)

	ctx, cancel := context.WithCancel(context.Background())
	go func() {
		<-sigCh
		fmt.Print("\033[?25h") // show cursor
		cancel()
		os.Exit(0)
	}()

	sources := []PingSource{
		&realPinger{address: gatewayIP.String()},
		&realPinger{address: cloudFlareIP},
	}

	fmt.Print("\033[?25l") // hide cursor

	if err := runLoop(ctx, &gotermTerminal{}, sources, *frames); err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		os.Exit(1)
	}

	fmt.Print("\033[?25h") // show cursor
}

func runLoop(ctx context.Context, term Terminal, sources []PingSource, maxFrames int) error {
	channels := make([]<-chan int64, len(sources))
	addresses := make([]string, len(sources))

	for i, src := range sources {
		ch, err := src.Start(ctx)
		if err != nil {
			return fmt.Errorf("start ping %s: %w", src.Address(), err)
		}
		channels[i] = ch
		addresses[i] = src.Address()
	}

	term.Clear()
	term.Flush()

	data := [][]float64{{}, {}}
	var maxRTT int64
	frameCount := 0

	for {
		select {
		case <-ctx.Done():
			return nil
		case v0 := <-channels[0]:
			v1 := <-channels[1]

			maxRTT = max(maxRTT, v0, v1)
			data[0] = appendData(data[0], v0)
			data[1] = appendData(data[1], v1)

			frame := renderFrame(addresses, data, []int64{v0, v1}, maxRTT, term.Width())

			term.MoveCursor(1, 1)
			if _, err := fmt.Fprint(term, frame); err != nil {
				return fmt.Errorf("write frame: %w", err)
			}
			if _, err := fmt.Fprint(term, "\033[J"); err != nil {
				return fmt.Errorf("clear screen: %w", err)
			}
			term.Flush()

			frameCount++
			if maxFrames > 0 && frameCount >= maxFrames {
				return nil
			}
		}
	}
}

// realPinger implements PingSource using pro-bing.
type realPinger struct {
	address string
}

func (p *realPinger) Address() string { return p.address }

func (p *realPinger) Start(ctx context.Context) (<-chan int64, error) {
	pinger, err := probing.NewPinger(p.address)
	if err != nil {
		return nil, fmt.Errorf("create pinger: %w", err)
	}

	pinger.SetPrivileged(false) // use UDP ping, no root required

	out := make(chan int64)

	go func() {
		<-ctx.Done()
		pinger.Stop()
	}()

	pinger.OnRecv = func(pkt *probing.Packet) {
		out <- pkt.Rtt.Milliseconds()
	}

	go func() {
		if err := pinger.Run(); err != nil {
			fmt.Fprintf(os.Stderr, "pinger %s: %v\n", p.address, err)
		}
	}()

	return out, nil
}

// gotermTerminal implements Terminal using goterm.
type gotermTerminal struct{}

func (*gotermTerminal) Write(p []byte) (n int, err error) { return goterm.Print(string(p)) }
func (*gotermTerminal) Clear()                             { goterm.Clear() }
func (*gotermTerminal) MoveCursor(x, y int)                { goterm.MoveCursor(x, y) }
func (*gotermTerminal) Flush()                             { goterm.Flush() }
func (*gotermTerminal) Width() int                         { return goterm.Width() }

func renderFrame(addresses []string, data [][]float64, rtts []int64, maxRTT int64, width int) string {
	var sb strings.Builder

	fmt.Fprintf(&sb, "Ping latency: %s (gateway) vs %s (CloudFlare DNS)\n\n", addresses[0], addresses[1])

	maxDataLen := max(len(data[0]), len(data[1]))
	// Use width-1 to avoid asciigraph interpolation artifacts when width == data length
	graphWidth := min(width-10, maxDataLen-1)
	graphWidth = max(graphWidth, 1)

	padding := float64(maxRTT) * 0.1 // 10% padding top
	if padding < 1 {
		padding = 1
	}

	// Render higher-value series last so it's visible on top when lines cross.
	// asciigraph overwrites earlier series with later ones at same position.
	plotData := data
	colors := []asciigraph.AnsiColor{asciigraph.Cyan, asciigraph.Magenta}
	legends := []string{
		fmt.Sprintf("Gateway: %02d ms", rtts[0]),
		fmt.Sprintf("CloudFlare: %02d ms", rtts[1]),
	}
	if rtts[0] > rtts[1] {
		plotData = [][]float64{data[1], data[0]}
		colors = []asciigraph.AnsiColor{asciigraph.Magenta, asciigraph.Cyan}
		legends = []string{legends[1], legends[0]}
	}

	graph := asciigraph.PlotMany(plotData,
		asciigraph.Width(graphWidth),
		asciigraph.Height(maxHeight),
		asciigraph.LowerBound(0),
		asciigraph.UpperBound(float64(maxRTT)+padding),
		asciigraph.SeriesColors(colors...),
		asciigraph.SeriesLegends(legends...),
	)
	// Clear to end of line after each line to prevent artifacts when scale changes
	graph = strings.ReplaceAll(graph, "\n", "\x1b[K\n")
	sb.WriteString(graph)
	sb.WriteString("\x1b[K") // clear last line too
	sb.WriteString("\n\n")
	sb.WriteString("Press Control-C to exit\n")

	return sb.String()
}

const (
	maxLen    = 40
	maxHeight = 10
)

func appendData(data []float64, rtt int64) []float64 {
	data = append(data, float64(rtt))
	if len(data) > maxLen {
		data = data[1:]
	}
	return data
}

