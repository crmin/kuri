package main

import (
	"context"
	"errors"
	"slices"
	"testing"
)

func TestMenuResumeRollsBackAfterFinishFailure(t *testing.T) {
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	denied := errors.New("authentication canceled")
	var calls []string
	steps := menuResumeSteps{
		begin:  func(context.Context) error { calls = append(calls, "begin"); return nil },
		start:  func(context.Context) (int, error) { calls = append(calls, "start"); return 23, nil },
		finish: func(context.Context) error { calls = append(calls, "finish"); cancel(); return denied },
		rollback: func(ctx context.Context) error {
			calls = append(calls, "rollback")
			if ctx.Err() != nil {
				t.Fatal("canceled start must still be rolled back")
			}
			return nil
		},
	}
	pid, err := runMenuResume(ctx, steps)
	if !errors.Is(err, denied) || pid != 0 || !slices.Equal(calls, []string{"begin", "start", "finish", "rollback"}) {
		t.Fatalf("resume: pid %d, error %v, calls %v", pid, err, calls)
	}
}
