package runtime

import (
	"bytes"
	"context"
	"reflect"
	"strings"
	"testing"
)

func TestComposeRunnerUp(t *testing.T) {
	t.Run("runs detached with build and services", func(t *testing.T) {
		var calls [][]string
		runner := NewComposeRunnerWithExec(makeFakeExec([]fakeStep{
			{stdout: "Container app-db-1 Started\n", stderr: "Network app_default Created\n", exitCode: 0},
		}, &calls))

		var outBuf, errBuf bytes.Buffer
		err := runner.Up(context.Background(), ComposeUpOptions{
			File:     "docker-compose.yml",
			Services: []string{"db", "backend"},
			Build:    true,
			Detach:   true,
		}, &outBuf, &errBuf)
		if err != nil {
			t.Fatalf("Up failed: %v", err)
		}

		want := []string{"docker", "compose", "-f", "docker-compose.yml", "up", "-d", "--build", "db", "backend"}
		if len(calls) != 1 || !reflect.DeepEqual(calls[0], want) {
			t.Fatalf("args mismatch:\ngot:  %v\nwant: %v", calls, want)
		}
		if !strings.Contains(outBuf.String(), "Started") {
			t.Errorf("expected stdout captured, got %q", outBuf.String())
		}
		if !strings.Contains(errBuf.String(), "Created") {
			t.Errorf("expected stderr captured, got %q", errBuf.String())
		}
	})

	t.Run("nil writers and foreground without build", func(t *testing.T) {
		var calls [][]string
		runner := NewComposeRunnerWithExec(makeFakeExec([]fakeStep{{exitCode: 0}}, &calls))

		err := runner.Up(context.Background(), ComposeUpOptions{
			File:   "my dir/compose.yaml",
			Detach: false,
			Build:  false,
		}, nil, nil)
		if err != nil {
			t.Fatalf("Up failed: %v", err)
		}
		want := []string{"docker", "compose", "-f", "my dir/compose.yaml", "up"}
		if !reflect.DeepEqual(calls[0], want) {
			t.Fatalf("args mismatch:\ngot:  %v\nwant: %v", calls[0], want)
		}
	})

	t.Run("wraps docker compose up failure", func(t *testing.T) {
		runner := NewComposeRunnerWithExec(makeFakeExec([]fakeStep{{stderr: "port busy", exitCode: 1}}, nil))
		err := runner.Up(context.Background(), ComposeUpOptions{File: "compose.yaml", Detach: true}, nil, nil)
		if err == nil || !strings.Contains(err.Error(), "docker compose up failed") {
			t.Fatalf("expected wrapped up error, got %v", err)
		}
	})
}

func TestComposeRunnerDown(t *testing.T) {
	t.Run("passes -v and --remove-orphans flags", func(t *testing.T) {
		var calls [][]string
		runner := NewComposeRunnerWithExec(makeFakeExec([]fakeStep{{stdout: "Removed\n", exitCode: 0}}, &calls))

		var outBuf bytes.Buffer
		err := runner.Down(context.Background(), ComposeDownOptions{
			File:          "compose.yml",
			RemoveVolumes: true,
			RemoveOrphans: true,
		}, &outBuf, nil)
		if err != nil {
			t.Fatalf("Down failed: %v", err)
		}
		want := []string{"docker", "compose", "-f", "compose.yml", "down", "-v", "--remove-orphans"}
		if !reflect.DeepEqual(calls[0], want) {
			t.Fatalf("args mismatch:\ngot:  %v\nwant: %v", calls[0], want)
		}
	})

	t.Run("wraps docker compose down failure", func(t *testing.T) {
		runner := NewComposeRunnerWithExec(makeFakeExec([]fakeStep{{stderr: "daemon unreachable", exitCode: 1}}, nil))
		err := runner.Down(context.Background(), ComposeDownOptions{File: "compose.yml"}, nil, nil)
		if err == nil || !strings.Contains(err.Error(), "docker compose down failed") {
			t.Fatalf("expected wrapped down error, got %v", err)
		}
	})
}

