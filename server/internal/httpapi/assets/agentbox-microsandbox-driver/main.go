//go:build agentbox_driver

package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"os/user"
	"path"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"

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
	path    string
	dest    string
}

type fileEntry struct {
	Type       string  `json:"type"`
	Size       int64   `json:"size"`
	ModifiedAt float64 `json:"modifiedAt"`
	Path       string  `json:"path"`
	Name       string  `json:"name"`
}

type fileStat struct {
	Size  int64 `json:"size"`
	IsDir bool  `json:"isDir"`
}

type fileExists struct {
	Exists bool `json:"exists"`
}

type runtimeImage struct {
	ID           string `json:"id"`
	Reference    string `json:"reference"`
	Architecture string `json:"architecture"`
	Size         string `json:"size"`
	Created      string `json:"created"`
	Format       string `json:"format"`
	Path         string `json:"path"`
	Source       string `json:"source"`
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
	case "images":
		return listImages(ctx, stdout)
	case "create":
		return createSandbox(ctx, parsed, stdout)
	case "prepare-image":
		if err := prepareImage(ctx, parsed.image, parsed.path); err != nil {
			return 1, err
		}
		fmt.Fprintln(stdout, parsed.image)
		return 0, nil
	case "inspect":
		handle, err := microsandbox.GetSandbox(ctx, parsed.target)
		if err != nil {
			return 1, err
		}
		if _, err := ensureMicrosandboxOwnership(ctx, handle, parsed.target); err != nil {
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
		handle, err = ensureMicrosandboxOwnership(ctx, handle, parsed.target)
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
	case "fs-list", "fs-read", "fs-write", "fs-mkdir", "fs-stat", "fs-exists", "fs-remove", "fs-rename", "fs-copy-from-host":
		return filesystemSandbox(ctx, parsed, stdin, stdout)
	default:
		return 1, fmt.Errorf("unsupported action: %s", parsed.action)
	}
}

func listImages(ctx context.Context, stdout io.Writer) (int, error) {
	handles, err := microsandbox.Image.List(ctx)
	if err != nil {
		return 1, fmt.Errorf("list microsandbox images: %w", err)
	}
	images := make([]runtimeImage, 0, len(handles))
	for _, handle := range handles {
		created := ""
		if !handle.CreatedAt().IsZero() {
			created = handle.CreatedAt().UTC().Format(time.RFC3339)
		}
		id := handle.ManifestDigest()
		if id == "" {
			id = handle.Reference()
		}
		images = append(images, runtimeImage{
			ID:           id,
			Reference:    handle.Reference(),
			Architecture: handle.Architecture(),
			Size:         formatImageSize(handle.SizeBytes()),
			Created:      created,
			Format:       "oci",
			Source:       "runtime-cache",
		})
	}
	return 0, json.NewEncoder(stdout).Encode(images)
}

func formatImageSize(bytes *int64) string {
	if bytes == nil || *bytes < 0 {
		return ""
	}
	const unit = int64(1024)
	if *bytes < unit {
		return fmt.Sprintf("%d B", *bytes)
	}
	value := float64(*bytes)
	for _, suffix := range []string{"KiB", "MiB", "GiB", "TiB"} {
		value /= float64(unit)
		if value < float64(unit) || suffix == "TiB" {
			return fmt.Sprintf("%.1f %s", value, suffix)
		}
	}
	return ""
}

func createSandbox(ctx context.Context, parsed command, stdout io.Writer) (int, error) {
	handle, err := microsandbox.GetSandbox(ctx, parsed.target)
	if err == nil {
		handle, err = ensureMicrosandboxOwnership(ctx, handle, parsed.target)
		if err != nil {
			return 1, err
		}
		if handle.Status() != microsandbox.SandboxStatusRunning {
			return startSandbox(ctx, parsed.target, stdout)
		}
		fmt.Fprintln(stdout, parsed.target)
		return 0, nil
	}
	if !microsandbox.IsKind(err, microsandbox.ErrSandboxNotFound) {
		return 1, err
	}
	if err := prepareImage(ctx, parsed.image, ""); err != nil {
		return 1, fmt.Errorf("prepare image %s: %w", parsed.image, err)
	}

	options := []microsandbox.SandboxOption{
		microsandbox.WithImage(parsed.image),
		microsandbox.WithCPUs(parsed.cpus),
		microsandbox.WithMemory(parsed.memory),
		microsandbox.WithWorkdir("/"),
		microsandbox.WithUser("root"),
		microsandbox.WithLabels(map[string]string{
			"agentbox.sandbox": strings.TrimPrefix(parsed.target, "agentbox-"),
		}),
		microsandbox.WithDetached(),
	}
	options = append(options, microsandbox.WithNetwork(microsandboxNetworkPolicy(parsed.network)))
	sandbox, err := microsandbox.CreateSandbox(ctx, parsed.target, options...)
	if err != nil {
		return 1, fmt.Errorf("create sandbox: %w", err)
	}
	if parsed.workdir != "" && parsed.workdir != "/" {
		if err := sandbox.FS().Mkdir(ctx, parsed.workdir); err != nil {
			_ = sandbox.Kill(context.Background())
			_ = microsandbox.RemoveSandbox(context.Background(), parsed.target)
			return 1, fmt.Errorf("initialize workdir %s: %w", parsed.workdir, err)
		}
	}
	if err := sandbox.Detach(ctx); err != nil {
		return 1, err
	}
	fmt.Fprintln(stdout, parsed.target)
	return 0, nil
}

