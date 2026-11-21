package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"strings"
	"time"

	"github.com/spf13/cobra"
	"github.com/sensi012/skipper/pkg/bootstrapper"
	"github.com/sensi012/skipper/pkg/build"
	"github.com/sensi012/skipper/pkg/cluster"
	"github.com/sensi012/skipper/pkg/config"
	"github.com/sensi012/skipper/pkg/dev"
	"github.com/sensi012/skipper/pkg/diagnostics"
	"github.com/sensi012/skipper/pkg/doctor"
	"github.com/sensi012/skipper/pkg/gitops"
	"github.com/sensi012/skipper/pkg/governance"
	"github.com/sensi012/skipper/pkg/local"
	"github.com/sensi012/skipper/pkg/mcp"
	"github.com/sensi012/skipper/pkg/opmem"
	"github.com/sensi012/skipper/pkg/rollout"
	"github.com/sensi012/skipper/pkg/runtime"
	"github.com/sensi012/skipper/pkg/secret"
	"github.com/sensi012/skipper/pkg/security"
	"github.com/sensi012/skipper/pkg/synthesizer"
	"github.com/sensi012/skipper/pkg/transpile"
	"github.com/sensi012/skipper/pkg/tunnel"
	"github.com/sensi012/skipper/schemas"
	"gopkg.in/yaml.v3"
)

var (
	version       = "0.1.0"
	targetRuntime string
	namespace     string
	appConfigFile string
	outputFormat  string
)

func newRootCmd() *cobra.Command {
	rootCmd := &cobra.Command{
		Use:           "skipper",
		Short:         "Skipper: Unified Container & Kubernetes Runtime Abstraction CLI",
		Long:          "A developer-first CLI unifying container build, local execution, production deployment, and deterministic diagnostics.",
		Version:       version,
		SilenceUsage:  true,
		SilenceErrors: true,
	}

	rootCmd.PersistentFlags().StringVarP(&targetRuntime, "runtime", "r", "docker", "Target runtime: 'docker' (local) or 'kubernetes' (remote)")
	rootCmd.PersistentFlags().StringVarP(&namespace, "namespace", "n", "default", "Kubernetes namespace (for kubernetes target)")
	rootCmd.PersistentFlags().StringVarP(&appConfigFile, "config", "c", "", "Path to skipper.yaml specification (defaults to skipper.yaml or app.yaml)")
	rootCmd.PersistentFlags().StringVarP(&outputFormat, "format", "f", "text", "Output format: 'text' or 'json' (optimized for AI agents)")

	rootCmd.AddCommand(newBuildCmd())
	rootCmd.AddCommand(newManifestCmd())
	rootCmd.AddCommand(newScanCmd())
	rootCmd.AddCommand(newDeployCmd())
	rootCmd.AddCommand(newUpCmd())
	rootCmd.AddCommand(newStatusCmd())
	rootCmd.AddCommand(newLogsCmd())
	rootCmd.AddCommand(newDiagnoseCmd())
	rootCmd.AddCommand(newRightsizingCmd())
	rootCmd.AddCommand(newPolicyCmd())
	rootCmd.AddCommand(newDebugCmd())
	rootCmd.AddCommand(newTimeTravelCmd())
	rootCmd.AddCommand(newDownCmd())
	rootCmd.AddCommand(newLocalCmd())
	rootCmd.AddCommand(newDoctorCmd())
	rootCmd.AddCommand(newInitCmd())
	rootCmd.AddCommand(newDevCmd())
	rootCmd.AddCommand(newPortForwardCmd())
	rootCmd.AddCommand(newSecretCmd())
	rootCmd.AddCommand(newImportCmd())
	rootCmd.AddCommand(newMCPCmd())
	rootCmd.AddCommand(newExplainCmd())
	rootCmd.AddCommand(newSBOMCmd())
	rootCmd.AddCommand(newSignCmd())
	rootCmd.AddCommand(newObserveCmd())
	rootCmd.AddCommand(newGitOpsCmd())
	rootCmd.AddCommand(newContextCmd())

	return rootCmd
}

func main() {
	rootCmd := newRootCmd()
	if err := rootCmd.Execute(); err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}
}

func getRuntimeAdapter() runtime.RuntimeEngine {
	if targetRuntime == "kubernetes" {
		return runtime.NewKubernetesAdapter(namespace)
	}
	return runtime.NewDockerAdapter()
}

func loadSpec() (*config.AppSpec, error) {
	configFile := appConfigFile
	if configFile == "" {
		if _, err := os.Stat("skipper.yaml"); err == nil {
			configFile = "skipper.yaml"
		} else if _, err := os.Stat("app.yaml"); err == nil {
			configFile = "app.yaml"
		} else {
			configFile = "skipper.yaml"
		}
	}

	data, err := os.ReadFile(configFile)
	if err != nil {
		return nil, fmt.Errorf("unable to read %s: %w", configFile, err)
	}
	var spec config.AppSpec
	if err := yaml.Unmarshal(data, &spec); err != nil {
		return nil, fmt.Errorf("invalid %s format: %w", configFile, err)
	}
	return &spec, nil
}

func newBuildCmd() *cobra.Command {
	var imageTag string
	cmd := &cobra.Command{
		Use:   "build",
		Short: "Auto-detect stack and build OCI-compliant container image",
		RunE: func(cmd *cobra.Command, args []string) error {
			spec, err := loadSpec()
			if err != nil {
				return err
			}
			if strings.TrimSpace(imageTag) != "" {
				spec.Image = strings.TrimSpace(imageTag)
			}
			builder := build.NewCNBBuilder()
			ctx := context.Background()

			tag, err := builder.Build(ctx, spec, cmd.OutOrStdout())
			if err != nil {
				return err
			}

			// Record build into operational memory
			if ledger, err := opmem.NewOperationalMemoryLedger(""); err == nil {
				_ = ledger.RecordTelemetryEvent(&opmem.CausalEvent{
					Timestamp:   time.Now(),
					Type:        "MUTATION",
					Source:      "app build",
					Description: fmt.Sprintf("Built and tagged OCI image %s", tag),
					Severity:    "INFO",
				})
			}

			fmt.Fprintf(cmd.OutOrStdout(), "==> Successfully built and tagged OCI image: %s\n", tag)
			return nil
		},
	}
	cmd.Flags().StringVarP(&imageTag, "tag", "t", "", "Override target OCI image tag (e.g. my-app:v1.0.0)")
	return cmd
}

func newDeployCmd() *cobra.Command {
	var strategy string
	var autoRollback bool

	cmd := &cobra.Command{
		Use:   "deploy",
		Short: "Deploy with progressive delivery (canary/blue-green) and health gates",
		RunE: func(cmd *cobra.Command, args []string) error {
			spec, err := loadSpec()
			if err != nil {
				return err
			}

			// Governance policy evaluation gate
			gov := governance.NewGovernanceEngine()
			violations, _ := gov.EvaluatePolicies(spec)
			hasBlocker := false
			for _, v := range violations {
				if v.Severity == "BLOCK" {
					fmt.Printf("[POLICY VIOLATION - BLOCKED] %s: %s\n", v.RuleName, v.Message)
					hasBlocker = true
				} else {
					fmt.Printf("[POLICY WARNING] %s: %s\n", v.RuleName, v.Message)
				}
			}
			if hasBlocker {
				return fmt.Errorf("deployment halted by platform governance policy")
			}

			engine := getRuntimeAdapter()
			ctx := context.Background()

			if err := engine.Initialize(ctx); err != nil {
				return err
			}

			controller := rollout.NewController(engine)
			status, err := controller.ExecuteCanary(ctx, spec, rollout.RolloutConfig{
				Strategy:      rollout.StrategyType(strategy),
				CanarySteps:   []int{10, 25, 50, 100},
				StepInterval:  1 * time.Second,
				AutoRollback:  autoRollback,
				MaxErrorRate:  0.01,
				MaxP99Latency: 500 * time.Millisecond,
			}, os.Stdout)
			if err != nil {
				return err
			}

			// Record deployment in operational memory ledger
			if ledger, err := opmem.NewOperationalMemoryLedger(""); err == nil {
				_ = ledger.RecordChange(&opmem.ChangeRecord{
					ID:        fmt.Sprintf("chg-%d", time.Now().Unix()),
					Timestamp: time.Now(),
					Actor: opmem.ChangeActor{
						Type:       "user",
						Identifier: os.Getenv("USERNAME"),
					},
					TargetWorkload: opmem.TargetWorkload{
						Namespace: namespace,
						Name:      spec.Name,
						Kind:      "Deployment",
					},
					ChangeType: "rollout",
					Diff: map[string]interface{}{
						"strategy": strategy,
						"status":   status.Phase,
					},
				})

				_ = ledger.RecordTelemetryEvent(&opmem.CausalEvent{
					Timestamp:   time.Now(),
					Type:        "MUTATION",
					Source:      "app deploy",
					Description: fmt.Sprintf("Rollout %s for %s finished with state %s", strategy, spec.Name, status.Phase),
					Severity:    "INFO",
				})
			}

			fmt.Printf("Deploy status: %s (%s)\n", status.Phase, status.Message)
			return nil
		},
	}
	cmd.Flags().StringVarP(&strategy, "strategy", "s", "canary", "Rollout strategy: 'canary' or 'blue-green'")
	cmd.Flags().BoolVar(&autoRollback, "auto-rollback", true, "Automatically rollback on health gate failure")
	return cmd
}

