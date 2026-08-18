package service

import (
	"context"
	"log"
	"time"

	"easydeploy/internal/docker"
)

// RunAutoscaler runs the CPU-based autoscaling control loop until ctx is
// cancelled. Every interval it samples each autoscaled service's average
// replica CPU and steps the replica count toward the target.
func (m *Manager) RunAutoscaler(ctx context.Context, interval time.Duration) {
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			m.scaleOnce(ctx)
		}
	}
}

// scaleOnce evaluates every autoscaled service once. Scaling is stepwise with
// hysteresis: add a replica when average CPU exceeds the target, remove one
// when it drops below half the target, always staying within [min, max].
func (m *Manager) scaleOnce(ctx context.Context) {
	svcs, err := m.store.ListServices(ctx, "")
	if err != nil {
		return
	}
	for _, svc := range svcs {
		if !svc.Autoscale {
			continue
		}
		cli, err := m.dockerFor(ctx, svc)
		if err != nil {
			continue
		}
		reps, err := cli.ListByLabel(ctx, docker.LabelService+"="+svc.Name, false)
		if err != nil || len(reps) == 0 {
			continue
		}
		var total float64
		for _, c := range reps {
			if p, err := cli.CPUPercent(ctx, c.ID); err == nil {
				total += p
			}
		}
		avg := total / float64(len(reps))
		current := len(reps)
		target := float64(svc.TargetCPUPct)

		desired := current
		switch {
		case avg > target && current < svc.MaxReplicas:
			desired = current + 1
		case avg < target/2 && current > svc.MinReplicas:
			desired = current - 1
		}
		desired = clamp(desired, svc.MinReplicas, svc.MaxReplicas)

		if desired != current {
			log.Printf("autoscale %s: avg CPU %.0f%% (target %.0f%%), %d -> %d replicas",
				svc.Name, avg, target, current, desired)
			if err := m.Scale(ctx, svc.Name, desired); err != nil {
				log.Printf("autoscale %s: scale failed: %v", svc.Name, err)
			}
		}
	}
}

func clamp(v, lo, hi int) int {
	if v < lo {
		return lo
	}
	if v > hi {
		return hi
	}
	return v
}
