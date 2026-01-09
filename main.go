package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"strings"

	"github.com/buger/goterm"
	"github.com/guptarohit/asciigraph"
	"github.com/jackpal/gateway"
	probing "github.com/prometheus-community/pro-bing"
)

const cloudFlareIP = "1.1.1.1"

func main() {
	frames := flag.Int("frames", 0, "number of frames to render (0 = unlimited)")
	flag.Parse()

	gatewayIP, err := gateway.DiscoverGateway()
	if err != nil {
		panic(err)
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

	out := [2]chan int64{
		make(chan int64),
		make(chan int64),
	}
	addresses := []string{gatewayIP.String(), cloudFlareIP}
	for i, address := range addresses {
		go func() {
			err := newPing(ctx, address, out[i])
			if err != nil {
				panic(err)
			}
		}()
	}

	fmt.Print("\033[?25l") // hide cursor
	goterm.Clear()
	goterm.Flush()

	data := [][]float64{{}, {}}
	var maxRTT int64
	frameCount := 0
	for {
		v0 := <-out[0]
		v1 := <-out[1]

		maxRTT = max(maxRTT, v0, v1)

		data[0] = appendData(data[0], v0)
		data[1] = appendData(data[1], v1)

		frame := renderFrame(addresses, data, []int64{v0, v1}, maxRTT, goterm.Width())

		goterm.Clear()
		goterm.MoveCursor(1, 1)
		goterm.Print(frame)
		goterm.Flush()

		frameCount++
		if *frames > 0 && frameCount >= *frames {
			fmt.Print("\033[?25h") // show cursor
			return
		}
	}
}

func renderFrame(addresses []string, data [][]float64, rtts []int64, maxRTT int64, width int) string {
	var sb strings.Builder

	sb.WriteString(fmt.Sprintf("Ping latency: %s (gateway) vs %s (CloudFlare DNS)\n\n", addresses[0], addresses[1]))

	maxDataLen := max(len(data[0]), len(data[1]))
	graphWidth := min(width-10, maxDataLen) // don't stretch sparse data
	graphWidth = max(graphWidth, 1)

	padding := float64(maxRTT) * 0.1 // 10% padding top
	if padding < 1 {
		padding = 1
	}

	graph := asciigraph.PlotMany(data,
		asciigraph.Width(graphWidth),
		asciigraph.Height(maxHeight),
		asciigraph.LowerBound(0),
		asciigraph.UpperBound(float64(maxRTT)+padding),
		asciigraph.SeriesColors(asciigraph.Cyan, asciigraph.Magenta),
		asciigraph.SeriesLegends(
			fmt.Sprintf("%s: %02d ms", addresses[0], rtts[0]),
			fmt.Sprintf("%s: %02d ms", addresses[1], rtts[1]),
		),
	)
	sb.WriteString(graph)
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

func newPing(ctx context.Context, address string, out chan int64) error {
	pinger, err := probing.NewPinger(address)
	if err != nil {
		return err
	}

	pinger.SetPrivileged(false) // use UDP ping, no root required

	go func() {
		<-ctx.Done()
		pinger.Stop()
	}()

	pinger.OnRecv = func(pkt *probing.Packet) {
		out <- pkt.Rtt.Milliseconds()
	}

	pinger.Run()

	return nil
}
