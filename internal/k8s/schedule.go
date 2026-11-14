package k8s

import (
	"context"
	"errors"
	"fmt"
	"log"
	"strings"
	"time"
	_ "time/tzdata"

	appsv1 "k8s.io/api/apps/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime/schema"
)

const (
	// DefaultScheduleInterval is how often workspace schedules are evaluated.
	DefaultScheduleInterval = 30 * time.Second
	clockLayout             = "15:04"
)

// Schedule keeps a workspace running only inside a daily time window.
// Start and stop are local times in the configured IANA timezone. A window
// where start is after stop spans midnight.
type Schedule struct {
	Enabled  bool   `json:"enabled" yaml:"enabled"`
	Timezone string `json:"timezone,omitempty" yaml:"timezone,omitempty"`
	Start    string `json:"start,omitempty" yaml:"start,omitempty"`
	Stop     string `json:"stop,omitempty" yaml:"stop,omitempty"`
}

// Normalize trims and validates the schedule. It accepts disabled schedules
// with empty fields, which clears the schedule when saved.
func (s *Schedule) Normalize() error {
	s.Timezone = strings.TrimSpace(s.Timezone)
	s.Start = strings.TrimSpace(s.Start)
	s.Stop = strings.TrimSpace(s.Stop)

	if s.Start != "" {
		start, err := time.Parse(clockLayout, s.Start)
		if err != nil {
			return fmt.Errorf("invalid start time %q: use HH:MM", s.Start)
		}
		s.Start = start.Format(clockLayout)
	}
	if s.Stop != "" {
		stop, err := time.Parse(clockLayout, s.Stop)
		if err != nil {
			return fmt.Errorf("invalid stop time %q: use HH:MM", s.Stop)
		}
		s.Stop = stop.Format(clockLayout)
	}
	if s.Timezone != "" {
		if _, err := time.LoadLocation(s.Timezone); err != nil {
			return fmt.Errorf("invalid timezone %q", s.Timezone)
		}
	}
	if !s.Enabled {
		return nil
	}
	if s.Timezone == "" || s.Start == "" || s.Stop == "" {
		return fmt.Errorf("timezone, start, and stop are required when the schedule is enabled")
	}
	if s.Start == s.Stop {
		return fmt.Errorf("start and stop times must differ")
	}
	return nil
}

// Empty reports whether the schedule carries no configuration.
func (s Schedule) Empty() bool {
	return !s.Enabled && s.Timezone == "" && s.Start == "" && s.Stop == ""
}

// IsActive reports whether the workspace should be running at now.
func (s Schedule) IsActive(now time.Time) (bool, error) {
	if !s.Enabled {
		return true, nil
	}
	location, err := time.LoadLocation(s.Timezone)
	if err != nil {
		return false, fmt.Errorf("invalid timezone %q: %w", s.Timezone, err)
	}
	start, err := clockMinutes(s.Start)
	if err != nil {
		return false, err
	}
	stop, err := clockMinutes(s.Stop)
	if err != nil {
		return false, err
	}
	if start == stop {
		return false, fmt.Errorf("start and stop times must differ")
	}
	local := now.In(location)
	current := local.Hour()*60 + local.Minute()
	if start < stop {
		return current >= start && current < stop, nil
	}
	return current >= start || current < stop, nil
}

func clockMinutes(value string) (int, error) {
	parsed, err := time.Parse(clockLayout, value)
	if err != nil {
		return 0, fmt.Errorf("invalid time %q: use HH:MM", value)
	}
	return parsed.Hour()*60 + parsed.Minute(), nil
}

func cloneSchedule(schedule *Schedule) *Schedule {
	if schedule == nil {
		return nil
	}
	copy := *schedule
	return &copy
}

