package ufs

import (
	"math"
	"testing"
)

func TestQuotaCanFitRejectsOverflowingSize(t *testing.T) {
	q := NewQuota(nil, 1<<30)
	q.SetUsage(1)

	if q.CanFit(math.MaxInt64) {
		t.Fatal("expected oversized write to be rejected")
	}

	q.SetUsage(1 << 20)
	if q.CanFit(math.MaxInt64 - 100) {
		t.Fatal("expected oversized write to be rejected")
	}
}

func TestQuotaCanFitAllowsShrinkingWrite(t *testing.T) {
	q := NewQuota(nil, 10)
	q.SetUsage(20)

	if !q.CanFit(-5) {
		t.Fatal("expected shrinking write to be allowed")
	}
}

func TestQuotaAddDoesNotResetOnPositiveOverflow(t *testing.T) {
	q := NewQuota(nil, 0)
	q.SetUsage(math.MaxInt64 - 1)

	if got := q.Add(10); got != math.MaxInt64 {
		t.Fatalf("expected usage to saturate at MaxInt64, got %d", got)
	}
}

func TestQuotaAddClampsSubtractionAtZero(t *testing.T) {
	q := NewQuota(nil, 0)
	q.SetUsage(5)

	if got := q.Add(-10); got != 0 {
		t.Fatalf("expected usage to clamp at zero, got %d", got)
	}
}