func TestComposeRunnerStatus(t *testing.T) {
	t.Run("parses NDJSON format with Publishers objects and ignores stderr warnings", func(t *testing.T) {
		ndjson := `{"Name":"shop-db-1","Service":"db","Image":"postgres:16","State":"running","Status":"Up 2 minutes (healthy)","Health":"healthy","Publishers":[{"URL":"","TargetPort":5432,"PublishedPort":0,"Protocol":"tcp"}]}` + "\r\n" +
			`{"Name":"shop-web-1","Service":"web","Image":"shop-web:latest","State":"running","Status":"Up 2 minutes","Health":"","Publishers":[{"URL":"0.0.0.0","TargetPort":80,"PublishedPort":8080,"Protocol":"tcp"},{"URL":"0.0.0.0","TargetPort":80,"PublishedPort":8080,"Protocol":"tcp"},{"URL":"","TargetPort":443,"PublishedPort":8443,"Protocol":""}]}` + "\n"

		var calls [][]string
		runner := NewComposeRunnerWithExec(makeFakeExec([]fakeStep{
			{stdout: ndjson, stderr: "WARN[0000] version is obsolete\n", exitCode: 0},
		}, &calls))

		statuses, err := runner.Status(context.Background(), "docker-compose.yml", []string{"db", "web"})
		if err != nil {
			t.Fatalf("Status failed: %v", err)
		}
		wantCmd := []string{"docker", "compose", "-f", "docker-compose.yml", "ps", "-a", "--format", "json", "db", "web"}
		if !reflect.DeepEqual(calls[0], wantCmd) {
			t.Fatalf("args mismatch:\ngot:  %v\nwant: %v", calls[0], wantCmd)
		}
		if len(statuses) != 2 {
			t.Fatalf("expected 2 statuses, got %d: %+v", len(statuses), statuses)
		}
		if statuses[0].Service != "db" || statuses[0].Health != "healthy" || statuses[0].Ports != "5432/tcp" {
			t.Errorf("unexpected db status: %+v", statuses[0])
		}
		if statuses[1].Service != "web" || statuses[1].Ports != "0.0.0.0:8080->80/tcp, 8443->443/tcp" {
			t.Errorf("unexpected web status: %+v", statuses[1])
		}
	})

	t.Run("parses legacy JSON array format and fallback Publishers string / Ports field", func(t *testing.T) {
		arrJSON := `[
			{"Name":"app-api-1","Service":"api","Image":"api:1","State":"running","Status":"Up 5m","Publishers":"0.0.0.0:3000->3000/tcp"},
			{"Name":"app-worker-1","Service":"worker","Image":"worker:1","State":"exited","Status":"Exited (0)","Ports":"9090/tcp","Publishers":null}
		]`
		runner := NewComposeRunnerWithExec(makeFakeExec([]fakeStep{{stdout: arrJSON, exitCode: 0}}, nil))

		statuses, err := runner.Status(context.Background(), "compose.yaml", nil)
		if err != nil {
			t.Fatalf("Status array parse failed: %v", err)
		}
		if len(statuses) != 2 {
			t.Fatalf("expected 2 statuses, got %d", len(statuses))
		}
		if statuses[0].Ports != "0.0.0.0:3000->3000/tcp" {
			t.Errorf("expected fallback string Publishers, got %q", statuses[0].Ports)
		}
		if statuses[1].Ports != "9090/tcp" || statuses[1].State != "exited" {
			t.Errorf("expected fallback Ports field, got %+v", statuses[1])
		}
	})

	t.Run("empty output returns empty non-nil slice", func(t *testing.T) {
		runner := NewComposeRunnerWithExec(makeFakeExec([]fakeStep{{stdout: "   \n", exitCode: 0}}, nil))
		statuses, err := runner.Status(context.Background(), "compose.yaml", nil)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if statuses == nil || len(statuses) != 0 {
			t.Fatalf("expected empty non-nil slice, got %#v", statuses)
		}
	})

	t.Run("malformed JSON and command failures return errors", func(t *testing.T) {
		badArray := NewComposeRunnerWithExec(makeFakeExec([]fakeStep{{stdout: "[{bad json]", exitCode: 0}}, nil))
		if _, err := badArray.Status(context.Background(), "compose.yaml", nil); err == nil {
			t.Errorf("expected error for malformed JSON array")
		}

		badNDJSON := NewComposeRunnerWithExec(makeFakeExec([]fakeStep{{stdout: "{not json}\n", exitCode: 0}}, nil))
		if _, err := badNDJSON.Status(context.Background(), "compose.yaml", nil); err == nil {
			t.Errorf("expected error for malformed NDJSON")
		}

		cmdFail := NewComposeRunnerWithExec(makeFakeExec([]fakeStep{{stderr: "no configuration file provided", exitCode: 1}}, nil))
		if _, err := cmdFail.Status(context.Background(), "compose.yaml", nil); err == nil || !strings.Contains(err.Error(), "no configuration file provided") {
			t.Errorf("expected wrapped stderr detail, got %v", err)
		}
	})
}

func TestComposeRunnerSecurityValidationAndConstructors(t *testing.T) {
	if r := NewComposeRunner(); r == nil || r.execCommand == nil {
		t.Fatalf("NewComposeRunner should initialize default execCommand")
	}
	if r := NewComposeRunnerWithExec(nil); r == nil || r.execCommand == nil {
		t.Fatalf("NewComposeRunnerWithExec(nil) should fall back to default execCommand")
	}

	runner := NewComposeRunner()
	badFiles := []string{"", "   ", "-f", "--file=compose.yml", "compose\n.yml", "compose\x00.yml", "compose\t.yml"}
	for _, f := range badFiles {
		if err := runner.Up(context.Background(), ComposeUpOptions{File: f}, nil, nil); err == nil {
			t.Errorf("Up should reject invalid file %q", f)
		}
		if err := runner.Down(context.Background(), ComposeDownOptions{File: f}, nil, nil); err == nil {
			t.Errorf("Down should reject invalid file %q", f)
		}
		if _, err := runner.Status(context.Background(), f, nil); err == nil {
			t.Errorf("Status should reject invalid file %q", f)
		}
	}

	badServices := []string{"", "--privileged", "-d", "bad name", "bad\nname"}
	for _, svc := range badServices {
		if err := runner.Up(context.Background(), ComposeUpOptions{File: "compose.yml", Services: []string{svc}}, nil, nil); err == nil {
			t.Errorf("Up should reject invalid service %q", svc)
		}
		if _, err := runner.Status(context.Background(), "compose.yml", []string{svc}); err == nil {
			t.Errorf("Status should reject invalid service %q", svc)
		}
	}
}
