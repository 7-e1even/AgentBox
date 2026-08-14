//go:build agentbox_driver

package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"os/user"
	"strconv"
	"strings"

	microsandbox "github.com/superradcompany/microsandbox/sdk/go"
)

type command struct {
	action  string
	target  string
	image   string
	workdir string
	network string
	command []string
	cpus    uint8
	memory  uint32
	stdin   bool
}

func main() {
	code, err := run(context.Background(), os.Args[1:], os.Stdin, os.Stdout, os.Stderr)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	os.Exit(code)
}

func run(ctx context.Context, args []string, stdin io.Reader, stdout, stderr io.Writer) (int, error) {
	parsed, err := parseCommand(args)
	if err != nil {
		return 1, err
	}
	if os.Getenv("HOME") == "" {
		current, err := user.Current()
		if err != nil || current.HomeDir == "" {
			return 1, errors.New("resolve current user home directory")
		}
		if err := os.Setenv("HOME", current.HomeDir); err != nil {
			return 1, fmt.Errorf("set HOME: %w", err)
		}
	}
	if _, err := os.Stat("/dev/kvm"); err != nil {
		return 1, fmt.Errorf("/dev/kvm is unavailable: %w", err)
	}
	if err := microsandbox.EnsureInstalled(ctx); err != nil {
		return 1, fmt.Errorf("install microsandbox runtime: %w", err)
	}

	switch parsed.action {
	case "probe":
		version, err := microsandbox.RuntimeVersion()
		if err != nil {
			return 1, fmt.Errorf("probe microsandbox runtime: %w", err)
		}
		fmt.Fprintln(stdout, version)
		return 0, nil
	case "create":
		return createSandbox(ctx, parsed, stdout)
	case "inspect":
		if _, err := microsandbox.GetSandbox(ctx, parsed.target); err != nil {
			return 1, err
		}
		fmt.Fprintln(stdout, parsed.target)
		return 0, nil
	case "start":
		return startSandbox(ctx, parsed.target, stdout)
	case "stop":
		handle, err := microsandbox.GetSandbox(ctx, parsed.target)
		if err != nil {
			return 1, err
		}
		if handle.Status() == microsandbox.SandboxStatusRunning || handle.Status() == microsandbox.SandboxStatusDraining {
			if err := handle.Stop(ctx); err != nil {
				return 1, err
			}
		}
		fmt.Fprintln(stdout, parsed.target)
		return 0, nil
	case "delete":
		return deleteSandbox(ctx, parsed.target)
	case "exec":
		return execSandbox(ctx, parsed, stdin, stdout, stderr)
	case "terminal":
		return terminalSandbox(ctx, parsed.target)
	default:
		return 1, fmt.Errorf("unsupported action: %s", parsed.action)
	}
}

func createSandbox(ctx context.Context, parsed command, stdout io.Writer) (int, error) {
	handle, err := microsandbox.GetSandbox(ctx, parsed.target)
	if err == nil {
		if handle.Status() != microsandbox.SandboxStatusRunning {
			return startSandbox(ctx, parsed.target, stdout)
		}
		fmt.Fprintln(stdout, parsed.target)
		return 0, nil
	}
	if !microsandbox.IsKind(err, microsandbox.ErrSandboxNotFound) {
		return 1, err
	}
	if err := seedImageFromDocker(ctx, parsed.image); err != nil {
		return 1, err
	}

	options := []microsandbox.SandboxOption{
		microsandbox.WithImage(parsed.image),
		microsandbox.WithCPUs(parsed.cpus),
		microsandbox.WithMemory(parsed.memory),
		microsandbox.WithWorkdir(parsed.workdir),
		microsandbox.WithUser("0:0"),
		microsandbox.WithLabels(map[string]string{
			"agentbox.sandbox": strings.TrimPrefix(parsed.target, "agentbox-"),
		}),
		microsandbox.WithDetached(),
	}
	if parsed.network == "none" {
		options = append(options, microsandbox.WithNetwork(microsandbox.NetworkPolicy.None()))
	}
	sandbox, err := microsandbox.CreateSandbox(ctx, parsed.target, options...)
	if err != nil {
		return 1, err
	}
	if err := sandbox.Detach(ctx); err != nil {
		return 1, err
	}
	fmt.Fprintln(stdout, parsed.target)
	return 0, nil
}

