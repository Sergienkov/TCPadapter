package main

import (
	"testing"
	"time"
)

func TestResolveProfileCycle(t *testing.T) {
	got, err := resolveProfileCycle("healthy", "healthy,flaky,bursty")
	if err != nil {
		t.Fatalf("resolveProfileCycle() error = %v", err)
	}
	if len(got) != 3 || got[1] != "flaky" || got[2] != "bursty" {
		t.Fatalf("unexpected profile cycle: %#v", got)
	}
}

func TestApplyProfile(t *testing.T) {
	base := simConfig{
		ackDelay:       100 * time.Millisecond,
		ackErrorRate:   0,
		ackDropRate:    0,
		burstSize:      1,
		burstSpacing:   50 * time.Millisecond,
		statusInterval: 5 * time.Second,
		bufferFree:     1500,
	}

	flaky, err := applyProfile(base, "flaky")
	if err != nil {
		t.Fatalf("applyProfile(flaky) error = %v", err)
	}
	if flaky.ackErrorRate < 0.15 || flaky.ackDropRate < 0.20 || flaky.bufferFree != 900 {
		t.Fatalf("unexpected flaky profile: %+v", flaky)
	}

	starved, err := applyProfile(base, "starved")
	if err != nil {
		t.Fatalf("applyProfile(starved) error = %v", err)
	}
	if starved.bufferFree != 96 {
		t.Fatalf("unexpected starved buffer_free: %+v", starved)
	}
}

func TestControllerStateApplyTriggerPayload(t *testing.T) {
	st := newControllerState(1500)
	payload := []byte{1, 0, 1, 1, 0, 1, 0, 1, 0, 0, 0, 0, 0, 0, 0, 0}
	if err := st.applyTriggerPayload(payload); err != nil {
		t.Fatalf("applyTriggerPayload() error = %v", err)
	}
	if !st.triggers.ManualUnlock || st.triggers.ScenarioUnlock || !st.triggers.AutoRegistration || !st.triggers.OpenForAll {
		t.Fatalf("unexpected trigger state: %+v", st.triggers)
	}
	if !st.triggers.NotifyUnknownIDSMS || !st.triggers.ScheduleForAllCalls {
		t.Fatalf("unexpected trigger state: %+v", st.triggers)
	}
}