func microsandboxNetworkPolicy(network string) *microsandbox.NetworkConfig {
	if network == "none" {
		return microsandbox.NetworkPolicy.None()
	}
	return microsandbox.NetworkPolicy.FromProfiles(
		microsandbox.NetworkProfilePublic,
		microsandbox.NetworkProfilePrivate,
		microsandbox.NetworkProfileHost,
	)
}

func prepareImage(ctx context.Context, reference, archivePath string) error {
	if _, err := microsandbox.Image.Get(ctx, reference); err == nil {
		return nil
	} else if !microsandbox.IsKind(err, microsandbox.ErrImageNotFound) {
		return err
	}
	if archivePath != "" {
		info, err := os.Stat(archivePath)
		if err != nil {
			return fmt.Errorf("inspect local OCI archive: %w", err)
		}
		if !info.Mode().IsRegular() {
			return fmt.Errorf("local OCI archive is not a regular file: %s", archivePath)
		}
		if _, err := microsandbox.Image.Load(ctx, archivePath, reference); err != nil {
			return fmt.Errorf("import Worker OCI image %s: %w", reference, err)
		}
		return nil
	}
	mode := strings.ToLower(strings.TrimSpace(os.Getenv("AGENTBOX_MICROSANDBOX_IMAGE_SOURCE")))
	if mode == "" {
		mode = "auto"
	}
	if mode == "registry" {
		return nil
	}
	if mode != "auto" && mode != "docker" {
		return fmt.Errorf("unsupported AGENTBOX_MICROSANDBOX_IMAGE_SOURCE %q", mode)
	}
	if _, err := exec.LookPath("docker"); err != nil {
		if mode == "docker" {
			return errors.New("Docker prewarm was requested, but Docker is unavailable")
		}
		return nil
	}
	if err := exec.CommandContext(ctx, "docker", "image", "inspect", reference).Run(); err != nil {
		if mode == "docker" {
			return fmt.Errorf("Docker prewarm image is unavailable: %s", reference)
		}
		return nil
	}

	archive, err := os.CreateTemp("", "agentbox-microsandbox-image-*.tar")
	if err != nil {
		return fmt.Errorf("create image archive: %w", err)
	}
	dockerArchivePath := archive.Name()
	if err := archive.Close(); err != nil {
		_ = os.Remove(dockerArchivePath)
		return fmt.Errorf("close image archive: %w", err)
	}
	defer os.Remove(dockerArchivePath)

	if output, err := exec.CommandContext(ctx, "docker", "save", "-o", dockerArchivePath, reference).CombinedOutput(); err != nil {
		return fmt.Errorf("export local Docker image %s: %w: %s", reference, err, strings.TrimSpace(string(output)))
	}
	if _, err := microsandbox.Image.Load(ctx, dockerArchivePath, reference); err != nil {
		return fmt.Errorf("import local Docker image %s: %w", reference, err)
	}
	return nil
}

func startSandbox(ctx context.Context, target string, stdout io.Writer) (int, error) {
	handle, err := microsandbox.GetSandbox(ctx, target)
	if err != nil {
		return 1, err
	}
	handle, err = ensureMicrosandboxOwnership(ctx, handle, target)
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
	handle, err = ensureMicrosandboxOwnership(ctx, handle, target)
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
	handle, err = ensureMicrosandboxOwnership(ctx, handle, target)
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
		handle, err = ensureMicrosandboxOwnership(ctx, handle, target)
		if err != nil {
			return nil, err
		}
	}
	return handle.Connect(ctx)
}

var microsandboxTargetPattern = regexp.MustCompile(`^agentbox-([a-z0-9]+(?:-[a-z0-9]+)*)$`)

