package bot

import (
	"log"
	"net"
	"sync/atomic"
	"time"
)

var internetAvailable atomic.Bool

func init() {
	internetAvailable.Store(true)
}

// IsInternetAvailable returns the current internet connectivity status.
func IsInternetAvailable() bool {
	return internetAvailable.Load()
}

func StartInternetMonitor() {
	go func() {
		for {
			isOnline := checkInternet()
			wasOnline := internetAvailable.Swap(isOnline)

			if isOnline != wasOnline {
				if isOnline {
					log.Println("Internet connection restored. Triggering reconnection...")
					go func() {
						if GlobalClient != nil && !GlobalClient.IsConnected() {
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
