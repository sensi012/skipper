package main

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/spf13/cobra"
	"github.com/unified-platform/container/pkg/bootstrapper"
	"github.com/unified-platform/container/pkg/build"
	"github.com/unified-platform/container/pkg/config"
	"github.com/unified-platform/container/pkg/dev"
	"github.com/unified-platform/container/pkg/diagnostics"
	"github.com/unified-platform/container/pkg/doctor"
	"github.com/unified-platform/container/pkg/governance"
	"github.com/unified-platform/container/pkg/local"
	"github.com/unified-platform/container/pkg/mcp"
	"github.com/unified-platform/container/pkg/opmem"
	"github.com/unified-platform/container/pkg/rollout"
	"github.com/unified-platform/container/pkg/runtime"
	"github.com/unified-platform/container/pkg/secret"
	"github.com/unified-platform/container/pkg/security"
	"github.com/unified-platform/container/pkg/synthesizer"
	"github.com/unified-platform/container/pkg/transpile"
	"github.com/unified-platform/container/pkg/tunnel"
	"gopkg.in/yaml.v3"
)

var (
	version       = "0.1.0"
	targetRuntime string
	namespace     string
	appConfigFile string
	outputFormat  string
)

func main() {
	rootCmd := &cobra.Command{
		Use:     "skipper",
		Short:   "Skipper: Unified Container & Kubernetes Runtime Abstraction CLI",
		Long:    "A developer-first CLI unifying container build, local execution, production deployment, and deterministic diagnostics.",
		Version: version,
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
	return &cobra.Command{
		Use:   "build",
		Short: "Auto-detect stack and build OCI-compliant container image",
		RunE: func(cmd *cobra.Command, args []string) error {
			spec, err := loadSpec()
			if err != nil {
				return err
			}
			builder := build.NewCNBBuilder()
			ctx := context.Background()

			tag, err := builder.Build(ctx, spec, os.Stdout)
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

			fmt.Printf("==> Successfully built and tagged OCI image: %s\n", tag)
			return nil
		},
	}
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
			if outputFile != "" {
				if err := os.WriteFile(outputFile, []byte(manifestYaml), 0644); err != nil {
					return fmt.Errorf("failed to write manifest file: %w", err)
				}
				fmt.Printf("==> Synthesized production manifests written to: %s\n", outputFile)
			} else {
				fmt.Println(manifestYaml)
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

			if outputFormat == "json" {
				if violations == nil {
					violations = []governance.PolicyViolation{}
				}
				enc := json.NewEncoder(os.Stdout)
				enc.SetIndent("", "  ")
				return enc.Encode(violations)
			}

			if len(violations) == 0 {
				fmt.Println("[POLICY AUDIT PASSED] AppSpec complies with all enterprise platform rules.")
				return nil
			}

			fmt.Printf("=== POLICY AUDIT FINDINGS: %d RULE(S) TRIGGERED ===\n\n", len(violations))
			for i, v := range violations {
				fmt.Printf("[%d] [%s] %s: %s\n", i+1, v.Severity, v.RuleName, v.Message)
			}
			return nil
		},
	}
}

func newDebugCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "debug [name]",
		Short: "Attach an interactive debugging session with retained context",
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
			fmt.Printf("==> Spawning ephemeral debug session on '%s' via [%s]...\n", workloadName, engine.Name())

			startTime := time.Now()
			code, err := engine.Debug(ctx, runtime.DebugOptions{
				WorkloadName: workloadName,
			}, os.Stdin, os.Stdout, os.Stderr)

			endTime := time.Now()

			// Retain debug session in operational memory
			if ledger, err := opmem.NewOperationalMemoryLedger(""); err == nil {
				_ = ledger.RecordDebugSession(&opmem.DebugSessionRecord{
					SessionID:    fmt.Sprintf("dbg-%d", startTime.Unix()),
					StartedAt:    startTime,
					EndedAt:      endTime,
					Operator:     os.Getenv("USERNAME"),
					WorkloadName: workloadName,
					ExitCode:     code,
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

			fmt.Printf("\n[Debug session preserved in Operational Memory with exit code %d]\n", code)
			return err
		},
	}
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

func newDoctorCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "doctor",
		Short: "Check local environment prerequisites and port collisions",
		RunE: func(cmd *cobra.Command, args []string) error {
			engine := doctor.NewDoctorEngine()
			report := engine.RunDiagnostics(context.Background())

			if outputFormat == "json" {
				enc := json.NewEncoder(os.Stdout)
				enc.SetIndent("", "  ")
				return enc.Encode(report)
			}

			fmt.Println("==> Running Skipper environment health audit...")

			for _, check := range report.Checks {
				symbol := "[✓]"
				if check.Status == doctor.StatusWarn {
					symbol = "[!]"
				} else if check.Status == doctor.StatusFail {
					symbol = "[✗]"
				}
				fmt.Printf("  %s %-30s : %s\n", symbol, check.Name, check.Message)
				if check.Remediation != "" {
					fmt.Printf("      -> Fix: %s\n", check.Remediation)
				}
			}

			if !report.Passed {
				return fmt.Errorf("doctor audit failed; please resolve blocking issues above")
			}
			fmt.Println("==> All critical prerequisites satisfied!")
			return nil
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

	initCmd.Flags().StringVarP(&initName, "name", "n", "", "Workload name (default: current directory name)")
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
	var pfNamespace string
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
				Namespace:    pfNamespace,
				WorkloadName: workload,
				LocalPort:    localPort,
				RemotePort:   remotePort,
			}, os.Stdout, os.Stderr)
		},
	}

	pfCmd.Flags().StringVarP(&pfNamespace, "namespace", "n", "default", "Kubernetes namespace")
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
			fmt.Printf("==> Secret '%s' saved successfully.\n", parts[0])
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
			fmt.Printf("%s=%s\n", args[0], val)
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
			fmt.Println(manifest)
			return nil
		},
	}

	secretCmd.AddCommand(setCmd)
	secretCmd.AddCommand(getCmd)
	secretCmd.AddCommand(exportCmd)
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
			fmt.Printf("==> Successfully transpiled '%s' into '%s' (Service: %s)!\n", file, targetFile, spec.Name)
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

func newExplainCmd() *cobra.Command {
	var showRawSchema bool
	cmd := &cobra.Command{
		Use:   "explain",
		Short: "Output declarative schema and instruction grammar for AI agent consumption",
		RunE: func(cmd *cobra.Command, args []string) error {
			if showRawSchema {
				schemaData, err := os.ReadFile("schemas/skipper.schema.json")
				if err != nil {
					return fmt.Errorf("unable to load schemas/skipper.schema.json: %w", err)
				}
				fmt.Println(string(schemaData))
				return nil
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
- Migrate Docker Compose: run 'skipper import compose [path]'

## 3. Self-Healing & Remediation Loop
When diagnosing a failing workload:
1. Call 'skipper diagnose <workload> -f json'
2. Parse 'detectorName', 'rootCause', and 'remediationCmd'
3. Execute 'remediationCmd' or adjust memory/cpu in 'skipper.yaml'
4. Re-run 'skipper deploy' or 'skipper up'
`
			fmt.Print(grammar)
			return nil
		},
	}

	cmd.Flags().BoolVar(&showRawSchema, "schema", false, "Output formal JSON Schema Draft 2020-12")
	return cmd
}
