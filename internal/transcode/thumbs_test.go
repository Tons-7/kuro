package transcode

import "testing"

func TestPlanSheetNeverSamplesPastTheEnd(t *testing.T) {
	tests := []struct {
		duration          float64
		interval          float64
		count, cols, rows int
	}{
		{180, 2, 90, 10, 9},
		{181, 2, 90, 10, 9},
		{182.5, 2, 91, 10, 10},
		{1440, 10, 144, 12, 12},
		{1421.5, 10, 143, 12, 12},
		{1, 2, 1, 1, 1},
	}
	for _, tt := range tests {
		got := PlanSheet(tt.duration)
		if got.Interval != tt.interval || got.Count != tt.count || got.Columns != tt.cols || got.Rows != tt.rows {
			t.Errorf("PlanSheet(%v) = %+v, want interval %v count %d grid %dx%d",
				tt.duration, got, tt.interval, tt.count, tt.cols, tt.rows)
		}
		if last := float64(got.Count-1) * got.Interval; last > tt.duration-1 {
			t.Errorf("PlanSheet(%v): last frame at %vs is too close to the end", tt.duration, last)
		}
	}
	if got := PlanSheet(0); got.Count != 0 {
		t.Errorf("PlanSheet(0) = %+v, want empty", got)
	}
}