// SetSchedule updates the schedule of a running or stopped workspace.
func (m *Manager) SetSchedule(ctx context.Context, name string, schedule Schedule) (Workspace, error) {
	if err := validateName(name); err != nil {
		return Workspace{}, err
	}
	if err := schedule.Normalize(); err != nil {
		return Workspace{}, err
	}
	stored := cloneSchedule(&schedule)
	if schedule.Empty() {
		stored = nil
	}

	release, err := m.lockWorkspace(ctx, name)
	if err != nil {
		return Workspace{}, err
	}
	defer release()

	if _, err := m.getStatefulSet(ctx, name); err == nil {
		updated, err := m.mutateStatefulSet(ctx, name, func(sts *appsv1.StatefulSet) error {
			sts.Annotations = annotationsWithSchedule(sts.Annotations, stored)
			sts.Spec.Template.Annotations = annotationsWithSchedule(sts.Spec.Template.Annotations, stored)
			return nil
		})
		if err != nil {
			return Workspace{}, err
		}
		return workspaceFromStatefulSet(updated), nil
	} else if !apierrors.IsNotFound(err) {
		return Workspace{}, err
	}

	pod, err := m.Get(ctx, name)
	if err == nil {
		state := m.stateForPod(ctx, pod)
		state.Schedule = stored
		raw, err := marshalState(state)
		if err != nil {
			return Workspace{}, err
		}
		if pod.Annotations == nil {
			pod.Annotations = map[string]string{}
		}
		pod.Annotations[annotationState] = raw
		updated, err := m.Client.CoreV1().Pods(m.Namespace).Update(ctx, pod, metav1.UpdateOptions{})
		if err != nil {
			return Workspace{}, err
		}
		return toWorkspace(updated), nil
	}
	if !apierrors.IsNotFound(err) {
		return Workspace{}, err
	}

	var out WorkspaceState
	err = m.stateStore().Update(ctx, func(states map[string]WorkspaceState) error {
		state, ok := states[name]
		if !ok {
			return apierrors.NewNotFound(schema.GroupResource{Resource: "workspaces"}, name)
		}
		if state.Name == "" {
			state.Name = name
		}
		state.Schedule = stored
		states[name] = state
		out = state
		return nil
	})
	if err != nil {
		return Workspace{}, err
	}
	return workspaceFromState(out, "Stopped"), nil
}

// ReconcileSchedules stops workspaces outside their active window and starts
// them again at the next window only when the scheduler stopped them.
func (m *Manager) ReconcileSchedules(ctx context.Context, now time.Time) error {
	workspaces, err := m.List(ctx)
	if err != nil {
		return err
	}
	for _, workspace := range workspaces {
		if workspace.Schedule == nil || !workspace.Schedule.Enabled {
			continue
		}
		active, err := workspace.Schedule.IsActive(now)
		if err != nil {
			log.Printf("schedule %s: %v", workspace.Name, err)
			continue
		}
		if !active {
			if workspace.Phase == "Stopped" || workspace.Phase == "Terminating" {
				continue
			}
			if _, err := m.StopScheduled(ctx, workspace.Name); err != nil && !errors.Is(err, ErrNotRunning) && !apierrors.IsNotFound(err) {
				log.Printf("schedule stop %s: %v", workspace.Name, err)
			}
			continue
		}
		if workspace.Phase == "Stopped" && workspace.StoppedBySchedule {
			if _, err := m.Start(ctx, workspace.Name); err != nil && !errors.Is(err, ErrAlreadyRunning) && !apierrors.IsNotFound(err) {
				log.Printf("schedule start %s: %v", workspace.Name, err)
			}
		}
	}
	return nil
}

// RunScheduleLoop evaluates schedules until the context is cancelled.
func (m *Manager) RunScheduleLoop(ctx context.Context, interval time.Duration) {
	if interval <= 0 {
		interval = DefaultScheduleInterval
	}
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			if err := m.ReconcileSchedules(ctx, time.Now()); err != nil {
				log.Printf("workspace schedules: %v", err)
			}
		}
	}
}