func newManifestCmd() *cobra.Command {
	var outputFile string

	cmd := &cobra.Command{
		Use:   "manifest",
		Short: "Synthesize production-grade, zero-lock-in Kubernetes YAML manifests",
		RunE: func(cmd *cobra.Command, args []string) error {
			spec, err := loadSpec()
			if err != nil {
				return err
			}

			synth := synthesizer.NewSynthesizer()
			bundle, err := synth.Synthesize(spec, namespace)
			if err != nil {
				return err
			}

			manifestYaml := bundle.FullYaml()
			out := cmd.OutOrStdout()
			if outputFile != "" {
				if err := os.WriteFile(outputFile, []byte(manifestYaml), 0644); err != nil {
					return fmt.Errorf("failed to write manifest file: %w", err)
				}
				fmt.Fprintf(out, "==> Synthesized production manifests written to: %s\n", outputFile)
			} else {
				fmt.Fprintln(out, manifestYaml)
			}
			return nil
		},
	}
	cmd.Flags().StringVarP(&outputFile, "output", "o", "", "Write output to file path instead of stdout")
	return cmd
}

func newScanCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "scan [image]",
		Short: "Scan OCI image for security vulnerabilities and misconfigurations",
		RunE: func(cmd *cobra.Command, args []string) error {
			imageName := ""
			if len(args) > 0 {
				imageName = args[0]
			} else {
				spec, err := loadSpec()
				if err == nil {
					imageName = spec.Image
					if imageName == "" {
						imageName = fmt.Sprintf("%s:latest", spec.Name)
					}
				}
			}
			if imageName == "" {
				return fmt.Errorf("image name required")
			}

			scanner := security.NewTrivyScanner()
			ctx := context.Background()
			res, err := scanner.ScanImage(ctx, imageName, os.Stdout)
			if err != nil {
				return err
			}

			fmt.Printf("\n--- Security Audit Report ---\nImage:    %s\nScanner:  %s\nCritical: %d\nHigh:     %d\nStatus:   %s\n",
				res.ImageScanned, res.ScannerUsed, res.Critical, res.High, map[bool]string{true: "PASSED", false: "FAILED"}[res.Passed])
			return nil
		},
	}
}

func newUpCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "up",
		Short: "Deploy or run the application on the chosen target (docker/kubernetes)",
		RunE: func(cmd *cobra.Command, args []string) error {
			spec, err := loadSpec()
			if err != nil {
				return err
			}
			engine := getRuntimeAdapter()
			ctx := context.Background()

			if err := engine.Initialize(ctx); err != nil {
				return err
			}

			fmt.Printf("==> Deploying '%s' to target runtime [%s]...\n", spec.Name, engine.Name())
			status, err := engine.Deploy(ctx, spec)
			if err != nil {
				return err
			}

			// Record mutation in operational memory
			if ledger, err := opmem.NewOperationalMemoryLedger(""); err == nil {
				_ = ledger.RecordTelemetryEvent(&opmem.CausalEvent{
					Timestamp:   time.Now(),
					Type:        "MUTATION",
					Source:      "app up",
					Description: fmt.Sprintf("Deployed %s to runtime %s (State: %s)", spec.Name, engine.Name(), status.State),
					Severity:    "INFO",
				})
			}

			fmt.Printf("==> Workload '%s' deployed successfully!\nState: %s (Replicas: %d/%d)\n",
				status.Name, status.State, status.Available, status.Replicas)
			return nil
		},
	}
}

func newStatusCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "status [name]",
		Short: "Inspect live status of running workload",
		RunE: func(cmd *cobra.Command, args []string) error {
			spec, err := loadSpec()
			workloadName := ""
			if err == nil {
				workloadName = spec.Name
			}
			if len(args) > 0 {
				workloadName = args[0]
			}
			if workloadName == "" {
				return fmt.Errorf("workload name required")
			}

			engine := getRuntimeAdapter()
			ctx := context.Background()
			status, err := engine.Status(ctx, workloadName)
			if err != nil {
				return err
			}

			fmt.Printf("Workload: %s\nTarget:   %s\nState:    %s\nReplicas: %d/%d\n",
				status.Name, status.Target, status.State, status.Available, status.Replicas)
			return nil
		},
	}
}

func newLogsCmd() *cobra.Command {
	var follow bool
	var tail int64

	cmd := &cobra.Command{
		Use:   "logs [name]",
		Short: "Stream logs from local container or Kubernetes pod",
		RunE: func(cmd *cobra.Command, args []string) error {
			spec, err := loadSpec()
			workloadName := ""
			if err == nil {
				workloadName = spec.Name
			}
			if len(args) > 0 {
				workloadName = args[0]
			}
			if workloadName == "" {
				return fmt.Errorf("workload name required")
			}

			engine := getRuntimeAdapter()
			ctx := context.Background()
			return engine.Logs(ctx, workloadName, runtime.LogOptions{
				Follow:    follow,
				TailLines: tail,
			}, os.Stdout)
		},
	}
	cmd.Flags().BoolVarP(&follow, "follow", "f", false, "Stream live logs continuously")
	cmd.Flags().Int64VarP(&tail, "tail", "t", 50, "Lines of recent log output to display")
	return cmd
}

func newDiagnoseCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "diagnose [name]",
		Short: "Diagnose workload using 8 deterministic detectors and context-grounded operational memory",
		RunE: func(cmd *cobra.Command, args []string) error {
			spec, err := loadSpec()
			workloadName := ""
			if err == nil {
				workloadName = spec.Name
			}
			if len(args) > 0 {
				workloadName = args[0]
			}
			if workloadName == "" {
				return fmt.Errorf("workload name required")
			}

			ledger, _ := opmem.NewOperationalMemoryLedger("")
			engine := diagnostics.NewContextGroundedEngine(ledger)
			ctx := context.Background()

			fmt.Printf("==> Evaluating deterministic diagnostic matrix & operational context on '%s'...\n", workloadName)
			findings, err := engine.RunDiagnostics(ctx, namespace, workloadName)
			if err != nil {
				return err
			}

			if outputFormat == "json" {
				enc := json.NewEncoder(os.Stdout)
				enc.SetIndent("", "  ")
				return enc.Encode(findings)
			}

			if len(findings) == 0 {
				fmt.Println("[DIAGNOSIS COMPLETE: 0 ANOMALIES DETECTED] Workload appears healthy.")
				return nil
			}

			fmt.Printf("\n=== DIAGNOSTIC REPORT: %d INCIDENT(S) IDENTIFIED ===\n\n", len(findings))
			for i, f := range findings {
				fmt.Printf("[%d] %s: %s (Confidence: %.0f%%)\n", i+1, f.Severity, f.DetectorName, f.Confidence*100)
				fmt.Printf("    Root Cause:    %s\n", f.RootCause)
				fmt.Printf("    Evidence:      %s\n", f.Evidence[0])
				if len(f.Evidence) > 1 {
					for _, ev := range f.Evidence[1:] {
						fmt.Printf("                   - %s\n", ev)
					}
				}
				fmt.Printf("    Suggested Fix: %s\n", f.SuggestedFix)
				if f.RemediationCmd != "" {
					fmt.Printf("    Remediation:   %s\n", f.RemediationCmd)
				}
				fmt.Println()
			}
			return nil
		},
	}
}

func newRightsizingCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "rightsize",
		Short: "Analyze workload CPU and memory usage to reclaim wasted cloud expenditure",
		RunE: func(cmd *cobra.Command, args []string) error {
			spec, err := loadSpec()
			if err != nil {
				return err
			}

			gov := governance.NewGovernanceEngine()
			ctx := context.Background()
			res, err := gov.AnalyzeRightsizing(ctx, spec)
			if err != nil {
				return err
			}

			if outputFormat == "json" {
				enc := json.NewEncoder(os.Stdout)
				enc.SetIndent("", "  ")
				return enc.Encode(res)
			}

			fmt.Printf("=== FINOPS WORKLOAD RIGHTSIZING ANALYSIS ===\n")
			fmt.Printf("Workload:            %s (Namespace: %s)\n", res.WorkloadName, res.Namespace)
			fmt.Printf("Allocated CPU:       %s (Actual P95: %s) -> Recommended: %s\n", res.RequestedCPU, res.ActualP95CPU, res.RecommendedCPU)
			fmt.Printf("Allocated Memory:    %s (Actual P95: %s) -> Recommended: %s\n", res.RequestedMemory, res.ActualP95Memory, res.RecommendedMem)
			fmt.Printf("Est. Monthly Saving: $%.2f USD / replica\n", res.MonthlySavings)
			fmt.Printf("Status:              OVERPROVISIONED (Rightsizing available)\n")
			return nil
		},
	}
}

func newPolicyCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "policy-check",
		Short: "Audit declarative app specification against platform compliance policies",
		RunE: func(cmd *cobra.Command, args []string) error {
			spec, err := loadSpec()
			if err != nil {
				return err
			}

			gov := governance.NewGovernanceEngine()
			violations, err := gov.EvaluatePolicies(spec)
			if err != nil {
				return err
			}

			out := cmd.OutOrStdout()
			if outputFormat == "json" {
				if violations == nil {
					violations = []governance.PolicyViolation{}
				}
				enc := json.NewEncoder(out)
				enc.SetIndent("", "  ")
				return enc.Encode(violations)
			}

			if len(violations) == 0 {
				fmt.Fprintln(out, "[POLICY AUDIT PASSED] AppSpec complies with all enterprise platform rules.")
				return nil
			}

			fmt.Fprintf(out, "=== POLICY AUDIT FINDINGS: %d RULE(S) TRIGGERED ===\n\n", len(violations))
			for i, v := range violations {
				fmt.Fprintf(out, "[%d] [%s] %s: %s\n", i+1, v.Severity, v.RuleName, v.Message)
			}
			return nil
		},
	}
}

func newDebugCmd() *cobra.Command {
	var (
		ephemeral       bool
		debugImage      string
		targetContainer string
		copyPod         bool
		shareProcesses  bool
		dryRun          bool
	)

	cmd := &cobra.Command{
		Use:   "debug [name]",
		Short: "Attach an interactive or ephemeral debugging sandbox with retained context",
		RunE: func(cmd *cobra.Command, args []string) error {
			spec, err := loadSpec()
			workloadName := ""
			if err == nil {
				workloadName = spec.Name
			}
			if len(args) > 0 {
				workloadName = args[0]
			}
			if workloadName == "" {
				return fmt.Errorf("workload name required")
			}

			out := cmd.OutOrStdout()
			ctx := context.Background()

			if ephemeral {
				ledger, _ := opmem.NewOperationalMemoryLedger("")
				launcher := runtime.NewEphemeralSandboxLauncher(ledger)
				res, err := launcher.Launch(ctx, runtime.EphemeralSandboxOptions{
					WorkloadName:    workloadName,
					Namespace:       namespace,
					Runtime:         targetRuntime,
					DebugImage:      debugImage,
					TargetContainer: targetContainer,
					CopyPod:         copyPod,
					ShareProcesses:  shareProcesses,
					DryRun:          dryRun,
					Operator:        os.Getenv("USERNAME"),
				}, os.Stdin, out, cmd.ErrOrStderr())
				if err != nil {
					return err
				}
				if outputFormat == "json" {
					enc := json.NewEncoder(out)
					enc.SetIndent("", "  ")
					return enc.Encode(res)
				}
				mode := "LIVE"
				if res.DryRun {
					mode = "DRY-RUN"
				}
				fmt.Fprintf(out, "==> Ephemeral debug sandbox [%s] (Session: %s)\n", mode, res.SessionID)
				fmt.Fprintf(out, "    Runtime:     %s\n", res.Runtime)
				fmt.Fprintf(out, "    Target:      %s\n", res.Target)
				fmt.Fprintf(out, "    Debug Image: %s\n", res.DebugImage)
				fmt.Fprintf(out, "    Command:     %s\n", strings.Join(res.Command, " "))
				return nil
			}

			engine := getRuntimeAdapter()
			fmt.Fprintf(out, "==> Spawning ephemeral debug session on '%s' via [%s]...\n", workloadName, engine.Name())

			startTime := time.Now()
			code, err := engine.Debug(ctx, runtime.DebugOptions{
				WorkloadName: workloadName,
			}, os.Stdin, out, cmd.ErrOrStderr())

			endTime := time.Now()

			// Retain debug session in operational memory
			if ledger, err := opmem.NewOperationalMemoryLedger(""); err == nil {
				_ = ledger.RecordDebugSession(&opmem.DebugSessionRecord{
					SessionID:         fmt.Sprintf("dbg-%d", startTime.Unix()),
					StartedAt:         startTime,
					EndedAt:           endTime,
					Operator:          os.Getenv("USERNAME"),
					WorkloadName:      workloadName,
					ExitCode:          code,
					ResolutionSummary: fmt.Sprintf("Debug session on %s concluded with exit code %d", workloadName, code),
				})

				_ = ledger.RecordTelemetryEvent(&opmem.CausalEvent{
					Timestamp:   endTime,
					Type:        "SYSTEM_SIGNAL",
					Source:      "app debug",
					Description: fmt.Sprintf("Debug session ended on %s (Duration: %v, ExitCode: %d)", workloadName, endTime.Sub(startTime).Round(time.Second), code),
					Severity:    "INFO",
				})
			}

			fmt.Fprintf(out, "\n[Debug session preserved in Operational Memory with exit code %d]\n", code)
			return err
		},
	}

	cmd.Flags().BoolVar(&ephemeral, "ephemeral", false, "Launch an isolated ephemeral debug sandbox container")
	cmd.Flags().StringVar(&debugImage, "debug-image", runtime.DefaultDebugImage, "Container image for ephemeral debug sandbox")
	cmd.Flags().StringVar(&targetContainer, "target-container", "", "Target container name inside pod for process namespace sharing")
	cmd.Flags().BoolVar(&copyPod, "copy-pod", false, "Debug a copy of the target pod (Kubernetes only)")
	cmd.Flags().BoolVar(&shareProcesses, "share-processes", true, "Share process namespace with target container")
	cmd.Flags().BoolVar(&dryRun, "dry-run", false, "Print resolved sandbox command without executing")
	return cmd
}

func newTimeTravelCmd() *cobra.Command {
	var windowStr string

	cmd := &cobra.Command{
		Use:   "time-travel [name]",
		Short: "Replay chronological operational events, mutations, and debug logs leading to an incident",
		RunE: func(cmd *cobra.Command, args []string) error {
			spec, err := loadSpec()
			workloadName := ""
			if err == nil {
				workloadName = spec.Name
			}
			if len(args) > 0 {
				workloadName = args[0]
			}

			window, err := time.ParseDuration(windowStr)
			if err != nil {
				window = 30 * time.Minute
			}

			ledger, err := opmem.NewOperationalMemoryLedger("")
			if err != nil {
				return err
			}

			events, err := ledger.QueryTimeline(workloadName, window)
			if err != nil {
				return err
			}

			fmt.Printf("=== OPERATIONAL MEMORY TIME-TRAVEL TIMELINE (Past %v) ===\n", window)
			if len(events) == 0 {
				fmt.Println("No recorded events or mutations found in this time window.")
				return nil
			}

			for _, ev := range events {
				timeFmt := ev.Timestamp.Format("15:04:05")
				fmt.Printf("[%s] [%-14s] (%-10s) %s\n", timeFmt, ev.Type, ev.Source, ev.Description)
			}
			return nil
		},
	}
	cmd.Flags().StringVarP(&windowStr, "window", "w", "30m", "Time travel retrospective window (e.g. 15m, 1h, 24h)")
	return cmd
}

func newDownCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "down [name]",
		Short: "Stop running workload on target runtime",
		RunE: func(cmd *cobra.Command, args []string) error {
			spec, err := loadSpec()
			workloadName := ""
			if err == nil {
				workloadName = spec.Name
			}
			if len(args) > 0 {
				workloadName = args[0]
			}
			if workloadName == "" {
				return fmt.Errorf("workload name required")
			}

			engine := getRuntimeAdapter()
			ctx := context.Background()
			if err := engine.Stop(ctx, workloadName); err != nil {
				return err
			}
			fmt.Printf("Workload '%s' stopped on target [%s].\n", workloadName, engine.Name())
			return nil
		},
	}
}

func newLocalCmd() *cobra.Command {
	clusterName := "app-dev"
	localCmd := &cobra.Command{
		Use:   "local",
		Short: "Manage local zero-friction Kubernetes clusters (Kind)",
	}

	initCmd := &cobra.Command{
		Use:   "init",
		Short: "Initialize an ingress-ready local Kubernetes cluster",
		RunE: func(cmd *cobra.Command, args []string) error {
			km := local.NewKindManager()
			ctx := context.Background()
			fmt.Printf("==> Checking local cluster status for '%s'...\n", clusterName)
			exists, err := km.Exists(ctx, clusterName)
			if err != nil {
				return err
			}
			if exists {
				fmt.Printf("Local cluster '%s' is already running.\n", clusterName)
				return nil
			}
			fmt.Printf("==> Bootstrapping local Kubernetes cluster '%s' via Kind...\n", clusterName)
			if err := km.Create(ctx, clusterName); err != nil {
				return err
			}
			fmt.Printf("==> Local cluster '%s' initialized and ready for development!\n", clusterName)
			return nil
		},
	}

	destroyCmd := &cobra.Command{
		Use:   "destroy",
		Short: "Tear down the local Kubernetes cluster",
		RunE: func(cmd *cobra.Command, args []string) error {
			km := local.NewKindManager()
			ctx := context.Background()
			fmt.Printf("==> Tearing down local cluster '%s'...\n", clusterName)
			if err := km.Delete(ctx, clusterName); err != nil {
				return err
			}
			fmt.Printf("==> Local cluster '%s' destroyed.\n", clusterName)
			return nil
		},
	}

	statusCmd := &cobra.Command{
		Use:   "status",
		Short: "Check whether the local cluster is running",
		RunE: func(cmd *cobra.Command, args []string) error {
			km := local.NewKindManager()
			ctx := context.Background()
			exists, err := km.Exists(ctx, clusterName)
			if err != nil {
				return err
			}
			if exists {
				fmt.Printf("Local cluster '%s': ACTIVE\n", clusterName)
			} else {
				fmt.Printf("Local cluster '%s': NOT FOUND\n", clusterName)
			}
			return nil
		},
	}

	localCmd.PersistentFlags().StringVar(&clusterName, "name", "skipper-dev", "Cluster name")
	localCmd.AddCommand(initCmd)
	localCmd.AddCommand(destroyCmd)
	localCmd.AddCommand(statusCmd)
	return localCmd
}

func renderDoctorReport(out io.Writer, report *doctor.DoctorReport, format string) error {
	if format == "json" {
		enc := json.NewEncoder(out)
		enc.SetIndent("", "  ")
		if err := enc.Encode(report); err != nil {
			return err
		}
		if !report.Passed {
			return fmt.Errorf("doctor audit failed; please resolve blocking issues above")
		}
		return nil
	}

	fmt.Fprintln(out, "==> Running Skipper environment health audit...")

	for _, check := range report.Checks {
		symbol := "[✓]"
		if check.Status == doctor.StatusWarn {
			symbol = "[!]"
		} else if check.Status == doctor.StatusFail {
			symbol = "[✗]"
		}
		fmt.Fprintf(out, "  %s %-30s : %s\n", symbol, check.Name, check.Message)
		if check.Remediation != "" {
			fmt.Fprintf(out, "      -> Fix: %s\n", check.Remediation)
		}
	}

	if !report.Passed {
		return fmt.Errorf("doctor audit failed; please resolve blocking issues above")
	}
	fmt.Fprintln(out, "==> All critical prerequisites satisfied!")
	return nil
}

var runDoctor = func(ctx context.Context) *doctor.DoctorReport {
	engine := doctor.NewDoctorEngine()
	return engine.RunDiagnostics(ctx)
}

func newDoctorCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "doctor",
		Short: "Check local environment prerequisites and port collisions",
		RunE: func(cmd *cobra.Command, args []string) error {
			report := runDoctor(context.Background())
			return renderDoctorReport(cmd.OutOrStdout(), report, outputFormat)
		},
	}
}

func newInitCmd() *cobra.Command {
	var initName, initTeam string
	var initPort int

	initCmd := &cobra.Command{
		Use:   "init",
		Short: "Interactively bootstrap a new skipper.yaml for the current project",
		RunE: func(cmd *cobra.Command, args []string) error {
			initr := bootstrapper.NewProjectInitializer()
			spec, err := initr.InspectAndGenerate(context.Background(), bootstrapper.InitOptions{
				Name: initName,
				Team: initTeam,
				Port: initPort,
			})
			if err != nil {
				return err
			}

			targetFile := "skipper.yaml"
			if err := initr.WriteSpecFile(spec, targetFile); err != nil {
				return err
			}
			fmt.Printf("==> Successfully generated '%s' for [%s] (Port: %d, Team: %s)!\n",
				targetFile, spec.Name, spec.Endpoints[0].Port, spec.Team)
			fmt.Println("    Run 'skipper up' to test locally, or 'skipper manifest' to review Kubernetes YAML.")
			return nil
		},
	}

	initCmd.Flags().StringVar(&initName, "name", "", "Workload name (default: current directory name)")
	initCmd.Flags().StringVarP(&initTeam, "team", "t", "platform", "Owning team for governance attribution")
	initCmd.Flags().IntVarP(&initPort, "port", "p", 0, "Primary service port (auto-detected if omitted)")
	return initCmd
}

func newDevCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "dev",
		Short: "Start active live development loop with automatic reload on change",
		RunE: func(cmd *cobra.Command, args []string) error {
			spec, err := loadSpec()
			if err != nil {
				return err
			}

			adapter := getRuntimeAdapter()
			builder := build.NewCNBBuilder()

			server := dev.NewDevServer(dev.DevOptions{
				WatchDir:   ".",
				Spec:       spec,
				RuntimeEng: adapter,
				Builder:    builder,
			})

			ctx := context.Background()
			return server.Start(ctx, os.Stdout)
		},
	}
}

func newPortForwardCmd() *cobra.Command {
	pfCmd := &cobra.Command{
		Use:   "port-forward [workload] [localPort:remotePort]",
		Short: "Forward local port to a remote Kubernetes deployment",
		Args:  cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			workload := args[0]
			localPort, remotePort, err := tunnel.ParsePortString(args[1])
			if err != nil {
				return err
			}

			forwarder := tunnel.NewPortForwarder()
			return forwarder.Forward(context.Background(), tunnel.PortForwardOptions{
				Namespace:    namespace,
				WorkloadName: workload,
				LocalPort:    localPort,
				RemotePort:   remotePort,
			}, os.Stdout, os.Stderr)
		},
	}

	return pfCmd
}

