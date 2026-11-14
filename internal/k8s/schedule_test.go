package k8s

import (
	"context"
	"testing"
	"time"
)

func TestScheduleNormalize(t *testing.T) {
	cases := []struct {
		name     string
		schedule Schedule
		wantErr  bool
		want     Schedule
	}{
		{
			name:     "normal",
			schedule: Schedule{Enabled: true, Timezone: "Europe/Paris", Start: "09:00", Stop: "18:00"},
			want:     Schedule{Enabled: true, Timezone: "Europe/Paris", Start: "09:00", Stop: "18:00"},
		},
		{
			name:     "overnight",
			schedule: Schedule{Enabled: true, Timezone: "UTC", Start: "22:00", Stop: "06:00"},
			want:     Schedule{Enabled: true, Timezone: "UTC", Start: "22:00", Stop: "06:00"},
		},
		{
			name:     "disabled empty",
			schedule: Schedule{},
			want:     Schedule{},
		},
		{name: "bad timezone", schedule: Schedule{Enabled: true, Timezone: "Mars/Olympus", Start: "09:00", Stop: "18:00"}, wantErr: true},
		{name: "bad start", schedule: Schedule{Enabled: true, Timezone: "UTC", Start: "9", Stop: "18:00"}, wantErr: true},
		{name: "equal times", schedule: Schedule{Enabled: true, Timezone: "UTC", Start: "09:00", Stop: "09:00"}, wantErr: true},
		{name: "missing fields", schedule: Schedule{Enabled: true, Timezone: "UTC"}, wantErr: true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			schedule := tc.schedule
			err := schedule.Normalize()
			if tc.wantErr {
				if err == nil {
					t.Fatalf("expected error, got %+v", schedule)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if schedule != tc.want {
				t.Fatalf("schedule = %+v, want %+v", schedule, tc.want)
			}
		})
	}
}

func TestScheduleIsActive(t *testing.T) {
	normal := Schedule{Enabled: true, Timezone: "UTC", Start: "09:00", Stop: "18:00"}
	overnight := Schedule{Enabled: true, Timezone: "UTC", Start: "22:00", Stop: "06:00"}
	cases := []struct {
		name     string
		schedule Schedule
		now      time.Time
		want     bool
	}{
		{"disabled", Schedule{}, time.Date(2026, 1, 15, 12, 0, 0, 0, time.UTC), true},
		{"before start", normal, time.Date(2026, 1, 15, 8, 59, 0, 0, time.UTC), false},
		{"at start", normal, time.Date(2026, 1, 15, 9, 0, 0, 0, time.UTC), true},
		{"inside", normal, time.Date(2026, 1, 15, 12, 0, 0, 0, time.UTC), true},
		{"at stop", normal, time.Date(2026, 1, 15, 18, 0, 0, 0, time.UTC), false},
		{"overnight evening", overnight, time.Date(2026, 1, 15, 23, 0, 0, 0, time.UTC), true},
		{"overnight morning", overnight, time.Date(2026, 1, 15, 5, 59, 0, 0, time.UTC), true},
		{"overnight at stop", overnight, time.Date(2026, 1, 15, 6, 0, 0, 0, time.UTC), false},
		{"overnight daytime", overnight, time.Date(2026, 1, 15, 12, 0, 0, 0, time.UTC), false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := tc.schedule.IsActive(tc.now)
			if err != nil {
				t.Fatal(err)
			}
			if got != tc.want {
				t.Fatalf("IsActive(%s) = %v, want %v", tc.now, got, tc.want)
			}
		})
	}
}

