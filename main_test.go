// Copyright © 2026 Zlatko Čalušić
//
// Use of this source code is governed by an MIT-style license that can be found in the LICENSE file.

package main

import (
	"bytes"
	"errors"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"reflect"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"
)

// fakeTray records what the notifier asks of the desktop.
type fakeTray struct {
	visible       bool
	tooltips      []string
	notifications []string
}

func (t *fakeTray) show()                  { t.visible = true }
func (t *fakeTray) hide()                  { t.visible = false }
func (t *fakeTray) setTooltip(text string) { t.tooltips = append(t.tooltips, text) }
func (t *fakeTray) notify(text string)     { t.notifications = append(t.notifications, text) }

// summary returns apt-get's summary line for the given number of upgrades.
func summary(upgraded int) string {
	return "Reading package lists...\n" + strconv.Itoa(upgraded) + " upgraded, 0 newly installed, 0 to remove and 0 not upgraded.\n"
}

// lockLine returns a /proc/locks line for a lock held on the file.
func lockLine(t *testing.T, file string) string {
	t.Helper()

	var st syscall.Stat_t
	if err := syscall.Stat(file, &st); err != nil {
		t.Fatal(err)
	}

	return fmt.Sprintf("1: POSIX  ADVISORY  WRITE 1234 fe:01:%d 0 EOF\n", st.Ino)
}

// touch creates an empty file in dir.
func touch(t *testing.T, dir, name string) string {
	t.Helper()

	file := filepath.Join(dir, name)
	if err := os.WriteFile(file, nil, 0o644); err != nil {
		t.Fatal(err)
	}

	return file
}

// harness is a notifier with a controllable clock and apt-get result.
type harness struct {
	*notifier
	fake    *fakeTray
	clock   time.Time
	out     string
	err     error
	simRuns int
	logs    bytes.Buffer
}

func newHarness(t *testing.T) *harness {
	t.Helper()

	dir := t.TempDir()
	h := &harness{fake: &fakeTray{}, clock: time.Date(2026, 11, 8, 12, 0, 0, 0, time.UTC)}
	h.notifier = &notifier{
		tray:      h.fake,
		locksFile: filepath.Join(dir, "locks"),
		lockFiles: []string{touch(t, dir, "lock-frontend"), touch(t, dir, "lock"), touch(t, dir, "lists-lock")},
		now:       func() time.Time { return h.clock },
		logger:    log.New(&h.logs, "", 0),
		simulate: func() (string, error) {
			h.simRuns++
			return h.out, h.err
		},
	}

	h.writeLocks(t, "")

	return h
}

// writeLocks sets the contents of the fake /proc/locks.
func (h *harness) writeLocks(t *testing.T, contents string) {
	t.Helper()

	if err := os.WriteFile(h.locksFile, []byte(contents), 0o644); err != nil {
		t.Fatal(err)
	}
}

// holdLock pretends that a package manager has locked the first lock file.
func (h *harness) holdLock(t *testing.T) {
	t.Helper()

	h.writeLocks(t, lockLine(t, h.lockFiles[0]))
}

func TestParseSummary(t *testing.T) {
	tests := []struct {
		name  string
		out   string
		count int
		ok    bool
	}{
		{"upgrades", "3 upgraded, 0 newly installed, 0 to remove and 0 not upgraded.\n", 3, true},
		{"upgrades and new", "Inst foo\n12 upgraded, 2 newly installed, 1 to remove and 0 not upgraded.\nConf foo\n", 14, true},
		{"nothing", "0 upgraded, 0 newly installed, 0 to remove and 5 not upgraded.\n", 0, true},
		{"not at line start", "foo 3 upgraded, 0 newly installed\n", 0, false},
		{"no summary", "E: Could not open lock file\n", 0, false},
		{"empty", "", 0, false},
		{"overflow", "99999999999999999999 upgraded, 0 newly installed\n", 0, false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			count, ok := parseSummary(tt.out)
			if count != tt.count || ok != tt.ok {
				t.Errorf("got %d, %v, want %d, %v", count, ok, tt.count, tt.ok)
			}
		})
	}
}