func ensureMicrosandboxOwnership(
	ctx context.Context,
	handle *microsandbox.SandboxHandle,
	target string,
) (*microsandbox.SandboxHandle, error) {
	expected, err := expectedMicrosandboxOwner(handle.Name(), target)
	if err != nil {
		return nil, err
	}
	config, err := handle.Config()
	if err != nil {
		return nil, fmt.Errorf("read sandbox ownership: %w", err)
	}
	needsAdoption, err := validateMicrosandboxLabels(config.Labels, target)
	if err != nil {
		return nil, err
	}
	if !needsAdoption {
		return handle, nil
	}
	plan, err := handle.Modify(ctx, microsandbox.ModifyOptions{
		Labels: map[string]string{
			"agentbox.sandbox": expected,
		},
		Policy: microsandbox.ModificationPolicyNoRestart,
	})
	if err != nil {
		return nil, fmt.Errorf("adopt legacy Microsandbox %s: %w", target, err)
	}
	if plan == nil || !plan.Applied || len(plan.Conflicts) > 0 {
		return nil, fmt.Errorf("legacy Microsandbox %s ownership adoption was not applied", target)
	}
	refreshed, err := handle.Refresh(ctx)
	if err != nil {
		return nil, fmt.Errorf("refresh adopted Microsandbox %s: %w", target, err)
	}
	if _, err := expectedMicrosandboxOwner(refreshed.Name(), target); err != nil {
		return nil, err
	}
	refreshedConfig, err := refreshed.Config()
	if err != nil {
		return nil, fmt.Errorf("read adopted sandbox ownership: %w", err)
	}
	needsAdoption, err = validateMicrosandboxLabels(refreshedConfig.Labels, target)
	if err != nil {
		return nil, err
	}
	if needsAdoption {
		return nil, fmt.Errorf("legacy Microsandbox %s ownership adoption could not be verified", target)
	}
	return refreshed, nil
}

func expectedMicrosandboxOwner(handleName, target string) (string, error) {
	match := microsandboxTargetPattern.FindStringSubmatch(target)
	if len(match) != 2 || len(match[1]) < 2 || len(match[1]) > 64 || handleName != target {
		return "", fmt.Errorf("refusing to use invalid Microsandbox target %q", target)
	}
	return match[1], nil
}

func validateMicrosandboxLabels(labels map[string]string, target string) (bool, error) {
	expected, err := expectedMicrosandboxOwner(target, target)
	if err != nil {
		return false, err
	}
	owner, exists := labels["agentbox.sandbox"]
	if !exists {
		return true, nil
	}
	if owner != expected {
		return false, fmt.Errorf("refusing to use Microsandbox %s with mismatched ownership", target)
	}
	return false, nil
}

func execSandbox(ctx context.Context, parsed command, stdin io.Reader, stdout, stderr io.Writer) (int, error) {
	sandbox, err := connectSandbox(ctx, parsed.target)
	if err != nil {
		return 1, err
	}
	defer sandbox.Detach(context.Background())

	executable := resolveExecutable(ctx, sandbox, parsed.command[0])
	options := []microsandbox.ExecOption{microsandbox.WithExecUser("root")}
	if parsed.workdir != "" {
		options = append(options, microsandbox.WithExecCwd(parsed.workdir))
	}
	if !parsed.stdin {
		output, err := sandbox.Exec(ctx, executable, parsed.command[1:], options...)
		if err != nil {
			return 1, err
		}
		_, _ = stdout.Write(output.StdoutBytes())
		_, _ = stderr.Write(output.StderrBytes())
		return output.ExitCode(), nil
	}

	options = append(options, microsandbox.WithExecStdinPipe())
	execution, err := sandbox.ExecStream(ctx, executable, parsed.command[1:], options...)
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
	shell := resolveExecutable(ctx, sandbox, "sh")
	return sandbox.Attach(ctx, shell, "-lc",
		"export HOME=/root USER=root LOGNAME=root TERM=xterm-256color; cd /workspace 2>/dev/null || cd /root 2>/dev/null || cd /; if command -v bash >/dev/null 2>&1; then exec bash -l; else exec "+shell+" -l; fi")
}

func resolveExecutable(ctx context.Context, sandbox *microsandbox.Sandbox, executable string) string {
	if strings.Contains(executable, "/") {
		return executable
	}
	for _, directory := range []string{"/usr/local/bin", "/usr/bin", "/bin", "/usr/local/sbin", "/usr/sbin", "/sbin"} {
		candidate := path.Join(directory, executable)
		exists, err := sandbox.FS().Exists(ctx, candidate)
		if err == nil && exists {
			return candidate
		}
	}
	return executable
}

