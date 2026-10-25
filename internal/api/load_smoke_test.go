package api

import (
	"bytes"
	"net/http"
	"net/http/httptest"
	"os"
	"runtime"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/router-for-me/CLIProxyAPI/v8/internal/util"
	log "github.com/sirupsen/logrus"
)

// TestLoadSmokeHighConcurrency drives the real middleware chain under
// concurrency and checks the two failure modes that matter on a weak host:
// unbounded goroutine growth and retained heap.
//
// It is skipped by default because it takes several seconds and allocates
// hundreds of megabytes of traffic. Run it with CPA_LOAD_SMOKE=1.
func TestLoadSmokeHighConcurrency(t *testing.T) {
	if testing.Short() {
		t.Skip("load smoke skipped in short mode")
	}
	if os.Getenv("CPA_LOAD_SMOKE") != "1" {
		t.Skip("set CPA_LOAD_SMOKE=1 to run the load smoke test")
	}

	previousLevel := log.GetLevel()
	log.SetLevel(log.PanicLevel)
	t.Cleanup(func() { log.SetLevel(previousLevel) })

	server := newTestServerWithConfig(t, parseTestServerConfigYAML(t, "max-in-flight-requests: 256\n"))
	server.engine.POST("/load-smoke", func(c *gin.Context) {
		if _, err := util.SharedRequestBody(c); err != nil {
			c.Status(http.StatusBadRequest)
			return
		}
		c.Status(http.StatusOK)
	})

	upstream := httptest.NewServer(server.engine)
	defer upstream.Close()

	payload := bytes.Repeat([]byte(`{"model":"gpt-5.1","messages":[{"role":"user","content":"load"}]}`), 17) // ~1 KiB
	client := &http.Client{Timeout: 30 * time.Second, Transport: &http.Transport{MaxIdleConnsPerHost: 64}}

	runtime.GC()
	var start runtime.MemStats
	runtime.ReadMemStats(&start)
	baseGoroutines := runtime.NumGoroutine()

	const (
		workers    = 200
		perWorker  = 25
		wantStatus = http.StatusOK
	)
	var (
		wg       sync.WaitGroup
		okCount  atomic.Int64
		rejected atomic.Int64
		other    atomic.Int64
	)

	wg.Add(workers)
	for range workers {
		go func() {
			defer wg.Done()
			for range perWorker {
				resp, err := client.Post(upstream.URL+"/load-smoke", "application/json", bytes.NewReader(payload))
				if err != nil {
					other.Add(1)
					continue
				}
				switch resp.StatusCode {
				case wantStatus:
					okCount.Add(1)
				case http.StatusTooManyRequests:
					rejected.Add(1)
				default:
					other.Add(1)
				}
				if errClose := resp.Body.Close(); errClose != nil {
					other.Add(1)
				}
			}
		}()
	}
	wg.Wait()

	if other.Load() != 0 {
		t.Fatalf("%d requests failed with an unexpected status or transport error (ok=%d rejected=%d)", other.Load(), okCount.Load(), rejected.Load())
	}
	if okCount.Load()+rejected.Load() != workers*perWorker {
		t.Fatalf("accounted requests = %d, want %d", okCount.Load()+rejected.Load(), workers*perWorker)
	}

	// Idle keep-alive connections own a reader and a writer goroutine each, so
	// they are closed before the goroutine count is judged: the check is for
	// request-handling leaks, not for a warm connection pool.
	client.CloseIdleConnections()
	upstream.Close()

	deadline := time.Now().Add(20 * time.Second)
	for runtime.NumGoroutine() > baseGoroutines && time.Now().Before(deadline) {
		time.Sleep(50 * time.Millisecond)
	}
	settled := runtime.NumGoroutine()

	runtime.GC()
	var end runtime.MemStats
	runtime.ReadMemStats(&end)
	grew := int64(end.HeapAlloc) - int64(start.HeapAlloc)

	t.Logf("requests: ok=%d rejected=%d; goroutines: %d -> %d (base %d); heap: %d -> %d (%+d bytes)",
		okCount.Load(), rejected.Load(), baseGoroutines, settled, baseGoroutines, start.HeapAlloc, end.HeapAlloc, grew)

	if settled > baseGoroutines+20 {
		t.Fatalf("goroutines did not settle: %d, want at most %d", settled, baseGoroutines+20)
	}
	if grew > 64<<20 {
		t.Fatalf("heap grew by %d bytes, want at most 64 MiB", grew)
	}
}
