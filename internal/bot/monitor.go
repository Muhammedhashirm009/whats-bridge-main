package bot

import (
	"log"
	"net"
	"sync"
	"sync/atomic"
	"time"
)

var internetAvailable atomic.Bool

// restartMu and lastRestart implement a 30-second debounce for RestartBot calls.
var restartMu sync.Mutex
var lastRestart time.Time

func init() {
	internetAvailable.Store(true)
}

// IsInternetAvailable returns the current internet connectivity status.
func IsInternetAvailable() bool {
	return internetAvailable.Load()
}

func StartInternetMonitor() {
	go func() {
		defer func() {
			if r := recover(); r != nil {
				log.Printf("Recovered from panic in internet monitor goroutine: %v", r)
			}
		}()
		for {
			isOnline := checkInternet()
			wasOnline := internetAvailable.Swap(isOnline)

			if isOnline != wasOnline {
				if isOnline {
					log.Println("Internet connection restored. Triggering reconnection...")
					go func() {
						defer func() {
							if r := recover(); r != nil {
								log.Printf("Recovered from panic in reconnection goroutine: %v", r)
							}
						}()
						c := GetClient()
						if c != nil && !c.IsConnected() {
							// Debounce: only restart if at least 30 seconds since last restart
							restartMu.Lock()
							if time.Since(lastRestart) < 30*time.Second {
								restartMu.Unlock()
								log.Println("RestartBot debounced: called too recently, skipping")
								return
							}
							lastRestart = time.Now()
							restartMu.Unlock()
							RestartBot()
						}
					}()
				} else {
					log.Println("Internet connection lost. Pausing operations.")
				}
			}
			time.Sleep(10 * time.Second)
		}
	}()
}

func checkInternet() bool {
	// Try to connect to Google DNS
	timeout := 5 * time.Second
	conn, err := net.DialTimeout("tcp", "8.8.8.8:53", timeout)
	if err != nil {
		return false
	}
	conn.Close()
	return true
}