func newSecretCmd() *cobra.Command {
	secretCmd := &cobra.Command{
		Use:   "secret",
		Short: "Manage local and cluster secrets seamlessly",
	}

	setCmd := &cobra.Command{
		Use:   "set [KEY=VALUE]",
		Short: "Store an encrypted/managed secret key-value pair",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			parts := strings.SplitN(args[0], "=", 2)
			if len(parts) != 2 {
				return fmt.Errorf("expected format KEY=VALUE")
			}
			store, err := secret.NewSecretStore("")
			if err != nil {
				return err
			}
			if err := store.Set(parts[0], parts[1]); err != nil {
				return err
			}
			fmt.Fprintf(cmd.OutOrStdout(), "==> Secret '%s' saved successfully.\n", parts[0])
			return nil
		},
	}

	getCmd := &cobra.Command{
		Use:   "get [KEY]",
		Short: "Retrieve a secret value",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			store, err := secret.NewSecretStore("")
			if err != nil {
				return err
			}
			val, err := store.Get(args[0])
			if err != nil {
				return err
			}
			fmt.Fprintf(cmd.OutOrStdout(), "%s=%s\n", args[0], val)
			return nil
		},
	}

	exportCmd := &cobra.Command{
		Use:   "export",
		Short: "Export all managed secrets as a Kubernetes v1 Secret manifest",
		RunE: func(cmd *cobra.Command, args []string) error {
			store, err := secret.NewSecretStore("")
			if err != nil {
				return err
			}
			manifest, err := store.ExportKubernetesManifest("skipper-secrets", namespace)
			if err != nil {
				return err
			}
			fmt.Fprintln(cmd.OutOrStdout(), manifest)
			return nil
		},
	}

	var (
		syncProvider       string
		syncVaultAddr      string
		syncVaultToken     string
		syncVaultMount     string
		syncVaultPath      string
		syncExternalSecret bool
		syncDirection      string
		syncName           string
		syncKeys           []string
		syncAWSRegion      string
		syncGCPProject     string
	)

	syncCmd := &cobra.Command{
		Use:   "sync",
		Short: "Sync secrets with HashiCorp Vault or generate ExternalSecrets manifests (Vault/AWS/GCP)",
		RunE: func(cmd *cobra.Command, args []string) error {
			out := cmd.OutOrStdout()
			dir := strings.ToLower(strings.TrimSpace(syncDirection))
			if syncExternalSecret {
				dir = "manifest"
			}
			if dir == "" {
				dir = "manifest"
			}

			switch dir {
			case "manifest":
				keys := syncKeys
				if len(keys) == 0 {
					if store, err := secret.NewSecretStore(""); err == nil {
						if existing, err := store.List(); err == nil {
							for k := range existing {
								keys = append(keys, k)
							}
						}
					}
				}
				if len(keys) == 0 {
					keys = []string{"APP_SECRET"}
				}

				opts := secret.ExternalSecretOptions{
					Name:         syncName,
					Namespace:    namespace,
					Provider:     secret.ExternalSecretProvider(strings.ToLower(syncProvider)),
					RemotePath:   syncVaultPath,
					VaultAddr:    syncVaultAddr,
					VaultMount:   syncVaultMount,
					AWSRegion:    syncAWSRegion,
					GCPProjectID: syncGCPProject,
				}
				manifest, err := secret.GenerateExternalSecretManifest(opts, keys)
				if err != nil {
					return err
				}
				if outputFormat == "json" {
					enc := json.NewEncoder(out)
					enc.SetIndent("", "  ")
					return enc.Encode(map[string]interface{}{
						"provider":  opts.Provider,
						"namespace": opts.Namespace,
						"name":      opts.Name,
						"keys":      keys,
						"manifest":  manifest,
					})
				}
				fmt.Fprintln(out, manifest)
				return nil

			case "push":
				store, err := secret.NewSecretStore("")
				if err != nil {
					return err
				}
				secrets, err := store.List()
				if err != nil {
					return err
				}
				client, err := secret.NewVaultSyncClient(syncVaultAddr, syncVaultToken)
				if err != nil {
					return err
				}
				if err := client.Push(context.Background(), syncVaultMount, syncVaultPath, secrets); err != nil {
					return err
				}
				if outputFormat == "json" {
					enc := json.NewEncoder(out)
					enc.SetIndent("", "  ")
					return enc.Encode(map[string]interface{}{
						"direction": "push",
						"vaultAddr": syncVaultAddr,
						"path":      syncVaultPath,
						"count":     len(secrets),
						"status":    "SYNCED",
					})
				}
				fmt.Fprintf(out, "==> Pushed %d secret(s) to Vault (%s/%s).\n", len(secrets), syncVaultMount, syncVaultPath)
				return nil

			case "pull":
				client, err := secret.NewVaultSyncClient(syncVaultAddr, syncVaultToken)
				if err != nil {
					return err
				}
				pulled, err := client.Pull(context.Background(), syncVaultMount, syncVaultPath)
				if err != nil {
					return err
				}
				store, err := secret.NewSecretStore("")
				if err != nil {
					return err
				}
				for k, v := range pulled {
					if err := store.Set(k, v); err != nil {
						return err
					}
				}
				if outputFormat == "json" {
					enc := json.NewEncoder(out)
					enc.SetIndent("", "  ")
					return enc.Encode(map[string]interface{}{
						"direction": "pull",
						"vaultAddr": syncVaultAddr,
						"path":      syncVaultPath,
						"count":     len(pulled),
						"status":    "SYNCED",
					})
				}
				fmt.Fprintf(out, "==> Pulled %d secret(s) from Vault (%s/%s).\n", len(pulled), syncVaultMount, syncVaultPath)
				return nil

			default:
				return fmt.Errorf("unsupported sync direction %q (expected push, pull, or manifest)", syncDirection)
			}
		},
	}

	syncCmd.Flags().StringVar(&syncProvider, "provider", "vault", "Secret backend provider: vault, aws, or gcp")
	syncCmd.Flags().StringVar(&syncVaultAddr, "vault-addr", os.Getenv("VAULT_ADDR"), "HashiCorp Vault server URL")
	syncCmd.Flags().StringVar(&syncVaultToken, "vault-token", os.Getenv("VAULT_TOKEN"), "HashiCorp Vault auth token")
	syncCmd.Flags().StringVar(&syncVaultMount, "vault-mount", "secret", "Vault KV v2 mount path")
	syncCmd.Flags().StringVar(&syncVaultPath, "vault-path", "skipper/app", "Remote secret path in Vault/AWS/GCP")
	syncCmd.Flags().BoolVar(&syncExternalSecret, "external-secret", false, "Output ExternalSecrets Operator SecretStore + ExternalSecret YAML")
	syncCmd.Flags().StringVar(&syncDirection, "direction", "manifest", "Sync mode: push, pull, or manifest")
	syncCmd.Flags().StringVar(&syncName, "name", "skipper-secrets", "ExternalSecret resource name")
	syncCmd.Flags().StringSliceVar(&syncKeys, "keys", nil, "Secret keys to include in ExternalSecret manifest")
	syncCmd.Flags().StringVar(&syncAWSRegion, "aws-region", "us-east-1", "AWS Secrets Manager region (for --provider aws)")
	syncCmd.Flags().StringVar(&syncGCPProject, "gcp-project", "", "GCP Project ID (for --provider gcp)")

	secretCmd.AddCommand(setCmd)
	secretCmd.AddCommand(getCmd)
	secretCmd.AddCommand(exportCmd)
	secretCmd.AddCommand(syncCmd)
	return secretCmd
}

func newImportCmd() *cobra.Command {
	importCmd := &cobra.Command{
		Use:   "import",
		Short: "Import existing configuration formats into skipper.yaml",
	}

	composeCmd := &cobra.Command{
		Use:   "compose [path-to-docker-compose.yaml]",
		Short: "Transpile a docker-compose.yaml file into skipper.yaml",
		Args:  cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			file := "docker-compose.yaml"
			if len(args) > 0 {
				file = args[0]
			}
			if _, err := os.Stat(file); os.IsNotExist(err) {
				file = "compose.yaml"
			}

			data, err := os.ReadFile(file)
			if err != nil {
				return fmt.Errorf("unable to read compose file: %w", err)
			}

			transpiler := transpile.NewTranspiler()
			spec, err := transpiler.TranspileCompose(data, "")
			if err != nil {
				return fmt.Errorf("transpilation failed: %w", err)
			}

			outData, err := yaml.Marshal(spec)
			if err != nil {
				return err
			}

			targetFile := "skipper.yaml"
			if err := os.WriteFile(targetFile, outData, 0644); err != nil {
				return err
			}
			fmt.Fprintf(cmd.OutOrStdout(), "==> Successfully transpiled '%s' into '%s' (Service: %s)!\n", file, targetFile, spec.Name)
			return nil
		},
	}

	importCmd.AddCommand(composeCmd)
	return importCmd
}

func newMCPCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "mcp",
		Short: "Start Skipper Model Context Protocol (MCP) server over stdio for AI agents",
		RunE: func(cmd *cobra.Command, args []string) error {
			srv := mcp.NewMCPServer()
			return srv.Start(context.Background())
		},
	}
}

type explainIntentModel struct {
	Description string   `json:"description"`
	Version     string   `json:"version"`
	Name        string   `json:"name"`
	Types       []string `json:"types"`
	Endpoints   string   `json:"endpoints"`
	Resources   string   `json:"resources"`
	Security    string   `json:"security"`
}

type explainPlaybookEntry struct {
	Task    string `json:"task"`
	Command string `json:"command"`
}

type explainSpec struct {
	Title           string                 `json:"title"`
	Version         string                 `json:"version"`
	IntentModel     explainIntentModel     `json:"intentModel"`
	Playbook        []explainPlaybookEntry `json:"playbook"`
	MCPTools        []string               `json:"mcpTools"`
	RemediationLoop []string               `json:"remediationLoop"`
}

func newExplainCmd() *cobra.Command {
	var showRawSchema bool
	cmd := &cobra.Command{
		Use:   "explain",
		Short: "Output declarative schema and instruction grammar for AI agent consumption",
		RunE: func(cmd *cobra.Command, args []string) error {
			out := cmd.OutOrStdout()
			if showRawSchema {
				fmt.Fprintln(out, strings.TrimSpace(string(schemas.SkipperSchemaJSON)))
				return nil
			}

			if outputFormat == "json" {
				payload := explainSpec{
					Title:   "Skipper AI Agent Operational Specification",
					Version: version,
					IntentModel: explainIntentModel{
						Description: "Skipper translates a declarative 'skipper.yaml' into standard OCI containers and hardened Kubernetes manifests.",
						Version:     "v1alpha1",
						Name:        "string (alphanumeric lowercase, dashes)",
						Types:       []string{"service", "worker", "job"},
						Endpoints:   "list of {name, port, protocol, public}",
						Resources:   "{cpu: {request, limit}, memory: {request, limit}}",
						Security:    "{runAsNonRoot: true, readOnlyRootFilesystem: bool}",
					},
					Playbook: []explainPlaybookEntry{
						{Task: "Check Workstation Status", Command: "skipper doctor -f json"},
						{Task: "Audit Policies Before Deploy", Command: "skipper policy-check -f json"},
						{Task: "Compile Kubernetes Manifests", Command: "skipper manifest"},
						{Task: "Diagnose Failing Workloads", Command: "skipper diagnose <name> -f json"},
						{Task: "Reclaim Cloud Cost / Rightsize", Command: "skipper rightsize -f json"},
						{Task: "Manage Encrypted Credentials", Command: "skipper secret set KEY=VAL / skipper secret export"},
						{Task: "Sync Secrets (Vault / ExternalSecrets)", Command: "skipper secret sync --provider vault --external-secret / --direction push|pull"},
						{Task: "Migrate Docker Compose", Command: "skipper import compose [path]"},
						{Task: "Generate SBOM (CycloneDX / SPDX)", Command: "skipper sbom --target . --sbom-format cyclonedx -f json"},
						{Task: "Sign or Verify OCI Images (Cosign)", Command: "skipper sign --image <ref> [--verify] [--dry-run] -f json"},
						{Task: "Launch Ephemeral Debug Sandbox", Command: "skipper debug <workload> --ephemeral [--copy-pod] [--share-processes] [--dry-run]"},
						{Task: "Observe SLO Health Gates & OTel Manifests", Command: "skipper observe [workload] --prometheus-url <url> / --otel-manifest -f json"},
						{Task: "Export GitOps Bundle (ArgoCD / FluxCD)", Command: "skipper gitops export --engine argocd|flux --repo <url> --env prod -o <dir>"},
						{Task: "Manage Fleet Contexts & Detect Drift", Command: "skipper context [list|current|use <name>|drift] -f json"},
					},
					MCPTools: []string{
						"skipper_doctor",
						"skipper_init",
						"skipper_policy_check",
						"skipper_manifest",
						"skipper_diagnose",
						"skipper_rightsize",
						"skipper_rollback",
						"skipper_investigate_incident",
						"skipper_sbom",
						"skipper_drift_check",
					},
					RemediationLoop: []string{
						"Call 'skipper diagnose <workload> -f json' or MCP 'skipper_investigate_incident'",
						"Parse 'detectorName', 'rootCause', and 'remediationCmd'",
						"Execute 'remediationCmd', attach 'skipper debug <workload> --ephemeral', or trigger MCP 'skipper_rollback'",
						"Verify drift via 'skipper context drift -f json' and SLOs via 'skipper observe <workload> -f json'",
						"Re-run 'skipper deploy' or 'skipper gitops export'",
					},
				}
				enc := json.NewEncoder(out)
				enc.SetEscapeHTML(false)
				enc.SetIndent("", "  ")
				return enc.Encode(payload)
			}

			// Clean, high-density AI instruction grammar
			grammar := `# Skipper AI Agent Operational Specification

## 1. Intent Model (skipper.yaml)
Skipper translates a declarative 'skipper.yaml' into standard OCI containers and hardened Kubernetes manifests.
- Version: v1alpha1
- Name: string (alphanumeric lowercase, dashes)
- Type: 'service' | 'worker' | 'job'
- Endpoints: list of {name, port, protocol, public}
- Runtime.Resources: {cpu: {request, limit}, memory: {request, limit}}
- Security: {runAsNonRoot: true, readOnlyRootFilesystem: bool}

## 2. Command Execution Playbook for AI Agents
- Check Workstation Status: run 'skipper doctor -f json'
- Audit Policies Before Deploy: run 'skipper policy-check -f json'
- Compile Kubernetes Manifests: run 'skipper manifest'
- Diagnose Failing Workloads: run 'skipper diagnose <name> -f json'
- Reclaim Cloud Cost / Rightsize: run 'skipper rightsize -f json'
- Manage Encrypted Credentials: run 'skipper secret set KEY=VAL' / 'skipper secret export'
- Sync Secrets (Vault / ExternalSecrets): run 'skipper secret sync --provider vault --external-secret' or '--direction push|pull'
- Migrate Docker Compose: run 'skipper import compose [path]'
- Generate SBOM (CycloneDX / SPDX): run 'skipper sbom --target . --sbom-format cyclonedx -f json'
- Sign or Verify OCI Images (Cosign): run 'skipper sign --image <ref> [--verify] [--dry-run] -f json'
- Launch Ephemeral Debug Sandbox: run 'skipper debug <workload> --ephemeral [--copy-pod] [--share-processes] [--dry-run]'
- Observe SLO Health Gates & OTel Manifests: run 'skipper observe [workload] --prometheus-url <url>' or 'skipper observe --otel-manifest'
- Export GitOps Bundle (ArgoCD / FluxCD): run 'skipper gitops export --engine argocd|flux --repo <url> --env prod -o <dir>'
- Manage Fleet Contexts & Detect Drift: run 'skipper context [list|current|use <name>|drift] -f json'

## 3. Model Context Protocol (MCP) Tools ('skipper mcp')
10 native JSON-RPC tools exposed over stdio:
- skipper_doctor
- skipper_init
- skipper_policy_check
- skipper_manifest
- skipper_diagnose
- skipper_rightsize
- skipper_rollback
- skipper_investigate_incident
- skipper_sbom
- skipper_drift_check

## 4. Self-Healing & Remediation Loop
When diagnosing a failing workload:
1. Call 'skipper diagnose <workload> -f json' or MCP 'skipper_investigate_incident'
2. Parse 'detectorName', 'rootCause', and 'remediationCmd'
3. Execute 'remediationCmd', attach 'skipper debug <workload> --ephemeral', or trigger MCP 'skipper_rollback'
4. Verify drift via 'skipper context drift -f json' and SLOs via 'skipper observe <workload> -f json'
5. Re-run 'skipper deploy' or 'skipper gitops export'
`
			fmt.Fprint(out, grammar)
			return nil
		},
	}

	cmd.Flags().BoolVar(&showRawSchema, "schema", false, "Output formal JSON Schema Draft 2020-12")
	return cmd
}