func TestIsAptRunning(t *testing.T) {
	inode := func(t *testing.T, file string) string {
		t.Helper()

		return strings.Fields(lockLine(t, file))[5]
	}

	tests := []struct {
		name  string
		locks func(t *testing.T, h *harness) string
		want  bool
	}{
		{"no locks", func(*testing.T, *harness) string { return "" }, false},
		{"frontend lock", func(t *testing.T, h *harness) string {
			t.Helper()

			return lockLine(t, h.lockFiles[0])
		}, true},
		{"dpkg lock", func(t *testing.T, h *harness) string {
			t.Helper()

			return lockLine(t, h.lockFiles[1])
		}, true},
		{"lists lock", func(t *testing.T, h *harness) string {
			t.Helper()

			return lockLine(t, h.lockFiles[2])
		}, true},
		{"among other locks", func(t *testing.T, h *harness) string {
			t.Helper()

			return lockLine(t, touch(t, t.TempDir(), "other")) + lockLine(t, h.lockFiles[1])
		}, true},
		{"unrelated lock", func(t *testing.T, _ *harness) string {
			t.Helper()

			return lockLine(t, touch(t, t.TempDir(), "other"))
		}, false},
		{"flock lock", func(t *testing.T, h *harness) string {
			t.Helper()

			_, rest, _ := strings.Cut(lockLine(t, h.lockFiles[0]), "fe:01:")
			return "2: FLOCK  ADVISORY  WRITE 99 fe:01:" + rest
		}, true},
		{"other device, as on btrfs", func(t *testing.T, h *harness) string {
			t.Helper()

			_, rest, _ := strings.Cut(lockLine(t, h.lockFiles[0]), "fe:01:")
			return "1: POSIX  ADVISORY  WRITE 1234 00:2f:" + rest
		}, true},
		{"inode is the whole number", func(t *testing.T, h *harness) string {
			t.Helper()

			return "1: POSIX  ADVISORY  WRITE 1234 fe:01:1" + inode(t, h.lockFiles[0])[6:] + " 0 EOF\n"
		}, false},
		{"lock file not created yet", func(t *testing.T, h *harness) string {
			t.Helper()

			if err := os.Remove(h.lockFiles[0]); err != nil {
				t.Fatal(err)
			}

			return lockLine(t, h.lockFiles[1])
		}, true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			h := newHarness(t)
			h.writeLocks(t, tt.locks(t, h))

			if got := h.isAptRunning(); got != tt.want {
				t.Errorf("got %v, want %v", got, tt.want)
			}
		})
	}
}

func TestIsAptRunningThrottled(t *testing.T) {
	h := newHarness(t)

	if h.isAptRunning() {
		t.Fatal("no package manager is running yet")
	}

	h.holdLock(t)

	h.clock = h.clock.Add(4 * time.Second)
	if h.isAptRunning() {
		t.Error("checked again before 5 seconds passed")
	}

	h.clock = h.clock.Add(time.Second)
	if !h.isAptRunning() {
		t.Error("didn't check again after 5 seconds")
	}
}

func TestUpdatesAvailableThrottled(t *testing.T) {
	h := newHarness(t)
	h.out = summary(2)

	if got := h.updatesAvailable(); got != 2 {
		t.Fatalf("got %d, want 2", got)
	}

	h.out = summary(7)

	h.clock = h.clock.Add(5*time.Minute - time.Second)
	if got := h.updatesAvailable(); got != 2 || h.simRuns != 1 {
		t.Errorf("checked again too early: got %d after %d runs", got, h.simRuns)
	}

	h.clock = h.clock.Add(time.Second)
	if got := h.updatesAvailable(); got != 7 || h.simRuns != 2 {
		t.Errorf("didn't check again: got %d after %d runs", got, h.simRuns)
	}
}

