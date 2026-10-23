package auth

import (
	"net/http"
	"testing"
)

func TestModelQuotaOrCapacityError(t *testing.T) {
	tests := []struct {
		name string
		err  error
		want bool
	}{
		{
			name: "capacity 429",
			err:  &Error{HTTPStatus: http.StatusTooManyRequests, Message: "Selected model is at capacity. Please try a different model."},
			want: true,
		},
		{
			name: "quota 503",
			err:  &Error{HTTPStatus: http.StatusServiceUnavailable, Message: "No capacity available for model gpt-5.5 on the server"},
			want: true,
		},
		{
			name: "auth cooldown excluded",
			err:  &Error{HTTPStatus: http.StatusTooManyRequests, Message: "auth in short cooldown"},
			want: false,
		},
		{
			name: "bare rate limit",
			err:  &Error{HTTPStatus: http.StatusTooManyRequests, Message: "rate limited"},
			want: false,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := ModelQuotaOrCapacityError(tt.err); got != tt.want {
				t.Fatalf("ModelQuotaOrCapacityError() = %v, want %v", got, tt.want)
			}
		})
	}
}
