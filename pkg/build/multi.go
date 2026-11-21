package build

import (
	"context"
	"fmt"
	"io"
	"time"

	"github.com/sensi012/skipper/pkg/config"
)

// ServiceBuildTarget describes one service to build or skip in a multi-service build.
type ServiceBuildTarget struct {
	Name       string
	Spec       *config.AppSpec
	SkipReason string
}

// ServiceBuildResult holds the build outcome for an individual service.
type ServiceBuildResult struct {
	Service    string `json:"service"`
	Image      string `json:"image,omitempty"`
	Context    string `json:"context,omitempty"`
	Status     string `json:"status"`
	Error      string `json:"error,omitempty"`
	DurationMs int64  `json:"durationMs"`
}

// BuildAll sequentially builds all targets, continuing past individual failures and
// recording built, skipped, and failed results.
func (b *CNBBuilder) BuildAll(ctx context.Context, targets []ServiceBuildTarget, out io.Writer) []ServiceBuildResult {
	if out == nil {
		out = io.Discard
	}
	results := make([]ServiceBuildResult, 0, len(targets))
	total := len(targets)

	for i, target := range targets {
		var buildCtx, img string
		if target.Spec != nil {
			buildCtx = target.Spec.Build.Context
			img = target.Spec.Image
		}

		if target.SkipReason != "" {
			_, _ = fmt.Fprintf(out, "==> [%d/%d] Skipping service '%s' (%s)\n", i+1, total, target.Name, target.SkipReason)
			results = append(results, ServiceBuildResult{
				Service: target.Name,
				Image:   img,
				Context: buildCtx,
				Status:  "skipped",
			})
			continue
		}

		_, _ = fmt.Fprintf(out, "==> [%d/%d] Building service '%s' (context: %s)\n", i+1, total, target.Name, buildCtx)
		start := time.Now()
		tag, err := b.Build(ctx, target.Spec, out)
		durMs := time.Since(start).Milliseconds()

		if err != nil {
			_, _ = fmt.Fprintf(out, "==> [!] Service '%s' failed: %v\n", target.Name, err)
			results = append(results, ServiceBuildResult{
				Service:    target.Name,
				Image:      img,
				Context:    buildCtx,
				Status:     "failed",
				Error:      err.Error(),
				DurationMs: durMs,
			})
			continue
		}

		results = append(results, ServiceBuildResult{
			Service:    target.Name,
			Image:      tag,
			Context:    buildCtx,
			Status:     "built",
			DurationMs: durMs,
		})
	}

	return results
}
