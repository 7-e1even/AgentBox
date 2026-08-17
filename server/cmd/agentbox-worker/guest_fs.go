package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

type guestFileEntry struct {
	Type       string  `json:"type"`
	Size       int64   `json:"size"`
	ModifiedAt float64 `json:"modifiedAt"`
	Path       string  `json:"path"`
	Name       string  `json:"name"`
}

type guestFileStat struct {
	Size  int64 `json:"size"`
	IsDir bool  `json:"isDir"`
}

type guestFileExists struct {
	Exists bool `json:"exists"`
}

func runGuestFS(arguments []string, stdin io.Reader, stdout io.Writer) error {
	if len(arguments) < 2 {
		return errors.New("usage: agentbox-guest guest-fs <list|read|write|append|mkdir|stat|remove|rename> <path>")
	}
	action, target := arguments[0], arguments[1]
	switch action {
	case "list":
		return listGuestDirectory(target, stdout)
	case "read":
		file, err := os.Open(target)
		if err != nil {
			return err
		}
		defer file.Close()
		_, err = io.Copy(stdout, file)
		return err
	case "write":
		return writeGuestFile(target, stdin)
	case "append":
		if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
			return err
		}
		file, err := os.OpenFile(target, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600)
		if err != nil {
			return err
		}
		_, copyErr := io.Copy(file, stdin)
		closeErr := file.Close()
		return errors.Join(copyErr, closeErr)
	case "mkdir":
		return os.MkdirAll(target, 0o755)
	case "stat":
		info, err := os.Stat(target)
		if err != nil {
			return err
		}
		return json.NewEncoder(stdout).Encode(guestFileStat{Size: info.Size(), IsDir: info.IsDir()})
	case "exists":
		_, err := os.Stat(target)
		if err != nil && !errors.Is(err, os.ErrNotExist) {
			return err
		}
		return json.NewEncoder(stdout).Encode(guestFileExists{Exists: err == nil})
	case "remove":
		return os.Remove(target)
	case "rename":
		if len(arguments) != 3 {
			return errors.New("rename requires source and destination paths")
		}
		if err := os.MkdirAll(filepath.Dir(arguments[2]), 0o755); err != nil {
			return err
		}
		return os.Rename(target, arguments[2])
	default:
		return fmt.Errorf("unsupported guest filesystem action: %s", action)
	}
}

func listGuestDirectory(target string, stdout io.Writer) error {
	entries, err := os.ReadDir(target)
	if err != nil {
		return err
	}
	result := make([]guestFileEntry, 0, min(len(entries), 1000))
	for _, entry := range entries {
		if len(result) == 1000 {
			break
		}
		info, err := entry.Info()
		if err != nil {
			// A single unreadable entry (e.g. a dangling symlink or a mount
			// permission problem) must not fail the whole directory listing.
			_, _ = fmt.Fprintf(os.Stderr, "guest-fs list: skipping %s: %v\n", entry.Name(), err)
			continue
		}
		entryType := "file"
		if entry.IsDir() {
			entryType = "directory"
		}
		result = append(result, guestFileEntry{
			Type:       entryType,
			Size:       info.Size(),
			ModifiedAt: float64(info.ModTime().UnixNano()) / 1e9,
			Path:       filepath.ToSlash(filepath.Join(target, entry.Name())),
			Name:       entry.Name(),
		})
	}
	sort.Slice(result, func(i, j int) bool {
		if result[i].Type != result[j].Type {
			return result[i].Type == "directory"
		}
		return strings.ToLower(result[i].Name) < strings.ToLower(result[j].Name)
	})
	return json.NewEncoder(stdout).Encode(result)
}

func writeGuestFile(target string, input io.Reader) error {
	parent := filepath.Dir(target)
	if err := os.MkdirAll(parent, 0o755); err != nil {
		return err
	}
	temporary, err := os.CreateTemp(parent, ".agentbox-write-*")
	if err != nil {
		return err
	}
	temporaryPath := temporary.Name()
	defer os.Remove(temporaryPath)
	if err := temporary.Chmod(0o600); err != nil {
		_ = temporary.Close()
		return err
	}
	if _, err := io.Copy(temporary, input); err != nil {
		_ = temporary.Close()
		return err
	}
	if err := temporary.Close(); err != nil {
		return err
	}
	return os.Rename(temporaryPath, target)
}
