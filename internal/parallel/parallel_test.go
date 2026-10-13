package parallel

import (
	"bytes"
	"context"
	"sync/atomic"
	"testing"
)

func TestForEach_VisitsEveryItem(t *testing.T) {
	items := make([]int, 500)
	for i := range items {
		items[i] = i
	}
	var sum atomic.Int64
	var progress bytes.Buffer
	ForEach(context.Background(), items, Options{Workers: 8, Progress: &progress, Label: "test"}, func(i int) {
		sum.Add(int64(i))
	})
	if got, want := sum.Load(), int64(499*500/2); got != want {
		t.Errorf("sum = %d, want %d", got, want)
	}
	if !bytes.Contains(progress.Bytes(), []byte("test")) {
		t.Errorf("progress output missing label: %q", progress.String())
	}
}

func TestForEach_StopsOnCancelAndStop(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	var calls atomic.Int32
	ForEach(ctx, []int{1, 2, 3}, Options{Workers: 2}, func(int) { calls.Add(1) })
	if calls.Load() != 0 {
		t.Errorf("%d calls after cancellation", calls.Load())
	}

	ForEach(context.Background(), []int{1, 2, 3}, Options{Workers: 1, Stop: func() bool { return calls.Load() >= 1 }}, func(int) { calls.Add(1) })
	if calls.Load() != 1 {
		t.Errorf("%d calls, want Stop to halt after the first", calls.Load())
	}
}

func TestForEach_ZeroWorkersStillRuns(t *testing.T) {
	var calls atomic.Int32
	ForEach(context.Background(), []int{1, 2}, Options{}, func(int) { calls.Add(1) })
	if calls.Load() != 2 {
		t.Errorf("calls = %d, want 2", calls.Load())
	}
}
