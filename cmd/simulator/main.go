package main

import (
	"bytes"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"net/http"
	"sync"
	"sync/atomic"
	"time"

	"github.com/google/uuid"
)

type TapPayload struct {
	TerminalID    string `json:"terminal_id"`
	TagUID        string `json:"tag_uid"`
	AmountPesewas int64  `json:"amount_pesewas"`
}

func main() {
	baseURL := flag.String("url", "http://localhost:8080", "Base URL of M-Tap switching service")
	terminalID := flag.String("terminal", "POD_001", "Terminal Identifier")
	psk := flag.String("psk", "secret_hmac_psk_key_boron_labs_2026", "Terminal Pre-Shared Secret")
	tagUID := flag.String("tag", "04A38CF2196880", "Active 7-byte NFC Tag UID")
	tapsCount := flag.Int("n", 20, "Number of proximity tap requests to dispatch")
	concurrency := flag.Int("c", 4, "Concurrent worker routines")
	flag.Parse()

	fmt.Printf("\n🚀 Launching Boron Labs M-Tap Proximity Switching Benchmark\n")
	fmt.Printf("Target: %s/api/v1/terminal/tap | Terminal: %s | Tag: %s\n", *baseURL, *terminalID, *tagUID)
	fmt.Printf("Total Requests: %d | Concurrent Workers: %d\n\n", *tapsCount, *concurrency)

	client := &http.Client{Timeout: 5 * time.Second}

	var successfulTaps int64
	var rateLimitedTaps int64
	var conflicts int64
	var failedTaps int64
	var totalLatencyMs int64

	jobs := make(chan int, *tapsCount)
	for i := 0; i < *tapsCount; i++ {
		jobs <- i
	}
	close(jobs)

	startOverall := time.Now()
	var wg sync.WaitGroup

	for w := 0; w < *concurrency; w++ {
		wg.Add(1)
		go func(workerID int) {
			defer wg.Done()

			for range jobs {
				reqStart := time.Now()

				// 1. Prepare Payload
				payload := TapPayload{
					TerminalID:    *terminalID,
					TagUID:        *tagUID,
					AmountPesewas: 2500, // GHS 25.00
				}
				bodyBytes, _ := json.Marshal(payload)

				// 2. Compute Anti-Replay HMAC-SHA256 Signature
				ts := time.Now().Unix()
				mac := hmac.New(sha256.New, []byte(*psk))
				mac.Write([]byte(fmt.Sprintf("%d.%s", ts, string(bodyBytes))))
				sig := hex.EncodeToString(mac.Sum(nil))

				// 3. Unique Idempotency Key
				idempotencyKey := uuid.New().String()

				// 4. Construct Request
				req, err := http.NewRequest(http.MethodPost, fmt.Sprintf("%s/api/v1/terminal/tap", *baseURL), bytes.NewBuffer(bodyBytes))
				if err != nil {
					atomic.AddInt64(&failedTaps, 1)
					continue
				}

				req.Header.Set("Content-Type", "application/json")
				req.Header.Set("X-Terminal-Id", *terminalID)
				req.Header.Set("X-Timestamp", fmt.Sprintf("%d", ts))
				req.Header.Set("X-Signature", sig)
				req.Header.Set("X-Idempotency-Key", idempotencyKey)

				// 5. Send Tap
				resp, err := client.Do(req)
				latency := time.Since(reqStart).Milliseconds()
				atomic.AddInt64(&totalLatencyMs, latency)

				if err != nil {
					atomic.AddInt64(&failedTaps, 1)
					continue
				}

				_, _ = io.Copy(io.Discard, resp.Body)
				resp.Body.Close()

				switch resp.StatusCode {
				case http.StatusAccepted:
					atomic.AddInt64(&successfulTaps, 1)
				case http.StatusTooManyRequests:
					atomic.AddInt64(&rateLimitedTaps, 1)
				case http.StatusConflict:
					atomic.AddInt64(&conflicts, 1)
				default:
					atomic.AddInt64(&failedTaps, 1)
				}
			}
		}(w)
	}

	wg.Wait()
	duration := time.Since(startOverall)

	totalRequests := int64(*tapsCount)
	avgLatency := float64(totalLatencyMs) / float64(totalRequests)
	rps := float64(totalRequests) / duration.Seconds()

	fmt.Println("=======================================================")
	fmt.Println("              BENCHMARK RESULTS & METRICS             ")
	fmt.Println("=======================================================")
	fmt.Printf("Total Duration:            %v\n", duration)
	fmt.Printf("Average Latency:           %.2f ms\n", avgLatency)
	fmt.Printf("Throughput (RPS):          %.2f req/sec\n", rps)
	fmt.Printf("Accepted Taps (202):       %d\n", successfulTaps)
	fmt.Printf("Rate-Limited Taps (429):   %d (Per-Terminal Rate Guard)\n", rateLimitedTaps)
	fmt.Printf("Idempotency Conflicts(409):%d\n", conflicts)
	fmt.Printf("Network/Server Errors:     %d\n", failedTaps)
	fmt.Println("=======================================================\n")
}