func newSBOMCmd() *cobra.Command {
	var (
		target     string
		sbomFormat string
		outputPath string
	)

	cmd := &cobra.Command{
		Use:   "sbom",
		Short: "Generate a CycloneDX 1.5 or SPDX 2.3 Software Bill of Materials (SBOM)",
		RunE: func(cmd *cobra.Command, args []string) error {
			if len(args) > 0 && target == "." {
				target = args[0]
			}
			parsedFormat, err := security.ParseSBOMFormat(sbomFormat)
			if err != nil {
				return err
			}

			gen := security.NewSBOMGenerator()
			report, err := gen.Generate(context.Background(), security.SBOMOptions{
				Target:     target,
				Format:     parsedFormat,
				OutputPath: outputPath,
			})
			if err != nil {
				return err
			}

			out := cmd.OutOrStdout()
			if outputFormat == "json" {
				enc := json.NewEncoder(out)
				enc.SetIndent("", "  ")
				return enc.Encode(map[string]interface{}{
					"format":         report.Format,
					"target":         report.Target,
					"tool":           report.Tool,
					"componentCount": report.ComponentCount,
					"outputPath":     report.OutputPath,
					"document":       json.RawMessage(report.Document),
				})
			}

			if outputPath != "" {
				fmt.Fprintf(out, "==> Generated %s SBOM (%d components, tool: %s) -> %s\n",
					report.Format, report.ComponentCount, report.Tool, report.OutputPath)
				return nil
			}
			fmt.Fprintln(out, string(report.Document))
			return nil
		},
	}

	cmd.Flags().StringVar(&target, "target", ".", "Target directory or OCI image reference")
	cmd.Flags().StringVar(&sbomFormat, "sbom-format", "cyclonedx", "SBOM format: cyclonedx or spdx")
	cmd.Flags().StringVarP(&outputPath, "output", "o", "", "Write SBOM JSON to file path")
	return cmd
}

func newSignCmd() *cobra.Command {
	var (
		image      string
		keyRef     string
		verify     bool
		dryRun     bool
		certID     string
		certIssuer string
	)

	cmd := &cobra.Command{
		Use:   "sign [image]",
		Short: "Sign or verify OCI container images using Cosign (keyed or keyless OIDC)",
		RunE: func(cmd *cobra.Command, args []string) error {
			img := strings.TrimSpace(image)
			if img == "" && len(args) > 0 {
				img = strings.TrimSpace(args[0])
			}
			if img == "" {
				if spec, err := loadSpec(); err == nil {
					img = spec.Image
					if img == "" && spec.Name != "" {
						img = fmt.Sprintf("%s:latest", spec.Name)
					}
				}
			}
			if img == "" {
				return fmt.Errorf("image reference required (pass --image or positional argument)")
			}

			out := cmd.OutOrStdout()
			signer := security.NewImageSigner()
			ctx := context.Background()

			if verify {
				res, err := signer.Verify(ctx, security.VerifyOptions{
					Image:               img,
					KeyRef:              keyRef,
					CertificateIdentity: certID,
					CertificateIssuer:   certIssuer,
					DryRun:              dryRun,
				})
				if err != nil {
					return err
				}
				if outputFormat == "json" {
					enc := json.NewEncoder(out)
					enc.SetIndent("", "  ")
					return enc.Encode(res)
				}
				fmt.Fprintf(out, "==> Cosign verify [%s] (dry-run=%v, verified=%v)\n    Command: %s\n",
					res.Image, res.DryRun, res.Verified, strings.Join(res.Command, " "))
				return nil
			}

			res, err := signer.Sign(ctx, security.SignOptions{
				Image:  img,
				KeyRef: keyRef,
				DryRun: dryRun,
				Yes:    true,
			})
			if err != nil {
				return err
			}
			if outputFormat == "json" {
				enc := json.NewEncoder(out)
				enc.SetIndent("", "  ")
				return enc.Encode(res)
			}
			fmt.Fprintf(out, "==> Cosign sign [%s] (keyless=%v, dry-run=%v, signed=%v)\n    Command: %s\n",
				res.Image, res.Keyless, res.DryRun, res.Signed, strings.Join(res.Command, " "))
			return nil
		},
	}

	cmd.Flags().StringVar(&image, "image", "", "OCI image reference to sign or verify")
	cmd.Flags().StringVar(&keyRef, "key", "", "Path or KMS URI to signing/verification key (keyless OIDC if omitted)")
	cmd.Flags().BoolVar(&verify, "verify", false, "Verify an existing image signature instead of signing")
	cmd.Flags().BoolVar(&dryRun, "dry-run", false, "Print resolved Cosign command without executing")
	cmd.Flags().StringVar(&certID, "certificate-identity", "", "Expected OIDC certificate identity (for keyless verify)")
	cmd.Flags().StringVar(&certIssuer, "certificate-issuer", "", "Expected OIDC certificate issuer (for keyless verify)")
	return cmd
}

func newObserveCmd() *cobra.Command {
	var (
		promURL      string
		maxErrorRate float64
		maxP95Ms     float64
		maxSat       float64
		otelManifest bool
		otlpEndpoint string
	)

	cmd := &cobra.Command{
		Use:   "observe [workload]",
		Short: "Evaluate Prometheus/OTel canary health gates or synthesize OpenTelemetry manifests",
		RunE: func(cmd *cobra.Command, args []string) error {
			out := cmd.OutOrStdout()

			if otelManifest {
				spec, err := loadSpec()
				if err != nil {
					return err
				}
				manifest, err := synthesizer.SynthesizeOTelConfig(spec, synthesizer.OTelOptions{
					Namespace:    namespace,
					OTLPEndpoint: otlpEndpoint,
				})
				if err != nil {
					return err
				}
				if outputFormat == "json" {
					enc := json.NewEncoder(out)
					enc.SetIndent("", "  ")
					return enc.Encode(map[string]interface{}{
						"workload":  spec.Name,
						"namespace": namespace,
						"manifest":  manifest,
					})
				}
				fmt.Fprintln(out, manifest)
				return nil
			}

			workloadName := ""
			if len(args) > 0 {
				workloadName = args[0]
			} else if spec, err := loadSpec(); err == nil {
				workloadName = spec.Name
			}
			if workloadName == "" {
				return fmt.Errorf("workload name required (pass positional arg or provide skipper.yaml)")
			}

			gate := rollout.NewPrometheusHealthGate(promURL, rollout.GateThresholds{
				MaxErrorRate:    maxErrorRate,
				MaxP95LatencyMs: maxP95Ms,
				MaxSaturation:   maxSat,
			})
			report, err := gate.Evaluate(context.Background(), workloadName)
			if err != nil {
				return err
			}

			if outputFormat == "json" {
				enc := json.NewEncoder(out)
				enc.SetIndent("", "  ")
				if encErr := enc.Encode(report); encErr != nil {
					return encErr
				}
				if !report.Healthy {
					return fmt.Errorf("health gate failed: %s", strings.Join(report.Violations, "; "))
				}
				return nil
			}

			fmt.Fprintf(out, "=== OBSERVABILITY HEALTH GATE: %s (%s) ===\n", report.Workload, report.Endpoint)
			for _, m := range report.Metrics {
				mark := "[✓]"
				if !m.Healthy {
					mark = "[✗]"
				}
				fmt.Fprintf(out, "  %s %-18s : %.4f %s (threshold: %.4f)\n", mark, m.Name, m.Value, m.Unit, m.Threshold)
			}
			if !report.Healthy {
				return fmt.Errorf("health gate failed: %s", strings.Join(report.Violations, "; "))
			}
			fmt.Fprintln(out, "==> All SLO health gates passed!")
			return nil
		},
	}

	cmd.Flags().StringVar(&promURL, "prometheus-url", "http://localhost:9090", "Prometheus/OTel HTTP query endpoint")
	cmd.Flags().Float64Var(&maxErrorRate, "max-error-rate", 0.01, "Maximum allowed 5xx error ratio (e.g. 0.01 = 1%)")
	cmd.Flags().Float64Var(&maxP95Ms, "max-p95-ms", 500.0, "Maximum allowed P95 latency in milliseconds")
	cmd.Flags().Float64Var(&maxSat, "max-saturation", 0.85, "Maximum allowed CPU/memory saturation ratio")
	cmd.Flags().BoolVar(&otelManifest, "otel-manifest", false, "Synthesize OpenTelemetry env/annotations and Prometheus ServiceMonitor YAML")
	cmd.Flags().StringVar(&otlpEndpoint, "otlp-endpoint", "http://otel-collector:4317", "OTLP exporter endpoint for --otel-manifest")
	return cmd
}