func TestUpdatesAvailableKeepsCountOnFailure(t *testing.T) {
	tests := []struct {
		name string
		out  string
		err  error
	}{
		{"apt-get failed", summary(9), errors.New("exit status 100")},
		{"no summary", "garbage\n", nil},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			h := newHarness(t)
			h.out = summary(2)
			h.updatesAvailable()

			h.out, h.err = tt.out, tt.err
			h.clock = h.clock.Add(5 * time.Minute)

			if got := h.updatesAvailable(); got != 2 {
				t.Errorf("got %d, want the previous count 2", got)
			}
		})
	}
}

func TestTickShowsAndNotifiesOnChange(t *testing.T) {
	h := newHarness(t)
	h.out = summary(1)

	h.tick()

	if !h.fake.visible {
		t.Error("icon should be visible")
	}

	wantTooltips := []string{"1 update available"}
	wantNotifications := []string{"There is 1 update ready to install."}

	// Same count: nothing new on the desktop.
	h.clock = h.clock.Add(5 * time.Minute)
	h.tick()

	if !reflect.DeepEqual(h.fake.tooltips, wantTooltips) {
		t.Errorf("tooltips %q, want %q", h.fake.tooltips, wantTooltips)
	}
	if !reflect.DeepEqual(h.fake.notifications, wantNotifications) {
		t.Errorf("notifications %q, want %q", h.fake.notifications, wantNotifications)
	}

	// New count.
	h.out = summary(4)
	h.clock = h.clock.Add(5 * time.Minute)
	h.tick()

	wantTooltips = append(wantTooltips, "4 updates available")
	wantNotifications = append(wantNotifications, "There are 4 updates ready to install.")

	if !reflect.DeepEqual(h.fake.tooltips, wantTooltips) {
		t.Errorf("tooltips %q, want %q", h.fake.tooltips, wantTooltips)
	}
	if !reflect.DeepEqual(h.fake.notifications, wantNotifications) {
		t.Errorf("notifications %q, want %q", h.fake.notifications, wantNotifications)
	}
}

func TestTickHidesWithoutUpdates(t *testing.T) {
	h := newHarness(t)
	h.out = summary(2)
	h.tick()

	h.out = summary(0)
	h.clock = h.clock.Add(5 * time.Minute)
	h.tick()

	if h.fake.visible {
		t.Error("icon should be hidden when there are no updates")
	}
}

func TestTickWhileAptRuns(t *testing.T) {
	h := newHarness(t)
	h.out = summary(2)
	h.tick()

	if !h.fake.visible || h.simRuns != 1 {
		t.Fatalf("visible %v after %d runs", h.fake.visible, h.simRuns)
	}

	// A package manager starts: the icon goes away, and apt-get isn't run.
	h.holdLock(t)
	h.clock = h.clock.Add(5 * time.Second)
	h.tick()

	if h.fake.visible || h.simRuns != 1 {
		t.Errorf("visible %v after %d runs, want hidden after 1", h.fake.visible, h.simRuns)
	}

	// It finishes: the count is refreshed right away, not 5 minutes after the last check.
	h.writeLocks(t, "")
	h.out = summary(0)
	h.clock = h.clock.Add(5 * time.Second)
	h.tick()

	if h.fake.visible || h.simRuns != 2 {
		t.Errorf("visible %v after %d runs, want hidden after 2", h.fake.visible, h.simRuns)
	}
}

// writeScript creates an executable shell script, standing in for apt-get.
func writeScript(t *testing.T, body string) string {
	t.Helper()

	script := filepath.Join(t.TempDir(), "apt-get")
	if err := os.WriteFile(script, []byte("#!/bin/sh\n"+body+"\n"), 0o755); err != nil {
		t.Fatal(err)
	}

	return script
}

