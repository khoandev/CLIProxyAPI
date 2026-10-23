package api

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
)

func waitSignal(t *testing.T, ch <-chan struct{}, what string) {
	t.Helper()
	select {
	case <-ch:
	case <-time.After(5 * time.Second):
		t.Fatalf("timed out waiting for %s", what)
	}
}

func TestInFlightAdmissionRejectsOverCapRequests(t *testing.T) {
	server := newTestServerWithConfig(t, parseTestServerConfigYAML(t, "max-in-flight-requests: 1\n"))

	entered := make(chan struct{}, 4)
	release := make(chan struct{})
	server.engine.GET("/admission-probe", func(c *gin.Context) {
		entered <- struct{}{}
		<-release
		c.Status(http.StatusOK)
	})

	statusA := make(chan int, 1)
	go func() {
		recA := httptest.NewRecorder()
		server.engine.ServeHTTP(recA, httptest.NewRequest(http.MethodGet, "/admission-probe", nil))
		statusA <- recA.Code
	}()
	waitSignal(t, entered, "request A to enter the handler")

	recB := httptest.NewRecorder()
	server.engine.ServeHTTP(recB, httptest.NewRequest(http.MethodGet, "/admission-probe", nil))
	if recB.Code != http.StatusTooManyRequests {
		t.Fatalf("request B status = %d, want 429; body=%s", recB.Code, recB.Body.String())
	}
	if !strings.Contains(recB.Body.String(), "too many in-flight requests") {
		t.Fatalf("request B body = %q, want the admission error", recB.Body.String())
	}
	if got := recB.Header().Get("Retry-After"); got != "1" {
		t.Fatalf("request B Retry-After = %q, want %q", got, "1")
	}

	close(release)
	select {
	case codeA := <-statusA:
		if codeA != http.StatusOK {
			t.Fatalf("request A status = %d, want 200", codeA)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("timed out waiting for request A to finish")
	}

	recC := httptest.NewRecorder()
	server.engine.ServeHTTP(recC, httptest.NewRequest(http.MethodGet, "/admission-probe", nil))
	if recC.Code != http.StatusOK {
		t.Fatalf("request C status = %d, want 200 after the slot freed", recC.Code)
	}
}

func TestInFlightAdmissionUnlimited(t *testing.T) {
	server := newTestServerWithConfig(t, parseTestServerConfigYAML(t, "max-in-flight-requests: -1\n"))

	entered := make(chan struct{}, 2)
	release := make(chan struct{})
	server.engine.GET("/admission-probe", func(c *gin.Context) {
		entered <- struct{}{}
		<-release
		c.Status(http.StatusOK)
	})

	var wg sync.WaitGroup
	statuses := make(chan int, 2)
	for range 2 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			rec := httptest.NewRecorder()
			server.engine.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/admission-probe", nil))
			statuses <- rec.Code
		}()
	}
	for range 2 {
		waitSignal(t, entered, "both concurrent requests to enter the handler")
	}
	close(release)
	wg.Wait()
	close(statuses)
	for code := range statuses {
		if code != http.StatusOK {
			t.Fatalf("unlimited admission status = %d, want 200", code)
		}
	}
	if got := server.inFlightRequests.Load(); got != 0 {
		t.Fatalf("in-flight counter = %d, want 0 after requests finished", got)
	}
}

func TestInFlightAdmissionSkipsManagementPaths(t *testing.T) {
	server := newTestServerWithConfig(t, parseTestServerConfigYAML(t, "max-in-flight-requests: 1\n"))

	entered := make(chan struct{})
	release := make(chan struct{})
	server.engine.GET("/admission-probe", func(c *gin.Context) {
		entered <- struct{}{}
		<-release
		c.Status(http.StatusOK)
	})

	doneA := make(chan struct{})
	go func() {
		defer close(doneA)
		server.engine.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, "/admission-probe", nil))
	}()
	waitSignal(t, entered, "request A to hold the only slot")

	recMgmt := httptest.NewRecorder()
	server.engine.ServeHTTP(recMgmt, httptest.NewRequest(http.MethodGet, "/v8/management/config", nil))
	if recMgmt.Code == http.StatusTooManyRequests && strings.Contains(recMgmt.Body.String(), "too many in-flight requests") {
		t.Fatalf("management path rejected by admission control: %s", recMgmt.Body.String())
	}

	close(release)
	waitSignal(t, doneA, "request A to finish")
}

func TestInFlightAdmissionAutoWithoutMemoryLimit(t *testing.T) {
	server := newTestServerWithConfig(t, parseTestServerConfigYAML(t, "\n"))
	if got := server.maxInFlightRequests(); got != 0 {
		t.Fatalf("maxInFlightRequests() = %d, want 0 when no memory limit is visible", got)
	}

	entered := make(chan struct{}, 4)
	release := make(chan struct{})
	server.engine.GET("/admission-probe", func(c *gin.Context) {
		entered <- struct{}{}
		<-release
		c.Status(http.StatusOK)
	})

	var wg sync.WaitGroup
	startRequest := func(statuses chan<- int) {
		wg.Add(1)
		go func() {
			defer wg.Done()
			rec := httptest.NewRecorder()
			server.engine.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/admission-probe", nil))
			statuses <- rec.Code
		}()
	}

	statuses := make(chan int, 2)
	startRequest(statuses)
	waitSignal(t, entered, "request A to enter the handler")
	startRequest(statuses)
	waitSignal(t, entered, "request B to enter the handler")

	close(release)
	wg.Wait()
	close(statuses)
	for code := range statuses {
		if code != http.StatusOK {
			t.Fatalf("auto admission status = %d, want 200 with no memory limit", code)
		}
	}
}
