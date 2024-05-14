package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"strconv"
	"time"
)

// Minimal price aggregator that blends two public sources.
// Publishes the result as JSON to STDOUT (can be piped to your infra).

type Price struct {
	Symbol string  `json:"symbol"`
	USD    float64 `json:"usd"`
	TS     int64   `json:"ts"`
	Source string  `json:"source"`
}

var httpc = &http.Client{Timeout: 7 * time.Second}

func coingecko(symbol string) (*Price, error) {
	url := fmt.Sprintf("https://api.coingecko.com/api/v3/simple/price?ids=%s&vs_currencies=usd", symbol)
	resp, err := httpc.Get(url)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	b, _ := io.ReadAll(resp.Body)
	var m map[string]map[string]float64
	if err := json.Unmarshal(b, &m); err != nil {
		return nil, err
	}
	usd := m[symbol]["usd"]
	return &Price{Symbol: symbol, USD: usd, TS: time.Now().Unix(), Source: "coingecko"}, nil
}

func binance(symbol string) (*Price, error) {
	// Binance wants tickers like ETHUSDT, BTCUSDT
	pair := map[string]string{"ethereum": "ETHUSDT", "bitcoin": "BTCUSDT"}
	s, ok := pair[symbol]
	if !ok {
		return nil, fmt.Errorf("binance pair unknown for %s", symbol)
	}
	resp, err := httpc.Get("https://api.binance.com/api/v3/ticker/price?symbol=" + s)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	var out struct {
		Price string `json:"price"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return nil, err
	}
	val, _ := strconv.ParseFloat(out.Price, 64)
	return &Price{Symbol: symbol, USD: val, TS: time.Now().Unix(), Source: "binance"}, nil
}

func blend(a, b *Price) *Price {
	// simple average with preference to newer timestamp
	avg := (a.USD + b.USD) / 2.0
	ts := a.TS
	if b.TS > ts {
		ts = b.TS
	}
	return &Price{Symbol: a.Symbol, USD: avg, TS: ts, Source: "avg(coingecko,binance)"}
}

func main() {
	if len(os.Args) < 2 {
		fmt.Println("usage: ./oracle <ethereum|bitcoin> [intervalSec]")
		os.Exit(1)
	}
	symbol := os.Args[1]
	interval := 0
	if len(os.Args) > 2 {
		if v, err := strconv.Atoi(os.Args[2]); err == nil {
			interval = v
		}
	}

	run := func(ctx context.Context) error {
		cg, err1 := coingecko(symbol)
		bn, err2 := binance(symbol)
		var final *Price
		switch {
		case err1 == nil && err2 == nil:
			final = blend(cg, bn)
		case err1 == nil:
			final = cg
		case err2 == nil:
			final = bn
		default:
			return fmt.Errorf("both sources failed: %v | %v", err1, err2)
		}
		enc := json.NewEncoder(os.Stdout)
		enc.SetIndent("", "  ")
		return enc.Encode(final)
	}

	if interval <= 0 {
		if err := run(context.Background()); err != nil {
			fmt.Fprintln(os.Stderr, "error:", err)
			os.Exit(1)
		}
		return
	}

	t := time.NewTicker(time.Duration(interval) * time.Second)
	defer t.Stop()
	for range t.C {
		if err := run(context.Background()); err != nil {
			fmt.Fprintln(os.Stderr, "error:", err)
		}
	}
}