func TestAptSimulate(t *testing.T) {
	out, err := aptSimulate(writeScript(t, `echo "$1 $2"`))
	if err != nil {
		t.Fatal(err)
	}
	if out != "-s dist-upgrade\n" {
		t.Errorf("got %q", out)
	}

	if _, err := aptSimulate(writeScript(t, "exit 100")); err == nil {
		t.Error("want an error from a failing apt-get")
	}

	if _, err := aptSimulate(filepath.Join(t.TempDir(), "missing")); err == nil {
		t.Error("want an error from a missing apt-get")
	}
}

func TestAptSimulateUntranslated(t *testing.T) {
	t.Setenv("LC_ALL", "hr_HR.UTF-8")

	script := writeScript(t, `if [ "$LC_ALL" = C ]; then echo "2 upgraded, 1 newly installed"; else echo "2 nadogradeno"; fi`)

	out, err := aptSimulate(script)
	if err != nil {
		t.Fatal(err)
	}

	if count, ok := parseSummary(out); count != 3 || !ok {
		t.Errorf("got %d, %v from %q, want 3, true", count, ok, out)
	}
}

func TestTickNotifiesAgainAfterNoUpdates(t *testing.T) {
	h := newHarness(t)

	h.out = summary(5)
	h.tick()

	h.out = summary(0)
	h.clock = h.clock.Add(5 * time.Minute)
	h.tick()

	h.out = summary(5)
	h.clock = h.clock.Add(5 * time.Minute)
	h.tick()

	if !h.fake.visible || len(h.fake.notifications) != 2 {
		t.Errorf("visible %v, notifications %q, want the icon back and one notification for each batch", h.fake.visible, h.fake.notifications)
	}
}

func TestTickDoesNotNotifyAgainAfterAptRuns(t *testing.T) {
	h := newHarness(t)

	h.out = summary(5)
	h.tick()

	h.holdLock(t)
	h.clock = h.clock.Add(5 * time.Second)
	h.tick()

	h.writeLocks(t, "")
	h.clock = h.clock.Add(5 * time.Second)
	h.tick()

	if !h.fake.visible || len(h.fake.notifications) != 1 {
		t.Errorf("visible %v, notifications %q, want the icon back and a single notification", h.fake.visible, h.fake.notifications)
	}
}

func TestFailedChecksAreLogged(t *testing.T) {
	tests := []struct {
		name string
		out  string
		err  error
		want string
	}{
		{"apt-get failed", "", errors.New("exit status 100"), "exit status 100"},
		{"no summary", "garbage\n", nil, "no summary line"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			h := newHarness(t)
			h.out, h.err = tt.out, tt.err
			h.updatesAvailable()

			if !strings.Contains(h.logs.String(), tt.want) {
				t.Errorf("log %q, want it to mention %q", h.logs.String(), tt.want)
			}

			h.updatesAvailable() // same 5 minutes: no second attempt, no second log line

			if got := strings.Count(h.logs.String(), "\n"); got != 1 {
				t.Errorf("log %q, want a single line", h.logs.String())
			}
		})
	}
}

func TestSuccessfulChecksAreNotLogged(t *testing.T) {
	h := newHarness(t)
	h.out = summary(2)
	h.tick()

	if h.logs.Len() != 0 {
		t.Errorf("log %q, want nothing", h.logs.String())
	}
}

func TestUnreadableLocksAreNotFatal(t *testing.T) {
	h := newHarness(t)
	h.locksFile = filepath.Join(t.TempDir(), "missing")

	if h.isAptRunning() {
		t.Error("nothing can be seen running")
	}

	h.isAptRunning() // same 5 seconds: no second attempt, no second log line

	if got := strings.Count(h.logs.String(), "can't check"); got != 1 {
		t.Errorf("log %q, want a single line", h.logs.String())
	}

	h.clock = h.clock.Add(5 * time.Second)
	h.isAptRunning()

	if got := strings.Count(h.logs.String(), "can't check"); got != 2 {
		t.Errorf("log %q, want one more line after 5 seconds", h.logs.String())
	}
}
