package main

import (
	"context"
	"flag"
	"fmt"
	"io"
	"os"
	"os/signal"
	"reflect"
	"strings"
	"time"

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
	if err := run(); err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		os.Exit(1)
	}
}

func run() error {
	frames := flag.Int("frames", 0, "number of frames to render (0 = unlimited)")
	flag.Parse()

	gatewayIP, err := gateway.DiscoverGateway()
	if err != nil {
		return fmt.Errorf("discover gateway: %v", err)
	}

	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, os.Interrupt)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	go func() {
		<-sigCh
		cancel()
	}()

	sources := []PingSource{
		&realPinger{address: gatewayIP.String()},
		&realPinger{address: cloudFlareIP},
	}

	fmt.Print("\033[?25l")       // hide cursor
	defer fmt.Print("\033[?25h") // show cursor

	return runLoop(ctx, &gotermTerminal{}, sources, *frames, time.Second)
}

func runLoop(ctx context.Context, term Terminal, sources []PingSource, maxFrames int, interval time.Duration) error {
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

	data := make([][]float64, len(sources))
	rtts := make([]int64, len(sources))
	hasData := make([]bool, len(sources))
	var maxRTT int64
	frameCount := 0

	render := func() error {
		frame := renderFrame(addresses, data, rtts, maxRTT, term.Width())

		term.MoveCursor(1, 1)
		if _, err := fmt.Fprint(term, frame); err != nil {
			return fmt.Errorf("write frame: %w", err)
		}
		if _, err := fmt.Fprint(term, "\033[J"); err != nil {
			return fmt.Errorf("clear screen: %w", err)
		}
		term.Flush()
		return nil
	}

	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	// Prepare select cases
	cases := make([]reflect.SelectCase, len(channels)+2)
	cases[0] = reflect.SelectCase{Dir: reflect.SelectRecv, Chan: reflect.ValueOf(ctx.Done())}
	cases[1] = reflect.SelectCase{Dir: reflect.SelectRecv, Chan: reflect.ValueOf(ticker.C)}
	for i, ch := range channels {
		cases[i+2] = reflect.SelectCase{Dir: reflect.SelectRecv, Chan: reflect.ValueOf(ch)}
	}

	for {
		if maxFrames > 0 && frameCount >= maxFrames {
			return nil
		}

		chosen, value, ok := reflect.Select(cases)
		if chosen == 0 { // ctx.Done()
			return nil
		}
		if chosen == 1 { // ticker.C
			ready := true
			for _, h := range hasData {
				if !h {
					ready = false
					break
				}
			}
			if ready {
				for i := range data {
					data[i] = appendData(data[i], rtts[i])
				}
				if err := render(); err != nil {
					return err
				}
				frameCount++
			}
			continue
		}

		// One of the channels
		if !ok {
			cases[chosen].Chan = reflect.ValueOf(nil)

			// Check if all pinger channels are closed
			allClosed := true
			for i := 2; i < len(cases); i++ {
				if cases[i].Chan.IsValid() && !cases[i].Chan.IsNil() {
					allClosed = false
					break
				}
			}
			if allClosed {
				return nil
			}
			continue
		}

		idx := chosen - 2
		v := value.Int()
		rtts[idx] = v
		hasData[idx] = true
		if v > maxRTT {
			maxRTT = v
		}

		// Update RTT in legend immediately
		ready := true
		for _, h := range hasData {
			if !h {
				ready = false
				break
			}
		}
		if ready {
			if err := render(); err != nil {
				return err
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

	sb.WriteString("Ping latency: ")
	for i, addr := range addresses {
		if i > 0 {
			sb.WriteString(" vs ")
		}
		label := "gateway"
		if i > 0 {
			label = "CloudFlare DNS"
		}
		fmt.Fprintf(&sb, "%s (%s)", addr, label)
	}
	sb.WriteString("\n\n")

	maxDataLen := 0
	for _, d := range data {
		if len(d) > maxDataLen {
			maxDataLen = len(d)
		}
	}

	// Use width-10 to leave space for Y-axis labels.
	// Also ensure graphWidth is at least 1.
	graphWidth := width - 10
	if maxDataLen-1 < graphWidth {
		graphWidth = maxDataLen - 1
	}
	if graphWidth < 1 {
		graphWidth = 1
	}

	padding := float64(maxRTT) * 0.1 // 10% padding top
	if padding < 1 {
		padding = 1
	}

	allColors := []asciigraph.AnsiColor{asciigraph.Cyan, asciigraph.Magenta, asciigraph.Yellow, asciigraph.Red, asciigraph.Green}
	colors := make([]asciigraph.AnsiColor, len(data))
	for i := range data {
		colors[i] = allColors[i%len(allColors)]
	}

	legends := make([]string, len(rtts))
	for i, rtt := range rtts {
		label := "Gateway"
		if i > 0 {
			label = "CloudFlare"
		}
		legends[i] = fmt.Sprintf("%s: %02d ms", label, rtt)
	}

	canPlot := true
	for _, d := range data {
		if len(d) == 0 {
			canPlot = false
			break
		}
	}

	if canPlot {
		graph := asciigraph.PlotMany(data,
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
	} else {
		sb.WriteString("\n   [ Waiting for more data... ]\n\n")
		// Add legends manually if we can't plot yet
		for i, l := range legends {
			if i > 0 {
				sb.WriteString("    ")
			}
			fmt.Fprintf(&sb, "%s", l)
		}
		sb.WriteString("\n")
	}
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