func seedImageFromDocker(ctx context.Context, reference string) error {
	if _, err := microsandbox.Image.Get(ctx, reference); err == nil {
		return nil
	} else if !microsandbox.IsKind(err, microsandbox.ErrImageNotFound) {
		return err
	}
	if _, err := exec.LookPath("docker"); err != nil {
		return nil
	}
	if err := exec.CommandContext(ctx, "docker", "image", "inspect", reference).Run(); err != nil {
		return nil
	}

	archive, err := os.CreateTemp("", "agentbox-microsandbox-image-*.tar")
	if err != nil {
		return fmt.Errorf("create image archive: %w", err)
	}
	archivePath := archive.Name()
	if err := archive.Close(); err != nil {
		_ = os.Remove(archivePath)
		return fmt.Errorf("close image archive: %w", err)
	}
	defer os.Remove(archivePath)

	if output, err := exec.CommandContext(ctx, "docker", "save", "-o", archivePath, reference).CombinedOutput(); err != nil {
		return fmt.Errorf("export local Docker image %s: %w: %s", reference, err, strings.TrimSpace(string(output)))
	}
	if _, err := microsandbox.Image.Load(ctx, archivePath, reference); err != nil {
		return fmt.Errorf("import local Docker image %s: %w", reference, err)
	}
	return nil
}

func startSandbox(ctx context.Context, target string, stdout io.Writer) (int, error) {
	handle, err := microsandbox.GetSandbox(ctx, target)
	if err != nil {
		return 1, err
	}
	if handle.Status() != microsandbox.SandboxStatusRunning {
		sandbox, err := handle.StartDetached(ctx)
		if err != nil {
			return 1, err
		}
		if err := sandbox.Detach(ctx); err != nil {
			return 1, err
		}
	}
	fmt.Fprintln(stdout, target)
	return 0, nil
}

func deleteSandbox(ctx context.Context, target string) (int, error) {
	handle, err := microsandbox.GetSandbox(ctx, target)
	if microsandbox.IsKind(err, microsandbox.ErrSandboxNotFound) {
		return 0, nil
	}
	if err != nil {
		return 1, err
	}
	if handle.Status() == microsandbox.SandboxStatusRunning || handle.Status() == microsandbox.SandboxStatusDraining {
		if err := handle.Kill(ctx); err != nil {
			return 1, err
		}
	}
	if err := microsandbox.RemoveSandbox(ctx, target); err != nil {
		return 1, err
	}
	return 0, nil
}

func connectSandbox(ctx context.Context, target string) (*microsandbox.Sandbox, error) {
	handle, err := microsandbox.GetSandbox(ctx, target)
	if err != nil {
		return nil, err
	}
	if handle.Status() != microsandbox.SandboxStatusRunning {
		sandbox, err := handle.StartDetached(ctx)
		if err != nil {
			return nil, err
		}
		if err := sandbox.Detach(ctx); err != nil {
			return nil, err
		}
		handle, err = microsandbox.GetSandbox(ctx, target)
		if err != nil {
			return nil, err
		}
	}
	return handle.Connect(ctx)
}

