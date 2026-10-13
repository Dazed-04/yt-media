package tui

import "testing"

func TestBatchStatsExcludeFailuresFromGoal(t *testing.T) {
	m := &Model{Jobs: []DownloadJob{
		{Status: StatusDone},
		{Status: StatusError},
		{Status: StatusDownloading, Progress: 0.5},
		{Status: StatusPending},
	}}
	st := m.batchStats()
	if st.total != 4 || st.done != 1 || st.failed != 1 || st.active != 2 {
		t.Fatalf("unexpected stats %+v", st)
	}
	if st.goal() != 3 {
		t.Fatalf("goal = %d, want 3", st.goal())
	}
	if st.progress != 1.5 {
		t.Fatalf("progress = %v, want 1.5", st.progress)
	}
}
