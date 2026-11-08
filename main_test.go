// Copyright © 2026 Zlatko Čalušić
//
// Use of this source code is governed by an MIT-style license that can be found in the LICENSE file.

package main

import (
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strconv"
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

// writeProc creates a fake /proc holding one process for each command name.
func writeProc(t *testing.T, comms ...string) string {
	t.Helper()

	root := t.TempDir()

	// Entries that are not processes must be skipped.
	if err := os.MkdirAll(filepath.Join(root, "sys"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "sys", "comm"), []byte("apt-get\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "99"), []byte("apt-get\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	for i, comm := range comms {
		dir := filepath.Join(root, strconv.Itoa(i+100))
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, "comm"), []byte(comm+"\n"), 0o644); err != nil {
			t.Fatal(err)
		}
	}

	return root
}

// harness is a notifier with a controllable clock and apt-get result.
type harness struct {
	*notifier
	fake    *fakeTray
	clock   time.Time
	out     string
	err     error
	simRuns int
}

func newHarness(t *testing.T, comms ...string) *harness {
	t.Helper()

	h := &harness{fake: &fakeTray{}, clock: time.Date(2026, 11, 8, 12, 0, 0, 0, time.UTC)}
	h.notifier = &notifier{
		tray:    h.fake,
		procDir: writeProc(t, comms...),
		now:     func() time.Time { return h.clock },
		simulate: func() (string, error) {
			h.simRuns++
			return h.out, h.err
		},
	}

	return h
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
	tests := []struct {
		name  string
		comms []string
		want  bool
	}{
		{"apt-get", []string{"bash", "apt-get"}, true},
		{"dselect", []string{"dselect"}, true},
		{"aptitude", []string{"aptitude", "bash"}, true},
		{"none", []string{"bash", "xfce4-panel"}, false},
		{"apt is not recognized", []string{"apt"}, false},
		{"synaptic is not recognized", []string{"synaptic"}, false},
		{"unattended-upgrades is not recognized", []string{"unattended-upgr"}, false},
		{"similar names", []string{"apt-cache", "apt-get2", "myapt-get"}, false},
		{"no processes", nil, false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			h := newHarness(t, tt.comms...)
			if got := h.isAptRunning(); got != tt.want {
				t.Errorf("got %v, want %v", got, tt.want)
			}
		})
	}
}

func TestIsAptRunningThrottled(t *testing.T) {
	h := newHarness(t, "bash")

	if h.isAptRunning() {
		t.Fatal("no package manager is running yet")
	}

	h.procDir = writeProc(t, "apt-get")

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
	h.procDir = writeProc(t, "apt-get")
	h.clock = h.clock.Add(5 * time.Second)
	h.tick()

	if h.fake.visible || h.simRuns != 1 {
		t.Errorf("visible %v after %d runs, want hidden after 1", h.fake.visible, h.simRuns)
	}

	// It finishes: the count is refreshed right away, not 5 minutes after the last check.
	h.procDir = writeProc(t, "bash")
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

func TestAptSimulateKeepsLocale(t *testing.T) {
	t.Setenv("LC_ALL", "hr_HR.UTF-8")

	out, err := aptSimulate(writeScript(t, `echo "$LC_ALL"`))
	if err != nil {
		t.Fatal(err)
	}

	if out != "hr_HR.UTF-8\n" {
		t.Errorf("got %q", out)
	}
}

func TestTickSameCountAfterNoUpdates(t *testing.T) {
	h := newHarness(t)

	h.out = summary(5)
	h.tick()

	h.out = summary(0)
	h.clock = h.clock.Add(5 * time.Minute)
	h.tick()

	h.out = summary(5)
	h.clock = h.clock.Add(5 * time.Minute)
	h.tick()

	if !h.fake.visible || len(h.fake.notifications) != 1 {
		t.Errorf("visible %v, notifications %q, want the icon back and a single notification", h.fake.visible, h.fake.notifications)
	}
}
