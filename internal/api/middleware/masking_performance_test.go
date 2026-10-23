package middleware

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
)

// TestMaskingRequestPathDoesNotReadBodyTwice proves the masking middleware
// reads the request body once per request.
//
// The body can be several megabytes, and the middleware needs the model field
// for two independent rule checks. Reading the body per check doubled the copy
// cost of every request.
func TestMaskingRequestPathDoesNotReadBodyTwice(t *testing.T) {
	body := `{"model":"gpt-5.6-sol","messages":[` + strings.Repeat(`{"role":"user","content":"`+strings.Repeat("x", 1024)+`"},`, 64) + `{"role":"user","content":"x"}]}`

	singleRead := testing.Benchmark(func(b *testing.B) {
		for range b.N {
			reader := strings.NewReader(body)
			if _, err := io.ReadAll(reader); err != nil {
				b.Fatalf("read body: %v", err)
			}
		}
	})
	fullPath := testing.Benchmark(func(b *testing.B) {
		for range b.N {
			gin.SetMode(gin.TestMode)
			c, _ := gin.CreateTestContext(httptest.NewRecorder())
			c.Request = httptest.NewRequest(http.MethodPost, "/v1/chat/completions", strings.NewReader(body))
			if set := maskRuleSetForRequest(c); len(set.rules) == 0 {
				b.Fatal("expected masking rules")
			}
		}
	})

	// The rule resolution reads the body once and parses the model out of it.
	// Reading it per rule check pushed the cost well past this bound.
	if fullPath.NsPerOp() > singleRead.NsPerOp()*17/10 {
		t.Fatalf("masking reads the body more than once: rule resolution %d ns/op, single read %d ns/op", fullPath.NsPerOp(), singleRead.NsPerOp())
	}
}