func execSandbox(ctx context.Context, parsed command, stdin io.Reader, stdout, stderr io.Writer) (int, error) {
	sandbox, err := connectSandbox(ctx, parsed.target)
	if err != nil {
		return 1, err
	}
	defer sandbox.Detach(context.Background())

	options := []microsandbox.ExecOption{microsandbox.WithExecUser("0:0")}
	if parsed.workdir != "" {
		options = append(options, microsandbox.WithExecCwd(parsed.workdir))
	}
	if !parsed.stdin {
		output, err := sandbox.Exec(ctx, parsed.command[0], parsed.command[1:], options...)
		if err != nil {
			return 1, err
		}
		_, _ = stdout.Write(output.StdoutBytes())
		_, _ = stderr.Write(output.StderrBytes())
		return output.ExitCode(), nil
	}

	options = append(options, microsandbox.WithExecStdinPipe())
	execution, err := sandbox.ExecStream(ctx, parsed.command[0], parsed.command[1:], options...)
	if err != nil {
		return 1, err
	}
	defer execution.Close()
	if sink := execution.TakeStdin(); sink != nil {
		go func() {
			_, _ = io.Copy(sink, stdin)
			_ = sink.Close()
		}()
	}
	exitCode := 1
	for {
		event, err := execution.Recv(ctx)
		if err != nil {
			return 1, err
		}
		switch event.Kind {
		case microsandbox.ExecEventStdout:
			_, _ = stdout.Write(event.Data)
		case microsandbox.ExecEventStderr:
			_, _ = stderr.Write(event.Data)
		case microsandbox.ExecEventExited:
			exitCode = event.ExitCode
		case microsandbox.ExecEventFailed:
			return 1, fmt.Errorf("microsandbox process failed: %v", event.Failure)
		case microsandbox.ExecEventDone:
			return exitCode, nil
		}
	}
}

func terminalSandbox(ctx context.Context, target string) (int, error) {
	sandbox, err := connectSandbox(ctx, target)
	if err != nil {
		return 1, err
	}
	defer sandbox.Detach(context.Background())
	return sandbox.Attach(ctx, "env",
		"HOME=/root",
		"USER=root",
		"LOGNAME=root",
		"TERM=xterm-256color",
		"sh", "-c",
		"cd /root 2>/dev/null || cd /; if command -v bash >/dev/null 2>&1; then exec bash -l; else exec sh -l; fi",
	)
}

func parseCommand(args []string) (command, error) {
	if len(args) > 0 && args[0] == "microsandbox" {
		args = args[1:]
	}
	if len(args) == 0 {
		return command{}, errors.New("usage: agentbox-microsandbox-driver <action>")
	}
	parsed := command{action: args[0], cpus: 2, memory: 4096, workdir: "/workspace", network: "restricted"}
	switch parsed.action {
	case "probe":
		if len(args) != 1 {
			return command{}, errors.New("probe does not accept arguments")
		}
	case "create":
		if len(args) < 3 {
			return command{}, errors.New("create requires target and image")
		}
		parsed.target, parsed.image = args[1], args[2]
		for index := 3; index < len(args); index++ {
			if index+1 >= len(args) {
				return command{}, fmt.Errorf("%s requires a value", args[index])
			}
			value := args[index+1]
			switch args[index] {
			case "--cpus":
				number, err := strconv.ParseUint(value, 10, 8)
				if err != nil || number == 0 {
					return command{}, fmt.Errorf("invalid CPU count: %s", value)
				}
				parsed.cpus = uint8(number)
			case "--memory":
				number, err := strconv.ParseUint(value, 10, 32)
				if err != nil || number == 0 {
					return command{}, fmt.Errorf("invalid memory MiB: %s", value)
				}
				parsed.memory = uint32(number)
			case "--workdir":
				parsed.workdir = value
			case "--network":
				parsed.network = value
			default:
				return command{}, fmt.Errorf("unsupported create option: %s", args[index])
			}
			index++
		}
	case "inspect", "start", "stop", "delete", "terminal":
		if len(args) != 2 {
			return command{}, fmt.Errorf("%s requires a target", parsed.action)
		}
		parsed.target = args[1]
	case "exec":
		index := 1
		for index < len(args) {
			switch args[index] {
			case "--stdin":
				parsed.stdin = true
				index++
			case "--workdir":
				if index+1 >= len(args) {
					return command{}, errors.New("--workdir requires a value")
				}
				parsed.workdir = args[index+1]
				index += 2
			default:
				parsed.target = args[index]
				index++
				if index < len(args) && args[index] == "--" {
					index++
				}
				parsed.command = append([]string(nil), args[index:]...)
				index = len(args)
			}
		}
		if parsed.target == "" || len(parsed.command) == 0 {
			return command{}, errors.New("exec requires a target and command")
		}
	default:
		return command{}, fmt.Errorf("unsupported action: %s", parsed.action)
	}
	return parsed, nil
}
