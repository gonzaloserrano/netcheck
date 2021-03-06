package main

import (
	"context"
	"fmt"
	"os"
	"os/signal"

	"github.com/buger/goterm"
	"github.com/fatih/color"
	"github.com/jackpal/gateway"
	"github.com/jesseduffield/asciigraph"
	"github.com/sparrc/go-ping"
)

const cloudFlareIP = "1.1.1.1"

func main() {
	gatewayIP, err := gateway.DiscoverGateway()
	if err != nil {
		panic(err)
	}

	// listen for ctrl-C signal
	c := make(chan os.Signal, 1)
	signal.Notify(c, os.Interrupt)

	ctx, cancel := context.WithCancel(context.Background())
	go func() {
		<-c
		cancel()
		os.Exit(0)
	}()

	out := [2]chan int64{
		make(chan int64),
		make(chan int64),
	}
	addresses := []string{gatewayIP.String(), cloudFlareIP}
	for i, address := range addresses {
		i := i
		address := address
		go func() {
			err := newPing(ctx, address, out[i])
			if err != nil {
				panic(err)
			}
		}()
	}

	goterm.Clear()

	data0 := []float64{0}
	data1 := []float64{0}
	var max int64
	for {
		goterm.MoveCursor(1, 1)

		color.Set(color.FgWhite)
		fmt.Println("Network check with ping:")
		fmt.Printf("%s (gateway) vs %s (CloudFlare's DNS)\n\n", addresses[0], addresses[1])

		v0 := <-out[0]
		v1 := <-out[1]

		if v0 > max {
			max = v0
		}
		if v1 > max {
			max = v1
		}

		color.Set(color.FgCyan)
		data0 = display(addresses[0], data0, v0, max)

		color.Set(color.FgMagenta)
		data1 = display(addresses[1], data1, v1, max)

		color.Set(color.FgWhite)
		fmt.Println("Press Control-C to exit")

		goterm.Flush()
	}
}

const (
	maxLen    = 40
	maxHeight = 10
)

func display(address string, data []float64, rtt, maxValue int64) []float64 {
	data = append(data, float64(rtt))
	if len(data) > maxLen {
		data = append([]float64{0}, data[2:maxLen+1]...)
	}
	caption := fmt.Sprintf("PING %s: %02d ms", address, rtt)
	graph := asciigraph.Plot(data,
		asciigraph.Height(maxHeight),
		asciigraph.Caption(caption),
		asciigraph.Max(float64(maxValue)),
	)
	fmt.Printf("%s\n\n", graph)

	return data
}

func newPing(ctx context.Context, address string, out chan int64) error {
	pinger, err := ping.NewPinger(address)
	if err != nil {
		return err
	}

	go func() {
		<-ctx.Done()
		pinger.Stop()
	}()

	pinger.OnRecv = func(pkt *ping.Packet) {
		out <- pkt.Rtt.Milliseconds()
	}

	pinger.Run()

	return nil
}
