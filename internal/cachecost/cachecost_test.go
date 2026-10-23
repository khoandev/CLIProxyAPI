package cachecost

import "testing"

func resetRatio(t *testing.T) {
	t.Helper()
	t.Cleanup(func() {
		ratio.Store(0)
		ratioConfigured.Store(false)
	})
}

func TestRatioUnsetIsNoOp(t *testing.T) {
	resetRatio(t)
	if got := Ratio(); got != 1 {
		t.Fatalf("unset ratio = %v, want 1", got)
	}
}

func TestSetRatioRoundTrip(t *testing.T) {
	resetRatio(t)
	SetRatio(0.5)
	if got := Ratio(); got != 0.5 {
		t.Fatalf("ratio = %v, want 0.5", got)
	}
}

func TestSetRatioClampsInvalidValues(t *testing.T) {
	resetRatio(t)
	for _, value := range []float64{-1, 1.5, 2} {
		SetRatio(value)
		if got := Ratio(); got != Default {
			t.Fatalf("ratio for %v = %v, want %v", value, got, Default)
		}
	}
}

func TestSetRatioAcceptsZero(t *testing.T) {
	resetRatio(t)
	SetRatio(0)
	if got := Ratio(); got != 0 {
		t.Fatalf("ratio = %v, want 0", got)
	}
}

func TestScaledCacheTokens(t *testing.T) {
	cases := []struct {
		name   string
		tokens int64
		ratio  float64
		want   int64
	}{
		{name: "zero ratio removes all cache tokens", tokens: 90, ratio: 0, want: 0},
		{name: "ratio one keeps tokens", tokens: 90, ratio: 1, want: 90},
		{name: "default keeps ninety percent", tokens: 90, ratio: Default, want: 81},
		{name: "half splits evenly", tokens: 90, ratio: 0.5, want: 45},
		{name: "floors remainder to uncached", tokens: 90, ratio: 0.3, want: 27},
		{name: "zero tokens stay zero", tokens: 0, ratio: 0.5, want: 0},
		{name: "negative tokens clamp to zero", tokens: -5, ratio: 0.5, want: 0},
		{name: "negative ratio uses default", tokens: 90, ratio: -1, want: 81},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := ScaledCacheTokens(tc.tokens, tc.ratio); got != tc.want {
				t.Fatalf("ScaledCacheTokens(%d, %v) = %d, want %d", tc.tokens, tc.ratio, got, tc.want)
			}
		})
	}
}
