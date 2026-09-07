//go:build linux
// +build linux

package libcontainer

import (
	"bufio"
	"fmt"
	"io/ioutil"
	"os"
	"path/filepath"
	"strings"

	"github.com/opencontainers/runc/libcontainer/cgroups"
	"golang.org/x/sys/unix"
)

const oomCgroupName = "memory"

type PressureLevel uint

const (
	LowPressure PressureLevel = iota
	MediumPressure
	CriticalPressure
)

func registerMemoryEvent(cgDir string, evName string, arg string) (<-chan struct{}, error) {
	evFile, err := os.Open(filepath.Join(cgDir, evName))
	if err != nil {
		return nil, err
	}
	fd, err := unix.Eventfd(0, unix.EFD_CLOEXEC)
	if err != nil {
		evFile.Close()
		return nil, err
	}

	eventfd := os.NewFile(uintptr(fd), "eventfd")

	eventControlPath := filepath.Join(cgDir, "cgroup.event_control")
	data := fmt.Sprintf("%d %d %s", eventfd.Fd(), evFile.Fd(), arg)
	if err := ioutil.WriteFile(eventControlPath, []byte(data), 0700); err != nil {
		eventfd.Close()
		evFile.Close()
		return nil, err
	}
	ch := make(chan struct{})
	go func() {
		defer func() {
			eventfd.Close()
			evFile.Close()
			close(ch)
		}()
		buf := make([]byte, 8)
		for {
			if _, err := eventfd.Read(buf); err != nil {
				return
			}
			// When a cgroup is destroyed, an event is sent to eventfd.
			// So if the control path is gone, return instead of notifying.
			if _, err := os.Lstat(eventControlPath); os.IsNotExist(err) {
				return
			}
			ch <- struct{}{}
		}
	}()
	return ch, nil
}

// notifyOnOOM returns channel on which you can expect event about OOM,
// if process died without OOM this channel will be closed.
func notifyOnOOM(paths map[string]string) (<-chan struct{}, error) {
	if cgroups.IsCgroup2UnifiedMode() {
		return notifyOnOOMV2(paths)
	}

	dir := paths[oomCgroupName]
	if dir == "" {
		return nil, fmt.Errorf("path %q missing", oomCgroupName)
	}

	return registerMemoryEvent(dir, "memory.oom_control", "")
}

// notifyOnOOMV2 uses inotify on memory.events to detect OOM kills on cgroups v2.
// On v2, the memory.events file contains key-value pairs including "oom_kill N".
// When an OOM kill occurs, the kernel modifies this file.
func notifyOnOOMV2(paths map[string]string) (<-chan struct{}, error) {
	dir := paths[oomCgroupName]
	if dir == "" {
		// On cgroups v2, paths are keyed by "" (unified) not "memory"
		dir = paths[""]
	}
	if dir == "" {
		return nil, fmt.Errorf("path for memory cgroup missing")
	}

	eventsPath := filepath.Join(dir, "memory.events")
	if _, err := os.Stat(eventsPath); err != nil {
		return nil, fmt.Errorf("cannot access memory.events: %s", err)
	}

	fd, err := unix.InotifyInit1(unix.IN_CLOEXEC)
	if err != nil {
		return nil, fmt.Errorf("inotify_init1: %s", err)
	}

	_, err = unix.InotifyAddWatch(fd, eventsPath, unix.IN_MODIFY)
	if err != nil {
		unix.Close(fd)
		return nil, fmt.Errorf("inotify_add_watch: %s", err)
	}

	ch := make(chan struct{})
	go func() {
		defer func() {
			unix.Close(fd)
			close(ch)
		}()
		buf := make([]byte, 4096)
		for {
			_, err := unix.Read(fd, buf)
			if err != nil {
				return
			}
			// Check if an OOM kill actually happened by reading memory.events
			if oomKillCount(eventsPath) > 0 {
				ch <- struct{}{}
			}
		}
	}()
	return ch, nil
}

// oomKillCount reads memory.events and returns the oom_kill counter value.
func oomKillCount(eventsPath string) uint64 {
	f, err := os.Open(eventsPath)
	if err != nil {
		return 0
	}
	defer f.Close()
	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		line := scanner.Text()
		if strings.HasPrefix(line, "oom_kill ") {
			parts := strings.Fields(line)
			if len(parts) == 2 {
				var n uint64
				fmt.Sscanf(parts[1], "%d", &n)
				return n
			}
		}
	}
	return 0
}

func notifyMemoryPressure(paths map[string]string, level PressureLevel) (<-chan struct{}, error) {
	dir := paths[oomCgroupName]
	if dir == "" {
		return nil, fmt.Errorf("path %q missing", oomCgroupName)
	}

	if level > CriticalPressure {
		return nil, fmt.Errorf("invalid pressure level %d", level)
	}

	levelStr := []string{"low", "medium", "critical"}[level]
	return registerMemoryEvent(dir, "memory.pressure_level", levelStr)
}