func filesystemSandbox(ctx context.Context, parsed command, stdin io.Reader, stdout io.Writer) (int, error) {
	sandbox, err := connectSandbox(ctx, parsed.target)
	if err != nil {
		return 1, err
	}
	defer sandbox.Detach(context.Background())
	fs := sandbox.FS()
	switch parsed.action {
	case "fs-list":
		entries, err := fs.List(ctx, parsed.path)
		if err != nil {
			return 1, err
		}
		result := make([]fileEntry, 0, len(entries))
		for _, entry := range entries {
			entryType := "file"
			if entry.Kind == microsandbox.FsEntryKindDirectory {
				entryType = "directory"
			}
			result = append(result, fileEntry{
				Type: entryType,
				Size: entry.Size,
				Path: entry.Path,
				Name: path.Base(entry.Path),
			})
		}
		sort.Slice(result, func(i, j int) bool {
			if result[i].Type != result[j].Type {
				return result[i].Type == "directory"
			}
			return strings.ToLower(result[i].Name) < strings.ToLower(result[j].Name)
		})
		return 0, json.NewEncoder(stdout).Encode(result)
	case "fs-read":
		stream, err := fs.ReadStream(ctx, parsed.path)
		if err != nil {
			return 1, err
		}
		defer stream.Close()
		_, err = stream.CopyTo(ctx, stdout)
		return 0, err
	case "fs-write":
		temporary := parsed.path + ".agentbox-write"
		_ = fs.Remove(ctx, temporary)
		stream, err := fs.WriteStream(ctx, temporary)
		if err != nil {
			return 1, err
		}
		_, copyErr := io.Copy(stream, stdin)
		closeErr := stream.Close(ctx)
		if err := errors.Join(copyErr, closeErr); err != nil {
			_ = fs.Remove(context.Background(), temporary)
			return 1, err
		}
		if err := fs.Rename(ctx, temporary, parsed.path); err != nil {
			_ = fs.Remove(context.Background(), temporary)
			return 1, err
		}
		return 0, nil
	case "fs-mkdir":
		return 0, fs.Mkdir(ctx, parsed.path)
	case "fs-stat":
		stat, err := fs.Stat(ctx, parsed.path)
		if err != nil {
			return 1, err
		}
		return 0, json.NewEncoder(stdout).Encode(fileStat{Size: stat.Size, IsDir: stat.IsDir})
	case "fs-exists":
		exists, err := fs.Exists(ctx, parsed.path)
		if err != nil {
			return 1, err
		}
		return 0, json.NewEncoder(stdout).Encode(fileExists{Exists: exists})
	case "fs-remove":
		stat, err := fs.Stat(ctx, parsed.path)
		if err != nil {
			return 1, err
		}
		if stat.IsDir {
			return 0, fs.RemoveDir(ctx, parsed.path)
		}
		return 0, fs.Remove(ctx, parsed.path)
	case "fs-rename":
		return 0, fs.Rename(ctx, parsed.path, parsed.dest)
	case "fs-copy-from-host":
		return 0, fs.CopyFromHost(ctx, parsed.path, parsed.dest)
	default:
		return 1, fmt.Errorf("unsupported filesystem action: %s", parsed.action)
	}
}

func parseCommand(args []string) (command, error) {
	if len(args) > 0 && args[0] == "microsandbox" {
		args = args[1:]
	}
	if len(args) == 0 {
		return command{}, errors.New("usage: agentbox-microsandbox-driver <action>")
	}
	parsed := command{action: args[0], cpus: 2, memory: 4096, workdir: "/workspace", network: "egress"}
	switch parsed.action {
	case "probe", "images":
		if len(args) != 1 {
			return command{}, fmt.Errorf("%s does not accept arguments", parsed.action)
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
		switch parsed.network {
		case "none", "egress":
		case "restricted":
			return command{}, errors.New("restricted network is not supported by Microsandbox")
		default:
			return command{}, fmt.Errorf("unsupported network policy: %s", parsed.network)
		}
	case "prepare-image":
		if len(args) != 2 && len(args) != 4 {
			return command{}, errors.New("prepare-image requires an image reference and optional --archive path")
		}
		parsed.image = args[1]
		if len(args) == 4 {
			if args[2] != "--archive" || strings.TrimSpace(args[3]) == "" {
				return command{}, errors.New("prepare-image supports only --archive <path>")
			}
			parsed.path = args[3]
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
	case "fs-list", "fs-read", "fs-write", "fs-mkdir", "fs-stat", "fs-exists", "fs-remove":
		if len(args) != 3 {
			return command{}, fmt.Errorf("%s requires a target and path", parsed.action)
		}
		parsed.target, parsed.path = args[1], args[2]
	case "fs-rename", "fs-copy-from-host":
		if len(args) != 4 {
			return command{}, fmt.Errorf("%s requires a target, source, and destination", parsed.action)
		}
		parsed.target, parsed.path, parsed.dest = args[1], args[2], args[3]
	default:
		return command{}, fmt.Errorf("unsupported action: %s", parsed.action)
	}
	return parsed, nil
}