func TestSetScheduleRunningAndStopped(t *testing.T) {
	m := newTestManager()
	ctx := context.Background()
	schedule := Schedule{Enabled: true, Timezone: "UTC", Start: "09:00", Stop: "18:00"}

	if _, err := m.Create(ctx, CreateRequest{Name: "app"}); err != nil {
		t.Fatal(err)
	}
	ws, err := m.SetSchedule(ctx, "app", schedule)
	if err != nil {
		t.Fatal(err)
	}
	if ws.Schedule == nil || *ws.Schedule != schedule {
		t.Fatalf("running schedule = %+v", ws.Schedule)
	}
	sts, err := m.GetStatefulSet(ctx, "app")
	if err != nil {
		t.Fatal(err)
	}
	state := stateFromStatefulSet(sts)
	if state.Schedule == nil || *state.Schedule != schedule {
		t.Fatalf("annotation schedule = %+v", state.Schedule)
	}

	if _, err := m.Stop(ctx, "app"); err != nil {
		t.Fatal(err)
	}
	ws, err = m.SetSchedule(ctx, "app", schedule)
	if err != nil {
		t.Fatal(err)
	}
	if ws.Schedule == nil || *ws.Schedule != schedule {
		t.Fatalf("stopped schedule = %+v", ws.Schedule)
	}
	state, err = m.State(ctx, "app")
	if err != nil {
		t.Fatal(err)
	}
	if state.Schedule == nil || *state.Schedule != schedule {
		t.Fatalf("state schedule = %+v", state.Schedule)
	}

	ws, err = m.SetSchedule(ctx, "app", Schedule{})
	if err != nil {
		t.Fatal(err)
	}
	if ws.Schedule != nil {
		t.Fatalf("cleared schedule = %+v", ws.Schedule)
	}
}

func TestReconcileSchedulesStopsAndStarts(t *testing.T) {
	m := newTestManager()
	ctx := context.Background()
	schedule := &Schedule{Enabled: true, Timezone: "UTC", Start: "09:00", Stop: "18:00"}
	if _, err := m.Create(ctx, CreateRequest{Name: "app", Schedule: schedule}); err != nil {
		t.Fatal(err)
	}

	if err := m.ReconcileSchedules(ctx, time.Date(2026, 1, 15, 20, 0, 0, 0, time.UTC)); err != nil {
		t.Fatal(err)
	}
	ws, err := m.GetWorkspace(ctx, "app")
	if err != nil {
		t.Fatal(err)
	}
	if ws.Phase != "Stopped" || !ws.StoppedBySchedule {
		t.Fatalf("after stop = %+v", ws)
	}
	if _, err := m.Get(ctx, "app"); err == nil {
		t.Fatal("pod still exists after scheduled stop")
	}

	if err := m.ReconcileSchedules(ctx, time.Date(2026, 1, 15, 10, 0, 0, 0, time.UTC)); err != nil {
		t.Fatal(err)
	}
	if _, err := m.Get(ctx, "app"); err != nil {
		t.Fatalf("pod was not started: %v", err)
	}
	sts, err := m.GetStatefulSet(ctx, "app")
	if err != nil {
		t.Fatal(err)
	}
	state := stateFromStatefulSet(sts)
	if state.Schedule == nil || *state.Schedule != *schedule {
		t.Fatalf("schedule lost after restart: %+v", state.Schedule)
	}
}

func TestReconcileSchedulesDoesNotStartManualStop(t *testing.T) {
	m := newTestManager()
	ctx := context.Background()
	schedule := &Schedule{Enabled: true, Timezone: "UTC", Start: "09:00", Stop: "18:00"}
	if _, err := m.Create(ctx, CreateRequest{Name: "app", Schedule: schedule}); err != nil {
		t.Fatal(err)
	}
	if _, err := m.Stop(ctx, "app"); err != nil {
		t.Fatal(err)
	}
	if err := m.ReconcileSchedules(ctx, time.Date(2026, 1, 15, 10, 0, 0, 0, time.UTC)); err != nil {
		t.Fatal(err)
	}
	ws, err := m.GetWorkspace(ctx, "app")
	if err != nil {
		t.Fatal(err)
	}
	if ws.Phase != "Stopped" || ws.StoppedBySchedule {
		t.Fatalf("manual stop was changed = %+v", ws)
	}
}
