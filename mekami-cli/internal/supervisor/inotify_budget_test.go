package supervisor

import (
	"sync"
	"testing"
)

func TestInotifyBudget_SetAndTotal(t *testing.T) {
	b := NewInotifyBudget()
	b.SetDaemonWatches("/proj/a", 100)
	b.SetDaemonWatches("/proj/b", 250)
	if got := b.Usage(); got != 350 {
		t.Fatalf("usage = %d, want 350", got)
	}
}

func TestInotifyBudget_ReplaceValue(t *testing.T) {
	b := NewInotifyBudget()
	b.SetDaemonWatches("/proj/a", 100)
	b.SetDaemonWatches("/proj/a", 50)
	if got := b.Usage(); got != 50 {
		t.Fatalf("after replace usage = %d, want 50", got)
	}
	b.SetDaemonWatches("/proj/a", 75)
	if got := b.Usage(); got != 75 {
		t.Fatalf("after second replace usage = %d, want 75", got)
	}
}

func TestInotifyBudget_RemoveZero(t *testing.T) {
	b := NewInotifyBudget()
	b.SetDaemonWatches("/proj/a", 100)
	b.SetDaemonWatches("/proj/b", 50)
	b.SetDaemonWatches("/proj/a", 0)
	if got := b.Usage(); got != 50 {
		t.Fatalf("after remove usage = %d, want 50", got)
	}
	if _, ok := b.perDaemon["/proj/a"]; ok {
		t.Fatalf("expected /proj/a to be removed from perDaemon")
	}
}

func TestInotifyBudget_LevelBuckets(t *testing.T) {
	b := &InotifyBudget{
		limit:     1000,
		perDaemon: make(map[string]int64),
	}
	cases := []struct {
		usage int64
		want  BudgetLevel
	}{
		{0, BudgetOK},
		{599, BudgetOK},
		{600, BudgetWarning},
		{799, BudgetWarning},
		{800, BudgetDegraded},
		{949, BudgetDegraded},
		{950, BudgetCritical},
		{1000, BudgetCritical},
		{2000, BudgetCritical},
	}
	for _, c := range cases {
		b.usage = c.usage
		if got := b.Level(); got != c.want {
			t.Errorf("usage=%d: got %v, want %v", c.usage, got, c.want)
		}
	}
}

func TestInotifyBudget_UnknownLevel(t *testing.T) {
	// Build a budget with an unknown limit directly; on Linux
	// NewInotifyBudget would probe /proc and find a real value.
	b := &InotifyBudget{limit: -1, perDaemon: make(map[string]int64)}
	b.SetDaemonWatches("/p", 100)
	if b.Level() != BudgetUnknown {
		t.Fatalf("expected BudgetUnknown with limit=-1, got %v", b.Level())
	}
	if b.Percent() != -1 {
		t.Fatalf("expected Percent=-1 with limit=-1")
	}
}

func TestInotifyBudget_SuggestPollingTargets(t *testing.T) {
	b := NewInotifyBudget()
	b.SetDaemonWatches("/small", 10)
	b.SetDaemonWatches("/big", 1000)
	b.SetDaemonWatches("/medium", 500)
	got := b.SuggestPollingTargets(2)
	if len(got) != 2 {
		t.Fatalf("expected 2 entries, got %d", len(got))
	}
	if got[0].Root != "/big" || got[1].Root != "/medium" {
		t.Fatalf("ordering wrong: %+v", got)
	}
}

func TestInotifyBudget_ConcurrentSafe(t *testing.T) {
	b := NewInotifyBudget()
	var wg sync.WaitGroup
	for i := 0; i < 20; i++ {
		wg.Add(2)
		go func(i int) {
			defer wg.Done()
			b.SetDaemonWatches("/p", 1)
		}(i)
		go func() {
			defer wg.Done()
			_ = b.Level()
		}()
	}
	wg.Wait()
	if got := b.Usage(); got != 1 {
		t.Fatalf("expected final usage=1 (replace semantics), got %d", got)
	}
}