func newGitOpsCmd() *cobra.Command {
	gitopsCmd := &cobra.Command{
		Use:   "gitops",
		Short: "Export declarative GitOps bundles for ArgoCD and FluxCD",
	}

	var (
		engine     string
		repoURL    string
		branch     string
		envName    string
		targetPath string
		outputDir  string
	)

	exportCmd := &cobra.Command{
		Use:   "export",
		Short: "Generate Kustomize base/overlay manifests and ArgoCD/FluxCD sync definitions",
		RunE: func(cmd *cobra.Command, args []string) error {
			spec, err := loadSpec()
			if err != nil {
				return err
			}

			res, err := gitops.Export(spec, gitops.ExportOptions{
				Engine:      gitops.Engine(strings.ToLower(engine)),
				RepoURL:     repoURL,
				Branch:      branch,
				TargetPath:  targetPath,
				Namespace:   namespace,
				Environment: envName,
				OutputDir:   outputDir,
			})
			if err != nil {
				return err
			}

			out := cmd.OutOrStdout()
			if outputFormat == "json" {
				enc := json.NewEncoder(out)
				enc.SetIndent("", "  ")
				return enc.Encode(res)
			}

			fmt.Fprintf(out, "==> Exported %s GitOps bundle for '%s' (env: %s, %d files)\n",
				res.Engine, res.AppName, res.Environment, len(res.Files))
			for _, f := range res.Files {
				fmt.Fprintf(out, "  - %s\n", f.Path)
			}
			if res.SyncPlan != "" {
				fmt.Fprintf(out, "\n%s\n", res.SyncPlan)
			}
			return nil
		},
	}

	exportCmd.Flags().StringVar(&engine, "engine", string(gitops.EngineArgoCD), "GitOps engine: argocd or flux")
	exportCmd.Flags().StringVar(&repoURL, "repo", "https://github.com/example/gitops-repo.git", "Target GitOps repository URL")
	exportCmd.Flags().StringVar(&branch, "branch", "main", "Target Git branch")
	exportCmd.Flags().StringVar(&envName, "env", "prod", "Target environment overlay (e.g. staging, prod)")
	exportCmd.Flags().StringVar(&targetPath, "target-path", "", "Optional path inside the Git repository")
	exportCmd.Flags().StringVarP(&outputDir, "output", "o", "", "Output directory to write generated GitOps files")

	gitopsCmd.AddCommand(exportCmd)
	return gitopsCmd
}

func newContextCmd() *cobra.Command {
	contextCmd := &cobra.Command{
		Use:   "context",
		Short: "Manage multi-cluster Kubernetes fleet contexts and detect configuration drift",
		RunE: func(cmd *cobra.Command, args []string) error {
			cm := cluster.NewContextManager()
			summary, err := cm.FleetStatus(context.Background())
			if err != nil {
				return err
			}
			out := cmd.OutOrStdout()
			if outputFormat == "json" {
				enc := json.NewEncoder(out)
				enc.SetIndent("", "  ")
				return enc.Encode(summary)
			}
			fmt.Fprintf(out, "Current Context: %s (Clusters: %d, Reachable: %d)\n",
				summary.CurrentContext, summary.TotalClusters, summary.ReachableCount)
			for _, c := range summary.Contexts {
				cur := " "
				if c.Current {
					cur = "*"
				}
				fmt.Fprintf(out, "  %s %-24s cluster=%-20s ns=%-12s reachable=%v\n",
					cur, c.Name, c.Cluster, c.Namespace, c.Reachable)
			}
			return nil
		},
	}

	listCmd := &cobra.Command{
		Use:   "list",
		Short: "List all configured Kubernetes contexts",
		RunE: func(cmd *cobra.Command, args []string) error {
			cm := cluster.NewContextManager()
			contexts, err := cm.ListContexts(context.Background())
			if err != nil {
				return err
			}
			out := cmd.OutOrStdout()
			if outputFormat == "json" {
				enc := json.NewEncoder(out)
				enc.SetIndent("", "  ")
				return enc.Encode(contexts)
			}
			for _, c := range contexts {
				cur := " "
				if c.Current {
					cur = "*"
				}
				fmt.Fprintf(out, "%s %s (cluster: %s, namespace: %s)\n", cur, c.Name, c.Cluster, c.Namespace)
			}
			return nil
		},
	}

	currentCmd := &cobra.Command{
		Use:   "current",
		Short: "Display the active Kubernetes context",
		RunE: func(cmd *cobra.Command, args []string) error {
			cm := cluster.NewContextManager()
			cur, err := cm.CurrentContext(context.Background())
			if err != nil {
				return err
			}
			out := cmd.OutOrStdout()
			if outputFormat == "json" {
				enc := json.NewEncoder(out)
				enc.SetIndent("", "  ")
				return enc.Encode(map[string]string{"currentContext": cur})
			}
			fmt.Fprintln(out, cur)
			return nil
		},
	}

	useCmd := &cobra.Command{
		Use:   "use <name>",
		Short: "Switch the active Kubernetes context",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			cm := cluster.NewContextManager()
			if err := cm.UseContext(context.Background(), args[0]); err != nil {
				return err
			}
			out := cmd.OutOrStdout()
			if outputFormat == "json" {
				enc := json.NewEncoder(out)
				enc.SetIndent("", "  ")
				return enc.Encode(map[string]string{"currentContext": args[0], "status": "SWITCHED"})
			}
			fmt.Fprintf(out, "==> Switched active context to '%s'.\n", args[0])
			return nil
		},
	}

	var (
		driftContext string
		desiredFile  string
		liveFile     string
	)
	driftCmd := &cobra.Command{
		Use:   "drift",
		Short: "Detect configuration drift between desired manifests and live cluster state",
		RunE: func(cmd *cobra.Command, args []string) error {
			detector := cluster.NewDriftDetector()
			var report *cluster.DriftReport

			if desiredFile != "" || liveFile != "" {
				if desiredFile == "" || liveFile == "" {
					return fmt.Errorf("both --desired and --live must be specified for file-based drift comparison")
				}
				dBytes, err := os.ReadFile(desiredFile)
				if err != nil {
					return fmt.Errorf("unable to read desired manifest %s: %w", desiredFile, err)
				}
				lBytes, err := os.ReadFile(liveFile)
				if err != nil {
					return fmt.Errorf("unable to read live manifest %s: %w", liveFile, err)
				}
				report, err = detector.DetectDrift(string(dBytes), string(lBytes))
				if err != nil {
					return err
				}
			} else {
				spec, err := loadSpec()
				if err != nil {
					return err
				}
				report, err = detector.DetectClusterDrift(context.Background(), spec, namespace, driftContext)
				if err != nil {
					return err
				}
			}

			out := cmd.OutOrStdout()
			if outputFormat == "json" {
				enc := json.NewEncoder(out)
				enc.SetIndent("", "  ")
				return enc.Encode(report)
			}

			if !report.HasDrift {
				fmt.Fprintln(out, "[NO DRIFT DETECTED] Live cluster state matches desired specification.")
				return nil
			}
			fmt.Fprintf(out, "=== DRIFT DETECTED: %d DIFFERENCE(S) ===\n", len(report.Drifts))
			for i, d := range report.Drifts {
				fmt.Fprintf(out, "[%d] %s/%s (%s - %s): desired=%q live=%q\n",
					i+1, d.Kind, d.Name, d.Field, d.DriftType, d.Desired, d.Live)
			}
			return nil
		},
	}
	driftCmd.Flags().StringVar(&driftContext, "context", "", "Target kubeconfig context for live drift check")
	driftCmd.Flags().StringVar(&desiredFile, "desired", "", "Path to desired Kubernetes YAML manifest")
	driftCmd.Flags().StringVar(&liveFile, "live", "", "Path to live Kubernetes YAML manifest")

	contextCmd.AddCommand(listCmd)
	contextCmd.AddCommand(currentCmd)
	contextCmd.AddCommand(useCmd)
	contextCmd.AddCommand(driftCmd)
	return contextCmd
}
